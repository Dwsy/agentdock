package observability

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestUserAgentContextNormalizes(t *testing.T) {
	ctx := WithUserAgent(context.Background(), "  ChatGPT/1.0 (+macOS)  ")
	if got := UserAgentFromContext(ctx); got != "ChatGPT/1.0 (+macOS)" {
		t.Fatalf("UserAgentFromContext() = %q", got)
	}
	if got := UserAgentFromContext(context.Background()); got != "" {
		t.Fatalf("missing UA = %q", got)
	}
	if got := UserAgentFromContext(WithUserAgent(context.Background(), "   ")); got != "" {
		t.Fatalf("blank UA = %q", got)
	}
}

func TestUserAgentContextStripsControlAndTruncates(t *testing.T) {
	dirty := "agent\x00dock\nclient\ttest"
	ctx := WithUserAgent(context.Background(), dirty)
	if got := UserAgentFromContext(ctx); strings.ContainsAny(got, "\x00\n\t") {
		t.Fatalf("control characters survived: %q", got)
	}

	long := strings.Repeat("a", 600)
	ctx = WithUserAgent(context.Background(), long)
	got := UserAgentFromContext(ctx)
	if len(got) != maxUserAgentBytes {
		t.Fatalf("truncated length = %d, want %d", len(got), maxUserAgentBytes)
	}
}

func TestUserAgentContextKeepsValidUTF8Boundary(t *testing.T) {
	// 300 个三字节字符 = 900 字节；截断必须落在字符边界，不能产生非法 UTF-8。
	value := strings.Repeat("调", 300)
	ctx := WithUserAgent(context.Background(), value)
	got := UserAgentFromContext(ctx)
	if len(got) > maxUserAgentBytes || !isValidUTF8(got) {
		t.Fatalf("truncation broke UTF-8: len=%d", len(got))
	}
}

func TestTaskIDContextNormalization(t *testing.T) {
	longASCII := strings.Repeat("a", 200)
	// 43 个三字节字符横跨 128 字节边界，截断必须退回到完整字符。
	longRunes := strings.Repeat("调", 43)
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "keeps value", in: "tsk_0123456789abcdef", want: "tsk_0123456789abcdef"},
		{name: "trims whitespace", in: "  tsk_0123456789abcdef\t", want: "tsk_0123456789abcdef"},
		{name: "strips control characters", in: "ts\x00k_1\n2\t3\x7f", want: "tsk_123"},
		{name: "blank is not recorded", in: " \t\n ", want: ""},
		{name: "empty is not recorded", in: "", want: ""},
		{name: "truncates to max bytes", in: longASCII, want: longASCII[:maxTaskIDBytes]},
		{name: "keeps utf8 boundary", in: longRunes, want: strings.Repeat("调", 42)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := WithTaskID(context.Background(), test.in)
			got := TaskIDFromContext(ctx)
			if got != test.want {
				t.Fatalf("TaskIDFromContext() = %q, want %q", got, test.want)
			}
			if len(got) > maxTaskIDBytes {
				t.Fatalf("task id length = %d, want <= %d", len(got), maxTaskIDBytes)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("truncation broke UTF-8: %q", got)
			}
		})
	}
}

func TestTaskIDContextHandlesNilContext(t *testing.T) {
	if got := TaskIDFromContext(nil); got != "" {
		t.Fatalf("nil context task id = %q", got)
	}
	if got := TaskIDFromContext(WithTaskID(nil, "tsk_0123456789abcdef")); got != "tsk_0123456789abcdef" {
		t.Fatalf("nil ctx WithTaskID = %q", got)
	}
}

func isValidUTF8(value string) bool {
	for _, r := range value {
		if r == '\uFFFD' {
			return false
		}
	}
	return true
}
