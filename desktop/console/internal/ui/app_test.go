package ui

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/uvwt/agentdock/desktop/console/internal/runtimeapi"
)

// stubBackend 是 runtimeapi.Backend 的测试桩：返回固定数据或固定错误。
type stubBackend struct {
	mu       sync.Mutex
	failures int // >0 时每个调用递减并返回错误

	status    *runtimeapi.Status
	overview  *runtimeapi.Overview
	analytics *runtimeapi.Analytics
	catalog   *runtimeapi.Catalog
	tasks     *runtimeapi.TaskList
	task      *runtimeapi.TaskDetail
}

func (s *stubBackend) fail() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failures > 0 {
		s.failures--
		return errors.New("boom")
	}
	return nil
}

func (s *stubBackend) Status() (*runtimeapi.Status, error) {
	if err := s.fail(); err != nil {
		return nil, err
	}
	return s.status, nil
}

func (s *stubBackend) Overview() (*runtimeapi.Overview, error) {
	if err := s.fail(); err != nil {
		return nil, err
	}
	return s.overview, nil
}

func (s *stubBackend) Analytics() (*runtimeapi.Analytics, error) {
	if err := s.fail(); err != nil {
		return nil, err
	}
	return s.analytics, nil
}

func (s *stubBackend) Catalog() (*runtimeapi.Catalog, error) {
	if err := s.fail(); err != nil {
		return nil, err
	}
	return s.catalog, nil
}

func (s *stubBackend) Skills() (*runtimeapi.Skills, error)   { return &runtimeapi.Skills{}, nil }
func (s *stubBackend) Plugins() (*runtimeapi.Plugins, error) { return &runtimeapi.Plugins{}, nil }

func (s *stubBackend) Tasks(status string, limit int) (*runtimeapi.TaskList, error) {
	if err := s.fail(); err != nil {
		return nil, err
	}
	return s.tasks, nil
}

func (s *stubBackend) Task(id string) (*runtimeapi.TaskDetail, error) {
	if err := s.fail(); err != nil {
		return nil, err
	}
	return s.task, nil
}

func (s *stubBackend) Traces(query, tool, source, status string, limit int) (*runtimeapi.Traces, error) {
	if err := s.fail(); err != nil {
		return nil, err
	}
	return &runtimeapi.Traces{}, nil
}

func (s *stubBackend) FileChanges(query string, limit int) (*runtimeapi.FileChanges, error) {
	if err := s.fail(); err != nil {
		return nil, err
	}
	return &runtimeapi.FileChanges{}, nil
}

func (s *stubBackend) Artifacts() (*runtimeapi.Artifacts, error) {
	if err := s.fail(); err != nil {
		return nil, err
	}
	return &runtimeapi.Artifacts{}, nil
}

// newTestApp 构造 App + Tester，后台拉取完成经由 notified 频道通知。
func newTestApp(t *testing.T, backend runtimeapi.Backend) (*App, *ui.Tester, chan struct{}) {
	t.Helper()
	app := New(backend)
	notified := make(chan struct{}, 32)
	app.SetInvalidator(func() {
		notified <- struct{}{}
	})
	tt := ui.NewTester(app.View, 1180, 740)
	return app, tt, notified
}

// waitNotify 等待一次后台拉取完成（缓存已写入）。
func waitNotify(t *testing.T, notified chan struct{}, what string) {
	t.Helper()
	select {
	case <-notified:
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for %s", what)
	}
}

func testOverviewData() (*runtimeapi.Status, *runtimeapi.Overview, *runtimeapi.Analytics) {
	status := &runtimeapi.Status{
		OK:            true,
		Version:       "v9.9.9",
		AgentDockHome: "/home/ada/.agentdock",
		PathModel:     "relative",
		AuthEnabled:   true,
		ToolCount:     12,
	}
	overview := &runtimeapi.Overview{
		OK:      true,
		Tasks:   runtimeapi.OverviewTasks{Active: 3, Completed: 7, Blocked: 1, ActiveRecent24h: 2},
		Skills:  runtimeapi.OverviewCount{Count: 4},
		Plugins: runtimeapi.OverviewPlugins{Count: 2, Available: true},
		MCP:     runtimeapi.OverviewCount{Count: 1},
	}
	analytics := &runtimeapi.Analytics{
		OK:          true,
		TotalCalls:  128,
		TotalErrors: 4,
		WindowCalls: 12,
		ActiveCalls: 2,
	}
	return status, overview, analytics
}

func testCatalogData() *runtimeapi.Catalog {
	return &runtimeapi.Catalog{
		OK: true,
		Tools: []runtimeapi.CatalogTool{
			{
				Name:        "读取文件",
				Title:       "Read File",
				Description: "读取工作区中的文件内容",
				InputSchema: map[string]any{"type": "object"},
			},
		},
		Rules: []runtimeapi.InstructionFile{
			{Scope: "global", Path: "AGENTS.md", Status: "active", Content: "# 规则"},
		},
		CommonSkills: runtimeapi.CommonSkillIndex{
			Items: []runtimeapi.CommonSkillItem{
				{Name: "审查代码", Description: "代码审查技能", File: "review/SKILL.md"},
			},
		},
	}
}

// TestOverviewDefaultRender 默认进入概览页：渲染侧边栏、指标与服务信息。
func TestOverviewDefaultRender(t *testing.T) {
	status, overview, analytics := testOverviewData()
	_, tt, notified := newTestApp(t, &stubBackend{
		status: status, overview: overview, analytics: analytics,
	})
	waitNotify(t, notified, "overview fetch")
	tt.Frame()

	for _, want := range []string{"概览", "观测", "服务信息", "v9.9.9", "总调用", "任务状态"} {
		if !tt.HasText(want) {
			t.Errorf("overview page missing text %q", want)
		}
	}
}

// TestNavigateToTasks 点击侧边栏“任务”切到任务页并渲染任务表格。
func TestNavigateToTasks(t *testing.T) {
	status, overview, analytics := testOverviewData()
	_, tt, notified := newTestApp(t, &stubBackend{
		status: status, overview: overview, analytics: analytics,
		tasks: &runtimeapi.TaskList{
			OK: true,
			Tasks: []runtimeapi.TaskListItem{
				{ID: "t1", Title: "修复构建流水线", Status: "active", Phase: "build", CompletedStepCount: 1, StepCount: 3},
			},
		},
	})
	waitNotify(t, notified, "overview fetch")
	tt.Frame()

	if err := tt.Click("任务"); err != nil {
		t.Fatalf("click 任务: %v", err)
	}
	waitNotify(t, notified, "tasks fetch")
	tt.Frame()

	if !tt.HasText("修复构建流水线") {
		t.Error("tasks page missing task title")
	}
	if !tt.HasText("任务列表与执行详情") {
		t.Error("tasks page missing subtitle")
	}
}

// TestTasksEmptyState 任务列表为空时渲染空态。
func TestTasksEmptyState(t *testing.T) {
	status, overview, analytics := testOverviewData()
	_, tt, notified := newTestApp(t, &stubBackend{
		status: status, overview: overview, analytics: analytics,
		tasks: &runtimeapi.TaskList{OK: true},
	})
	waitNotify(t, notified, "overview fetch")
	tt.Frame()

	if err := tt.Click("任务"); err != nil {
		t.Fatalf("click 任务: %v", err)
	}
	waitNotify(t, notified, "tasks fetch")
	tt.Frame()

	if !tt.HasText("暂无任务") {
		t.Error("tasks page missing empty state")
	}
}

// TestOverviewErrorState 后端报错时渲染错误态，“重试”可再次拉取成功。
func TestOverviewErrorState(t *testing.T) {
	status, overview, analytics := testOverviewData()
	_, tt, notified := newTestApp(t, &stubBackend{
		failures: 3, // Status/Overview/Analytics 各失败一次
		status:   status, overview: overview, analytics: analytics,
	})
	waitNotify(t, notified, "overview fetch")
	tt.Frame()

	if !tt.HasText("概览加载失败") {
		t.Error("overview page missing error state")
	}
	if !tt.HasText("重试") {
		t.Error("overview error state missing retry button")
	}

	if err := tt.Click("重试"); err != nil {
		t.Fatalf("click 重试: %v", err)
	}
	waitNotify(t, notified, "overview retry fetch")
	tt.Frame()

	if !tt.HasText("服务信息") {
		t.Error("overview page did not recover after retry")
	}
}

// TestCatalogRendersTools 能力目录页渲染分组列表与工具详情。
func TestCatalogRendersTools(t *testing.T) {
	status, overview, analytics := testOverviewData()
	_, tt, notified := newTestApp(t, &stubBackend{
		status: status, overview: overview, analytics: analytics,
		catalog: testCatalogData(),
	})
	waitNotify(t, notified, "overview fetch")
	tt.Frame()

	if err := tt.Click("能力"); err != nil {
		t.Fatalf("click 能力: %v", err)
	}
	waitNotify(t, notified, "catalog fetch")
	tt.Frame()

	if !tt.HasText("读取文件") {
		t.Fatal("catalog page missing tool name")
	}
	if !tt.HasText("常用技能") {
		t.Error("catalog page missing common skills group")
	}

	if err := tt.Click("读取文件"); err != nil {
		t.Fatalf("click tool row: %v", err)
	}
	tt.Frame()

	if !tt.HasText("输入 Schema") {
		t.Error("catalog detail missing input schema section")
	}
	if !tt.HasText("读取工作区中的文件内容") {
		t.Error("catalog detail missing tool description")
	}
}

// TestDefaultPageIsOverview 默认页面 id 为 overview。
func TestDefaultPageIsOverview(t *testing.T) {
	app := New(&stubBackend{})
	if app.page != pageOverview {
		t.Errorf("default page = %q, want %q", app.page, pageOverview)
	}
	if got := app.pageTitle(); !strings.Contains(got, "概览") {
		t.Errorf("page title = %q, want 概览", got)
	}
}
