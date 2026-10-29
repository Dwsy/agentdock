package task

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/uvwt/agentdock/internal/taskstate"
)

func newActionTask(t *testing.T, service *Service, steps ...taskstate.TaskStepInput) string {
	t.Helper()
	if len(steps) == 0 {
		steps = []taskstate.TaskStepInput{{ID: "inspect", Title: "Inspect"}, {ID: "verify", Title: "Verify"}}
	}
	task, err := service.tasks.Create("action task", "verify task actions", []string{"done"}, steps, nil)
	if err != nil {
		t.Fatal(err)
	}
	return task.ID
}

func mustTaskAction(t *testing.T, service *Service, id, body string) Result {
	t.Helper()
	result, err := service.RuntimeTaskAction(id, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if result["ok"] != true || result["source"] != "agentdock-api" || result["task_id"] != id {
		t.Fatalf("action result = %#v", result)
	}
	return result
}

func requireActionError(t *testing.T, err error, code, category string) {
	t.Helper()
	var toolErr *ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != code || toolErr.Category != category {
		t.Fatalf("error = %#v, want code %s category %s", err, code, category)
	}
}

// TestRuntimeTaskActionCheckpointAdvancesSteps 复用 BatchCheckpoint 状态机，
// 响应形状与其它写操作一致：ok/source/action/task_id/task 摘要。
func TestRuntimeTaskActionCheckpointAdvancesSteps(t *testing.T) {
	service, _ := newTaskTestService(t)
	id := newActionTask(t, service)

	result := mustTaskAction(t, service, id, `{"action":"checkpoint","completed_step_ids":["inspect"],"current_step_id":"verify"}`)
	if result["action"] != "checkpoint" {
		t.Fatalf("result = %#v", result)
	}
	summary, ok := result["task"].(map[string]any)
	if !ok || summary["id"] != id || summary["status"] != taskstate.StatusActive {
		t.Fatalf("task summary = %#v", result["task"])
	}

	task, err := service.tasks.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if task.Steps[0].Status != taskstate.StepCompleted || task.Steps[1].Status != taskstate.StepInProgress {
		t.Fatalf("steps = %#v", task.Steps)
	}
	if task.Summary != "checkpoint" {
		t.Fatalf("summary = %q", task.Summary)
	}
}

func TestRuntimeTaskActionCheckpointRejectsIllegalTransition(t *testing.T) {
	service, _ := newTaskTestService(t)
	id := newActionTask(t, service)
	mustTaskAction(t, service, id, `{"action":"checkpoint","completed_step_ids":["inspect"],"current_step_id":"verify"}`)

	// completed → in_progress 是非法迁移：状态机拒绝后必须报 409 冲突。
	_, err := service.RuntimeTaskAction(id, []byte(`{"action":"checkpoint","current_step_id":"inspect"}`))
	requireActionError(t, err, "INVALID_STATUS", "conflict")
	if !strings.Contains(err.Error(), "cannot move step") {
		t.Fatalf("error = %v, want readable transition reason", err)
	}
}

func TestRuntimeTaskActionCompleteRequiresFinalReview(t *testing.T) {
	service, _ := newTaskTestService(t)
	id := newActionTask(t, service)

	_, err := service.RuntimeTaskAction(id, []byte(`{"action":"complete"}`))
	requireActionError(t, err, "INVALID_STATUS", "conflict")
	if !strings.Contains(err.Error(), "final_review") {
		t.Fatalf("error = %v, want final_review reason", err)
	}
}

func TestRuntimeTaskActionCompleteFinishesReviewedTask(t *testing.T) {
	service, _ := newTaskTestService(t)
	id := newActionTask(t, service, taskstate.TaskStepInput{ID: "work", Title: "Work"})
	mustTaskAction(t, service, id, `{"action":"checkpoint","completed_step_ids":["work"]}`)

	if _, err := service.tasks.FinalReview(id, taskstate.FinalReviewInput{
		Status: taskstate.FinalReviewPass, Summary: "verified", VerifiedFacts: []string{"checked"},
	}); err != nil {
		t.Fatal(err)
	}

	result := mustTaskAction(t, service, id, `{"action":"complete"}`)
	summary := result["task"].(map[string]any)
	if summary["status"] != taskstate.StatusCompleted {
		t.Fatalf("task summary = %#v", summary)
	}

	// 已完成任务不可再写：第二次 complete 必须拿明确冲突（409）。
	_, err := service.RuntimeTaskAction(id, []byte(`{"action":"complete"}`))
	requireActionError(t, err, "INVALID_STATUS", "conflict")
}

func TestRuntimeTaskActionDeleteMatchesRuntimeDelete(t *testing.T) {
	service, _ := newTaskTestService(t)
	id := newActionTask(t, service)

	result := mustTaskAction(t, service, id, `{"action":"delete"}`)
	if result["action"] != "delete" {
		t.Fatalf("result = %#v", result)
	}
	summary := result["task"].(map[string]any)
	if summary["id"] != id {
		t.Fatalf("task summary = %#v", summary)
	}
	if _, err := service.tasks.Get(id); !errors.Is(err, taskstate.ErrTaskNotFound) {
		t.Fatalf("deleted task still resolvable: %v", err)
	}

	_, err := service.RuntimeTaskAction(id, []byte(`{"action":"delete"}`))
	requireActionError(t, err, "TASK_NOT_FOUND", "not_found")
}

func TestRuntimeTaskActionRejectsInvalidRequests(t *testing.T) {
	service, _ := newTaskTestService(t)
	id := newActionTask(t, service)

	tests := []struct {
		name string
		body string
	}{
		{name: "malformed json", body: `{"action":`},
		{name: "missing action", body: `{}`},
		{name: "unknown action", body: `{"action":"block"}`},
		{name: "unknown field", body: `{"action":"delete","bogus":true}`},
		{name: "checkpoint without steps", body: `{"action":"checkpoint"}`},
		{name: "trailing json value", body: `{"action":"delete"} {}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.RuntimeTaskAction(id, []byte(test.body))
			requireActionError(t, err, "INVALID_TASK_ACTION", "validation")
		})
	}

	t.Run("oversized body", func(t *testing.T) {
		_, err := service.RuntimeTaskAction(id, []byte(`{"action":"delete","summary":"`+strings.Repeat("x", 64*1024)+`"}`))
		requireActionError(t, err, "INVALID_TASK_ACTION", "validation")
	})
	t.Run("malformed task id", func(t *testing.T) {
		_, err := service.RuntimeTaskAction("not-a-task", []byte(`{"action":"delete"}`))
		requireActionError(t, err, "TASK_STATE_ERROR", "validation")
	})
}

// TestRuntimeTaskActionConcurrentCompleteSingleWinner 验证并发写语义：
// 同一任务并发写只有一个成功，失败方拿到明确冲突（INVALID_STATUS）。
func TestRuntimeTaskActionConcurrentCompleteSingleWinner(t *testing.T) {
	service, _ := newTaskTestService(t)
	id := newActionTask(t, service, taskstate.TaskStepInput{ID: "work", Title: "Work"})
	mustTaskAction(t, service, id, `{"action":"checkpoint","completed_step_ids":["work"]}`)
	if _, err := service.tasks.FinalReview(id, taskstate.FinalReviewInput{
		Status: taskstate.FinalReviewPass, Summary: "verified", VerifiedFacts: []string{"checked"},
	}); err != nil {
		t.Fatal(err)
	}

	const writers = 4
	results := make(chan error, writers)
	var wait sync.WaitGroup
	wait.Add(writers)
	for i := 0; i < writers; i++ {
		go func() {
			defer wait.Done()
			_, err := service.RuntimeTaskAction(id, []byte(`{"action":"complete"}`))
			results <- err
		}()
	}
	wait.Wait()
	close(results)

	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
			continue
		}
		requireActionError(t, err, "INVALID_STATUS", "conflict")
	}
	if succeeded != 1 {
		t.Fatalf("succeeded writes = %d, want 1", succeeded)
	}
}

func TestRuntimeTaskActionRejectsConcurrentWrite(t *testing.T) {
	service, _ := newTaskTestService(t)
	id := newActionTask(t, service)

	release, ok := service.tryAcquireTaskWrite(id)
	if !ok {
		t.Fatal("write guard must be acquirable for an idle task")
	}
	_, err := service.RuntimeTaskAction(id, []byte(`{"action":"checkpoint","completed_step_ids":["inspect"],"current_step_id":"verify"}`))
	requireActionError(t, err, "INVALID_STATUS", "conflict")
	if !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("error = %v, want explicit conflict reason", err)
	}
	release()

	// 释放后写操作恢复正常，冲突不留下粘性状态。
	mustTaskAction(t, service, id, `{"action":"checkpoint","completed_step_ids":["inspect"],"current_step_id":"verify"}`)
}

func TestRuntimeTaskActionResultIsStableJSONContract(t *testing.T) {
	service, _ := newTaskTestService(t)
	id := newActionTask(t, service)
	result := mustTaskAction(t, service, id, `{"action":"checkpoint","completed_step_ids":["inspect"],"current_step_id":"verify"}`)

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	for _, field := range []string{`"ok"`, `"source"`, `"action"`, `"task_id"`, `"task"`, `"status"`, `"steps"`} {
		if !strings.Contains(body, field) {
			t.Fatalf("action JSON is missing %s: %s", field, body)
		}
	}
}
