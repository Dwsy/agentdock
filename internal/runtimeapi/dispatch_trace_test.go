package runtimeapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/observability"
)

// traceDetailRuntimeStub 在 runtimeStub 之上补详情能力，用来验证路由与参数；
// 裸 runtimeStub 本身就是“旧实现”，必须明确报不支持而不是返回空记录。
type traceDetailRuntimeStub struct {
	runtimeStub
	detailID uint64
}

func (r *traceDetailRuntimeStub) RuntimeTraceDetail(id uint64) (app.Result, error) {
	r.detailID = id
	return app.Result{"ok": true, "record": map[string]any{"id": id}}, nil
}

func TestDispatchTraceViewModes(t *testing.T) {
	tests := []struct {
		name    string
		view    string
		summary bool
		wantErr string
	}{
		{name: "default is full", view: "", summary: false},
		{name: "explicit full", view: "full", summary: false},
		{name: "summary", view: "summary", summary: true},
		{name: "unknown view", view: "raw", wantErr: "INVALID_ARGUMENT"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &runtimeStub{}
			query := url.Values{}
			if test.view != "" {
				query.Set("view", test.view)
			}
			_, err := Dispatch(context.Background(), stub, Request{
				Method: http.MethodGet,
				Path:   "/internal/runtime/traces",
				Query:  query,
			})
			if test.wantErr != "" {
				var toolErr *app.ToolError
				if !errors.As(err, &toolErr) || toolErr.Code != test.wantErr || toolErr.Category != "validation" {
					t.Fatalf("error = %#v, want %s", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if stub.traceQuery.Summary != test.summary {
				t.Fatalf("summary = %v, want %v", stub.traceQuery.Summary, test.summary)
			}
		})
	}
}

func TestDispatchTraceDetail(t *testing.T) {
	t.Run("passes id", func(t *testing.T) {
		stub := &traceDetailRuntimeStub{}
		result, err := Dispatch(context.Background(), stub, Request{
			Method: http.MethodGet,
			Path:   "/internal/runtime/traces/detail",
			Query:  url.Values{"id": {"42"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if stub.detailID != 42 || result["record"] == nil {
			t.Fatalf("detail = %#v id = %d", result, stub.detailID)
		}
	})

	for _, query := range []url.Values{{}, {"id": {"0"}}, {"id": {"abc"}}, {"id": {"-3"}}} {
		t.Run("rejects "+query.Encode(), func(t *testing.T) {
			_, err := Dispatch(context.Background(), &traceDetailRuntimeStub{}, Request{
				Method: http.MethodGet,
				Path:   "/internal/runtime/traces/detail",
				Query:  query,
			})
			var toolErr *app.ToolError
			if !errors.As(err, &toolErr) || toolErr.Code != "INVALID_ARGUMENT" || toolErr.Category != "validation" {
				t.Fatalf("error = %#v", err)
			}
		})
	}

	t.Run("fails closed without capability", func(t *testing.T) {
		_, err := Dispatch(context.Background(), &runtimeStub{}, Request{
			Method: http.MethodGet,
			Path:   "/internal/runtime/traces/detail",
			Query:  url.Values{"id": {"1"}},
		})
		var toolErr *app.ToolError
		if !errors.As(err, &toolErr) || toolErr.Code != "TRACES_DETAIL_UNSUPPORTED" || toolErr.Category != "not_found" {
			t.Fatalf("error = %#v", err)
		}
	})

	t.Run("read only route", func(t *testing.T) {
		if !MethodAllowed(http.MethodGet, "/internal/runtime/traces/detail") {
			t.Fatal("GET must be allowed on trace detail")
		}
		if MethodAllowed(http.MethodPost, "/internal/runtime/traces/detail") || MethodAllowed(http.MethodDelete, "/internal/runtime/traces/detail") {
			t.Fatal("trace detail must be read-only")
		}
		if got := AllowHeader("/internal/runtime/traces/detail"); got != "GET" {
			t.Fatalf("AllowHeader = %q", got)
		}
	})
}

func TestTraceQuerySummaryIsIndependentOfFilters(t *testing.T) {
	// 摘要视图沿用同一套过滤语义，只是不再下发正文；这里直接验证结构字段
	// 不会在 Dispatch 层被丢掉。
	stub := &runtimeStub{}
	_, err := Dispatch(context.Background(), stub, Request{
		Method: http.MethodGet,
		Path:   "/internal/runtime/traces",
		Query:  url.Values{"view": {"summary"}, "tool": {"exec_command"}, "limit": {"10"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !stub.traceQuery.Summary || stub.traceQuery.Tool != "exec_command" || stub.traceQuery.Limit != 10 {
		t.Fatalf("query = %#v", stub.traceQuery)
	}
	if got := (observability.TraceQuery{}).Summary; got {
		t.Fatal("zero TraceQuery must default to full view")
	}
}
