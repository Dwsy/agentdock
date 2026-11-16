package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestThemeModeCycle 固化外观切换按钮的循环顺序：浅色 → 深色 → 跟随系统 → 浅色。
func TestThemeModeCycle(t *testing.T) {
	cases := []struct {
		in, want ThemeMode
	}{
		{ThemeModeLight, ThemeModeDark},
		{ThemeModeDark, ThemeModeSystem},
		{ThemeModeSystem, ThemeModeLight},
		{ThemeMode(99), ThemeModeLight}, // 未知值回到默认
	}
	for _, tc := range cases {
		if got := tc.in.cycle(); got != tc.want {
			t.Errorf("cycle(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestThemeModeStringParse 固化持久化标识的往返与未知值回退。
func TestThemeModeStringParse(t *testing.T) {
	for _, m := range []ThemeMode{ThemeModeLight, ThemeModeDark, ThemeModeSystem} {
		if got := ParseThemeMode(m.String()); got != m {
			t.Errorf("round trip %d: ParseThemeMode(%q) = %d", m, m.String(), got)
		}
	}
	for _, s := range []string{"", "garbage", "Light", "DARK"} {
		if got := ParseThemeMode(s); got != ThemeModeLight {
			t.Errorf("ParseThemeMode(%q) = %d, want 默认浅色 %d", s, got, ThemeModeLight)
		}
	}
}

// TestAppDefaultThemeMode 是用户偏好的回归线：默认必须是浅色，不是深色。
func TestAppDefaultThemeMode(t *testing.T) {
	app := New(&stubBackend{})
	t.Cleanup(app.Close)
	if got := app.ThemeMode(); got != ThemeModeLight {
		t.Fatalf("默认外观模式 = %d, want 浅色 %d", got, ThemeModeLight)
	}
}

// TestSetThemeModeClamps 越界值归到浅色，合法值原样保留。
func TestSetThemeModeClamps(t *testing.T) {
	app := New(&stubBackend{})
	t.Cleanup(app.Close)
	app.SetThemeMode(ThemeModeSystem)
	if got := app.ThemeMode(); got != ThemeModeSystem {
		t.Errorf("SetThemeMode(system) 后 = %d, want %d", got, ThemeModeSystem)
	}
	app.SetThemeMode(ThemeMode(42))
	if got := app.ThemeMode(); got != ThemeModeLight {
		t.Errorf("越界值未归到浅色：= %d, want %d", got, ThemeModeLight)
	}
}

// TestThemeModeAppliedToFrame 断言 View 帧首真的把外观应用到 mygo 主题：
// 浅色/深色显式 SetTheme，跟随系统时主题随 Tester 的系统外观变化。
func TestThemeModeAppliedToFrame(t *testing.T) {
	app := New(&stubBackend{})
	t.Cleanup(app.Close)
	var got *ui.Theme
	tt := ui.NewTester(func(c *ui.Context) {
		app.View(c)
		got = c.Theme() // View 渲染完后本帧生效的主题
	}, 1180, 740)

	tt.Frame()
	if got == nil || got.Dark {
		t.Fatalf("默认（浅色）帧主题应为亮色，实际 %+v", got)
	}
	if want := ui.LightTheme(); got.Background != want.Background {
		t.Errorf("浅色帧 Background = %v, want %v（ui.LightTheme）", got.Background, want.Background)
	}

	app.SetThemeMode(ThemeModeDark)
	tt.Frame()
	if got == nil || !got.Dark {
		t.Fatalf("深色模式帧主题应为暗色，实际 %+v", got)
	}
	if want := ui.DarkTheme(); got.Background != want.Background {
		t.Errorf("深色帧 Background = %v, want %v（ui.DarkTheme）", got.Background, want.Background)
	}

	// 跟随系统：不覆盖框架默认，主题随系统外观（Tester.SetDark）走。
	app.SetThemeMode(ThemeModeSystem)
	tt.SetDark(false)
	tt.Frame()
	if got == nil || got.Dark {
		t.Errorf("跟随系统 + 浅色系统外观：帧主题应为亮色，实际 %+v", got)
	}
	tt.SetDark(true)
	tt.Frame()
	if got == nil || !got.Dark {
		t.Errorf("跟随系统 + 深色系统外观：帧主题应为暗色，实际 %+v", got)
	}
}

// TestThemeButtonCyclesAndPersists 点工具条按钮循环切换外观并触发持久化回调。
func TestThemeButtonCyclesAndPersists(t *testing.T) {
	app := New(&stubBackend{})
	t.Cleanup(app.Close)
	var persisted []ThemeMode
	app.SetThemePersist(func(m ThemeMode) { persisted = append(persisted, m) })
	tt := ui.NewTester(app.View, 1180, 740)
	tt.Frame()

	for _, step := range []struct {
		click, want ThemeMode
		label       string
	}{
		{ThemeModeLight, ThemeModeDark, "浅色"},     // 当前显示“浅色”，点一下切深色
		{ThemeModeDark, ThemeModeSystem, "深色"},    // 再点切跟随系统
		{ThemeModeSystem, ThemeModeLight, "跟随系统"}, // 再点回到浅色
	} {
		if _, ok := tt.Find(step.label); !ok {
			t.Fatalf("工具条上找不到外观按钮文案 %q（当前模式 %v，文本 %v）", step.label, app.ThemeMode(), tt.Texts())
		}
		if err := tt.Click(step.label); err != nil {
			t.Fatalf("点击 %q 失败: %v", step.label, err)
		}
		tt.Frame()
		if got := app.ThemeMode(); got != step.want {
			t.Fatalf("点击 %q 后模式 = %d, want %d", step.label, got, step.want)
		}
	}
	if len(persisted) != 3 {
		t.Fatalf("持久化回调应触发 3 次，实际 %d 次: %v", len(persisted), persisted)
	}
	want := []ThemeMode{ThemeModeDark, ThemeModeSystem, ThemeModeLight}
	for i := range want {
		if persisted[i] != want[i] {
			t.Errorf("第 %d 次持久化 = %d, want %d", i+1, persisted[i], want[i])
		}
	}
}
