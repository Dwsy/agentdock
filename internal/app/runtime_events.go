package app

import (
	"context"

	"github.com/uvwt/agentdock/internal/observability"
	"github.com/uvwt/agentdock/internal/taskstate"
)

// RuntimeEventsSubscribe 返回进程内运行时事件订阅通道，供 SSE 传输写流。
// ctx 取消即退订并关闭通道；事件发布在热路径上永不阻塞。
func (r *Runtime) RuntimeEventsSubscribe(ctx context.Context) <-chan observability.Event {
	if r == nil {
		return nil
	}
	return r.events.Subscribe(ctx)
}

// RuntimeEventsSubscriberCount 返回当前事件订阅者数量，供传输层断连断言与诊断。
func (r *Runtime) RuntimeEventsSubscriberCount() int {
	if r == nil {
		return 0
	}
	return r.events.SubscriberCount()
}

// publishTaskEvent 把任务状态迁移投影成 task 事件。由 task 服务在
// checkpoint/complete/delete 成功后回调，payload 与任务摘要契约一致。
func (r *Runtime) publishTaskEvent(task taskstate.Task) {
	if r == nil {
		return
	}
	r.events.Publish(observability.EventKindTask, observability.TaskEvent{
		ID:        task.ID,
		Title:     task.Title,
		Status:    string(task.Status),
		Phase:     string(task.Phase),
		UpdatedAt: task.UpdatedAt.UTC(),
	})
}

// publishArtifactEvent 把发布物写操作投影成 artifact 事件。
func (r *Runtime) publishArtifactEvent(action, artifactID string) {
	if r == nil {
		return
	}
	r.events.Publish(observability.EventKindArtifact, observability.ArtifactEvent{
		Action:     action,
		ArtifactID: artifactID,
	})
}
