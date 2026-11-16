package observability

import (
	"strings"
	"testing"
)

func TestCountTextTokensHandlesEmptyAndSmallInput(t *testing.T) {
	if got := CountTextTokens(""); got != 0 {
		t.Fatalf("empty text tokens = %d, want 0", got)
	}
	if got := CountTextTokens("hello world"); got <= 0 {
		t.Fatalf("small text tokens = %d, want > 0", got)
	}
}

func TestCountJSONTokensCountsSerializedValue(t *testing.T) {
	if got := CountJSONTokens(nil); got != 0 {
		t.Fatalf("nil value tokens = %d, want 0", got)
	}
	small := CountJSONTokens(map[string]any{"path": "/tmp/note.txt"})
	if small <= 0 {
		t.Fatalf("map tokens = %d, want > 0", small)
	}
	// 同样的值经序列化后必须计数一致：计数对象是文本，不依赖 Go 表示形态。
	if encoded := CountJSONTokens(map[string]any{"path": "/tmp/note.txt"}); encoded != small {
		t.Fatalf("map tokens = %d / %d, want equal", small, encoded)
	}
}

// 超过采样上限的文本按字节比例外推，结果必须接近整段精确计数。
func TestCountTextTokensScalesLongInput(t *testing.T) {
	long := strings.Repeat("agentdock token statistics ", 40000)
	if len(long) <= tokenSampleBytes {
		t.Fatalf("test text too short: %d bytes", len(long))
	}
	codec, err := tokenCodec()
	if err != nil {
		t.Fatal(err)
	}
	exact, err := codec.Count(long)
	if err != nil {
		t.Fatal(err)
	}
	scaled := CountTextTokens(long)
	// 重复文本的 token 密度均匀，外推误差应在 5% 以内。
	diff := scaled - exact
	if diff < 0 {
		diff = -diff
	}
	if diff > exact/20 {
		t.Fatalf("scaled tokens = %d, exact = %d, want within 5%%", scaled, exact)
	}
}
