package httpx

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/observability"
)

func TestCaptureHandlerIsPassThroughWhenDisabled(t *testing.T) {
	store := observability.NewCaptureStore(false, 10)
	called := false
	handler := captureHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}), store)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if !called {
		t.Fatal("handler must still run when capture is disabled")
	}
	if records := store.Query(observability.CaptureQuery{}); len(records) != 0 {
		t.Fatalf("disabled store captured %d records, want 0", len(records))
	}
}

func TestCaptureHandlerRecordsFullExchange(t *testing.T) {
	store := observability.NewCaptureStore(true, 10)
	handler := captureHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if string(body) != `{"jsonrpc":"2.0"}` {
			t.Fatalf("handler read body %q, want the full request body", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}), store)

	request := httptest.NewRequest(http.MethodPost, "/mcp?trace=1", strings.NewReader(`{"jsonrpc":"2.0"}`))
	request.Header.Set("Authorization", "Bearer secret-token")
	request.Header.Set("X-Tag", "a")
	request.Header.Add("X-Tag", "b")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	records := store.Query(observability.CaptureQuery{})
	if len(records) != 1 {
		t.Fatalf("captured %d records, want 1", len(records))
	}
	record := records[0]
	if record.Method != http.MethodPost || record.Path != "/mcp" || record.Query != "trace=1" {
		t.Fatalf("record request line = %s %s?%s, want POST /mcp?trace=1", record.Method, record.Path, record.Query)
	}
	if record.Status != http.StatusCreated || record.Proto != "HTTP/1.1" {
		t.Fatalf("record status/proto = %d %s, want 201 HTTP/1.1", record.Status, record.Proto)
	}
	if record.Request.Body != `{"jsonrpc":"2.0"}` || record.Request.Bytes != int64(len(`{"jsonrpc":"2.0"}`)) {
		t.Fatalf("record request body = %q (%d bytes), want full body", record.Request.Body, record.Request.Bytes)
	}
	if record.Response.Body != `{"ok":true}` || record.Response.Bytes != int64(len(`{"ok":true}`)) {
		t.Fatalf("record response body = %q (%d bytes), want full body", record.Response.Body, record.Response.Bytes)
	}
	if record.Request.Truncated || record.Response.Truncated {
		t.Fatal("small payloads must not be marked truncated")
	}
	if !headerPresent(record.Request.Headers, "Authorization", "Bearer secret-token") {
		t.Fatalf("request headers = %+v, want Authorization kept verbatim", record.Request.Headers)
	}
	if !headerPresent(record.Request.Headers, "X-Tag", "a") || !headerPresent(record.Request.Headers, "X-Tag", "b") {
		t.Fatalf("request headers = %+v, want both X-Tag values kept", record.Request.Headers)
	}
	if !headerPresent(record.Response.Headers, "Content-Type", "application/json") {
		t.Fatalf("response headers = %+v, want Content-Type kept", record.Response.Headers)
	}
}

func TestCaptureHandlerKeepsBodyWhenHandlerIgnoresIt(t *testing.T) {
	store := observability.NewCaptureStore(true, 10)
	handler := captureHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// 鉴权失败路径不读请求体：正文仍要被抓到，否则 401 排障时看不到载荷。
		w.WriteHeader(http.StatusUnauthorized)
	}), store)

	body := `{"jsonrpc":"2.0","method":"tools/list"}`
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body)))

	records := store.Query(observability.CaptureQuery{})
	if len(records) != 1 {
		t.Fatalf("captured %d records, want 1", len(records))
	}
	record := records[0]
	if record.Request.Body != body {
		t.Fatalf("request body = %q, want %q even though the handler never read it", record.Request.Body, body)
	}
	if record.Request.Bytes != int64(len(body)) {
		t.Fatalf("request bytes = %d, want %d", record.Request.Bytes, len(body))
	}
}

func TestCaptureHandlerBoundsBodyButKeepsStreamIntact(t *testing.T) {
	store := observability.NewCaptureStore(true, 10)
	payload := strings.Repeat("a", observability.CaptureBodyBytes+1024)
	handler := captureHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if len(body) != len(payload) {
			t.Fatalf("handler read %d bytes, want %d", len(body), len(payload))
		}
		_, _ = w.Write([]byte(payload))
	}), store)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(payload)))

	records := store.Query(observability.CaptureQuery{})
	if len(records) != 1 {
		t.Fatalf("captured %d records, want 1", len(records))
	}
	record := records[0]
	if record.Request.Bytes != int64(len(payload)) || record.Response.Bytes != int64(len(payload)) {
		t.Fatalf("bytes = request %d response %d, want %d", record.Request.Bytes, record.Response.Bytes, len(payload))
	}
	if !record.Request.Truncated || !record.Response.Truncated {
		t.Fatal("oversized bodies must be marked truncated")
	}
	if len(record.Request.Body) != observability.CaptureBodyBytes {
		t.Fatalf("captured %d request body bytes, want the %d byte cap", len(record.Request.Body), observability.CaptureBodyBytes)
	}
}

func TestCaptureHandlerSkipsItsOwnAPI(t *testing.T) {
	store := observability.NewCaptureStore(true, 10)
	handler := captureHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), store)

	for _, path := range []string{"/internal/runtime/network", "/internal/runtime/network/detail?id=1"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/mcp", nil))

	records := store.Query(observability.CaptureQuery{})
	// 面板自身的轮询不能淹进真实流量：只留下 /mcp 一条。
	if len(records) != 1 || records[0].Path != "/mcp" {
		t.Fatalf("captured %+v, want only the /mcp request", records)
	}
}

func TestCaptureWriterPassesFlushThrough(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := &captureResponseWriter{ResponseWriter: recorder, body: &captureBody{limit: 8}}
	writer.Flush()
	if !recorder.Flushed {
		t.Fatal("Flush must reach the underlying ResponseWriter for SSE streaming")
	}
}

func headerPresent(headers []observability.CaptureHeader, name, value string) bool {
	for _, header := range headers {
		if header.Name == name && header.Value == value {
			return true
		}
	}
	return false
}
