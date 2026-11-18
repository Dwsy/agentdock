package observability

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/tiktoken-go/tokenizer"
	toolcore "github.com/uvwt/agentdock/internal/tool/core"
)

// TokenCountMode 控制工具与手动推理桥接的 token 估算口径：
// tiktoken（默认，使用 cl100k_base + 大文本采样外推）或 chars（纯 UTF-8 字符数 / 4 向上取整）。
type TokenCountMode string

const (
	TokenCountModeTiktoken TokenCountMode = "tiktoken"
	TokenCountModeChars    TokenCountMode = "chars"
)

// tokenSampleBytes 是 tiktoken BPE 统计的精确计数上限。
const tokenSampleBytes = 16 << 10

// largeJSONStringThreshold bounds when a top-level string field inside a result map
// is counted directly instead of escaped and marshaled through encoding/json.
const largeJSONStringThreshold = 2 * tokenSampleBytes

// tokenCodec 是共享的 cl100k_base 编码器，词表通过 sync.Once 懒加载。
var tokenCodec = sync.OnceValues(func() (tokenizer.Codec, error) {
	return tokenizer.Get(tokenizer.Cl100kBase)
})

var activeTokenCountMode atomic.Int32 // 0 = tiktoken (default), 1 = chars

// ParseTokenCountMode 解析并校验 token 估算模式；空字符串回落为 tiktoken。
func ParseTokenCountMode(value string) (TokenCountMode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(TokenCountModeTiktoken):
		return TokenCountModeTiktoken, nil
	case string(TokenCountModeChars):
		return TokenCountModeChars, nil
	default:
		return "", fmt.Errorf("unsupported token count mode %q; expected tiktoken or chars", value)
	}
}

// SetTokenCountMode 切换全局默认 token 统计模式；未识别值回落为 tiktoken。
func SetTokenCountMode(mode TokenCountMode) {
	if mode == TokenCountModeChars {
		activeTokenCountMode.Store(1)
		return
	}
	activeTokenCountMode.Store(0)
}

// CurrentTokenCountMode 返回当前全局默认 token 统计模式。
func CurrentTokenCountMode() TokenCountMode {
	if activeTokenCountMode.Load() == 1 {
		return TokenCountModeChars
	}
	return TokenCountModeTiktoken
}

func normalizeTokenCountMode(mode TokenCountMode) TokenCountMode {
	if mode == TokenCountModeChars {
		return TokenCountModeChars
	}
	return TokenCountModeTiktoken
}

func tokensFromCharCount(chars int) int {
	if chars <= 0 {
		return 0
	}
	return (chars + 3) / 4
}

// CountTextTokens 按当前全局配置模式统计文本的 token 数。
func CountTextTokens(text string) int {
	return CountTextTokensWithMode(text, CurrentTokenCountMode())
}

// CountTextTokensWithMode 按指定模式统计文本的 token 数。
func CountTextTokensWithMode(text string, mode TokenCountMode) int {
	if text == "" {
		return 0
	}
	if normalizeTokenCountMode(mode) == TokenCountModeChars {
		return tokensFromCharCount(utf8.RuneCountInString(text))
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

// CountJSONTokens 按当前全局配置模式统计值经 JSON 序列化后的 token 数。
func CountJSONTokens(value any) int {
	return CountJSONTokensWithMode(value, CurrentTokenCountMode())
}

// CountJSONTokensWithMode 按指定模式统计值经 JSON 序列化后的 token 数。
func CountJSONTokensWithMode(value any, mode TokenCountMode) int {
	if value == nil {
		return 0
	}
	mode = normalizeTokenCountMode(mode)
	switch v := value.(type) {
	case string:
		if len(v) > largeJSONStringThreshold {
			return CountTextTokensWithMode(v, mode)
		}
	case []byte:
		if len(v) > largeJSONStringThreshold {
			if mode == TokenCountModeChars {
				return tokensFromCharCount(base64.StdEncoding.EncodedLen(len(v)) + 2)
			}
			return countLargeBytesTokens(v)
		}
	case map[string]any:
		return countMapJSONTokensWithMode(v, mode)
	case toolcore.Result:
		return countMapJSONTokensWithMode(map[string]any(v), mode)
	}
	return countEncodedJSONTokensWithMode(value, mode)
}

func countLargeBytesTokens(data []byte) int {
	encodedTotal := base64.StdEncoding.EncodedLen(len(data))
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

func countMapJSONTokensWithMode(m map[string]any, mode TokenCountMode) int {
	hasLarge := false
	for _, entry := range m {
		if s, ok := entry.(string); ok && len(s) > largeJSONStringThreshold {
			hasLarge = true
			break
		}
	}
	if !hasLarge {
		return countEncodedJSONTokensWithMode(m, mode)
	}
	largeCount := 0
	rest := make(map[string]any, len(m))
	for k, entry := range m {
		if s, ok := entry.(string); ok && len(s) > largeJSONStringThreshold {
			if mode == TokenCountModeChars {
				largeCount += utf8.RuneCountInString(s)
			} else {
				largeCount += CountTextTokensWithMode(s, TokenCountModeTiktoken)
			}
			rest[k] = ""
			continue
		}
		rest[k] = entry
	}
	if mode == TokenCountModeChars {
		return tokensFromCharCount(largeCount + encodedJSONCharCount(rest))
	}
	return largeCount + countEncodedJSONTokensWithMode(rest, TokenCountModeTiktoken)
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

type runeCountWriter struct {
	chars    int
	lastByte byte
	total    int
}

func (w *runeCountWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		if b&0xc0 != 0x80 {
			w.chars++
		}
		w.lastByte = b
	}
	w.total += len(p)
	return len(p), nil
}

func encodedJSONCharCount(value any) int {
	var w runeCountWriter
	if err := json.NewEncoder(&w).Encode(value); err != nil {
		return 0
	}
	chars := w.chars
	// 去掉 json.Encoder 末尾自动追加的单个换行符，使其与 json.Marshal 一致。
	if w.total > 0 && w.lastByte == '\n' && chars > 0 {
		chars--
	}
	return chars
}

func countEncodedJSONTokensWithMode(value any, mode TokenCountMode) int {
	if mode == TokenCountModeChars {
		return tokensFromCharCount(encodedJSONCharCount(value))
	}
	var w sampleCountWriter
	if err := json.NewEncoder(&w).Encode(value); err != nil {
		return 0
	}
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
		return len(sample) / 4
	}
	counted, err := codec.Count(sample)
	if err != nil {
		return len(sample) / 4
	}
	return counted
}
