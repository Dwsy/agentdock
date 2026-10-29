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

func TestRuntimeTaskActivityDisabledReportsOff(t *testing.T) {
	rt := newRuntimeValidationTestRuntime(t)
	result, err := rt.RuntimeTaskActivity("tsk_0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if result["ok"] != true || result["mode"] != string(observability.TraceModeOff) || result["task_id"] != "tsk_0123456789abcdef" {
		t.Fatalf("activity result = %#v", result)
	}
	if calls, ok := result["calls"].([]taskActivityCall); !ok || len(calls) != 0 {
		t.Fatalf("calls = %#v", result["calls"])
	}
	if changes, ok := result["file_changes"].([]taskActivityChange); !ok || len(changes) != 0 {
		t.Fatalf("file_changes = %#v", result["file_changes"])
	}
	if result["call_count"] != 0 || result["change_count"] != 0 {
		t.Fatalf("counts = %v/%v", result["call_count"], result["change_count"])
	}
}

// TestRuntimeTaskActivityAggregatesCallsAndFileChanges 验证任务↔调用↔文件的关联：
// 同一 task_id 的调用与文件变更聚到一条活动线，其他任务与无任务调用不掺入。
func TestRuntimeTaskActivityAggregatesCallsAndFileChanges(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeMemory)
	taskCtx := observability.WithTaskID(context.Background(), "tsk_0123456789abcdef")
	otherCtx := observability.WithTaskID(context.Background(), "tsk_fedcba9876543210")

	target := filepath.Join(t.TempDir(), "activity.txt")
	if err := os.WriteFile(target, []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Call(taskCtx, "file_edit", map[string]any{
		"action": "replace", "path": target, "old": "a", "new": "b",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Call(taskCtx, "agentdock_context", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Call(otherCtx, "agentdock_context", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Call(context.Background(), "agentdock_context", nil); err != nil {
		t.Fatal(err)
	}

	result, err := rt.RuntimeTaskActivity("tsk_0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if result["ok"] != true || result["source"] != "agentdock-api" || result["task_id"] != "tsk_0123456789abcdef" {
		t.Fatalf("activity result = %#v", result)
	}
	if result["mode"] != string(observability.TraceModeMemory) {
		t.Fatalf("mode = %#v", result["mode"])
	}
	calls := result["calls"].([]taskActivityCall)
	if result["call_count"] != 2 || len(calls) != 2 {
		t.Fatalf("calls = %#v", result)
	}
	for _, call := range calls {
		if call.Tool == "" || call.StartedAt.IsZero() {
			t.Fatalf("call projection = %#v", call)
		}
	}
	if calls[0].Tool != "agentdock_context" {
		t.Fatalf("calls must be newest first: %#v", calls)
	}
	changes := result["file_changes"].([]taskActivityChange)
	if result["change_count"] != 1 || len(changes) != 1 {
		t.Fatalf("file_changes = %#v", result)
	}
	change := changes[0]
	if change.Tool != "file_edit" || change.Path == "" || change.Action != "edit" || change.Diff == "" {
		t.Fatalf("change projection = %#v", change)
	}

	// JSON 契约：Swift 端解码器依赖这些字段名，改名必须先改 UI。
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	for _, field := range []string{
		`"task_id"`, `"mode"`, `"calls"`, `"file_changes"`, `"call_count"`, `"change_count"`,
		`"id"`, `"tool"`, `"source"`, `"started_at"`, `"duration_ms"`, `"success"`, `"trace_id"`,
		`"path"`, `"action"`, `"insertions"`, `"deletions"`, `"diff"`, `"truncated"`,
	} {
		if !strings.Contains(body, field) {
			t.Fatalf("activity JSON is missing %s: %s", field, body)
		}
	}
}

func TestRuntimeTaskActivityUnknownTaskReturnsEmptyWindow(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeMemory)
	if _, err := rt.Call(observability.WithTaskID(context.Background(), "tsk_0123456789abcdef"), "agentdock_context", nil); err != nil {
		t.Fatal(err)
	}
	result, err := rt.RuntimeTaskActivity("tsk_aaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if result["call_count"] != 0 || result["change_count"] != 0 {
		t.Fatalf("unknown task activity = %#v", result)
	}
}
