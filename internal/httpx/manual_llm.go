package httpx

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/uvwt/agentdock/internal/auth"
	"github.com/uvwt/agentdock/internal/buildinfo"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/manualllm"
	"github.com/uvwt/agentdock/internal/runtimeapi"
)

const maxManualResponsesBodyBytes = 16 << 20 // 16 MiB handles multi-turn Codex transcripts and screenshots.

// manualModelsOwner marks every /v1/models 条目来源为人工桥接，而不是真实供应商目录。
const manualModelsOwner = "manual-bridge"

func manualLLMBroker(runtime runtimeapi.Runtime) *manualllm.Broker {
	if source, ok := runtime.(runtimeapi.ManualLLMSource); ok {
		return source.ManualLLM()
	}
	return nil
}

func registerManualLLM(mux *http.ServeMux, runtime runtimeapi.Runtime, cfg config.Config, oauthStore *auth.OAuthStore) {
	if _, ok := runtime.(runtimeapi.ManualLLMSource); !ok {
		return
	}
	mux.HandleFunc("/v1/responses", manualResponsesHandler(runtime, cfg, oauthStore))
	mux.HandleFunc("/v1/models", manualModelsHandler(runtime, cfg, oauthStore))
	mcpHandler := manualMCPEndpointHandler(runtime, cfg, oauthStore)
	mux.HandleFunc("/mcp/manual", mcpHandler)
	mux.HandleFunc("/mcp/manual/", mcpHandler)
}

func authorizeManualLLMRequest(w http.ResponseWriter, r *http.Request, cfg config.Config, oauthStore *auth.OAuthStore) bool {
	if cfg.AuthRequired() {
		authorizer := auth.Bearer{Token: cfg.AuthToken}
		staticOK := cfg.AuthToken != "" && authorizer.Authorized(r)
		oauthOK := authorizedOAuth(r, cfg, oauthStore)
		if !staticOK && !oauthOK {
			setBearerChallenge(w, cfg, r, strings.TrimSpace(r.Header.Get("Authorization")) != "")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return false
		}
		return true
	}
	if !isDirectLoopbackRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}

func manualResponsesHandler(runtime runtimeapi.Runtime, cfg config.Config, oauthStore *auth.OAuthStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		broker := manualLLMBroker(runtime)
		if broker == nil {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !authorizeManualLLMRequest(w, r, cfg, oauthStore) {
			return
		}
		// Manual responses can stream for minutes while waiting for human/LLM turns.
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})

		r.Body = http.MaxBytesReader(w, r.Body, maxManualResponsesBodyBytes)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			(&manualllm.ResponsesError{
				StatusCode: http.StatusBadRequest,
				Type:       "invalid_request_error",
				Code:       "invalid_body",
				Message:    err.Error(),
			}).WriteJSON(w)
			return
		}
		broker.ServeResponses(r.Context(), w, body)
	}
}

// manualModelsHandler 暴露实际观测到的模型名（OpenAI 兼容 /v1/models 形状）。
// 手工推理桥接没有供应商目录：data 完全由真实 /v1/responses 流量推导，无观测时返回空数组。
func manualModelsHandler(runtime runtimeapi.Runtime, cfg config.Config, oauthStore *auth.OAuthStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		broker := manualLLMBroker(runtime)
		if broker == nil {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !authorizeManualLLMRequest(w, r, cfg, oauthStore) {
			return
		}
		observed := broker.Models()
		// Models() 已保证非 nil 空切片，这里保持 OpenAI 的 "data": [] 形状。
		data := make([]map[string]any, 0, len(observed))
		for _, model := range observed {
			data = append(data, map[string]any{
				"id":        model.ID,
				"object":    "model",
				"created":   model.Created,
				"owned_by":  manualModelsOwner,
				"requests":  model.Requests,
				"last_seen": model.LastSeen,
			})
		}
		if r.Method == http.MethodHead {
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(http.StatusOK)
			return
		}
		writeJSON(w, map[string]any{
			"object": "list",
			"data":   data,
		})
	}
}

func manualMCPEndpointHandler(runtime runtimeapi.Runtime, cfg config.Config, oauthStore *auth.OAuthStore) http.HandlerFunc {
	var (
		mu          sync.Mutex
		cachedFor   *manualllm.Broker
		cachedTrans http.Handler
	)
	transportFor := func(broker *manualllm.Broker) http.Handler {
		mu.Lock()
		defer mu.Unlock()
		if cachedFor != broker || cachedTrans == nil {
			cachedFor = broker
			cachedTrans = manualllm.NewMCPHTTPHandler(broker, buildinfo.Version)
		}
		return cachedTrans
	}

	return func(w http.ResponseWriter, r *http.Request) {
		broker := manualLLMBroker(runtime)
		if broker == nil {
			http.NotFound(w, r)
			return
		}
		if !authorizeManualLLMRequest(w, r, cfg, oauthStore) {
			return
		}
		if r.Method == http.MethodPost && !prepareMCPRequestBody(w, r) {
			return
		}
		// llm_wait long-polls up to 55s per MCP request.
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		transportFor(broker).ServeHTTP(w, r)
	}
}
