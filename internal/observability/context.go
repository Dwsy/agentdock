package observability

import (
	"context"
	"sort"
	"strings"
	"unicode/utf8"
)

// maxUserAgentBytes 限制 UA 进入记录的长度：真实客户端 UA 可到数百字节，
// 记录只需要足够识别来源，不能让每个 trace 都被 UA 撑大。
const maxUserAgentBytes = 256

// maxTaskIDBytes 限制任务 ID 进入记录的长度：任务 ID 是关联键而不是自由文本，
// 正常值远小于该上限，截断只为防止调用方塞入超长字符串撑大每条 trace。
const maxTaskIDBytes = 128

// maxUpstreamHeaders 与 maxUpstreamHeaderValueBytes 限制上游头进入记录的规模：
// 上游头的用途是识别来源（UA、请求 ID、链路追踪），不是复刻整份请求头，
// 因此条数与单值长度都要有上限，避免每条 trace 被头撑大。
const (
	maxUpstreamHeaders          = 16
	maxUpstreamHeaderValueBytes = 256
)

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

// UpstreamHeaders 是自远端中继（Nexus）转发而来的「上游客户端请求头」，键一律小写。
// 它描述的是最初发起请求的客户端（ChatGPT / Claude / Codex 等），不描述 Core
// 自己那条连接：本地 HTTP request header 由传输层采集，两者不可混用。
type UpstreamHeaders map[string]string

// NormalizeUpstreamHeaders 复制并清洗一份上游头：键小写去空白，空键空值丢弃，
// 控制字符剔除，超长截断，条数按字典序保留前 maxUpstreamHeaders 条。
// 返回值是独立副本，调用方之后改动入参不会影响已写入的记录。
func NormalizeUpstreamHeaders(headers map[string]string) UpstreamHeaders {
	if len(headers) == 0 {
		return nil
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	normalized := make(UpstreamHeaders, len(keys))
	for _, key := range keys {
		name := strings.ToLower(strings.TrimSpace(key))
		if !validUpstreamHeaderName(name) {
			continue
		}
		if _, exists := normalized[name]; exists {
			continue
		}
		value := normalizeMetadataText(headers[key], maxUpstreamHeaderValueBytes)
		if value == "" {
			continue
		}
		normalized[name] = value
		if len(normalized) >= maxUpstreamHeaders {
			break
		}
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

// validUpstreamHeaderName 只接受 HTTP token 字符集（RFC 9110），
// 防止上游把任意文本塞进键名污染面板。
func validUpstreamHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return false
		}
	}
	return true
}

type upstreamHeadersContextKey struct{}

// WithUpstreamHeaders 把上游客户端头挂进本次调用的上下文，供 trace 记录落库。
// 空值视为“上游未提供”，不写上下文，保持与老 Nexus 完全一致的行为。
func WithUpstreamHeaders(ctx context.Context, headers map[string]string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized := NormalizeUpstreamHeaders(headers)
	if len(normalized) == 0 {
		return ctx
	}
	return context.WithValue(ctx, upstreamHeadersContextKey{}, normalized)
}

// UpstreamHeadersFromContext 返回本次调用的上游客户端头；未提供时返回 nil。
func UpstreamHeadersFromContext(ctx context.Context) UpstreamHeaders {
	if ctx == nil {
		return nil
	}
	headers, _ := ctx.Value(upstreamHeadersContextKey{}).(UpstreamHeaders)
	return headers
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
