package observability

import (
	"encoding/json"
	"log/slog"
	"math"
	"os"
	"runtime"
	"runtime/metrics"
	"sort"
	"sync"
	"time"

	"github.com/uvwt/agentdock/internal/fs/atomicfile"
)

const DefaultRecentCapacity = 500

// WindowMinutes 是统计窗口长度（分钟）。窗口调用数与工具聚合都按这个
// 长度滚动统计：窗口内发生过多少次调用就是多少次，不再被最近调用环的
// 固定容量截断。该值通过快照的 window_minutes 下发，控制台不再复制常量。
const WindowMinutes = 5

// windowDuration 是统计窗口长度。
const windowDuration = time.Duration(WindowMinutes) * time.Minute

// windowMaxRecords 限制内存中保留的调用统计明细，超限时先淘汰最旧记录。
const windowMaxRecords = 20000

// ExecutionRecord 是运行分析保存的最小工具调用元数据。
// 这里有意不包含参数、结果、错误正文或路径等 Payload。
type ExecutionRecord struct {
	ID            uint64        `json:"id"`
	Tool          string        `json:"tool"`
	Source        Source        `json:"source"`
	UserAgent     string        `json:"user_agent,omitempty"`
	TaskID        string        `json:"task_id,omitempty"`
	TraceID       string        `json:"trace_id,omitempty"`
	SpanID        string        `json:"span_id,omitempty"`
	StartedAt     time.Time     `json:"started_at"`
	DurationMS    float64       `json:"duration_ms"`
	Success       bool          `json:"success"`
	ErrorCode     string        `json:"error_code,omitempty"`
	ErrorCategory string        `json:"error_category,omitempty"`
	Stages        []StageRecord `json:"stages,omitempty"`
	// Insertions/Deletions 是本次调用造成的代码行数变动，只有文件编辑类调用非零。
	Insertions int `json:"insertions,omitempty"`
	Deletions  int `json:"deletions,omitempty"`
	// InputTokens/OutputTokens 是入参与结果序列化文本的 token 数（估算口径）。
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
}

type ToolStats struct {
	Tool          string  `json:"tool"`
	Count         int     `json:"count"`
	ErrorCount    int     `json:"error_count"`
	ErrorRate     float64 `json:"error_rate"`
	P50DurationMS float64 `json:"p50_duration_ms"`
	P95DurationMS float64 `json:"p95_duration_ms"`
	P99DurationMS float64 `json:"p99_duration_ms"`
	// Insertions/Deletions 是该工具在窗口内造成的代码行数变动。
	Insertions int `json:"insertions"`
	Deletions  int `json:"deletions"`
	// InputTokens/OutputTokens 是该工具在窗口内的入参与结果 token 数。
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type ProcessSnapshot struct {
	Goroutines     int    `json:"goroutines"`
	HeapAllocBytes uint64 `json:"heap_alloc_bytes"`
	HeapInuseBytes uint64 `json:"heap_inuse_bytes"`
	HeapSysBytes   uint64 `json:"heap_sys_bytes"`
	GCCycles       uint32 `json:"gc_cycles"`
	UptimeMS       int64  `json:"uptime_ms"`
}

type Snapshot struct {
	StartedAt time.Time `json:"started_at"`
	// GeneratedAt 是本快照的计算时间。它与 StartedAt（进程启动时间）和窗口
	// 起点都不同，控制台不能拿页面刷新时间冒充它。
	GeneratedAt time.Time `json:"generated_at"`
	// RecentCapacity 是最近调用环的容量：它只约束「最近调用流水」的条数。
	RecentCapacity int `json:"recent_capacity"`
	// WindowMinutes 是窗口调用数与工具聚合使用的滚动窗口长度。
	WindowMinutes int `json:"window_minutes"`
	// WindowHistoryMinutes 是窗口明细环保留的历史长度（24 小时）。
	WindowHistoryMinutes int `json:"window_history_minutes"`
	// WindowCapacity 是窗口明细环的条数上限；超限时淘汰最旧记录。
	WindowCapacity int `json:"window_capacity"`
	// WindowLimited 表示当前窗口内已因明细容量上限丢弃过记录，窗口统计可能偏低。
	WindowLimited bool `json:"window_limited"`
	// WindowCalls 是统计窗口（WindowMinutes）内完成的调用次数。
	WindowCalls     int    `json:"window_calls"`
	TotalCalls      uint64 `json:"total_calls"`
	TotalErrors     uint64 `json:"total_errors"`
	ActiveCalls     int    `json:"active_calls"`
	TotalInsertions uint64 `json:"total_insertions"`
	TotalDeletions  uint64 `json:"total_deletions"`
	// TotalInputTokens/TotalOutputTokens 是进程启动以来的 token 累计值。
	TotalInputTokens  uint64 `json:"total_input_tokens"`
	TotalOutputTokens uint64 `json:"total_output_tokens"`
	// TokenWindowSeconds 是 token 累计段长度（30 分钟）。段按钟表半点对齐，
	// 段内持续累加，跨段清零重计，不做滚动淘汰。
	TokenWindowSeconds int64 `json:"token_window_seconds"`
	// TokenSegmentStartedAt 是当前累计段的起点（UTC RFC3339）。
	TokenSegmentStartedAt time.Time `json:"token_segment_started_at"`
	// WindowInputTokens/WindowOutputTokens 是当前累计段内的 token 值。
	WindowInputTokens  uint64 `json:"window_input_tokens"`
	WindowOutputTokens uint64 `json:"window_output_tokens"`
	// DailyTokenRetentionDays 是自然日 token 桶的保留天数。
	DailyTokenRetentionDays int `json:"daily_token_retention_days"`
	// DailyTokens 是按本地日期分桶的 token 累计，最近的在前。
	DailyTokens []DailyTokens     `json:"daily_tokens"`
	ToolStats   []ToolStats       `json:"tool_stats"`
	RecentCalls []ExecutionRecord `json:"recent_calls"`
	Process     ProcessSnapshot   `json:"process"`
	// TotalsRestored 表示累计值是否从本地状态文件恢复（包含此前进程的累计）。
	TotalsRestored bool `json:"totals_restored"`
}

// DailyTokens 是一个自然日（本地时区）的 token 累计值。
type DailyTokens struct {
	Date         string `json:"date"`
	InputTokens  uint64 `json:"input_tokens"`
	OutputTokens uint64 `json:"output_tokens"`
}

// CallMeta 是一次工具调用的白名单元数据。
// 用结构体而不是位置参数：字段已经超过十个，再追加代码变动统计只会更难读。
type CallMeta struct {
	Tool          string
	Source        Source
	StartedAt     time.Time
	Duration      time.Duration
	Success       bool
	ErrorCode     string
	ErrorCategory string
	UserAgent     string
	TaskID        string
	TraceID       string
	SpanID        string
	Stages        []StageRecord
	// Insertions/Deletions 是本次调用造成的代码行数变动，只有文件编辑类调用非零。
	Insertions int
	Deletions  int
	// InputTokens/OutputTokens 是入参与结果序列化文本的 token 数（估算口径）。
	InputTokens  int
	OutputTokens int
}

// TokenSegmentMinutes 是 token 累计段长度：一次调用只计数一次，段按钟表
// 半点对齐，段内持续累加，跨段清零重计。
const TokenSegmentMinutes = 30

// tokenSegmentSeconds 是累计段长度（秒）。
const tokenSegmentSeconds = TokenSegmentMinutes * 60

// dailyTokenRetentionDays 是按日 token 统计的保留天数，超期桶在写入路径淘汰。
const dailyTokenRetentionDays = 30

// dailyBucket 是一个自然日的 token 累加桶。字段导出是为了 JSON 落盘恢复。
type dailyBucket struct {
	Input  uint64 `json:"input_tokens"`
	Output uint64 `json:"output_tokens"`
}

// windowStat 和最近调用环共享不可变的白名单元数据。
type windowStat struct {
	record      *ExecutionRecord
	completedAt time.Time
}

type Recorder struct {
	mu sync.RWMutex

	startedAt time.Time
	records   []*ExecutionRecord
	capacity  int
	next      int
	size      int

	// window 按完成时刻升序。旧分析和告警只读取尾部的 5 分钟。
	window          []windowStat
	windowDroppedAt time.Time

	nextID      uint64
	totalCalls  uint64
	totalErrors uint64
	activeCalls int
	// totalInsertions/totalDeletions 是进程启动以来的代码行数变动累计值。
	totalInsertions uint64
	totalDeletions  uint64
	// totalInputTokens/totalOutputTokens 是进程启动以来的 token 累计值。
	totalInputTokens  uint64
	totalOutputTokens uint64
	// segmentIndex 是当前累计段编号；segmentInput/segmentOutput 是段内累计。
	segmentIndex  int64
	segmentInput  uint64
	segmentOutput uint64
	// dailyTokens 按本地日期累加 token，超期桶在写入路径淘汰。
	dailyTokens map[string]dailyBucket
	// statePath 是累计统计的落盘路径；空字符串表示不持久化（测试）。
	statePath string
	// totalsRestored 表示启动时成功读取了状态文件，累计值包含此前进程的历史。
	totalsRestored bool
	// persistPending 表示有未落盘的累计变化；lastPersist 用于落盘节流。
	persistPending bool
	lastPersist    time.Time
}

// persistMinInterval 是累计统计的落盘节流间隔：原子写含 fsync，每次工具
// 调用都写会让高频调用都排在磁盘后面；最多丢最后一个间隔内的增量。
const persistMinInterval = 5 * time.Second

// persistedStats 是重启后需要恢复的累计统计。最近调用环与 tool_stats
// 是窗口视图，不持久化。
type persistedStats struct {
	TotalCalls        uint64                 `json:"total_calls"`
	TotalErrors       uint64                 `json:"total_errors"`
	TotalInsertions   uint64                 `json:"total_insertions"`
	TotalDeletions    uint64                 `json:"total_deletions"`
	TotalInputTokens  uint64                 `json:"total_input_tokens"`
	TotalOutputTokens uint64                 `json:"total_output_tokens"`
	DailyTokens       map[string]dailyBucket `json:"daily_tokens"`
	// SegmentIndex/SegmentInput/SegmentOutput 是当前累计段，仅未跨段时可恢复。
	SegmentIndex  int64  `json:"segment_index"`
	SegmentInput  uint64 `json:"segment_input_tokens"`
	SegmentOutput uint64 `json:"segment_output_tokens"`
}

// NewRecorder 创建记录器。statePath 非空时从该文件恢复历史累计，并在计数
// 变化后节流落盘（节流窗口内最多丢 5s 增量，Flush/优雅退出会补齐）；
// 读写失败只降级为不持久化，统计本身不受影响。
func NewRecorder(capacity int, statePath string) *Recorder {
	if capacity <= 0 {
		capacity = DefaultRecentCapacity
	}
	recorder := &Recorder{
		startedAt:   time.Now(),
		records:     make([]*ExecutionRecord, capacity),
		capacity:    capacity,
		dailyTokens: make(map[string]dailyBucket),
		statePath:   statePath,
	}
	recorder.loadPersistedState()
	return recorder
}

func (r *Recorder) BeginTool() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.activeCalls++
	r.mu.Unlock()
}

// EndTool 记录一次工具调用并返回分配的记录 ID（未记录时为 0）。
// 返回值供事件流把 call 事件与 analytics 记录对齐。
func (r *Recorder) EndTool(meta CallMeta) uint64 {
	if r == nil {
		return 0
	}
	startedAt := meta.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	duration := meta.Duration
	if duration < 0 {
		duration = 0
	}

	r.mu.Lock()
	var persistPayload []byte
	defer func() {
		r.mu.Unlock()
		// 落盘在解锁后执行：原子写含 fsync，持锁写会阻塞并发的 BeginTool
		// 与快照读，而载荷已在锁内拷贝完毕。
		r.writeState(persistPayload)
	}()

	if r.activeCalls > 0 {
		r.activeCalls--
	}
	if r.capacity <= 0 || len(r.records) == 0 {
		return 0
	}

	r.nextID++
	r.totalCalls++
	if !meta.Success {
		r.totalErrors++
	}
	r.totalInsertions += uint64(maxInt(meta.Insertions, 0))
	r.totalDeletions += uint64(maxInt(meta.Deletions, 0))
	r.totalInputTokens += uint64(maxInt(meta.InputTokens, 0))
	r.totalOutputTokens += uint64(maxInt(meta.OutputTokens, 0))
	r.addTokenUsageLocked(meta.InputTokens, meta.OutputTokens)

	record := &ExecutionRecord{
		ID:            r.nextID,
		Tool:          meta.Tool,
		Source:        normalizedSource(meta.Source),
		UserAgent:     normalizeUserAgent(meta.UserAgent),
		TaskID:        normalizeTaskID(meta.TaskID),
		StartedAt:     startedAt.UTC(),
		DurationMS:    durationMilliseconds(duration),
		Success:       meta.Success,
		ErrorCode:     meta.ErrorCode,
		ErrorCategory: meta.ErrorCategory,
		TraceID:       meta.TraceID,
		SpanID:        meta.SpanID,
		Stages:        append([]StageRecord(nil), meta.Stages...),
		Insertions:    maxInt(meta.Insertions, 0),
		Deletions:     maxInt(meta.Deletions, 0),
		InputTokens:   maxInt(meta.InputTokens, 0),
		OutputTokens:  maxInt(meta.OutputTokens, 0),
	}
	r.records[r.next] = record
	r.next = (r.next + 1) % r.capacity
	if r.size < r.capacity {
		r.size++
	}
	r.appendWindowLocked(record)
	persistPayload = r.schedulePersistLocked()
	return record.ID
}

// appendWindowLocked 以记账时间计入窗口，与 Token 的完工记账口径一致。
func (r *Recorder) appendWindowLocked(record *ExecutionRecord) {
	now := time.Now()
	r.window = append(r.window, windowStat{record: record, completedAt: now})
	r.pruneWindowLocked(now)
}

func (r *Recorder) pruneWindowLocked(now time.Time) {
	expired := len(r.window) - len(r.windowSinceLocked(now.Add(-callHistoryDuration)))
	if overflow := len(r.window) - expired - windowMaxRecords; overflow > 0 {
		expired += overflow
		r.windowDroppedAt = r.window[expired-1].completedAt
	}
	// 从队首推进，避免高负载下每淘汰一条都搬移整个队列。
	clear(r.window[:expired])
	r.window = r.window[expired:]
}

// windowSinceLocked 利用完成顺序查找范围起点。调用方必须持有锁。
func (r *Recorder) windowSinceLocked(from time.Time) []windowStat {
	first := sort.Search(len(r.window), func(i int) bool {
		return !r.window[i].completedAt.Before(from)
	})
	return r.window[first:]
}

// schedulePersistLocked 标记未落盘变化，并在距上次落盘超过节流间隔时
// 返回本次要写的载荷。
func (r *Recorder) schedulePersistLocked() []byte {
	if r.statePath == "" {
		return nil
	}
	r.persistPending = true
	now := time.Now()
	if !r.lastPersist.IsZero() && now.Sub(r.lastPersist) < persistMinInterval {
		return nil
	}
	r.lastPersist = now
	r.persistPending = false
	return r.marshalStateLocked()
}

// WindowErrorStats 返回统计窗口内的调用数与失败数。告警规则用它算窗口
// 错误率，口径与 analytics 页的「窗口调用」一致。
func (r *Recorder) WindowErrorStats() (calls int, errors int) {
	if r == nil {
		return 0, 0
	}
	r.mu.Lock()
	now := time.Now()
	r.pruneWindowLocked(now)
	for _, stat := range r.windowSinceLocked(now.Add(-windowDuration)) {
		calls++
		if !stat.record.Success {
			errors++
		}
	}
	r.mu.Unlock()
	return calls, errors
}

// RecentCalls 返回当前内存环里最近调用的独立副本，不计算百分位或进程指标。
func (r *Recorder) RecentCalls() []ExecutionRecord {
	if r == nil {
		return []ExecutionRecord{}
	}
	r.mu.RLock()
	recent := r.recentLocked()
	r.mu.RUnlock()
	return cloneRecentStages(recent)
}

func (r *Recorder) Snapshot() Snapshot {
	if r == nil {
		return Snapshot{}
	}

	r.mu.Lock()
	now := time.Now()
	r.pruneWindowLocked(now)
	windowStart := now.Add(-windowDuration)
	startedAt := r.startedAt
	capacity := r.capacity
	totalCalls := r.totalCalls
	totalErrors := r.totalErrors
	activeCalls := r.activeCalls
	totalInsertions := r.totalInsertions
	totalDeletions := r.totalDeletions
	totalInputTokens := r.totalInputTokens
	totalOutputTokens := r.totalOutputTokens
	windowInputTokens, windowOutputTokens, segmentStartedAt := r.tokenSegmentLocked()
	dailyTokens := r.dailyTokensLocked()
	recent := r.recentLocked()
	windowDroppedAt := r.windowDroppedAt
	totalsRestored := r.totalsRestored
	// 窗口切片在解锁后仍会被写入路径追加，必须拷贝后再用。
	window := append([]windowStat(nil), r.windowSinceLocked(windowStart)...)
	r.mu.Unlock()

	heapAlloc, heapInuse, heapSys, gcCycles := readProcessMemory()

	return Snapshot{
		StartedAt:               startedAt.UTC(),
		GeneratedAt:             now.UTC(),
		RecentCapacity:          capacity,
		WindowMinutes:           WindowMinutes,
		WindowHistoryMinutes:    int(callHistoryDuration / time.Minute),
		WindowCapacity:          windowMaxRecords,
		WindowLimited:           !windowDroppedAt.IsZero() && !windowDroppedAt.Before(windowStart),
		WindowCalls:             len(window),
		TotalCalls:              totalCalls,
		TotalErrors:             totalErrors,
		ActiveCalls:             activeCalls,
		TotalInsertions:         totalInsertions,
		TotalDeletions:          totalDeletions,
		TotalInputTokens:        totalInputTokens,
		TotalOutputTokens:       totalOutputTokens,
		TokenWindowSeconds:      tokenSegmentSeconds,
		TokenSegmentStartedAt:   segmentStartedAt,
		WindowInputTokens:       windowInputTokens,
		WindowOutputTokens:      windowOutputTokens,
		DailyTokenRetentionDays: dailyTokenRetentionDays,
		DailyTokens:             dailyTokens,
		ToolStats:               aggregateToolStats(window),
		RecentCalls:             cloneRecentStages(recent),
		TotalsRestored:          totalsRestored,
		Process: ProcessSnapshot{
			Goroutines:     runtime.NumGoroutine(),
			HeapAllocBytes: heapAlloc,
			HeapInuseBytes: heapInuse,
			HeapSysBytes:   heapSys,
			GCCycles:       gcCycles,
			UptimeMS:       maxInt64(0, time.Since(startedAt).Milliseconds()),
		},
	}
}

func readProcessMemory() (heapAlloc, heapInuse, heapSys uint64, gcCycles uint32) {
	// Read runtime/metrics instead of runtime.ReadMemStats so analytics polling does
	// not trigger stop-the-world pauses across the Go runtime.
	samples := [5]metrics.Sample{
		{Name: "/memory/classes/heap/objects:bytes"},
		{Name: "/memory/classes/heap/unused:bytes"},
		{Name: "/memory/classes/heap/free:bytes"},
		{Name: "/memory/classes/heap/released:bytes"},
		{Name: "/gc/cycles/total:gc-cycles"},
	}
	metrics.Read(samples[:])
	for _, sample := range samples {
		if sample.Value.Kind() != metrics.KindUint64 {
			var memory runtime.MemStats
			runtime.ReadMemStats(&memory)
			return memory.HeapAlloc, memory.HeapInuse, memory.HeapSys, memory.NumGC
		}
	}
	objects := samples[0].Value.Uint64()
	unused := samples[1].Value.Uint64()
	free := samples[2].Value.Uint64()
	released := samples[3].Value.Uint64()
	heapAlloc = objects
	heapInuse = objects + unused
	heapSys = heapInuse + free + released
	gcCycles = uint32(samples[4].Value.Uint64())
	return heapAlloc, heapInuse, heapSys, gcCycles
}

// addTokenUsageLocked 把本次调用的 token 累加进当前累计段和当日桶，然后
// 淘汰过期的日桶。段/日键取记账时刻而不是调用起始时刻：跨段的长调用
// 在结束时计入当前段。
func (r *Recorder) addTokenUsageLocked(input, output int) {
	now := time.Now()
	segment := now.Unix() / tokenSegmentSeconds
	if segment != r.segmentIndex {
		r.segmentIndex = segment
		r.segmentInput = 0
		r.segmentOutput = 0
	}
	in := uint64(maxInt(input, 0))
	out := uint64(maxInt(output, 0))
	r.segmentInput += in
	r.segmentOutput += out
	day := now.Format("2006-01-02")
	bucket := r.dailyTokens[day]
	bucket.Input += in
	bucket.Output += out
	r.dailyTokens[day] = bucket
	cutoff := now.AddDate(0, 0, -dailyTokenRetentionDays).Format("2006-01-02")
	for key := range r.dailyTokens {
		if key < cutoff {
			delete(r.dailyTokens, key)
		}
	}
}

// tokenSegmentLocked 汇总当前累计段的 token 值。段已跨过时（自上次写入
// 后进入了新段）返回零值，段起点是新段的开始。
func (r *Recorder) tokenSegmentLocked() (input, output uint64, startedAt time.Time) {
	now := time.Now()
	segment := now.Unix() / tokenSegmentSeconds
	startedAt = time.Unix(segment*tokenSegmentSeconds, 0).UTC()
	if segment != r.segmentIndex {
		return 0, 0, startedAt
	}
	return r.segmentInput, r.segmentOutput, startedAt
}

// dailyTokensLocked 返回按日 token 累计的副本，最近的在前。
func (r *Recorder) dailyTokensLocked() []DailyTokens {
	if len(r.dailyTokens) == 0 {
		return []DailyTokens{}
	}
	dates := make([]string, 0, len(r.dailyTokens))
	for date := range r.dailyTokens {
		dates = append(dates, date)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))
	daily := make([]DailyTokens, 0, len(dates))
	for _, date := range dates {
		bucket := r.dailyTokens[date]
		daily = append(daily, DailyTokens{
			Date:         date,
			InputTokens:  bucket.Input,
			OutputTokens: bucket.Output,
		})
	}
	return daily
}

// loadPersistedState 恢复重启前的累计统计。文件缺失时静默跳过；损坏时
// 放弃恢复但不阻断 recorder 创建——统计落盘是尽力而为，不能拖垮 Core 启动。
func (r *Recorder) loadPersistedState() {
	if r.statePath == "" {
		return
	}
	encoded, err := os.ReadFile(r.statePath)
	if err != nil {
		return
	}
	var state persistedStats
	if err := json.Unmarshal(encoded, &state); err != nil {
		slog.Warn("observability stats state is corrupted, starting fresh", "path", r.statePath, "error", err)
		return
	}
	r.totalsRestored = true
	r.totalCalls = state.TotalCalls
	r.totalErrors = state.TotalErrors
	r.totalInsertions = state.TotalInsertions
	r.totalDeletions = state.TotalDeletions
	r.totalInputTokens = state.TotalInputTokens
	r.totalOutputTokens = state.TotalOutputTokens
	if state.DailyTokens != nil {
		r.dailyTokens = state.DailyTokens
	}
	// 当前段的累计只有落在未过期的段里才恢复；跨重启的旧段直接作废。
	segment := time.Now().Unix() / tokenSegmentSeconds
	if state.SegmentIndex == segment {
		r.segmentIndex = state.SegmentIndex
		r.segmentInput = state.SegmentInput
		r.segmentOutput = state.SegmentOutput
	}
}

// Flush 立即落盘未持久化的累计统计，供 Core 优雅退出前收尾，保证正常
// 重启不丢数据。
func (r *Recorder) Flush() {
	if r == nil {
		return
	}
	r.mu.Lock()
	var payload []byte
	if r.statePath != "" && r.persistPending {
		payload = r.marshalStateLocked()
		r.persistPending = false
		r.lastPersist = time.Now()
	}
	r.mu.Unlock()
	r.writeState(payload)
}

// marshalStateLocked 序列化当前累计统计。调用方必须持有锁。
func (r *Recorder) marshalStateLocked() []byte {
	state := persistedStats{
		TotalCalls:        r.totalCalls,
		TotalErrors:       r.totalErrors,
		TotalInsertions:   r.totalInsertions,
		TotalDeletions:    r.totalDeletions,
		TotalInputTokens:  r.totalInputTokens,
		TotalOutputTokens: r.totalOutputTokens,
		DailyTokens:       r.dailyTokens,
		SegmentIndex:      r.segmentIndex,
		SegmentInput:      r.segmentInput,
		SegmentOutput:     r.segmentOutput,
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		slog.Warn("encode observability stats state", "error", err)
		return nil
	}
	return encoded
}

// writeState 原子落盘累计统计。写入失败只记日志：统计是度量，
// 不能反过来让工具调用失败。
func (r *Recorder) writeState(payload []byte) {
	if payload == nil || r.statePath == "" {
		return
	}
	if err := atomicfile.Write(r.statePath, payload, 0o600); err != nil {
		slog.Warn("persist observability stats state", "path", r.statePath, "error", err)
	}
}

func (r *Recorder) recentLocked() []ExecutionRecord {
	if r.size == 0 {
		return []ExecutionRecord{}
	}
	recent := make([]ExecutionRecord, 0, r.size)
	for offset := 0; offset < r.size; offset++ {
		index := (r.next - 1 - offset + r.capacity) % r.capacity
		recent = append(recent, *r.records[index])
	}
	return recent
}

func cloneRecentStages(recent []ExecutionRecord) []ExecutionRecord {
	for i := range recent {
		recent[i].Stages = append([]StageRecord(nil), recent[i].Stages...)
	}
	return recent
}

func aggregateToolStats(stats []windowStat) []ToolStats {
	type accumulator struct {
		count        int
		errorCount   int
		durations    []float64
		insertions   int
		deletions    int
		inputTokens  int
		outputTokens int
	}
	byTool := make(map[string]*accumulator)
	for _, stat := range stats {
		record := stat.record
		current := byTool[record.Tool]
		if current == nil {
			current = &accumulator{}
			byTool[record.Tool] = current
		}
		current.count++
		if !record.Success {
			current.errorCount++
		}
		current.durations = append(current.durations, record.DurationMS)
		current.insertions += record.Insertions
		current.deletions += record.Deletions
		current.inputTokens += record.InputTokens
		current.outputTokens += record.OutputTokens
	}

	toolStats := make([]ToolStats, 0, len(byTool))
	for tool, current := range byTool {
		sort.Float64s(current.durations)
		toolStats = append(toolStats, ToolStats{
			Tool:          tool,
			Count:         current.count,
			ErrorCount:    current.errorCount,
			ErrorRate:     float64(current.errorCount) / float64(current.count),
			P50DurationMS: percentile(current.durations, 0.50),
			P95DurationMS: percentile(current.durations, 0.95),
			P99DurationMS: percentile(current.durations, 0.99),
			Insertions:    current.insertions,
			Deletions:     current.deletions,
			InputTokens:   current.inputTokens,
			OutputTokens:  current.outputTokens,
		})
	}
	sort.Slice(toolStats, func(i, j int) bool {
		if toolStats[i].Count != toolStats[j].Count {
			return toolStats[i].Count > toolStats[j].Count
		}
		return toolStats[i].Tool < toolStats[j].Tool
	})
	return toolStats
}

// percentile 使用 nearest-rank 定义：P95 表示至少 95% 的样本不大于该值。
func percentile(sorted []float64, quantile float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if quantile <= 0 {
		return sorted[0]
	}
	if quantile >= 1 {
		return sorted[len(sorted)-1]
	}
	index := int(math.Ceil(quantile*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func durationMilliseconds(duration time.Duration) float64 {
	value := float64(duration) / float64(time.Millisecond)
	return math.Round(value*1000) / 1000
}

func normalizedSource(source Source) Source {
	switch source {
	case SourceMCP, SourceNexus, SourceInternal:
		return source
	default:
		return SourceInternal
	}
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func maxInt(value, floor int) int {
	if value > floor {
		return value
	}
	return floor
}
