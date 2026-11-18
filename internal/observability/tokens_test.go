package observability

import (
	"encoding/json"
	"strings"
	"testing"

	toolcore "github.com/uvwt/agentdock/internal/tool/core"
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

func TestCountJSONTokensMatchesMarshalParity(t *testing.T) {
	cases := []struct {
		name  string
		value any
	}{
		{"small string", "hello world <tag> & ok"},
		{"number", 42},
		{"slice", []string{"alpha", "beta", "gamma"}},
		{"struct", struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		}{Name: "agentdock", Count: 7}},
		{"medium map", map[string]any{
			"ok":     true,
			"lines":  []int{1, 2, 3, 4},
			"nested": map[string]any{"a": "b", "c": strings.Repeat("token ", 500)},
		}},
		{"toolcore result", toolcore.Result{"ok": true, "path": "/tmp/a.go", "content": "package main\n"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			want := CountTextTokens(string(raw))
			got := CountJSONTokens(tc.value)
			if got != want {
				t.Fatalf("CountJSONTokens = %d, want %d (exact match)", got, want)
			}
		})
	}
}

func TestCountJSONTokensLargeResultMapBoundedAndAccurate(t *testing.T) {
	big := strings.Repeat("log line output; ", 200_000)
	res := toolcore.Result{
		"exit_code": 0,
		"stdout":    big,
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := CountTextTokens(string(raw))
	got := CountJSONTokens(res)
	diff := got - want
	if diff < 0 {
		diff = -diff
	}
	if diff > want/20 {
		t.Fatalf("large result CountJSONTokens = %d, exact = %d, want within 5%%", got, want)
	}

	baselineAllocs := testing.AllocsPerRun(5, func() {
		if CountTextTokens(big) <= 0 {
			t.Fatal("expected positive baseline token count")
		}
	})
	allocs := testing.AllocsPerRun(5, func() {
		if CountJSONTokens(res) <= 0 {
			t.Fatal("expected positive token count")
		}
	})
	// Large map sampling must avoid marshaling the multi-MB string: overhead over
	// sampling the string directly is bounded to encoding the stripped metadata map.
	if allocs > baselineAllocs+250 {
		t.Fatalf("unexpected JSON overhead allocs: total=%.1f, text_baseline=%.1f", allocs, baselineAllocs)
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
