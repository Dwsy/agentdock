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
		result := Result{
			"ok": true, "source": runtimeAPISource, "mode": string(observability.TraceModeOff),
			"view": view, "records": []observability.TraceRecord{}, "count": 0, "tools": []string{},
			"has_more": false,
		}
		if query.Summary && query.Projects {
			result["project_paths"] = map[uint64]string{}
			result["file_names"] = map[uint64][]string{}
		}
		return result, nil
	}
	withProjects := query.Summary && query.Projects
	if withProjects {
		query.Summary = false // The list projections need the redacted previews for this page.
	}
	records, hasMore := r.traces.QueryPage(query)
	result := Result{
		"ok":       true,
		"source":   runtimeAPISource,
		"view":     view,
		"status":   r.traces.Status(),
		"records":  records,
		"count":    len(records),
		"tools":    r.traces.ToolNames(),
		"has_more": hasMore,
	}
	if withProjects {
		paths := make(map[uint64]string)
		fileNames := make(map[uint64][]string)
		for i, record := range records {
			if path := r.traceProjectPath(record); path != "" {
				paths[record.ID] = path
			}
			if names := traceFileNames(record); len(names) > 0 {
				fileNames[record.ID] = names
			}
			records[i] = record.Summary()
		}
		result["project_paths"] = paths
		result["file_names"] = fileNames
	}
	return result, nil
}

// RuntimeTraceDetail 按 ID 返回单条完整记录，详情页只在选中记录时调用。
// 记录可能已被保留窗口淘汰，此时返回 not_found 让 UI 提示刷新列表。
// full=true 时额外附带旁路全文副本（单侧上限 observability.TraceFullBytes）：
// 旁路缺失（memory 模式、未截断的小 payload、已清理）时 full_available=false，
// UI 应降级为预览而不是报错。
func (r *Runtime) RuntimeTraceDetail(id uint64, full bool) (Result, error) {
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
	result := Result{
		"ok":     true,
		"source": runtimeAPISource,
		"record": record,
	}
	if full {
		fullRecord, found := r.traces.FullRecord(id)
		result["full"] = nil
		if found {
			result["full"] = fullRecord
		}
		result["full_available"] = found
	}
	return result, nil
}

// RuntimeTraceImage 按 trace ID 读取私有图片 sidecar。图片只在用户打开详情时
// 通过受保护的本机 Runtime API 传输，不进入 trace JSON 预览或公开 Artifact。
func (r *Runtime) RuntimeTraceImage(id uint64) (Result, error) {
	if id == 0 {
		return nil, toolErrorDetails("INVALID_ARGUMENT", "trace id must be a positive integer", "validation", map[string]any{"id": id})
	}
	if r.traces == nil {
		return nil, toolErrorDetails("TRACE_IMAGE_NOT_FOUND", "trace capture is disabled", "not_found", map[string]any{"id": id})
	}
	data, mimeType, ok := r.traces.ImagePreview(id)
	if !ok {
		return nil, toolErrorDetails("TRACE_IMAGE_NOT_FOUND", "trace image preview is unavailable", "not_found", map[string]any{"id": id})
	}
	return Result{"ok": true, "mime_type": mimeType, "data": data}, nil
}

// RuntimeFileChanges 返回 trace 窗口内的文件创建/编辑历史。
// trace 关闭时返回空集合并标记 mode=off，让 UI 区分“没开历史”和“没有变更”。
func (r *Runtime) RuntimeFileChanges(query string, limit int) (Result, error) {
	return r.runtimeFileChanges(query, limit, false), nil
}

// RuntimeFileChangeSummaries returns file-change metadata without diff previews.
func (r *Runtime) RuntimeFileChangeSummaries(query string) (Result, error) {
	return r.runtimeFileChanges(query, 0, true), nil
}

func (r *Runtime) runtimeFileChanges(query string, limit int, summary bool) Result {
	changes := []observability.FileChangeEntry{}
	result := Result{"ok": true, "source": runtimeAPISource}
	if r.traces == nil {
		result["mode"] = string(observability.TraceModeOff)
	} else {
		result["status"] = r.traces.Status()
	}
	if summary {
		hasMore := false
		if r.traces != nil {
			changes, hasMore = r.traces.FileChangeSummaries(query)
		}
		result["view"] = "summary"
		result["has_more"] = hasMore
	} else if r.traces != nil {
		changes = r.traces.FileChanges(query, limit)
	}
	result["changes"] = changes
	result["count"] = len(changes)
	return result
}
