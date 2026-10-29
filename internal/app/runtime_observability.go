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
		recordID = r.observer.EndTool(toolName, source, startedAt, duration, success, errorCode, errorCategory, userAgent, taskID, traceID, spanID, stages)
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
			Result:        tracePayload(result),
			Error:         traceErrorText(callErr),
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
