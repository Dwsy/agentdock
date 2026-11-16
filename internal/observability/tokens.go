package observability

import (
	"encoding/json"
	"sync"

	"github.com/tiktoken-go/tokenizer"
)

// tokenSampleBytes 是 token 统计的精确计数上限。BPE 计数器（regexp2 分词）
// 吞吐约 15 MB/s，128 KiB 采样在工具返回路径上要 8ms+；统计口径只要
// 数量级准确，16 KiB 采样 + 字节比例外推降到 1ms 内。
const tokenSampleBytes = 16 << 10

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
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	return CountTextTokens(string(encoded))
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
