package app

import (
	"context"
	"os"
	"path/filepath"

	"github.com/uvwt/agentdock/internal/agentinstructions"
)

// catalogTool 是控制面板的只读投影：只含模型可见的公开定义，不含 handler、
// validator 或运行时状态，避免 UI 契约跟随内部实现漂移。
type catalogTool struct {
	Name         string                  `json:"name"`
	Title        string                  `json:"title,omitempty"`
	Description  string                  `json:"description"`
	InputSchema  map[string]any          `json:"input_schema"`
	OutputSchema map[string]any          `json:"output_schema"`
	Annotations  *catalogToolAnnotations `json:"annotations,omitempty"`
}

type catalogToolAnnotations struct {
	ReadOnlyHint    bool  `json:"read_only_hint"`
	DestructiveHint *bool `json:"destructive_hint,omitempty"`
	IdempotentHint  bool  `json:"idempotent_hint"`
	OpenWorldHint   *bool `json:"open_world_hint,omitempty"`
}

// RuntimeCatalog 为桌面控制面板汇总“本机到底提供了什么”：
// 当前配置下的全部工具定义，加上本次扫描到的 AGENTS.md 规则。
// Skill 与 Plugin 有独立只读端点，这里不重复投影，避免两份索引口径分叉。
func (r *Runtime) RuntimeCatalog(ctx context.Context) (Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	definitions := r.ToolDefinitions()
	tools := make([]catalogTool, 0, len(definitions))
	for _, definition := range definitions {
		tools = append(tools, catalogTool{
			Name:         definition.Name,
			Title:        definition.Title,
			Description:  definition.Description,
			InputSchema:  definition.InputSchema,
			OutputSchema: definition.OutputSchema,
			Annotations:  catalogAnnotations(definition.Annotations),
		})
	}

	// 全局 AGENTS.md 固定为 ~/.agentdock/AGENTS.md；workspace 规则从默认工作
	// 目录逐级继承。GUI 没有 per-request workdir，因此用默认工作目录作为扫描入口，
	// 与 workspace_context 在同一 workdir 下的结果保持一致。
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	snapshot, err := agentinstructions.Load(ctx, agentinstructions.Options{
		Home:       filepath.Join(home, ".agentdock"),
		DefaultDir: r.ws.Root(),
		Workdir:    r.ws.Root(),
	})
	if err != nil {
		return nil, err
	}

	return Result{
		"ok":             true,
		"source":         runtimeAPISource,
		"tools":          tools,
		"tool_count":     len(tools),
		"workdir":        snapshot.Workdir,
		"workspace_root": snapshot.WorkspaceRoot,
		"rules":          snapshot.Files,
		// common Skill 是低优先级共享来源（~/.agents/skills），与 managed / plugin
		// 分开投影，避免同名候选在 UI 里被静默合并。
		"common_skills": commonSkillIndexForCatalog(),
	}, nil
}

// commonSkillIndexForCatalog 失败时返回空索引而不是让整个能力目录失败：
// 工具与规则仍可浏览，共享 Skill 只是少一块。
func commonSkillIndexForCatalog() any {
	index, err := commonSkillCapabilityIndex()
	if err != nil {
		return map[string]any{"root": "", "total": 0, "truncated": false, "items": []any{}}
	}
	return index
}

func catalogAnnotations(value *ToolAnnotations) *catalogToolAnnotations {
	if value == nil {
		return nil
	}
	return &catalogToolAnnotations{
		ReadOnlyHint:    value.ReadOnlyHint,
		DestructiveHint: value.DestructiveHint,
		IdempotentHint:  value.IdempotentHint,
		OpenWorldHint:   value.OpenWorldHint,
	}
}
