package runtimeapi

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/observability"
)

// toolCallingRuntime 把 agentdock_context 调用塞进 RuntimeCapabilities，
// 因为生产路由里没有“dispatch 期间顺便调用工具”的路径，而 headers → trace
// 的链路只能在 dispatch 提供的上下文里证明。除这一处外全部委托真实 Runtime。
type toolCallingRuntime struct {
	*app.Runtime
}

func (r toolCallingRuntime) RuntimeCapabilities(ctx context.Context, refresh bool) (app.Result, error) {
	if _, err := r.Call(ctx, "agentdock_context", nil); err != nil {
		return nil, err
	}
	return r.Runtime.RuntimeCapabilities(ctx, refresh)
}

func TestDispatchCarriesUpstreamHeadersIntoTraces(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		AgentDockDefaultDir: root,
		AgentDockHome:       filepath.Join(root, ".agentdock"),
		TraceMode:           config.TraceModeMemory,
		TraceMaxRecords:     100,
		TraceBufferSize:     1,
		TraceFlushMS:        100,
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("close runtime: %v", err)
		}
	})

	headers := map[string]string{
		"User-Agent":   "ChatGPT/1.0 (+macOS)",
		"TraceParent":  "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"X-Request-Id": "req-42",
	}
	if _, err := Dispatch(context.Background(), toolCallingRuntime{Runtime: runtime}, Request{
		Method:  "GET",
		Path:    "/internal/runtime/capabilities",
		Headers: headers,
	}); err != nil {
		t.Fatal(err)
	}

	traces, err := runtime.RuntimeTraces(observability.TraceQuery{Limit: 10, Summary: true})
	if err != nil {
		t.Fatal(err)
	}
	records := traces["records"].([]observability.TraceRecord)
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	// 键统一小写，值保持原样：这是上游客户端的来源信息，不是本地连接头。
	want := observability.UpstreamHeaders{
		"user-agent":   "ChatGPT/1.0 (+macOS)",
		"traceparent":  "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"x-request-id": "req-42",
	}
	if !reflect.DeepEqual(records[0].UpstreamHeaders, want) {
		t.Fatalf("upstream headers = %#v, want %#v", records[0].UpstreamHeaders, want)
	}
}

func TestDispatchWithoutHeadersKeepsTracesUnchanged(t *testing.T) {
	// 老 Nexus 不带 headers：行为必须与扩展前完全一致，trace 里不出现该字段。
	rt := &runtimeStub{}
	if _, err := Dispatch(context.Background(), rt, Request{Method: "GET", Path: "/internal/runtime/capabilities"}); err != nil {
		t.Fatal(err)
	}
	if rt.upstreamHeaders != nil {
		t.Fatalf("upstream headers = %#v, want nil", rt.upstreamHeaders)
	}
}
