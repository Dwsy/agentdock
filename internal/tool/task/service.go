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
}

func New(configProvider ConfigProvider, tasks *taskstate.Store, evolution ...EvolutionProvider) *Service {
	service := &Service{config: configProvider, tasks: tasks}
	if len(evolution) > 0 {
		service.evolution = evolution[0]
	}
	return service
}
