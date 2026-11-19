package config

import "testing"

func TestFromEnvCommandShellDefaultsToBash(t *testing.T) {
	t.Setenv("AGENTDOCK_SHELL", "")
	t.Setenv("SHELL", "/usr/bin/fish") // ambient fish must not leak into the default
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CommandShell != "/bin/bash" {
		t.Fatalf("CommandShell = %q, want /bin/bash (ambient SHELL ignored)", cfg.CommandShell)
	}
	if len(cfg.CommandShellArgs) != 1 || cfg.CommandShellArgs[0] != "-c" {
		t.Fatalf("CommandShellArgs = %v, want [-c]", cfg.CommandShellArgs)
	}
	if cfg.RTKEnabled {
		t.Fatal("RTKEnabled default = true, want false")
	}
}

func TestFromEnvCommandShellOverride(t *testing.T) {
	t.Setenv("AGENTDOCK_SHELL", "/bin/zsh")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CommandShell != "/bin/zsh" {
		t.Fatalf("CommandShell = %q, want /bin/zsh", cfg.CommandShell)
	}
	// An explicit AGENTDOCK_SHELL is required; ambient SHELL is never inherited.
	t.Setenv("AGENTDOCK_SHELL", "")
	t.Setenv("SHELL", "/bin/sh")
	cfg2, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.CommandShell != "/bin/bash" {
		t.Fatalf("CommandShell fallback = %q, want /bin/bash (ambient SHELL ignored)", cfg2.CommandShell)
	}
}

func TestFromEnvRTKEnabled(t *testing.T) {
	t.Setenv("AGENTDOCK_RTK_ENABLED", "true")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.RTKEnabled {
		t.Fatal("RTKEnabled = false, want true")
	}
}
