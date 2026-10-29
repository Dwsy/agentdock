package runtimeapi

import (
	"encoding/json"
	"fmt"
	"io"
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

// 端点路径（全 GET、JSON 响应）。
const (
	pathStatus      = "/internal/runtime/status"
	pathOverview    = "/internal/runtime/overview"
	pathAnalytics   = "/internal/runtime/analytics"
	pathCatalog     = "/internal/runtime/catalog"
	pathSkills      = "/internal/runtime/skills"
	pathPlugins     = "/internal/runtime/plugins"
	pathTasks       = "/internal/runtime/tasks"
	pathTraces      = "/internal/runtime/traces"
	pathFileChanges = "/internal/runtime/file-changes"
	pathArtifacts   = "/internal/runtime/artifacts"
)

// NewClient 创建指向 baseURL 的 Runtime API 客户端；token 为空表示不鉴权。
func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		Token:      token,
		HTTPClient: &http.Client{Timeout: defaultTimeout},
	}
}

// getJSON 发起 GET 请求并把响应 JSON 解码到 out。
//
// 错误约定：
//   - 非 2xx 或响应体 "ok":false  → *APIError（StatusCode/Code/Message），
//     错误体形状 {"ok":false,"code":"...","error":"..."}；
//   - 连接失败、读取失败、解码失败 → 带端点信息的普通 error（%w 包装底层错误）。
func (c *Client) getJSON(endpoint string, query url.Values, out any) error {
	u := strings.TrimRight(c.BaseURL, "/") + endpoint
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("runtimeapi: GET %s: %w", endpoint, err)
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
		return fmt.Errorf("runtimeapi: GET %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("runtimeapi: GET %s: read response: %w", endpoint, err)
	}

	// 先探测错误信封 {"ok":...,"code":...,"error":...}；ok 字段缺失（nil）不算失败。
	var env struct {
		OK    *bool  `json:"ok"`
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	envErr := json.Unmarshal(body, &env)
	okFalse := envErr == nil && env.OK != nil && !*env.OK
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices || okFalse {
		apiErr := &APIError{StatusCode: resp.StatusCode}
		if envErr == nil {
			apiErr.Code = env.Code
			apiErr.Message = env.Error
		}
		if apiErr.Message == "" {
			detail := strings.TrimSpace(string(body))
			if len(detail) > maxErrorDetail {
				detail = detail[:maxErrorDetail] + "..."
			}
			if detail == "" {
				detail = http.StatusText(resp.StatusCode)
			}
			apiErr.Message = fmt.Sprintf("status %d: %s", resp.StatusCode, detail)
		}
		return apiErr
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("runtimeapi: GET %s: decode response: %w", endpoint, err)
	}
	return nil
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
