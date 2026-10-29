package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/uvwt/agentdock/desktop/console/internal/runtimeapi"
)

// searchTestTasks 是搜索测试用的任务缓存：t0 不匹配查询，t1/t2 匹配"修复"。
func searchTestTasks() *runtimeapi.TaskList {
	return &runtimeapi.TaskList{
		OK: true,
		Tasks: []runtimeapi.TaskListItem{
			{ID: "t-doc", Title: "编写发布文档", Status: "active"},
			{ID: "t-build", Title: "修复构建流水线", Status: "active", Goal: "让流水线变绿"},
			{ID: "t-env", Title: "修复测试环境", Status: "blocked"},
		},
	}
}

// searchTestApp 构造带任务缓存的 App，进入 ⌘K 搜索浮层并等聚合拉取完成。
func searchTestApp(t *testing.T) (*App, *ui.Tester, chan struct{}) {
	t.Helper()
	status, overview, analytics := testOverviewData()
	app, tt, notified := newTestApp(t, &stubBackend{
		status: status, overview: overview, analytics: analytics,
		tasks: searchTestTasks(),
		task: &runtimeapi.TaskDetail{Task: &runtimeapi.Task{
			ID: "t-build", Title: "修复构建流水线", Goal: "让流水线变绿", Status: "active",
		}},
	})
	waitNotify(t, notified, "overview fetch")
	tt.Frame()

	tt.Key(ui.Cmd, ui.KeyK)
	waitNotify(t, notified, "search aggregate fetch")
	tt.Frame()
	return app, tt, notified
}

// TestSearchShortcutOpensAndEscCloses ⌘K 打开搜索浮层（空查询提示），Esc 关闭。
func TestSearchShortcutOpensAndEscCloses(t *testing.T) {
	status, overview, analytics := testOverviewData()
	app, tt, notified := newTestApp(t, &stubBackend{
		status: status, overview: overview, analytics: analytics,
	})
	waitNotify(t, notified, "overview fetch")
	tt.Frame()

	if app.searchOpen {
		t.Fatal("search overlay open before ⌘K")
	}
	tt.Key(ui.Cmd, ui.KeyK)
	if !app.searchOpen {
		t.Fatal("⌘K did not open the search overlay")
	}
	if !tt.HasText("输入以搜索…") {
		t.Error("search overlay missing the empty query hint")
	}

	tt.Key(0, ui.KeyEscape)
	if app.searchOpen {
		t.Error("Esc did not close the search overlay")
	}
	if tt.HasText("输入以搜索…") {
		t.Error("search overlay still rendered after Esc")
	}
}

// TestSearchFiltersResults 输入即时过滤出结果，分组展示并显示计数。
func TestSearchFiltersResults(t *testing.T) {
	_, tt, _ := searchTestApp(t)

	if !tt.HasText("输入以搜索…") {
		t.Error("search overlay missing the empty query hint")
	}

	tt.Type("修复")
	if !tt.HasText("任务（2）") {
		t.Errorf("group header missing count, texts: %v", tt.Texts())
	}
	for _, want := range []string{"修复构建流水线", "修复测试环境"} {
		if !tt.HasText(want) {
			t.Errorf("search results missing %q", want)
		}
	}
	if tt.HasText("编写发布文档") {
		t.Error("search results include a non-matching task")
	}
}

// TestSearchSelectJumpsToTasks 回车选中结果：跳转到任务页并定位选中项与详情。
func TestSearchSelectJumpsToTasks(t *testing.T) {
	app, tt, notified := searchTestApp(t)

	tt.Type("修复")
	tt.Key(0, ui.KeyEnter)

	if app.page != pageTasks {
		t.Errorf("page = %q, want %q", app.page, pageTasks)
	}
	if app.taskSel != 1 {
		t.Errorf("taskSel = %d, want 1", app.taskSel)
	}
	if app.searchOpen {
		t.Error("search overlay stayed open after picking a result")
	}

	// 任务列表与详情各拉取一次。
	waitNotify(t, notified, "tasks fetch")
	waitNotify(t, notified, "task detail fetch")
	tt.Frame()

	if app.taskDetailID != "t-build" {
		t.Errorf("taskDetailID = %q, want t-build", app.taskDetailID)
	}
	if !tt.HasText("任务详情") {
		t.Error("tasks page missing the located task detail")
	}
}

// TestSearchArrowKeysSelect 方向键 + 回车选中结果。
func TestSearchArrowKeysSelect(t *testing.T) {
	app, tt, notified := searchTestApp(t)

	tt.Type("修复")
	tt.Key(0, ui.KeyDown) // 从第 1 条移到第 2 条
	tt.Key(0, ui.KeyEnter)

	if app.page != pageTasks {
		t.Errorf("page = %q, want %q", app.page, pageTasks)
	}
	if app.taskSel != 2 {
		t.Errorf("taskSel = %d, want 2", app.taskSel)
	}
	if app.searchOpen {
		t.Error("search overlay stayed open after picking a result")
	}
	waitNotify(t, notified, "tasks fetch")
	waitNotify(t, notified, "task detail fetch")
	tt.Frame()
	if app.taskDetailID != "t-env" {
		t.Errorf("taskDetailID = %q, want t-env", app.taskDetailID)
	}
}

// TestSearchNoResultsState 查询无匹配时渲染空态。
func TestSearchNoResultsState(t *testing.T) {
	_, tt, _ := searchTestApp(t)

	tt.Type("zzzzz")
	if !tt.HasText("没有匹配结果") {
		t.Error("search overlay missing the empty state for no results")
	}
}
