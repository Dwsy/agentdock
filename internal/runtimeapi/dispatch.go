package runtimeapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/mcp/oauthclient"
	"github.com/uvwt/agentdock/internal/observability"
	"github.com/uvwt/agentdock/internal/taskstate"
)

// MaxArtifactPublishBodyBytes 限制控制面主动发布请求 JSON 正文上限（28 MiB），
// 足以容纳 20 MiB 原始文件对应的 base64 编码及元信息字段。
const MaxArtifactPublishBodyBytes = 28 << 20

// MethodAllowed 返回指定 Runtime API 路径允许当前方法与否。
// HTTP 入口用它生成 405，其他传输也复用同一份方法契约。
func MethodAllowed(method, path string) bool {
	method = strings.ToUpper(strings.TrimSpace(method))
	cleanPath := strings.TrimSuffix(strings.TrimSpace(path), "/")
	if _, sub, ok := runtimeTaskPath(cleanPath); ok {
		// 任务子资源各自只有一种读/写语义，方法守卫必须按子资源收紧，
		// 不能沿用“GET 一律放行”的兜底。
		switch sub {
		case "actions":
			return method == http.MethodPost
		case "activity":
			return method == http.MethodGet
		default:
			return method == http.MethodGet || method == http.MethodDelete
		}
	}
	if _, sub, ok := runtimeArtifactPath(cleanPath); ok {
		// 发布物子资源各司其职：撤销是 DELETE，延期是 POST，内容读取是 GET/HEAD，
		// 不允许 GET 兜底放行写操作。
		switch sub {
		case "extend":
			return method == http.MethodPost
		case "content":
			return method == http.MethodGet || method == http.MethodHead
		default:
			return method == http.MethodDelete
		}
	}
	if cleanPath == "/internal/runtime/traces/image" {
		return method == http.MethodGet
	}
	if cleanPath == "/internal/runtime/traces" {
		// 查询是 GET，清空是 DELETE；两条语义共用一条路由。
		return method == http.MethodGet || method == http.MethodDelete
	}
	if cleanPath == "/internal/runtime/network" {
		// 与 traces 同形：查询是 GET，清空是 DELETE。
		return method == http.MethodGet || method == http.MethodDelete
	}
	if cleanPath == "/internal/runtime/events" {
		// SSE 事件流是只读长连接。
		return method == http.MethodGet
	}
	if cleanPath == "/internal/runtime/alerts" {
		return method == http.MethodGet
	}
	// config 有两个子路由，方法语义不同，先按路径收紧再走通用兜底。
	if cleanPath == "/internal/runtime/config/restart" {
		return method == http.MethodPost
	}
	if cleanPath == "/internal/runtime/manual-llm/cancel" || cleanPath == "/internal/runtime/manual-llm/toggle" {
		return method == http.MethodPost
	}
	if cleanPath == "/internal/runtime/remote" {
		return method == http.MethodGet
	}
	if cleanPath == "/internal/runtime/remote/token" || cleanPath == "/internal/runtime/remote/nexus/pair" || cleanPath == "/internal/runtime/remote/tunnel" {
		return method == http.MethodPost
	}
	if cleanPath == "/internal/runtime/config" {
		return method == http.MethodGet || method == http.MethodPost
	}
	if method == http.MethodGet {
		return true
	}
	return method == http.MethodPost && (cleanPath == "/internal/runtime/capabilities" || cleanPath == "/internal/runtime/mcp" || cleanPath == "/internal/runtime/mcp/oauth/callback" || cleanPath == "/internal/runtime/evolve" || cleanPath == "/internal/runtime/artifacts" || cleanPath == ResourceLibraryPath)
}

func AllowHeader(path string) string {
	cleanPath := strings.TrimSuffix(strings.TrimSpace(path), "/")
	if _, sub, ok := runtimeTaskPath(cleanPath); ok {
		switch sub {
		case "actions":
			return "POST"
		case "activity":
			return "GET"
		default:
			return "GET, DELETE"
		}
	}
	if _, sub, ok := runtimeArtifactPath(cleanPath); ok {
		switch sub {
		case "extend":
			return "POST"
		case "content":
			return "GET, HEAD"
		default:
			return "DELETE"
		}
	}
	if cleanPath == "/internal/runtime/artifacts" {
		return "GET, POST"
	}
	if cleanPath == "/internal/runtime/traces/image" {
		return "GET"
	}
	if cleanPath == "/internal/runtime/traces" || cleanPath == "/internal/runtime/network" {
		return "GET, DELETE"
	}
	if cleanPath == "/internal/runtime/events" || cleanPath == "/internal/runtime/alerts" {
		return "GET"
	}
	if cleanPath == "/internal/runtime/capabilities" || cleanPath == "/internal/runtime/mcp" {
		return "GET, POST"
	}
	if cleanPath == "/internal/runtime/evolve" || cleanPath == "/internal/runtime/mcp/oauth/callback" || cleanPath == "/internal/runtime/config/restart" || cleanPath == "/internal/runtime/manual-llm/cancel" || cleanPath == "/internal/runtime/manual-llm/toggle" {
		return "POST"
	}
	if cleanPath == "/internal/runtime/remote" {
		return "GET"
	}
	if cleanPath == "/internal/runtime/remote/token" || cleanPath == "/internal/runtime/remote/nexus/pair" || cleanPath == "/internal/runtime/remote/tunnel" {
		return "POST"
	}
	if cleanPath == "/internal/runtime/config" {
		return "GET, POST"
	}
	if cleanPath == ResourceLibraryPath {
		return "GET, POST"
	}
	return "GET"
}

// IsTaskActionsPath 判断路径是否为任务写操作路由；HTTP 入口据此决定是否读请求体。
func IsTaskActionsPath(path string) bool {
	_, sub, ok := runtimeTaskPath(strings.TrimSuffix(strings.TrimSpace(path), "/"))
	return ok && sub == "actions"
}

// IsArtifactExtendPath 判断路径是否为发布物延期写操作路由；HTTP 入口据此决定是否读请求体。
func IsArtifactExtendPath(path string) bool {
	_, sub, ok := runtimeArtifactPath(strings.TrimSuffix(strings.TrimSpace(path), "/"))
	return ok && sub == "extend"
}

// IsArtifactPublishPath 判断路径是否为控制面主动发布文件的写操作路由。
func IsArtifactPublishPath(method, path string) bool {
	return strings.EqualFold(strings.TrimSpace(method), http.MethodPost) && strings.TrimSuffix(strings.TrimSpace(path), "/") == "/internal/runtime/artifacts"
}

// IsArtifactContentPath 判断路径是否为发布物内容流只读路由。
func IsArtifactContentPath(path string) bool {
	_, sub, ok := runtimeArtifactPath(strings.TrimSuffix(strings.TrimSpace(path), "/"))
	return ok && sub == "content"
}

// ArtifactContentID 解析发布物内容流路由中的 artifact ID。
func ArtifactContentID(path string) (string, bool) {
	id, sub, ok := runtimeArtifactPath(strings.TrimSuffix(strings.TrimSpace(path), "/"))
	return id, ok && sub == "content"
}

// Dispatch 只负责 Runtime API 的路由、参数校验与应用能力调用，不感知 HTTP request/response。
func Dispatch(ctx context.Context, runtime Runtime, request Request) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// 上游客户端头只用于观测：挂进上下文后，dispatch 期间产生的 trace 会带上它，
	// header 本身不参与路由与方法判定。老 Nexus 不带 headers 时这里不写上下文。
	ctx = observability.WithUpstreamHeaders(ctx, request.Headers)
	method := strings.ToUpper(strings.TrimSpace(request.Method))
	path := strings.TrimSuffix(strings.TrimSpace(request.Path), "/")
	if !MethodAllowed(method, path) {
		return nil, &app.ToolError{Code: "NOT_FOUND", Message: "runtime API route not found", Category: "not_found"}
	}

	taskID, taskSub, isTaskPath := runtimeTaskPath(path)
	artifactID, artifactSub, isArtifactPath := runtimeArtifactPath(path)
	switch {
	case path == "/internal/runtime/alerts":
		if ctxAlerts, ok := runtime.(ContextAlertRuntime); ok {
			result, err := ctxAlerts.RuntimeAlertsContext(ctx)
			return map[string]any(result), err
		}
		alerts, ok := runtime.(AlertRuntime)
		if !ok {
			return nil, &app.ToolError{
				Code: "ALERTS_UNSUPPORTED", Message: "runtime does not support threshold alerts", Category: "not_found",
			}
		}
		result, err := alerts.RuntimeAlerts()
		return map[string]any(result), err
	case path == "/internal/runtime/events":
		// 事件流是长连接：JSON 请求/响应传输装不下 SSE，只能由 HTTP 传输直接写流。
		return nil, &app.ToolError{
			Code: "EVENTS_STREAM_UNSUPPORTED", Message: "event stream requires the HTTP streaming transport", Category: "not_found",
		}
	case path == "/internal/runtime/status":
		return map[string]any(runtime.RuntimeStatus()), nil
	case path == "/internal/runtime/analytics":
		return map[string]any(runtime.RuntimeAnalytics()), nil
	case path == "/internal/runtime/overview":
		if ctxOverview, ok := runtime.(ContextOverviewRuntime); ok {
			result, err := ctxOverview.RuntimeOverviewContext(ctx)
			return map[string]any(result), err
		}
		result, err := runtime.RuntimeOverview()
		return map[string]any(result), err
	case path == "/internal/runtime/diagnostics":
		diagnostics, ok := runtime.(DiagnosticsRuntime)
		if !ok {
			return nil, &app.ToolError{
				Code: "DIAGNOSTICS_UNSUPPORTED", Message: "runtime does not support diagnostics", Category: "not_found",
			}
		}
		return map[string]any(diagnostics.RuntimeDiagnostics()), nil
	case path == "/internal/runtime/catalog":
		catalog, ok := runtime.(CatalogRuntime)
		if !ok {
			return nil, &app.ToolError{
				Code: "CATALOG_UNSUPPORTED", Message: "runtime does not support the capability catalog", Category: "not_found",
			}
		}
		result, err := catalog.RuntimeCatalog(ctx)
		return map[string]any(result), err
	case path == "/internal/runtime/network" && method == http.MethodDelete:
		clearRuntime, ok := runtime.(NetworkClearRuntime)
		if !ok {
			return nil, &app.ToolError{
				Code: "NETWORK_CLEAR_UNSUPPORTED", Message: "runtime does not support clearing network capture records", Category: "not_found",
			}
		}
		result, err := clearRuntime.RuntimeNetworkClear()
		return map[string]any(result), err
	case path == "/internal/runtime/network/detail":
		detailRuntime, ok := runtime.(NetworkDetailRuntime)
		if !ok {
			return nil, &app.ToolError{
				Code: "NETWORK_DETAIL_UNSUPPORTED", Message: "runtime does not support network capture detail lookups", Category: "not_found",
			}
		}
		id, parseErr := strconv.ParseUint(strings.TrimSpace(request.queryValue("id")), 10, 64)
		if parseErr != nil || id == 0 {
			return nil, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "network capture id must be a positive integer", Category: "validation"}
		}
		result, err := detailRuntime.RuntimeNetworkDetail(id)
		return map[string]any(result), err
	case path == "/internal/runtime/network":
		captureRuntime, ok := runtime.(NetworkRuntime)
		if !ok {
			return nil, &app.ToolError{
				Code: "NETWORK_UNSUPPORTED", Message: "runtime does not support network capture records", Category: "not_found",
			}
		}
		limit := 0
		if raw := strings.TrimSpace(request.queryValue("limit")); raw != "" {
			parsed, parseErr := strconv.Atoi(raw)
			if parseErr != nil || parsed < 0 {
				return nil, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "network capture limit must be a non-negative integer", Category: "validation"}
			}
			limit = parsed
		}
		// 与 trace 同约定：view=summary 只下发列表摘要，正文由 detail 接口拉。
		summary := false
		switch view := strings.ToLower(strings.TrimSpace(request.queryValue("view"))); view {
		case "", "full":
		case "summary":
			summary = true
		default:
			return nil, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "network capture view must be full or summary", Category: "validation"}
		}
		result, err := captureRuntime.RuntimeNetwork(observability.CaptureQuery{
			Query:   request.queryValue("q"),
			Channel: request.queryValue("channel"),
			Method:  request.queryValue("method"),
			Status:  request.queryValue("status"),
			Limit:   limit,
			Summary: summary,
		})
		return map[string]any(result), err
	case path == "/internal/runtime/traces" && method == http.MethodDelete:
		clearRuntime, ok := runtime.(TraceClearRuntime)
		if !ok {
			return nil, &app.ToolError{
				Code: "TRACES_CLEAR_UNSUPPORTED", Message: "runtime does not support clearing trace records", Category: "not_found",
			}
		}
		result, err := clearRuntime.RuntimeTracesClear()
		return map[string]any(result), err
	case path == "/internal/runtime/traces/export":
		exportRuntime, ok := runtime.(TraceExportRuntime)
		if !ok {
			return nil, &app.ToolError{
				Code: "TRACES_EXPORT_UNSUPPORTED", Message: "runtime does not support trace export", Category: "not_found",
			}
		}
		format := strings.ToLower(strings.TrimSpace(request.queryValue("format")))
		if format == "" {
			format = "json"
		}
		if format != "json" && format != "csv" {
			return nil, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "trace export format must be json or csv", Category: "validation"}
		}
		limit := 0
		if raw := strings.TrimSpace(request.queryValue("limit")); raw != "" {
			parsed, parseErr := strconv.Atoi(raw)
			if parseErr != nil || parsed < 0 {
				return nil, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "trace export limit must be a non-negative integer", Category: "validation"}
			}
			limit = parsed
		}
		body, contentType, filename, err := exportRuntime.RuntimeTracesExport(
			format,
			request.queryValue("q"),
			request.queryValue("tool"),
			request.queryValue("source"),
			request.queryValue("status"),
			limit,
		)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"ok": true, "source": app.RuntimeAPISource, "action": "export",
			"format": format, "content_type": contentType, "filename": filename, "body": string(body),
		}, nil
	case path == "/internal/runtime/traces/image":
		imageRuntime, ok := runtime.(TraceImageRuntime)
		if !ok {
			return nil, &app.ToolError{
				Code: "TRACE_IMAGE_UNSUPPORTED", Message: "runtime does not support trace image previews", Category: "not_found",
			}
		}
		id, parseErr := strconv.ParseUint(strings.TrimSpace(request.queryValue("id")), 10, 64)
		if parseErr != nil || id == 0 {
			return nil, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "trace id must be a positive integer", Category: "validation"}
		}
		result, err := imageRuntime.RuntimeTraceImage(id)
		return map[string]any(result), err
	case path == "/internal/runtime/traces/detail":
		detailRuntime, ok := runtime.(TraceDetailRuntime)
		if !ok {
			return nil, &app.ToolError{
				Code: "TRACES_DETAIL_UNSUPPORTED", Message: "runtime does not support trace detail lookups", Category: "not_found",
			}
		}
		id, parseErr := strconv.ParseUint(strings.TrimSpace(request.queryValue("id")), 10, 64)
		if parseErr != nil || id == 0 {
			return nil, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "trace id must be a positive integer", Category: "validation"}
		}
		full := false
		switch value := strings.ToLower(strings.TrimSpace(request.queryValue("full"))); value {
		case "", "0", "false":
		case "1", "true":
			full = true
		default:
			return nil, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "trace full must be 1 or 0", Category: "validation"}
		}
		result, err := detailRuntime.RuntimeTraceDetail(id, full)
		return map[string]any(result), err
	case path == "/internal/runtime/traces":
		traceRuntime, ok := runtime.(TraceRuntime)
		if !ok {
			return nil, &app.ToolError{
				Code: "TRACES_UNSUPPORTED", Message: "runtime does not support trace records", Category: "not_found",
			}
		}
		limit := 0
		if raw := strings.TrimSpace(request.queryValue("limit")); raw != "" {
			parsed, parseErr := strconv.Atoi(raw)
			if parseErr != nil || parsed < 0 {
				return nil, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "trace limit must be a non-negative integer", Category: "validation"}
			}
			limit = parsed
		}
		// view=summary 只下发列表摘要；旧客户端不传 view 时保持完整预览语义。
		summary := false
		switch view := strings.ToLower(strings.TrimSpace(request.queryValue("view"))); view {
		case "", "full":
		case "summary":
			summary = true
		default:
			return nil, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "trace view must be full or summary", Category: "validation"}
		}
		result, err := traceRuntime.RuntimeTraces(observability.TraceQuery{
			Query:   request.queryValue("q"),
			Tool:    request.queryValue("tool"),
			Source:  request.queryValue("source"),
			Status:  request.queryValue("status"),
			Limit:   limit,
			Summary: summary,
		})
		return map[string]any(result), err
	case path == "/internal/runtime/artifacts" && method == http.MethodPost:
		publishRuntime, ok := runtime.(ArtifactPublishRuntime)
		if !ok {
			return nil, &app.ToolError{
				Code: "ARTIFACT_PUBLISH_UNSUPPORTED", Message: "runtime does not support publishing artifacts", Category: "not_found",
			}
		}
		input, err := decodeArtifactPublishRequest(request.Body)
		if err != nil {
			return nil, err
		}
		result, err := publishRuntime.RuntimeArtifactPublish(ctx, input)
		return map[string]any(result), err
	case path == "/internal/runtime/artifacts":
		artifacts, ok := runtime.(ArtifactRuntime)
		if !ok {
			return nil, &app.ToolError{
				Code: "ARTIFACTS_UNSUPPORTED", Message: "runtime does not expose published artifacts", Category: "not_found",
			}
		}
		result, err := artifacts.RuntimeArtifacts(ctx)
		return map[string]any(result), err
	case isArtifactPath && artifactSub == "content":
		return nil, &app.ToolError{
			Code: "ARTIFACT_CONTENT_STREAM_REQUIRED", Message: "artifact content requires the HTTP streaming transport", Category: "not_found",
		}
	case isArtifactPath && artifactSub == "extend":
		manage, ok := runtime.(ArtifactManageRuntime)
		if !ok {
			return nil, &app.ToolError{
				Code: "ARTIFACT_MANAGE_UNSUPPORTED", Message: "runtime does not support artifact revocation or extension", Category: "not_found",
			}
		}
		hours, err := decodeArtifactExtendRequest(request.Body)
		if err != nil {
			return nil, err
		}
		result, err := manage.RuntimeArtifactExtend(artifactID, hours)
		return map[string]any(result), err
	case isArtifactPath:
		manage, ok := runtime.(ArtifactManageRuntime)
		if !ok {
			return nil, &app.ToolError{
				Code: "ARTIFACT_MANAGE_UNSUPPORTED", Message: "runtime does not support artifact revocation or extension", Category: "not_found",
			}
		}
		result, err := manage.RuntimeArtifactRevoke(artifactID)
		return map[string]any(result), err
	case path == "/internal/runtime/file-changes":
		fileChanges, ok := runtime.(FileChangeRuntime)
		if !ok {
			return nil, &app.ToolError{
				Code: "FILE_CHANGES_UNSUPPORTED", Message: "runtime does not support file change history", Category: "not_found",
			}
		}
		limit := 0
		if raw := strings.TrimSpace(request.queryValue("limit")); raw != "" {
			parsed, parseErr := strconv.Atoi(raw)
			if parseErr != nil || parsed < 0 {
				return nil, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "file change limit must be a non-negative integer", Category: "validation"}
			}
			limit = parsed
		}
		result, err := fileChanges.RuntimeFileChanges(request.queryValue("q"), limit)
		return map[string]any(result), err
	case path == "/internal/runtime/capabilities":
		refresh := strings.EqualFold(request.queryValue("refresh"), "true") || method == http.MethodPost
		result, err := runtime.RuntimeCapabilities(ctx, refresh)
		return map[string]any(result), err
	case path == "/internal/runtime/skills":
		if ctxSkills, ok := runtime.(ContextSkillsRuntime); ok {
			result, err := ctxSkills.RuntimeSkillsContext(ctx)
			return map[string]any(result), err
		}
		result, err := runtime.RuntimeSkills()
		return map[string]any(result), err
	case strings.HasPrefix(path, "/internal/runtime/skills/"):
		skill, filePath, action, ok := runtimeSkillRoute(path)
		if !ok {
			return nil, &app.ToolError{Code: "NOT_FOUND", Message: "runtime Skill API route not found", Category: "not_found"}
		}
		target := skill
		if exact := strings.TrimSpace(request.queryValue("skill_ref")); exact != "" {
			target = exact
		}
		switch action {
		case "detail":
			result, err := runtime.RuntimeSkill(target)
			return map[string]any(result), err
		case "files":
			result, err := runtime.RuntimeSkillFiles(target)
			return map[string]any(result), err
		case "file":
			result, err := runtime.RuntimeSkillFile(target, filePath)
			return map[string]any(result), err
		default:
			return nil, &app.ToolError{Code: "NOT_FOUND", Message: "runtime Skill API route not found", Category: "not_found"}
		}
	case path == "/internal/runtime/plugins":
		result, err := runtime.RuntimePlugins(ctx)
		return map[string]any(result), err
	case strings.HasPrefix(path, "/internal/runtime/plugins/"):
		name, ok := runtimePluginName(path)
		if !ok {
			return nil, &app.ToolError{Code: "PLUGIN_NAME_REQUIRED", Message: "Plugin name is required", Category: "validation"}
		}
		result, err := runtime.RuntimePlugin(ctx, name)
		return map[string]any(result), err
	case path == ResourceLibraryPath:
		// 控制面只接收小 JSON。压缩包走配对 Nexus 的出站 HTTPS，不能塞进 Runtime 请求体。
		if len(request.Body) > 64*1024 {
			return nil, &app.ToolError{Code: "RESOURCE_LIBRARY_BODY_TOO_LARGE", Message: "resource library control body is too large", Category: "validation"}
		}
		library, ok := runtime.(ResourceLibraryRuntime)
		if !ok {
			return nil, &app.ToolError{
				Code: "RESOURCE_LIBRARY_UNSUPPORTED", Message: "runtime does not support the resource library", Category: "not_found",
			}
		}
		result, err := library.RuntimeResourceLibrary(ctx, method, request.Body)
		return map[string]any(result), err
	case path == "/internal/runtime/manual-llm" && method == http.MethodGet:
		manualRuntime, ok := runtime.(ManualLLMRuntime)
		if !ok {
			return nil, &app.ToolError{Code: "MANUAL_LLM_UNSUPPORTED", Message: "runtime does not support manual LLM queue", Category: "not_found"}
		}
		return map[string]any(manualRuntime.RuntimeManualLLMQueue()), nil
	case path == "/internal/runtime/manual-llm/cancel" && method == http.MethodPost:
		manualRuntime, ok := runtime.(ManualLLMRuntime)
		if !ok {
			return nil, &app.ToolError{Code: "MANUAL_LLM_UNSUPPORTED", Message: "runtime does not support manual LLM queue", Category: "not_found"}
		}
		var payload struct {
			RequestID string `json:"request_id"`
			Reason    string `json:"reason"`
		}
		if err := json.Unmarshal(request.Body, &payload); err != nil {
			return nil, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "manual-llm cancel body must be valid JSON", Category: "validation"}
		}
		result, err := manualRuntime.RuntimeManualLLMCancel(payload.RequestID, payload.Reason)
		if err != nil {
			return nil, &app.ToolError{Code: "MANUAL_LLM_CANCEL_FAILED", Message: err.Error(), Category: "validation"}
		}
		return map[string]any(result), nil
	case path == "/internal/runtime/manual-llm/toggle" && method == http.MethodPost:
		manualRuntime, ok := runtime.(ManualLLMRuntime)
		if !ok {
			return nil, &app.ToolError{Code: "MANUAL_LLM_UNSUPPORTED", Message: "runtime does not support manual LLM toggle", Category: "not_found"}
		}
		var payload struct {
			Enabled bool  `json:"enabled"`
			Persist *bool `json:"persist,omitempty"`
		}
		if err := json.Unmarshal(request.Body, &payload); err != nil {
			return nil, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "manual-llm toggle body must be valid JSON", Category: "validation"}
		}
		persist := true
		if payload.Persist != nil {
			persist = *payload.Persist
		}
		result, err := manualRuntime.RuntimeManualLLMToggle(payload.Enabled, persist)
		if err != nil {
			return nil, &app.ToolError{Code: "MANUAL_LLM_TOGGLE_FAILED", Message: err.Error(), Category: "validation"}
		}
		return map[string]any(result), nil
	case path == "/internal/runtime/config/restart" && method == http.MethodPost:
		configRuntime, ok := runtime.(ConfigRuntime)
		if !ok {
			return nil, &app.ToolError{Code: "CONFIG_UNSUPPORTED", Message: "runtime does not support configuration", Category: "not_found"}
		}
		result, err := configRuntime.RuntimeConfigRestart(ctx)
		return map[string]any(result), err
	case path == "/internal/runtime/config" && method == http.MethodPost:
		configRuntime, ok := runtime.(ConfigRuntime)
		if !ok {
			return nil, &app.ToolError{Code: "CONFIG_UNSUPPORTED", Message: "runtime does not support configuration", Category: "not_found"}
		}
		var updates map[string]string
		if err := json.Unmarshal(request.Body, &updates); err != nil {
			return nil, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "config body must be an object of string values", Category: "validation"}
		}
		result, err := configRuntime.RuntimeConfigUpdate(updates)
		if err != nil {
			return nil, &app.ToolError{Code: "CONFIG_INVALID", Message: err.Error(), Category: "validation"}
		}
		return map[string]any(result), err
	case path == "/internal/runtime/config" && method == http.MethodGet:
		configRuntime, ok := runtime.(ConfigRuntime)
		if !ok {
			return nil, &app.ToolError{Code: "CONFIG_UNSUPPORTED", Message: "runtime does not support configuration", Category: "not_found"}
		}
		return map[string]any(configRuntime.RuntimeConfig()), nil
	case path == "/internal/runtime/remote" && method == http.MethodGet:
		remoteRuntime, ok := runtime.(RemoteAccessRuntime)
		if !ok {
			return nil, &app.ToolError{Code: "REMOTE_UNSUPPORTED", Message: "runtime does not support remote access management", Category: "not_found"}
		}
		return map[string]any(remoteRuntime.RuntimeRemoteAccess(ctx)), nil
	case path == "/internal/runtime/remote/token" && method == http.MethodPost:
		remoteRuntime, ok := runtime.(RemoteAccessRuntime)
		if !ok {
			return nil, &app.ToolError{Code: "REMOTE_UNSUPPORTED", Message: "runtime does not support remote access management", Category: "not_found"}
		}
		var payload struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(request.Body, &payload); err != nil {
			return nil, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "remote token body must be valid JSON", Category: "validation"}
		}
		result, err := remoteRuntime.RuntimeRemoteTokenUpdate(payload.Token)
		if err != nil {
			return nil, &app.ToolError{Code: "TOKEN_INVALID", Message: err.Error(), Category: "validation"}
		}
		return map[string]any(result), err
	case path == "/internal/runtime/remote/nexus/pair" && method == http.MethodPost:
		remoteRuntime, ok := runtime.(RemoteAccessRuntime)
		if !ok {
			return nil, &app.ToolError{Code: "REMOTE_UNSUPPORTED", Message: "runtime does not support remote access management", Category: "not_found"}
		}
		var payload struct {
			Endpoint string `json:"endpoint"`
			Code     string `json:"code"`
			Name     string `json:"name,omitempty"`
		}
		if err := json.Unmarshal(request.Body, &payload); err != nil {
			return nil, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "nexus pair body must be valid JSON", Category: "validation"}
		}
		result, err := remoteRuntime.RuntimeNexusPair(ctx, payload.Endpoint, payload.Code, payload.Name)
		if err != nil {
			return nil, &app.ToolError{Code: "NEXUS_PAIR_FAILED", Message: err.Error(), Category: "validation"}
		}
		return map[string]any(result), err
	case path == "/internal/runtime/remote/tunnel" && method == http.MethodPost:
		remoteRuntime, ok := runtime.(RemoteAccessRuntime)
		if !ok {
			return nil, &app.ToolError{Code: "REMOTE_UNSUPPORTED", Message: "runtime does not support remote access management", Category: "not_found"}
		}
		var payload struct {
			Mode        string `json:"mode"`
			ServerURL   string `json:"server_url,omitempty"`
			TunnelToken string `json:"tunnel_token,omitempty"`
		}
		if err := json.Unmarshal(request.Body, &payload); err != nil {
			return nil, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "remote tunnel body must be valid JSON", Category: "validation"}
		}
		result, err := remoteRuntime.RuntimeTunnelConfigure(ctx, payload.Mode, payload.ServerURL, payload.TunnelToken)
		if err != nil {
			return nil, &app.ToolError{Code: "TUNNEL_CONFIGURE_FAILED", Message: err.Error(), Category: "validation"}
		}
		return map[string]any(result), err
	case path == "/internal/runtime/evolve" && method == http.MethodPost:
		args, err := decodeRuntimeEvolutionRequest(request.Body)
		if err != nil {
			return nil, err
		}
		result, err := runtime.RuntimeEvolve(ctx, args)
		return map[string]any(result), err
	case path == "/internal/runtime/mcp/oauth/callback" && method == http.MethodPost:
		oauthRuntime, ok := runtime.(MCPOAuthRuntime)
		if !ok {
			return nil, &app.ToolError{Code: "MCP_AUTH_UNSUPPORTED", Message: "runtime does not support Remote MCP OAuth", Category: "not_found"}
		}
		callback, err := decodeMCPOAuthCallback(request.Body)
		if err != nil {
			return nil, err
		}
		if err := oauthRuntime.RuntimeMCPOAuthCallback(ctx, callback); err != nil {
			return nil, err
		}
		return map[string]any{"accepted": true}, nil
	case path == "/internal/runtime/mcp" && method == http.MethodPost:
		args, err := decodeRuntimeMCPRequest(request.Body)
		if err != nil {
			return nil, err
		}
		result, err := runtime.RuntimeMCPManage(ctx, args)
		return map[string]any(result), err
	case path == "/internal/runtime/mcp":
		result, err := runtime.RuntimeMCPServers(ctx)
		return map[string]any(result), err
	case strings.HasPrefix(path, "/internal/runtime/mcp/"):
		name, ok := runtimeMCPName(path)
		if !ok {
			return nil, &app.ToolError{Code: "MCP_NAME_REQUIRED", Message: "dynamic MCP server name is required", Category: "validation"}
		}
		result, err := runtime.RuntimeMCPServer(ctx, name)
		return map[string]any(result), err
	case path == "/internal/runtime/tasks":
		limit, err := parseRuntimeTaskLimit(request.queryValue("limit"))
		if err != nil {
			return nil, err
		}
		if ctxTasks, ok := runtime.(ContextTasksRuntime); ok {
			result, err := ctxTasks.RuntimeTasksContext(ctx, request.queryValue("status"), limit)
			return map[string]any(result), err
		}
		result, err := runtime.RuntimeTasks(request.queryValue("status"), limit)
		return map[string]any(result), err
	case isTaskPath && taskSub == "activity":
		activity, ok := runtime.(TaskActivityRuntime)
		if !ok {
			return nil, &app.ToolError{
				Code: "TASK_ACTIVITY_UNSUPPORTED", Message: "runtime does not support task activity", Category: "not_found",
			}
		}
		result, err := activity.RuntimeTaskActivity(taskID)
		return map[string]any(result), err
	case isTaskPath && taskSub == "actions":
		actionRuntime, ok := runtime.(TaskActionRuntime)
		if !ok {
			return nil, &app.ToolError{
				Code: "TASK_ACTIONS_UNSUPPORTED", Message: "runtime does not support task actions", Category: "not_found",
			}
		}
		result, err := actionRuntime.RuntimeTaskAction(taskID, request.Body)
		return map[string]any(result), err
	case isTaskPath && method == http.MethodDelete:
		result, err := runtime.RuntimeTaskDelete(taskID)
		return map[string]any(result), err
	case isTaskPath:
		result, err := runtime.RuntimeTask(taskID)
		return map[string]any(result), err
	default:
		return nil, &app.ToolError{Code: "NOT_FOUND", Message: "runtime API route not found", Category: "not_found"}
	}
}

type runtimeMCPRequest struct {
	Action      string            `json:"action"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Transport   string            `json:"transport"`
	URL         string            `json:"url"`
	Command     string            `json:"command"`
	Args        []string          `json:"args"`
	Cwd         string            `json:"cwd"`
	HeaderEnv   map[string]string `json:"header_env"`
	EnvFromEnv  map[string]string `json:"env_from_env"`
	Enabled     *bool             `json:"enabled"`
	TimeoutMS   int               `json:"timeout_ms"`
	Key         string            `json:"key"`
	Value       *string           `json:"value"`
	CallbackID  string            `json:"callback_id"`
}

var runtimeMCPManageActions = map[string]bool{
	"add": true, "remove": true, "enable": true, "disable": true,
	"env_set": true, "env_unset": true, "env_list": true, "refresh": true,
	"authorize": true, "auth_clear": true,
}

func decodeRuntimeMCPRequest(body []byte) (map[string]any, error) {
	if len(body) > 64*1024 {
		return nil, runtimeMCPRequestError("MCP request body is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var request runtimeMCPRequest
	if err := decoder.Decode(&request); err != nil {
		return nil, runtimeMCPRequestError("invalid MCP request body")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, runtimeMCPRequestError("request body must contain exactly one JSON value")
	}
	action := strings.ToLower(strings.TrimSpace(request.Action))
	if action == "" {
		return nil, &app.ToolError{Code: "MCP_ACTION_REQUIRED", Message: "dynamic MCP action is required", Category: "validation"}
	}
	if !runtimeMCPManageActions[action] {
		return nil, &app.ToolError{Code: "MCP_ACTION_UNSUPPORTED", Message: "dynamic MCP action is not available through the Runtime API", Category: "validation"}
	}
	args := map[string]any{"action": action}
	// Runtime API 与模型工具最终进入同一份公共契约。只转发请求中真正提供的可选字段，
	// 避免 Go 零值被解释成 schema 中有语义的空 enum 值或未声明允许的 null。
	if request.Name != "" {
		args["name"] = request.Name
	}
	if request.Description != "" {
		args["description"] = request.Description
	}
	if request.Transport != "" {
		args["transport"] = request.Transport
	}
	if request.URL != "" {
		args["url"] = request.URL
	}
	if request.Command != "" {
		args["command"] = request.Command
	}
	if request.Cwd != "" {
		args["cwd"] = request.Cwd
	}
	if request.Key != "" {
		args["key"] = request.Key
	}
	if request.Args != nil {
		args["args"] = request.Args
	}
	if request.HeaderEnv != nil {
		args["header_env"] = request.HeaderEnv
	}
	if request.EnvFromEnv != nil {
		args["env_from_env"] = request.EnvFromEnv
	}
	if request.Value != nil {
		args["value"] = *request.Value
	}
	if request.Enabled != nil {
		args["enabled"] = *request.Enabled
	}
	if request.TimeoutMS > 0 {
		args["timeout_ms"] = request.TimeoutMS
	}
	if request.CallbackID != "" {
		args["callback_id"] = request.CallbackID
	}
	return args, nil
}

func decodeMCPOAuthCallback(body []byte) (oauthclient.CallbackResult, error) {
	if len(body) > 16*1024 {
		return oauthclient.CallbackResult{}, &app.ToolError{Code: "INVALID_MCP_AUTH_CALLBACK", Message: "OAuth callback body is too large", Category: "validation"}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var callback oauthclient.CallbackResult
	if err := decoder.Decode(&callback); err != nil {
		return oauthclient.CallbackResult{}, &app.ToolError{Code: "INVALID_MCP_AUTH_CALLBACK", Message: "invalid OAuth callback body", Category: "validation"}
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return oauthclient.CallbackResult{}, &app.ToolError{Code: "INVALID_MCP_AUTH_CALLBACK", Message: "OAuth callback body must contain exactly one JSON value", Category: "validation"}
	}
	callback.State = strings.TrimSpace(callback.State)
	callback.Code = strings.TrimSpace(callback.Code)
	callback.Issuer = strings.TrimSpace(callback.Issuer)
	callback.Error = strings.TrimSpace(callback.Error)
	callback.ErrorDescription = strings.TrimSpace(callback.ErrorDescription)
	if callback.State == "" {
		return oauthclient.CallbackResult{}, &app.ToolError{Code: "INVALID_MCP_AUTH_CALLBACK", Message: "OAuth callback state is required", Category: "validation"}
	}
	if callback.Code == "" && callback.Error == "" {
		return oauthclient.CallbackResult{}, &app.ToolError{Code: "INVALID_MCP_AUTH_CALLBACK", Message: "OAuth callback must contain code or error", Category: "validation"}
	}
	return callback, nil
}

func runtimeMCPRequestError(message string) error {
	return &app.ToolError{Code: "INVALID_MCP_REQUEST", Message: message, Category: "validation"}
}

func runtimeSkillRoute(path string) (skill, filePath, action string, ok bool) {
	const prefix = "/internal/runtime/skills/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return "", "", "", false
	}
	skill = parts[0]
	switch {
	case len(parts) == 1:
		return skill, "", "detail", true
	case len(parts) == 2 && parts[1] == "files":
		return skill, "", "files", true
	case len(parts) >= 3 && parts[1] == "files":
		filePath = strings.Join(parts[2:], "/")
		if strings.TrimSpace(filePath) == "" {
			return "", "", "", false
		}
		return skill, filePath, "file", true
	default:
		return "", "", "", false
	}
}

func runtimePluginName(path string) (string, bool) {
	const prefix = "/internal/runtime/plugins/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	name := strings.TrimSpace(strings.TrimPrefix(path, prefix))
	if name == "" || strings.Contains(name, "/") {
		return "", false
	}
	return name, true
}

func runtimeMCPName(path string) (string, bool) {
	const prefix = "/internal/runtime/mcp/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	name := strings.TrimSpace(strings.TrimPrefix(path, prefix))
	if name == "" || strings.Contains(name, "/") {
		return "", false
	}
	return name, true
}

func runtimeTaskPath(path string) (id, sub string, ok bool) {
	const prefix = "/internal/runtime/tasks/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	id = strings.TrimSpace(parts[0])
	if id == "" {
		return "", "", false
	}
	switch {
	case len(parts) == 1:
		return id, "", true
	case len(parts) == 2 && parts[1] == "activity":
		return id, "activity", true
	case len(parts) == 2 && parts[1] == "actions":
		return id, "actions", true
	default:
		// 子资源只允许固定枚举，任务 ID 不能吞掉额外路径段。
		return "", "", false
	}
}

func parseRuntimeTaskLimit(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 0 || limit > taskstate.MaxListLimit {
		return 0, &app.ToolError{
			Code: "INVALID_LIMIT", Message: fmt.Sprintf("limit must be an integer between 0 and %d", taskstate.MaxListLimit), Category: "validation",
			Details: map[string]any{"limit": raw, "minimum": 0, "maximum": taskstate.MaxListLimit},
		}
	}
	return limit, nil
}

// runtimeArtifactPath 解析发布物子资源路由。子资源只允许固定枚举（撤销为
// 空、延期为 extend、内容流为 content），ID 不能吞掉额外路径段，也不能携带穿越型标识。
func runtimeArtifactPath(path string) (id, sub string, ok bool) {
	const prefix = "/internal/runtime/artifacts/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	id = strings.TrimSpace(parts[0])
	if id == "" {
		return "", "", false
	}
	switch {
	case len(parts) == 1:
		return id, "", true
	case len(parts) == 2 && parts[1] == "extend":
		return id, "extend", true
	case len(parts) == 2 && parts[1] == "content":
		return id, "content", true
	default:
		return "", "", false
	}
}

// decodeArtifactPublishRequest 解析控制面主动发布请求，严格禁止未知字段并校验 base64 大小。
func decodeArtifactPublishRequest(body []byte) (app.ArtifactPublishInput, error) {
	if len(body) > MaxArtifactPublishBodyBytes {
		return app.ArtifactPublishInput{}, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "artifact publish body is too large", Category: "validation"}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var request struct {
		Path             string `json:"path"`
		Filename         string `json:"filename"`
		MimeType         string `json:"mime_type"`
		DataBase64       string `json:"data_base64"`
		RetentionSeconds int    `json:"retention_seconds"`
	}
	if err := decoder.Decode(&request); err != nil {
		return app.ArtifactPublishInput{}, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "invalid artifact publish body", Category: "validation"}
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return app.ArtifactPublishInput{}, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "request body must contain exactly one JSON value", Category: "validation"}
	}
	var data []byte
	if encoded := strings.TrimSpace(request.DataBase64); encoded != "" {
		if int64(base64.StdEncoding.DecodedLen(len(encoded))) > app.MaxDashboardPublishBytes+2 {
			return app.ArtifactPublishInput{}, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "uploaded file exceeds 20 MiB limit", Category: "validation"}
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return app.ArtifactPublishInput{}, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "invalid data_base64 payload", Category: "validation"}
		}
		if int64(len(decoded)) > app.MaxDashboardPublishBytes {
			return app.ArtifactPublishInput{}, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "uploaded file exceeds 20 MiB limit", Category: "validation"}
		}
		data = decoded
	}
	return app.ArtifactPublishInput{
		Path:             request.Path,
		Filename:         request.Filename,
		MimeType:         request.MimeType,
		Data:             data,
		RetentionSeconds: request.RetentionSeconds,
	}, nil
}

// decodeArtifactExtendRequest 解析延期正文 {"hours":N}。hours 缺失或正文
// 不是单个合法 JSON 值都返回 INVALID_ARGUMENT；取值范围由应用层校验。
func decodeArtifactExtendRequest(body []byte) (int, error) {
	if len(body) > 64*1024 {
		return 0, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "artifact extend body is too large", Category: "validation"}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var request struct {
		Hours *int `json:"hours"`
	}
	if err := decoder.Decode(&request); err != nil {
		return 0, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "invalid artifact extend body", Category: "validation"}
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return 0, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "request body must contain exactly one JSON value", Category: "validation"}
	}
	if request.Hours == nil {
		return 0, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "artifact extend hours is required", Category: "validation"}
	}
	return *request.Hours, nil
}
