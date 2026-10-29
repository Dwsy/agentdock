package httpx

import (
	"context"
	"net/http"
	"testing"

	"github.com/uvwt/agentdock/internal/observability"
)

func TestMCPContextInjectsUserAgentAndTaskID(t *testing.T) {
	header := http.Header{}
	header.Set("User-Agent", "ChatGPT/1.0 (+macOS)")
	header.Set("X-AgentDock-Task", " tsk_0123456789abcdef ")

	ctx := mcpCallContext(context.Background(), header)
	if got := observability.UserAgentFromContext(ctx); got != "ChatGPT/1.0 (+macOS)" {
		t.Fatalf("user agent = %q", got)
	}
	// 任务关联头同样走归一化：空白被剔除，空值不记录。
	if got := observability.TaskIDFromContext(ctx); got != "tsk_0123456789abcdef" {
		t.Fatalf("task id = %q", got)
	}

	empty := mcpCallContext(context.Background(), http.Header{"X-AgentDock-Task": []string{"   "}})
	if got := observability.TaskIDFromContext(empty); got != "" {
		t.Fatalf("blank task header recorded %q", got)
	}
}
