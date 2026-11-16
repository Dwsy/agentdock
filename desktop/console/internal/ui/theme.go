package ui

import "github.com/egoist/mygo/ui"

// ThemeMode 是控制台外观模式。
//
// mygo 原生支持两套主题：ui.LightTheme / ui.DarkTheme，且默认主题每帧跟随系统
// 外观（frame 开头重置为 defaultTheme）。覆盖方式是帧内 Context.SetTheme；
// 因此这里只在浅色/深色模式下显式 SetTheme，跟随系统模式不覆盖、保持框架默认。
type ThemeMode int

const (
	// ThemeModeLight 浅色（默认：用户偏好浅色，深色不好看）。
	ThemeModeLight ThemeMode = iota
	// ThemeModeDark 深色。
	ThemeModeDark
	// ThemeModeSystem 跟随系统外观。
	ThemeModeSystem
)

// String 返回外观模式的稳定标识（持久化用，全小写）。
func (m ThemeMode) String() string {
	switch m {
	case ThemeModeDark:
		return "dark"
	case ThemeModeSystem:
		return "system"
	}
	return "light"
}

// ParseThemeMode 把持久化标识解析回外观模式；未知值回退浅色（偏好文件损坏
// 时不阻塞启动，也不猜测用户意图）。
func ParseThemeMode(s string) ThemeMode {
	switch s {
	case "dark":
		return ThemeModeDark
	case "system":
		return ThemeModeSystem
	}
	return ThemeModeLight
}

// label 返回外观模式的界面文案（工具条按钮用）。
func (m ThemeMode) label() string {
	switch m {
	case ThemeModeDark:
		return "深色"
	case ThemeModeSystem:
		return "跟随系统"
	}
	return "浅色"
}

// cycle 返回循环切换的下一个模式：浅色 → 深色 → 跟随系统 → 浅色。
func (m ThemeMode) cycle() ThemeMode {
	switch m {
	case ThemeModeLight:
		return ThemeModeDark
	case ThemeModeDark:
		return ThemeModeSystem
	default:
		return ThemeModeLight
	}
}

// apply 把外观模式应用到本帧：浅色/深色显式 SetTheme，跟随系统不覆盖。
func (m ThemeMode) apply(c *ui.Context) {
	switch m {
	case ThemeModeLight:
		c.SetTheme(ui.LightTheme())
	case ThemeModeDark:
		c.SetTheme(ui.DarkTheme())
	}
}

// ThemeMode 返回当前外观模式。
func (a *App) ThemeMode() ThemeMode {
	return a.themeMode
}

// SetThemeMode 设置外观模式（越界值归到浅色）并请求重绘。不触发持久化回调：
// 启动时恢复用户上次的选择不应重复写盘，持久化只由界面上的切换触发。
func (a *App) SetThemeMode(m ThemeMode) {
	if m < ThemeModeLight || m > ThemeModeSystem {
		m = ThemeModeLight
	}
	a.themeMode = m
	a.notify()
}

// SetThemePersist 注入外观模式的持久化回调（main 里写入 mygo PathUserData
// 下的偏好文件）；未注入时切换只在内存生效。
func (a *App) SetThemePersist(fn func(ThemeMode)) {
	a.themePersist = fn
}

// cycleThemeMode 循环切换外观模式并调用持久化回调（仅 UI 线程调用）。
func (a *App) cycleThemeMode() {
	a.themeMode = a.themeMode.cycle()
	if a.themePersist != nil {
		a.themePersist(a.themeMode)
	}
}
