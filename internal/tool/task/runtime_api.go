package task

import (
	"context"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/taskstate"
)

const runtimeOverviewTaskLimit = taskstate.MaxListLimit

// RuntimeOverview 返回控制面板需要的 Task 轻量统计，避免为了几个数字把完整
// Task 列表跨 Nexus Bridge 传输。统计上限与既有 Runtime 概览保持一致。
func (s *Service) RuntimeOverview() (map[string]any, error) {
	return s.RuntimeOverviewContext(context.Background())
}

func (s *Service) RuntimeOverviewContext(ctx context.Context) (map[string]any, error) {
	counts, err := s.tasks.OverviewCountsContext(ctx, runtimeOverviewTaskLimit)
	if err != nil {
		return nil, taskToolError(err)
	}
	return map[string]any{
		"active":            counts.Active,
		"completed":         counts.Completed,
		"blocked":           counts.Blocked,
		"active_recent_24h": counts.ActiveRecent,
		// 计数不是无界的全量哈希：控制台需要知道扫描了多少条、截断发生在哪里。
		"scope": map[string]any{
			"limit":               counts.Limit,
			"matched":             counts.Matched,
			"truncated":           counts.Matched > counts.Limit,
			"recent_window_hours": int(taskstate.OverviewRecentWindow / time.Hour),
		},
	}, nil
}

func (s *Service) RuntimeTasks(status string, limit int) (Result, error) {
	return s.RuntimeTasksContext(context.Background(), status, limit)
}

func (s *Service) RuntimeTasksContext(ctx context.Context, status string, limit int) (Result, error) {
	statusFilter := taskstate.Status(strings.ToLower(strings.TrimSpace(status)))
	if statusFilter != "" && statusFilter != taskstate.StatusActive && statusFilter != taskstate.StatusBlocked && statusFilter != taskstate.StatusCompleted {
		return nil, toolErrorDetails("INVALID_STATUS", "unsupported task status filter", "validation", map[string]any{"status": statusFilter, "allowed": []string{"active", "blocked", "completed"}})
	}
	if limit <= 0 {
		limit = 50
	}
	tasks, err := s.tasks.ListContext(ctx, statusFilter, limit)
	if err != nil {
		return nil, taskToolError(err)
	}
	items := make([]map[string]any, 0, len(tasks))
	for _, task := range tasks {
		item := compactTaskListItem(task)
		item["created_at"] = task.CreatedAt
		item["event_count"] = len(task.Events)
		if task.CompletedAt != nil {
			item["completed_at"] = *task.CompletedAt
		}
		if len(task.SourceTemplates) > 0 {
			item["source_templates"] = task.SourceTemplates
		} else if task.Template != nil {
			// 旧任务仍可通过 Runtime API 查看原模板来源。
			item["source_templates"] = []taskstate.TemplateReference{{ID: task.Template.ID, Version: task.Template.Version, Hash: task.Template.Hash}}
		}
		items = append(items, item)
	}
	return Result{
		"ok":           true,
		"source":       "agentdock-api",
		"action":       "list",
		"generated_at": time.Now().UTC(),
		"limit":        limit,
		"tasks":        items,
		"count":        len(items),
	}, nil
}

func (s *Service) RuntimeTask(id string) (Result, error) {
	task, err := s.tasks.Get(strings.TrimSpace(id))
	if err != nil {
		return nil, taskToolError(err)
	}
	return Result{"ok": true, "source": "agentdock-api", "action": "get", "task": task}, nil
}

func (s *Service) RuntimeTaskDelete(id string) (Result, error) {
	task, err := s.tasks.Delete(strings.TrimSpace(id))
	if err != nil {
		return nil, taskToolError(err)
	}
	s.notifyTaskEvent(task)
	return Result{
		"ok": true, "source": "agentdock-api", "action": "delete",
		"task_id": task.ID, "deleted_task": compactTaskSummary(task),
	}, nil
}

// BlockedTasks 返回全部 blocked 状态任务，供阈值告警评估阻塞停留时长。
func (s *Service) BlockedTasks() ([]taskstate.Task, error) {
	return s.BlockedTasksContext(context.Background())
}

func (s *Service) BlockedTasksContext(ctx context.Context) ([]taskstate.Task, error) {
	return s.tasks.ListContext(ctx, taskstate.StatusBlocked, taskstate.MaxListLimit)
}
