package observability

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

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
	fullFile         *os.File
	fullWriter       *bufio.Writer
	fullDay          string
	closed           bool
	stop             chan struct{}
	done             chan struct{}
	closeOne         sync.Once
	closeErr         error

	flushEvery time.Duration
	bufferSize int
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
	Arguments     string    `json:"arguments,omitempty"`
	Result        string    `json:"result,omitempty"`
	Error         string    `json:"error,omitempty"`
	Truncated     bool      `json:"truncated,omitempty"`
	// Command 只在命令类工具（exec_command 等）非空：它把命令、cwd 与输出
	// 拆成有上限的独立字段，UI 才能分段渲染并标出到底哪一侧被截断。
	Command *CommandTrace `json:"command,omitempty"`
	// FileChanges 只在 file_edit 成功时非空；关闭 trace 时不产生。
	FileChanges []FileChange `json:"file_changes,omitempty"`
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
		mode:       options.Mode,
		max:        options.MaxRecords,
		bufferSize: options.BufferSize,
		flushEvery: options.FlushInterval,
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
	entries, err := os.ReadDir(s.root)
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
	// 全文旁路同样是磁盘占用，告警口径不能漏掉它。
	fullEntries, err := os.ReadDir(s.fullDir())
	if err != nil {
		return total
	}
	for _, entry := range fullEntries {
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
func (s *TraceStore) Record(record TraceRecord) {
	if s == nil {
		return
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
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.nextID++
	record.ID = s.nextID
	s.records = append(s.records, record)
	if len(s.records) > s.max {
		s.records = append([]TraceRecord(nil), s.records[len(s.records)-s.max:]...)
	}
	if s.mode != TraceModeDisk {
		return
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
	out = traceJSONSecretPattern.ReplaceAllStringFunc(out, traceJSONSecretReplacement)
	return traceJSONOpenSecretPattern.ReplaceAllStringFunc(out, traceJSONSecretReplacement)
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
	limit := query.Limit
	if limit <= 0 {
		limit = traceDefaultLimit
	}
	if limit > traceQueryLimit {
		limit = traceQueryLimit
	}
	return s.filterRecords(query, limit, true)
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
	s.pending = nil
	s.pendingFull = nil
	s.pendingFullBytes = 0
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

// traceSummaryRecord 去掉正文只保留摘要。这里复制 Command 而不是原地修改：
// 记录本身仍在保留窗口里，摘要投影不能破坏后续详情读取。
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
	return record
}

func traceMatches(record TraceRecord, needle string) bool {
	fields := []string{record.Tool, string(record.Source), record.UserAgent, record.TaskID, record.ErrorCode, record.Error, record.Arguments, record.Result, record.TraceID}
	if record.Command != nil {
		fields = append(fields, record.Command.searchFields()...)
	}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), needle) {
			return true
		}
	}
	return false
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
			if needle != "" && !strings.Contains(strings.ToLower(change.Path+"\n"+change.MoveTo+"\n"+change.Action+"\n"+change.Diff), needle) {
				continue
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
		loaded = loaded[len(loaded)-s.max:]
	}
	s.records = loaded
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
}

func traceDay(t time.Time) string { return t.Format("2006-01-02") }
