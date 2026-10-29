package app

import (
	"context"
	"os"
	"path/filepath"
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
	result, err := rt.RuntimeTraces("", "", "", "", 10)
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
	result, err := rt.RuntimeTraces("", "", "", "", 10)
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
	result, err := rt.RuntimeTraces("", "", "", "error", 10)
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
	result, err := second.RuntimeTraces("", "", "", "", 10)
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
