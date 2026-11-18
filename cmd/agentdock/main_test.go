package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/buildinfo"
	"github.com/uvwt/agentdock/internal/desktopruntime"
)

func TestRunPrintsVersionWithoutLoadingServerConfiguration(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(context.Background(), []string{"--version"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "AgentDock v"+strings.TrimPrefix(buildinfo.Version, "v")) || !strings.Contains(stdout.String(), "platform:") {
		t.Fatalf("unexpected version output: %s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestRunPrintsMachineReadableBuildInfo(t *testing.T) {
	var stdout bytes.Buffer
	if err := run(context.Background(), []string{"version", "--json"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var info buildinfo.Info
	if err := json.Unmarshal(stdout.Bytes(), &info); err != nil {
		t.Fatalf("version --json returned invalid JSON: %v", err)
	}
	if info.Version != buildinfo.Version || info.Platform == "" || info.GoVersion == "" {
		t.Fatalf("unexpected build info: %#v", info)
	}
}

func TestRunRejectsUnexpectedUpdateArguments(t *testing.T) {
	err := run(context.Background(), []string{"update", "--check", "extra"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "agentdock update [--check|--progress-json|--local-archive") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInstallEngineReadyDoesNotStartServer(t *testing.T) {
	stdout := &bytes.Buffer{}
	if err := run(context.Background(), []string{"install", "--engine-ready"}, stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "agentdock-installer-engine") {
		t.Fatalf("engine-ready handshake missing: %q", stdout.String())
	}
}

func TestInstallInspectRequiresStateRoot(t *testing.T) {
	err := run(context.Background(), []string{"install", "inspect"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--state-root") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInstallCommitRequiresInstallRoot(t *testing.T) {
	err := run(context.Background(), []string{"install", "commit"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--install-root") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInstallAbandonRequiresInstallRoot(t *testing.T) {
	err := run(context.Background(), []string{"install", "abandon"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--install-root") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrepareWindowsLegacyIsRejectedOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows implementation has its own native tests")
	}
	err := run(context.Background(), []string{"install", "prepare-windows-legacy"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "仅支持 Windows") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDetachEngineIsRejectedOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows implementation has its own native tests")
	}
	err := run(context.Background(), []string{"install", "detach-engine"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "仅支持 Windows") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	err := run(context.Background(), []string{"unknown"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "未知命令或参数") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNexusStatusReportsUnpairedWithoutExposingToken(t *testing.T) {
	t.Setenv("AGENTDOCK_HOME", t.TempDir())
	var stdout bytes.Buffer
	if err := run(context.Background(), []string{"nexus", "status", "--json"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var status struct {
		Paired            bool `json:"paired"`
		DeviceTokenStored bool `json:"device_token_stored"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("nexus status --json returned invalid JSON: %v", err)
	}
	if status.Paired || status.DeviceTokenStored || strings.Contains(stdout.String(), "device_token\"") {
		t.Fatalf("unexpected unpaired status: %s", stdout.String())
	}
}

func TestDefaultRuntimeRootUsesEnvironmentOverride(t *testing.T) {
	runtimeRoot := t.TempDir()
	t.Setenv("AGENTDOCK_RUNTIME_ROOT", runtimeRoot)
	if got := desktopruntime.DefaultRuntimeRoot(); got != runtimeRoot {
		t.Fatalf("default runtime root = %q, want %q", got, runtimeRoot)
	}
}

func TestRunTunnelHelp(t *testing.T) {
	var stdout bytes.Buffer
	if err := run(context.Background(), []string{"tunnel", "-h"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if output := stdout.String(); !strings.Contains(output, "agentdock tunnel status") || !strings.Contains(output, "[--runtime-root <目录>]") {
		t.Fatalf("unexpected tunnel help: %s", output)
	}
}

func TestLoadConfiguredEnvFilePrecedenceAndFailures(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "custom.env")
	if err := os.WriteFile(envPath, []byte("AGENTDOCK_MANUAL_LLM_ENABLED=true\nAGENTDOCK_MANUAL_LLM_PREVIEW_BYTES=2400\nAGENTDOCK_HOST=0.0.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Isolate and auto-restore any modified env vars cleanly.
	for _, k := range []string{"AGENTDOCK_ENV_FILE", "AGENTDOCK_RUNTIME_ROOT", "AGENTDOCK_MANUAL_LLM_ENABLED", "AGENTDOCK_MANUAL_LLM_PREVIEW_BYTES", "AGENTDOCK_HOST"} {
		prev, existed := os.LookupEnv(k)
		_ = os.Unsetenv(k)
		t.Cleanup(func() {
			if existed {
				_ = os.Setenv(k, prev)
			} else {
				_ = os.Unsetenv(k)
			}
		})
	}
	_ = os.Setenv("AGENTDOCK_HOST", "127.0.0.1")

	loaded, err := loadConfiguredEnvFile([]string{"--env-file=" + envPath})
	if err != nil {
		t.Fatalf("loadConfiguredEnvFile(--env-file) error = %v", err)
	}
	if loaded != envPath {
		t.Fatalf("loaded = %q, want %q", loaded, envPath)
	}
	if got := os.Getenv("AGENTDOCK_MANUAL_LLM_ENABLED"); got != "true" {
		t.Fatalf("AGENTDOCK_MANUAL_LLM_ENABLED = %q, want true", got)
	}
	if got := os.Getenv("AGENTDOCK_MANUAL_LLM_PREVIEW_BYTES"); got != "2400" {
		t.Fatalf("AGENTDOCK_MANUAL_LLM_PREVIEW_BYTES = %q, want 2400", got)
	}
	// Explicit env takes precedence over file value.
	if got := os.Getenv("AGENTDOCK_HOST"); got != "127.0.0.1" {
		t.Fatalf("AGENTDOCK_HOST = %q, want 127.0.0.1 override", got)
	}
	if got := os.Getenv("AGENTDOCK_ENV_FILE"); got != envPath {
		t.Fatalf("AGENTDOCK_ENV_FILE = %q, want %q", got, envPath)
	}

	// Explicit missing file must error.
	if _, err := loadConfiguredEnvFile([]string{"--env-file", filepath.Join(dir, "missing.env")}); err == nil {
		t.Fatal("expected error for missing explicit --env-file")
	}

	// Implicit missing agentdock.env under AGENTDOCK_RUNTIME_ROOT should be ignored.
	_ = os.Unsetenv("AGENTDOCK_ENV_FILE")
	_ = os.Setenv("AGENTDOCK_RUNTIME_ROOT", filepath.Join(dir, "empty-runtime"))
	if got, err := loadConfiguredEnvFile(nil); err != nil || got != "" {
		t.Fatalf("expected missing implicit runtime env ignored cleanly, got (%q, %v)", got, err)
	}
}
