package runtimeapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Client 是 Backend 的真实实现：只用标准库 HTTP 只读访问本机 Runtime API（/internal/runtime/*）。
type Client struct {
	// BaseURL 是 Runtime API 根地址，形如 http://127.0.0.1:8765。
	BaseURL string
	// Token 非空时随请求发送 Authorization: Bearer <token>；为空则不带鉴权头。
	Token string
	// HTTPClient 可注入自定义传输层；为 nil 时按需使用默认 5s 超时的客户端。
	HTTPClient *http.Client
}

// 编译期断言：Client 必须满足 Backend。
var _ Backend = (*Client)(nil)

const (
	defaultTimeout   = 5 * time.Second
	defaultTaskLimit = 50
	maxErrorDetail   = 512
)

// 端点路径（除注明外均为 GET、JSON 响应）。
const (
	pathStatus       = "/internal/runtime/status"
	pathOverview     = "/internal/runtime/overview"
	pathAnalytics    = "/internal/runtime/analytics"
	pathCatalog      = "/internal/runtime/catalog"
	pathSkills       = "/internal/runtime/skills"
	pathPlugins      = "/internal/runtime/plugins"
	pathTasks        = "/internal/runtime/tasks"
	pathTraces       = "/internal/runtime/traces"
	pathTracesExport = "/internal/runtime/traces/export"
	pathFileChanges  = "/internal/runtime/file-changes"
	pathArtifacts    = "/internal/runtime/artifacts"
)

// NewClient 创建指向 baseURL 的 Runtime API 客户端；token 为空表示不鉴权。
func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		Token:      token,
		HTTPClient: &http.Client{Timeout: defaultTimeout},
	}
}

// request 发起一次 HTTP 请求：body 非 nil 时 JSON 编码后作为请求体（POST/DELETE），
// Token 非空时带 Authorization: Bearer。返回响应体与响应头。
//
// 错误约定（与 getJSON 一致）：
//   - 非 2xx 或响应体 "ok":false  → *APIError（StatusCode/Code/Message），
//     错误体形状 {"ok":false,"code":"...","error":"..."}（兼容 "message" 兜底）；
//   - 请求构造/连接/读取失败 → 带方法与端点信息的普通 error（%w 包装底层错误）。
func (c *Client) request(method, endpoint string, query url.Values, body any) ([]byte, http.Header, error) {
	u := strings.TrimRight(c.BaseURL, "/") + endpoint
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, nil, fmt.Errorf("runtimeapi: %s %s: encode request: %w", method, endpoint, err)
		}
	}
	req, err := http.NewRequest(method, u, bytes.NewReader(payload))
	if err != nil {
		return nil, nil, fmt.Errorf("runtimeapi: %s %s: %w", method, endpoint, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	hc := c.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("runtimeapi: %s %s: %w", method, endpoint, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("runtimeapi: %s %s: read response: %w", method, endpoint, err)
	}
	if apiErr := responseError(method, endpoint, resp.StatusCode, data); apiErr != nil {
		return nil, resp.Header, apiErr
	}
	return data, resp.Header, nil
}

// responseError 探测错误信封 {"ok":...,"code":...,"error":...}；ok 字段缺失（nil）不算失败。
// 非 2xx 或 ok:false 时返回 *APIError，否则返回 nil。
func responseError(method, endpoint string, statusCode int, body []byte) *APIError {
	var env struct {
		OK      *bool  `json:"ok"`
		Code    string `json:"code"`
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	envErr := json.Unmarshal(body, &env)
	okFalse := envErr == nil && env.OK != nil && !*env.OK
	if statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices && !okFalse {
		return nil
	}
	apiErr := &APIError{StatusCode: statusCode}
	if envErr == nil {
		apiErr.Code = env.Code
		apiErr.Message = env.Error
		if apiErr.Message == "" {
			// 冲突等错误体可能用 "message" 字段。
			apiErr.Message = env.Message
		}
	}
	if apiErr.Message == "" {
		detail := strings.TrimSpace(string(body))
		if len(detail) > maxErrorDetail {
			detail = detail[:maxErrorDetail] + "..."
		}
		if detail == "" {
			detail = http.StatusText(statusCode)
		}
		apiErr.Message = fmt.Sprintf("status %d: %s", statusCode, detail)
	}
	return apiErr
}

// doJSON 发起请求并把响应 JSON 解码到 out（body 非 nil 时作为 JSON 请求体）。
func (c *Client) doJSON(method, endpoint string, query url.Values, body, out any) error {
	data, _, err := c.request(method, endpoint, query, body)
	if err != nil {
		return err
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("runtimeapi: %s %s: decode response: %w", method, endpoint, err)
		}
	}
	return nil
}

// getJSON 发起 GET 请求并把响应 JSON 解码到 out。
//
// 错误约定：
//   - 非 2xx 或响应体 "ok":false  → *APIError（StatusCode/Code/Message），
//     错误体形状 {"ok":false,"code":"...","error":"..."}；
//   - 连接失败、读取失败、解码失败 → 带端点信息的普通 error（%w 包装底层错误）。
func (c *Client) getJSON(endpoint string, query url.Values, out any) error {
	return c.doJSON(http.MethodGet, endpoint, query, nil, out)
}

// fetchJSON 是各只读端点的泛型取数助手。
func fetchJSON[T any](c *Client, endpoint string, query url.Values) (*T, error) {
	var v T
	if err := c.getJSON(endpoint, query, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// Status 实现 Backend：GET /internal/runtime/status。
func (c *Client) Status() (*Status, error) {
	return fetchJSON[Status](c, pathStatus, nil)
}

// Overview 实现 Backend：GET /internal/runtime/overview。
func (c *Client) Overview() (*Overview, error) {
	return fetchJSON[Overview](c, pathOverview, nil)
}

// Analytics 实现 Backend：GET /internal/runtime/analytics。
func (c *Client) Analytics() (*Analytics, error) {
	return fetchJSON[Analytics](c, pathAnalytics, nil)
}

// Catalog 实现 Backend：GET /internal/runtime/catalog。
func (c *Client) Catalog() (*Catalog, error) {
	return fetchJSON[Catalog](c, pathCatalog, nil)
}

// Skills 实现 Backend：GET /internal/runtime/skills。
func (c *Client) Skills() (*Skills, error) {
	return fetchJSON[Skills](c, pathSkills, nil)
}

// Plugins 实现 Backend：GET /internal/runtime/plugins。
func (c *Client) Plugins() (*Plugins, error) {
	return fetchJSON[Plugins](c, pathPlugins, nil)
}

// Tasks 实现 Backend：GET /internal/runtime/tasks?status=&limit=。
// status 为空则不带 status 参数；limit<=0 时默认 50。
func (c *Client) Tasks(status string, limit int) (*TaskList, error) {
	q := url.Values{}
	if status != "" {
		q.Set("status", status)
	}
	if limit <= 0 {
		limit = defaultTaskLimit
	}
	q.Set("limit", strconv.Itoa(limit))
	return fetchJSON[TaskList](c, pathTasks, q)
}

// Task 实现 Backend：GET /internal/runtime/tasks/{id}，id 按单个路径段转义。
func (c *Client) Task(id string) (*TaskDetail, error) {
	return fetchJSON[TaskDetail](c, pathTasks+"/"+url.PathEscape(id), nil)
}

// TaskActions 实现 Backend：POST /internal/runtime/tasks/{id}/actions。
// 非法状态迁移（code=INVALID_STATUS）与并发冲突（code=CONFLICT）映射为 *APIError。
func (c *Client) TaskActions(id string, req TaskActionsRequest) (*TaskActionResponse, error) {
	endpoint := pathTasks + "/" + url.PathEscape(id) + "/actions"
	return fetchJSONBody[TaskActionResponse](c, http.MethodPost, endpoint, req)
}

// TaskActivity 实现 Backend：GET /internal/runtime/tasks/{id}/activity。
func (c *Client) TaskActivity(id string) (*TaskActivity, error) {
	return fetchJSON[TaskActivity](c, pathTasks+"/"+url.PathEscape(id)+"/activity", nil)
}

// fetchJSONBody 是带 JSON 请求体的取数助手。
func fetchJSONBody[T any](c *Client, method, endpoint string, body any) (*T, error) {
	var v T
	if err := c.doJSON(method, endpoint, nil, body, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// Traces 实现 Backend：GET /internal/runtime/traces?q=&tool=&source=&status=&limit=。
// 空参数不发送；limit>0 时才带 limit。
func (c *Client) Traces(query, tool, source, status string, limit int) (*Traces, error) {
	q := url.Values{}
	if query != "" {
		q.Set("q", query)
	}
	if tool != "" {
		q.Set("tool", tool)
	}
	if source != "" {
		q.Set("source", source)
	}
	if status != "" {
		q.Set("status", status)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return fetchJSON[Traces](c, pathTraces, q)
}

// FileChanges 实现 Backend：GET /internal/runtime/file-changes?q=&limit=。
// 空参数不发送；limit>0 时才带 limit。
func (c *Client) FileChanges(query string, limit int) (*FileChanges, error) {
	q := url.Values{}
	if query != "" {
		q.Set("q", query)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return fetchJSON[FileChanges](c, pathFileChanges, q)
}

// Artifacts 实现 Backend：GET /internal/runtime/artifacts。
func (c *Client) Artifacts() (*Artifacts, error) {
	return fetchJSON[Artifacts](c, pathArtifacts, nil)
}

// ArtifactRevoke 实现 Backend：DELETE /internal/runtime/artifacts/{id}，
// 撤销产物；不存在映射为 *APIError（code=NOT_FOUND）。
func (c *Client) ArtifactRevoke(id string) error {
	var out ArtifactAction
	return c.doJSON(http.MethodDelete, pathArtifacts+"/"+url.PathEscape(id), nil, nil, &out)
}

// ArtifactExtend 实现 Backend：POST /internal/runtime/artifacts/{id}/extend，
// body {"hours":N}（1..720 由调用方校验），返回延期后的 artifact 对象。
func (c *Client) ArtifactExtend(id string, hours int) (*Artifact, error) {
	endpoint := pathArtifacts + "/" + url.PathEscape(id) + "/extend"
	resp, err := fetchJSONBody[ArtifactAction](c, http.MethodPost, endpoint, struct {
		Hours int `json:"hours"`
	}{Hours: hours})
	if err != nil {
		return nil, err
	}
	return resp.Artifact, nil
}

// TracesExport 实现 Backend：GET /internal/runtime/traces/export?format=&q=&tool=&source=&status=&limit=。
// 返回 (data, contentType, filename, err)：原始文件流、响应 Content-Type 的媒体类型
// （如 application/json / text/csv）与 Content-Disposition 里的 filename（缺失为空）。
func (c *Client) TracesExport(format, query, tool, source, status string, limit int) ([]byte, string, string, error) {
	q := url.Values{}
	if format != "" {
		q.Set("format", format)
	}
	if query != "" {
		q.Set("q", query)
	}
	if tool != "" {
		q.Set("tool", tool)
	}
	if source != "" {
		q.Set("source", source)
	}
	if status != "" {
		q.Set("status", status)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	data, header, err := c.request(http.MethodGet, pathTracesExport, q, nil)
	if err != nil {
		return nil, "", "", err
	}
	contentType, filename := header.Get("Content-Type"), ""
	if contentType != "" {
		if mediaType, _, perr := mime.ParseMediaType(contentType); perr == nil {
			contentType = mediaType
		}
	}
	if cd := header.Get("Content-Disposition"); cd != "" {
		if _, params, perr := mime.ParseMediaType(cd); perr == nil {
			filename = params["filename"]
		}
	}
	return data, contentType, filename, nil
}

// TracesClear 实现 Backend：DELETE /internal/runtime/traces，清空追踪记录，返回清空条数。
func (c *Client) TracesClear() (int, error) {
	var out TracesClearResponse
	if err := c.doJSON(http.MethodDelete, pathTraces, nil, nil, &out); err != nil {
		return 0, err
	}
	return out.Cleared, nil
}

// LoadConfig 依次读环境变量与 agentdock.env，组装 Runtime API 的 baseURL 与 token。
//
// 变量优先级：进程环境变量 > agentdock.env 文件 > 内置默认值。
//   - AGENTDOCK_HOST 默认 127.0.0.1
//   - AGENTDOCK_PORT 默认 8765
//   - AGENTDOCK_AUTH_TOKEN 默认空（不鉴权）
//
// env 文件路径取 AGENTDOCK_ENV_FILE（显式设置，读不到/解析失败会报错）；
// 未设置时用默认路径 ~/Library/Application Support/AgentDock/agentdock.env，
// 仅当该文件存在时才解析，用于补充缺失变量（文件里的键就是 AGENTDOCK_*）。
// 返回的 baseURL 形如 http://host:port。
func LoadConfig() (baseURL, token string, err error) {
	host := os.Getenv("AGENTDOCK_HOST")
	port := os.Getenv("AGENTDOCK_PORT")
	token = os.Getenv("AGENTDOCK_AUTH_TOKEN")

	envPath, explicit := os.LookupEnv("AGENTDOCK_ENV_FILE")
	if envPath == "" {
		explicit = false
		if home, herr := os.UserHomeDir(); herr == nil {
			envPath = filepath.Join(home, "Library", "Application Support", "AgentDock", "agentdock.env")
		}
	}
	if envPath != "" {
		data, rerr := os.ReadFile(envPath)
		switch {
		case rerr == nil:
			kv, perr := parseEnvFile(string(data))
			if perr != nil {
				return "", "", perr
			}
			if host == "" {
				host = kv["AGENTDOCK_HOST"]
			}
			if port == "" {
				port = kv["AGENTDOCK_PORT"]
			}
			if token == "" {
				token = kv["AGENTDOCK_AUTH_TOKEN"]
			}
		case explicit || !os.IsNotExist(rerr):
			// 显式指定的文件读不到要报错；默认路径只在存在时才处理。
			return "", "", fmt.Errorf("runtimeapi: read env file %s: %w", envPath, rerr)
		}
	}

	if host == "" {
		host = "127.0.0.1"
	}
	if port == "" {
		port = "8765"
	}
	return "http://" + host + ":" + port, token, nil
}

// parseEnvFile 解析 KEY=VALUE 行（跳过空行与 # 注释，容忍 export 前缀与成对引号）。
func parseEnvFile(data string) (map[string]string, error) {
	kv := make(map[string]string)
	for i, raw := range strings.Split(data, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("runtimeapi: env file line %d: invalid KEY=VALUE: %q", i+1, raw)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		kv[key] = value
	}
	return kv, nil
}
