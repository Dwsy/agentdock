package ui

import (
	"testing"
)

// 布局几何探针与回归断言（ui.NewTester 无窗口渲染，1180x740）。
//
// 背景：一次视觉回归里根 Row 未拉伸（ui.Row 默认 AlignItems=Center）导致侧栏
// 悬浮居中、工具条 Fill 的 height:100% 撑满内容列把标题挤到窗口中部、SectionHeader
// 与指标卡掉出窗口底部。这里的断言把这些症状固化成红线：
//   - 侧栏全高贴左（height>=700、x<240、贴顶贴底）；
//   - 工具条（含标题）贴顶（y<80）；
//   - SectionHeader 在可视区上部（y<160）；
//   - 正文首个指标数值在可视区（y<320 且真正在渲染，rect 非零）；
//   - 所有可见文本不越出窗口。

// probeRects 打印关键文本的 Rect，供人工定位布局病灶（无断言，诊断用）。
func TestLayoutProbe(t *testing.T) {
	status, overview, analytics := testOverviewData()
	_, tt, notified := newTestApp(t, &stubBackend{
		status: status, overview: overview, analytics: analytics,
	})
	waitNotify(t, notified, "overview fetch")
	tt.Frame()

	t.Log("--- key rects (window 1180x740) ---")
	for _, s := range []string{
		"侧栏",                 // Sidebar 容器（Label）
		"任务",                 // 侧栏最后一项
		"概览",                 // 侧栏项（Find 取第一个命中）
		"观测",                 // 侧栏分组标题
		"工具条",                // 工具条行（Label）
		"刷新",                 // 工具条按钮
		"轮询",                 // 顶栏指示灯
		"Runtime 状态、任务与调用指标", // SectionHeader 副标题
		"调用指标",               // 正文小节标题
		"总调用",                // MetricCard 标签
		"128",                // MetricCard 数值
		"服务信息",               // 正文小节
	} {
		r, ok := tt.Find(s)
		t.Logf("find %-28q -> x=%7.1f y=%7.1f w=%7.1f h=%7.1f ok=%v", s, r.X, r.Y, r.W, r.H, ok)
	}
}

// TestLayoutRegressionOverview 固化概览页布局红线：侧栏全高贴左、工具条与
// SectionHeader 贴顶、正文指标数值在可视区、可见文本不越出窗口。
func TestLayoutRegressionOverview(t *testing.T) {
	status, overview, analytics := testOverviewData()
	_, tt, notified := newTestApp(t, &stubBackend{
		status: status, overview: overview, analytics: analytics,
	})
	waitNotify(t, notified, "overview fetch")
	tt.Frame()

	// 侧栏全高、贴左、贴顶贴底。
	r, ok := tt.Find("侧栏")
	if !ok {
		t.Fatal("sidebar (Label 侧栏) not found")
	}
	if r.H < 700 {
		t.Errorf("sidebar height = %.1f, want >= 700 (full window height)", r.H)
	}
	if r.X >= 240 {
		t.Errorf("sidebar x = %.1f, want < 240", r.X)
	}
	if r.Y > 4 || r.Y+r.H < 736 {
		t.Errorf("sidebar rect %v, want flush with window top and bottom", r)
	}

	// 工具条（含标题）贴顶。
	tb, ok := tt.Find("工具条")
	if !ok {
		t.Fatal("toolbar (Label 工具条) not found")
	}
	if tb.Y >= 80 {
		t.Errorf("toolbar y = %.1f, want < 80", tb.Y)
	}
	if r, ok := tt.Find("刷新"); !ok {
		t.Error("toolbar button 刷新 not found")
	} else if r.Y >= 80 {
		t.Errorf("toolbar 刷新 y = %.1f, want < 80", r.Y)
	}

	// SectionHeader 在可视区上部。
	if r, ok := tt.Find("Runtime 状态、任务与调用指标"); !ok {
		t.Error("section header subtitle not found")
	} else if r.Y >= 160 {
		t.Errorf("section header y = %.1f, want < 160", r.Y)
	}

	// 正文首个指标数值在可视区（y<320 且真的渲染出来，rect 非零）。
	r, ok = tt.Find("128")
	if !ok {
		t.Fatal("metric value 128 not found")
	}
	if r.Y >= 320 {
		t.Errorf("metric value y = %.1f, want < 320", r.Y)
	}
	if r.W == 0 || r.H == 0 {
		t.Errorf("metric value rect %v degenerate, want visible in viewport", r)
	}

	// 所有可见文本（rect 非零）不越出窗口；零矩形是被滚动区裁掉的折叠内容，
	// 属于正常滚动语义，不算越界。
	const winW, winH = 1180, 740
	for _, s := range tt.Texts() {
		r, ok := tt.Find(s)
		if !ok || r.W == 0 || r.H == 0 {
			continue
		}
		if r.X < 0 || r.Y < 0 || r.X+r.W > winW || r.Y+r.H > winH {
			t.Errorf("text %q rect %v out of window %dx%d", s, r, winW, winH)
		}
	}
}

// TestLayoutRegressionToolbarTitle 工具条标题贴顶（y<80）且落在工具条行内。
// "概览"页标题与侧栏项同文案（Find 取首个命中），故切到标题唯一的"文件变更"
// 页断言标题文本本身。
func TestLayoutRegressionToolbarTitle(t *testing.T) {
	status, overview, analytics := testOverviewData()
	_, tt, notified := newTestApp(t, &stubBackend{
		status: status, overview: overview, analytics: analytics,
	})
	waitNotify(t, notified, "overview fetch")
	tt.Frame()

	if err := tt.Click("文件"); err != nil {
		t.Fatalf("click 文件: %v", err)
	}
	waitNotify(t, notified, "files fetch")
	tt.Frame()

	tb, ok := tt.Find("工具条")
	if !ok {
		t.Fatal("toolbar (Label 工具条) not found")
	}
	// 首个"文件变更"命中工具条标题（构建序先于 SectionHeader 的同名文本）：
	// 断言它确实落在工具条行内，再断言贴顶。
	r, ok := tt.Find("文件变更")
	if !ok {
		t.Fatal("toolbar title 文件变更 not found")
	}
	if r.Y < tb.Y || r.Y+r.H > tb.Y+tb.H {
		t.Errorf("toolbar title rect %v not inside toolbar %v", r, tb)
	}
	if r.Y >= 80 {
		t.Errorf("toolbar title y = %.1f, want < 80", r.Y)
	}
}
