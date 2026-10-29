package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/observability"
)

func newTraceTestRuntime(t *testing.T, mode config.TraceMode) *Runtime {
	t.Helper()
	root := t.TempDir()
	cfg := config.Config{
		AgentDockDefaultDir: root,
		AgentDockHome:       filepath.Join(root, ".agentdock"),
		TraceMode:           mode,
		TraceMaxRecords:     100,
		TraceBufferSize:     1,
		TraceFlushMS:        100,
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("close runtime: %v", err)
		}
	})
	return runtime
}

func TestRuntimeTracesDisabledByDefault(t *testing.T) {
	rt := newRuntimeValidationTestRuntime(t)
	result, err := rt.RuntimeTraces(observability.TraceQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result["mode"] != string(observability.TraceModeOff) {
		t.Fatalf("mode = %#v", result["mode"])
	}
	if records, ok := result["records"].([]observability.TraceRecord); !ok || len(records) != 0 {
		t.Fatalf("records = %#v", result["records"])
	}
}

func TestRuntimeTracesCaptureArgumentsAndResult(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeMemory)
	if _, err := rt.Call(context.Background(), "agentdock_context", nil); err != nil {
		t.Fatal(err)
	}
	result, err := rt.RuntimeTraces(observability.TraceQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	records := result["records"].([]observability.TraceRecord)
	if len(records) != 1 {
		t.Fatalf("records = %d", len(records))
	}
	record := records[0]
	if record.Tool != "agentdock_context" || !record.Success {
		t.Fatalf("record = %#v", record)
	}
	if record.Arguments != "{}" {
		t.Fatalf("arguments = %q", record.Arguments)
	}
	if record.Result == "" {
		t.Fatal("result preview is empty")
	}
	if result["count"] != 1 {
		t.Fatalf("count = %v", result["count"])
	}
	if tools := result["tools"].([]string); len(tools) != 1 || tools[0] != "agentdock_context" {
		t.Fatalf("tools = %#v", tools)
	}
}

func TestRuntimeTracesRecordFailures(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeMemory)
	if _, err := rt.Call(context.Background(), "read_file", map[string]any{}); err == nil {
		t.Fatal("expected validation error")
	}
	result, err := rt.RuntimeTraces(observability.TraceQuery{Status: "error", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	records := result["records"].([]observability.TraceRecord)
	if len(records) != 1 {
		t.Fatalf("records = %d", len(records))
	}
	if records[0].Success || records[0].ErrorCode != "INVALID_ARGUMENT" {
		t.Fatalf("record = %#v", records[0])
	}
	if records[0].Arguments != "{}" {
		t.Fatalf("arguments = %q", records[0].Arguments)
	}
}

func TestRuntimeTracesDiskModeSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		AgentDockDefaultDir: root,
		AgentDockHome:       filepath.Join(root, ".agentdock"),
		TraceMode:           config.TraceModeDisk,
		TraceMaxRecords:     100,
		TraceBufferSize:     1,
		TraceFlushMS:        100,
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	first, err := NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Call(context.Background(), "agentdock_context", nil); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	result, err := second.RuntimeTraces(observability.TraceQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	records := result["records"].([]observability.TraceRecord)
	if len(records) != 1 || records[0].Tool != "agentdock_context" {
		t.Fatalf("records after restart = %#v", records)
	}
}

func TestRuntimeFileChangesCaptureEdits(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeMemory)
	target := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(target, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Call(context.Background(), "file_edit", map[string]any{
		"action": "replace",
		"path":   target,
		"old":    "one",
		"new":    "two",
	}); err != nil {
		t.Fatal(err)
	}
	// macOS 的临时目录在 /var → /private/var 上有符号链接，文件服务返回解析后的路径。
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}

	result, err := rt.RuntimeFileChanges("", 10)
	if err != nil {
		t.Fatal(err)
	}
	changes := result["changes"].([]observability.FileChangeEntry)
	if len(changes) != 1 {
		t.Fatalf("changes = %#v", changes)
	}
	change := changes[0]
	if change.Path != resolvedTarget || change.Action != "replace" {
		t.Fatalf("change = %#v", change)
	}
	if change.Insertions != 1 || change.Deletions != 1 {
		t.Fatalf("diff stats = %#v", change)
	}
	if change.Diff == "" {
		t.Fatal("diff preview is empty")
	}
}

func TestRuntimeFileChangesDisabledReportsOff(t *testing.T) {
	rt := newRuntimeValidationTestRuntime(t)
	result, err := rt.RuntimeFileChanges("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if result["mode"] != string(observability.TraceModeOff) {
		t.Fatalf("mode = %#v", result["mode"])
	}
	if changes := result["changes"].([]observability.FileChangeEntry); len(changes) != 0 {
		t.Fatalf("changes = %#v", changes)
	}
}

// TestRuntimeTraceAPIsJSONShape 锁定 Swift 端依赖的字段名；改字段必须先改 UI 解码器。
func TestRuntimeTraceAPIsJSONShape(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeMemory)
	target := filepath.Join(t.TempDir(), "shape.txt")
	if err := os.WriteFile(target, []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Call(context.Background(), "file_edit", map[string]any{
		"action": "replace", "path": target, "old": "a", "new": "b",
	}); err != nil {
		t.Fatal(err)
	}
	uaCtx := observability.WithUserAgent(context.Background(), "ChatGPT/1.0 (+macOS)")
	uaCtx = observability.WithTaskID(uaCtx, "tsk_0123456789abcdef")
	if _, err := rt.Call(uaCtx, "agentdock_context", nil); err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct {
		result Result
		fields []string
	}{
		"catalog": {
			result: mustCatalog(t, rt),
			fields: []string{`"tools"`, `"rules"`, `"common_skills"`, `"input_schema"`, `"output_schema"`, `"workspace_root"`, `"read_only_hint"`},
		},
		"traces": {
			result: mustTraces(t, rt),
			fields: []string{`"records"`, `"started_at"`, `"duration_ms"`, `"arguments"`, `"result"`, `"tools"`, `"status"`, `"max_records"`, `"user_agent"`, `"task_id"`},
		},
		"file-changes": {
			result: mustFileChanges(t, rt),
			fields: []string{`"changes"`, `"path"`, `"action"`, `"insertions"`, `"deletions"`, `"diff"`, `"started_at"`},
		},
	}
	for name, test := range cases {
		encoded, err := json.Marshal(test.result)
		if err != nil {
			t.Fatalf("%s marshal: %v", name, err)
		}
		body := string(encoded)
		for _, field := range test.fields {
			if !strings.Contains(body, field) {
				t.Fatalf("%s is missing %s: %s", name, field, body)
			}
		}
	}
}

func mustCatalog(t *testing.T, rt *Runtime) Result {
	t.Helper()
	result, err := rt.RuntimeCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustTraces(t *testing.T, rt *Runtime) Result {
	t.Helper()
	result, err := rt.RuntimeTraces(observability.TraceQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustFileChanges(t *testing.T, rt *Runtime) Result {
	t.Helper()
	result, err := rt.RuntimeFileChanges("", 10)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRuntimeFileChangesSkipsFailedEdits(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeMemory)
	target := filepath.Join(t.TempDir(), "fail.txt")
	if err := os.WriteFile(target, []byte("content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// old 不匹配 → 工具失败，不产生 diff，也不应进入文件历史。
	if _, err := rt.Call(context.Background(), "file_edit", map[string]any{
		"action": "replace", "path": target, "old": "missing", "new": "x",
	}); err == nil {
		t.Fatal("expected edit to fail")
	}
	result, err := rt.RuntimeFileChanges("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if changes := result["changes"].([]observability.FileChangeEntry); len(changes) != 0 {
		t.Fatalf("failed edit leaked into history: %#v", changes)
	}
}

func TestRuntimeCapturesUserAgent(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeMemory)
	ctx := observability.WithUserAgent(context.Background(), "ChatGPT/1.0 (+macOS)")
	if _, err := rt.Call(ctx, "agentdock_context", nil); err != nil {
		t.Fatal(err)
	}

	records := rt.observer.Snapshot().RecentCalls
	if len(records) != 1 || records[0].UserAgent != "ChatGPT/1.0 (+macOS)" {
		t.Fatalf("analytics recent calls = %#v", records)
	}

	result, err := rt.RuntimeTraces(observability.TraceQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	traces := result["records"].([]observability.TraceRecord)
	if len(traces) != 1 || traces[0].UserAgent != "ChatGPT/1.0 (+macOS)" {
		t.Fatalf("traces = %#v", traces)
	}
}

func TestRuntimeCapturesTaskID(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeMemory)
	ctx := observability.WithTaskID(context.Background(), "tsk_0123456789abcdef")
	if _, err := rt.Call(ctx, "agentdock_context", nil); err != nil {
		t.Fatal(err)
	}

	records := rt.observer.Snapshot().RecentCalls
	if len(records) != 1 || records[0].TaskID != "tsk_0123456789abcdef" {
		t.Fatalf("analytics recent calls = %#v", records)
	}

	result, err := rt.RuntimeTraces(observability.TraceQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	traces := result["records"].([]observability.TraceRecord)
	if len(traces) != 1 || traces[0].TaskID != "tsk_0123456789abcdef" {
		t.Fatalf("traces = %#v", traces)
	}
}

func TestRuntimeWithoutTaskIDKeepsRecordClean(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeMemory)
	if _, err := rt.Call(context.Background(), "agentdock_context", nil); err != nil {
		t.Fatal(err)
	}

	records := rt.observer.Snapshot().RecentCalls
	if len(records) != 1 || records[0].TaskID != "" {
		t.Fatalf("analytics recent calls = %#v", records)
	}
	encoded, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"task_id"`) {
		t.Fatalf("empty task id leaked into JSON: %s", encoded)
	}
}
