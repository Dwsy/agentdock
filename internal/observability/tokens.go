package observability

import (
	"encoding/base64"
	"encoding/json"
	"sync"

	"github.com/tiktoken-go/tokenizer"
	toolcore "github.com/uvwt/agentdock/internal/tool/core"
)

// tokenSampleBytes 是 token 统计的精确计数上限。BPE 计数器（regexp2 分词）
// 吞吐约 15 MB/s，128 KiB 采样在工具返回路径上要 8ms+；统计口径只要
// 数量级准确，16 KiB 采样 + 字节比例外推降到 1ms 内。
const tokenSampleBytes = 16 << 10

// largeJSONStringThreshold bounds when a top-level string field inside a result map
// is sampled directly instead of escaped and marshaled through encoding/json.
const largeJSONStringThreshold = 2 * tokenSampleBytes

// tokenCodec 是共享的 cl100k_base 编码器。Codec 计数无状态，词表用包内
// sync.Once 懒加载，这里只缓存实例避免每次调用重复构建。
var tokenCodec = sync.OnceValues(func() (tokenizer.Codec, error) {
	return tokenizer.Get(tokenizer.Cl100kBase)
})

// CountTextTokens 统计文本的 token 数（cl100k_base 近似口径）。
// 超过 tokenSampleBytes 的文本只精确统计前缀，超出部分按字节比例外推；
// 计数失败退化为字节/4 估算——token 统计是度量，不能反过来让工具调用失败。
func CountTextTokens(text string) int {
	if text == "" {
		return 0
	}
	sample := text
	if len(sample) > tokenSampleBytes {
		sample = sample[:tokenSampleBytes]
	}
	counted := countSampleTokens(sample)
	if len(sample) == len(text) {
		return counted
	}
	return counted * len(text) / len(sample)
}

// CountJSONTokens 统计值经 JSON 序列化后的 token 数，序列化失败时返回 0。
// 只累加计数，任何正文都不会被保存。
func CountJSONTokens(value any) int {
	if value == nil {
		return 0
	}
	switch v := value.(type) {
	case string:
		if len(v) > largeJSONStringThreshold {
			return CountTextTokens(v)
		}
	case []byte:
		if len(v) > largeJSONStringThreshold {
			return countLargeBytesTokens(v)
		}
	case map[string]any:
		return countMapJSONTokens(v)
	case toolcore.Result:
		return countMapJSONTokens(map[string]any(v))
	}
	return countEncodedJSONTokens(value)
}

func countLargeBytesTokens(data []byte) int {
	encodedTotal := base64.StdEncoding.EncodedLen(len(data))
	// 12 KiB of raw bytes encodes to exactly 16 KiB (tokenSampleBytes) of base64.
	const rawSampleMax = (tokenSampleBytes / 4) * 3
	raw := data
	if len(raw) > rawSampleMax {
		raw = raw[:rawSampleMax]
	}
	var sampleBuf [tokenSampleBytes]byte
	base64.StdEncoding.Encode(sampleBuf[:], raw)
	sampleLen := base64.StdEncoding.EncodedLen(len(raw))
	counted := countSampleTokens(string(sampleBuf[:sampleLen]))
	if sampleLen == encodedTotal {
		return counted
	}
	return counted * encodedTotal / sampleLen
}

func countMapJSONTokens(m map[string]any) int {
	hasLarge := false
	for _, entry := range m {
		if s, ok := entry.(string); ok && len(s) > largeJSONStringThreshold {
			hasLarge = true
			break
		}
	}
	if !hasLarge {
		return countEncodedJSONTokens(m)
	}
	largeTokens := 0
	rest := make(map[string]any, len(m))
	for k, entry := range m {
		if s, ok := entry.(string); ok && len(s) > largeJSONStringThreshold {
			largeTokens += CountTextTokens(s)
			rest[k] = ""
			continue
		}
		rest[k] = entry
	}
	return largeTokens + countEncodedJSONTokens(rest)
}

type sampleCountWriter struct {
	buf       [tokenSampleBytes]byte
	sampleLen int
	total     int
}

func (w *sampleCountWriter) Write(p []byte) (int, error) {
	w.total += len(p)
	if remain := len(w.buf) - w.sampleLen; remain > 0 {
		n := copy(w.buf[w.sampleLen:], p[:min(len(p), remain)])
		w.sampleLen += n
	}
	return len(p), nil
}

func countEncodedJSONTokens(value any) int {
	var w sampleCountWriter
	if err := json.NewEncoder(&w).Encode(value); err != nil {
		return 0
	}
	// Strip the single trailing newline added by json.Encoder.Encode so results match json.Marshal.
	total := w.total
	if total > 0 && (w.sampleLen < total || w.buf[w.sampleLen-1] == '\n') {
		total--
	}
	if total <= 0 {
		return 0
	}
	sampleLen := min(w.sampleLen, total)
	counted := countSampleTokens(string(w.buf[:sampleLen]))
	if sampleLen == total {
		return counted
	}
	return counted * total / sampleLen
}

func countSampleTokens(sample string) int {
	codec, err := tokenCodec()
	if err != nil {
		// 词表内嵌在二进制里，正常构建不可能失败；真失败时退化估算
		// 而不是让统计归零。
		return len(sample) / 4
	}
	counted, err := codec.Count(sample)
	if err != nil {
		return len(sample) / 4
	}
	return counted
}
