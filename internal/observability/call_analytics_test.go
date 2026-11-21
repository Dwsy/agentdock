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

func TestCallAnalyticsPaginationFreezesWindowAndPagesFilteredCalls(t *testing.T) {
	r := NewRecorder(1, "")
	now := time.Now()
	r.startedAt = now.Add(-2 * time.Hour)
	for i := 1; i <= 65; i++ {
		tool := "selected"
		if i%2 == 0 {
			tool = "other"
		}
		r.EndTool(CallMeta{Tool: tool, StartedAt: now.Add(-time.Duration(i) * time.Second), Success: true})
		// Put calls on either side of a completion time tie across the page boundary.
		r.window[i-1].completedAt = now.Add(-time.Duration((65-i)/11) * time.Second)
	}

	query := CallAnalyticsQuery{Tool: "selected", Page: 1, To: now}
	first := r.CallAnalytics(query)
	if first.Page == nil || *first.Page != (CallAnalyticsPage{Page: 1, PageSize: 30}) || len(first.Calls) != 30 {
		t.Fatalf("page one: pagination = %+v, rows = %d", first.Page, len(first.Calls))
	}
	for i, call := range first.Calls {
		if want := uint64(65 - 2*i); call.ID != want {
			t.Fatalf("page one row %d = %d, want %d", i, call.ID, want)
		}
	}
	if !first.Window.To.Equal(now) || !first.Buckets[len(first.Buckets)-1].End.Equal(now) || first.Totals.Count != 33 {
		t.Fatalf("page one window = %+v, totals = %+v", first.Window, first.Totals)
	}

	r.EndTool(CallMeta{Tool: "selected", Success: true})
	r.EndTool(CallMeta{Tool: "new facet", Success: true})
	query.Page = 2
	second := r.CallAnalytics(query)
	if second.Page == nil || *second.Page != (CallAnalyticsPage{Page: 2, PageSize: 30}) || len(second.Calls) != 3 {
		t.Fatalf("page two: pagination = %+v, rows = %d", second.Page, len(second.Calls))
	}
	for i, call := range second.Calls {
		if want := uint64(5 - 2*i); call.ID != want {
			t.Fatalf("page two row %d = %d, want %d", i, call.ID, want)
		}
	}
	if second.Window != first.Window || second.Totals != first.Totals || !reflect.DeepEqual(second.Buckets, first.Buckets) ||
		!reflect.DeepEqual(second.ByTool, first.ByTool) || !reflect.DeepEqual(second.Facets, first.Facets) {
		t.Fatal("paging and new records changed the frozen window's aggregates or facets")
	}

	query.Page = int(^uint(0) >> 1)
	if page := r.CallAnalytics(query); page.Page == nil || page.Page.Page != query.Page || page.Calls == nil || len(page.Calls) != 0 || page.Totals != first.Totals {
		t.Fatalf("out-of-range page: pagination = %+v, calls = %v, totals = %+v", page.Page, page.Calls, page.Totals)
	}
	if legacy := r.CallAnalytics(CallAnalyticsQuery{Tool: "selected"}); legacy.Page != nil || len(legacy.Calls) != 30 || legacy.Calls[0].ID != 66 || legacy.Totals.Count != 34 {
		t.Fatalf("legacy: pagination = %+v, rows = %d, totals = %+v", legacy.Page, len(legacy.Calls), legacy.Totals)
	}
}

func TestCallAnalyticsFrozenWindowStillPrunesAtRealTime(t *testing.T) {
	r := NewRecorder(1, "")
	now := time.Now()
	r.startedAt = now.Add(-48 * time.Hour)
	r.window = []windowStat{
		{record: &ExecutionRecord{ID: 1, Tool: "expired"}, completedAt: now.Add(-24*time.Hour - 30*time.Minute)},
		{record: &ExecutionRecord{ID: 2, Tool: "retained"}, completedAt: now.Add(-2 * time.Hour)},
	}
	to := now.Add(-time.Hour)
	result := r.CallAnalytics(CallAnalyticsQuery{Range: "24h", Page: 1, To: to})
	if len(r.window) != 1 || len(result.Calls) != 1 || result.Calls[0].ID != 2 || result.Totals.Count != 1 {
		t.Fatalf("retained window = %v, calls = %v, totals = %+v", r.window, result.Calls, result.Totals)
	}
	if !result.Window.To.Equal(to) || !result.Window.From.Equal(to.Add(-24*time.Hour)) || result.Window.LimitedBy != "retention" ||
		result.Window.CoveredFrom.Before(now.Add(-24*time.Hour)) || result.Window.CoveredFrom.After(time.Now().Add(-24*time.Hour)) {
		t.Fatalf("window coverage = %+v", result.Window)
	}

	to = now.Add(-26 * time.Hour)
	expired := r.CallAnalytics(CallAnalyticsQuery{Range: "24h", Page: 2, To: to})
	if expired.Totals.Count != 0 || len(expired.Calls) != 0 || expired.Window.LimitedBy != "retention" || !expired.Window.CoveredFrom.Equal(to) {
		t.Fatalf("fully expired window = %+v, totals = %+v, calls = %v", expired.Window, expired.Totals, expired.Calls)
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
