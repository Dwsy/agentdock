package httpx

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/auth"
	"github.com/uvwt/agentdock/internal/envstore"
	"github.com/uvwt/agentdock/internal/manualllm"
	"github.com/uvwt/agentdock/internal/mcp"
)

type bearerRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (b *bearerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.Header.Set("Authorization", "Bearer "+b.token)
	rt := b.base
	if rt == nil {
		rt = http.DefaultTransport
	}
	return rt.RoundTrip(clone)
}

func TestManualLLMEndpointsDisabledByDefault(t *testing.T) {
	cfg := testConfig(t)
	cfg.ManualLLMEnabled = false
	runtime, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer runtime.Close()

	mux := http.NewServeMux()
	oauthStore := auth.NewOAuthStore()
	registerRuntimeAPI(mux, runtime, cfg, oauthStore)
	registerManualLLM(mux, runtime, cfg, oauthStore)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when disabled, got %d", rec.Code)
	}

	mcpRec := httptest.NewRecorder()
	mux.ServeHTTP(mcpRec, httptest.NewRequest(http.MethodPost, "/mcp/manual", strings.NewReader(`{}`)))
	if mcpRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on /mcp/manual when disabled, got %d", mcpRec.Code)
	}

	qRec := httptest.NewRecorder()
	mux.ServeHTTP(qRec, newLocalRuntimeRequest(http.MethodGet, "/internal/runtime/manual-llm", nil))
	if qRec.Code != http.StatusOK || !strings.Contains(qRec.Body.String(), `"enabled":false`) {
		t.Fatalf("unexpected runtime manual-llm response: status=%d body=%s", qRec.Code, qRec.Body.String())
	}

	// Verify live toggle activates /v1/responses and /mcp/manual immediately without restart.
	tRec := httptest.NewRecorder()
	mux.ServeHTTP(tRec, newLocalRuntimeRequest(http.MethodPost, "/internal/runtime/manual-llm/toggle", strings.NewReader(`{"enabled":true,"persist":false}`)))
	if tRec.Code != http.StatusOK || !strings.Contains(tRec.Body.String(), `"enabled":true`) {
		t.Fatalf("live toggle failed: status=%d body=%s", tRec.Code, tRec.Body.String())
	}
	liveRec := httptest.NewRecorder()
	liveReq := newLocalRuntimeRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"stream":false}`))
	mux.ServeHTTP(liveRec, liveReq)
	if liveRec.Code != http.StatusBadRequest || !strings.Contains(liveRec.Body.String(), "stream_required") {
		t.Fatalf("expected active /v1/responses handler after toggle, got %d (%s)", liveRec.Code, liveRec.Body.String())
	}

	// Verify toggling back false closes routes to 404 again.
	offRec := httptest.NewRecorder()
	mux.ServeHTTP(offRec, newLocalRuntimeRequest(http.MethodPost, "/internal/runtime/manual-llm/toggle", strings.NewReader(`{"enabled":false,"persist":false}`)))
	if offRec.Code != http.StatusOK || !strings.Contains(offRec.Body.String(), `"enabled":false`) {
		t.Fatalf("live toggle off failed: status=%d body=%s", offRec.Code, offRec.Body.String())
	}
	afterOffRec := httptest.NewRecorder()
	mux.ServeHTTP(afterOffRec, newLocalRuntimeRequest(http.MethodPost, "/mcp/manual", strings.NewReader(`{}`)))
	if afterOffRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on /mcp/manual after toggle off, got %d", afterOffRec.Code)
	}

	// Verify omitting persist defaults to persisting AGENTDOCK_MANUAL_LLM_ENABLED into agentdock.env.
	runtimeRoot := t.TempDir()
	t.Setenv("AGENTDOCK_RUNTIME_ROOT", runtimeRoot)
	persistRec := httptest.NewRecorder()
	mux.ServeHTTP(persistRec, newLocalRuntimeRequest(http.MethodPost, "/internal/runtime/manual-llm/toggle", strings.NewReader(`{"enabled":true}`)))
	if persistRec.Code != http.StatusOK || !strings.Contains(persistRec.Body.String(), `"persisted":true`) {
		t.Fatalf("expected default persisted toggle, got status=%d body=%s", persistRec.Code, persistRec.Body.String())
	}
	envVals, envErr := envstore.ParseFile(filepath.Join(runtimeRoot, "agentdock.env"))
	if envErr != nil || envVals["AGENTDOCK_MANUAL_LLM_ENABLED"] != "true" {
		t.Fatalf("expected AGENTDOCK_MANUAL_LLM_ENABLED=true persisted in agentdock.env, got %+v err=%v", envVals, envErr)
	}
	_ = os.Remove(filepath.Join(runtimeRoot, "agentdock.env"))
}

func TestManualLLMAuthLoopbackAndEndToEndSSEBridge(t *testing.T) {
	// Verify unauthenticated mode enforces direct loopback strictly on /v1/responses and /mcp/manual.
	cfgNoAuth := testConfig(t)
	cfgNoAuth.ManualLLMEnabled = true
	cfgNoAuth.AuthToken = ""
	rtNoAuth, err := app.NewRuntime(cfgNoAuth)
	if err != nil {
		t.Fatalf("new runtime no-auth: %v", err)
	}
	defer rtNoAuth.Close()

	noAuthMux := http.NewServeMux()
	registerManualLLM(noAuthMux, rtNoAuth, cfgNoAuth, auth.NewOAuthStore())
	evilReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"stream":true,"input":"hi"}`))
	evilReq.RemoteAddr = "127.0.0.1:43210"
	evilReq.Host = "127.0.0.1:8765"
	evilReq.Header.Set("Origin", "https://evil.example")
	evilRec := httptest.NewRecorder()
	noAuthMux.ServeHTTP(evilRec, evilReq)
	if evilRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-origin loopback request, got %d", evilRec.Code)
	}

	// Full authenticated HTTP test server with /mcp, /mcp/manual, /v1/responses, and /internal/runtime/*.
	cfg := testConfig(t)
	cfg.ManualLLMEnabled = true
	cfg.AuthToken = "manual-secret-token"
	runtime, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer runtime.Close()

	oauthStore := auth.NewOAuthStore()
	mux := http.NewServeMux()
	registerRuntimeAPI(mux, runtime, cfg, oauthStore)
	mux.HandleFunc("/mcp", mcpEndpointHandler(mcp.NewServer(runtime, cfg), cfg, oauthStore))
	registerManualLLM(mux, runtime, cfg, oauthStore)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// 1. Unauthorized calls rejected with 401.
	respUnauth, err := http.Post(srv.URL+"/v1/responses", "application/json", strings.NewReader(`{"stream":true,"input":"hi"}`))
	if err != nil {
		t.Fatalf("unauth post failed: %v", err)
	}
	respUnauth.Body.Close()
	if respUnauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on /v1/responses without bearer, got %d", respUnauth.StatusCode)
	}

	// 2. Connect official MCP SDK client to /mcp/manual with Bearer auth.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	authedClient := &http.Client{
		Transport: &bearerRoundTripper{token: cfg.AuthToken},
	}
	mcpClient := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "manual-bridge-test", Version: "1.0.0"}, nil)
	session, err := mcpClient.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint:   srv.URL + "/mcp/manual",
		HTTPClient: authedClient,
	}, nil)
	if err != nil {
		t.Fatalf("connect /mcp/manual: %v", err)
	}
	defer session.Close()

	toolsList, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list /mcp/manual tools: %v", err)
	}
	if len(toolsList.Tools) != 2 {
		t.Fatalf("expected 2 tools on /mcp/manual, got %d", len(toolsList.Tools))
	}

	// 3. Verify standard /mcp still exposes full AgentDock tool catalog untouched.
	mainClient := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "main-test", Version: "1.0.0"}, nil)
	mainSession, err := mainClient.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint:   srv.URL + "/mcp",
		HTTPClient: authedClient,
	}, nil)
	if err != nil {
		t.Fatalf("connect /mcp: %v", err)
	}
	defer mainSession.Close()
	mainTools, err := mainSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list /mcp tools: %v", err)
	}
	if len(mainTools.Tools) < 10 {
		t.Fatalf("expected intact full /mcp catalog, got %d tools", len(mainTools.Tools))
	}

	// 4. Send streaming POST /v1/responses request and bridge it via /mcp/manual!
	sseDone := make(chan string, 1)
	sseErr := make(chan error, 1)
	go func() {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/v1/responses", bytes.NewReader([]byte(`{"model":"manual","stream":true,"input":"Hello via HTTP"}`)))
		if reqErr != nil {
			sseErr <- reqErr
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, doErr := authedClient.Do(req)
		if doErr != nil {
			sseErr <- doErr
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(resp.Body)
			sseErr <- http.ErrAbortHandler
			t.Errorf("unexpected status %d: %s", resp.StatusCode, string(raw))
			return
		}
		var out strings.Builder
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			out.WriteString(sc.Text())
			out.WriteByte('\n')
		}
		sseDone <- out.String()
	}()

	waitRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "llm_wait",
		Arguments: map[string]any{"wait_ms": 5000},
	})
	if err != nil || waitRes.IsError {
		t.Fatalf("llm_wait failed: err=%v res=%+v", err, waitRes)
	}
	var claimed manualllm.WaitOutput
	if err := json.Unmarshal([]byte(waitRes.Content[0].(*mcpsdk.TextContent).Text), &claimed); err != nil {
		t.Fatalf("decode wait output: %v", err)
	}
	reqID := claimed.Turn.RequestID

	// Check runtime API queue endpoint reports the claimed request.
	qReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/internal/runtime/manual-llm", nil)
	qReq.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	qResp, err := http.DefaultClient.Do(qReq)
	if err != nil {
		t.Fatalf("GET /internal/runtime/manual-llm failed: %v", err)
	}
	qBody, _ := io.ReadAll(qResp.Body)
	qResp.Body.Close()
	if qResp.StatusCode != http.StatusOK || !strings.Contains(string(qBody), reqID) || !strings.Contains(string(qBody), "llm_wait") {
		t.Fatalf("unexpected queue API response: status=%d body=%s", qResp.StatusCode, string(qBody))
	}

	subRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "llm_submit",
		Arguments: map[string]any{
			"request_id": reqID,
			"events": []any{
				map[string]any{"type": "text", "text": "HTTP SSE bridged!"},
				map[string]any{"type": "done", "reason": "stop"},
			},
		},
	})
	if err != nil || subRes.IsError {
		t.Fatalf("llm_submit failed: err=%v res=%+v", err, subRes)
	}

	select {
	case err := <-sseErr:
		t.Fatalf("SSE client failed: %v", err)
	case body := <-sseDone:
		if !strings.Contains(body, "HTTP SSE bridged!") || !strings.Contains(body, "event: response.completed") || !strings.Contains(body, "data: [DONE]") {
			t.Fatalf("unexpected SSE stream body:\n%s", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for SSE completion")
	}

	// Verify POST /internal/runtime/manual-llm/cancel aborts a queued request.
	sse2Done := make(chan string, 1)
	go func() {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/v1/responses", bytes.NewReader([]byte(`{"model":"manual","stream":true,"input":"Cancel me"}`)))
		req.Header.Set("Content-Type", "application/json")
		resp, doErr := authedClient.Do(req)
		if doErr != nil {
			return
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		sse2Done <- string(raw)
	}()

	// Poll queue until request appears, then cancel via runtime API.
	var cancelTarget string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		qReq2, _ := http.NewRequest(http.MethodGet, srv.URL+"/internal/runtime/manual-llm", nil)
		qReq2.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
		resp, err := http.DefaultClient.Do(qReq2)
		if err == nil {
			var snap struct {
				Requests []manualllm.QueueEntryView `json:"requests"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&snap)
			resp.Body.Close()
			for _, entry := range snap.Requests {
				if entry.Status == "queued" {
					cancelTarget = entry.RequestID
					break
				}
			}
			if cancelTarget != "" {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if cancelTarget == "" {
		t.Fatal("queued request did not appear in /internal/runtime/manual-llm")
	}

	cReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/internal/runtime/manual-llm/cancel", strings.NewReader(`{"request_id":"`+cancelTarget+`","reason":"dashboard cancel test"}`))
	cReq.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	cReq.Header.Set("Content-Type", "application/json")
	cResp, err := http.DefaultClient.Do(cReq)
	if err != nil {
		t.Fatalf("POST cancel failed: %v", err)
	}
	cResp.Body.Close()
	if cResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on cancel, got %d", cResp.StatusCode)
	}
	body2 := <-sse2Done
	if !strings.Contains(body2, "event: response.failed") || !strings.Contains(body2, "dashboard cancel test") {
		t.Fatalf("expected response.failed on cancelled SSE, got:\n%s", body2)
	}
}
