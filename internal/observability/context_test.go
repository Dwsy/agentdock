package observability

import (
	"context"
	"strings"
	"testing"
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

func isValidUTF8(value string) bool {
	for _, r := range value {
		if r == '\uFFFD' {
			return false
		}
	}
	return true
}
