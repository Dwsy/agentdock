package file

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMatchesAnyWithDoubleStar(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		patterns []string
		want     bool
	}{
		{name: "root go file", path: "main.go", patterns: []string{"**/*.go"}, want: true},
		{name: "nested go file", path: "internal/tool/file/text.go", patterns: []string{"**/*.go"}, want: true},
		{name: "scoped go file", path: "internal/tool/file/text.go", patterns: []string{"internal/**/*.go"}, want: true},
		{name: "single star stays at root", path: "internal/tool/file/text.go", patterns: []string{"*.go"}, want: false},
		{name: "single star matches root", path: "text.go", patterns: []string{"*.go"}, want: true},
		{name: "scoped single star does not cross slash", path: "internal/a/b.go", patterns: []string{"internal/*.go"}, want: false},
		{name: "wrong extension", path: "internal/tool/file/text.md", patterns: []string{"**/*.go"}, want: false},
		{name: "empty pattern ignored", path: "internal/tool/file/text.go", patterns: []string{"", "**/*.go"}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesAny(tt.path, tt.patterns); got != tt.want {
				t.Fatalf("matchesAny(%q, %v) = %v, want %v", tt.path, tt.patterns, got, tt.want)
			}
		})
	}
}

func TestSliceTextTruncatesAtUTF8Boundary(t *testing.T) {
	content := "第一行\n第二行\n第三行"
	got, meta := sliceText(content, 1, 3, len("第一行\n第")+1)
	if !utf8.ValidString(got) {
		t.Fatalf("sliceText returned invalid UTF-8: %q", got)
	}
	if !meta.Truncated || meta.TruncatedReason != "max_bytes" {
		t.Fatalf("expected max_bytes truncation, got %#v", meta)
	}
	if meta.NextStartLine == 0 {
		t.Fatalf("expected next_start_line on truncation, got %#v", meta)
	}
}

func TestTruncateStringPreservesUTF8(t *testing.T) {
	got := truncateString("你好世界", 5)
	if !utf8.ValidString(got) {
		t.Fatalf("truncateString returned invalid UTF-8: %q", got)
	}
	if got != "你" {
		t.Fatalf("truncateString = %q, want first complete rune", got)
	}
}

func TestSliceTextHandlesEndLineBeforeStartLineWithoutPanic(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	got, meta := sliceText(b.String(), 200, 80, 0)
	if got != "" || meta.Start != 200 || meta.End != 80 || meta.Total != 301 {
		t.Fatalf("inverted range = (%q, %#v), want empty slice with total=301", got, meta)
	}
}

func TestSliceTextMatchesSplitJoinReference(t *testing.T) {
	cases := []struct {
		name      string
		content   string
		startLine int
		endLine   int
		maxBytes  int
	}{
		{"empty", "", 1, 0, 0},
		{"single line", "hello", 1, 1, 0},
		{"trailing newline middle", "a\nb\n", 2, 2, 0},
		{"trailing newline to end", "a\nb\n", 1, 0, 0},
		{"start past total", "a\nb\n", 4, 0, 0},
		{"zero endLine reads tail", "1\n2\n3\n4", 3, 0, 0},
		{"multibyte maxBytes", "第一行\n第二行\n第三行", 1, 3, len("第一行\n第") + 1},
	}

	refSliceText := func(content string, startLine, endLine int) (string, textMeta) {
		if startLine < 1 {
			startLine = 1
		}
		lines := strings.Split(content, "\n")
		total := len(lines)
		if endLine <= 0 || endLine > total {
			endLine = total
		}
		if startLine > total || startLine > endLine {
			return "", textMeta{Start: startLine, End: endLine, Total: total}
		}
		return strings.Join(lines[startLine-1:endLine], "\n"), textMeta{Start: startLine, End: endLine, Total: total}
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, gotMeta := sliceText(tc.content, tc.startLine, tc.endLine, tc.maxBytes)
			if tc.maxBytes == 0 {
				want, wantMeta := refSliceText(tc.content, tc.startLine, tc.endLine)
				if got != want || gotMeta != wantMeta {
					t.Fatalf("sliceText = (%q, %#v), want (%q, %#v)", got, gotMeta, want, wantMeta)
				}
			} else if !utf8.ValidString(got) || !gotMeta.Truncated {
				t.Fatalf("truncated slice invalid: (%q, %#v)", got, gotMeta)
			}
		})
	}
}

func BenchmarkSliceText4MWindow(b *testing.B) {
	content := strings.Repeat("func exampleLine() { return }\n", (4<<20)/30)
	b.SetBytes(int64(len(content)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, meta := sliceText(content, 1000, 1200, 0)
		if got == "" || meta.Total < 1200 {
			b.Fatalf("unexpected sliceText result: %#v", meta)
		}
	}
}
