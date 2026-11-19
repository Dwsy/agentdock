//go:build darwin || linux

package session

import (
	"context"
	"os/exec"
)

// shellCommand is kept for callers that use the platform default. New code
// should pass a ShellSpec so the configured interpreter is honoured.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	return shellCommandSpec(ctx, command, ShellSpec{})
}

// shellCommandSpec builds the command with an explicit interpreter when given,
// otherwise defaulting to bash. Bash is the default (not /bin/sh and not the
// ambient $SHELL) because exec_command's documented syntax and output
// rewriting assume bash-compatible scripts.
func shellCommandSpec(ctx context.Context, command string, spec ShellSpec) *exec.Cmd {
	shell := spec.Path
	if shell == "" {
		shell = "/bin/bash"
	}
	args := spec.shellArgs()
	return exec.CommandContext(ctx, shell, append(args, command)...)
}
