package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
)

func remoteAccessTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGENTDOCK_HOME", filepath.Join(home, ".agentdock"))
	t.Setenv("AGENTDOCK_RUNTIME_ROOT", filepath.Join(home, "runtime-root"))
	cfg := config.Config{
		AgentDockDefaultDir: home,
		AgentDockHome:       filepath.Join(home, ".agentdock"),
		LogLevel:            "info",
		MCPAppsMode:         config.MCPAppsModeFull,
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatalf("Normalize config: %v", err)
	}
	runtime, err := NewRuntime(cfg)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	t.Cleanup(func() { runtime.Close() })
	return runtime
}

func TestRuntimeRemoteTokenUpdateValidatesAndPersists(t *testing.T) {
	runtime := remoteAccessTestRuntime(t)
	envPath := filepath.Join(t.TempDir(), "agentdock.env")
	t.Setenv("AGENTDOCK_ENV_FILE", envPath)
	if err := os.WriteFile(envPath, []byte("AGENTDOCK_PORT=8765\n"), 0o600); err != nil {
		t.Fatalf("seed env file: %v", err)
	}

	cases := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"too short", "short-token"},
		{"contains space", "validlength token value!"},
		{"contains control", "validlength\x00token"},
	}
	for _, tc := range cases {
		if _, err := runtime.RuntimeRemoteTokenUpdate(tc.token); err == nil {
			t.Fatalf("%s: expected validation error", tc.name)
		}
	}

	const token = "test-token-0123456789abcdef"
	result, err := runtime.RuntimeRemoteTokenUpdate(token)
	if err != nil {
		t.Fatalf("RuntimeRemoteTokenUpdate: %v", err)
	}
	if hint, _ := result["restart_hint"].(string); hint == "" {
		t.Fatalf("expected restart_hint in result: %#v", result)
	}
	raw, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	// envstore.Marshal 会给值加引号，断言键与值分开匹配。
	if !strings.Contains(string(raw), "AGENTDOCK_AUTH_TOKEN=") || !strings.Contains(string(raw), token) {
		t.Fatalf("env file missing new token: %s", raw)
	}
	// 未知键必须保留：轮换 token 不能顺手抹掉其它配置。
	if !strings.Contains(string(raw), "AGENTDOCK_PORT=") {
		t.Fatalf("env file lost existing keys: %s", raw)
	}
}

func TestRuntimeRemoteAccessNeverEchoesSecrets(t *testing.T) {
	runtime := remoteAccessTestRuntime(t)
	// 注入假的 nexus status 响应：验证映射链路，避免单测真的派生子进程
	// （os.Executable 在测试里是 test binary，会重新拉起整个测试套件）。
	const pairedEndpoint = "https://nexus.example.com"
	originalNexusStatus := execNexusStatusCommand
	execNexusStatusCommand = func(ctx context.Context, self, home string) ([]byte, error) {
		return []byte(`{"paired":true,"endpoint":"` + pairedEndpoint + `","node_id":"node-1","device_id":"dev-1","device_token_stored":true,"server_edition":"self-hosted","protocol_version":"2"}`), nil
	}
	t.Cleanup(func() { execNexusStatusCommand = originalNexusStatus })

	const token = "super-secret-token-value"
	cfg := runtime.cfg
	cfg.AuthToken = token
	runtime.cfg = cfg

	result := runtime.RuntimeRemoteAccess(t.Context())
	if ok, _ := result["ok"].(bool); !ok {
		t.Fatalf("expected ok status: %#v", result)
	}
	if configured, _ := result["auth_token_configured"].(bool); !configured {
		t.Fatal("expected auth_token_configured=true")
	}
	// 密钥值绝不进响应：全量序列化里不允许出现 token 原文。
	assertResultOmits(t, result, token)
	nexus, _ := result["nexus"].(map[string]any)
	if nexus == nil {
		t.Fatalf("expected nexus map: %#v", result)
	}
	if endpoint, _ := nexus["endpoint"].(string); endpoint != pairedEndpoint {
		t.Fatalf("expected injected endpoint in nexus map: %#v", nexus)
	}
	if _, hasTunnel := result["tunnel"]; !hasTunnel {
		t.Fatalf("expected tunnel entry (nil or map): %#v", result)
	}
}

func TestRuntimeTunnelConfigureValidatesInput(t *testing.T) {
	runtime := remoteAccessTestRuntime(t)

	if _, err := runtime.RuntimeTunnelConfigure(t.Context(), "invalid", "", ""); err == nil {
		t.Fatal("expected mode validation error")
	}
	if _, err := runtime.RuntimeTunnelConfigure(t.Context(), "named", "https://example.com", ""); err == nil {
		t.Fatal("expected named mode to require tunnel token")
	}
}

func TestRuntimeNexusPairValidatesInput(t *testing.T) {
	runtime := remoteAccessTestRuntime(t)
	if _, err := runtime.RuntimeNexusPair(t.Context(), "", "code123", ""); err == nil {
		t.Fatal("expected endpoint validation error")
	}
	if _, err := runtime.RuntimeNexusPair(t.Context(), "https://nexus.example.com", "", ""); err == nil {
		t.Fatal("expected pairing code validation error")
	}
}

func assertResultOmits(t *testing.T, value any, secret string) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		for _, v := range typed {
			assertResultOmits(t, v, secret)
		}
	case []any:
		for _, v := range typed {
			assertResultOmits(t, v, secret)
		}
	case string:
		if strings.Contains(typed, secret) {
			t.Fatalf("result echoes secret value")
		}
	}
}
