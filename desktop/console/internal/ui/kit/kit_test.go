package kit

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// ---------- 无窗口渲染测试（ui.NewTester 内存渲染） ----------

func TestStatusChip(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Padding(16).Gap(8).Children(func() {
			StatusChip(c, "运行中", ToneSuccess)
			StatusChip(c, "已降级", ToneWarning)
			StatusChip(c, "失败", ToneDanger)
		})
	}, 640, 480)
	for _, want := range []string{"运行中", "已降级", "失败"} {
		if !tt.HasText(want) {
			t.Fatalf("StatusChip 未渲染文本 %q，实际文本: %v", want, tt.Texts())
		}
	}
}

func TestSectionHeader(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		SectionHeader(c, "能力目录", "本机已安装的能力与插件")
	}, 640, 480)
	if !tt.HasText("能力目录") {
		t.Fatalf("SectionHeader 缺少标题，实际文本: %v", tt.Texts())
	}
	if !tt.HasText("本机已安装的能力与插件") {
		t.Fatalf("SectionHeader 缺少副标题，实际文本: %v", tt.Texts())
	}
}

func TestKV(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		KV(c, "运行时", "agentdock-runtime v1.2.3")
	}, 640, 480)
	if !tt.HasText("运行时") || !tt.HasText("agentdock-runtime v1.2.3") {
		t.Fatalf("KV 未渲染键值，实际文本: %v", tt.Texts())
	}
}

func TestMetricCard(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		MetricCard(c, "任务总数", "128", "较昨日 +12")
	}, 640, 480)
	if !tt.HasText("任务总数") {
		t.Fatalf("MetricCard 缺少标签，实际文本: %v", tt.Texts())
	}
	if !tt.HasText("128") {
		t.Fatalf("MetricCard 缺少数值，实际文本: %v", tt.Texts())
	}
	if !tt.HasText("较昨日 +12") {
		t.Fatalf("MetricCard 缺少提示，实际文本: %v", tt.Texts())
	}
}

func TestMonospaceBlock(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Padding(8).Children(func() {
			MonospaceBlock(c, "line 1\nline 2")
		})
	}, 640, 480)
	if !tt.HasText("line 1") {
		t.Fatalf("MonospaceBlock 缺少正文，实际文本: %v", tt.Texts())
	}

	tt = ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Padding(8).Children(func() {
			MonospaceBlock(c, "   ")
		})
	}, 640, 480)
	if !tt.HasText("（无）") {
		t.Fatalf("MonospaceBlock 空文本应显示（无），实际文本: %v", tt.Texts())
	}
}

func TestEmptyState(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		EmptyState(c, "暂无任务", "任务执行后会出现在这里")
	}, 640, 480)
	if !tt.HasText("暂无任务") {
		t.Fatalf("EmptyState 缺少标题，实际文本: %v", tt.Texts())
	}
	if !tt.HasText("任务执行后会出现在这里") {
		t.Fatalf("EmptyState 缺少提示，实际文本: %v", tt.Texts())
	}
}

func TestLoadingState(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		LoadingState(c, "加载任务列表…")
	}, 640, 480)
	if !tt.HasText("加载任务列表…") {
		t.Fatalf("LoadingState 缺少消息，实际文本: %v", tt.Texts())
	}
}

func TestErrorState(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ErrorState(c, "运行时连接失败")
	}, 640, 480)
	if !tt.HasText("运行时连接失败") {
		t.Fatalf("ErrorState 缺少错误消息，实际文本: %v", tt.Texts())
	}
}

func TestDiffText(t *testing.T) {
	diff := "@@ -1,3 +1,3 @@\n context line\n-旧的行\n+新的行\n"
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Padding(8).Children(func() {
			DiffText(c, diff)
		})
	}, 640, 480)
	for _, want := range []string{"@@ -1,3 +1,3 @@", " context line", "-旧的行", "+新的行"} {
		if !tt.HasText(want) {
			t.Fatalf("DiffText 未渲染行 %q，实际文本: %v", want, tt.Texts())
		}
	}

	tt = ui.NewTester(func(c *ui.Context) {
		DiffText(c, "")
	}, 640, 480)
	if !tt.HasText("（无）") {
		t.Fatalf("DiffText 空 diff 应显示（无），实际文本: %v", tt.Texts())
	}
}

func TestIconsParseAndCache(t *testing.T) {
	icons := map[string]func() *ui.SVG{
		"IconGauge":        IconGauge,
		"IconPuzzle":       IconPuzzle,
		"IconListChecks":   IconListChecks,
		"IconFileDiff":     IconFileDiff,
		"IconShare":        IconShare,
		"IconActivity":     IconActivity,
		"IconChartBar":     IconChartBar,
		"IconSearch":       IconSearch,
		"IconRefresh":      IconRefresh,
		"IconCopy":         IconCopy,
		"IconExternalLink": IconExternalLink,
		"IconCheck":        IconCheck,
	}
	if len(icons) != 12 {
		t.Fatalf("应有 12 个图标，实际 %d", len(icons))
	}
	for name, get := range icons {
		a := get()
		if a == nil {
			t.Fatalf("%s 返回 nil", name)
		}
		if b := get(); b != a {
			t.Fatalf("%s 未缓存解析结果", name)
		}
	}

	// 图标能在无窗口渲染里显示。
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Padding(8).Gap(8).Children(func() {
			ui.Icon(c, IconGauge())
			ui.Icon(c, IconCheck())
			ui.Text(c, "图标")
		})
	}, 640, 480)
	if !tt.HasText("图标") {
		t.Fatalf("图标渲染测试异常，实际文本: %v", tt.Texts())
	}
}

// ---------- 纯函数表驱动测试 ----------

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		name string
		in   int64
		want string
	}{
		{"零", 0, "0 B"},
		{"字节", 512, "512 B"},
		{"不足 1K", 1023, "1023 B"},
		{"整 1K", 1024, "1.0 KB"},
		{"1.5K", 1536, "1.5 KB"},
		{"整 1M", 1024 * 1024, "1.0 MB"},
		{"5G", 5 * 1024 * 1024 * 1024, "5.0 GB"},
		{"负数", -2048, "-2.0 KB"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatBytes(tc.in); got != tc.want {
				t.Fatalf("FormatBytes(%d) = %q，期望 %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		name string
		in   float64
		want string
	}{
		{"零", 0, "0 ms"},
		{"毫秒", 5, "5 ms"},
		{"不足 1s", 999, "999 ms"},
		{"整 1s", 1000, "1.0 s"},
		{"1.5s", 1500, "1.5 s"},
		{"整 1m", 60_000, "1 m 00 s"},
		{"3m20s", 200_000, "3 m 20 s"},
		{"整 1h", 3_600_000, "1 h 00 m"},
		{"1h30m", 5_400_000, "1 h 30 m"},
		{"负数", -1500, "-1.5 s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatDuration(tc.in); got != tc.want {
				t.Fatalf("FormatDuration(%v) = %q，期望 %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestTruncateMiddle(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"未超长", "hello", 10, "hello"},
		{"正好等于上限", "hello", 5, "hello"},
		{"省略中间", "hello world", 8, "hel…orld"},
		{"上限为 1", "abc", 1, "…"},
		{"上限为 0", "abc", 0, ""},
		{"上限为负", "abc", -1, ""},
		{"偶数上限", "abcdef", 4, "a…ef"},
		{"按字符而非字节", "一二三四五", 3, "一…五"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TruncateMiddle(tc.in, tc.max); got != tc.want {
				t.Fatalf("TruncateMiddle(%q, %d) = %q，期望 %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

func TestFormatTime(t *testing.T) {
	local := time.Date(2024, 3, 4, 5, 6, 7, 0, time.Local)
	utc := time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC)
	cases := []struct {
		name string
		in   time.Time
		want string
	}{
		{"本地时间", local, "2024-03-04 05:06:07"},
		{"UTC 转本地", utc, utc.Local().Format("2006-01-02 15:04:05")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatTime(tc.in); got != tc.want {
				t.Fatalf("FormatTime(%v) = %q，期望 %q", tc.in, got, tc.want)
			}
		})
	}
}
