package runtimeapi

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/uvwt/agentdock/internal/app"
)

// manageRuntimeStub 在 runtimeStub 之上补发布物写操作与 trace 导出/清空能力，
// 用于验证路由、参数与响应包装；不支持的场景直接用裸 runtimeStub。
type manageRuntimeStub struct {
	runtimeStub
	revokedID    string
	extendID     string
	extendHours  int
	exportFormat string
	exportQuery  string
	exportTool   string
	exportSource string
	exportStatus string
	exportLimit  int
	exportCalled bool
	clearCalled  bool
}

func (r *manageRuntimeStub) RuntimeArtifactRevoke(id string) (app.Result, error) {
	r.revokedID = id
	return app.Result{"ok": true, "source": app.RuntimeAPISource, "action": "revoke", "artifact_id": id}, nil
}

func (r *manageRuntimeStub) RuntimeArtifactExtend(id string, hours int) (app.Result, error) {
	r.extendID, r.extendHours = id, hours
	return app.Result{"ok": true, "source": app.RuntimeAPISource, "action": "extend", "artifact": map[string]any{"artifact_id": id, "hours": hours}}, nil
}

func (r *manageRuntimeStub) RuntimeTracesExport(format, query, tool, source, status string, limit int) ([]byte, string, string, error) {
	r.exportCalled = true
	r.exportFormat, r.exportQuery, r.exportTool, r.exportSource, r.exportStatus, r.exportLimit = format, query, tool, source, status, limit
	if format == "csv" {
		return []byte("id,started_at\n"), "text/csv", "traces.csv", nil
	}
	return []byte("[]"), "application/json", "traces.json", nil
}

func (r *manageRuntimeStub) RuntimeTracesClear() (app.Result, error) {
	r.clearCalled = true
	return app.Result{"ok": true, "source": app.RuntimeAPISource, "action": "clear", "cleared": 3}, nil
}

func TestMethodContractForArtifactAndTraceMaintenance(t *testing.T) {
	tests := []struct {
		method string
		path   string
		allow  string
		ok     bool
	}{
		{"DELETE", "/internal/runtime/artifacts/0123456789abcdef0123456789abcdef", "DELETE", true},
		{"GET", "/internal/runtime/artifacts/0123456789abcdef0123456789abcdef", "DELETE", false},
		{"POST", "/internal/runtime/artifacts/0123456789abcdef0123456789abcdef", "DELETE", false},
		{"POST", "/internal/runtime/artifacts/0123456789abcdef0123456789abcdef/extend", "POST", true},
		{"GET", "/internal/runtime/artifacts/0123456789abcdef0123456789abcdef/extend", "POST", false},
		{"DELETE", "/internal/runtime/artifacts/0123456789abcdef0123456789abcdef/extend", "POST", false},
		{"GET", "/internal/runtime/artifacts", "GET", true},
		{"DELETE", "/internal/runtime/traces", "GET, DELETE", true},
		{"POST", "/internal/runtime/traces", "GET, DELETE", false},
		{"GET", "/internal/runtime/traces/export", "GET", true},
		{"POST", "/internal/runtime/traces/export", "GET", false},
		{"DELETE", "/internal/runtime/traces/export", "GET", false},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			if got := MethodAllowed(test.method, test.path); got != test.ok {
				t.Fatalf("MethodAllowed() = %v, want %v", got, test.ok)
			}
			if got := AllowHeader(test.path); got != test.allow {
				t.Fatalf("AllowHeader() = %q, want %q", got, test.allow)
			}
		})
	}
}

func TestDispatchArtifactRevokeAndExtend(t *testing.T) {
	const artifactID = "0123456789abcdef0123456789abcdef"

	t.Run("revoke routes to runtime", func(t *testing.T) {
		stub := &manageRuntimeStub{}
		result, err := Dispatch(context.Background(), stub, Request{
			Method: "DELETE",
			Path:   "/internal/runtime/artifacts/" + artifactID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if stub.revokedID != artifactID {
			t.Fatalf("revoked = %q", stub.revokedID)
		}
		if result["action"] != "revoke" || result["artifact_id"] != artifactID || result["ok"] != true {
			t.Fatalf("result = %#v", result)
		}
	})

	t.Run("extend decodes hours", func(t *testing.T) {
		stub := &manageRuntimeStub{}
		result, err := Dispatch(context.Background(), stub, Request{
			Method: "POST",
			Path:   "/internal/runtime/artifacts/" + artifactID + "/extend",
			Body:   []byte(`{"hours":48}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		if stub.extendID != artifactID || stub.extendHours != 48 {
			t.Fatalf("extend routing = %q %d", stub.extendID, stub.extendHours)
		}
		if result["action"] != "extend" {
			t.Fatalf("result = %#v", result)
		}
	})

	for _, body := range []string{`{}`, `{"hours":"48"}`, `not-json`, `{"hours":1,"extra":true}`, `{"hours":1}{"hours":2}`} {
		t.Run("rejects body "+body, func(t *testing.T) {
			_, err := Dispatch(context.Background(), &manageRuntimeStub{}, Request{
				Method: "POST",
				Path:   "/internal/runtime/artifacts/" + artifactID + "/extend",
				Body:   []byte(body),
			})
			var toolErr *app.ToolError
			if !errors.As(err, &toolErr) || toolErr.Code != "INVALID_ARGUMENT" || toolErr.Category != "validation" {
				t.Fatalf("body %q error = %#v", body, err)
			}
		})
	}

	t.Run("unknown sub route is not found", func(t *testing.T) {
		_, err := Dispatch(context.Background(), &manageRuntimeStub{}, Request{
			Method: "GET",
			Path:   "/internal/runtime/artifacts/" + artifactID + "/files",
		})
		var toolErr *app.ToolError
		if !errors.As(err, &toolErr) || toolErr.Code != "NOT_FOUND" {
			t.Fatalf("error = %#v", err)
		}
	})

	t.Run("fails closed without capability", func(t *testing.T) {
		_, err := Dispatch(context.Background(), &runtimeStub{}, Request{
			Method: "DELETE",
			Path:   "/internal/runtime/artifacts/" + artifactID,
		})
		var toolErr *app.ToolError
		if !errors.As(err, &toolErr) || toolErr.Code != "ARTIFACT_MANAGE_UNSUPPORTED" || toolErr.Category != "not_found" {
			t.Fatalf("error = %#v", err)
		}
	})
}

func TestDispatchTracesExportAndClear(t *testing.T) {
	t.Run("export passes filters", func(t *testing.T) {
		stub := &manageRuntimeStub{}
		result, err := Dispatch(context.Background(), stub, Request{
			Method: "GET",
			Path:   "/internal/runtime/traces/export",
			Query: url.Values{
				"format": {"csv"}, "q": {"file"}, "tool": {"read_file"},
				"source": {"mcp"}, "status": {"error"}, "limit": {"25"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if stub.exportFormat != "csv" || stub.exportQuery != "file" || stub.exportTool != "read_file" || stub.exportSource != "mcp" || stub.exportStatus != "error" || stub.exportLimit != 25 {
			t.Fatalf("export args = %#v", stub)
		}
		if result["format"] != "csv" || result["content_type"] != "text/csv" || result["filename"] != "traces.csv" || result["body"] != "id,started_at\n" {
			t.Fatalf("export result = %#v", result)
		}
		if result["ok"] != true || result["source"] != app.RuntimeAPISource || result["action"] != "export" {
			t.Fatalf("export envelope = %#v", result)
		}
	})

	t.Run("export defaults to json", func(t *testing.T) {
		stub := &manageRuntimeStub{}
		result, err := Dispatch(context.Background(), stub, Request{
			Method: "GET",
			Path:   "/internal/runtime/traces/export",
		})
		if err != nil {
			t.Fatal(err)
		}
		if stub.exportFormat != "json" || stub.exportLimit != 0 || result["format"] != "json" {
			t.Fatalf("default export = %#v", result)
		}
	})

	for _, query := range []url.Values{
		{"format": {"xml"}},
		{"format": {"JSONL"}},
		{"limit": {"-1"}},
		{"limit": {"many"}},
	} {
		t.Run("rejects query "+query.Encode(), func(t *testing.T) {
			_, err := Dispatch(context.Background(), &manageRuntimeStub{}, Request{
				Method: "GET",
				Path:   "/internal/runtime/traces/export",
				Query:  query,
			})
			var toolErr *app.ToolError
			if !errors.As(err, &toolErr) || toolErr.Code != "INVALID_ARGUMENT" {
				t.Fatalf("query %v error = %#v", query, err)
			}
		})
	}

	t.Run("clear routes to runtime", func(t *testing.T) {
		stub := &manageRuntimeStub{}
		result, err := Dispatch(context.Background(), stub, Request{
			Method: "DELETE",
			Path:   "/internal/runtime/traces",
		})
		if err != nil {
			t.Fatal(err)
		}
		if !stub.clearCalled {
			t.Fatal("clear was not called")
		}
		if result["action"] != "clear" || result["cleared"] != 3 || result["ok"] != true {
			t.Fatalf("clear result = %#v", result)
		}
	})

	t.Run("fails closed without capability", func(t *testing.T) {
		var toolErr *app.ToolError
		_, err := Dispatch(context.Background(), &runtimeStub{}, Request{
			Method: "GET",
			Path:   "/internal/runtime/traces/export",
		})
		if !errors.As(err, &toolErr) || toolErr.Code != "TRACES_EXPORT_UNSUPPORTED" || toolErr.Category != "not_found" {
			t.Fatalf("export error = %#v", err)
		}
		_, err = Dispatch(context.Background(), &runtimeStub{}, Request{
			Method: "DELETE",
			Path:   "/internal/runtime/traces",
		})
		if !errors.As(err, &toolErr) || toolErr.Code != "TRACES_CLEAR_UNSUPPORTED" || toolErr.Category != "not_found" {
			t.Fatalf("clear error = %#v", err)
		}
	})
}
