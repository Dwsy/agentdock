package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"

	"github.com/uvwt/agentdock/internal/taskstate"
)

// maxRuntimeTaskActionBytes 与其它 Runtime API 控制体一致：任务写操作只需要小 JSON。
const maxRuntimeTaskActionBytes = 64 << 10

// runtimeTaskActionNames 是任务写操作端点允许的动作。
// 这里只暴露控制面板需要的原子写入；block/resume/final_review 仍归 task_manage
// 工具的完整状态机，避免 Runtime API 长出第二套生命周期入口。
var runtimeTaskActionNames = []string{"checkpoint", "complete", "delete"}

type runtimeTaskActionRequest struct {
	Action           string   `json:"action"`
	CompletedStepIDs []string `json:"completed_step_ids,omitempty"`
	CurrentStepID    string   `json:"current_step_id,omitempty"`
	Summary          string   `json:"summary,omitempty"`
}

// RuntimeTaskAction 处理任务写操作。checkpoint/complete 直接复用 taskstate
// 状态机与校验，delete 与 RuntimeTaskDelete 同语义，不复制任何迁移逻辑。
func (s *Service) RuntimeTaskAction(id string, body []byte) (Result, error) {
	id = strings.TrimSpace(id)
	// ID 格式必须先于写入门闩校验：格式错误是入参问题（400），
	// 不能被误归类成状态冲突（409）。
	if err := taskstate.ValidateTaskID(id); err != nil {
		return nil, taskToolError(err)
	}
	request, err := decodeRuntimeTaskActionRequest(body)
	if err != nil {
		return nil, err
	}

	release, ok := s.tryAcquireTaskWrite(id)
	if !ok {
		return nil, taskActionConflictError(id)
	}
	defer release()

	switch request.Action {
	case "checkpoint":
		if len(request.CompletedStepIDs) == 0 && strings.TrimSpace(request.CurrentStepID) == "" {
			return nil, toolErrorDetails("INVALID_TASK_ACTION", "completed_step_ids or current_step_id is required", "validation", map[string]any{"action": request.Action})
		}
		summary := strings.TrimSpace(request.Summary)
		if summary == "" {
			// 状态机要求 checkpoint 带摘要；控制面板的批量推进没有叙述文本，
			// 用固定摘要保证任务事件流仍然可读。
			summary = "checkpoint"
		}
		task, err := s.tasks.BatchCheckpoint(id, request.CompletedStepIDs, request.CurrentStepID, summary)
		if err != nil {
			return nil, taskActionWriteError(err)
		}
		s.notifyTaskEvent(task)
		return runtimeTaskActionResult(request.Action, task), nil
	case "complete":
		task, err := s.tasks.Complete(id)
		if err != nil {
			return nil, taskActionWriteError(err)
		}
		s.notifyTaskEvent(task)
		return runtimeTaskActionResult(request.Action, task), nil
	case "delete":
		task, err := s.tasks.Delete(id)
		if err != nil {
			return nil, taskToolError(err)
		}
		s.notifyTaskEvent(task)
		return runtimeTaskActionResult(request.Action, task), nil
	default:
		// decodeRuntimeTaskActionRequest 已限定动作枚举，这里 fail fast 防止新增分支漏配。
		return nil, toolErrorDetails("INVALID_TASK_ACTION", "unsupported task action", "validation", map[string]any{"action": request.Action, "allowed": runtimeTaskActionNames})
	}
}

// tryAcquireTaskWrite 用 TryLock 把同一任务的并发写变成显式冲突：
// taskstate.Store 只保证单次迁移原子，抢不到门闩的请求必须立刻失败，
// 让调用方拿到“明确冲突”而不是静默排队。
func (s *Service) tryAcquireTaskWrite(id string) (release func(), ok bool) {
	guard, _ := s.taskWrites.LoadOrStore(id, &sync.Mutex{})
	mu := guard.(*sync.Mutex)
	if !mu.TryLock() {
		return nil, false
	}
	return mu.Unlock, true
}

// runtimeTaskActionResult 统一写操作响应形状：控制面板只消费任务摘要，
// 完整任务仍走 RuntimeTask 查询。
func runtimeTaskActionResult(action string, task taskstate.Task) Result {
	return Result{
		"ok": true, "source": "agentdock-api", "action": action,
		"task_id": task.ID, "task": compactTaskSummary(task),
	}
}

// taskActionWriteError 把状态机拒绝归为 INVALID_STATUS（HTTP 409）：
// 迁移失败说明请求与当前状态冲突，可读原因随消息返回，客户端刷新任务后可重试。
func taskActionWriteError(err error) error {
	if errors.Is(err, taskstate.ErrTaskNotFound) {
		return taskToolError(err)
	}
	return toolErrorDetails("INVALID_STATUS", err.Error(), "conflict", map[string]any{"retryable": false})
}

// taskActionConflictError 是并发写冲突：同一任务同时只有一个写操作能进入状态机。
// 错误码与非法迁移保持一致，冲突语义都属于“与当前状态不一致”。
func taskActionConflictError(id string) error {
	return toolErrorDetails("INVALID_STATUS", "task write conflict: another write for this task is in progress", "conflict", map[string]any{"task_id": id, "retryable": true})
}

func decodeRuntimeTaskActionRequest(body []byte) (runtimeTaskActionRequest, error) {
	var request runtimeTaskActionRequest
	if len(body) > maxRuntimeTaskActionBytes {
		return request, runtimeTaskActionError("task action body is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, runtimeTaskActionError("invalid task action body")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return request, runtimeTaskActionError("request body must contain exactly one JSON value")
	}
	request.Action = strings.ToLower(strings.TrimSpace(request.Action))
	request.CurrentStepID = strings.TrimSpace(request.CurrentStepID)
	request.Summary = strings.TrimSpace(request.Summary)
	if request.Action == "" {
		return request, toolErrorDetails("INVALID_TASK_ACTION", "task action is required", "validation", map[string]any{"allowed": runtimeTaskActionNames})
	}
	for _, allowed := range runtimeTaskActionNames {
		if request.Action == allowed {
			return request, nil
		}
	}
	return request, toolErrorDetails("INVALID_TASK_ACTION", "unsupported task action", "validation", map[string]any{"action": request.Action, "allowed": runtimeTaskActionNames})
}

func runtimeTaskActionError(message string) error {
	return toolErrorDetails("INVALID_TASK_ACTION", message, "validation", map[string]any{"retryable": false})
}
