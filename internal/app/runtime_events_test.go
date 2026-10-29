package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/observability"
	"github.com/uvwt/agentdock/internal/taskstate"
	tooltask "github.com/uvwt/agentdock/internal/tool/task"
)

// collectEvents 在超时前收集 n 个事件，避免测试因事件丢失而永久阻塞。
func collectEvents(t *testing.T, events <-chan observability.Event, n int, timeout time.Duration) []observability.Event {
	t.Helper()
	return collectKindEvents(t, events, "", n, timeout)
}

// collectKindEvents 在超时前收集 n 个指定 kind 的事件（kind 为空表示不过滤）。
func collectKindEvents(t *testing.T, events <-chan observability.Event, kind string, n int, timeout time.Duration) []observability.Event {
	t.Helper()
	collected := make([]observability.Event, 0, n)
	deadline := time.After(timeout)
	for len(collected) < n {
		select {
		case event := <-events:
			if kind == "" || event.Kind == kind {
				collected = append(collected, event)
			}
		case <-deadline:
			t.Fatalf("timed out collecting %s events: got %d, want %d", kind, len(collected), n)
		}
	}
	return collected
}

// requireJSONKeys 断言 payload 序列化后的键集合与契约完全一致。
func requireJSONKeys(t *testing.T, payload any, want ...string) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != len(want) {
		t.Fatalf("payload keys = %v, want %v (%s)", keysOf(decoded), want, encoded)
	}
	for _, key := range want {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("payload missing key %q: %s", key, encoded)
		}
	}
	return decoded
}

func keysOf(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func TestObserveToolCallPublishesCallEvent(t *testing.T) {
	rt := newRuntimeValidationTestRuntime(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := rt.events.Subscribe(ctx)

	if _, err := rt.Call(context.Background(), "agentdock_context", map[string]any{}); err != nil {
		t.Fatalf("agentdock_context: %v", err)
	}
	if _, err := rt.Call(context.Background(), "definitely-not-a-tool", nil); err == nil {
		t.Fatal("expected unknown tool error")
	}

	collected := collectEvents(t, events, 2, time.Second)
	for index, event := range collected {
		if event.Kind != observability.EventKindCall || event.At.IsZero() {
			t.Fatalf("event %d = %#v", index, event)
		}
	}
	decoded := requireJSONKeys(t, collected[0].Payload, "id", "tool", "source", "task_id", "success", "duration_ms", "error_code", "started_at")
	if decoded["tool"] != "agentdock_context" || decoded["success"] != true || decoded["error_code"] != "" {
		t.Fatalf("success call event = %#v", decoded)
	}
	if decoded["id"].(float64) < 1 || decoded["duration_ms"].(float64) < 0 || decoded["started_at"] == "" {
		t.Fatalf("call event metadata = %#v", decoded)
	}

	failed := requireJSONKeys(t, collected[1].Payload, "id", "tool", "source", "task_id", "success", "duration_ms", "error_code", "started_at")
	if failed["tool"] != "unknown" || failed["success"] != false || failed["error_code"] != "UNKNOWN_TOOL" {
		t.Fatalf("failed call event = %#v", failed)
	}

	// call 事件的 id 必须与 analytics 记录对齐。
	recent := rt.observer.RecentCalls()
	if recent[1].ID != uint64(decoded["id"].(float64)) || recent[0].ID != uint64(failed["id"].(float64)) {
		t.Fatalf("event ids %v/%v do not match analytics ids %d/%d",
			decoded["id"], failed["id"], recent[1].ID, recent[0].ID)
	}
}

func TestTaskTransitionsPublishTaskEvents(t *testing.T) {
	rt := newRuntimeValidationTestRuntime(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := rt.events.Subscribe(ctx)

	created, err := rt.taskTools.Manage(context.Background(), tooltask.ManageRequest{
		Action:               "create",
		Title:                "Ship events",
		Goal:                 "ship runtime event stream",
		CompletionConditions: []string{"tests pass"},
		Steps:                []tooltask.StepRequest{{ID: "code", Title: "Code"}, {ID: "test", Title: "Test"}},
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	taskID := created["task_id"].(string)

	// create 不在发布清单里；只允许 checkpoint/complete/delete 发布任务事件。
	select {
	case event := <-events:
		t.Fatalf("unexpected event after create: %#v", event)
	case <-time.After(50 * time.Millisecond):
	}

	if _, err := rt.RuntimeTaskAction(taskID, []byte(`{"action":"checkpoint","completed_step_ids":["code"],"current_step_id":"test","summary":"wip"}`)); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if _, err := rt.taskTools.Manage(context.Background(), tooltask.ManageRequest{
		Action: "checkpoint", TaskID: taskID, StepID: "test", Status: "completed", Summary: "done",
	}); err != nil {
		t.Fatalf("manage checkpoint: %v", err)
	}
	// 两次 checkpoint 各发一条任务事件。
	for index, event := range collectKindEvents(t, events, observability.EventKindTask, 2, time.Second) {
		decoded := requireJSONKeys(t, event.Payload, "id", "title", "status", "phase", "updated_at")
		if decoded["status"] != "active" {
			t.Fatalf("checkpoint event %d status = %v", index, decoded["status"])
		}
	}
	// final_review 不产生任务事件。
	if _, err := rt.taskTools.Manage(context.Background(), tooltask.ManageRequest{
		Action: "final_review", TaskID: taskID, Status: "pass", Summary: "verified", Verified: []string{"tests pass"},
	}); err != nil {
		t.Fatalf("final review: %v", err)
	}
	select {
	case event := <-events:
		t.Fatalf("unexpected event after final_review: %#v", event)
	case <-time.After(50 * time.Millisecond):
	}

	if _, err := rt.RuntimeTaskAction(taskID, []byte(`{"action":"complete"}`)); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := rt.RuntimeTaskDelete(taskID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	collected := collectKindEvents(t, events, observability.EventKindTask, 2, time.Second)
	wantStatuses := []string{"completed", "completed"}
	for index, event := range collected {
		if event.Kind != observability.EventKindTask {
			t.Fatalf("event %d kind = %q", index, event.Kind)
		}
		decoded := requireJSONKeys(t, event.Payload, "id", "title", "status", "phase", "updated_at")
		if decoded["id"] != taskID || decoded["title"] != "Ship events" {
			t.Fatalf("task event %d = %#v", index, decoded)
		}
		if decoded["status"] != wantStatuses[index] {
			t.Fatalf("task event %d status = %v, want %q", index, decoded["status"], wantStatuses[index])
		}
		if decoded["phase"] == "" || decoded["updated_at"] == "" {
			t.Fatalf("task event %d missing phase/updated_at: %#v", index, decoded)
		}
	}
}

func TestArtifactMutationsPublishArtifactEvents(t *testing.T) {
	rt := newRuntimeValidationTestRuntime(t)
	source := filepath.Join(rt.cfg.AgentDockDefaultDir, "report.txt")
	if err := os.WriteFile(source, []byte("report\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := rt.events.Subscribe(ctx)

	published, err := rt.Call(context.Background(), "file_publish", map[string]any{"path": source})
	if err != nil {
		t.Fatalf("file_publish: %v", err)
	}
	artifactID := published["artifact_id"].(string)

	extended, err := rt.RuntimeArtifactExtend(artifactID, 2)
	if err != nil {
		t.Fatalf("extend: %v", err)
	}
	if extended["action"] != "extend" {
		t.Fatalf("extend result = %#v", extended)
	}
	if _, err := rt.RuntimeArtifactRevoke(artifactID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	// file_publish 还会产生一条 call 事件，这里只取 artifact 事件。
	collected := collectKindEvents(t, events, observability.EventKindArtifact, 3, time.Second)
	wantActions := []string{"publish", "extend", "revoke"}
	for index, event := range collected {
		if event.Kind != observability.EventKindArtifact {
			t.Fatalf("event %d kind = %q", index, event.Kind)
		}
		decoded := requireJSONKeys(t, event.Payload, "action", "artifact_id")
		if decoded["action"] != wantActions[index] {
			t.Fatalf("artifact event %d action = %v, want %q", index, decoded["action"], wantActions[index])
		}
		if decoded["artifact_id"] != artifactID {
			t.Fatalf("artifact event %d id = %v, want %q", index, decoded["artifact_id"], artifactID)
		}
	}
}

func TestFailedArtifactMutationPublishesNoEvent(t *testing.T) {
	rt := newRuntimeValidationTestRuntime(t)
	events := rt.events.Subscribe(context.Background())
	if _, err := rt.RuntimeArtifactRevoke("missing-artifact"); err == nil {
		t.Fatal("expected not found error")
	}
	if _, err := rt.RuntimeArtifactExtend("missing-artifact", 0); err == nil {
		t.Fatal("expected validation error")
	}
	select {
	case event := <-events:
		t.Fatalf("failure published an artifact event: %#v", event)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestTaskEventPublisherIsWiredToEventBus 覆盖 app→task 服务的接线：
// SetTaskEventPublisher 注册的回调必须进入事件总线。
func TestTaskEventPublisherIsWiredToEventBus(t *testing.T) {
	rt := newRuntimeValidationTestRuntime(t)
	events := rt.events.Subscribe(context.Background())
	now := time.Now().UTC()
	rt.publishTaskEvent(taskstate.Task{ID: "tsk_x", Title: "T", Status: taskstate.StatusBlocked, Phase: taskstate.PhaseVerify, UpdatedAt: now})

	collected := collectEvents(t, events, 1, time.Second)
	decoded := requireJSONKeys(t, collected[0].Payload, "id", "title", "status", "phase", "updated_at")
	if decoded["id"] != "tsk_x" || decoded["status"] != "blocked" || decoded["phase"] != "verify" {
		t.Fatalf("task event = %#v", decoded)
	}
}
