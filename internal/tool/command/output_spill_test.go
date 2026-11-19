package command

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestExecDefaultsSpillLargeOutputToTempFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test command uses POSIX shell syntax")
	}
	service, cfg := newCommandTestService(t)
	// 5000 行远超默认 2000 行上限，触发 tail + spill。
	cmd := `i=0; while [ $i -lt 5000 ]; do printf 'line-%04d\n' "$i"; i=$((i+1)); done`
	result, err := service.execArgs(context.Background(), map[string]any{
		"cmd":            cmd,
		"execution_mode": "sync",
		"timeout_ms":     10000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "exited" {
		t.Fatalf("status = %v, want exited: %#v", result["status"], result)
	}
	path, ok := result["full_output_path"].(string)
	if !ok || path == "" {
		t.Fatalf("no full_output_path in result: %#v", result)
	}
	if !strings.HasPrefix(path, cfg.AgentDockHome) {
		t.Fatalf("spill path %q is not under AgentDockHome %q", path, cfg.AgentDockHome)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read spill file: %v", err)
	}
	if !strings.Contains(string(data), "line-0000") || !strings.Contains(string(data), "line-4999") {
		t.Fatalf("spill file missing full output (head or tail): %d bytes", len(data))
	}
	// stdout in the model-facing result is a short tail.
	stdout, _ := result["stdout"].(string)
	if strings.Count(stdout, "\n") > 2000 {
		t.Fatalf("stdout lines = %d, want <= 2000 tail", strings.Count(stdout, "\n"))
	}
	if result["stdout_truncated"] != true {
		t.Fatalf("stdout_truncated = %v, want true: %#v", result["stdout_truncated"], result)
	}
	if _, ok := result["output_notice"]; !ok {
		t.Fatalf("no output_notice hint in result: %#v", result)
	}
}

func TestExecSmallOutputDoesNotSpill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test command uses POSIX shell syntax")
	}
	service, _ := newCommandTestService(t)
	result, err := service.execArgs(context.Background(), map[string]any{
		"cmd":            "printf 'hello\n'",
		"execution_mode": "sync",
		"timeout_ms":     10000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result["full_output_path"]; ok {
		t.Fatalf("small output should not spill: %#v", result)
	}
	if result["stdout"] != "hello\n" {
		t.Fatalf("stdout = %q, want hello", result["stdout"])
	}
}
