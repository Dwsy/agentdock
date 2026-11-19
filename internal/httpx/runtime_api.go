package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/auth"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/runtimeapi"
)

func registerRuntimeAPI(mux *http.ServeMux, runtime runtimeapi.Runtime, cfg config.Config, oauthStore *auth.OAuthStore) {
	h := runtimeAPIHandler(runtime, cfg, oauthStore)
	mux.HandleFunc("/internal/runtime/status", h)
	mux.HandleFunc("/internal/runtime/config", h)
	mux.HandleFunc("/internal/runtime/config/restart", h)
	mux.HandleFunc("/internal/runtime/analytics", h)
	mux.HandleFunc("/internal/runtime/events", h)
	mux.HandleFunc("/internal/runtime/alerts", h)
	mux.HandleFunc("/internal/runtime/overview", h)
	mux.HandleFunc("/internal/runtime/diagnostics", h)
	mux.HandleFunc("/internal/runtime/catalog", h)
	mux.HandleFunc("/internal/runtime/traces", h)
	mux.HandleFunc("/internal/runtime/traces/detail", h)
	mux.HandleFunc("/internal/runtime/traces/image", h)
	mux.HandleFunc("/internal/runtime/traces/export", h)
	mux.HandleFunc("/internal/runtime/network", h)
	mux.HandleFunc("/internal/runtime/network/detail", h)
	mux.HandleFunc("/internal/runtime/file-changes", h)
	mux.HandleFunc("/internal/runtime/artifacts", h)
	mux.HandleFunc("/internal/runtime/artifacts/", h)
	mux.HandleFunc("/internal/runtime/capabilities", h)
	mux.HandleFunc("/internal/runtime/skills", h)
	mux.HandleFunc("/internal/runtime/skills/", h)
	mux.HandleFunc("/internal/runtime/plugins", h)
	mux.HandleFunc("/internal/runtime/plugins/", h)
	mux.HandleFunc("/internal/runtime/tasks", h)
	mux.HandleFunc("/internal/runtime/tasks/", h)
	mux.HandleFunc("/internal/runtime/evolve", h)
	mux.HandleFunc(runtimeapi.ResourceLibraryPath, h)
	mux.HandleFunc("/internal/runtime/mcp", h)
	mux.HandleFunc("/internal/runtime/mcp/", h)
	mux.HandleFunc("/internal/runtime/manual-llm", h)
	mux.HandleFunc("/internal/runtime/manual-llm/", h)
	mux.HandleFunc("/internal/runtime/remote", h)
	mux.HandleFunc("/internal/runtime/remote/", h)
}

func runtimeAPIHandler(runtime runtimeapi.Runtime, cfg config.Config, oauthStore *auth.OAuthStore) http.HandlerFunc {
	authorizer := auth.Bearer{Token: cfg.AuthToken}
	authRequired := cfg.AuthRequired()
	return func(w http.ResponseWriter, r *http.Request) {
		if !runtimeapi.MethodAllowed(r.Method, r.URL.Path) {
			w.Header().Set("Allow", runtimeapi.AllowHeader(r.URL.Path))
			writeRuntimeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		staticOK := cfg.AuthToken != "" && authorizer.Authorized(r)
		oauthOK := authorizedOAuth(r, cfg, oauthStore)
		if !authRequired && !isDirectLoopbackRequest(r) {
			writeRuntimeAPIError(w, http.StatusForbidden, "LOCAL_ACCESS_REQUIRED", "runtime API requires local access or authentication")
			return
		}
		if authRequired && !staticOK && !oauthOK {
			setBearerChallenge(w, cfg, r, strings.TrimSpace(r.Header.Get("Authorization")) != "")
			writeRuntimeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
			return
		}

		body, err := runtimeRequestBody(r)
		if err != nil {
			writeRuntimeAPIError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "failed to read runtime request body")
			return
		}
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && runtimeapi.IsArtifactContentPath(r.URL.Path) {
			serveRuntimeArtifactContent(w, r, runtime)
			return
		}
		if r.Method == http.MethodGet && isRuntimeEventsPath(r.URL.Path) {
			// SSE 事件流是长连接：不能套 Dispatch 的超时上下文，也不能走 JSON 写出。
			subscriber, ok := runtime.(runtimeEventSubscriber)
			if !ok {
				writeRuntimeAPIError(w, http.StatusNotFound, "EVENTS_UNSUPPORTED", "runtime does not support the event stream")
				return
			}
			streamRuntimeEvents(w, r, subscriber)
			return
		}
		timeout := 8 * time.Second
		// install_prepare 要在同一次控制请求里下载有上限的 ZIP。沿用 Skill 下载的两分钟预算；
		// 超时、断网和摘要失败都直接返回，不转入后台补装。
		if strings.TrimSuffix(r.URL.Path, "/") == runtimeapi.ResourceLibraryPath {
			timeout = 2 * time.Minute
		} else if strings.TrimSuffix(r.URL.Path, "/") == "/internal/runtime/remote/tunnel" {
			// tunnel configure 由独立 CLI 进程执行，quick/named 首次配置可能
			// 需要下载 cloudflared 组件，沿用资源库的两分钟预算。
			timeout = 2 * time.Minute
		} else if runtimeapi.IsArtifactPublishPath(r.Method, r.URL.Path) {
			timeout = 30 * time.Second
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		result, err := runtimeapi.Dispatch(ctx, runtime, runtimeapi.Request{
			Method: r.Method,
			Path:   r.URL.Path,
			Query:  r.URL.Query(),
			Body:   body,
		})
		if err != nil {
			writeRuntimeAPIHandlerError(w, err)
			return
		}
		writeJSON(w, result)
	}
}

func runtimeRequestBody(r *http.Request) ([]byte, error) {
	cleanPath := strings.TrimSuffix(r.URL.Path, "/")
	if runtimeapi.IsArtifactPublishPath(r.Method, cleanPath) {
		return io.ReadAll(io.LimitReader(r.Body, runtimeapi.MaxArtifactPublishBodyBytes+1))
	}
	if r.Method != http.MethodPost || (cleanPath != "/internal/runtime/mcp" && cleanPath != "/internal/runtime/mcp/oauth/callback" && cleanPath != "/internal/runtime/evolve" && cleanPath != "/internal/runtime/config" && cleanPath != "/internal/runtime/manual-llm/cancel" && cleanPath != "/internal/runtime/manual-llm/toggle" && cleanPath != "/internal/runtime/remote/token" && cleanPath != "/internal/runtime/remote/nexus/pair" && cleanPath != "/internal/runtime/remote/tunnel" && cleanPath != runtimeapi.ResourceLibraryPath && !runtimeapi.IsTaskActionsPath(cleanPath) && !runtimeapi.IsArtifactExtendPath(cleanPath)) {
		return nil, nil
	}
	return io.ReadAll(io.LimitReader(r.Body, 64*1024+1))
}

func serveRuntimeArtifactContent(w http.ResponseWriter, r *http.Request, runtime runtimeapi.Runtime) {
	id, ok := runtimeapi.ArtifactContentID(r.URL.Path)
	if !ok {
		writeRuntimeAPIError(w, http.StatusNotFound, "NOT_FOUND", "runtime API route not found")
		return
	}
	contentRuntime, ok := runtime.(runtimeapi.ArtifactContentRuntime)
	if !ok {
		writeRuntimeAPIError(w, http.StatusNotFound, "ARTIFACT_CONTENT_UNSUPPORTED", "runtime does not expose artifact content")
		return
	}
	meta, file, err := contentRuntime.RuntimeArtifactOpen(id)
	if err != nil {
		writeRuntimeAPIHandlerError(w, err)
		return
	}
	defer file.Close()

	mimeType := strings.TrimSpace(meta.MimeType)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	mediaType, _, parseErr := mime.ParseMediaType(strings.ToLower(mimeType))
	if parseErr != nil {
		mediaType = strings.ToLower(mimeType)
	}
	download := r.URL.Query().Get("download") == "1"
	disposition := "attachment"
	if !download && isSafeInlineArtifactMedia(mediaType) {
		disposition = "inline"
	}

	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": meta.Filename}))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if !download && mediaType == "application/pdf" {
		// PDF 插件在 sandbox 下无法渲染；禁止外部嵌套的同时允许同源内联查看。
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'self'")
	} else {
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'")
	}
	http.ServeContent(w, r, meta.Filename, meta.CreatedAt, file)
}

func isSafeInlineArtifactMedia(mediaType string) bool {
	if strings.HasPrefix(mediaType, "audio/") || strings.HasPrefix(mediaType, "video/") {
		return true
	}
	switch mediaType {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "image/bmp", "image/x-icon", "image/vnd.microsoft.icon", "application/pdf", "text/plain":
		return true
	default:
		return false
	}
}

func writeRuntimeAPIHandlerError(w http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) {
		writeRuntimeAPIError(w, 499, "REQUEST_CANCELED", "request canceled")
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		writeRuntimeAPIError(w, http.StatusGatewayTimeout, "RUNTIME_API_TIMEOUT", "request timed out")
		return
	}
	var toolErr *app.ToolError
	if errors.As(err, &toolErr) {
		status := http.StatusInternalServerError
		switch toolErr.Category {
		case "validation":
			status = http.StatusBadRequest
		case "not_found":
			status = http.StatusNotFound
		case "conflict":
			// 非法状态迁移与并发写冲突都是“与当前状态不一致”：
			// 409 让客户端知道刷新任务状态后才能重试。
			status = http.StatusConflict
		}
		writeRuntimeAPIError(w, status, toolErr.Code, toolErr.Message)
		return
	}
	writeRuntimeAPIError(w, http.StatusInternalServerError, "RUNTIME_API_ERROR", err.Error())
}

func writeRuntimeAPIError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]any{"ok": false, "code": code, "error": message}); err != nil && !isClientDisconnectError(err) {
		slog.Warn("write runtime API error response failed", "status", status, "code", code, "error", err)
	}
}
