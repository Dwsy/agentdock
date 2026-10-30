package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/uvwt/agentdock/internal/observability"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// observeToolCall 只记录固定白名单元数据。参数、结果、错误正文和 Details
// 都不能进入运行分析，避免诊断能力反过来扩大敏感数据暴露面。
func (r *Runtime) observeToolCall(parentCtx, ctx context.Context, name string, startedAt time.Time, args map[string]any, result *Result, callErr *error) {
	recovered := recover()
	success := recovered == nil && (callErr == nil || *callErr == nil)
	errorCode := ""
	errorCategory := ""
	if recovered != nil {
		errorCode = "PANIC"
		errorCategory = "runtime"
	} else if callErr != nil && *callErr != nil {
		errorCode, errorCategory = observableError(*callErr)
	}

	toolName := "unknown"
	if r != nil {
		if _, available := r.toolValidators[name]; available {
			toolName = name
		}
	}
	source := observability.SourceFromContext(ctx)
	userAgent := observability.UserAgentFromContext(ctx)
	taskID := observability.TaskIDFromContext(ctx)
	duration := time.Since(startedAt)
	stages := observability.CloseExecution(ctx)
	traceID, spanID := observability.TraceIdentifiers(parentCtx, ctx)
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		observability.AddStageEvents(span, startedAt, stages)
		span.SetAttributes(
			attribute.String("agentdock.tool.name", toolName),
			attribute.String("agentdock.tool.source", string(source)),
			attribute.Bool("agentdock.tool.success", success),
		)
		if errorCode != "" {
			span.SetAttributes(
				attribute.String("error.type", errorCode),
				attribute.String("agentdock.error.category", errorCategory),
			)
			span.SetStatus(codes.Error, errorCode)
		}
	}
	recordID := uint64(0)
	if r != nil && r.observer != nil {
		// 代码行数变动不依赖 trace 开关：分析页要在 trace 关闭时也能统计。
		insertions, deletions := traceCodeChangeCounts(toolName, result, success)
		recordID = r.observer.EndTool(observability.CallMeta{
			Tool:          toolName,
			Source:        source,
			StartedAt:     startedAt,
			Duration:      duration,
			Success:       success,
			ErrorCode:     errorCode,
			ErrorCategory: errorCategory,
			UserAgent:     userAgent,
			TaskID:        taskID,
			TraceID:       traceID,
			SpanID:        spanID,
			Stages:        stages,
			Insertions:    insertions,
			Deletions:     deletions,
		})
	}
	// call 事件与 recorder 同源同步：发布非阻塞，事件里同样只有白名单元数据。
	if r != nil {
		r.events.Publish(observability.EventKindCall, observability.CallEvent{
			ID:         recordID,
			Tool:       toolName,
			Source:     string(source),
			TaskID:     taskID,
			Success:    success,
			DurationMS: float64(duration) / float64(time.Millisecond),
			ErrorCode:  errorCode,
			StartedAt:  startedAt.UTC(),
		})
	}
	// trace 是独立开关：默认关闭时 r.traces 为 nil，不产生任何序列化开销。
	if r != nil && r.traces != nil {
		var resultMap map[string]any
		if result != nil {
			resultMap = *result
		}
		command := observability.CommandTraceFrom(toolName, args, resultMap)
		r.traces.Record(observability.TraceRecord{
			Tool:          toolName,
			Source:        string(source),
			UserAgent:     userAgent,
			TaskID:        taskID,
			StartedAt:     startedAt.UTC(),
			DurationMS:    float64(duration) / float64(time.Millisecond),
			Success:       success,
			ErrorCode:     errorCode,
			ErrorCategory: errorCategory,
			TraceID:       traceID,
			Arguments:     tracePayload(args),
			Result:        traceResultText(result, command),
			Error:         traceErrorText(callErr),
			Command:       command,
			FileChanges:   traceFileChanges(toolName, args, result, success),
		})
	}

	attributes := []any{
		"tool", toolName,
		"source", string(source),
		"user_agent", userAgent,
		"duration_ms", float64(duration) / float64(time.Millisecond),
		"ok", success,
	}
	if taskID != "" {
		attributes = append(attributes, "task_id", taskID)
	}
	if errorCode != "" {
		attributes = append(attributes, "error_code", errorCode, "error_category", errorCategory)
	}
	if traceID != "" {
		attributes = append(attributes, "trace_id", traceID)
		if spanID != "" {
			attributes = append(attributes, "span_id", spanID)
		}
	}
	slog.Info("tool finished", attributes...)

	if recovered != nil {
		panic(recovered)
	}
}

// tracePayload 只负责把 Payload 变成文本；截断与脱敏由 TraceStore 统一完成，
// 避免两处各自实现导致预览长度/脱敏规则不一致。
func tracePayload(value any) string {
	if value == nil {
		return ""
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// traceResultText 在命令类工具上用结构化摘要替代完整 Result 的 JSON 序列化。
// exec_command 的 Result 含最多 max_output_bytes（上限 4 MiB）的 stdout/stderr，
// 整份序列化再脱敏会让工具返回路径多花几十到几百毫秒；正文已经由
// CommandTrace 分字段保存，所以这里只留规模与退出状态。
//
// 图像类结果（view_image）同理：内联 base64 是几百 KB 到 2 MB，且按 key
// 字典序排在最前（'_' < 字母），会占满 8 KiB 预览把 source/image 元数据
// 整段挤掉，控制面板只能看到一串编码。剥掉 base64 并记录省略字节数，
// 元数据就能完整保留。
func traceResultText(result *Result, command *observability.CommandTrace) string {
	if command != nil {
		if summary, ok := command.ResultSummary(); ok {
			return summary
		}
	}
	if result != nil {
		if payload, ok := traceImageResultText(*result); ok {
			return payload
		}
	}
	return tracePayload(result)
}

// traceImageResultText 剥离内联图像 base64，返回剩余元数据的 JSON。
// ok=false 表示结果里没有内联图像，调用方回退到通用序列化。
func traceImageResultText(result Result) (string, bool) {
	encoded, exists := result["_mcp_image_base64"].(string)
	if !exists || encoded == "" {
		return "", false
	}
	// 复制后剥离：工具返回的原 Result 仍在调用方手里，不能被 trace 改写。
	trimmed := make(map[string]any, len(result))
	for key, value := range result {
		if key == "_mcp_image_base64" {
			continue
		}
		trimmed[key] = value
	}
	// 记录原始字节数，UI 才能说明“省略了多大的图片”。
	trimmed["image_bytes_omitted"] = base64DecodedLen(encoded)
	return tracePayload(trimmed), true
}

// base64DecodedLen 由编码长度推算原始字节数，不真正解码：这里只用于展示。
func base64DecodedLen(encoded string) int {
	size := len(encoded) / 4 * 3
	if padding := len(encoded) % 4; padding > 0 {
		size += padding - 1
	}
	for index := len(encoded) - 1; index >= 0 && encoded[index] == '='; index-- {
		size--
	}
	return max(size, 0)
}

func traceErrorText(callErr *error) string {
	if callErr == nil || *callErr == nil {
		return ""
	}
	return (*callErr).Error()
}

func observableError(err error) (string, string) {
	var toolErr *ToolError
	if errors.As(err, &toolErr) {
		return toolErr.Code, toolErr.Category
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "DEADLINE_EXCEEDED", "runtime"
	}
	if errors.Is(err, context.Canceled) {
		return "CANCELED", "runtime"
	}
	return "UNCLASSIFIED", "runtime"
}
