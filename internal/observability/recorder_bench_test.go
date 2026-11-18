package observability

import (
	"strings"
	"testing"
	"time"

	toolcore "github.com/uvwt/agentdock/internal/tool/core"
)

func BenchmarkCountTextTokens16K(b *testing.B) {
	sample := strings.Repeat("the quick brown fox jumps over the lazy dog; ", (16<<10)/45)
	b.SetBytes(int64(len(sample)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CountTextTokens(sample)
	}
}

func BenchmarkCountTextTokens4MExtrapolated(b *testing.B) {
	sample := strings.Repeat("the quick brown fox jumps over the lazy dog; ", (4<<20)/45)
	b.SetBytes(int64(len(sample)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CountTextTokens(sample)
	}
}

func BenchmarkCountJSONTokensSmallMap(b *testing.B) {
	value := map[string]any{
		"ok":   true,
		"path": "/workspace/src/main.go",
		"line": 42,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CountJSONTokens(value)
	}
}

func BenchmarkCountJSONTokens4MResult(b *testing.B) {
	stdout := strings.Repeat("the quick brown fox jumps over the lazy dog; ", (4<<20)/45)
	result := toolcore.Result{
		"ok":        true,
		"exit_code": 0,
		"stdout":    stdout,
	}
	b.SetBytes(int64(len(stdout)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CountJSONTokens(result)
	}
}

func BenchmarkRecorderSnapshot(b *testing.B) {
	recorder := NewRecorder(500, "")
	for i := 0; i < 100; i++ {
		recorder.BeginTool()
		recorder.EndTool(CallMeta{
			Tool: "read_file", Source: SourceMCP, StartedAt: time.Now(),
			Duration: time.Millisecond, Success: true, InputTokens: 50, OutputTokens: 150,
		})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = recorder.Snapshot()
	}
}

func BenchmarkEndToolWithPersist(b *testing.B) {
	path := b.TempDir() + "/state.json"
	recorder := NewRecorder(500, path)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		recorder.BeginTool()
		recorder.EndTool(CallMeta{
			Tool: "read_file", Source: SourceMCP, StartedAt: time.Now(),
			Duration: time.Millisecond, Success: true, InputTokens: 100, OutputTokens: 200,
		})
	}
}

func BenchmarkTraceStoreRecordSteadyState(b *testing.B) {
	store, err := NewTraceStore(TraceOptions{Mode: TraceModeMemory, MaxRecords: 2000})
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()

	sample := TraceRecord{
		Tool:      "read_file",
		Source:    string(SourceMCP),
		UserAgent: "AgentDockTest/1.0",
		Success:   true,
		Arguments: `{"path":"internal/app/runtime.go","start_line":1,"end_line":100}`,
		Result:    `{"ok":true,"path":"internal/app/runtime.go","content":"` + strings.Repeat("code line ", 200) + `"}`,
		StartedAt: time.Now(),
	}
	for i := 0; i < 2000; i++ {
		store.Record(sample)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.Record(sample)
	}
}

func BenchmarkTraceStoreQuerySearch(b *testing.B) {
	store, err := NewTraceStore(TraceOptions{Mode: TraceModeMemory, MaxRecords: 2000})
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()

	sample := TraceRecord{
		Tool:      "read_file",
		Source:    string(SourceMCP),
		UserAgent: "AgentDockTest/1.0",
		Success:   true,
		Arguments: `{"path":"internal/app/runtime.go"}`,
		Result:    `{"ok":true,"content":"` + strings.Repeat("ordinary output line; ", 200) + `"}`,
		StartedAt: time.Now(),
	}
	for i := 0; i < 2000; i++ {
		store.Record(sample)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = store.Query(TraceQuery{Query: "nonexistent-needle", Limit: 50, Summary: true})
	}
}
