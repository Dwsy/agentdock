package observability

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CaptureStore 保存入站 HTTP 请求/响应的完整副本，供桌面端 Network 面板排障。
// 它与 TraceStore 是两种取舍：TraceStore 默认关闭且落盘时要脱敏，这里只存内存、
// 不脱敏、不落盘 —— 排障时要看到的就是原始 header 与 body。因此保留条数必须由
// 调用方显式限制，单侧正文也各自截断，避免把内存成本转嫁给整条请求链。
type CaptureStore struct {
	mu      sync.RWMutex
	enabled bool
	max     int
	records []CaptureRecord
	nextID  uint64
}

const (
	// CaptureBodyBytes 是单侧正文的内存保留上限。最坏情况 =
	// max * 2 * 上限，因此 max 上限必须和它一起看。
	CaptureBodyBytes = 32 << 10
	// CaptureHeaderBytes 是单侧 header 的保留上限。Cookie、Authorization
	// 都是自由文本，不限总量时一个畸形 header 就能把环撑爆。
	CaptureHeaderBytes = 16 << 10
	// captureQueryLimit 是单次查询可返回的记录数上限。
	captureQueryLimit   = 500
	captureDefaultLimit = 200
)

// CaptureHeader 是一条原始 header。用 Name/Value 而不是 map：
// 同名 header 可以重复出现，map 会丢信息。
type CaptureHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// CaptureMessage 是请求或响应的一侧快照。Body 是原始字节的 UTF-8 文本投影，
// 二进制内容会以替换字符呈现；Bytes 仍是真实字节数。
type CaptureMessage struct {
	Headers   []CaptureHeader `json:"headers"`
	Bytes     int64           `json:"bytes"`
	Body      string          `json:"body,omitempty"`
	Truncated bool            `json:"truncated,omitempty"`
}

// CaptureRecord 是一次调用的完整证据。Channel 决定字段语义：
// http 是入站请求/响应，nexus 是桥接 WebSocket 上的节点操作。
type CaptureRecord struct {
	ID         uint64         `json:"id"`
	Channel    CaptureChannel `json:"channel"`
	StartedAt  time.Time      `json:"started_at"`
	DurationMS float64        `json:"duration_ms"`
	Method     string         `json:"method"`
	Host       string         `json:"host"`
	Path       string         `json:"path"`
	Query      string         `json:"query,omitempty"`
	RawURL     string         `json:"raw_url"`
	Proto      string         `json:"proto"`
	RemoteAddr string         `json:"remote_addr"`
	UserAgent  string         `json:"user_agent,omitempty"`
	RequestID  string         `json:"request_id,omitempty"`
	// Status 是 HTTP 状态码；Nexus 通道没有 HTTP 语义，恒为 0。不能用
	// omitempty：字段缺省会让只认 Int 的解码端整条记录失败。
	Status int `json:"status"`
	// Outcome 是两个通道统一的结果键，面板只按它粗筛成功/失败。
	Outcome   string         `json:"outcome"`
	ErrorCode string         `json:"error_code,omitempty"`
	Request   CaptureMessage `json:"request"`
	Response  CaptureMessage `json:"response"`
}

// CaptureChannel 区分记录来自哪条调用链。
type CaptureChannel string

const (
	CaptureChannelHTTP  CaptureChannel = "http"
	CaptureChannelNexus CaptureChannel = "nexus"
)

const (
	// CaptureOutcomeOK / CaptureOutcomeError 是 Outcome 的全部取值。
	CaptureOutcomeOK    = "ok"
	CaptureOutcomeError = "error"
)

// CaptureStatus 是面板头部显示的采集状态。
type CaptureStatus struct {
	Enabled    bool `json:"enabled"`
	MaxRecords int  `json:"max_records"`
	Count      int  `json:"count"`
}

// CaptureQuery 是列表过滤条件。Summary 为 true 时只下发列表摘要。
type CaptureQuery struct {
	Query   string
	Channel string
	Method  string
	Status  string
	Limit   int
	Summary bool
}

// CaptureText 把任意文本截断成抓包正文，并报告是否发生截断。
// Nexus 通道没有 header 概念，正文是它的全部载荷。
func CaptureText(text string, limit int) (body string, truncated bool) {
	if limit <= 0 || len(text) <= limit {
		return text, false
	}
	return text[:limit], true
}

// NewCaptureStore 建一个只存内存的抓包环。max <= 0 时返回 nil，
// 调用方据此判断“未启用”，不必到处写 enabled 分支。
func NewCaptureStore(enabled bool, max int) *CaptureStore {
	if !enabled || max <= 0 {
		return nil
	}
	return &CaptureStore{enabled: true, max: max}
}

// Enabled 报告采集是否生效。
func (s *CaptureStore) Enabled() bool {
	return s != nil && s.enabled
}

// Record 追加一条记录并淘汰最旧记录。store 为 nil 或已禁用时是空操作。
func (s *CaptureStore) Record(record CaptureRecord) {
	if s == nil || !s.enabled {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	record.ID = s.nextID
	s.records = append(s.records, record)
	if len(s.records) > s.max {
		s.records = s.records[len(s.records)-s.max:]
	}
}

// Query 返回按时间倒序的记录。limit <= 0 时用默认值。
func (s *CaptureStore) Query(query CaptureQuery) []CaptureRecord {
	if s == nil {
		return nil
	}
	limit := query.Limit
	if limit <= 0 {
		limit = captureDefaultLimit
	}
	if limit > captureQueryLimit {
		limit = captureQueryLimit
	}
	needle := strings.ToLower(strings.TrimSpace(query.Query))
	channelFilter := strings.TrimSpace(query.Channel)
	methodFilter := strings.ToUpper(strings.TrimSpace(query.Method))
	statusFilter := strings.TrimSpace(query.Status)

	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]CaptureRecord, 0, min(limit, len(s.records)))
	for i := len(s.records) - 1; i >= 0; i-- {
		record := s.records[i]
		if !captureMatches(record, needle, channelFilter, methodFilter, statusFilter) {
			continue
		}
		if query.Summary {
			record = captureSummaryRecord(record)
		}
		out = append(out, record)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// RecordByID 按 ID 返回完整记录；记录已被保留窗口淘汰时返回 false。
func (s *CaptureStore) RecordByID(id uint64) (CaptureRecord, bool) {
	if s == nil || id == 0 {
		return CaptureRecord{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := len(s.records) - 1; i >= 0; i-- {
		if s.records[i].ID == id {
			return s.records[i], true
		}
	}
	return CaptureRecord{}, false
}

// Clear 清空内存环，返回被丢弃的条数。
func (s *CaptureStore) Clear() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	count := len(s.records)
	s.records = nil
	return count
}

// Status 返回采集状态；store 为 nil 表示未启用。
func (s *CaptureStore) Status() CaptureStatus {
	if s == nil {
		return CaptureStatus{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return CaptureStatus{Enabled: s.enabled, MaxRecords: s.max, Count: len(s.records)}
}

// CaptureHeaders 把 http.Header 摊平成有序列表，总量超过 limit 就停止收录并
// 标记截断。摊平而不是转 map，是为了保留同名 header 的出现顺序与次数。
func CaptureHeaders(header http.Header, limit int) (headers []CaptureHeader, truncated bool) {
	keys := make([]string, 0, len(header))
	for key := range header {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	used := 0
	for _, key := range keys {
		for _, value := range header.Values(key) {
			cost := len(key) + len(value)
			if used+cost > limit {
				return headers, true
			}
			headers = append(headers, CaptureHeader{Name: key, Value: value})
			used += cost
		}
	}
	return headers, false
}

// captureSummaryRecord 去掉正文与 header 列表，只保留列表视图需要的规模信息。
// 复制而不是原地修改：环里的记录是共享的，裁剪不能污染详情页。
func captureSummaryRecord(record CaptureRecord) CaptureRecord {
	record.Request.Body = ""
	record.Response.Body = ""
	record.Request.Headers = nil
	record.Response.Headers = nil
	return record
}

// captureMatches 是列表过滤：needle 匹配方法、路径、查询串与远端地址。
func captureMatches(record CaptureRecord, needle, channelFilter, methodFilter, statusFilter string) bool {
	if channelFilter != "" && !strings.EqualFold(string(record.Channel), channelFilter) {
		return false
	}
	if methodFilter != "" && !strings.EqualFold(record.Method, methodFilter) {
		return false
	}
	if statusFilter != "" {
		// ok/error 走两通道统一的 Outcome；HTTP 记录还可以按状态码或状态文本筛。
		matched := strings.EqualFold(statusFilter, record.Outcome) ||
			statusFilter == strconv.Itoa(record.Status) ||
			strings.EqualFold(statusFilter, http.StatusText(record.Status))
		if !matched {
			return false
		}
	}
	if needle == "" {
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{
		record.Method, record.Path, record.Query, record.RawURL, record.RemoteAddr, record.UserAgent,
	}, " "))
	return strings.Contains(haystack, needle)
}

// CaptureOutcome 把 HTTP 状态码归类成两通道统一的 Outcome。
func CaptureOutcome(status int) string {
	if status >= http.StatusBadRequest {
		return CaptureOutcomeError
	}
	return CaptureOutcomeOK
}
