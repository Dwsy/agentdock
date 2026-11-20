package observability

import (
	"reflect"
	"testing"
	"time"
)

func TestCallAnalyticsUsesAllMatchesBeyondTheRecentRing(t *testing.T) {
	r := NewRecorder(500, "")
	for call := 1; call <= 550; call++ {
		r.EndTool(CallMeta{
			Tool: "exec_command", Source: SourceNexus, Success: true,
			Duration: time.Duration(call) * time.Millisecond, InputTokens: 2, OutputTokens: 4,
		})
	}
	result := r.CallAnalytics(CallAnalyticsQuery{Range: "5m"})
	want := CallMetrics{
		Count: 550, AvgDurationMS: 275.5, P50DurationMS: 275, P95DurationMS: 523,
		P99DurationMS: 545, MaxDurationMS: 550, InputTokens: 1100, OutputTokens: 2200,
	}
	if result.Totals != want {
		t.Fatalf("totals = %+v, want %+v", result.Totals, want)
	}
	if got := len(result.Calls); got != 30 || result.Calls[0].ID != 550 || len(r.RecentCalls()) != 500 {
		t.Fatalf("detail = %d rows, latest = %d", got, result.Calls[0].ID)
	}
	if len(result.Buckets) != 30 || !result.Buckets[29].End.Equal(result.Window.To) || result.Buckets[29].Count != 550 {
		t.Fatalf("bucket boundary or count: last = %+v", result.Buckets[29])
	}
}

func TestCallAnalyticsFiltersUseCompletionTimeAndPreserveFacets(t *testing.T) {
	r := NewRecorder(2, "")
	now := time.Now()
	r.startedAt = now.Add(-3 * time.Hour)
	r.EndTool(CallMeta{Tool: "read_file", Source: SourceMCP, StartedAt: now.Add(-2 * time.Hour), Success: true})
	r.EndTool(CallMeta{
		Tool: "exec_command", Source: SourceNexus, StartedAt: now.Add(-90 * time.Minute),
		Duration: 45 * time.Millisecond, ErrorCode: "TIMEOUT", ErrorCategory: "runtime", TaskID: "task-a", UserAgent: "client-a",
		InputTokens: 10, OutputTokens: 20,
		Stages: []StageRecord{
			{Name: StageCommandStart, DurationMS: 5, Success: true},
			{Name: StageCommandForegroundWait, DurationMS: 40, Success: false},
		},
	})
	r.EndTool(CallMeta{Tool: "file_edit", Source: SourceNexus, Success: true, Insertions: 5, Deletions: 2})
	r.window[0].completedAt = now.Add(-20 * time.Minute)
	r.window[1].completedAt = now.Add(-15 * time.Minute)

	query := CallAnalyticsQuery{
		Range: "1h", Source: SourceNexus, Tool: "exec_command", Status: "error", UserAgent: "client-a",
		TaskID: "task-a", ErrorCode: "TIMEOUT", ErrorCategory: "runtime", Stage: StageCommandForegroundWait,
	}
	result := r.CallAnalytics(query)
	if got := result.Totals; got.Count != 1 || got.ErrorCount != 1 || got.P95DurationMS != 45 || got.OutputTokens != 20 {
		t.Fatalf("filtered totals = %+v", got)
	}
	if result.Window.LimitedBy != "" || len(result.ByError) != 1 || result.ByError[0].Key != "TIMEOUT" || result.ByError[0].Category != "runtime" {
		t.Fatalf("coverage = %+v, errors = %+v", result.Window, result.ByError)
	}
	if len(result.ByStage) != 2 || result.ByStage[0].Count != 1 || result.ByStage[0].ErrorCount != 1 || result.ByStage[0].InputTokens != 0 {
		t.Fatalf("stage counts = %+v", result.ByStage)
	}
	if len(result.Calls) != 1 || !result.Calls[0].CompletedAt.Equal(r.window[1].completedAt) {
		t.Fatalf("calls = %+v", result.Calls)
	}
	result.Calls[0].Stages[1].DurationMS = 999
	if got := r.CallAnalytics(query).ByStage[0].P95DurationMS; got != 40 {
		t.Fatalf("readers changed stored stages: P95 = %v", got)
	}
	if want := (CallFacets{Tools: []string{"exec_command", "file_edit", "read_file"}, Sources: []string{"mcp", "nexus"}}); !reflect.DeepEqual(result.Facets, want) {
		t.Fatalf("facets = %+v, want %+v", result.Facets, want)
	}
	if legacy := r.Snapshot(); legacy.WindowCalls != 1 || len(legacy.ToolStats) != 1 || legacy.ToolStats[0].Insertions != 5 {
		t.Fatalf("legacy 5-minute window: calls = %d, tools = %+v", legacy.WindowCalls, legacy.ToolStats)
	}
	if calls, errors := r.WindowErrorStats(); calls != 1 || errors != 0 {
		t.Fatalf("5-minute alert counts = %d calls, %d errors", calls, errors)
	}
}

func TestCallAnalyticsWindowCoverage(t *testing.T) {
	r := NewRecorder(1, "")
	initial := r.CallAnalytics(CallAnalyticsQuery{Range: "24h"})
	if initial.Window.LimitedBy != "restart" || !initial.Window.CoveredFrom.Equal(r.startedAt) || initial.Totals.Count != 0 || len(initial.Buckets) != 24 {
		t.Fatalf("empty startup window = %+v, totals = %+v", initial.Window, initial.Totals)
	}

	now := time.Now()
	r.startedAt = now.Add(-48 * time.Hour)
	old := &ExecutionRecord{Tool: "old", Source: SourceMCP, Success: true}
	recent := &ExecutionRecord{Tool: "recent", Source: SourceMCP, Success: true}
	r.window = append(r.window, windowStat{record: old, completedAt: now.Add(-25 * time.Hour)})
	for i := 0; i <= windowMaxRecords; i++ {
		r.window = append(r.window, windowStat{record: recent, completedAt: now.Add(-15*time.Minute + time.Duration(i)*time.Millisecond)})
	}
	result := r.CallAnalytics(CallAnalyticsQuery{Range: "24h"})
	if result.Window.LimitedBy != "capacity" || !result.Window.CoveredFrom.Equal(now.Add(-15*time.Minute)) || result.Totals.Count != windowMaxRecords || len(result.ByTool) != 1 {
		t.Fatalf("capped window = %+v, count = %d, tools = %+v", result.Window, result.Totals.Count, result.ByTool)
	}
	lastFive := r.CallAnalytics(CallAnalyticsQuery{Range: "5m"})
	if lastFive.Window.LimitedBy != "" || !lastFive.Window.CoveredFrom.Equal(lastFive.Window.From) || lastFive.Totals.Count != 0 {
		t.Fatalf("unaffected 5-minute range = %+v, count = %d", lastFive.Window, lastFive.Totals.Count)
	}
}
