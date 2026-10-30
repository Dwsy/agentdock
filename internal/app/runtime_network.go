package app

import "github.com/uvwt/agentdock/internal/observability"

// NetworkCapture 返回内存抓包环；HTTP 采集中间件与查询 API 必须共用同一实例，
// 否则面板看到的不是自己抓到的流量。关闭采集时返回 nil。
func (r *Runtime) NetworkCapture() *observability.CaptureStore {
	if r == nil {
		return nil
	}
	return r.networkCapture
}

// RuntimeNetwork 返回当前抓包窗口内的请求记录。
// 采集关闭时也返回成功结构（enabled=false），让面板区分“未开启”和“查询失败”。
// query.Summary 为 true 时只返回列表摘要：每条记录单侧正文 32 KiB，
// 一次拉 200 条完整记录会让响应到 MB 级，面板滚动与解码都会被拖慢。
func (r *Runtime) RuntimeNetwork(query observability.CaptureQuery) (Result, error) {
	view := "full"
	if query.Summary {
		view = "summary"
	}
	if !r.networkCapture.Enabled() {
		return Result{
			"ok": true, "source": runtimeAPISource, "view": view,
			"enabled": false,
			"status":  observability.CaptureStatus{},
			"records": []observability.CaptureRecord{}, "count": 0,
		}, nil
	}
	records := r.networkCapture.Query(query)
	return Result{
		"ok":      true,
		"source":  runtimeAPISource,
		"view":    view,
		"enabled": true,
		"status":  r.networkCapture.Status(),
		"records": records,
		"count":   len(records),
	}, nil
}

// RuntimeNetworkDetail 按 ID 返回单条完整记录，详情页只在选中记录时调用。
// 记录可能已被保留窗口淘汰，此时返回 not_found 让面板提示刷新列表。
func (r *Runtime) RuntimeNetworkDetail(id uint64) (Result, error) {
	if id == 0 {
		return nil, toolErrorDetails("INVALID_ARGUMENT", "network capture id must be a positive integer", "validation", map[string]any{"id": id})
	}
	if !r.networkCapture.Enabled() {
		return nil, toolErrorDetails("NETWORK_RECORD_NOT_FOUND", "network capture is disabled", "not_found", map[string]any{"id": id})
	}
	record, ok := r.networkCapture.RecordByID(id)
	if !ok {
		return nil, toolErrorDetails("NETWORK_RECORD_NOT_FOUND", "network capture record is not in the retention window", "not_found", map[string]any{"id": id})
	}
	return Result{
		"ok":     true,
		"source": runtimeAPISource,
		"record": record,
	}, nil
}

// RuntimeNetworkClear 清空内存环。只清内存：抓包从不落盘，没有需要同步清理的副本。
func (r *Runtime) RuntimeNetworkClear() (Result, error) {
	if !r.networkCapture.Enabled() {
		return Result{"ok": true, "source": runtimeAPISource, "enabled": false, "cleared": 0}, nil
	}
	cleared := r.networkCapture.Clear()
	return Result{"ok": true, "source": runtimeAPISource, "enabled": true, "cleared": cleared}, nil
}
