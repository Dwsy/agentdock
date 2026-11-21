package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setTestUserHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTDOCK_HOME", "")
	t.Setenv("AGENTDOCK_DEFAULT_DIR", "")
}

func TestNormalizeDefaultsToUserDirectories(t *testing.T) {
	home := t.TempDir()
	setTestUserHome(t, home)
	cfg := Config{}
	if err := cfg.Normalize(); err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	wantHome := filepath.Join(home, ".agentdock")
	wantDefault := filepath.Join(home, "AgentDock")
	if cfg.AgentDockHome != wantHome {
		t.Fatalf("AgentDockHome = %q, want %q", cfg.AgentDockHome, wantHome)
	}
	if cfg.AgentDockDefaultDir != wantDefault {
		t.Fatalf("AgentDockDefaultDir = %q, want %q", cfg.AgentDockDefaultDir, wantDefault)
	}
	if cfg.BrowserExecutablePath != "" {
		t.Fatalf("BrowserExecutablePath = %q, want empty auto-detection", cfg.BrowserExecutablePath)
	}
}

func TestFromEnvParsesCommandEnvironmentMapping(t *testing.T) {
	t.Setenv("AGENTDOCK_COMMAND_ENV_FROM_ENV_JSON", `{"NIX_LD":"NIX_LD","CHILD_TOKEN":"HOST_TOKEN"}`)

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if cfg.CommandEnvFromEnv["NIX_LD"] != "NIX_LD" || cfg.CommandEnvFromEnv["CHILD_TOKEN"] != "HOST_TOKEN" {
		t.Fatalf("CommandEnvFromEnv = %#v", cfg.CommandEnvFromEnv)
	}
}

func TestFromEnvMCPAppsModeDefaultsAndMigratesLegacyBool(t *testing.T) {
	t.Setenv("AGENTDOCK_MCP_APPS_MODE", "")
	t.Setenv("AGENTDOCK_MCP_APPS_ENABLED", "")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if cfg.MCPAppsMode != MCPAppsModeFull {
		t.Fatalf("MCPAppsMode = %q, want full by default", cfg.MCPAppsMode)
	}

	t.Setenv("AGENTDOCK_MCP_APPS_ENABLED", "false")
	cfg, err = FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() with legacy false error = %v", err)
	}
	if cfg.MCPAppsMode != MCPAppsModeOff {
		t.Fatalf("MCPAppsMode = %q, want off from legacy false", cfg.MCPAppsMode)
	}

	t.Setenv("AGENTDOCK_MCP_APPS_MODE", "compact")
	cfg, err = FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() with mode error = %v", err)
	}
	if cfg.MCPAppsMode != MCPAppsModeCompact {
		t.Fatalf("MCPAppsMode = %q, want compact; explicit mode must win over legacy bool", cfg.MCPAppsMode)
	}
}

func TestFromEnvRejectsInvalidMCPAppsMode(t *testing.T) {
	t.Setenv("AGENTDOCK_MCP_APPS_MODE", "custom")
	if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), "AGENTDOCK_MCP_APPS_MODE") {
		t.Fatalf("FromEnv() error = %v, want MCP Apps mode validation error", err)
	}
}

func TestCommandEnvironmentMappingRejectsInvalidNames(t *testing.T) {
	t.Setenv("AGENTDOCK_COMMAND_ENV_FROM_ENV_JSON", `{"BAD-NAME":"HOST_TOKEN"}`)
	if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), "AGENTDOCK_COMMAND_ENV_FROM_ENV_JSON") {
		t.Fatalf("FromEnv() error = %v, want command env mapping validation error", err)
	}

	setTestUserHome(t, t.TempDir())
	cfg := Config{CommandEnvFromEnv: map[string]string{"CHILD_TOKEN": "BAD-HOST"}}
	if err := cfg.Normalize(); err == nil || !strings.Contains(err.Error(), "AGENTDOCK_COMMAND_ENV_FROM_ENV_JSON") {
		t.Fatalf("Normalize() error = %v, want command env mapping validation error", err)
	}
}

func TestCommandEnvironmentMappingRejectsSkillDataDir(t *testing.T) {
	setTestUserHome(t, t.TempDir())
	for _, key := range []string{"SKILL_DATA_DIR", "skill_data_dir"} {
		t.Run(key, func(t *testing.T) {
			cfg := Config{CommandEnvFromEnv: map[string]string{key: "HOST_DATA_DIR"}}
			if err := cfg.Normalize(); err == nil || !strings.Contains(err.Error(), "reserved") {
				t.Fatalf("Normalize() error = %v, want reserved Skill runtime variable error", err)
			}
		})
	}
}

func TestFromEnvIgnoresOldDirectoryConfig(t *testing.T) {
	home := t.TempDir()
	setTestUserHome(t, home)
	t.Setenv("AGENTDOCK_WORKSPACE", "/tmp/old-workspace")
	t.Setenv("AGENTDOCK_RUNTIME_PROFILE", "workspace")
	t.Setenv("AGENTDOCK_DIR", "/tmp/old-control")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if cfg.AgentDockHome != filepath.Join(home, ".agentdock") {
		t.Fatalf("AgentDockHome = %q", cfg.AgentDockHome)
	}
	if cfg.AgentDockDefaultDir != filepath.Join(home, "AgentDock") {
		t.Fatalf("AgentDockDefaultDir = %q", cfg.AgentDockDefaultDir)
	}
}

func TestFromEnvIgnoresRemovedNexusCredentials(t *testing.T) {
	t.Setenv("AGENTDOCK_NEXUS_ENDPOINT", "https://legacy-nexus.example.com")
	t.Setenv("AGENTDOCK_NEXUS_TOKEN", "legacy-secret")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if cfg.NexusEndpoint != "" || cfg.NexusDeviceToken != "" {
		t.Fatalf("legacy Nexus credentials were loaded: %#v", cfg)
	}
}

func TestFromEnvUsesExplicitAgentDockDirectories(t *testing.T) {
	profile := t.TempDir()
	setTestUserHome(t, profile)
	home := filepath.Join(t.TempDir(), "state")
	workspace := filepath.Join(t.TempDir(), "workspace")
	t.Setenv("AGENTDOCK_HOME", home)
	t.Setenv("AGENTDOCK_DEFAULT_DIR", workspace)

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if cfg.AgentDockHome != home {
		t.Fatalf("AgentDockHome = %q, want %q", cfg.AgentDockHome, home)
	}
	if cfg.AgentDockDefaultDir != workspace {
		t.Fatalf("AgentDockDefaultDir = %q, want %q", cfg.AgentDockDefaultDir, workspace)
	}
}

func TestFromEnvRejectsRelativeAgentDockDirectories(t *testing.T) {
	setTestUserHome(t, t.TempDir())
	for _, test := range []struct {
		name string
		key  string
		want string
	}{
		{name: "home", key: "AGENTDOCK_HOME", want: "AgentDockHome"},
		{name: "default directory", key: "AGENTDOCK_DEFAULT_DIR", want: "AgentDockDefaultDir"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AGENTDOCK_HOME", "")
			t.Setenv("AGENTDOCK_DEFAULT_DIR", "")
			t.Setenv(test.key, "relative/path")
			cfg, err := FromEnv()
			if err != nil {
				t.Fatalf("FromEnv() error = %v", err)
			}
			err = cfg.Normalize()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Normalize() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestSkillDirUsesAgentDockHome(t *testing.T) {
	home := t.TempDir()
	cfg := Config{AgentDockHome: filepath.Join(home, ".agentdock")}
	got := SkillDir(cfg)
	want := filepath.Join(cfg.AgentDockHome, "skills")
	if got != want {
		t.Fatalf("SkillDir() = %q, want %q", got, want)
	}
}

func TestSkillDataDirUsesManagedPrivateRoot(t *testing.T) {
	home := t.TempDir()
	cfg := Config{AgentDockHome: filepath.Join(home, ".agentdock")}
	got, err := SkillDataDir(cfg, "demo-skill")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cfg.AgentDockHome, "data", "skills", "demo-skill")
	if got != want {
		t.Fatalf("SkillDataDir() = %q, want %q", got, want)
	}
}

func TestSkillDataDirRejectsUnsafeIdentity(t *testing.T) {
	cfg := Config{AgentDockHome: filepath.Join(t.TempDir(), ".agentdock")}
	for _, skill := range []string{
		"", "../demo", "demo/child", `demo\child`, "/tmp/demo",
		"Demo-skill", "demo_skill", " demo-skill", "demo-skill ",
	} {
		t.Run(skill, func(t *testing.T) {
			if got, err := SkillDataDir(cfg, skill); err == nil {
				t.Fatalf("SkillDataDir(%q) = %q, want validation error", skill, got)
			}
		})
	}
	if _, err := SkillDataDir(Config{AgentDockHome: "relative"}, "demo-skill"); err == nil {
		t.Fatal("SkillDataDir accepted relative AgentDockHome")
	}
}

func TestValidateAuthAllowsNoOAuthOrServerURLOnly(t *testing.T) {
	cases := []Config{
		{Host: "127.0.0.1"},
		{Host: "127.0.0.1", OAuthServerURL: "https://agentdock.example.com"},
		{Host: "0.0.0.0", AuthToken: "static-token", OAuthServerURL: "https://agentdock.example.com"},
	}
	for _, cfg := range cases {
		if err := cfg.ValidateAuth(); err != nil {
			t.Fatalf("ValidateAuth() error = %v for cfg %#v", err, cfg)
		}
		if cfg.OAuthEnabled {
			t.Fatalf("OAuthEnabled = true without the explicit enable flag")
		}
	}
}

func TestValidateAuthOAuthRequiresCompleteConfig(t *testing.T) {
	base := Config{OAuthEnabled: true, OAuthServerURL: "https://agentdock.example.com"}
	t.Setenv("AGENTDOCK_OAUTH_PASSWORD", "strong-password")
	t.Setenv("AGENTDOCK_OAUTH_TOKEN_SECRET", "0123456789abcdef0123456789abcdef")
	if err := base.ValidateAuth(); err != nil {
		t.Fatalf("ValidateAuth() complete config error = %v", err)
	}

	cases := []struct {
		name    string
		cfg     Config
		unset   string
		missing string
	}{
		{name: "server url", cfg: Config{OAuthEnabled: true}, missing: "AGENTDOCK_SERVER_URL"},
		{name: "password", cfg: base, unset: "AGENTDOCK_OAUTH_PASSWORD", missing: "AGENTDOCK_OAUTH_PASSWORD"},
		{name: "token secret", cfg: base, unset: "AGENTDOCK_OAUTH_TOKEN_SECRET", missing: "AGENTDOCK_OAUTH_TOKEN_SECRET"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AGENTDOCK_OAUTH_PASSWORD", "strong-password")
			t.Setenv("AGENTDOCK_OAUTH_TOKEN_SECRET", "0123456789abcdef0123456789abcdef")
			if tc.unset != "" {
				t.Setenv(tc.unset, "")
			}
			err := tc.cfg.ValidateAuth()
			if err == nil || !strings.Contains(err.Error(), tc.missing) {
				t.Fatalf("ValidateAuth() error = %v, want missing %s", err, tc.missing)
			}
		})
	}
}

func TestValidateAuthRejectsWeakOAuthCredentials(t *testing.T) {
	cfg := Config{OAuthEnabled: true, OAuthServerURL: "https://agentdock.example.com"}
	for name, credentials := range map[string][2]string{
		"short password": {"short", "0123456789abcdef0123456789abcdef"},
		"short secret":   {"strong-password", "short"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("AGENTDOCK_OAUTH_PASSWORD", credentials[0])
			t.Setenv("AGENTDOCK_OAUTH_TOKEN_SECRET", credentials[1])
			if err := cfg.ValidateAuth(); err == nil {
				t.Fatal("ValidateAuth() accepted weak OAuth credentials")
			}
		})
	}
}

func TestFromEnvRejectsInvalidTypedValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "port", key: "AGENTDOCK_PORT", value: "not-a-number"},
		{name: "browser enabled", key: "AGENTDOCK_BROWSER_ENABLED", value: "sometimes"},
		{name: "browser reuse existing cdp", key: "AGENTDOCK_BROWSER_REUSE_EXISTING_CDP", value: "sometimes"},
		{name: "oauth enabled", key: "AGENTDOCK_OAUTH_ENABLED", value: "enabled"},
		{name: "oauth access token ttl", key: "AGENTDOCK_OAUTH_ACCESS_TOKEN_TTL", value: "one-day"},
		{name: "remote auth enabled", key: "AGENTDOCK_REMOTE_AUTH_ENABLED", value: "enabled"},
		{name: "remote auth session ttl", key: "AGENTDOCK_REMOTE_AUTH_SESSION_TTL", value: "one-day"},
		{name: "stdio", key: "AGENTDOCK_STDIO", value: "enabled"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AGENTDOCK_PORT", "")
			t.Setenv("AGENTDOCK_BROWSER_ENABLED", "")
			t.Setenv("AGENTDOCK_BROWSER_REUSE_EXISTING_CDP", "")
			t.Setenv("AGENTDOCK_OAUTH_ENABLED", "")
			t.Setenv("AGENTDOCK_OAUTH_ACCESS_TOKEN_TTL", "")
			t.Setenv("AGENTDOCK_REMOTE_AUTH_ENABLED", "")
			t.Setenv("AGENTDOCK_REMOTE_AUTH_SESSION_TTL", "")
			t.Setenv("AGENTDOCK_STDIO", "")
			t.Setenv(test.key, test.value)
			if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), test.key) {
				t.Fatalf("FromEnv() error = %v, want %s parse error", err, test.key)
			}
		})
	}
}

func TestFromEnvParsesTypedValues(t *testing.T) {
	tempDir := t.TempDir()
	browserPath := filepath.Join(tempDir, "chromium")
	t.Setenv("AGENTDOCK_PORT", " 9876 ")
	t.Setenv("AGENTDOCK_BROWSER_ENABLED", "true")
	t.Setenv("AGENTDOCK_BROWSER_EXECUTABLE_PATH", browserPath)
	t.Setenv("AGENTDOCK_BROWSER_CDP_URL", "http://127.0.0.1:9222")
	t.Setenv("AGENTDOCK_BROWSER_REUSE_EXISTING_CDP", "true")
	t.Setenv("AGENTDOCK_OAUTH_ENABLED", "true")
	t.Setenv("AGENTDOCK_OAUTH_ACCESS_TOKEN_TTL", "24h")
	t.Setenv("AGENTDOCK_STDIO", "1")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if cfg.Port != 9876 || !cfg.BrowserEnabled || !cfg.OAuthEnabled || !cfg.Stdio || cfg.OAuthAccessTokenTTLSeconds != int64(24*time.Hour/time.Second) ||
		cfg.BrowserExecutablePath != browserPath || cfg.BrowserCDPURL != "http://127.0.0.1:9222" || !cfg.BrowserReuseExistingCDP {
		t.Fatalf("config = %#v", cfg)
	}
}

func TestFromEnvParsesLongOAuthAccessTokenTTLInDays(t *testing.T) {
	t.Setenv("AGENTDOCK_OAUTH_ACCESS_TOKEN_TTL", "999999d")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	want := int64(999999 * 24 * 60 * 60)
	if cfg.OAuthAccessTokenTTLSeconds != want {
		t.Fatalf("OAuthAccessTokenTTLSeconds = %d, want %d", cfg.OAuthAccessTokenTTLSeconds, want)
	}
}

func TestFromEnvParsesNeverExpiringOAuthAccessToken(t *testing.T) {
	t.Setenv("AGENTDOCK_OAUTH_ACCESS_TOKEN_TTL", "never")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if !cfg.OAuthAccessTokenNeverExpires || cfg.OAuthAccessTokenTTLSeconds != 0 {
		t.Fatalf("config = %#v, want a non-expiring OAuth access token", cfg)
	}
}

func TestValidateOAuthAccessTokenTTL(t *testing.T) {
	for _, value := range []string{"1m", "24h", "30d", "999999d", "never"} {
		if err := ValidateOAuthAccessTokenTTL(value); err != nil {
			t.Fatalf("ValidateOAuthAccessTokenTTL(%q) error = %v", value, err)
		}
	}
	for _, value := range []string{"", "59s", "1000000d", "1.5s", "invalid"} {
		if err := ValidateOAuthAccessTokenTTL(value); err == nil {
			t.Fatalf("ValidateOAuthAccessTokenTTL(%q) accepted invalid value", value)
		}
	}
}

func TestNormalizeValidatesOAuthAccessTokenTTL(t *testing.T) {
	home := t.TempDir()
	for _, test := range []struct {
		name       string
		ttlSeconds int64
	}{
		{name: "too short", ttlSeconds: 1},
		{name: "too long", ttlSeconds: int64(999999*24*60*60) + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := Config{
				AgentDockHome:              filepath.Join(home, test.name, "home"),
				AgentDockDefaultDir:        filepath.Join(home, test.name, "workspace"),
				OAuthAccessTokenTTLSeconds: test.ttlSeconds,
			}
			if err := cfg.Normalize(); err == nil || !strings.Contains(err.Error(), "AGENTDOCK_OAUTH_ACCESS_TOKEN_TTL") {
				t.Fatalf("Normalize() error = %v, want OAuth access token TTL error", err)
			}
		})
	}
}

func TestNormalizeRejectsRelativeBrowserExecutablePath(t *testing.T) {
	home := t.TempDir()
	setTestUserHome(t, home)
	cfg := Config{BrowserExecutablePath: "relative/chromium"}
	if err := cfg.Normalize(); err == nil || !strings.Contains(err.Error(), "BrowserExecutablePath must resolve to an absolute path") {
		t.Fatalf("Normalize() error = %v, want absolute BrowserExecutablePath error", err)
	}
}

func TestNormalizeValidatesPortAndLogLevel(t *testing.T) {
	home := t.TempDir()
	for _, test := range []struct {
		name     string
		port     int
		logLevel string
		want     string
	}{
		{name: "negative port", port: -1, logLevel: "info", want: "port must be between"},
		{name: "large port", port: 65536, logLevel: "info", want: "port must be between"},
		{name: "unknown log level", port: 8765, logLevel: "verbose", want: "unsupported log level"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := Config{
				AgentDockHome:       filepath.Join(home, test.name, "home"),
				AgentDockDefaultDir: filepath.Join(home, test.name, "workspace"),
				Port:                test.port, LogLevel: test.logLevel,
			}
			if err := cfg.Normalize(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Normalize() error = %v, want %q", err, test.want)
			}
		})
	}

	cfg := Config{
		AgentDockHome:       filepath.Join(home, "valid", "home"),
		AgentDockDefaultDir: filepath.Join(home, "valid", "workspace"),
		Port:                443,
		LogLevel:            " WARNING ",
		Host:                " 0.0.0.0 ",
		OAuthServerURL:      " https://agentdock.example.com ",
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if cfg.LogLevel != "warn" || cfg.Host != "0.0.0.0" || cfg.OAuthServerURL != "https://agentdock.example.com" {
		t.Fatalf("normalized config = %#v", cfg)
	}
}

func TestValidateAuthRejectsInvalidServerURL(t *testing.T) {
	t.Setenv("AGENTDOCK_OAUTH_PASSWORD", "strong-password")
	t.Setenv("AGENTDOCK_OAUTH_TOKEN_SECRET", "0123456789abcdef0123456789abcdef")
	for _, serverURL := range []string{
		"relative/path",
		"ftp://agentdock.example",
		"http://agentdock.example",
		"https://user:pass@agentdock.example",
		"https://agentdock.example/base",
		"https://agentdock.example?mode=test",
		"https://agentdock.example/#fragment",
	} {
		cfg := Config{OAuthEnabled: true, OAuthServerURL: serverURL}
		if err := cfg.ValidateAuth(); err == nil {
			t.Fatalf("ValidateAuth() accepted %q", serverURL)
		}
	}
}

func TestValidateAuthAllowsHTTPOnlyForLoopbackHosts(t *testing.T) {
	t.Setenv("AGENTDOCK_OAUTH_PASSWORD", "strong-password")
	t.Setenv("AGENTDOCK_OAUTH_TOKEN_SECRET", "0123456789abcdef0123456789abcdef")
	for _, serverURL := range []string{
		"http://localhost:8765",
		"http://127.0.0.1:8765",
		"http://[::1]:8765",
	} {
		cfg := Config{OAuthEnabled: true, OAuthServerURL: serverURL}
		if err := cfg.ValidateAuth(); err != nil {
			t.Fatalf("ValidateAuth() rejected loopback URL %q: %v", serverURL, err)
		}
	}
}

func TestValidateAuthRejectsUnauthenticatedNonLoopbackListener(t *testing.T) {
	for _, host := range []string{"0.0.0.0", "::", "192.0.2.10", "agentdock.internal"} {
		cfg := Config{Host: host}
		if err := cfg.ValidateAuth(); err == nil || !strings.Contains(err.Error(), "requires AGENTDOCK_AUTH_TOKEN or OAuth") {
			t.Fatalf("ValidateAuth(%q) error = %v, want non-loopback authentication error", host, err)
		}
	}
}

func TestValidateAuthAllowsSafeListenerModes(t *testing.T) {
	for _, cfg := range []Config{
		{Host: "127.0.0.1"},
		{Host: "::1"},
		{Host: "localhost"},
		{Host: "0.0.0.0", AuthToken: "configured-token"},
		{Host: "0.0.0.0", Stdio: true},
	} {
		if err := cfg.ValidateAuth(); err != nil {
			t.Fatalf("ValidateAuth(%#v) error = %v", cfg, err)
		}
	}
}

func TestNormalizeValidatesAndCanonicalizesTrustedProxyCIDRs(t *testing.T) {
	home := t.TempDir()
	cfg := Config{
		AgentDockHome:       filepath.Join(home, "home"),
		AgentDockDefaultDir: filepath.Join(home, "workspace"),
		TrustedProxyCIDRs:   []string{"127.0.0.1/8", "127.0.0.0/8", "::1/128"},
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	if len(cfg.TrustedProxyCIDRs) != 2 || cfg.TrustedProxyCIDRs[0] != "127.0.0.0/8" || cfg.TrustedProxyCIDRs[1] != "::1/128" {
		t.Fatalf("TrustedProxyCIDRs = %#v", cfg.TrustedProxyCIDRs)
	}
	cfg.TrustedProxyCIDRs = []string{"not-a-cidr"}
	if err := cfg.Normalize(); err == nil || !strings.Contains(err.Error(), "invalid CIDR") {
		t.Fatalf("Normalize() error = %v, want invalid CIDR", err)
	}
}

func TestFromEnvReadsTrustedProxyCIDRs(t *testing.T) {
	home := t.TempDir()
	setTestUserHome(t, home)
	t.Setenv("AGENTDOCK_TRUSTED_PROXY_CIDRS", "127.0.0.0/8, ::1/128")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	if len(cfg.TrustedProxyCIDRs) != 2 {
		t.Fatalf("TrustedProxyCIDRs = %#v", cfg.TrustedProxyCIDRs)
	}
}

func TestValidateAuthTreatsEmptyHostAsWildcard(t *testing.T) {
	cfg := Config{Host: ""}
	if err := cfg.ValidateAuth(); err == nil || !strings.Contains(err.Error(), "requires AGENTDOCK_AUTH_TOKEN or OAuth") {
		t.Fatalf("ValidateAuth() error = %v, want wildcard authentication error", err)
	}
}

func TestFromEnvManualLLMEnabled(t *testing.T) {
	home := t.TempDir()
	setTestUserHome(t, home)

	t.Setenv("AGENTDOCK_MANUAL_LLM_ENABLED", "")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if cfg.ManualLLMEnabled {
		t.Fatal("ManualLLMEnabled should default to false")
	}

	t.Setenv("AGENTDOCK_MANUAL_LLM_ENABLED", "true")
	cfg, err = FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if !cfg.ManualLLMEnabled {
		t.Fatal("ManualLLMEnabled = false, want true")
	}

	t.Setenv("AGENTDOCK_MANUAL_LLM_ENABLED", "invalid")
	if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), "AGENTDOCK_MANUAL_LLM_ENABLED") {
		t.Fatalf("FromEnv() error = %v, want AGENTDOCK_MANUAL_LLM_ENABLED validation error", err)
	}
}

func TestFromEnvManualLLMTuning(t *testing.T) {
	home := t.TempDir()
	setTestUserHome(t, home)

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() defaults error = %v", err)
	}
	if cfg.ManualLLMPreviewBytes != 1800 || cfg.ManualLLMSliceBytes != 4096 || cfg.ManualLLMMaxSliceBytes != 16384 ||
		cfg.ManualLLMWaitMs != 30000 || cfg.ManualLLMMaxWaitMs != 55000 || cfg.ManualLLMMaxPending != 32 ||
		cfg.ManualLLMCacheMaxResponses != 256 || cfg.ManualLLMCacheTTLSec != 1800 ||
		cfg.ManualLLMKeepaliveMS != 15000 || cfg.ManualLLMCacheMaxImageMB != 32 {
		t.Fatalf("unexpected manual LLM defaults: %+v", cfg)
	}

	t.Setenv("AGENTDOCK_MANUAL_LLM_PREVIEW_BYTES", "2400")
	t.Setenv("AGENTDOCK_MANUAL_LLM_SLICE_BYTES", "8192")
	t.Setenv("AGENTDOCK_MANUAL_LLM_MAX_SLICE_BYTES", "32768")
	t.Setenv("AGENTDOCK_MANUAL_LLM_WAIT_MS", "20000")
	t.Setenv("AGENTDOCK_MANUAL_LLM_MAX_WAIT_MS", "45000")
	t.Setenv("AGENTDOCK_MANUAL_LLM_MAX_PENDING", "64")
	t.Setenv("AGENTDOCK_MANUAL_LLM_CACHE_MAX_RESPONSES", "512")
	t.Setenv("AGENTDOCK_MANUAL_LLM_CACHE_TTL_SEC", "3600")
	t.Setenv("AGENTDOCK_MANUAL_LLM_KEEPALIVE_MS", "10000")
	t.Setenv("AGENTDOCK_MANUAL_LLM_CACHE_MAX_IMAGE_MB", "16")

	cfg, err = FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() overrides error = %v", err)
	}
	if cfg.ManualLLMPreviewBytes != 2400 || cfg.ManualLLMSliceBytes != 8192 || cfg.ManualLLMMaxSliceBytes != 32768 ||
		cfg.ManualLLMWaitMs != 20000 || cfg.ManualLLMMaxWaitMs != 45000 || cfg.ManualLLMMaxPending != 64 ||
		cfg.ManualLLMCacheMaxResponses != 512 || cfg.ManualLLMCacheTTLSec != 3600 ||
		cfg.ManualLLMKeepaliveMS != 10000 || cfg.ManualLLMCacheMaxImageMB != 16 {
		t.Fatalf("unexpected manual LLM overrides: %+v", cfg)
	}

	t.Setenv("AGENTDOCK_MANUAL_LLM_SLICE_BYTES", "65536")
	t.Setenv("AGENTDOCK_MANUAL_LLM_MAX_SLICE_BYTES", "32768")
	badSliceCfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() unexpected error: %v", err)
	}
	if err := badSliceCfg.Normalize(); err == nil || !strings.Contains(err.Error(), "AGENTDOCK_MANUAL_LLM_SLICE_BYTES") {
		t.Fatalf("Normalize() expected slice > max_slice error, got %v", err)
	}

	t.Setenv("AGENTDOCK_MANUAL_LLM_SLICE_BYTES", "4096")
	t.Setenv("AGENTDOCK_MANUAL_LLM_WAIT_MS", "90000")
	t.Setenv("AGENTDOCK_MANUAL_LLM_MAX_WAIT_MS", "60000")
	badWaitCfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() unexpected error: %v", err)
	}
	if err := badWaitCfg.Normalize(); err == nil || !strings.Contains(err.Error(), "AGENTDOCK_MANUAL_LLM_WAIT_MS") {
		t.Fatalf("Normalize() expected wait_ms > max_wait_ms error, got %v", err)
	}

	if cfg.UsageCountMode != TokenCountModeTiktoken {
		t.Fatalf("default UsageCountMode = %q, want tiktoken", cfg.UsageCountMode)
	}
	t.Setenv("AGENTDOCK_USAGE_COUNT_MODE", "chars")
	charsCfg, err := FromEnv()
	if err != nil || charsCfg.UsageCountMode != TokenCountModeChars {
		t.Fatalf("AGENTDOCK_USAGE_COUNT_MODE=chars got (%+v, %v)", charsCfg.UsageCountMode, err)
	}
	t.Setenv("AGENTDOCK_USAGE_COUNT_MODE", "unknown")
	if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), "AGENTDOCK_USAGE_COUNT_MODE") {
		t.Fatalf("expected error for invalid AGENTDOCK_USAGE_COUNT_MODE, got %v", err)
	}
}

func TestValidateAuthRemoteConsoleLogin(t *testing.T) {
	base := Config{
		Host:                        "127.0.0.1",
		AuthToken:                   "static-token",
		RemoteAuthEnabled:           true,
		RemoteAuthPassword:          "console-password",
		RemoteAuthSessionTTLSeconds: 3600,
		RemoteAuthPublicURL:         "https://dock.example.com",
	}
	if err := base.ValidateAuth(); err != nil {
		t.Fatalf("ValidateAuth() error = %v", err)
	}

	withConfig := func(mutate func(*Config)) Config {
		cfg := base
		mutate(&cfg)
		return cfg
	}
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "missing password",
			cfg:  Config{AuthToken: base.AuthToken, RemoteAuthEnabled: true, RemoteAuthSessionTTLSeconds: 3600},
			want: "AGENTDOCK_REMOTE_AUTH_PASSWORD",
		},
		{
			name: "short password",
			cfg:  withConfig(func(c *Config) { c.RemoteAuthPassword = "short" }),
			want: "at least 12 characters",
		},
		{
			name: "padded password",
			cfg:  withConfig(func(c *Config) { c.RemoteAuthPassword = "console-password " }),
			want: "whitespace",
		},
		{
			name: "missing machine token",
			cfg:  withConfig(func(c *Config) { c.AuthToken = "" }),
			want: "AGENTDOCK_AUTH_TOKEN",
		},
		{
			name: "password without enable flag",
			cfg:  withConfig(func(c *Config) { c.RemoteAuthEnabled = false }),
			want: "AGENTDOCK_REMOTE_AUTH_ENABLED",
		},
		{
			name: "session ttl too short",
			cfg:  withConfig(func(c *Config) { c.RemoteAuthSessionTTLSeconds = 30 }),
			want: "AGENTDOCK_REMOTE_AUTH_SESSION_TTL",
		},
		{
			name: "session ttl too long",
			cfg:  withConfig(func(c *Config) { c.RemoteAuthSessionTTLSeconds = 31 * 24 * 60 * 60 }),
			want: "AGENTDOCK_REMOTE_AUTH_SESSION_TTL",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.ValidateAuth()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateAuth() error = %v, want %s", err, tc.want)
			}
		})
	}

	for _, publicURL := range []string{
		"relative/path",
		"ftp://dock.example.com",
		"http://dock.example.com",
		"https://user:pass@dock.example.com",
		"https://dock.example.com/base",
		"https://dock.example.com?mode=test",
		"https://dock.example.com/#fragment",
	} {
		cfg := base
		cfg.RemoteAuthPublicURL = publicURL
		if err := cfg.ValidateAuth(); err == nil {
			t.Fatalf("ValidateAuth() accepted public URL %q", publicURL)
		}
	}

	// 回环地址允许 http：本机调试不需要证书。
	cfg := base
	cfg.RemoteAuthPublicURL = "http://127.0.0.1:8765"
	if err := cfg.ValidateAuth(); err != nil {
		t.Fatalf("loopback public URL rejected: %v", err)
	}
}

func TestFromEnvParsesRemoteConsoleLogin(t *testing.T) {
	t.Setenv("AGENTDOCK_AUTH_TOKEN", "")
	t.Setenv("AGENTDOCK_OAUTH_ENABLED", "")
	t.Setenv("AGENTDOCK_REMOTE_AUTH_ENABLED", "true")
	t.Setenv("AGENTDOCK_REMOTE_AUTH_PASSWORD", "console-password")
	t.Setenv("AGENTDOCK_REMOTE_AUTH_SESSION_TTL", "7d")
	t.Setenv("AGENTDOCK_REMOTE_AUTH_PUBLIC_URL", " https://dock.example.com ")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if !cfg.RemoteAuthEnabled || cfg.RemoteAuthPassword != "console-password" ||
		cfg.RemoteAuthPublicURL != "https://dock.example.com" ||
		cfg.RemoteAuthSessionTTLSeconds != int64(7*24*60*60) {
		t.Fatalf("config = %#v", cfg)
	}
	if cfg.AuthRequired() {
		t.Fatal("remote console login must not make the runtime API require credentials on its own")
	}
}

func TestNormalizeDefaultsRemoteAuthSessionTTL(t *testing.T) {
	cfg := Config{AgentDockDefaultDir: t.TempDir(), AgentDockHome: filepath.Join(t.TempDir(), "home")}
	if err := cfg.Normalize(); err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if cfg.RemoteAuthSessionTTLSeconds != defaultRemoteAuthSessionTTLSeconds {
		t.Fatalf("RemoteAuthSessionTTLSeconds = %d, want %d", cfg.RemoteAuthSessionTTLSeconds, defaultRemoteAuthSessionTTLSeconds)
	}
}
