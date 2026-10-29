// Package kit 是 AgentDock 控制面板（MyGo GPU 自绘 native UI）的自研领域组件包。
//
// 全部组件基于 mygo 内置 ui 包的薄封装，不引入任何第三方组件库：
//   - 组件都是纯渲染函数，接收 *ui.Context 返回 ui.Element，可自由组合；
//   - 颜色一律优先取 c.Theme() 主题色（失败 Danger、成功 Success、警示 Warning），
//     半透明背景用 ui.RGBA 由主题色派生，主题色缺失时回退到内置调色板（ui.RGBA）；
//   - 纯格式化函数（TruncateMiddle/FormatBytes/FormatDuration/FormatTime）
//     不依赖 UI，便于单测与页面层复用。
package kit

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// Tone 是组件的语义色系：成功、警示、失败、中性、强调。
type Tone int

const (
	// ToneNeutral 中性灰（次要信息）。
	ToneNeutral Tone = iota
	// ToneSuccess 成功（主题 Success）。
	ToneSuccess
	// ToneWarning 警示（主题 Warning）。
	ToneWarning
	// ToneDanger 失败/危险（主题 Danger）。
	ToneDanger
	// ToneAccent 强调/主操作（主题 Accent）。
	ToneAccent
)

// toneColor 返回 tone 对应的主题色；主题色缺省时回退到内置调色板。
func toneColor(t *ui.Theme, tone Tone) ui.Color {
	var c ui.Color
	switch tone {
	case ToneSuccess:
		c = t.Success
	case ToneWarning:
		c = t.Warning
	case ToneDanger:
		c = t.Danger
	case ToneAccent:
		c = t.Accent
	default:
		c = t.TextMuted
	}
	if c.A == 0 {
		return toneFallback(tone)
	}
	return c
}

// toneFallback 是主题色缺失时的内置调色板，统一用 ui.RGBA 构造。
func toneFallback(tone Tone) ui.Color {
	switch tone {
	case ToneSuccess:
		return ui.RGBA(0x16, 0xA3, 0x4A, 1)
	case ToneWarning:
		return ui.RGBA(0xD9, 0x77, 0x06, 1)
	case ToneDanger:
		return ui.RGBA(0xDC, 0x26, 0x26, 1)
	case ToneAccent:
		return ui.RGBA(0x25, 0x63, 0xEB, 1)
	default:
		return ui.RGBA(0x6B, 0x72, 0x80, 1)
	}
}

// tint 把颜色降为 alpha 透明度的背景色（ui.RGBA 派生）。
func tint(c ui.Color, alpha float32) ui.Color {
	return ui.RGBA(c.R, c.G, c.B, alpha)
}

// radius 返回主题圆角，主题缺省时用 6。
func radius(t *ui.Theme) float32 {
	if t.Radius > 0 {
		return t.Radius
	}
	return 6
}

// StatusChip 是圆角小标签：背景为主题色 15% 透明度，文字用对应主题色。
func StatusChip(c *ui.Context, label string, tone Tone) ui.Element {
	t := c.Theme()
	fg := toneColor(t, tone)
	return ui.Row(c).
		AlignItems(ui.Center).
		Padding(2, 8).
		Radius(999).
		Background(tint(fg, 0.15)).
		Children(func() {
			ui.Text(c, label).FontSize(12).TextColor(fg)
		})
}

// SectionHeader 是区块标题：Bold 15 号标题 + 灰色副标题（副标题可为空）。
func SectionHeader(c *ui.Context, title, subtitle string) ui.Element {
	t := c.Theme()
	return ui.Column(c).Gap(2).Children(func() {
		ui.Text(c, title).Bold().FontSize(15)
		if subtitle != "" {
			ui.Text(c, subtitle).FontSize(12).TextColor(t.TextMuted).Wrap()
		}
	})
}

// KV 是键值行：键 TextMuted 固定宽约 120，值可换行。
func KV(c *ui.Context, key, value string) ui.Element {
	t := c.Theme()
	return ui.Row(c).Gap(8).Children(func() {
		ui.Text(c, key).TextColor(t.TextMuted).Width(120)
		ui.Text(c, value).Wrap().Grow(1)
	})
}

// MetricCard 是指标卡：Surface 背景、圆角、Padding 12（标签 + 数值 + 提示）。
func MetricCard(c *ui.Context, label, value, hint string) ui.Element {
	t := c.Theme()
	return ui.Column(c).
		Gap(4).
		Padding(12).
		Radius(radius(t)).
		Background(t.Surface).
		Children(func() {
			ui.Text(c, label).FontSize(12).TextColor(t.TextMuted)
			ui.Text(c, value).Bold().FontSize(22)
			if hint != "" {
				ui.Text(c, hint).FontSize(11).TextColor(t.TextMuted).Wrap()
			}
		})
}

// monoPanel 是 MonospaceBlock 与 DiffText 共用的容器：
// Surface 背景、内边距 8、最大高度 320，外包 ui.Scroll。
func monoPanel(c *ui.Context, build func()) ui.Element {
	t := c.Theme()
	return ui.Scroll(c).MaxHeight(320).Fill().Children(func() {
		ui.Column(c).
			Padding(8).
			Radius(radius(t)).
			Background(t.Surface).
			Children(build)
	})
}

// MonospaceBlock 是等宽可选中多行块：monospace 12 号、Wrap、Selectable。
// 空文本显示 TextMuted 的“（无）”。
func MonospaceBlock(c *ui.Context, text string) ui.Element {
	t := c.Theme()
	return monoPanel(c, func() {
		if strings.TrimSpace(text) == "" {
			ui.Text(c, "（无）").FontSize(12).TextColor(t.TextMuted)
			return
		}
		ui.Text(c, text).
			Font("monospace").
			FontSize(12).
			Wrap().
			Selectable()
	})
}

// EmptyState 是居中空态：标题 + 灰色提示。
func EmptyState(c *ui.Context, title, hint string) ui.Element {
	t := c.Theme()
	return ui.Column(c).
		Fill().
		AlignItems(ui.Center).
		Justify(ui.Center).
		Gap(6).
		Padding(24).
		Children(func() {
			ui.Text(c, title).Bold().FontSize(15)
			if hint != "" {
				ui.Text(c, hint).FontSize(12).TextColor(t.TextMuted).Wrap()
			}
		})
}

// LoadingState 是加载态：Spinner + 文本横排居中。
func LoadingState(c *ui.Context, message string) ui.Element {
	return ui.Row(c).
		Fill().
		AlignItems(ui.Center).
		Justify(ui.Center).
		Gap(8).
		Padding(16).
		Children(func() {
			ui.Spinner(c)
			if message != "" {
				ui.Text(c, message).FontSize(13)
			}
		})
}

// ErrorState 是 Danger 色提示块（Danger 15% 背景 + Danger 文字）。
func ErrorState(c *ui.Context, message string) ui.Element {
	t := c.Theme()
	fg := toneColor(t, ToneDanger)
	return ui.Row(c).
		Padding(10).
		Radius(radius(t)).
		Background(tint(fg, 0.15)).
		Children(func() {
			ui.Text(c, message).TextColor(fg).Wrap()
		})
}

// DiffText 渲染 unified diff：逐行按前缀着色的等宽行集合，
// “+” Success 绿、“-” Danger 红、“@” Accent 蓝、其余 TextMuted，
// 放在 MonospaceBlock 风格容器里。空 diff 显示“（无）”。
func DiffText(c *ui.Context, diff string) ui.Element {
	t := c.Theme()
	return monoPanel(c, func() {
		if strings.TrimSpace(diff) == "" {
			ui.Text(c, "（无）").Font("monospace").FontSize(12).TextColor(t.TextMuted)
			return
		}
		for _, line := range strings.Split(strings.TrimRight(diff, "\n"), "\n") {
			col := t.TextMuted
			switch {
			case strings.HasPrefix(line, "+"):
				col = toneColor(t, ToneSuccess)
			case strings.HasPrefix(line, "-"):
				col = toneColor(t, ToneDanger)
			case strings.HasPrefix(line, "@"):
				col = toneColor(t, ToneAccent)
			}
			ui.Text(c, line).
				Font("monospace").
				FontSize(12).
				TextColor(col).
				Wrap().
				Selectable()
		}
	})
}

// TruncateMiddle 把过长的字符串缩到至多 max 个字符，中间用 “…” 省略；
// max <= 0 返回空串，不超过 max 原样返回。
func TruncateMiddle(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	keep := max - 1 // 留给省略号
	head := keep / 2
	tail := keep - head
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
}

// FormatBytes 把字节数格式化为人类可读文本（1024 进制）：
// “0 B”、“512 B”、“1.5 KB”、“2.0 MB”……
func FormatBytes(n int64) string {
	sign := ""
	v := float64(n)
	if v < 0 {
		sign, v = "-", -v
	}
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%s%d B", sign, int64(v))
	}
	return fmt.Sprintf("%s%.1f %s", sign, v, units[i])
}

// FormatDuration 把毫秒格式化为人类可读时长：
// “250 ms”、“1.5 s”、“3 m 20 s”、“2 h 05 m”。
func FormatDuration(ms float64) string {
	sign := ""
	if ms < 0 {
		sign, ms = "-", -ms
	}
	switch {
	case ms < 1000:
		return fmt.Sprintf("%s%.0f ms", sign, ms)
	case ms < 60_000:
		return fmt.Sprintf("%s%.1f s", sign, ms/1000)
	case ms < 3_600_000:
		return fmt.Sprintf("%s%d m %02d s", sign, int(ms/60_000), int(ms/1000)%60)
	default:
		return fmt.Sprintf("%s%d h %02d m", sign, int(ms/3_600_000), int(ms/60_000)%60)
	}
}

// FormatTime 按本地时间格式化为 “2006-01-02 15:04:05”。
func FormatTime(t time.Time) string {
	return t.Local().Format("2006-01-02 15:04:05")
}
