package observability

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRecorderKeepsBoundedNewestFirstHistory(t *testing.T) {
	recorder := NewRecorder(3)
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
	if snapshot.RecentCapacity != 3 || snapshot.WindowCalls != 3 {
		t.Fatalf("snapshot capacity/window = %d/%d, want 3/3", snapshot.RecentCapacity, snapshot.WindowCalls)
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
	recorder := NewRecorder(200)
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
	recorder := NewRecorder(500)
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
	if snapshot.WindowCalls != 500 || snapshot.ActiveCalls != 0 {
		t.Fatalf("window/active = %d/%d, want 500/0", snapshot.WindowCalls, snapshot.ActiveCalls)
	}
}

func TestSnapshotJSONIsZeroPayload(t *testing.T) {
	recorder := NewRecorder(10)
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
	recorder := NewRecorder(10)
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
