package app

import (
	"context"
	"encoding/json"
	"errors"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/observability"
	toolcore "github.com/uvwt/agentdock/internal/tool/core"
)

// largeOutputCommand 在 POSIX 上产出恰好 1 MiB 的 stdout，用来验证 trace 链路
// 对 MB 级输出的上限与截断标记，而不是只测小样本。
const largeOutputCommand = "yes a | head -c 1048576"

const largeOutputBytes = 1 << 20

func TestRuntimeTraceCapturesExecCommandPayload(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("test command uses POSIX shell syntax")
	}
	rt := newTraceTestRuntime(t, config.TraceModeMemory)

	if _, err := rt.Call(context.Background(), "exec_command", map[string]any{
		"cmd":              largeOutputCommand,
		"workdir":          t.TempDir(),
		"execution_mode":   "sync",
		"timeout_ms":       10000,
		"max_output_bytes": largeOutputBytes,
	}); err != nil {
		t.Fatalf("exec_command: %v", err)
	}

	payload := mustTraces(t, rt)
	records := payload["records"].([]observability.TraceRecord)
	if len(records) != 1 {
		t.Fatalf("records = %d", len(records))
	}
	record := records[0]
	if record.Command == nil {
		t.Fatal("exec_command record has no structured command trace")
	}
	command := record.Command
	if command.Command != largeOutputCommand || !strings.Contains(command.Summary, "yes a") {
		t.Fatalf("command = %q summary = %q", command.Command, command.Summary)
	}
	if command.TimeoutMS == nil || *command.TimeoutMS != 10000 || command.ExecutionMode != "sync" {
		t.Fatalf("execution fields = %#v", command)
	}
	if command.ExitCode == nil || *command.ExitCode != 0 || command.CommandOK == nil || !*command.CommandOK {
		t.Fatalf("exit fields = %#v", command)
	}
	if command.StdoutBytes != largeOutputBytes {
		t.Fatalf("stdout bytes = %d, want %d", command.StdoutBytes, largeOutputBytes)
	}
	if len(command.Stdout) > observability.TraceCommandTextBytes+len("…(truncated)") {
		t.Fatalf("stdout preview = %d bytes", len(command.Stdout))
	}
	if !command.StdoutPreviewTruncated || !record.Truncated {
		t.Fatalf("truncation flags = %#v", record)
	}
	// 结果摘要只保留规模与退出状态：MB 级 stdout 不能再重复进 Result。
	if len(record.Result) > 256 || strings.Contains(record.Result, "aaaa") {
		t.Fatalf("result preview = %d bytes: %q", len(record.Result), record.Result)
	}
	if !strings.Contains(record.Result, "stdout=1048576B") {
		t.Fatalf("result summary = %q", record.Result)
	}
	// 入参预览保留命令本身，供 grep/回读；正文大小受通用上限约束。
	if !strings.Contains(record.Arguments, "yes a") || len(record.Arguments) > observability.TracePreviewBytes+len("…(truncated)") {
		t.Fatalf("arguments preview = %d bytes: %q", len(record.Arguments), record.Arguments)
	}

	// 完整列表响应体必须被字段上限约束住，不能随 stdout 体积线性增长。
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > 32<<10 {
		t.Fatalf("runtime traces response = %d bytes, want <= 32 KiB", len(body))
	}
	detailPayload, err := rt.RuntimeTraceDetail(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	detailBody, err := json.Marshal(detailPayload)
	if err != nil {
		t.Fatal(err)
	}
	if len(detailBody) > 32<<10 {
		t.Fatalf("trace detail response = %d bytes, want <= 32 KiB", len(detailBody))
	}
	// 锁定 Swift 端 TraceRenderView 依赖的字段名：改这里必须同步改解码器。
	for _, field := range []string{`"command"`, `"summary"`, `"stdout"`, `"stdout_bytes"`, `"exit_code"`, `"command_ok"`} {
		if !strings.Contains(string(detailBody), field) {
			t.Fatalf("detail response missing %s: %s", field, detailBody)
		}
	}
	if !strings.Contains(string(body), `"view":"full"`) {
		t.Fatalf("list response missing view marker: %s", body[:200])
	}
	t.Logf("full list response=%d bytes detail response=%d bytes (stdout=%d bytes)", len(body), len(detailBody), command.StdoutBytes)
}

func TestRuntimeTraceSummaryAndDetail(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("test command uses POSIX shell syntax")
	}
	rt := newTraceTestRuntime(t, config.TraceModeMemory)
	if _, err := rt.Call(context.Background(), "exec_command", map[string]any{
		"cmd": "printf 'hello\\n'", "execution_mode": "sync", "timeout_ms": 5000,
	}); err != nil {
		t.Fatalf("exec_command: %v", err)
	}

	summaryPayload := mustTracesQuery(t, rt, observability.TraceQuery{Limit: 10, Summary: true})
	if summaryPayload["view"] != "summary" {
		t.Fatalf("view = %#v", summaryPayload["view"])
	}
	summary := summaryPayload["records"].([]observability.TraceRecord)[0]
	if summary.Command == nil {
		t.Fatal("summary lost the command metadata")
	}
	if summary.Arguments != "" || summary.Result != "" {
		t.Fatalf("summary kept payloads: %#v", summary)
	}
	if summary.Command.Command != "" || summary.Command.Stdout != "" || summary.Command.Stderr != "" {
		t.Fatalf("summary kept command payload: %#v", summary.Command)
	}
	if summary.Command.Summary == "" || summary.Command.ExitCode == nil {
		t.Fatalf("summary lost metadata: %#v", summary.Command)
	}
	body, err := json.Marshal(summaryPayload)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > 2<<10 {
		t.Fatalf("summary response = %d bytes, want <= 2 KiB", len(body))
	}
	t.Logf("summary list response=%d bytes", len(body))

	detail, err := rt.RuntimeTraceDetail(summary.ID)
	if err != nil {
		t.Fatal(err)
	}
	full := detail["record"].(observability.TraceRecord)
	if full.Command == nil || full.Command.Stdout != "hello\n" || full.Arguments == "" {
		t.Fatalf("detail record = %#v", full)
	}
	detailBody, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("detail response=%d bytes", len(detailBody))

	for _, test := range []struct {
		name string
		id   uint64
		code string
	}{
		{name: "zero id", id: 0, code: "INVALID_ARGUMENT"},
		{name: "evicted id", id: 9999, code: "TRACE_NOT_FOUND"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := rt.RuntimeTraceDetail(test.id)
			var toolErr *toolcore.ToolError
			if !errors.As(err, &toolErr) || toolErr.Code != test.code {
				t.Fatalf("error = %#v, want %s", err, test.code)
			}
		})
	}
}

func TestRuntimeTraceCommandNilForOtherTools(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeMemory)
	if _, err := rt.Call(context.Background(), "agentdock_context", nil); err != nil {
		t.Fatal(err)
	}
	record := mustTraces(t, rt)["records"].([]observability.TraceRecord)[0]
	if record.Command != nil {
		t.Fatalf("non-command tool got structured command: %#v", record.Command)
	}
}

// TestRuntimeTraceSummaryKeepsListResponseSmall 用 100 条“每条 8 KiB stdout”的记录
// 量化列表视图：摘要响应不随输出体积增长，完整视图则接近 MB 级。
func TestRuntimeTraceSummaryKeepsListResponseSmall(t *testing.T) {
	rt := newTraceTestRuntime(t, config.TraceModeMemory)
	exitCode := 0
	stdout := strings.Repeat("build ok\n", observability.TraceCommandTextBytes/9)
	for i := 0; i < 100; i++ {
		rt.traces.Record(observability.TraceRecord{
			Tool: "exec_command", Source: "mcp", Success: true, StartedAt: time.Now(),
			Arguments: `{"cmd":"make build"}`, Result: "exit_code=0",
			Command: &observability.CommandTrace{
				Summary: "make build", Command: "make build", Stdout: stdout,
				StdoutBytes: largeOutputBytes, ExitCode: &exitCode,
			},
		})
	}

	summaryBody, err := json.Marshal(mustTracesQuery(t, rt, observability.TraceQuery{Limit: 100, Summary: true}))
	if err != nil {
		t.Fatal(err)
	}
	if len(summaryBody) > 96<<10 {
		t.Fatalf("summary response = %d bytes, want <= 96 KiB", len(summaryBody))
	}
	fullBody, err := json.Marshal(mustTracesQuery(t, rt, observability.TraceQuery{Limit: 100}))
	if err != nil {
		t.Fatal(err)
	}
	if len(fullBody) <= len(summaryBody) {
		t.Fatalf("full response %d <= summary %d", len(fullBody), len(summaryBody))
	}
	if !strings.Contains(string(summaryBody), `"summary"`) || strings.Contains(string(summaryBody), `"stdout"`) {
		t.Fatalf("summary view shape = %s", summaryBody)
	}
	t.Logf("100 records: summary=%d bytes full=%d bytes", len(summaryBody), len(fullBody))
}

func mustTracesQuery(t *testing.T, rt *Runtime, query observability.TraceQuery) Result {
	t.Helper()
	result, err := rt.RuntimeTraces(query)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
