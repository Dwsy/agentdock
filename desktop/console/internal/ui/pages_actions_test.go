package ui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/uvwt/agentdock/desktop/console/internal/runtimeapi"
)

// testActionBackend 构造带任务/详情/活动/产物的测试桩。
func testActionBackend() *stubBackend {
	status, overview, analytics := testOverviewData()
	return &stubBackend{
		status:    status,
		overview:  overview,
		analytics: analytics,
		tasks: &runtimeapi.TaskList{
			OK: true,
			Tasks: []runtimeapi.TaskListItem{
				{ID: "t1", Title: "修复构建流水线", Status: "active", Phase: "build", CompletedStepCount: 1, StepCount: 3},
			},
		},
		task: &runtimeapi.TaskDetail{
			OK: true,
			Task: &runtimeapi.Task{
				ID: "t1", Title: "修复构建流水线", Goal: "让构建通过", Status: "active", Phase: "build",
			},
		},
	}
}

// startTaskDetail 进入任务页并点选第一行任务，等待详情与活动拉取完成。
func startTaskDetail(t *testing.T, backend *stubBackend) (*App, *ui.Tester, chan struct{}) {
	t.Helper()
	app, tt, notified := newTestApp(t, backend)
	waitNotify(t, notified, "overview fetch")
	tt.Frame()
	if err := tt.Click("任务"); err != nil {
		t.Fatalf("click 任务: %v", err)
	}
	waitNotify(t, notified, "tasks fetch")
	tt.Frame()
	if err := tt.Click("修复构建流水线"); err != nil {
		t.Fatalf("click task row: %v", err)
	}
	waitNotify(t, notified, "task detail fetch")
	waitNotify(t, notified, "task activity fetch")
	tt.Frame()
	return app, tt, notified
}

// TestTaskDetailActivitySection 任务详情底部渲染“本任务的调用与文件”：
// 调用表（时间/工具/来源/UA/耗时/成功）与文件变更列表（路径/±行数）。
func TestTaskDetailActivitySection(t *testing.T) {
	backend := testActionBackend()
	backend.activity = &runtimeapi.TaskActivity{
		OK: true, TaskID: "t1", Mode: "memory",
		Calls: []runtimeapi.ActivityCall{{
			ID: 1, Tool: "bash", Source: "cli", UserAgent: "agent/1.0 (macOS)",
			StartedAt: time.Now(), DurationMS: 12, Success: true,
		}},
		FileChanges: []runtimeapi.ActivityChange{{
			TraceID: "tr-1", Tool: "write_file", StartedAt: time.Now(),
			Path: "a.go", Action: "modify", Insertions: 2, Deletions: 1,
			Diff: "-old line\n+new line",
		}},
		CallCount: 1, ChangeCount: 1,
	}
	_, tt, _ := startTaskDetail(t, backend)

	for _, want := range []string{"本任务的调用与文件", "bash", "agent/1.0", "a.go", "+2/-1"} {
		if !tt.HasText(want) {
			t.Errorf("task activity section missing text %q", want)
		}
	}
	if tt.HasText("trace 未开启") {
		t.Error("activity section should not show off state when mode != off")
	}
}

// TestTaskDetailActivityOff trace 未开启（mode=off）时渲染 EmptyState。
func TestTaskDetailActivityOff(t *testing.T) {
	backend := testActionBackend()
	backend.activity = &runtimeapi.TaskActivity{
		OK: true, TaskID: "t1", Mode: "off",
		Calls: []runtimeapi.ActivityCall{}, FileChanges: []runtimeapi.ActivityChange{},
	}
	_, tt, _ := startTaskDetail(t, backend)

	if !tt.HasText("trace 未开启") {
		t.Error("activity section missing off empty state")
	}
	if tt.HasText("本任务的调用与文件") && tt.HasText("a.go") {
		t.Error("off state should not render calls or file changes")
	}
}

// TestTaskDeleteConfirmFlow 删除走 AlertDialog 二次确认：取消不执行，确认才调用
// TaskActions(delete)，成功后 Toast 反馈并刷新任务列表。
func TestTaskDeleteConfirmFlow(t *testing.T) {
	backend := testActionBackend()
	_, tt, notified := startTaskDetail(t, backend)

	// 首次点击 Delete：弹出确认对话框，取消不执行。
	if err := tt.Click("Delete"); err != nil {
		t.Fatalf("click Delete: %v", err)
	}
	tt.Frame()
	if !tt.HasText("删除后不可恢复") {
		t.Fatal("confirm dialog missing message")
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatalf("click 取消: %v", err)
	}
	tt.Frame()
	if ids, reqs := backend.actionCalls(); len(ids) != 0 || len(reqs) != 0 {
		t.Fatalf("TaskActions called after cancel: %v %v", ids, reqs)
	}

	// 再次点击 Delete 并确认：执行删除。
	if err := tt.Click("Delete"); err != nil {
		t.Fatalf("click Delete again: %v", err)
	}
	tt.Frame()
	if !tt.HasText("确认删除") {
		t.Fatal("confirm dialog missing confirm button")
	}
	if err := tt.Click("确认删除"); err != nil {
		t.Fatalf("click 确认删除: %v", err)
	}
	waitNotify(t, notified, "delete action")
	tt.Frame()

	ids, reqs := backend.actionCalls()
	if len(ids) != 1 || ids[0] != "t1" || len(reqs) != 1 || reqs[0].Action != "delete" {
		t.Errorf("TaskActions calls = %v %v, want [t1] [{delete}]", ids, reqs)
	}
	if !tt.HasText("任务已删除") {
		t.Error("missing success toast for delete")
	}

	// 成功后刷新任务列表（再拉一次 Tasks）。
	deadline := time.After(5 * time.Second)
	for backend.tasksFetches() < 2 {
		select {
		case <-notified:
		case <-deadline:
			t.Fatal("timeout waiting for task list refresh after delete")
		}
	}
}

// TestTaskActionErrorFeedback 操作失败时以 ErrorState 展示可读原因。
func TestTaskActionErrorFeedback(t *testing.T) {
	backend := testActionBackend()
	backend.taskActionErr = errors.New("任务已被其他进程更新")
	_, tt, notified := startTaskDetail(t, backend)

	if err := tt.Click("Checkpoint"); err != nil {
		t.Fatalf("click Checkpoint: %v", err)
	}
	waitNotify(t, notified, "checkpoint action")
	tt.Frame()

	if !tt.HasText("操作失败：任务已被其他进程更新") {
		t.Error("missing error state for failed action")
	}
	if ids, _ := backend.actionCalls(); len(ids) != 0 {
		t.Errorf("TaskActions recorded %v, want none on error", ids)
	}
}

// TestPublishedExtendValidation 延期小时数校验：非法输入提示并不调用后端，合法输入调用 ArtifactExtend。
func TestPublishedExtendValidation(t *testing.T) {
	backend := testActionBackend()
	backend.artifacts = &runtimeapi.Artifacts{
		OK: true, Configured: true,
		Artifacts: []runtimeapi.Artifact{{
			ArtifactID: "a1", Filename: "shot.png", MimeType: "image/png",
			SizeBytes: 10, URL: "https://pub.example/a1",
		}},
		Count: 1,
	}
	app, tt, notified := newTestApp(t, backend)
	waitNotify(t, notified, "overview fetch")
	tt.Frame()
	if err := tt.Click("发布"); err != nil {
		t.Fatalf("click 发布: %v", err)
	}
	waitNotify(t, notified, "artifacts fetch")
	tt.Frame()
	if err := tt.Click("shot.png"); err != nil {
		t.Fatalf("click artifact row: %v", err)
	}
	tt.Frame()

	// 超出 1..720：提示校验错误，不调用后端。
	app.pubExtendInput = "721"
	tt.Frame()
	if err := tt.Click("延期"); err != nil {
		t.Fatalf("click 延期: %v", err)
	}
	tt.Frame()
	if !tt.HasText("延期小时数需在 1 到 720 之间") {
		t.Error("missing validation error for out-of-range hours")
	}
	if got := backend.extendCalls(); len(got) != 0 {
		t.Errorf("ArtifactExtend called with %v, want none on invalid input", got)
	}

	// 合法小时数：调用 ArtifactExtend。
	app.pubExtendInput = "24"
	tt.Frame()
	if err := tt.Click("延期"); err != nil {
		t.Fatalf("click 延期 again: %v", err)
	}
	waitNotify(t, notified, "extend action")
	tt.Frame()
	if got := backend.extendCalls(); len(got) != 1 || got[0] != 24 {
		t.Errorf("ArtifactExtend calls = %v, want [24]", got)
	}
}

// TestTracesExportShowsPath 导出 JSON 写入缓存目录，状态栏显示保存路径，可复制路径。
func TestTracesExportShowsPath(t *testing.T) {
	backend := testActionBackend()
	backend.exportData = []byte(`{"records":[]}`)
	backend.exportType = "application/json"
	backend.exportName = "traces.json"

	cacheDir := t.TempDir()
	oldFn := userCacheDirFn
	userCacheDirFn = func() (string, error) { return cacheDir, nil }
	t.Cleanup(func() { userCacheDirFn = oldFn })

	_, tt, notified := newTestApp(t, backend)
	waitNotify(t, notified, "overview fetch")
	tt.Frame()
	if err := tt.Click("追踪"); err != nil {
		t.Fatalf("click 追踪: %v", err)
	}
	waitNotify(t, notified, "traces fetch")
	tt.Frame()

	if err := tt.Click("导出 JSON"); err != nil {
		t.Fatalf("click 导出 JSON: %v", err)
	}
	waitNotify(t, notified, "export done")
	tt.Frame()

	wantPath := filepath.Join(cacheDir, "agentdock-console", "exports", "traces.json")
	if !tt.HasText("已保存") {
		t.Error("status bar missing saved marker")
	}
	if !tt.HasText(wantPath) {
		t.Errorf("status bar missing export path %q", wantPath)
	}
	if got := backend.exportCalls(); len(got) != 1 || got[0] != "json" {
		t.Errorf("TracesExport calls = %v, want [json]", got)
	}
	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read export file: %v", err)
	}
	if string(data) != `{"records":[]}` {
		t.Errorf("export file = %s, want raw stream", data)
	}

	// 复制路径。
	if err := tt.Click("复制路径"); err != nil {
		t.Fatalf("click 复制路径: %v", err)
	}
	tt.Frame()
	if got := tt.Clipboard(); got != wantPath {
		t.Errorf("clipboard = %q, want %q", got, wantPath)
	}
}

// TestTracesClearConfirm 清空走二次确认（提示文件变更历史将同步清空），
// 取消不执行，确认才调用 TracesClear。
func TestTracesClearConfirm(t *testing.T) {
	backend := testActionBackend()
	backend.clearCount = 5
	_, tt, notified := newTestApp(t, backend)
	waitNotify(t, notified, "overview fetch")
	tt.Frame()
	if err := tt.Click("追踪"); err != nil {
		t.Fatalf("click 追踪: %v", err)
	}
	waitNotify(t, notified, "traces fetch")
	tt.Frame()

	if err := tt.Click("清空"); err != nil {
		t.Fatalf("click 清空: %v", err)
	}
	tt.Frame()
	if !tt.HasText("文件变更历史将同步清空") {
		t.Fatal("confirm dialog missing warning about file change history")
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatalf("click 取消: %v", err)
	}
	tt.Frame()
	if got := backend.clearCalls(); got != 0 {
		t.Fatalf("TracesClear called %d times after cancel, want 0", got)
	}

	if err := tt.Click("清空"); err != nil {
		t.Fatalf("click 清空 again: %v", err)
	}
	tt.Frame()
	if err := tt.Click("确认清空"); err != nil {
		t.Fatalf("click 确认清空: %v", err)
	}
	waitNotify(t, notified, "clear done")
	tt.Frame()
	if got := backend.clearCalls(); got != 1 {
		t.Errorf("TracesClear calls = %d, want 1", got)
	}
	if !tt.HasText("已清空 5 条追踪记录") {
		t.Error("missing success toast for clear")
	}
}
