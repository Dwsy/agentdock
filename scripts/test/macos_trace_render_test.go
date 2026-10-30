package scripts

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TraceCodeHighlighter 的 shell 分词必须按源码顺序吐出 token。
// 待发空白如果不先 flush，就会堆到后面的引号/操作符之前：命令里的参数空格
// 会被吞掉（lib.ts search 'x' 渲染成 lib.tssearch  'x'）。macOS 侧没有 Swift
// 单测框架，这里锁住分词器的 flush 顺序，防止回归。
func TestMacOSShellHighlighterKeepsTokenOrder(t *testing.T) {
	path := filepath.Join("..", "..", "desktop", "macos", "AgentDockApp", "Sources", "TraceRenderView.swift")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read TraceRenderView.swift: %v", err)
	}
	content := string(data)

	start := strings.Index(content, "private static func shellTokens(")
	if start < 0 {
		t.Fatal("cannot locate shellTokens in TraceRenderView.swift")
	}
	rest := content[start:]
	end := strings.Index(rest, "private static func shellWordKind(")
	if end < 0 {
		t.Fatal("cannot locate shellWordKind in TraceRenderView.swift")
	}
	body := rest[:end]

	normalized := regexp.MustCompile(`\s+`).ReplaceAllString(body, " ")
	if !strings.Contains(normalized, "flushPlain() tokens.append(TraceToken(text: word,") {
		t.Fatal("shell highlighter must flush pending whitespace before appending a word token")
	}
}
