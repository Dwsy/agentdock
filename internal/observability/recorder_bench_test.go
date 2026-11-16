package observability

import (
	"strings"
	"testing"
	"time"
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
