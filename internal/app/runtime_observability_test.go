package app

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/observability"
	"go.opentelemetry.io/otel/trace"
)

func TestRuntimeAnalyticsRecordsSafeToolMetadata(t *testing.T) {
	runtime := newRuntimeValidationTestRuntime(t)
	ctx := observability.WithSource(context.Background(), observability.SourceMCP)
	secret := "/Users/example/private/token-file"

	if _, err := runtime.Call(ctx, "agentdock_context", map[string]any{"future_field": secret}); err == nil {
		t.Fatal("expected validation error")
	}
	if _, err := runtime.Call(ctx, "secret-tool-"+secret, nil); err == nil {
		t.Fatal("expected unknown tool error")
	}

	snapshot := runtime.observer.Snapshot()
	if snapshot.TotalCalls != 2 || snapshot.TotalErrors != 2 || snapshot.WindowCalls != 2 {
		t.Fatalf("analytics totals = calls %d errors %d window %d", snapshot.TotalCalls, snapshot.TotalErrors, snapshot.WindowCalls)
	}
	if snapshot.RecentCalls[0].Tool != "unknown" {
		t.Fatalf("unknown tool name was retained: %#v", snapshot.RecentCalls[0])
	}
	known := snapshot.RecentCalls[1]
	if known.TraceID != "" || known.SpanID != "" {
		t.Fatalf("default no-op call unexpectedly created trace identifiers = %q / %q", known.TraceID, known.SpanID)
	}
	if known.Tool != "agentdock_context" || known.Source != observability.SourceMCP {
		t.Fatalf("known call metadata = %#v", known)
	}
	if known.ErrorCode != "INVALID_ARGUMENT" || known.ErrorCategory != "validation" {
		t.Fatalf("known call error metadata = %#v", known)
	}

	encoded, err := json.Marshal(runtime.RuntimeAnalytics())
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	for _, forbidden := range []string{secret, "future_field", "secret-tool-"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("runtime analytics leaked %q: %s", forbidden, body)
		}
	}
}

func TestRuntimeAnalyticsIncludesCommandStages(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("test command uses POSIX shell syntax")
	}
	rt := newRuntimeValidationTestRuntime(t)
	result, err := rt.Call(context.Background(), "exec_command", map[string]any{
		"cmd":            "sleep 0.02",
		"execution_mode": "sync",
		"timeout_ms":     2000,
	})
	if err != nil {
		t.Fatalf("exec_command: %v", err)
	}
	if result["status"] != "exited" {
		t.Fatalf("exec_command result = %#v", result)
	}

	record := rt.observer.Snapshot().RecentCalls[0]
	if record.Tool != "exec_command" || len(record.Stages) != 2 {
		t.Fatalf("command analytics = %#v", record)
	}
	if record.Stages[0].Name != observability.StageCommandStart ||
		record.Stages[1].Name != observability.StageCommandForegroundWait {
		t.Fatalf("command stages = %#v", record.Stages)
	}
}

func TestRuntimeToolLogOmitsTraceIdentifiersWithoutSDK(t *testing.T) {
	rt := newRuntimeValidationTestRuntime(t)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	ctx := observability.WithSource(context.Background(), observability.SourceMCP)
	if _, err := rt.Call(ctx, "agentdock_context", map[string]any{}); err != nil {
		t.Fatalf("agentdock_context: %v", err)
	}
	record := rt.observer.Snapshot().RecentCalls[0]
	if record.TraceID != "" || record.SpanID != "" {
		t.Fatalf("default no-op call unexpectedly created trace identifiers: %#v", record)
	}
	body := logs.String()
	for _, forbidden := range []string{"\"trace_id\"", "\"span_id\""} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("tool log unexpectedly contains %s: %s", forbidden, body)
		}
	}
}

func TestRuntimeToolLogCorrelatesRemoteTraceWithoutFakeChildSpan(t *testing.T) {
	rt := newRuntimeValidationTestRuntime(t)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	parentSpanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	parent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: parentSpanID, TraceFlags: trace.FlagsSampled, Remote: true,
	})
	ctx := trace.ContextWithRemoteSpanContext(context.Background(), parent)
	ctx = observability.WithSource(ctx, observability.SourceNexus)
	if _, err := rt.Call(ctx, "agentdock_context", map[string]any{}); err != nil {
		t.Fatalf("agentdock_context: %v", err)
	}

	record := rt.observer.Snapshot().RecentCalls[0]
	if record.TraceID != traceID.String() || record.SpanID != "" {
		t.Fatalf("remote correlation record = %#v", record)
	}
	body := logs.String()
	if !strings.Contains(body, "\"trace_id\":\""+traceID.String()+"\"") {
		t.Fatalf("tool log missing remote trace id: %s", body)
	}
	if strings.Contains(body, "\"span_id\"") {
		t.Fatalf("tool log invented a local child span id: %s", body)
	}
}

func TestRuntimeDiagnosticsExposeOnlyRemoteSafeRecentCalls(t *testing.T) {
	runtime := newRuntimeValidationTestRuntime(t)
	ctx := observability.WithSource(context.Background(), observability.SourceNexus)
	secret := "/Users/example/private/secret.txt"
	if _, err := runtime.Call(ctx, "agentdock_context", map[string]any{"future_field": secret}); err == nil {
		t.Fatal("expected validation error")
	}

	encoded, err := json.Marshal(runtime.RuntimeDiagnostics())
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ToLower(string(encoded))
	for _, forbidden := range []string{
		strings.ToLower(secret), "span_id", "tool_stats", "process", "p50_duration_ms",
		"arguments", "output", "command", "path", "error_message", "stack",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("runtime diagnostics leaked %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, `"error_code":"invalid_argument"`) {
		t.Fatalf("runtime diagnostics missing stable error code: %s", body)
	}
}

// 调用分析要统计代码行数变动：file_edit 的增删行数进入 analytics 负载，
// 与 trace 开关无关（trace 关闭时统计依然成立）。
func TestRuntimeAnalyticsCountsCodeLineChanges(t *testing.T) {
	runtime := newTraceTestRuntime(t, config.TraceModeMemory)
	target := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(target, []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Call(context.Background(), "file_edit", map[string]any{
		"action": "replace", "path": target, "old": "two", "new": "three\nfour",
	}); err != nil {
		t.Fatal(err)
	}

	result := runtime.RuntimeAnalytics()
	if result["total_insertions"] != uint64(2) || result["total_deletions"] != uint64(1) {
		t.Fatalf("analytics totals = +%v -%v, want +2 -1", result["total_insertions"], result["total_deletions"])
	}
	stats, ok := result["tool_stats"].([]observability.ToolStats)
	if !ok || len(stats) == 0 {
		t.Fatalf("tool stats = %#v", result["tool_stats"])
	}
	found := false
	for _, stat := range stats {
		if stat.Tool != "file_edit" {
			continue
		}
		found = true
		if stat.Insertions != 2 || stat.Deletions != 1 {
			t.Fatalf("file_edit stats = %#v", stat)
		}
	}
	if !found {
		t.Fatalf("file_edit missing from tool stats: %#v", stats)
	}
}

// view_image 的 Result 带几百 KB 内联 base64，按 key 字典序排在最前，
// 会把 8 KiB 预览整段占满，让 source/image 元数据完全丢失。
// traceResultText 必须剥离 base64 并保留元数据，控制面板才能渲染。
func TestTraceResultTextStripsInlineImageBase64(t *testing.T) {
	encoded := strings.Repeat("/9j/4AAQ", 100000)
	result := Result{
		"source":               "/tmp/payment-state-desktop.png",
		"image":                map[string]any{"filename": "payment-state-desktop.png", "mime_type": "image/jpeg", "size_bytes": 750000, "width": 1280, "height": 720},
		"original":             map[string]any{"size_bytes": 900000, "width": 1600, "height": 900, "mime_type": "image/png"},
		"resized":              true,
		"warnings":             []string{},
		"_mcp_image_base64":    encoded,
		"_mcp_image_mime_type": "image/jpeg",
	}

	text := traceResultText(&result, nil)
	if strings.Contains(text, "/9j/4AAQ") {
		t.Fatal("trace result still contains inline image base64")
	}
	if len(text) > 1<<10 {
		t.Fatalf("trace result = %d bytes, want metadata only", len(text))
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("trace result is not valid JSON: %v\n%s", err, text)
	}
	if decoded["source"] != "/tmp/payment-state-desktop.png" {
		t.Fatalf("trace result lost source: %#v", decoded["source"])
	}
	if _, ok := decoded["image"]; !ok {
		t.Fatalf("trace result lost image metadata: %#v", decoded)
	}
	omitted, _ := decoded["image_bytes_omitted"].(float64)
	if omitted <= 0 {
		t.Fatalf("trace result lost omitted size: %#v", decoded["image_bytes_omitted"])
	}
	// 原 Result 不能被 trace 改写：调用方还要把 base64 交给 MCP 传输层。
	if result["_mcp_image_base64"] != encoded {
		t.Fatal("trace result mutated the original Result")
	}
}
