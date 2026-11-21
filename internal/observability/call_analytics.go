package observability

import (
	"sort"
	"time"
)

const (
	callHistoryDuration = 24 * time.Hour
	callGroupLimit      = 20
	callDetailLimit     = 30
)

// CallAnalyticsQuery selects completed calls and filters on recorded metadata.
type CallAnalyticsQuery struct {
	Range         string
	Tool          string
	Source        Source
	Status        string
	UserAgent     string
	TaskID        string
	ErrorCode     string
	ErrorCategory string
	Stage         Stage
	Page          int
	To            time.Time
}

// CallAnalyticsPage describes the current page of matching calls.
type CallAnalyticsPage struct {
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

// CallAnalyticsSnapshot contains aggregates and a bounded selection of matching calls.
type CallAnalyticsSnapshot struct {
	Window   CallAnalyticsWindow `json:"window"`
	Totals   CallMetrics         `json:"totals"`
	Buckets  []CallBucket        `json:"buckets"`
	ByTool   []CallGroup         `json:"by_tool"`
	BySource []CallGroup         `json:"by_source"`
	ByClient []CallGroup         `json:"by_client"`
	ByTask   []CallGroup         `json:"by_task"`
	ByError  []CallGroup         `json:"by_error"`
	ByStage  []CallGroup         `json:"by_stage"`
	Facets   CallFacets          `json:"facets"`
	Page     *CallAnalyticsPage  `json:"pagination,omitempty"`
	Calls    []AnalyticsCall     `json:"calls"`
}

// CallAnalyticsWindow describes the requested time range and the part that can be measured.
type CallAnalyticsWindow struct {
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	CoveredFrom time.Time `json:"covered_from"`
	LimitedBy   string    `json:"limited_by,omitempty"`
}

// CallMetrics summarizes recorded calls or individual stage instances.
type CallMetrics struct {
	Count         int     `json:"count"`
	ErrorCount    int     `json:"error_count"`
	AvgDurationMS float64 `json:"avg_duration_ms"`
	P50DurationMS float64 `json:"p50_duration_ms"`
	P95DurationMS float64 `json:"p95_duration_ms"`
	P99DurationMS float64 `json:"p99_duration_ms"`
	MaxDurationMS float64 `json:"max_duration_ms"`
	InputTokens   uint64  `json:"input_tokens"`
	OutputTokens  uint64  `json:"output_tokens"`
	Insertions    uint64  `json:"insertions"`
	Deletions     uint64  `json:"deletions"`
}

// CallBucket holds metrics for a chronological interval inside the requested range.
type CallBucket struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	CallMetrics
}

// CallGroup holds metrics for one dimension value. Errors also carry a category.
type CallGroup struct {
	Key      string `json:"key"`
	Category string `json:"category,omitempty"`
	CallMetrics
}

// CallFacets lists choices from the full range before applying filters.
type CallFacets struct {
	Tools   []string `json:"tools"`
	Sources []string `json:"sources"`
}

// AnalyticsCall adds the recorder's completion time to the single-call metadata.
type AnalyticsCall struct {
	ExecutionRecord
	CompletedAt time.Time `json:"completed_at"`
}

// CallAnalytics measures the retained completed calls for a rolling range.
func (r *Recorder) CallAnalytics(query CallAnalyticsQuery) CallAnalyticsSnapshot {
	span, bucketCount := time.Hour, 30
	switch query.Range {
	case "5m":
		span = windowDuration
	case "24h":
		span, bucketCount = callHistoryDuration, 24
	}

	var records []windowStat
	var droppedAt time.Time
	if r != nil {
		r.mu.Lock()
	}
	now := time.Now()
	to := now
	if !query.To.IsZero() && query.To.Before(now) {
		to = query.To
	}
	from := to.Add(-span)
	startedAt := now
	if r != nil {
		// A frozen query limits the read window without extending record retention.
		r.pruneWindowLocked(now)
		startedAt, droppedAt = r.startedAt, r.windowDroppedAt
		entries := r.windowSinceLocked(from)
		end := sort.Search(len(entries), func(i int) bool {
			return entries[i].completedAt.After(to)
		})
		// Entries are immutable, so only the slice of references needs copying here.
		records = append(records, entries[:end]...)
		r.mu.Unlock()
	}
	window := CallAnalyticsWindow{From: from.UTC(), To: to.UTC(), CoveredFrom: from.UTC()}
	if startedAt.After(from) {
		window.CoveredFrom = startedAt.UTC()
		window.LimitedBy = "restart"
	}
	if !droppedAt.IsZero() && !droppedAt.Before(window.CoveredFrom) {
		window.CoveredFrom = droppedAt.UTC()
		window.LimitedBy = "capacity"
	}
	if retentionFrom := now.Add(-callHistoryDuration); retentionFrom.After(window.CoveredFrom) {
		window.CoveredFrom = retentionFrom.UTC()
		window.LimitedBy = "retention"
	}
	if window.CoveredFrom.After(window.To) {
		window.CoveredFrom = window.To
	}
	return summarizeCalls(window, span/time.Duration(bucketCount), bucketCount, records, query)
}

func summarizeCalls(window CallAnalyticsWindow, bucketSpan time.Duration, bucketCount int, records []windowStat, query CallAnalyticsQuery) CallAnalyticsSnapshot {
	result := CallAnalyticsSnapshot{
		Window:  window,
		Buckets: make([]CallBucket, bucketCount),
		Calls:   make([]AnalyticsCall, 0, min(len(records), callDetailLimit)),
	}
	var offset int
	if query.Page > 0 {
		result.Page = &CallAnalyticsPage{Page: query.Page, PageSize: callDetailLimit}
		// Bound the multiplication even when an API caller requests a very large page.
		offset = min(query.Page-1, len(records)/callDetailLimit+1) * callDetailLimit
	}
	var totals callAccumulator
	buckets := make([]callAccumulator, bucketCount)
	tools, sources := make(map[string]struct{}), make(map[string]struct{})
	byTool, bySource, byClient := make(callGroups), make(callGroups), make(callGroups)
	byTask, byError, byStage := make(callGroups), make(callGroups), make(callGroups)
	// Completion time and ID both advance under the recorder lock.
	for i := len(records) - 1; i >= 0; i-- {
		entry := records[i]
		call := entry.record
		source := string(call.Source)
		tools[call.Tool] = struct{}{}
		sources[source] = struct{}{}
		if !query.matches(call) {
			continue
		}

		totals.addCall(call)
		// The right endpoint belongs to the last bucket, so all samples are represented.
		bucket := min(int(entry.completedAt.Sub(window.From)/bucketSpan), bucketCount-1)
		buckets[bucket].addCall(call)
		byTool.group(call.Tool, "").addCall(call)
		bySource.group(source, "").addCall(call)
		byClient.group(call.UserAgent, "").addCall(call)
		byTask.group(call.TaskID, "").addCall(call)
		if !call.Success {
			byError.group(call.ErrorCode, call.ErrorCategory).addCall(call)
		}
		for _, stage := range call.Stages {
			byStage.group(string(stage.Name), "").addDuration(stage.DurationMS, stage.Success)
		}
		if totals.counts.Count > offset && len(result.Calls) < callDetailLimit {
			copy := *call
			copy.Stages = append([]StageRecord(nil), call.Stages...)
			result.Calls = append(result.Calls, AnalyticsCall{ExecutionRecord: copy, CompletedAt: entry.completedAt.UTC()})
		}
	}

	result.Totals = totals.metrics()
	for i := range buckets {
		start := window.From.Add(time.Duration(i) * bucketSpan)
		result.Buckets[i] = CallBucket{Start: start, End: start.Add(bucketSpan), CallMetrics: buckets[i].metrics()}
	}
	result.ByTool = byTool.ranked(len(byTool))
	result.BySource = bySource.ranked(len(bySource))
	result.ByClient = byClient.ranked(callGroupLimit)
	result.ByTask = byTask.ranked(callGroupLimit)
	result.ByError = byError.ranked(callGroupLimit)
	result.ByStage = byStage.ranked(len(byStage))
	result.Facets = CallFacets{Tools: sortedKeys(tools), Sources: sortedKeys(sources)}
	return result
}

func (query CallAnalyticsQuery) matches(call *ExecutionRecord) bool {
	if (query.Tool != "" && call.Tool != query.Tool) ||
		(query.Source != "" && call.Source != query.Source) ||
		(query.UserAgent != "" && call.UserAgent != query.UserAgent) ||
		(query.TaskID != "" && call.TaskID != query.TaskID) {
		return false
	}
	if (query.Status == "success" && !call.Success) || (query.Status == "error" && call.Success) {
		return false
	}
	if (query.ErrorCode != "" && (call.Success || call.ErrorCode != query.ErrorCode)) ||
		(query.ErrorCategory != "" && (call.Success || call.ErrorCategory != query.ErrorCategory)) {
		return false
	}
	if query.Stage == "" {
		return true
	}
	for _, stage := range call.Stages {
		if stage.Name == query.Stage {
			return true
		}
	}
	return false
}

type callAccumulator struct {
	counts    CallMetrics
	duration  float64
	durations []float64
}

func (a *callAccumulator) addDuration(duration float64, success bool) {
	a.counts.Count++
	if !success {
		a.counts.ErrorCount++
	}
	a.duration += duration
	a.durations = append(a.durations, duration)
}

func (a *callAccumulator) addCall(call *ExecutionRecord) {
	a.addDuration(call.DurationMS, call.Success)
	a.counts.InputTokens += uint64(call.InputTokens)
	a.counts.OutputTokens += uint64(call.OutputTokens)
	a.counts.Insertions += uint64(call.Insertions)
	a.counts.Deletions += uint64(call.Deletions)
}

func (a *callAccumulator) metrics() CallMetrics {
	result := a.counts
	if result.Count == 0 {
		return result
	}
	sort.Float64s(a.durations)
	result.AvgDurationMS = a.duration / float64(result.Count)
	result.P50DurationMS = percentile(a.durations, 0.50)
	result.P95DurationMS = percentile(a.durations, 0.95)
	result.P99DurationMS = percentile(a.durations, 0.99)
	result.MaxDurationMS = a.durations[len(a.durations)-1]
	return result
}

type callGroupKey struct {
	value    string
	category string
}

type callGroups map[callGroupKey]*callAccumulator

func (groups callGroups) group(value, category string) *callAccumulator {
	key := callGroupKey{value: value, category: category}
	if groups[key] == nil {
		groups[key] = &callAccumulator{}
	}
	return groups[key]
}

func (groups callGroups) ranked(limit int) []CallGroup {
	keys := make([]callGroupKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := keys[i], keys[j]
		if a, b := groups[left].counts.Count, groups[right].counts.Count; a != b {
			return a > b
		}
		if left.value != right.value {
			return left.value < right.value
		}
		return left.category < right.category
	})
	result := make([]CallGroup, 0, min(limit, len(keys)))
	for _, key := range keys[:cap(result)] {
		result = append(result, CallGroup{Key: key.value, Category: key.category, CallMetrics: groups[key].metrics()})
	}
	return result
}

func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
