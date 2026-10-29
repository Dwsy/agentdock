package app

import "github.com/uvwt/agentdock/internal/observability"

// RuntimeTraces 返回当前 trace 窗口内的调用记录。
// trace 关闭时也返回成功结构（mode=off），让控制面板能区分“未开启”和“查询失败”。
func (r *Runtime) RuntimeTraces(query, tool, source, status string, limit int) (Result, error) {
	if r.traces == nil {
		return Result{
			"ok": true, "source": runtimeAPISource, "mode": string(observability.TraceModeOff),
			"records": []observability.TraceRecord{}, "count": 0, "tools": []string{},
		}, nil
	}
	records := r.traces.Query(observability.TraceQuery{
		Query: query, Tool: tool, Source: source, Status: status, Limit: limit,
	})
	return Result{
		"ok":      true,
		"source":  runtimeAPISource,
		"status":  r.traces.Status(),
		"records": records,
		"count":   len(records),
		"tools":   r.traces.ToolNames(),
	}, nil
}
