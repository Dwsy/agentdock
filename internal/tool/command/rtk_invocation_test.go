package command

import (
	"context"
	"strings"
	"testing"
)

func TestInvocationAppliesRTKRewriteAndShell(t *testing.T) {
	service, cfg := newCommandTestService(t)
	cfg.RTKEnabled = true
	cfg.CommandShell = "/bin/bash"
	cfg.CommandShellArgs = []string{"-c"}

	prefix := rtkPrefix()
	if prefix == "" {
		t.Skip("rtk binary not available on PATH")
	}

	inv, err := service.newHostCommandInvocation(context.Background(), ExecRequest{
		Cmd: "git status --short",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(inv.command, prefix) || !strings.Contains(inv.command, "git status") {
		t.Fatalf("invocation command = %q, want rtk-prefixed git", inv.command)
	}
	if inv.shell.Path != "/bin/bash" {
		t.Fatalf("invocation shell = %q, want /bin/bash", inv.shell.Path)
	}
	// RTK_DB_PATH must be injected so rtk can record savings.
	found := false
	for _, e := range inv.env {
		if strings.HasPrefix(e, "RTK_DB_PATH=") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no RTK_DB_PATH in invocation env: %v", inv.env)
	}
}

func TestInvocationDoesNotRewriteWhenRTKDisabled(t *testing.T) {
	service, cfg := newCommandTestService(t)
	cfg.RTKEnabled = false
	inv, err := service.newHostCommandInvocation(context.Background(), ExecRequest{
		Cmd: "git status --short",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(inv.command, "rtk") {
		t.Fatalf("command was rewritten with rtk while disabled: %q", inv.command)
	}
	if inv.shell.Path != "" && inv.shell.Path != "/bin/bash" {
		t.Fatalf("unexpected shell from test config: %q", inv.shell.Path)
	}
}
