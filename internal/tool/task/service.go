package task

import (
	"context"
	"sync"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/taskstate"
)

type ConfigProvider func() config.Config

type EvolutionProvider interface {
	Guidance(context.Context, taskstate.Task) ([]taskstate.EvolutionContextItem, error)
	Candidates(context.Context, taskstate.Task) ([]taskstate.EvolutionContextItem, error)
	ValidateBindings(context.Context, taskstate.Task, []taskstate.EvolutionBinding) ([]taskstate.EvolutionBinding, error)
	ResolveBindings(context.Context, taskstate.Task) error
}

type Service struct {
	config    ConfigProvider
	tasks     *taskstate.Store
	evolution EvolutionProvider
	// taskWrites 是按任务 ID 的写入门闩：同一任务的并发写必须一个成功、
	// 其余立刻拿到冲突，而不是排队后把同一状态迁移各执行一遍。条目常驻，
	// 任务数量有限，事后删除反而会引入 ABA 竞态。
	taskWrites sync.Map
	// publishTaskEvent 在 checkpoint/complete/delete 成功后被调用。
	// 只能在服务投入使用前设置一次；回调不得反向调用任务写操作。
	publishTaskEvent TaskEventPublisher
}

// TaskEventPublisher 接收任务状态迁移摘要。app 层把它接到进程内事件总线，
// 服务层不依赖具体事件实现。
type TaskEventPublisher func(task taskstate.Task)

func New(configProvider ConfigProvider, tasks *taskstate.Store, evolution ...EvolutionProvider) *Service {
	service := &Service{config: configProvider, tasks: tasks}
	if len(evolution) > 0 {
		service.evolution = evolution[0]
	}
	return service
}

// SetTaskEventPublisher 注册任务事件回调。在服务投入使用前调用一次。
func (s *Service) SetTaskEventPublisher(publish TaskEventPublisher) {
	s.publishTaskEvent = publish
}

// notifyTaskEvent 在状态迁移成功后发布任务事件；无回调时零开销。
func (s *Service) notifyTaskEvent(task taskstate.Task) {
	if s == nil || s.publishTaskEvent == nil {
		return
	}
	s.publishTaskEvent(task)
}
