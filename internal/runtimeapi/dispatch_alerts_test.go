package runtimeapi

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/uvwt/agentdock/internal/app"
)

type alertsRuntimeStub struct {
	runtimeStub
	called bool
}

func (r *alertsRuntimeStub) RuntimeAlerts() (app.Result, error) {
	r.called = true
	return app.Result{
		"ok": true, "source": "agentdock-api",
		"alerts":         []any{},
		"resolved_count": 0,
		"rules":          []any{map[string]any{"kind": "error_rate", "threshold": 0.1, "enabled": true}},
	}, nil
}

func TestDispatchAlerts(t *testing.T) {
	runtime := &alertsRuntimeStub{}
	result, err := Dispatch(context.Background(), runtime, Request{
		Method: http.MethodGet,
		Path:   "/internal/runtime/alerts",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !runtime.called || result["ok"] != true || result["source"] != app.RuntimeAPISource {
		t.Fatalf("alerts result = %#v", result)
	}
	if _, ok := result["rules"]; !ok {
		t.Fatalf("alerts result missing rules: %#v", result)
	}
}

func TestDispatchAlertsUnsupportedRuntime(t *testing.T) {
	_, err := Dispatch(context.Background(), &runtimeStub{}, Request{
		Method: http.MethodGet,
		Path:   "/internal/runtime/alerts",
	})
	var toolErr *app.ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != "ALERTS_UNSUPPORTED" || toolErr.Category != "not_found" {
		t.Fatalf("error = %#v, want ALERTS_UNSUPPORTED", err)
	}
}

func TestDispatchEventsRequiresStreamingTransport(t *testing.T) {
	// 事件流是长连接，JSON 请求/响应传输装不下：必须明确报不支持而不是假装查询成功。
	_, err := Dispatch(context.Background(), &runtimeStub{}, Request{
		Method: http.MethodGet,
		Path:   "/internal/runtime/events",
	})
	var toolErr *app.ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != "EVENTS_STREAM_UNSUPPORTED" || toolErr.Category != "not_found" {
		t.Fatalf("error = %#v, want EVENTS_STREAM_UNSUPPORTED", err)
	}
}

func TestEventsAndAlertsMethodContract(t *testing.T) {
	for _, path := range []string{"/internal/runtime/events", "/internal/runtime/alerts"} {
		if !MethodAllowed(http.MethodGet, path) {
			t.Fatalf("GET must be allowed on %s", path)
		}
		for _, method := range []string{http.MethodPost, http.MethodDelete, http.MethodPut} {
			if MethodAllowed(method, path) {
				t.Fatalf("%s must not be allowed on %s", method, path)
			}
		}
		if got := AllowHeader(path); got != "GET" {
			t.Fatalf("AllowHeader(%s) = %q, want GET", path, got)
		}
	}
}
