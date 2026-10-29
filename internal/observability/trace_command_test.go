package observability

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func newMemoryTraceStore(t *testing.T) *TraceStore {
	t.Helper()
	store, err := NewTraceStore(TraceOptions{Mode: TraceModeMemory, MaxRecords: 10})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestCommandTraceFromExecCommand(t *testing.T) {
	args := map[string]any{
		"cmd":              "git status --short",
		"workdir":          "/repo",
		"runtime":          "windows",
		"wsl_distribution": "Ubuntu",
		"skill_ref":        "core-skills/git",
		"execution_mode":   "sync",
		// MCP 参数经 JSON 解码后数字是 float64；混入 json.Number 覆盖两条解码路径。
		"timeout_ms":       float64(15000),
		"yield_time_ms":    json.Number("5000"),
		"max_output_bytes": 65536,
		"stdin":            "not-copied-into-command-trace",
	}
	result := map[string]any{
		"session_id":         "cmd_1",
		"status":             "exited",
		"exit_code":          2,
		"command_ok":         false,
		"timed_out":          true,
		"elapsed_ms":         12,
		"stdout":             " M a.go\n",
		"stderr":             "fatal: not a repo\n",
		"stdout_total_bytes": 900,
		"stderr_total_bytes": 22,
		"stdout_truncated":   true,
		"stderr_truncated":   false,
	}

	trace := CommandTraceFrom("exec_command", args, result)
	if trace == nil {
		t.Fatal("exec_command must produce a command trace")
	}
	if trace.Command != "git status --short" || trace.Summary != "git status --short" {
		t.Fatalf("command = %q summary = %q", trace.Command, trace.Summary)
	}
	if trace.Workdir != "/repo" || trace.Runtime != "windows" || trace.WSLDistribution != "Ubuntu" || trace.SkillRef != "core-skills/git" {
		t.Fatalf("context fields = %#v", trace)
	}
	if trace.ExecutionMode != "sync" || trace.TimeoutMS == nil || *trace.TimeoutMS != 15000 {
		t.Fatalf("execution fields = %#v", trace)
	}
	if trace.YieldTimeMS == nil || *trace.YieldTimeMS != 5000 || trace.MaxOutputBytes == nil || *trace.MaxOutputBytes != 65536 {
		t.Fatalf("yield/max output = %#v", trace)
	}
	if trace.SessionID != "cmd_1" || trace.Status != "exited" || trace.ExitCode == nil || *trace.ExitCode != 2 {
		t.Fatalf("result fields = %#v", trace)
	}
	if trace.CommandOK == nil || *trace.CommandOK || !trace.TimedOut || trace.ElapsedMS == nil || *trace.ElapsedMS != 12 {
		t.Fatalf("status fields = %#v", trace)
	}
	if trace.Stdout != " M a.go\n" || trace.Stderr != "fatal: not a repo\n" {
		t.Fatalf("output = %#v", trace)
	}
	if trace.StdoutBytes != 900 || trace.StderrBytes != 22 {
		t.Fatalf("output sizes = %d/%d", trace.StdoutBytes, trace.StderrBytes)
	}
	if !trace.StdoutTruncated || trace.StderrTruncated {
		t.Fatalf("tool level truncation = %#v", trace)
	}
	// stdin 可能是密码或密钥，结构化证据不复制；它只出现在通用入参预览里。
	encoded, err := json.Marshal(trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "not-copied-into-command-trace") {
		t.Fatalf("stdin leaked into command trace: %s", encoded)
	}

	if got := CommandTraceFrom("read_file", args, result); got != nil {
		t.Fatalf("non-command tool got a command trace: %#v", got)
	}
	if got := CommandTraceFrom("exec_command", map[string]any{}, nil); got != nil {
		t.Fatalf("empty command must not produce a trace: %#v", got)
	}
}

func TestCommandTraceResultSummary(t *testing.T) {
	tests := []struct {
		name   string
		result map[string]any
		want   string
		wantOK bool
	}{
		{name: "no output fields", result: map[string]any{"count": 1}, wantOK: false},
		{name: "exit only", result: map[string]any{"status": "exited", "exit_code": 0}, wantOK: true},
		{name: "without exit code", result: map[string]any{"stdout": "x"}, wantOK: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			trace := CommandTraceFrom("session_observe", map[string]any{"action": "status"}, test.result)
			if trace == nil {
				t.Fatal("session_observe must produce a command trace")
			}
			summary, ok := trace.ResultSummary()
			if ok != test.wantOK {
				t.Fatalf("ok = %v, want %v (summary=%q)", ok, test.wantOK, summary)
			}
			if !ok {
				return
			}
			if !strings.Contains(summary, "stdout=") || !strings.Contains(summary, "stderr=") {
				t.Fatalf("summary = %q", summary)
			}
			if len(summary) > 256 {
				t.Fatalf("summary too long: %d", len(summary))
			}
		})
	}
}

func TestTraceStoreCommandPreviewLimits(t *testing.T) {
	longCommand := "echo " + strings.Repeat("a", TraceCommandTextBytes*2)
	tests := []struct {
		name          string
		stdout        string
		wantBytes     int
		wantTruncated bool
	}{
		{name: "under limit", stdout: strings.Repeat("a", 1024), wantBytes: 1024},
		{name: "at limit", stdout: strings.Repeat("a", TraceCommandTextBytes), wantBytes: TraceCommandTextBytes},
		{name: "one megabyte", stdout: strings.Repeat("a", 1<<20), wantBytes: 1 << 20, wantTruncated: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newMemoryTraceStore(t)
			store.Record(TraceRecord{
				Tool: "exec_command", Success: true, StartedAt: time.Now(),
				Command: &CommandTrace{
					Command:     longCommand,
					Stdout:      test.stdout,
					StdoutBytes: len(test.stdout),
				},
			})
			record := store.Query(TraceQuery{})[0]
			if record.Command == nil {
				t.Fatal("command trace was dropped")
			}
			if len(record.Command.Stdout) > TraceCommandTextBytes+len(traceTruncatedMarker) {
				t.Fatalf("stdout preview = %d bytes", len(record.Command.Stdout))
			}
			if record.Command.StdoutPreviewTruncated != test.wantTruncated {
				t.Fatalf("stdout preview truncated = %v, want %v", record.Command.StdoutPreviewTruncated, test.wantTruncated)
			}
			if record.Command.StdoutBytes != test.wantBytes {
				t.Fatalf("stdout bytes = %d, want %d", record.Command.StdoutBytes, test.wantBytes)
			}
			// 命令自身超过上限也必须截断，不能只限制输出。
			if !record.Command.CommandPreviewTruncated || len(record.Command.Command) > TraceCommandTextBytes+len(traceTruncatedMarker) {
				t.Fatalf("command preview = %d bytes truncated=%v", len(record.Command.Command), record.Command.CommandPreviewTruncated)
			}
			if !record.Truncated {
				t.Fatal("record level truncated flag is false")
			}
		})
	}
}

func TestTraceStoreCommandPreviewRedactsSecrets(t *testing.T) {
	store := newMemoryTraceStore(t)
	store.Record(TraceRecord{
		Tool: "exec_command", Success: true, StartedAt: time.Now(),
		Command: &CommandTrace{
			Command: `curl -H "Authorization: Bearer abcdef" "https://x?token=supersecret"`,
			Stdout:  `{"api_key":"stdout-secret"}` + "\npassword=stdout-password\n",
			Stderr:  "GITHUB_TOKEN=ghp_abcdefghijklmnop",
		},
	})
	record := store.Query(TraceQuery{})[0]
	serialized, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"supersecret", "stdout-secret", "stdout-password", "Bearer abcdef", "ghp_abcdefghijklmnop"} {
		if strings.Contains(string(serialized), forbidden) {
			t.Fatalf("secret %q leaked: %s", forbidden, serialized)
		}
	}
	if !strings.Contains(record.Command.Command, "Authorization:") {
		t.Fatalf("redaction dropped the header name: %q", record.Command.Command)
	}
	if record.Command.Summary == "" {
		t.Fatal("summary must stay populated after redaction")
	}
	if strings.Contains(record.Command.Summary, "supersecret") {
		t.Fatalf("summary leaked secret: %q", record.Command.Summary)
	}
}

func TestTraceStoreSummaryQueryAndRecordByID(t *testing.T) {
	store := newMemoryTraceStore(t)
	store.Record(TraceRecord{
		Tool: "exec_command", Source: "mcp", Success: true, StartedAt: time.Now(),
		Arguments: `{"cmd":"go test ./..."}`,
		Result:    "exit_code=0",
		Command: &CommandTrace{
			Summary: "go test ./...", Command: "go test ./...", Workdir: "/repo",
			Stdout: "ok\n", Stderr: "warn\n", StdoutBytes: 3, StderrBytes: 5,
			ExitCode: traceIntPtr(map[string]any{"exit_code": 0}, "exit_code"),
		},
	})

	summary := store.Query(TraceQuery{Summary: true, Query: "go test"})
	if len(summary) != 1 {
		t.Fatalf("summary query = %#v", summary)
	}
	if summary[0].Arguments != "" || summary[0].Result != "" {
		t.Fatalf("summary kept payloads: %#v", summary[0])
	}
	if summary[0].Command == nil || summary[0].Command.Stdout != "" || summary[0].Command.Stderr != "" || summary[0].Command.Command != "" {
		t.Fatalf("summary kept command payload: %#v", summary[0].Command)
	}
	if summary[0].Command.Summary != "go test ./..." || summary[0].Command.ExitCode == nil {
		t.Fatalf("summary lost metadata: %#v", summary[0].Command)
	}

	// 摘要投影不能破坏保留窗口里的完整记录。
	full := store.Query(TraceQuery{})
	if full[0].Arguments == "" || full[0].Command.Stdout != "ok\n" {
		t.Fatalf("full record was mutated: %#v", full[0])
	}

	detail, ok := store.RecordByID(full[0].ID)
	if !ok || detail.Command == nil || detail.Command.Stdout != "ok\n" {
		t.Fatalf("RecordByID = %#v ok=%v", detail, ok)
	}
	if _, ok := store.RecordByID(999); ok {
		t.Fatal("unknown id must not resolve")
	}
	if _, ok := store.RecordByID(0); ok {
		t.Fatal("zero id must not resolve")
	}
}

func TestTraceStoreTruncatesBeforeRedaction(t *testing.T) {
	// 预处理窗口先于脱敏：1 MiB 输出即使结尾带密钥，最终预览也只能来自
	// 前 traceRedactionScanBytes 字节。这里只断言上限与标记，耗时由 benchmark 验证。
	store := newMemoryTraceStore(t)
	payload := strings.Repeat("line\n", 200000) + "token=tail-secret"
	store.Record(TraceRecord{Tool: "exec_command", Command: &CommandTrace{Stdout: payload}})
	record := store.Query(TraceQuery{})[0]
	if len(record.Command.Stdout) > TraceCommandTextBytes+len(traceTruncatedMarker) {
		t.Fatalf("preview length = %d", len(record.Command.Stdout))
	}
	if !record.Command.StdoutPreviewTruncated || !record.Truncated {
		t.Fatalf("truncation flags = %#v", record)
	}
}

// BenchmarkTraceRecordLargeCommandOutput 量化大输出记录的采集成本：
// 旧路径对整份 JSON 预览跑脱敏正则，1 MiB 约 65 ms；现在只扫描 64 KiB。
func BenchmarkTraceRecordLargeCommandOutput(b *testing.B) {
	store, err := NewTraceStore(TraceOptions{Mode: TraceModeMemory, MaxRecords: 10})
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	stdout := strings.Repeat("build line with token=abc123 and some payload\n", 24000)
	b.SetBytes(int64(len(stdout)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.Record(TraceRecord{
			Tool: "exec_command", StartedAt: time.Now(),
			Command: &CommandTrace{Command: "make build", Stdout: stdout, StdoutBytes: len(stdout)},
		})
	}
}

// BenchmarkTracePreviewLargePayload 对比旧路径（整份文本脱敏）与新路径
// （先裁扫描窗口再脱敏）的差距，作为“大输出不拖垮调用路径”的量化证据。
func BenchmarkTracePreviewLargePayload(b *testing.B) {
	payload := strings.Repeat(`{"stdout":"line with token=abc123 and \"quoted\" value"}`+"\n", 18000)
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tracePreview(payload)
	}
}

func TestTraceStoreDiskPersistsCommandTrace(t *testing.T) {
	root := t.TempDir()
	options := TraceOptions{
		Mode: TraceModeDisk, Root: root, Retention: 24 * time.Hour,
		BufferSize: 1, FlushInterval: 10 * time.Millisecond, MaxRecords: 10,
	}
	store, err := NewTraceStore(options)
	if err != nil {
		t.Fatal(err)
	}
	exitCode := 0
	store.Record(TraceRecord{
		Tool: "exec_command", Source: "mcp", Success: true, StartedAt: time.Now(),
		Command: &CommandTrace{
			Command: "printf hi", Workdir: "/repo", Stdout: "hi\n", StdoutBytes: 3, ExitCode: &exitCode,
		},
	})
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewTraceStore(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reloaded.Close() })
	records := reloaded.Query(TraceQuery{})
	if len(records) != 1 || records[0].Command == nil {
		t.Fatalf("reloaded records = %#v", records)
	}
	detail, ok := reloaded.RecordByID(records[0].ID)
	if !ok || detail.Command.Command != "printf hi" || detail.Command.Stdout != "hi\n" || detail.Command.ExitCode == nil {
		t.Fatalf("reloaded command = %#v", detail.Command)
	}
}

// BenchmarkLegacyTracePreview 复现改造前的采集路径（整份文本先脱敏再截断），
// 只用于对照，不作为生产路径。1 MiB 量级约 60 ms，是现在路径的数十倍。
func BenchmarkLegacyTracePreview(b *testing.B) {
	payload := strings.Repeat(`{"stdout":"line with token=abc123 and \"quoted\" value"}`+"\n", 18000)
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		redacted := redactTracePayload(payload)
		if len(redacted) > TracePreviewBytes {
			redacted = redacted[:TracePreviewBytes]
		}
		_ = redacted
	}
}

func TestTracePreviewRedactsUnterminatedJSONSecret(t *testing.T) {
	// 预截断可能把一个超长敏感值切成未闭合字符串：此时键值规则匹配不到，
	// 必须由“未闭合敏感值”规则兜底，宁可丢预览尾部也不泄漏凭据前缀。
	tests := []struct {
		name    string
		payload string
	}{
		{
			name:    "giant token value",
			payload: `{"api_key":"` + strings.Repeat("A", 1<<20) + `"}`,
		},
		{
			name:    "truncated in the middle of preview",
			payload: strings.Repeat("x", TracePreviewBytes-16) + `"password":"` + strings.Repeat("B", 1<<20),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			preview, truncated := tracePreview(test.payload)
			if !truncated {
				t.Fatal("oversized payload was not marked truncated")
			}
			for _, forbidden := range []string{"AAAA", "BBBB"} {
				if strings.Contains(preview, forbidden) {
					t.Fatalf("unterminated secret leaked: %q", preview[max(0, len(preview)-120):])
				}
			}
		})
	}
}
