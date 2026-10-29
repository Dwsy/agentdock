package observability

import (
	"context"
	"strings"
	"unicode/utf8"
)

// maxUserAgentBytes 限制 UA 进入记录的长度：真实客户端 UA 可到数百字节，
// 记录只需要足够识别来源，不能让每个 trace 都被 UA 撑大。
const maxUserAgentBytes = 256

// maxTaskIDBytes 限制任务 ID 进入记录的长度：任务 ID 是关联键而不是自由文本，
// 正常值远小于该上限，截断只为防止调用方塞入超长字符串撑大每条 trace。
const maxTaskIDBytes = 128

// Source 标识一次工具调用进入 Runtime 的传输来源。
// 来源只允许固定枚举，避免把任意请求数据带入观测记录。
type Source string

const (
	SourceInternal Source = "internal"
	SourceMCP      Source = "mcp"
	SourceNexus    Source = "nexus"
)

type sourceContextKey struct{}

func WithSource(ctx context.Context, source Source) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	switch source {
	case SourceMCP, SourceNexus, SourceInternal:
	default:
		source = SourceInternal
	}
	return context.WithValue(ctx, sourceContextKey{}, source)
}

func SourceFromContext(ctx context.Context) Source {
	if ctx == nil {
		return SourceInternal
	}
	source, ok := ctx.Value(sourceContextKey{}).(Source)
	if !ok {
		return SourceInternal
	}
	switch source {
	case SourceMCP, SourceNexus, SourceInternal:
		return source
	default:
		return SourceInternal
	}
}

type userAgentContextKey struct{}

// WithUserAgent 记录本次工具调用的客户端 User-Agent。
// 空值与纯空白视为“未提供”，不做记录，避免 UI 出现无意义的空格。
func WithUserAgent(ctx context.Context, userAgent string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	value := normalizeUserAgent(userAgent)
	if value == "" {
		return ctx
	}
	return context.WithValue(ctx, userAgentContextKey{}, value)
}

func UserAgentFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(userAgentContextKey{}).(string)
	return value
}

// normalizeUserAgent 去除控制字符并截断到合法 UTF-8 边界。
func normalizeUserAgent(value string) string {
	return normalizeMetadataText(value, maxUserAgentBytes)
}

// normalizeMetadataText 去除控制字符并按字节截断到合法 UTF-8 边界。
// UA 与任务 ID 共用一套归一化：两者都只是识别性元数据，规则必须一致，
// 否则同一个请求头在不同记录里会出现两种清洗结果。
func normalizeMetadataText(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value)
	cleaned = strings.TrimSpace(cleaned)
	if len(cleaned) <= maxBytes {
		return cleaned
	}
	cut := maxBytes
	for cut > 0 && !utf8.ValidString(cleaned[:cut]) {
		cut--
	}
	return cleaned[:cut]
}

type taskIDContextKey struct{}

// WithTaskID 记录本次工具调用关联的任务 ID，用于把任务、调用与文件变更串起来。
// 空值与纯空白视为“未提供”，不做记录，避免 trace 出现无意义的空关联。
func WithTaskID(ctx context.Context, taskID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	value := normalizeTaskID(taskID)
	if value == "" {
		return ctx
	}
	return context.WithValue(ctx, taskIDContextKey{}, value)
}

func TaskIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(taskIDContextKey{}).(string)
	return value
}

func normalizeTaskID(value string) string {
	return normalizeMetadataText(value, maxTaskIDBytes)
}
