package observability

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRecorderKeepsBoundedNewestFirstHistory(t *testing.T) {
	recorder := NewRecorder(3, "")
	for index := 1; index <= 5; index++ {
		recorder.BeginTool()
		recorder.EndTool(CallMeta{
			Tool:      "tool",
			Source:    SourceMCP,
			StartedAt: time.Unix(int64(index), 0),
			Duration:  time.Duration(index) * time.Millisecond,
			Success:   true,
		})
	}

	snapshot := recorder.Snapshot()
	if snapshot.RecentCapacity != 3 || len(snapshot.RecentCalls) != 3 {
		t.Fatalf("snapshot capacity/recent = %d/%d, want 3/3", snapshot.RecentCapacity, len(snapshot.RecentCalls))
	}
	// 窗口调用数按时间窗口统计，不受最近调用环的容量限制。
	if snapshot.WindowCalls != 5 {
		t.Fatalf("snapshot window = %d, want 5", snapshot.WindowCalls)
	}
	if snapshot.TotalCalls != 5 || snapshot.TotalErrors != 0 || snapshot.ActiveCalls != 0 {
		t.Fatalf("snapshot totals = calls %d errors %d active %d", snapshot.TotalCalls, snapshot.TotalErrors, snapshot.ActiveCalls)
	}
	wantIDs := []uint64{5, 4, 3}
	for index, wantID := range wantIDs {
		if got := snapshot.RecentCalls[index].ID; got != wantID {
			t.Fatalf("recent call %d ID = %d, want %d", index, got, wantID)
		}
	}
}

func TestRecorderAggregatesNearestRankPercentilesAndErrors(t *testing.T) {
	recorder := NewRecorder(200, "")
	for index := 1; index <= 100; index++ {
		recorder.BeginTool()
		success := index != 100
		code := ""
		category := ""
		if !success {
			code = "TIMEOUT"
			category = "runtime"
		}
		recorder.EndTool(CallMeta{
			Tool:          "exec_command",
			Source:        SourceNexus,
			StartedAt:     time.Now(),
			Duration:      time.Duration(index) * time.Millisecond,
			Success:       success,
			ErrorCode:     code,
			ErrorCategory: category,
		})
	}

	snapshot := recorder.Snapshot()
	if len(snapshot.ToolStats) != 1 {
		t.Fatalf("tool stats count = %d, want 1", len(snapshot.ToolStats))
	}
	stats := snapshot.ToolStats[0]
	if stats.Count != 100 || stats.ErrorCount != 1 || stats.ErrorRate != 0.01 {
		t.Fatalf("stats counts/rate = %d/%d/%f", stats.Count, stats.ErrorCount, stats.ErrorRate)
	}
	if stats.P50DurationMS != 50 || stats.P95DurationMS != 95 || stats.P99DurationMS != 99 {
		t.Fatalf("percentiles = p50 %.3f p95 %.3f p99 %.3f", stats.P50DurationMS, stats.P95DurationMS, stats.P99DurationMS)
	}
	if snapshot.RecentCalls[0].ErrorCode != "TIMEOUT" || snapshot.RecentCalls[0].ErrorCategory != "runtime" {
		t.Fatalf("latest error metadata = %#v", snapshot.RecentCalls[0])
	}
}

func TestRecorderZeroValueDoesNotPanicOrLeakActiveCount(t *testing.T) {
	recorder := &Recorder{}
	recorder.BeginTool()
	recorder.EndTool(CallMeta{
		Tool:      "tool",
		Source:    SourceInternal,
		StartedAt: time.Now(),
		Duration:  time.Millisecond,
		Success:   true,
	})

	snapshot := recorder.Snapshot()
	if snapshot.ActiveCalls != 0 {
		t.Fatalf("active calls = %d, want 0", snapshot.ActiveCalls)
	}
}

func TestRecorderConcurrentWritesStayConsistent(t *testing.T) {
	recorder := NewRecorder(500, "")
	const writers = 16
	const callsPerWriter = 100

	var wait sync.WaitGroup
	wait.Add(writers)
	for writer := 0; writer < writers; writer++ {
		go func() {
			defer wait.Done()
			for call := 0; call < callsPerWriter; call++ {
				recorder.BeginTool()
				recorder.EndTool(CallMeta{
					Tool:      "tool",
					Source:    SourceMCP,
					StartedAt: time.Now(),
					Duration:  time.Millisecond,
					Success:   true,
				})
			}
		}()
	}
	wait.Wait()

	snapshot := recorder.Snapshot()
	wantTotal := uint64(writers * callsPerWriter)
	if snapshot.TotalCalls != wantTotal {
		t.Fatalf("total calls = %d, want %d", snapshot.TotalCalls, wantTotal)
	}
	if snapshot.WindowCalls != int(wantTotal) || snapshot.ActiveCalls != 0 {
		t.Fatalf("window/active = %d/%d, want %d/0", snapshot.WindowCalls, snapshot.ActiveCalls, wantTotal)
	}
}

// 窗口统计不被最近调用环的容量截断：环只有 3 条时，窗口仍要看到全部调用。
func TestRecorderWindowAggregatesBeyondRingCapacity(t *testing.T) {
	recorder := NewRecorder(3, "")
	for index := 0; index < 5; index++ {
		recorder.BeginTool()
		recorder.EndTool(CallMeta{
			Tool:      "exec_command",
			Source:    SourceMCP,
			StartedAt: time.Now(),
			Duration:  time.Duration(index+1) * time.Millisecond,
			Success:   true,
		})
	}

	snapshot := recorder.Snapshot()
	if snapshot.WindowCalls != 5 {
		t.Fatalf("window calls = %d, want 5", snapshot.WindowCalls)
	}
	if len(snapshot.ToolStats) != 1 || snapshot.ToolStats[0].Count != 5 {
		t.Fatalf("tool stats = %#v, want exec_command count 5", snapshot.ToolStats)
	}
}

// 窗口按时间淘汰：超过窗口长度的调用不再计入窗口调用数与工具聚合。
func TestRecorderWindowDropsExpiredCalls(t *testing.T) {
	recorder := NewRecorder(500, "")
	for index := 0; index < 3; index++ {
		recorder.BeginTool()
		recorder.EndTool(CallMeta{
			Tool:      "read_file",
			Source:    SourceMCP,
			StartedAt: time.Now(),
			Duration:  time.Millisecond,
			Success:   true,
		})
	}

	// 把窗口里的条目推到窗口之外，再取一次快照。
	recorder.mu.Lock()
	for index := range recorder.window {
		recorder.window[index].completedAt = time.Now().Add(-2 * windowDuration)
	}
	recorder.mu.Unlock()

	snapshot := recorder.Snapshot()
	if snapshot.WindowCalls != 0 {
		t.Fatalf("window calls = %d, want 0", snapshot.WindowCalls)
	}
	if len(snapshot.ToolStats) != 0 {
		t.Fatalf("tool stats = %#v, want empty", snapshot.ToolStats)
	}
	if snapshot.TotalCalls != 3 {
		t.Fatalf("total calls = %d, want 3", snapshot.TotalCalls)
	}
}

func TestSnapshotJSONIsZeroPayload(t *testing.T) {
	recorder := NewRecorder(10, "")
	recorder.BeginTool()
	recorder.EndTool(CallMeta{
		Tool:          "read_file",
		Source:        SourceMCP,
		StartedAt:     time.Now(),
		Duration:      2 * time.Millisecond,
		Success:       false,
		ErrorCode:     "DENIED",
		ErrorCategory: "permission",
	})

	encoded, err := json.Marshal(recorder.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ToLower(string(encoded))
	for _, forbidden := range []string{"arguments", "result", "output", "details", "message", "command", "path"} {
		if strings.Contains(body, `"`+forbidden+`"`) {
			t.Fatalf("snapshot JSON contains forbidden payload field %q: %s", forbidden, body)
		}
	}
}

func TestSourceContextUsesClosedSet(t *testing.T) {
	if got := SourceFromContext(nil); got != SourceInternal {
		t.Fatalf("nil context source = %q", got)
	}
	if got := SourceFromContext(WithSource(nil, SourceNexus)); got != SourceNexus {
		t.Fatalf("nexus source = %q", got)
	}
	if got := SourceFromContext(WithSource(nil, Source("user-data"))); got != SourceInternal {
		t.Fatalf("unexpected source = %q", got)
	}
}

func TestRecorderAggregatesCodeLineChanges(t *testing.T) {
	recorder := NewRecorder(10, "")
	for _, change := range []CallMeta{
		{Tool: "file_edit", Source: SourceMCP, Insertions: 12, Deletions: 3},
		{Tool: "file_edit", Source: SourceMCP, Insertions: 5, Deletions: 1},
		{Tool: "read_file", Source: SourceMCP},
	} {
		change.StartedAt = time.Now()
		change.Duration = time.Millisecond
		change.Success = true
		recorder.BeginTool()
		recorder.EndTool(change)
	}

	snapshot := recorder.Snapshot()
	if snapshot.TotalInsertions != 17 || snapshot.TotalDeletions != 4 {
		t.Fatalf("totals = +%d -%d, want +17 -4", snapshot.TotalInsertions, snapshot.TotalDeletions)
	}
	if len(snapshot.ToolStats) != 2 {
		t.Fatalf("tool stats = %#v", snapshot.ToolStats)
	}
	// 按调用次数排序：file_edit(2) 在前，read_file(1) 在后。
	edit := snapshot.ToolStats[0]
	if edit.Tool != "file_edit" || edit.Insertions != 17 || edit.Deletions != 4 {
		t.Fatalf("file_edit stats = %#v", edit)
	}
	read := snapshot.ToolStats[1]
	if read.Tool != "read_file" || read.Insertions != 0 || read.Deletions != 0 {
		t.Fatalf("read_file stats = %#v", read)
	}
}

func TestRecorderAggregatesTokenUsage(t *testing.T) {
	recorder := NewRecorder(10, "")
	for _, call := range []CallMeta{
		{Tool: "read_file", Source: SourceMCP, InputTokens: 120, OutputTokens: 340},
		{Tool: "file_edit", Source: SourceMCP, InputTokens: 60, OutputTokens: 20},
	} {
		call.StartedAt = time.Now()
		call.Duration = time.Millisecond
		call.Success = true
		recorder.BeginTool()
		recorder.EndTool(call)
	}

	snapshot := recorder.Snapshot()
	if snapshot.TotalInputTokens != 180 || snapshot.TotalOutputTokens != 360 {
		t.Fatalf("token totals = in %d out %d, want 180/360", snapshot.TotalInputTokens, snapshot.TotalOutputTokens)
	}
	if snapshot.WindowInputTokens != 180 || snapshot.WindowOutputTokens != 360 {
		t.Fatalf("token window = in %d out %d, want 180/360", snapshot.WindowInputTokens, snapshot.WindowOutputTokens)
	}
	if snapshot.TokenWindowSeconds != TokenSegmentMinutes*60 {
		t.Fatalf("token window span = %ds, want %ds", snapshot.TokenWindowSeconds, TokenSegmentMinutes*60)
	}
	today := time.Now().Format("2006-01-02")
	if len(snapshot.DailyTokens) != 1 || snapshot.DailyTokens[0].Date != today ||
		snapshot.DailyTokens[0].InputTokens != 180 || snapshot.DailyTokens[0].OutputTokens != 360 {
		t.Fatalf("daily tokens = %#v, want %s 180/360", snapshot.DailyTokens, today)
	}
	if len(snapshot.ToolStats) != 2 {
		t.Fatalf("tool stats = %#v", snapshot.ToolStats)
	}
	// 按工具名查找：同调用次数时排序键是工具名，file_edit 在前。
	for _, stat := range snapshot.ToolStats {
		switch stat.Tool {
		case "read_file":
			if stat.InputTokens != 120 || stat.OutputTokens != 340 {
				t.Fatalf("read_file stats = %#v", stat)
			}
		case "file_edit":
			if stat.InputTokens != 60 || stat.OutputTokens != 20 {
				t.Fatalf("file_edit stats = %#v", stat)
			}
		}
	}
}

// 累计段按钟表半点对齐：段内持续累加，跨段清零重计，进程总量不丢。
func TestRecorderTokenSegmentResetsAcrossSegments(t *testing.T) {
	recorder := NewRecorder(10, "")
	recorder.BeginTool()
	recorder.EndTool(CallMeta{
		Tool: "read_file", Source: SourceMCP, StartedAt: time.Now(),
		Duration: time.Millisecond, Success: true, InputTokens: 10, OutputTokens: 20,
	})

	// 把当前段搬到上一段，模拟跨过半点边界。
	recorder.mu.Lock()
	recorder.segmentIndex--
	recorder.mu.Unlock()

	snapshot := recorder.Snapshot()
	if snapshot.TotalInputTokens != 10 || snapshot.TotalOutputTokens != 20 {
		t.Fatalf("token totals = in %d out %d, want 10/20", snapshot.TotalInputTokens, snapshot.TotalOutputTokens)
	}
	if snapshot.WindowInputTokens != 0 || snapshot.WindowOutputTokens != 0 {
		t.Fatalf("token window = in %d out %d, want 0/0", snapshot.WindowInputTokens, snapshot.WindowOutputTokens)
	}

	// 新段的写入只累计新值，旧段的量留在进程总量与当日桶里。
	recorder.BeginTool()
	recorder.EndTool(CallMeta{
		Tool: "read_file", Source: SourceMCP, StartedAt: time.Now(),
		Duration: time.Millisecond, Success: true, InputTokens: 1, OutputTokens: 2,
	})
	snapshot = recorder.Snapshot()
	if snapshot.WindowInputTokens != 1 || snapshot.WindowOutputTokens != 2 {
		t.Fatalf("token window = in %d out %d, want 1/2", snapshot.WindowInputTokens, snapshot.WindowOutputTokens)
	}
	if snapshot.TotalInputTokens != 11 || snapshot.TotalOutputTokens != 22 {
		t.Fatalf("token totals = in %d out %d, want 11/22", snapshot.TotalInputTokens, snapshot.TotalOutputTokens)
	}
}

// 落盘状态在重启后恢复：总量、当日桶保留；未跨段的当前段也恢复。
func TestRecorderPersistsAndRestoresAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "analytics-state.json")
	recorder := NewRecorder(10, path)
	recorder.BeginTool()
	recorder.EndTool(CallMeta{
		Tool: "file_edit", Source: SourceMCP, StartedAt: time.Now(),
		Duration: time.Millisecond, Success: false, ErrorCode: "DENIED", ErrorCategory: "permission",
		Insertions: 4, Deletions: 2, InputTokens: 10, OutputTokens: 20,
	})

	restored := NewRecorder(10, path)
	snapshot := restored.Snapshot()
	if snapshot.TotalCalls != 1 || snapshot.TotalErrors != 1 {
		t.Fatalf("restored totals = calls %d errors %d, want 1/1", snapshot.TotalCalls, snapshot.TotalErrors)
	}
	if snapshot.TotalInsertions != 4 || snapshot.TotalDeletions != 2 {
		t.Fatalf("restored line changes = +%d -%d, want +4 -2", snapshot.TotalInsertions, snapshot.TotalDeletions)
	}
	if snapshot.TotalInputTokens != 10 || snapshot.TotalOutputTokens != 20 {
		t.Fatalf("restored token totals = in %d out %d, want 10/20", snapshot.TotalInputTokens, snapshot.TotalOutputTokens)
	}
	if snapshot.WindowInputTokens != 10 || snapshot.WindowOutputTokens != 20 {
		t.Fatalf("restored token window = in %d out %d, want 10/20", snapshot.WindowInputTokens, snapshot.WindowOutputTokens)
	}
	today := time.Now().Format("2006-01-02")
	if len(snapshot.DailyTokens) != 1 || snapshot.DailyTokens[0].Date != today ||
		snapshot.DailyTokens[0].InputTokens != 10 {
		t.Fatalf("restored daily tokens = %#v", snapshot.DailyTokens)
	}
	// 最近调用环是窗口视图，不跨重启恢复。
	if snapshot.TotalCalls != 1 || len(snapshot.RecentCalls) != 0 {
		t.Fatalf("restored recent calls = %#v, want empty", snapshot.RecentCalls)
	}
	if !snapshot.TotalsRestored {
		t.Fatal("restored snapshot must report totals_restored=true")
	}
}

// 快照必须自带计算时间与统计范围：控制台用这些字段区分「统计时间」和
// 「取样窗口」，不能在前端复制窗口常量或拿页面刷新时间顶替。
func TestSnapshotExposesStatisticsScope(t *testing.T) {
	recorder := NewRecorder(10, "")
	recorder.BeginTool()
	recorder.EndTool(CallMeta{
		Tool: "read_file", Source: SourceMCP, StartedAt: time.Now(),
		Duration: time.Millisecond, Success: true, InputTokens: 1, OutputTokens: 2,
	})

	snapshot := recorder.Snapshot()
	if snapshot.GeneratedAt.IsZero() {
		t.Fatal("GeneratedAt is zero")
	}
	if snapshot.WindowMinutes != WindowMinutes {
		t.Fatalf("WindowMinutes = %d, want %d", snapshot.WindowMinutes, WindowMinutes)
	}
	if snapshot.WindowCapacity != windowMaxRecords {
		t.Fatalf("WindowCapacity = %d, want %d", snapshot.WindowCapacity, windowMaxRecords)
	}
	if snapshot.WindowHistoryMinutes != int(callHistoryDuration/time.Minute) {
		t.Fatalf("WindowHistoryMinutes = %d, want %d", snapshot.WindowHistoryMinutes, int(callHistoryDuration/time.Minute))
	}
	if snapshot.DailyTokenRetentionDays != dailyTokenRetentionDays {
		t.Fatalf("DailyTokenRetentionDays = %d, want %d", snapshot.DailyTokenRetentionDays, dailyTokenRetentionDays)
	}
	if snapshot.TotalsRestored {
		t.Fatal("fresh recorder must report totals_restored=false")
	}
	if snapshot.WindowLimited {
		t.Fatal("fresh recorder must not report a limited window")
	}
}

func TestRecorderSnapshotProcessMetricsConsistent(t *testing.T) {
	recorder := NewRecorder(10, "")
	// Ensure some live heap objects exist so HeapAllocBytes is positive.
	sink := make([]byte, 4096)
	sink[0] = 1
	snapshot := recorder.Snapshot()
	if sink[0] != 1 {
		t.Fatal("unreachable")
	}
	if snapshot.Process.HeapAllocBytes == 0 {
		t.Fatalf("HeapAllocBytes = 0, want > 0")
	}
	if snapshot.Process.HeapInuseBytes < snapshot.Process.HeapAllocBytes {
		t.Fatalf("HeapInuseBytes (%d) < HeapAllocBytes (%d)", snapshot.Process.HeapInuseBytes, snapshot.Process.HeapAllocBytes)
	}
	if snapshot.Process.HeapSysBytes < snapshot.Process.HeapInuseBytes {
		t.Fatalf("HeapSysBytes (%d) < HeapInuseBytes (%d)", snapshot.Process.HeapSysBytes, snapshot.Process.HeapInuseBytes)
	}
}
