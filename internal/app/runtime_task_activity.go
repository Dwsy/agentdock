package app

import (
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/observability"
)

// taskActivityCall 是任务活动视图里的单次工具调用投影。
// 只保留识别与结果元数据：参数、结果预览和错误正文不随活动聚合再次扩散。
// 字段不用 omitempty：活动契约固定下发完整键集合，UI 解码器不需要判缺席。
type taskActivityCall struct {
	ID         uint64    `json:"id"`
	Tool       string    `json:"tool"`
	Source     string    `json:"source"`
	UserAgent  string    `json:"user_agent"`
	StartedAt  time.Time `json:"started_at"`
	DurationMS float64   `json:"duration_ms"`
	Success    bool      `json:"success"`
	ErrorCode  string    `json:"error_code"`
	TraceID    string    `json:"trace_id"`
}

// taskActivityChange 是任务活动视图里的文件变更投影。
// 与 FileChangeEntry 分离：活动契约只要文件维度，不再重复调用维度字段。
type taskActivityChange struct {
	TraceID    string    `json:"trace_id"`
	Tool       string    `json:"tool"`
	StartedAt  time.Time `json:"started_at"`
	Path       string    `json:"path"`
	Action     string    `json:"action"`
	Insertions int       `json:"insertions"`
	Deletions  int       `json:"deletions"`
	Diff       string    `json:"diff"`
	Truncated  bool      `json:"truncated"`
}

// RuntimeTaskActivity 聚合单个任务在 trace 窗口内的调用与文件变更，
// 把任务↔调用↔文件三者串成一条活动线。trace 关闭时返回 mode=off 与空数组：
// UI 必须能区分“没开历史”和“这个任务没有活动”。
func (r *Runtime) RuntimeTaskActivity(id string) (Result, error) {
	taskID := strings.TrimSpace(id)
	if r.traces == nil {
		return Result{
			"ok": true, "source": runtimeAPISource, "task_id": taskID,
			"mode":         string(observability.TraceModeOff),
			"calls":        []taskActivityCall{},
			"file_changes": []taskActivityChange{},
			"call_count":   0, "change_count": 0,
		}, nil
	}
	records := r.traces.TaskRecords(taskID)
	calls := make([]taskActivityCall, 0, len(records))
	changes := make([]taskActivityChange, 0)
	for _, record := range records {
		calls = append(calls, taskActivityCall{
			ID:         record.ID,
			Tool:       record.Tool,
			Source:     record.Source,
			UserAgent:  record.UserAgent,
			StartedAt:  record.StartedAt.UTC(),
			DurationMS: record.DurationMS,
			Success:    record.Success,
			ErrorCode:  record.ErrorCode,
			TraceID:    record.TraceID,
		})
		for _, change := range record.FileChanges {
			changes = append(changes, taskActivityChange{
				TraceID:    record.TraceID,
				Tool:       record.Tool,
				StartedAt:  record.StartedAt.UTC(),
				Path:       change.Path,
				Action:     change.Action,
				Insertions: change.Insertions,
				Deletions:  change.Deletions,
				Diff:       change.Diff,
				Truncated:  change.Truncated,
			})
		}
	}
	return Result{
		"ok": true, "source": runtimeAPISource, "task_id": taskID,
		"mode":         string(r.traces.Mode()),
		"calls":        calls,
		"file_changes": changes,
		"call_count":   len(calls), "change_count": len(changes),
	}, nil
}
