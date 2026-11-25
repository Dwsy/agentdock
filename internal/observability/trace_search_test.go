package observability

import (
	"strings"
	"testing"
)

func TestContainsFoldMatchesLowercaseSearch(t *testing.T) {
	tests := []struct {
		name     string
		haystack string
		query    string
	}{
		{name: "empty query"},
		{name: "lowercase match across byte blocks", haystack: "1234567needle end", query: "needle"},
		{name: "uppercase after byte block", haystack: "12345678Upper text", query: "upper"},
		{name: "mixed case suffix", haystack: "read read REaDer", query: "reader"},
		{name: "mixed case uncapitalized start", haystack: "x reADFilE", query: "READFILE"},
		{name: "single uppercase letter", haystack: "output K", query: "k"},
		{name: "nonletter first byte", haystack: "\\src\\Config.Go", query: "\\config.go"},
		{name: "distinct punctuation", haystack: "run {task}", query: "[task]"},
		{name: "final candidate too short", haystack: "not here n", query: "needle"},
		{name: "Chinese and Latin case", haystack: "配置/Änderung.MD", query: "配置/änderung.md"},
		{name: "Unicode lowercase maps to ASCII", haystack: "unit K", query: "k"},
		{name: "Unicode after uppercase byte", haystack: "UNIT K", query: "k"},
		{name: "Unicode lowercase stays distinct", haystack: "word ſ", query: "s"},
		{name: "invalid UTF-8 replacement", haystack: "\xffabc", query: "�A"},
		{name: "original byte length guard", haystack: "\xff", query: "�"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			needle := strings.ToLower(tt.query)
			want := len(tt.haystack) >= len(needle) && strings.Contains(strings.ToLower(tt.haystack), needle)
			if got := containsFold(tt.haystack, needle); got != want {
				t.Errorf("containsFold(%q, %q) = %t, want %t", tt.haystack, needle, got, want)
			}
		})
	}
}

func TestLowercaseASCIIPrefixBytePositions(t *testing.T) {
	text := []byte("0123456789abcdef")
	for offset, original := range text {
		for value := 0; value <= 0xff; value++ {
			text[offset] = byte(value)
			want := len(text)
			if value >= 0x80 || 'A' <= value && value <= 'Z' {
				want = offset
			}
			if got := lowercaseASCIIPrefix(string(text)); got != want {
				t.Fatalf("byte %#x at offset %d: prefix = %d, want %d", value, offset, got, want)
			}
		}
		text[offset] = original
	}
}
