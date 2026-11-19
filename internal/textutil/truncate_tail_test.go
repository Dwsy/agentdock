package textutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSafeTruncateTailString(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		maxBytes  int
		text      string
		truncated bool
		omitted   int
	}{
		{name: "unlimited", value: "hello", maxBytes: 0, text: "hello"},
		{name: "exact length", value: "hello", maxBytes: 5, text: "hello"},
		{name: "keeps tail", value: "abcdefghij", maxBytes: 4, text: "ghij", truncated: true, omitted: 6},
		{name: "utf8 boundary", value: "abc世界", maxBytes: 4, text: "界", truncated: true, omitted: 6},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := SafeTruncateTailString(test.value, test.maxBytes)
			if got.Text != test.text || got.Truncated != test.truncated || got.Omitted != test.omitted {
				t.Fatalf("SafeTruncateTailString() = %#v, want text=%q truncated=%v omitted=%d",
					got, test.text, test.truncated, test.omitted)
			}
			if !utf8.ValidString(got.Text) {
				t.Fatalf("result is invalid UTF-8: %q", got.Text)
			}
		})
	}
}

func TestSafeTruncateTailStringSingleRuneLargerThanBudget(t *testing.T) {
	// A single multibyte rune wider than maxBytes must not produce invalid UTF-8.
	got := SafeTruncateTailString(strings.Repeat("你", 10), 2)
	if !utf8.ValidString(got.Text) {
		t.Fatalf("result is invalid UTF-8: %q", got.Text)
	}
}
