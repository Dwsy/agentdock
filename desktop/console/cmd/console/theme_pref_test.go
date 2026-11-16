package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo"

	console "github.com/uvwt/agentdock/desktop/console/internal/ui"
)

// TestThemePrefRoundTrip 固化外观偏好的持久化契约：缺文件默认浅色、三种模式
// 往返一致、损坏内容回退浅色、rename 后不留 .tmp。
func TestThemePrefRoundTrip(t *testing.T) {
	dir := t.TempDir()
	mygo.App.SetPath(mygo.PathUserData, dir)
	t.Cleanup(func() { mygo.App.SetPath(mygo.PathUserData, "") })

	if got := loadThemeMode(); got != console.ThemeModeLight {
		t.Fatalf("无偏好文件应默认浅色，实际 %v", got)
	}

	for _, mode := range []console.ThemeMode{
		console.ThemeModeDark, console.ThemeModeSystem, console.ThemeModeLight,
	} {
		saveThemeMode(mode)
		if got := loadThemeMode(); got != mode {
			t.Errorf("round trip %s: load = %v, want %v", mode, got, mode)
		}
	}

	if _, err := os.Stat(filepath.Join(dir, themePrefFile+".tmp")); !os.IsNotExist(err) {
		t.Errorf("rename 替换后不应残留 .tmp（stat err=%v）", err)
	}

	broken := filepath.Join(dir, themePrefFile)
	if err := os.WriteFile(broken, []byte("garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadThemeMode(); got != console.ThemeModeLight {
		t.Errorf("损坏内容应回退浅色，实际 %v", got)
	}
}
