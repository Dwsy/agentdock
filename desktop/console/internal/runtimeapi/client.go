package runtimeapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	pathEvents       = "/internal/runtime/events"
	pathAlerts       = "/internal/runtime/alerts"
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
// 响应是 JSON 包封（TracesExportEnvelope）：body 为导出原文字符串，
// content_type/filename 从同名字段取；ok:false 走 {"ok":false,"code","error"} → *APIError。
// 返回 (data, contentType, filename, err)，签名与导出消费方约定不变。
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
	env, err := fetchJSON[TracesExportEnvelope](c, pathTracesExport, q)
	if err != nil {
		return nil, "", "", err
	}
	return []byte(env.Body), env.ContentType, env.Filename, nil
}

// TracesClear 实现 Backend：DELETE /internal/runtime/traces，清空追踪记录，返回清空条数。
func (c *Client) TracesClear() (int, error) {
	var out TracesClearResponse
	if err := c.doJSON(http.MethodDelete, pathTraces, nil, nil, &out); err != nil {
		return 0, err
	}
	return out.Cleared, nil
}

// Alerts 实现 Backend：GET /internal/runtime/alerts，返回告警列表与规则。
func (c *Client) Alerts() (*AlertsResponse, error) {
	return fetchJSON[AlertsResponse](c, pathAlerts, nil)
}

// ---------- GET /internal/runtime/events（SSE） ----------

// SSE 流错误：Recv 的返回值可被 errors.Is 识别。
var (
	// ErrSSEDisconnected 表示流因对端断开/读取失败而中止（可重连）。
	ErrSSEDisconnected = errors.New("runtimeapi: events stream disconnected")
	// ErrSSEClosed 表示调用方已 Close 后继续 Recv。
	ErrSSEClosed = errors.New("runtimeapi: events stream closed")
)

// Events 是一条 SSE 事件流：Recv 顺序返回帧，Close 关闭连接。
// 断线时 Recv 返回包装 ErrSSEDisconnected 的错误；ctx 取消时返回包装
// context 取消原因的错误（errors.Is(err, context.Canceled/DeadlineExceeded)）。
type Events interface {
	Recv() (SSEEvent, error)
	Close() error
}

// eventStream 是 Events 的实现：bufio.Scanner 按 SSE 帧（event:/data: 行
// + 空行）解析 text/event-stream 响应体。
type eventStream struct {
	ctx    context.Context
	body   io.ReadCloser
	scan   *bufio.Scanner
	closed bool
}

// OpenEvents 实现 EventsOpener：GET /internal/runtime/events 建立 SSE 流。
//
// 约定：
//   - 请求带 Accept: text/event-stream 与 Bearer 鉴权（Token 非空时）；
//   - 流式响应不受 HTTPClient 总超时约束（浅拷贝后清零 Timeout），
//     连接生命周期由 ctx 与 Close 管理；
//   - 非 2xx → *APIError（错误体 {"ok":false,"code","error"}），
//     连接/构造失败 → 带端点信息的普通 error；
//   - 帧内 data: 为 JSON（ping 保活帧同样透传，由调用方按 Kind 过滤）。
func (c *Client) OpenEvents(ctx context.Context) (Events, error) {
	u := strings.TrimRight(c.BaseURL, "/") + pathEvents
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("runtimeapi: GET %s: %w", pathEvents, err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.streamClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("runtimeapi: GET %s: %w", pathEvents, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorDetail))
		resp.Body.Close()
		if apiErr := responseError(http.MethodGet, pathEvents, resp.StatusCode, data); apiErr != nil {
			return nil, apiErr
		}
		return nil, fmt.Errorf("runtimeapi: GET %s: status %d", pathEvents, resp.StatusCode)
	}
	scan := bufio.NewScanner(resp.Body)
	scan.Buffer(make([]byte, 0, 64*1024), 1<<20) // 单行上限 1MB
	return &eventStream{ctx: ctx, body: resp.Body, scan: scan}, nil
}

// streamClient 返回流式请求用的 HTTP 客户端：复用注入的 Transport，但去掉
// 总超时（http.Client.Timeout 会掐断长连接），生命周期交给 ctx。
func (c *Client) streamClient() *http.Client {
	if c.HTTPClient != nil {
		cp := *c.HTTPClient
		cp.Timeout = 0
		return &cp
	}
	return &http.Client{}
}

// Recv 读取下一帧 SSE 事件：event: 定 Kind，data: 行（可多行，按 \n 拼接）
// 定 Data；空行分帧；`:` 注释行与 id:/retry: 字段忽略。帧格式见 types.go。
//
// 错误约定：
//   - 对端断开/读取失败 → 包装 ErrSSEDisconnected（可重连）；
//   - ctx 取消 → 包装 ctx.Err()；
//   - Close 后再 Recv → ErrSSEClosed。
func (s *eventStream) Recv() (SSEEvent, error) {
	if s.closed {
		return SSEEvent{}, ErrSSEClosed
	}
	var kind string
	var data []string
	for s.scan.Scan() {
		line := s.scan.Text()
		if line == "" {
			if kind == "" && len(data) == 0 {
				continue // 空帧跳过
			}
			return s.frame(kind, data), nil
		}
		if strings.HasPrefix(line, ":") {
			continue // 注释/保活行
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ") // SSE 规范：冒号后单个前导空格
		switch field {
		case "event":
			kind = value
		case "data":
			data = append(data, value)
		}
		// id:/retry: 与未知字段忽略
	}
	if s.closed {
		return SSEEvent{}, ErrSSEClosed
	}
	if s.ctx.Err() != nil {
		return SSEEvent{}, fmt.Errorf("runtimeapi: events stream: %w", s.ctx.Err())
	}
	if err := s.scan.Err(); err != nil {
		return SSEEvent{}, fmt.Errorf("runtimeapi: events stream read: %w: %w", err, ErrSSEDisconnected)
	}
	if kind != "" || len(data) > 0 {
		// 末帧容错：收尾缺空行分帧也投递，下次 Recv 再报断线。
		return s.frame(kind, data), nil
	}
	return SSEEvent{}, fmt.Errorf("runtimeapi: events stream: EOF: %w", ErrSSEDisconnected)
}

// frame 把一帧的 event/data 行组装为 SSEEvent。
func (s *eventStream) frame(kind string, data []string) SSEEvent {
	var raw json.RawMessage
	if len(data) > 0 {
		raw = json.RawMessage(strings.Join(data, "\n"))
	}
	return SSEEvent{Kind: kind, Data: raw}
}

// Close 关闭事件流；幂等。进行中的 Recv 会立即返回。
func (s *eventStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.body.Close()
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
