//go:build darwin

package desktopruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareCoreEnvironmentAddsHomebrewToPath(t *testing.T) {
	const launchdPath = "/usr/bin:/bin:/usr/sbin:/sbin"
	tests := []struct {
		name   string
		config string
		want   string
	}{
		{
			name: "launchd default",
			want: launchdPath + ":/opt/homebrew/bin:/usr/local/bin",
		},
		{
			name:   "configured path has priority",
			config: "PATH=/custom/bin:/usr/local/bin:" + launchdPath + "\n",
			want:   "/custom/bin:/usr/local/bin:" + launchdPath + ":/opt/homebrew/bin",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			runtimeRoot := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("PATH", launchdPath)
			t.Setenv("AGENTDOCK_RUNTIME_ROOT", "")
			t.Chdir(home)
			if err := os.WriteFile(filepath.Join(runtimeRoot, "agentdock.env"), []byte(tt.config), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := PrepareCoreEnvironment(runtimeRoot); err != nil {
				t.Fatal(err)
			}
			if got := os.Getenv("PATH"); got != tt.want {
				t.Fatalf("Core PATH = %q, want %q", got, tt.want)
			}
		})
	}
}
