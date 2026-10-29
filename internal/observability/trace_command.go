package observability

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// traceCommandTools 是写入结构化命令证据的工具集合。它们共享 stdout/stderr/
// 退出码这套输出契约，控制面板可以复用同一份渲染逻辑。
var traceCommandTools = map[string]struct{}{
	"exec_command":    {},
	"session_observe": {},
	"session_act":     {},
}

// traceSummaryChars 是列表摘要保留的命令首行字符数。按 rune 截断，
// 避免把中文命令切成半个 UTF-8 序列。
const traceSummaryChars = 160

// CommandTrace 是命令类工具的结构化证据：入参（命令、cwd、超时、执行模式）
// 与出参（stdout/stderr/退出码）分字段保存，每侧独立截断并带标记。
// 之所以不塞进 Arguments/Result 的 JSON 文本：预览截断会破坏 JSON 合法性，
// UI 只能原文展示；结构化字段才能让详情页分段渲染并按需展开。
type CommandTrace struct {
	// Summary 是列表页专用的一行摘要（命令首行），列表请求不再下发正文。
	Summary                 string `json:"summary,omitempty"`
	Command                 string `json:"command,omitempty"`
	Workdir                 string `json:"workdir,omitempty"`
	Runtime                 string `json:"runtime,omitempty"`
	WSLDistribution         string `json:"wsl_distribution,omitempty"`
	SkillRef                string `json:"skill_ref,omitempty"`
	ExecutionMode           string `json:"execution_mode,omitempty"`
	TimeoutMS               *int   `json:"timeout_ms,omitempty"`
	YieldTimeMS             *int   `json:"yield_time_ms,omitempty"`
	MaxOutputBytes          *int   `json:"max_output_bytes,omitempty"`
	Action                  string `json:"action,omitempty"`
	SessionID               string `json:"session_id,omitempty"`
	Status                  string `json:"status,omitempty"`
	ElapsedMS               *int   `json:"elapsed_ms,omitempty"`
	Stdout                  string `json:"stdout,omitempty"`
	Stderr                  string `json:"stderr,omitempty"`
	StdoutBytes             int    `json:"stdout_bytes,omitempty"`
	StderrBytes             int    `json:"stderr_bytes,omitempty"`
	ExitCode                *int   `json:"exit_code,omitempty"`
	CommandOK               *bool  `json:"command_ok,omitempty"`
	TimedOut                bool   `json:"timed_out,omitempty"`
	StdoutTruncated         bool   `json:"stdout_truncated,omitempty"`
	StderrTruncated         bool   `json:"stderr_truncated,omitempty"`
	CommandPreviewTruncated bool   `json:"command_preview_truncated,omitempty"`
	StdoutPreviewTruncated  bool   `json:"stdout_preview_truncated,omitempty"`
	StderrPreviewTruncated  bool   `json:"stderr_preview_truncated,omitempty"`
}

// CommandTraceFrom 从工具入参与结果中抽取命令证据；非命令工具返回 nil，
// 表示该记录不需要结构化渲染。result 允许为 nil（校验失败或工具报错）。
func CommandTraceFrom(tool string, args, result map[string]any) *CommandTrace {
	if _, ok := traceCommandTools[tool]; !ok {
		return nil
	}
	trace := &CommandTrace{}
	trace.Command, _ = traceString(args, "cmd")
	trace.Workdir, _ = traceString(args, "workdir")
	trace.Runtime, _ = traceString(args, "runtime")
	trace.WSLDistribution, _ = traceString(args, "wsl_distribution")
	trace.SkillRef, _ = traceString(args, "skill_ref")
	trace.ExecutionMode, _ = traceString(args, "execution_mode")
	trace.TimeoutMS = traceIntPtr(args, "timeout_ms")
	trace.YieldTimeMS = traceIntPtr(args, "yield_time_ms")
	trace.MaxOutputBytes = traceIntPtr(args, "max_output_bytes")
	trace.Action, _ = traceString(args, "action")
	trace.SessionID, _ = traceString(args, "session_id")

	if workdir, ok := traceString(result, "workdir"); ok {
		trace.Workdir = workdir
	}
	if runtimeName, ok := traceString(result, "runtime"); ok {
		trace.Runtime = runtimeName
	}
	if distribution, ok := traceString(result, "wsl_distribution"); ok {
		trace.WSLDistribution = distribution
	}
	if sessionID, ok := traceString(result, "session_id"); ok {
		trace.SessionID = sessionID
	}
	trace.Status, _ = traceString(result, "status")
	trace.ElapsedMS = traceIntPtr(result, "elapsed_ms")
	trace.ExitCode = traceIntPtr(result, "exit_code")
	trace.CommandOK = traceBoolPtr(result, "command_ok")
	trace.TimedOut, _ = traceBool(result, "timed_out")
	trace.Stdout, _ = traceString(result, "stdout")
	trace.Stderr, _ = traceString(result, "stderr")
	trace.StdoutTruncated, _ = traceBool(result, "stdout_truncated")
	trace.StderrTruncated, _ = traceBool(result, "stderr_truncated")
	trace.StdoutBytes = traceOutputBytes(result, "stdout_total_bytes", "stdout_output_bytes", trace.Stdout)
	trace.StderrBytes = traceOutputBytes(result, "stderr_total_bytes", "stderr_output_bytes", trace.Stderr)
	trace.Summary = traceCommandSummary(trace.Command, trace.Action)
	if trace.Command == "" && trace.Stdout == "" && trace.Stderr == "" && trace.SessionID == "" && trace.Action == "" {
		return nil
	}
	return trace
}

// traceOutputBytes 优先报告子进程实际产出的总字节数：命令输出被 max_output_bytes
// 截断时，片段长度不能代表真实规模，UI 需要“1 MB 输出，只展示 8 KiB”。
func traceOutputBytes(result map[string]any, totalKey, segmentKey, segment string) int {
	size := len(segment)
	if total, ok := traceInt(result, totalKey); ok && total > size {
		size = total
	}
	if captured, ok := traceInt(result, segmentKey); ok && captured > size {
		size = captured
	}
	return size
}

// ResultSummary 生成紧凑的结果摘要，替代命令工具完整 Result 的 JSON 序列化：
// 1 MB stdout 序列化后再脱敏会把工具返回路径拖慢一个数量级，而正文已经
// 在 CommandTrace 中单独截断保存。ok=false 表示没有命令输出字段可摘要，
// 调用方应回退到通用 JSON 预览。
func (c *CommandTrace) ResultSummary() (string, bool) {
	if c == nil {
		return "", false
	}
	if c.Stdout == "" && c.Stderr == "" && c.Status == "" && c.ExitCode == nil && !c.TimedOut {
		return "", false
	}
	parts := make([]string, 0, 5)
	if c.Status != "" {
		parts = append(parts, "status="+c.Status)
	}
	if c.ExitCode != nil {
		parts = append(parts, fmt.Sprintf("exit_code=%d", *c.ExitCode))
	}
	if c.TimedOut {
		parts = append(parts, "timed_out=true")
	}
	parts = append(parts, fmt.Sprintf("stdout=%dB", c.StdoutBytes), fmt.Sprintf("stderr=%dB", c.StderrBytes))
	return strings.Join(parts, " "), true
}

// trim 在写入保留窗口前统一裁剪与脱敏命令字段，返回是否有字段被截断。
// 与 TraceRecord 的通用预览共用同一套上限和标记，避免两条截断路径漂移。
func (c *CommandTrace) trim() bool {
	command, commandTruncated := traceCommandPreview(c.Command, TraceCommandTextBytes)
	stdout, stdoutTruncated := traceCommandPreview(c.Stdout, TraceCommandTextBytes)
	stderr, stderrTruncated := traceCommandPreview(c.Stderr, TraceCommandTextBytes)
	c.Command, c.Stdout, c.Stderr = command, stdout, stderr
	c.CommandPreviewTruncated = commandTruncated
	c.StdoutPreviewTruncated = stdoutTruncated
	c.StderrPreviewTruncated = stderrTruncated
	// 摘要取自脱敏后的命令，否则列表页会把命令首行的 token 原样带出来。
	c.Summary = traceCommandSummary(c.Command, c.Action)
	return commandTruncated || stdoutTruncated || stderrTruncated
}

// searchFields 让结构化命令内容参与自由文本搜索：正文一旦从 Arguments/Result
// 移到 Command，旧的子串搜索语义必须跟着覆盖新字段。
func (c *CommandTrace) searchFields() []string {
	if c == nil {
		return nil
	}
	return []string{c.Summary, c.Command, c.Workdir, c.Runtime, c.SessionID, c.Status, c.Action, c.SkillRef, c.Stdout, c.Stderr}
}

func traceCommandSummary(command, action string) string {
	line := command
	if index := strings.IndexAny(line, "\r\n"); index >= 0 {
		line = line[:index]
	}
	line = strings.TrimSpace(line)
	if line == "" {
		line = action
	}
	runes := []rune(line)
	if len(runes) > traceSummaryChars {
		return string(runes[:traceSummaryChars]) + "…"
	}
	return string(runes)
}

// traceCommandSecretPattern 覆盖命令行常见的头部凭据写法。替换保留键名与分隔符，
// 让预览仍能看出“这里传了一个 Authorization 头”。
var traceCommandSecretPattern = regexp.MustCompile(`(?i)((?:authorization|x-api-key|api-key)\s*:\s*)(?:bearer\s+)?[^\s"']+`)

func redactCommandPayload(value string) string {
	return traceCommandSecretPattern.ReplaceAllString(redactTracePayload(value), "$1***")
}

// traceCut 在 rune 边界上截断，避免把一个多字节字符切成非法 UTF-8。
func traceCut(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

func traceString(source map[string]any, key string) (string, bool) {
	if source == nil {
		return "", false
	}
	value, ok := source[key].(string)
	if !ok || value == "" {
		return "", false
	}
	return value, true
}

func traceInt(source map[string]any, key string) (int, bool) {
	if source == nil {
		return 0, false
	}
	switch value := source[key].(type) {
	case int:
		return value, true
	case int64:
		return int(value), true
	case float64:
		return int(value), true
	case json.Number:
		parsed, err := value.Int64()
		if err != nil {
			return 0, false
		}
		return int(parsed), true
	default:
		return 0, false
	}
}

func traceBool(source map[string]any, key string) (bool, bool) {
	if source == nil {
		return false, false
	}
	value, ok := source[key].(bool)
	return value, ok
}

func traceIntPtr(source map[string]any, key string) *int {
	value, ok := traceInt(source, key)
	if !ok {
		return nil
	}
	return &value
}

func traceBoolPtr(source map[string]any, key string) *bool {
	value, ok := traceBool(source, key)
	if !ok {
		return nil
	}
	return &value
}
