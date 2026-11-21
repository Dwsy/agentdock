package observability

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	toolcore "github.com/uvwt/agentdock/internal/tool/core"
)

// TraceStore 保存工具调用的入参/出参副本，供桌面端人工排障。
// 它与 Recorder 刻意分离：Recorder 是低开销聚合指标，永远不接触 Payload；
// TraceStore 默认关闭，开启后也只保留截断并脱敏的预览，避免把大文件、
// 图片 base64 或凭据整份写进磁盘。
type TraceStore struct {
	mu      sync.RWMutex
	mode    TraceMode
	max     int
	records []TraceRecord
	// buf provides double-buffered sliding window backing storage at capacity so steady-state
	// Record calls advance in O(1) without allocating a fresh 2000-element slice each time.
	buf     []TraceRecord
	nextID  uint64
	root    string
	retain  time.Duration
	pending []TraceRecord
	file    *os.File
	writer  *bufio.Writer
	day     string
	// pendingFull 保存预览会被截断记录的全文副本（已脱敏），由 flushLocked
	// 与预览一起刷盘到 full/<day>.jsonl。全文不进内存环：环常驻 2000 条，
	// 带正文会把常驻内存从 MB 级拉到 GB 级。
	pendingFull      []TraceFullRecord
	pendingFullBytes int
	// 图片预览与 JSON trace 分离保存：disk 模式写入私有 images/ sidecar，
	// memory 模式只保留有总量上限的最近图片，避免大块二进制进入记录环。
	imagePreviews     map[uint64]traceImagePreviewData
	imagePreviewBytes int
	fullFile          *os.File
	fullWriter        *bufio.Writer
	fullDay           string
	closed            bool
	stop              chan struct{}
	done              chan struct{}
	closeOne          sync.Once
	closeErr          error

	flushEvery time.Duration
	bufferSize int

	diskBytesMu      sync.Mutex
	diskBytesAt      time.Time
	diskBytesRootMod int64
	diskBytesRootCnt int
	diskBytesFullMod int64
	diskBytesFullCnt int
	diskBytes        int64
}

type TraceMode string

const (
	TraceModeOff    TraceMode = "off"
	TraceModeMemory TraceMode = "memory"
	TraceModeDisk   TraceMode = "disk"
)

const (
	// TracePreviewBytes 是通用 Arguments/Result 单侧预览上限。
	TracePreviewBytes = 8 << 10
	// TraceCommandTextBytes 是 exec_command 的命令/stdout/stderr 单侧预览上限。
	// 与通用预览同一量级，保证单条命令记录最多约 30 KiB：trace 默认保留
	// 2000 条且全部驻留内存，再大就会让“记住一次调用”变成几十 MB 的常驻成本。
	TraceCommandTextBytes = 8 << 10
	// traceRedactionScanBytes 是脱敏正则的扫描窗口。脱敏对每个字节都要跑一遍
	// 规则，实测 1 MiB 命令输出约 65 ms；先把原始文本裁到保留窗口的两倍
	// （留出脱敏收缩的余量），再脱敏、再裁到预览上限，
	// 避免大输出阻塞工具返回路径。
	traceRedactionScanBytes = 2 * TracePreviewBytes
	traceQueryLimit         = 500
	traceDefaultLimit       = 100
	// traceExportLimit 是导出的默认条数：导出要覆盖完整保留窗口，
	// 不能沿用 UI 首屏的 traceQueryLimit 上限。
	traceExportLimit     = 2000
	traceTruncatedMarker = "…(truncated)"
)

type TraceOptions struct {
	Mode          TraceMode
	Root          string
	Retention     time.Duration
	BufferSize    int
	FlushInterval time.Duration
	MaxRecords    int
}

// FileChange 是 file_edit 调用产生的文件级变更摘要。
// Diff 是工具返回的 unified diff 预览（再次截断），用于控制面板的历史预览；
// 它不替代文件系统快照，也不保证能重放变更。
type FileChange struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	// MoveTo 是 move 的目标路径；其他动作为空。
	MoveTo string `json:"move_to,omitempty"`
	// ProjectRoot 是路径向上探测到的项目根（.git/go.mod 等标记），
	// 供文件历史按项目分组；探测失败时是文件的父目录。
	ProjectRoot string `json:"project_root,omitempty"`
	Insertions  int    `json:"insertions,omitempty"`
	Deletions   int    `json:"deletions,omitempty"`
	Diff        string `json:"diff,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
}

// FileChangeEntry 是文件历史视图的扁平投影：一次调用可能影响多个文件
// （patch/move），因此按文件拆成条目，但保留同一个 trace id 与时间。
type FileChangeEntry struct {
	ID          uint64    `json:"id"`
	TraceID     string    `json:"trace_id,omitempty"`
	Tool        string    `json:"tool"`
	Source      string    `json:"source"`
	UserAgent   string    `json:"user_agent,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	Success     bool      `json:"success"`
	Path        string    `json:"path"`
	Action      string    `json:"action"`
	MoveTo      string    `json:"move_to,omitempty"`
	ProjectRoot string    `json:"project_root,omitempty"`
	Insertions  int       `json:"insertions,omitempty"`
	Deletions   int       `json:"deletions,omitempty"`
	Diff        string    `json:"diff,omitempty"`
	Truncated   bool      `json:"truncated,omitempty"`
}

// TraceRecord 是一条可读的工具调用证据。Arguments/Result 是截断后的 JSON
// 文本而不是 json.RawMessage：截断会破坏 JSON 合法性，UI 只需要原文预览。
type TraceRecord struct {
	ID            uint64    `json:"id"`
	Tool          string    `json:"tool"`
	Source        string    `json:"source"`
	UserAgent     string    `json:"user_agent,omitempty"`
	TaskID        string    `json:"task_id,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	DurationMS    float64   `json:"duration_ms"`
	Success       bool      `json:"success"`
	ErrorCode     string    `json:"error_code,omitempty"`
	ErrorCategory string    `json:"error_category,omitempty"`
	TraceID       string    `json:"trace_id,omitempty"`
	// UpstreamHeaders 是本次调用所属上游会话的客户端请求头（自 Nexus 转发而来，
	// 键全小写）。它与 UserAgent 不重叠：UserAgent 是本机看到的传输标识，
	// 这里是最初发起请求的客户端提供的来源信息。
	UpstreamHeaders UpstreamHeaders `json:"upstream_headers,omitempty"`
	Arguments       string          `json:"arguments,omitempty"`
	Result          string          `json:"result,omitempty"`
	Error           string          `json:"error,omitempty"`
	Truncated       bool            `json:"truncated,omitempty"`
	// Command 只在命令类工具（exec_command 等）非空：它把命令、cwd 与输出
	// 拆成有上限的独立字段，UI 才能分段渲染并标出到底哪一侧被截断。
	Command *CommandTrace `json:"command,omitempty"`
	// FileChanges 只在 file_edit 成功时非空；关闭 trace 时不产生。
	FileChanges []FileChange `json:"file_changes,omitempty"`
	// ImagePreview 是图片预览 sidecar 的轻量元数据；图片字节不进入 JSONL。
	ImagePreview *TraceImagePreview `json:"image_preview,omitempty"`
	// ImagePreviewData 与 ImagePreviewMimeType 仅供 Record 写入 sidecar，禁止序列化。
	ImagePreviewData     []byte `json:"-"`
	ImagePreviewMimeType string `json:"-"`
}

type TraceQuery struct {
	Query  string
	Tool   string
	Source string
	Status string
	Limit  int
	// Summary 只返回列表摘要：去掉 Arguments/Result 与命令输出正文，
	// 让“100 条记录”的控制面板请求保持在几十 KB 而不是几 MB。
	Summary bool
	// Cursor 表示调用方显式请求 ID 游标分页；未置位时完全保持旧列表行为。
	// 磁盘回放可能按 StartedAt 而不是 ID 排序，游标路径必须自己按 ID 排序，
	// 否则翻页会跳记录。
	Cursor bool
	// BeforeID 是游标上界：只返回 ID 小于它的记录。Cursor 为 true 且
	// BeforeID 为 0 表示游标第一页（无上界，仍按 ID 降序）。
	BeforeID uint64
}

type TraceStatus struct {
	Mode          TraceMode `json:"mode"`
	Root          string    `json:"root,omitempty"`
	RetentionDays int       `json:"retention_days,omitempty"`
	MaxRecords    int       `json:"max_records"`
	Count         int       `json:"count"`
	Buffered      int       `json:"buffered"`
}

// NewTraceStore 在 mode=off 时返回 nil，调用方无需再判断开关。
func NewTraceStore(options TraceOptions) (*TraceStore, error) {
	if options.Mode == "" || options.Mode == TraceModeOff {
		return nil, nil
	}
	if options.MaxRecords <= 0 {
		options.MaxRecords = 2000
	}
	if options.BufferSize <= 0 {
		options.BufferSize = 32
	}
	if options.FlushInterval <= 0 {
		options.FlushInterval = time.Second
	}
	store := &TraceStore{
		mode:          options.Mode,
		max:           options.MaxRecords,
		bufferSize:    options.BufferSize,
		flushEvery:    options.FlushInterval,
		imagePreviews: make(map[uint64]traceImagePreviewData),
	}
	if options.Mode == TraceModeMemory {
		return store, nil
	}
	if options.Mode != TraceModeDisk {
		return nil, fmt.Errorf("unsupported trace mode %q", options.Mode)
	}
	if strings.TrimSpace(options.Root) == "" {
		return nil, errors.New("trace root is required for disk mode")
	}
	if options.Retention <= 0 {
		options.Retention = 72 * time.Hour
	}
	if err := os.MkdirAll(options.Root, 0o700); err != nil {
		return nil, fmt.Errorf("create trace root: %w", err)
	}
	store.root = options.Root
	store.retain = options.Retention
	store.stop = make(chan struct{})
	store.done = make(chan struct{})
	if err := store.loadRecent(); err != nil {
		return nil, err
	}
	store.cleanupExpired()
	go store.flushLoop()
	return store, nil
}

func (s *TraceStore) Mode() TraceMode {
	if s == nil {
		return TraceModeOff
	}
	return s.mode
}

// DiskBytes 统计 trace 根目录下全部 JSONL 保留文件的总字节数，
// 供阈值告警评估磁盘占用。memory/off 模式恒为 0。
func (s *TraceStore) DiskBytes() int64 {
	if s == nil || s.mode != TraceModeDisk || s.root == "" {
		return 0
	}
	const diskBytesTTL = 2 * time.Second
	rootMod, rootCnt := dirJSONLFingerprint(s.root)
	fullMod, fullCnt := dirJSONLFingerprint(s.fullDir())

	s.diskBytesMu.Lock()
	if !s.diskBytesAt.IsZero() &&
		time.Since(s.diskBytesAt) < diskBytesTTL &&
		s.diskBytesRootMod == rootMod &&
		s.diskBytesRootCnt == rootCnt &&
		s.diskBytesFullMod == fullMod &&
		s.diskBytesFullCnt == fullCnt {
		cached := s.diskBytes
		s.diskBytesMu.Unlock()
		return cached
	}
	s.diskBytesMu.Unlock()

	total := sumJSONLBytes(s.root) + sumJSONLBytes(s.fullDir())

	s.diskBytesMu.Lock()
	s.diskBytes = total
	s.diskBytesRootMod = rootMod
	s.diskBytesRootCnt = rootCnt
	s.diskBytesFullMod = fullMod
	s.diskBytesFullCnt = fullCnt
	s.diskBytesAt = time.Now()
	s.diskBytesMu.Unlock()
	return total
}

func (s *TraceStore) invalidateDiskBytes() {
	s.diskBytesMu.Lock()
	s.diskBytesAt = time.Time{}
	s.diskBytesMu.Unlock()
}

func dirJSONLFingerprint(dir string) (int64, int) {
	info, err := os.Stat(dir)
	if err != nil {
		return 0, 0
	}
	f, err := os.Open(dir)
	if err != nil {
		return info.ModTime().UnixNano(), 0
	}
	names, err := f.Readdirnames(-1)
	_ = f.Close()
	if err != nil {
		return info.ModTime().UnixNano(), 0
	}
	count := 0
	for _, name := range names {
		if strings.HasSuffix(name, ".jsonl") {
			count++
		}
	}
	return info.ModTime().UnixNano(), count
}

func sumJSONLBytes(dir string) int64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		total += info.Size()
	}
	return total
}

func (s *TraceStore) Status() TraceStatus {
	if s == nil {
		return TraceStatus{Mode: TraceModeOff}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := TraceStatus{Mode: s.mode, Root: s.root, MaxRecords: s.max, Count: len(s.records), Buffered: len(s.pending)}
	if s.retain > 0 {
		status.RetentionDays = int(s.retain / (24 * time.Hour))
	}
	return status
}

// Record 只保存预览：两侧各最多 TracePreviewBytes，超限立即截断并标记。
// 任何序列化/脱敏失败都不影响真实工具调用结果。
func (s *TraceStore) Record(record TraceRecord) uint64 {
	if s == nil {
		return 0
	}
	// 全文副本必须在预览截断之前捕获；只给会被截断的记录写旁路，
	// 小 payload 的预览本身就是全文。
	var full *TraceFullRecord
	if s.mode == TraceModeDisk && traceNeedsFullCopy(record) {
		built := buildTraceFull(record)
		full = &built
	}
	record.Arguments, record.Truncated = tracePreview(record.Arguments)
	result, truncated := tracePreview(record.Result)
	record.Result = result
	if truncated {
		record.Truncated = true
	}
	if record.Command != nil && record.Command.trim() {
		record.Truncated = true
	}
	record.Error = toolcore.RedactSecrets(record.Error, nil)

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0
	}
	s.nextID++
	record.ID = s.nextID
	if len(record.ImagePreviewData) > 0 && record.StartedAt.IsZero() {
		record.StartedAt = time.Now().UTC()
	}
	record.ImagePreview = s.storeImagePreviewLocked(record.ID, record.StartedAt, record.ImagePreviewData, record.ImagePreviewMimeType)
	record.ImagePreviewData = nil
	record.ImagePreviewMimeType = ""
	if len(s.records) < s.max {
		s.records = append(s.records, record)
	} else {
		evict := len(s.records) - s.max + 1
		evicted := s.records[:evict]
		for i, old := range evicted {
			s.removeImagePreviewLocked(old)
			evicted[i] = TraceRecord{}
		}
		if cap(s.records) > len(s.records) {
			s.records = append(s.records[evict:], record)
		} else {
			if len(s.buf) < 2*s.max {
				s.buf = make([]TraceRecord, 2*s.max)
			}
			n := copy(s.buf, s.records[evict:])
			s.buf[n] = record
			clear(s.buf[n+1:])
			s.records = s.buf[:n+1]
		}
	}
	if s.mode != TraceModeDisk {
		id := record.ID
		s.mu.Unlock()
		return id
	}
	s.pending = append(s.pending, record)
	if full != nil && s.pendingFullBytes < traceFullPendingBytes {
		// ID 在锁内分配，全文旁路必须用同一条记录的 ID 才能按 detail 回读。
		full.ID = record.ID
		s.pendingFull = append(s.pendingFull, *full)
		s.pendingFullBytes += len(full.Arguments) + len(full.Result) + len(full.Error)
	}
	if len(s.pending) >= s.bufferSize || s.pendingFullBytes >= traceFullPendingBytes {
		s.flushLocked()
	}
	id := record.ID
	s.mu.Unlock()
	return id
}

// tracePreview 先裁扫描窗口再脱敏，最后裁到预览上限。顺序不能反：
// 脱敏代价与输入长度成正比，先对 MB 级文本跑正则会卡住调用方。
func tracePreview(value string) (string, bool) {
	return tracePreviewWith(value, TracePreviewBytes, redactTracePayload)
}

func tracePreviewLimit(value string, limit int) (string, bool) {
	return tracePreviewWith(value, limit, redactTracePayload)
}

// traceCommandPreview 在通用脱敏之外补命令行凭据规则；通用规则只认
// KEY=value，而 curl -H "Authorization: Bearer ..." 这类写法匹配不到。
func traceCommandPreview(value string, limit int) (string, bool) {
	return tracePreviewWith(value, limit, redactCommandPayload)
}

func tracePreviewWith(value string, limit int, redact func(string) string) (string, bool) {
	truncated := false
	// 扫描窗口是上限的两倍（留脱敏收缩余量），但不低于
	// traceRedactionScanBytes：全文旁路用更大的上限时窗口必须跟着放大，
	// 否则“全文”会被隐式钉在 16 KiB。
	scan := max(traceRedactionScanBytes, 2*limit)
	if len(value) > scan {
		value = value[:scan]
		truncated = true
	}
	value = redact(value)
	if len(value) > limit {
		value = traceCut(value, limit)
		truncated = true
	}
	if truncated {
		value += traceTruncatedMarker
	}
	return value, truncated
}

// JSON 参数里的凭据是 "token": "..." 形式，不带等号，默认命令输出规则
// 匹配不到。这里在通用脱敏之后补一层 JSON 键值脱敏，只影响 trace 预览。
var traceJSONSecretPattern = regexp.MustCompile(`(?i)"(token|password|secret|api[_-]?key|authorization|access[_-]?token|refresh[_-]?token|private[_-]?key)"\s*:\s*"(?:[^"\\]|\\.)*"`)

func redactTracePayload(value string) string {
	out := toolcore.RedactSecrets(value, nil)
	if strings.IndexByte(out, ':') < 0 || strings.IndexByte(out, '"') < 0 || !hasAnyTraceSecretKeyword(out) {
		return out
	}
	out = traceJSONSecretPattern.ReplaceAllStringFunc(out, traceJSONSecretReplacement)
	return traceJSONOpenSecretPattern.ReplaceAllStringFunc(out, traceJSONSecretReplacement)
}

func hasAnyTraceSecretKeyword(s string) bool {
	return containsFold(s, "token") ||
		containsFold(s, "password") ||
		containsFold(s, "secret") ||
		containsFold(s, "key") ||
		containsFold(s, "authorization")
}

// traceJSONOpenSecretPattern 处理“截断正好落在敏感值中间”的尾巴：预截断后
// 未闭合的字符串匹配不到键值规则，若不额外抹掉，预览会把凭据前缀带出去。
// 只匹配到文本结尾的未闭合值，合法 JSON 不会命中。
var traceJSONOpenSecretPattern = regexp.MustCompile(`(?i)"(token|password|secret|api[_-]?key|authorization|access[_-]?token|refresh[_-]?token|private[_-]?key)"\s*:\s*"(?:[^"\\]|\\.)*$`)

func traceJSONSecretReplacement(match string) string {
	index := strings.Index(match, ":")
	if index < 0 {
		return "***"
	}
	return match[:index+1] + `"***"`
}

// RecordByID 返回保留窗口内指定 ID 的完整记录，供详情页按需读取；
// 列表接口只带摘要，避免一次拉取全部大 payload。
func (s *TraceStore) RecordByID(id uint64) (TraceRecord, bool) {
	if s == nil || id == 0 {
		return TraceRecord{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if idx, found := slices.BinarySearchFunc(s.records, id, func(r TraceRecord, target uint64) int {
		return cmp.Compare(r.ID, target)
	}); found {
		return s.records[idx], true
	}
	// Disk replay sorts concurrent records by StartedAt rather than ID; fall back to linear scan.
	for i := len(s.records) - 1; i >= 0; i-- {
		if s.records[i].ID == id {
			return s.records[i], true
		}
	}
	return TraceRecord{}, false
}

// Query 从新到旧过滤，保证 UI 打开即看到最近一次调用。
func (s *TraceStore) Query(query TraceQuery) []TraceRecord {
	if s == nil {
		return nil
	}
	return s.filterRecords(query, traceQueryLimitOf(query.Limit), true)
}

// QueryPage 返回一页记录以及是否还有更早的匹配记录。Cursor 未置位时走
// Query 的既有顺序，只多探一条判断 has_more；Cursor 置位时只取 ID < BeforeID
// 的记录（BeforeID=0 表示无上界）并显式按 ID 降序，避免磁盘回放的
// StartedAt 顺序导致首屏拿到旧 ID、后续翻页跳记录。
func (s *TraceStore) QueryPage(query TraceQuery) ([]TraceRecord, bool) {
	if s == nil {
		return nil, false
	}
	limit := traceQueryLimitOf(query.Limit)
	if !query.Cursor {
		records := s.filterRecords(query, limit+1, true)
		if len(records) > limit {
			return records[:limit], true
		}
		return records, false
	}
	return s.filterRecordsBeforeID(query, limit)
}

func traceQueryLimitOf(limit int) int {
	if limit <= 0 {
		return traceDefaultLimit
	}
	if limit > traceQueryLimit {
		return traceQueryLimit
	}
	return limit
}

// ExportRecords 按与 Query 相同的过滤语义导出记录（新到旧），但 limit
// 默认 traceExportLimit 且不设查询上限：导出要覆盖完整保留窗口。
// 返回值永远非 nil，方便调用方直接序列化成 JSON 数组。
func (s *TraceStore) ExportRecords(query TraceQuery) []TraceRecord {
	if s == nil {
		return []TraceRecord{}
	}
	limit := query.Limit
	if limit <= 0 {
		limit = traceExportLimit
	}
	return s.filterRecords(query, limit, false)
}

// filterRecords 承载 Query/ExportRecords 共用的过滤循环。invalidStatusNil
// 保持 Query 的既有语义：非法 status 过滤返回 nil；导出返回空数组。
func (s *TraceStore) filterRecords(query TraceQuery, limit int, invalidStatusNil bool) []TraceRecord {
	needle := strings.ToLower(strings.TrimSpace(query.Query))
	toolFilter := strings.TrimSpace(query.Tool)
	sourceFilter := strings.TrimSpace(query.Source)
	statusFilter := strings.ToLower(strings.TrimSpace(query.Status))
	if !validTraceStatusFilter(statusFilter) {
		if invalidStatusNil {
			return nil
		}
		return []TraceRecord{}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]TraceRecord, 0, min(limit, len(s.records)))
	for i := len(s.records) - 1; i >= 0; i-- {
		record := s.records[i]
		if !traceRecordMatches(record, needle, toolFilter, sourceFilter, statusFilter) {
			continue
		}
		if query.Summary {
			record = traceSummaryRecord(record)
		}
		out = append(out, record)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func validTraceStatusFilter(statusFilter string) bool {
	switch statusFilter {
	case "", "all", "success", "error":
		return true
	default:
		return false
	}
}

// filterRecordsBeforeID 是游标分页路径。保留窗口默认最多 2000 条，整批收集
// 再按 ID 排序的代价可忽略；这样能修正磁盘回放的 StartedAt 顺序，并让
// has_more 对本页过滤后的匹配集合同样准确。
func (s *TraceStore) filterRecordsBeforeID(query TraceQuery, limit int) ([]TraceRecord, bool) {
	needle := strings.ToLower(strings.TrimSpace(query.Query))
	toolFilter := strings.TrimSpace(query.Tool)
	sourceFilter := strings.TrimSpace(query.Source)
	statusFilter := strings.ToLower(strings.TrimSpace(query.Status))
	if !validTraceStatusFilter(statusFilter) {
		return nil, false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	matches := make([]TraceRecord, 0, min(len(s.records), limit+1))
	for _, record := range s.records {
		if record.ID == 0 {
			continue
		}
		// 0 是游标第一页的显式“无上界”，不能用 ID >= 0 把所有记录排除。
		if query.BeforeID != 0 && record.ID >= query.BeforeID {
			continue
		}
		if !traceRecordMatches(record, needle, toolFilter, sourceFilter, statusFilter) {
			continue
		}
		matches = append(matches, record)
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].ID > matches[j].ID })
	hasMore := len(matches) > limit
	if hasMore {
		matches = matches[:limit]
	}
	if query.Summary {
		for i := range matches {
			matches[i] = traceSummaryRecord(matches[i])
		}
	}
	return matches, hasMore
}

func traceRecordMatches(record TraceRecord, needle, toolFilter, sourceFilter, statusFilter string) bool {
	if toolFilter != "" && record.Tool != toolFilter {
		return false
	}
	if sourceFilter != "" && string(record.Source) != sourceFilter {
		return false
	}
	switch statusFilter {
	case "success":
		if !record.Success {
			return false
		}
	case "error":
		if record.Success {
			return false
		}
	}
	return needle == "" || traceMatches(record, needle)
}

// Clear 清空保留窗口：memory 只清内存索引；disk 再删除全部保留 JSONL 文件
// 并重置写句柄，让后续记录写进新文件而不是已删除的 inode。返回清除条数，
// 并发安全沿用既有互斥锁。
func (s *TraceStore) Clear() (int, error) {
	if s == nil {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cleared := len(s.records)
	s.records = nil
	if s.buf != nil {
		clear(s.buf)
		s.buf = nil
	}
	s.pending = nil
	s.pendingFull = nil
	s.pendingFullBytes = 0
	s.imagePreviews = make(map[uint64]traceImagePreviewData)
	s.imagePreviewBytes = 0
	s.invalidateDiskBytes()
	if s.mode != TraceModeDisk {
		return cleared, nil
	}
	if s.writer != nil {
		_ = s.writer.Flush()
		s.writer = nil
	}
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
	s.day = ""
	if s.fullWriter != nil {
		_ = s.fullWriter.Flush()
		s.fullWriter = nil
	}
	if s.fullFile != nil {
		_ = s.fullFile.Close()
		s.fullFile = nil
	}
	s.fullDay = ""
	// 全文旁路与预览同生命周期：清空保留窗口时不能留下可按 ID 回读的正文。
	s.cleanFullFiles("")
	if err := s.clearImagePreviewsLocked(); err != nil {
		return cleared, err
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cleared, nil
		}
		return cleared, fmt.Errorf("read trace root: %w", err)
	}
	var clearErrs []error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		if err := os.Remove(filepath.Join(s.root, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			clearErrs = append(clearErrs, fmt.Errorf("remove trace file %s: %w", entry.Name(), err))
		}
	}
	return cleared, errors.Join(clearErrs...)
}

// traceSummaryRecord 去掉正文只保留摘要。这里复制 Command 与 FileChanges
// 而不是原地修改：记录本身仍在保留窗口里，摘要投影不能破坏后续详情读取。
func traceSummaryRecord(record TraceRecord) TraceRecord {
	record.Arguments = ""
	record.Result = ""
	if record.Command != nil {
		command := *record.Command
		command.Command = ""
		command.Stdout = ""
		command.Stderr = ""
		record.Command = &command
	}
	// FileChanges 的 Diff 与命令输出同属正文：列表页只需要路径与增删计数，
	// 保留 diff 会让 100 条记录多传几百 KB，违背 summary 模式的体积约定。
	// Truncated 保留，UI 据此知道详情里还有被截断的 diff 可看。
	if len(record.FileChanges) > 0 {
		changes := make([]FileChange, len(record.FileChanges))
		copy(changes, record.FileChanges)
		for index := range changes {
			changes[index].Diff = ""
		}
		record.FileChanges = changes
	}
	return record
}

func traceMatches(record TraceRecord, needle string) bool {
	if containsFold(record.Tool, needle) ||
		containsFold(string(record.Source), needle) ||
		containsFold(record.UserAgent, needle) ||
		containsFold(record.TaskID, needle) ||
		containsFold(record.ErrorCode, needle) ||
		containsFold(record.TraceID, needle) ||
		containsFold(record.Error, needle) ||
		containsFold(record.Arguments, needle) ||
		containsFold(record.Result, needle) {
		return true
	}
	if record.Command != nil && commandMatchesFold(record.Command, needle) {
		return true
	}
	return false
}

func commandMatchesFold(c *CommandTrace, needle string) bool {
	return containsFold(c.Summary, needle) ||
		containsFold(c.Command, needle) ||
		containsFold(c.Workdir, needle) ||
		containsFold(c.Runtime, needle) ||
		containsFold(c.SessionID, needle) ||
		containsFold(c.Status, needle) ||
		containsFold(c.Action, needle) ||
		containsFold(c.SkillRef, needle) ||
		containsFold(c.Stdout, needle) ||
		containsFold(c.Stderr, needle)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// containsFold reports whether haystack contains lowercase needle case-insensitively
// without allocating on ASCII inputs, falling back to strings.ToLower for non-ASCII UTF-8.
func containsFold(haystack, needleLower string) bool {
	n := len(needleLower)
	if n == 0 {
		return true
	}
	if len(haystack) < n {
		return false
	}
	if isASCII(haystack) && isASCII(needleLower) {
		limit := len(haystack) - n
		first := needleLower[0]
		for i := 0; i <= limit; i++ {
			c := haystack[i]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != first {
				continue
			}
			matched := true
			for j := 1; j < n; j++ {
				hc := haystack[i+j]
				if hc >= 'A' && hc <= 'Z' {
					hc += 'a' - 'A'
				}
				if hc != needleLower[j] {
					matched = false
					break
				}
			}
			if matched {
				return true
			}
		}
		return false
	}
	return strings.Contains(strings.ToLower(haystack), needleLower)
}

// TaskRecords 返回指定 task_id 的调用记录（新到旧），供任务活动聚合。
// 这里必须精确匹配任务 ID：自由文本 Query 是子串搜索，会把碰巧包含
// 同名子串的其他调用混进任务活动里。
func (s *TraceStore) TaskRecords(taskID string) []TraceRecord {
	if s == nil {
		return nil
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return []TraceRecord{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]TraceRecord, 0)
	for i := len(s.records) - 1; i >= 0; i-- {
		record := s.records[i]
		if record.TaskID != taskID {
			continue
		}
		out = append(out, record)
	}
	return out
}

// FileChanges 返回当前保留窗口内的文件变更条目（新到旧）。
// 过滤基于路径子串，大小写不敏感；limit<=0 使用默认值。
func (s *TraceStore) FileChanges(query string, limit int) []FileChangeEntry {
	if s == nil {
		return nil
	}
	if limit <= 0 {
		limit = traceDefaultLimit
	}
	if limit > traceQueryLimit {
		limit = traceQueryLimit
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]FileChangeEntry, 0, limit)
	for i := len(s.records) - 1; i >= 0; i-- {
		record := s.records[i]
		for j := len(record.FileChanges) - 1; j >= 0; j-- {
			change := record.FileChanges[j]
			if needle != "" {
				if strings.IndexByte(needle, '\n') >= 0 {
					if !strings.Contains(strings.ToLower(change.Path+"\n"+change.MoveTo+"\n"+change.Action+"\n"+change.Diff), needle) {
						continue
					}
				} else if !containsFold(change.Path, needle) && !containsFold(change.MoveTo, needle) && !containsFold(change.Action, needle) && !containsFold(change.Diff, needle) {
					continue
				}
			}
			out = append(out, FileChangeEntry{
				ID: record.ID, TraceID: record.TraceID, Tool: record.Tool, Source: record.Source,
				StartedAt: record.StartedAt, Success: record.Success,
				Path: change.Path, Action: change.Action,
				MoveTo: change.MoveTo, ProjectRoot: change.ProjectRoot,
				Insertions: change.Insertions, Deletions: change.Deletions,
				Diff: change.Diff, Truncated: change.Truncated,
			})
			if len(out) >= limit {
				return out
			}
		}
	}
	return out
}

// ToolNames 返回当前窗口内出现过的工具名，供 UI 构建筛选器。
func (s *TraceStore) ToolNames() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := map[string]struct{}{}
	for _, record := range s.records {
		seen[record.Tool] = struct{}{}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (s *TraceStore) Close() error {
	if s == nil {
		return nil
	}
	s.closeOne.Do(func() {
		if s.mode == TraceModeDisk {
			close(s.stop)
			<-s.done
		}
		s.mu.Lock()
		s.closed = true
		if s.mode == TraceModeDisk {
			s.flushLocked()
		}
		s.mu.Unlock()
		if s.file != nil {
			s.closeErr = s.file.Close()
			s.file = nil
		}
		if s.fullFile != nil {
			_ = s.fullFile.Close()
			s.fullFile = nil
		}
	})
	return s.closeErr
}

func (s *TraceStore) flushLoop() {
	ticker := time.NewTicker(s.flushEvery)
	defer ticker.Stop()
	defer close(s.done)
	cleanup := time.NewTicker(6 * time.Hour)
	defer cleanup.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.mu.Lock()
			s.flushLocked()
			s.mu.Unlock()
		case <-cleanup.C:
			s.cleanupExpired()
		}
	}
}

// flushLocked 需要持有写锁；磁盘错误只终止本次写入，不能让 trace 影响服务。
func (s *TraceStore) flushLocked() {
	s.flushPreviewsLocked()
	s.flushFullLocked()
	s.invalidateDiskBytes()
}

func (s *TraceStore) flushPreviewsLocked() {
	if len(s.pending) == 0 {
		return
	}
	if err := s.ensureWriter(); err != nil {
		s.pending = nil
		return
	}
	for _, record := range s.pending {
		encoded, err := json.Marshal(record)
		if err != nil {
			continue
		}
		if _, err := s.writer.Write(encoded); err != nil {
			s.pending = nil
			_ = s.writer.Flush()
			return
		}
		if err := s.writer.WriteByte('\n'); err != nil {
			s.pending = nil
			return
		}
	}
	s.pending = nil
	_ = s.writer.Flush()
}

// flushFullLocked 把全文旁路写进 full/<day>.jsonl；失败只丢本次待写队列，
// 预览照常保留，UI 会降级为“仅预览”。
func (s *TraceStore) flushFullLocked() {
	if len(s.pendingFull) == 0 {
		return
	}
	if err := s.ensureFullWriter(); err != nil {
		s.pendingFull = nil
		s.pendingFullBytes = 0
		return
	}
	for _, record := range s.pendingFull {
		encoded, err := json.Marshal(record)
		if err != nil {
			continue
		}
		if _, err := s.fullWriter.Write(encoded); err != nil {
			s.pendingFull = nil
			s.pendingFullBytes = 0
			_ = s.fullWriter.Flush()
			return
		}
		if err := s.fullWriter.WriteByte('\n'); err != nil {
			s.pendingFull = nil
			s.pendingFullBytes = 0
			return
		}
	}
	s.pendingFull = nil
	s.pendingFullBytes = 0
	_ = s.fullWriter.Flush()
}

func (s *TraceStore) ensureFullWriter() error {
	day := traceDay(time.Now())
	if s.fullFile != nil && s.fullDay == day {
		return nil
	}
	if s.fullWriter != nil {
		_ = s.fullWriter.Flush()
	}
	if s.fullFile != nil {
		_ = s.fullFile.Close()
		s.fullFile = nil
	}
	if err := os.MkdirAll(s.fullDir(), 0o700); err != nil {
		return err
	}
	path := filepath.Join(s.fullDir(), day+".jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	s.fullFile = file
	s.fullWriter = bufio.NewWriterSize(file, 32<<10)
	s.fullDay = day
	return nil
}

// cleanFullFiles 删除 full/ 下早于 cutoff 的保留文件；
// cutoff 为空表示全部删除（清空保留窗口时用）。
func (s *TraceStore) cleanFullFiles(cutoff string) {
	entries, err := os.ReadDir(s.fullDir())
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		if cutoff == "" || strings.TrimSuffix(entry.Name(), ".jsonl") < cutoff {
			_ = os.Remove(filepath.Join(s.fullDir(), entry.Name()))
		}
	}
}

func (s *TraceStore) ensureWriter() error {
	day := traceDay(time.Now())
	if s.file != nil && s.day == day {
		return nil
	}
	if s.writer != nil {
		_ = s.writer.Flush()
	}
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
	path := filepath.Join(s.root, day+".jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	s.file = file
	s.writer = bufio.NewWriterSize(file, 32<<10)
	s.day = day
	return nil
}

// loadRecent 只回放保留窗口内的文件，保证重启后搜索仍覆盖磁盘上的证据。
func (s *TraceStore) loadRecent() error {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read trace root: %w", err)
	}
	cutoff := traceDay(time.Now().Add(-s.retain))
	var loaded []TraceRecord
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		day := strings.TrimSuffix(entry.Name(), ".jsonl")
		if day < cutoff {
			continue
		}
		file, err := os.Open(filepath.Join(s.root, entry.Name()))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
		for scanner.Scan() {
			var record TraceRecord
			if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
				continue
			}
			loaded = append(loaded, record)
		}
		_ = file.Close()
	}
	sort.Slice(loaded, func(i, j int) bool { return loaded[i].StartedAt.Before(loaded[j].StartedAt) })
	if len(loaded) > s.max {
		// Copy the kept tail so an oversized replay slice is released to GC immediately.
		loaded = slices.Clone(loaded[len(loaded)-s.max:])
	}
	s.records = loaded
	s.cleanImagePreviewsForRecords()
	for _, record := range loaded {
		if record.ID > s.nextID {
			s.nextID = record.ID
		}
	}
	return nil
}

func (s *TraceStore) cleanupExpired() {
	if s == nil || s.mode != TraceModeDisk || s.root == "" {
		return
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return
	}
	cutoff := traceDay(time.Now().Add(-s.retain))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		if strings.TrimSuffix(entry.Name(), ".jsonl") < cutoff {
			_ = os.Remove(filepath.Join(s.root, entry.Name()))
		}
	}
	s.cleanFullFiles(cutoff)
	s.cleanImagePreviews(cutoff)
	s.invalidateDiskBytes()
}

func traceDay(t time.Time) string { return t.Format("2006-01-02") }
