package runtimeapi

import (
	"strings"

	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/observability"
)

func dispatchCallAnalytics(runtime Runtime, request Request) (map[string]any, error) {
	reader, ok := runtime.(CallAnalyticsRuntime)
	if !ok {
		return nil, &app.ToolError{Code: "CALL_ANALYTICS_UNSUPPORTED", Message: "runtime does not support call analytics", Category: "not_found"}
	}

	value := func(key string) string { return strings.TrimSpace(request.queryValue(key)) }
	query := observability.CallAnalyticsQuery{
		Range:         value("range"),
		Tool:          value("tool"),
		Source:        observability.Source(value("source")),
		Status:        value("status"),
		UserAgent:     value("user_agent"),
		TaskID:        value("task_id"),
		ErrorCode:     value("error_code"),
		ErrorCategory: value("error_category"),
		Stage:         observability.Stage(value("stage")),
	}
	switch query.Range {
	case "":
		query.Range = "1h"
	case "5m", "1h", "24h":
	default:
		return nil, invalidCallAnalyticsQuery("range must be 5m, 1h or 24h")
	}
	switch query.Source {
	case "", observability.SourceMCP, observability.SourceNexus, observability.SourceInternal:
	default:
		return nil, invalidCallAnalyticsQuery("source must be mcp, nexus or internal")
	}
	if query.Status != "" && query.Status != "success" && query.Status != "error" {
		return nil, invalidCallAnalyticsQuery("status must be success or error")
	}
	for _, filter := range []string{query.Tool, query.UserAgent, query.TaskID, query.ErrorCode, query.ErrorCategory, string(query.Stage)} {
		if len(filter) > 256 {
			return nil, invalidCallAnalyticsQuery("filter value is too long")
		}
	}
	return map[string]any(reader.RuntimeCallAnalytics(query)), nil
}

func invalidCallAnalyticsQuery(message string) *app.ToolError {
	return &app.ToolError{Code: "INVALID_ARGUMENT", Message: message, Category: "validation"}
}
