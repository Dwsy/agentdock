package command

import (
	"testing"

	"github.com/uvwt/agentdock/internal/config"
)

const testRTKPrefix = "/opt/homebrew/bin/rtk "

func TestRewriteWithPrefix(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    string
		changed bool
	}{
		{name: "git", command: "git status --short", want: testRTKPrefix + "git status --short", changed: true},
		{name: "go test", command: "go test ./...", want: testRTKPrefix + "go test ./...", changed: true},
		{name: "rg", command: "rg -n foo .", want: testRTKPrefix + "rg -n foo .", changed: true},
		{name: "cat", command: "cat file.txt", want: testRTKPrefix + "cat file.txt", changed: true},
		{name: "env prefix", command: "FOO=bar git log", want: "FOO=bar " + testRTKPrefix + "git log", changed: true},
		{name: "not allowlisted", command: "echo hello", want: "echo hello", changed: false},
		{name: "pipeline skipped", command: "git log | head -20", want: "git log | head -20", changed: false},
		{name: "redirect passes through rtk", command: "rg foo > out.txt", want: testRTKPrefix + "rg foo > out.txt", changed: true},
		{name: "subshell skipped", command: "echo $(git log)", want: "echo $(git log)", changed: false},
		{name: "already rtk", command: testRTKPrefix + "git status", want: testRTKPrefix + "git status", changed: false},
		{name: "quoted env prefix skipped", command: `FOO="a b" git log`, want: `FOO="a b" git log`, changed: false},
		{name: "empty", command: "   ", want: "   ", changed: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, changed := rewriteWithPrefix(test.command, testRTKPrefix)
			if got != test.want || changed != test.changed {
				t.Fatalf("rewriteWithPrefix(%q) = %q,%v want %q,%v", test.command, got, changed, test.want, test.changed)
			}
		})
	}
}

func TestRewriteCommandForRTKGates(t *testing.T) {
	base := config.Config{RTKEnabled: true, CommandShell: "/bin/bash"}
	prefix := rtkPrefix()
	if prefix != "" {
		if got, changed := rewriteCommandForRTK(base, "git status"); !changed || got != prefix+"git status" {
			t.Fatalf("enabled bash: got %q changed=%v (rtkPrefix=%q)", got, changed, prefix)
		}
	}
	// Disabled is always a no-op.
	disabled := base
	disabled.RTKEnabled = false
	if got, changed := rewriteCommandForRTK(disabled, "git status"); changed || got != "git status" {
		t.Fatalf("disabled: got %q changed=%v", got, changed)
	}
	// fish is not bash-compatible -> no rewrite.
	fish := base
	fish.CommandShell = "/usr/bin/fish"
	if got, changed := rewriteCommandForRTK(fish, "git status"); changed || got != "git status" {
		t.Fatalf("fish: got %q changed=%v", got, changed)
	}
}

func TestIsBashShell(t *testing.T) {
	for shell, want := range map[string]bool{
		"": true, "/bin/bash": true, "bash": true, "sh": true,
		"/bin/zsh": false, "/usr/bin/fish": false,
	} {
		if got := isBashShell(shell); got != want {
			t.Fatalf("isBashShell(%q) = %v, want %v", shell, got, want)
		}
	}
}
