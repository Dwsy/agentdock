package app

import "github.com/uvwt/agentdock/internal/observability"

// RuntimeTraces 返回当前 trace 窗口内的调用记录。
// trace 关闭时也返回成功结构（mode=off），让控制面板能区分“未开启”和“查询失败”。
// query.Summary 为 true 时只返回列表摘要：100 条记录每侧 8 KiB 预览会让响应
// 达到 MB 级，控制面板滚动与解码都会被拖慢。
func (r *Runtime) RuntimeTraces(query observability.TraceQuery) (Result, error) {
	view := "full"
	if query.Summary {
		view = "summary"
	}
	if r.traces == nil {
		return Result{
			"ok": true, "source": runtimeAPISource, "mode": string(observability.TraceModeOff),
			"view": view, "records": []observability.TraceRecord{}, "count": 0, "tools": []string{},
		}, nil
	}
	records := r.traces.Query(query)
	return Result{
		"ok":      true,
		"source":  runtimeAPISource,
		"view":    view,
		"status":  r.traces.Status(),
		"records": records,
		"count":   len(records),
		"tools":   r.traces.ToolNames(),
	}, nil
}

// RuntimeTraceDetail 按 ID 返回单条完整记录，详情页只在选中记录时调用。
// 记录可能已被保留窗口淘汰，此时返回 not_found 让 UI 提示刷新列表。
func (r *Runtime) RuntimeTraceDetail(id uint64) (Result, error) {
	if id == 0 {
		return nil, toolErrorDetails("INVALID_ARGUMENT", "trace id must be a positive integer", "validation", map[string]any{"id": id})
	}
	if r.traces == nil {
		return nil, toolErrorDetails("TRACE_NOT_FOUND", "trace capture is disabled", "not_found", map[string]any{"id": id})
	}
	record, ok := r.traces.RecordByID(id)
	if !ok {
		return nil, toolErrorDetails("TRACE_NOT_FOUND", "trace record is not in the retention window", "not_found", map[string]any{"id": id})
	}
	return Result{
		"ok":     true,
		"source": runtimeAPISource,
		"record": record,
	}, nil
}

// RuntimeFileChanges 返回 trace 窗口内的文件创建/编辑历史。
// trace 关闭时返回空集合并标记 mode=off，让 UI 区分“没开历史”和“没有变更”。
func (r *Runtime) RuntimeFileChanges(query string, limit int) (Result, error) {
	if r.traces == nil {
		return Result{
			"ok": true, "source": runtimeAPISource, "mode": string(observability.TraceModeOff),
			"changes": []observability.FileChangeEntry{}, "count": 0,
		}, nil
	}
	changes := r.traces.FileChanges(query, limit)
	return Result{
		"ok":      true,
		"source":  runtimeAPISource,
		"status":  r.traces.Status(),
		"changes": changes,
		"count":   len(changes),
	}, nil
}
