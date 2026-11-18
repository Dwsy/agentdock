package observability

import (
	"context"
	"fmt"
	"reflect"
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

func TestUpstreamHeadersContextNormalizes(t *testing.T) {
	input := map[string]string{
		" User-Agent ":     " ChatGPT/1.0 (+macOS) ",
		"traceparent":      "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"x-request\x00-id": "req\x00-1",
		"x-empty":          "   ",
		"bad key":          "dropped",
	}
	headers := NormalizeUpstreamHeaders(input)
	want := UpstreamHeaders{
		"user-agent":  "ChatGPT/1.0 (+macOS)",
		"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	}
	if !reflect.DeepEqual(headers, want) {
		t.Fatalf("NormalizeUpstreamHeaders() = %#v, want %#v", headers, want)
	}
	// 入参之后被改动不能影响已归一化的副本。
	input["traceparent"] = "changed"
	if headers["traceparent"] == "changed" {
		t.Fatal("normalized headers aliased the caller map")
	}
}

func TestUpstreamHeadersContextCapsEntriesAndValues(t *testing.T) {
	input := make(map[string]string, maxUpstreamHeaders+4)
	for i := 0; i < maxUpstreamHeaders+4; i++ {
		input[fmt.Sprintf("x-header-%02d", i)] = "v"
	}
	input["x-long"] = strings.Repeat("a", 600)
	ctx := WithUpstreamHeaders(context.Background(), input)
	got := UpstreamHeadersFromContext(ctx)
	// 字典序最后保留到 x-header-15；x-long 排在后面被挤掉，条数上限生效。
	if len(got) != maxUpstreamHeaders {
		t.Fatalf("header count = %d, want %d", len(got), maxUpstreamHeaders)
	}
	if _, ok := got["x-header-00"]; !ok {
		t.Fatalf("deterministic prefix missing: %#v", got)
	}
	if _, ok := got["x-long"]; ok {
		t.Fatal("cap should drop later keys deterministically")
	}
	only := NormalizeUpstreamHeaders(map[string]string{"x-long": strings.Repeat("a", 600)})
	if len(only["x-long"]) != maxUpstreamHeaderValueBytes {
		t.Fatalf("value length = %d, want %d", len(only["x-long"]), maxUpstreamHeaderValueBytes)
	}
}

func TestUpstreamHeadersContextAbsentWhenEmpty(t *testing.T) {
	if got := UpstreamHeadersFromContext(context.Background()); got != nil {
		t.Fatalf("missing headers = %#v", got)
	}
	if got := UpstreamHeadersFromContext(WithUpstreamHeaders(context.Background(), nil)); got != nil {
		t.Fatalf("nil headers = %#v", got)
	}
	if got := UpstreamHeadersFromContext(WithUpstreamHeaders(context.Background(), map[string]string{"x-empty": ""})); got != nil {
		t.Fatalf("empty-only headers = %#v", got)
	}
	if got := UpstreamHeadersFromContext(nil); got != nil {
		t.Fatalf("nil context headers = %#v", got)
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
