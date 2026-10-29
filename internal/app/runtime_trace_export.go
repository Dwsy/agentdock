package app

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/observability"
)

const (
	// traceExportDefaultLimit 是导出的默认条数，覆盖完整保留窗口的常规规模。
	traceExportDefaultLimit = 2000
	traceExportJSONType     = "application/json"
	traceExportCSVType      = "text/csv"
)

// traceExportCSVHeader 固定表头：一条记录占一行，列顺序不允许随实现漂移。
var traceExportCSVHeader = []string{
	"id", "started_at", "tool", "source", "user_agent", "trace_id",
	"duration_ms", "success", "error_code", "arguments", "result", "error",
}

// RuntimeTracesExport 按与 RuntimeTraces 相同的过滤语义导出记录。
// format=json 输出 records 数组 JSON（缩进 2 空格）；format=csv 输出固定
// 表头的 CSV 文本。body 是导出原文，filename 是建议文件名；非法 format
// 返回 INVALID_ARGUMENT。limit<=0 使用默认 2000。
func (r *Runtime) RuntimeTracesExport(format, query, tool, source, status string, limit int) (body []byte, contentType string, filename string, err error) {
	normalized := strings.ToLower(strings.TrimSpace(format))
	if normalized == "" {
		normalized = "json"
	}
	if normalized != "json" && normalized != "csv" {
		return nil, "", "", toolErrorDetails("INVALID_ARGUMENT", "trace export format must be json or csv", "validation", map[string]any{"format": format})
	}
	if limit <= 0 {
		limit = traceExportDefaultLimit
	}
	records := []observability.TraceRecord{}
	if r.traces != nil {
		records = r.traces.ExportRecords(observability.TraceQuery{
			Query: query, Tool: tool, Source: source, Status: status, Limit: limit,
		})
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	if normalized == "json" {
		encoded, marshalErr := json.MarshalIndent(records, "", "  ")
		if marshalErr != nil {
			return nil, "", "", marshalErr
		}
		return encoded, traceExportJSONType, "traces-" + stamp + ".json", nil
	}
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	if err := writer.Write(traceExportCSVHeader); err != nil {
		return nil, "", "", err
	}
	for _, record := range records {
		if err := writer.Write(traceCSVRow(record)); err != nil {
			return nil, "", "", err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, "", "", err
	}
	return buf.Bytes(), traceExportCSVType, "traces-" + stamp + ".csv", nil
}

// traceCSVRow 把记录投影成固定列顺序的 CSV 行。
func traceCSVRow(record observability.TraceRecord) []string {
	return []string{
		strconv.FormatUint(record.ID, 10),
		record.StartedAt.UTC().Format(time.RFC3339Nano),
		record.Tool,
		record.Source,
		record.UserAgent,
		record.TraceID,
		strconv.FormatFloat(record.DurationMS, 'f', -1, 64),
		strconv.FormatBool(record.Success),
		record.ErrorCode,
		traceCSVSingleLine(record.Arguments),
		traceCSVSingleLine(record.Result),
		record.Error,
	}
}

// traceCSVSingleLine 把预览里的换行转成字面 \n，保证一条记录永远占一行；
// 逗号/引号交给 encoding/csv 做标准转义（引号翻倍、整列加引号）。
func traceCSVSingleLine(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\\n")
	value = strings.ReplaceAll(value, "\r", "\\n")
	value = strings.ReplaceAll(value, "\n", "\\n")
	return value
}

// RuntimeTracesClear 清空 trace 窗口：内存索引与磁盘保留文件一起清掉，
// 返回清除条数。trace 关闭时清 0 条而不是报错，保持“未开启也是成功结构”。
func (r *Runtime) RuntimeTracesClear() (Result, error) {
	cleared := 0
	if r.traces != nil {
		count, err := r.traces.Clear()
		if err != nil {
			return nil, err
		}
		cleared = count
	}
	return Result{"ok": true, "source": runtimeAPISource, "action": "clear", "cleared": cleared}, nil
}
