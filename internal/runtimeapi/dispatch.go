package runtimeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/mcp/oauthclient"
	"github.com/uvwt/agentdock/internal/observability"
)

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
		// 发布物子资源是写操作路由：撤销是 DELETE，延期是 POST，
		// 不允许 GET 兑底放行。
		if sub == "extend" {
			return method == http.MethodPost
		}
		return method == http.MethodDelete
	}
	if cleanPath == "/internal/runtime/traces" {
		// 查询是 GET，清空是 DELETE；两条语义共用一条路由。
		return method == http.MethodGet || method == http.MethodDelete
	}
	if cleanPath == "/internal/runtime/events" {
		// SSE 事件流是只读长连接。
		return method == http.MethodGet
	}
	if cleanPath == "/internal/runtime/alerts" {
		return method == http.MethodGet
	}
	if method == http.MethodGet {
		return true
	}
	return method == http.MethodPost && (cleanPath == "/internal/runtime/capabilities" || cleanPath == "/internal/runtime/mcp" || cleanPath == "/internal/runtime/mcp/oauth/callback" || cleanPath == "/internal/runtime/evolve" || cleanPath == ResourceLibraryPath)
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
		if sub == "extend" {
			return "POST"
		}
		return "DELETE"
	}
	if cleanPath == "/internal/runtime/traces" {
		return "GET, DELETE"
	}
	if cleanPath == "/internal/runtime/events" || cleanPath == "/internal/runtime/alerts" {
		return "GET"
	}
	if cleanPath == "/internal/runtime/capabilities" || cleanPath == "/internal/runtime/mcp" {
		return "GET, POST"
	}
	if cleanPath == "/internal/runtime/evolve" || cleanPath == "/internal/runtime/mcp/oauth/callback" {
		return "POST"
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

// Dispatch 只负责 Runtime API 的路由、参数校验与应用能力调用，不感知 HTTP request/response。
func Dispatch(ctx context.Context, runtime Runtime, request Request) (map[string]any, error) {
	method := strings.ToUpper(strings.TrimSpace(request.Method))
	path := strings.TrimSuffix(strings.TrimSpace(request.Path), "/")
	if !MethodAllowed(method, path) {
		return nil, &app.ToolError{Code: "NOT_FOUND", Message: "runtime API route not found", Category: "not_found"}
	}

	taskID, taskSub, isTaskPath := runtimeTaskPath(path)
	artifactID, artifactSub, isArtifactPath := runtimeArtifactPath(path)
	switch {
	case path == "/internal/runtime/alerts":
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
		result, err := detailRuntime.RuntimeTraceDetail(id)
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
	case path == "/internal/runtime/artifacts":
		artifacts, ok := runtime.(ArtifactRuntime)
		if !ok {
			return nil, &app.ToolError{
				Code: "ARTIFACTS_UNSUPPORTED", Message: "runtime does not expose published artifacts", Category: "not_found",
			}
		}
		result, err := artifacts.RuntimeArtifacts(ctx)
		return map[string]any(result), err
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
	if err != nil || limit < 0 || limit > 200 {
		return 0, &app.ToolError{
			Code: "INVALID_LIMIT", Message: "limit must be an integer between 0 and 200", Category: "validation",
			Details: map[string]any{"limit": raw, "minimum": 0, "maximum": 200},
		}
	}
	return limit, nil
}

// runtimeArtifactPath 解析发布物子资源路由。子资源只允许固定枚举（撤销为
// 空、延期为 extend），ID 不能吞掉额外路径段，也不能携带穿越型标识。
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
	default:
		return "", "", false
	}
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
