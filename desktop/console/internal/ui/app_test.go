package ui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/uvwt/agentdock/desktop/console/internal/runtimeapi"
	"github.com/uvwt/agentdock/desktop/console/internal/ui/kit"
)

// stubBackend 是 runtimeapi.Backend 的测试桩：返回固定数据或固定错误，
// 并记录操作闭环（任务动作/产物延期撤销/导出清空）的调用供测试断言。
type stubBackend struct {
	mu       sync.Mutex
	failures int // >0 时每个调用递减并返回错误

	status    *runtimeapi.Status
	overview  *runtimeapi.Overview
	analytics *runtimeapi.Analytics
	catalog   *runtimeapi.Catalog
	tasks     *runtimeapi.TaskList
	task      *runtimeapi.TaskDetail
	activity  *runtimeapi.TaskActivity
	artifacts *runtimeapi.Artifacts
	alerts    *runtimeapi.AlertsResponse // 告警响应（nil 时返回空告警）

	// 操作桩：错误与响应可注入，调用被记录。
	taskActionErr error
	exportData    []byte
	exportType    string
	exportName    string
	exportErr     error
	clearCount    int

	taskActions  []runtimeapi.TaskActionsRequest
	taskActIDs   []string
	revoked      []string
	extended     []int
	clears       int
	exports      []string // 每次导出的 format
	taskFetches  int      // Tasks() 拉取次数（验证操作后刷新任务列表）
	alertFetches int      // Alerts() 拉取次数（验证节流拉取）
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

func (s *stubBackend) TaskActions(id string, req runtimeapi.TaskActionsRequest) (*runtimeapi.TaskActionResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.taskActionErr != nil {
		return nil, s.taskActionErr
	}
	s.taskActions = append(s.taskActions, req)
	s.taskActIDs = append(s.taskActIDs, id)
	return &runtimeapi.TaskActionResponse{
		OK: true, Source: "agentdock-api", Action: req.Action, TaskID: id,
	}, nil
}

func (s *stubBackend) TaskActivity(id string) (*runtimeapi.TaskActivity, error) {
	if err := s.fail(); err != nil {
		return nil, err
	}
	if s.activity != nil {
		return s.activity, nil
	}
	return &runtimeapi.TaskActivity{}, nil
}

func (s *stubBackend) ArtifactRevoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revoked = append(s.revoked, id)
	return nil
}

func (s *stubBackend) ArtifactExtend(id string, hours int) (*runtimeapi.Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.extended = append(s.extended, hours)
	return &runtimeapi.Artifact{ArtifactID: id, ExpiresAt: time.Now().Add(time.Duration(hours) * time.Hour)}, nil
}

func (s *stubBackend) TracesExport(format, query, tool, source, status string, limit int) ([]byte, string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exports = append(s.exports, format)
	if s.exportErr != nil {
		return nil, "", "", s.exportErr
	}
	data, typ, name := s.exportData, s.exportType, s.exportName
	if data == nil {
		data = []byte("{}")
	}
	if typ == "" {
		typ = "application/json"
	}
	return data, typ, name, nil
}

func (s *stubBackend) TracesClear() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clears++
	return s.clearCount, nil
}

// 操作记录读取（测试断言用）。
func (s *stubBackend) actionCalls() ([]string, []runtimeapi.TaskActionsRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := append([]string(nil), s.taskActIDs...)
	reqs := append([]runtimeapi.TaskActionsRequest(nil), s.taskActions...)
	return ids, reqs
}

func (s *stubBackend) revokeCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.revoked...)
}

func (s *stubBackend) extendCalls() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int(nil), s.extended...)
}

func (s *stubBackend) exportCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.exports...)
}

func (s *stubBackend) clearCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clears
}

func (s *stubBackend) Tasks(status string, limit int) (*runtimeapi.TaskList, error) {
	if err := s.fail(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.taskFetches++
	s.mu.Unlock()
	return s.tasks, nil
}

func (s *stubBackend) tasksFetches() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.taskFetches
}

// Alerts 实现 Backend：返回注入的告警响应（默认空告警），并计数拉取次数。
func (s *stubBackend) Alerts() (*runtimeapi.AlertsResponse, error) {
	if err := s.fail(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alertFetches++
	if s.alerts != nil {
		return s.alerts, nil
	}
	return &runtimeapi.AlertsResponse{OK: true, Source: "agentdock-api"}, nil
}

func (s *stubBackend) alertsFetches() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.alertFetches
}

func (s *stubBackend) setAlerts(resp *runtimeapi.AlertsResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alerts = resp
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
	if s.artifacts != nil {
		return s.artifacts, nil
	}
	return &runtimeapi.Artifacts{}, nil
}

// newTestApp 构造 App + Tester，后台拉取完成经由 notified 频道通知；
// 测试收尾自动 Close（停止实时事件流并等待后台 goroutine，防泄漏）。
func newTestApp(t *testing.T, backend runtimeapi.Backend) (*App, *ui.Tester, chan struct{}) {
	t.Helper()
	app := New(backend)
	t.Cleanup(app.Close)
	notified := make(chan struct{}, 32)
	app.SetInvalidator(func() {
		// 非阻塞投递：满则丢弃，避免后台 goroutine 反压阻塞 Close 回收。
		select {
		case notified <- struct{}{}:
		default:
		}
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

// ---------- 顶栏实时指示灯语义 ----------

// sseFailBackend 在 stubBackend 上实现 EventsOpener：OpenEvents 恒失败，
// 模拟旧 Core（无 /internal/runtime/events）——SSE 永远连不上、走轮询兜底。
type sseFailBackend struct {
	stubBackend
}

func (s *sseFailBackend) OpenEvents(context.Context) (runtimeapi.Events, error) {
	return nil, errors.New("sse endpoint missing")
}

// TestRealtimeIndicatorSemantics 固化顶栏指示灯语义：
// SSE 已连接="实时"（accent）；断开/未接入但轮询兜底有数据="轮询"（neutral）；
// 完全拿不到数据且在重连="重连中…"（warning）。
func TestRealtimeIndicatorSemantics(t *testing.T) {
	open := func(context.Context) (runtimeapi.Events, error) {
		return nil, errors.New("sse down")
	}
	check := func(name string, a *App, want string, wantTone kit.Tone) {
		t.Helper()
		label, tone := a.realtimeIndicator()
		if label != want || tone != wantTone {
			t.Errorf("%s: indicator = (%q, %v), want (%q, %v)", name, label, tone, want, wantTone)
		}
	}

	a := New(&stubBackend{}) // 未接入 SSE：纯轮询后端
	check("no sse", a, "轮询", kit.ToneNeutral)
	a.Close()

	a = New(&stubBackend{}) // SSE 已连接
	a.openEvents = open
	a.setRealtime(true)
	check("sse connected", a, "实时", kit.ToneAccent)
	a.Close()

	a = New(&stubBackend{}) // SSE 断开但轮询兜底拿到数据（旧 Core）
	a.openEvents = open
	a.setRealtime(false)
	a.done(pageOverview, func() {}, nil)
	check("polling fallback", a, "轮询", kit.ToneNeutral)
	a.Close()

	a = New(&stubBackend{}) // 完全拿不到数据且在重连
	a.openEvents = open
	a.setRealtime(false)
	a.done(pageOverview, func() {}, errors.New("backend down"))
	check("starved", a, "重连中…", kit.ToneWarning)
	a.Close()
}

// TestRealtimeIndicatorPollingFallback 旧 Core（SSE 连不上）但轮询有数据时，
// 顶栏显示"轮询"而不是吓人的"重连中…"（截图症状 5 的回归）。
func TestRealtimeIndicatorPollingFallback(t *testing.T) {
	status, overview, analytics := testOverviewData()
	_, tt, notified := newTestApp(t, &sseFailBackend{stubBackend: stubBackend{
		status: status, overview: overview, analytics: analytics,
	}})
	waitNotify(t, notified, "overview fetch")
	tt.Frame()

	if !tt.HasText("轮询") {
		t.Error("indicator should show 轮询 when the polling fallback gets data")
	}
	if tt.HasText("重连中") {
		t.Error("indicator must not show 重连中… while the polling fallback gets data")
	}
}

// TestRealtimeIndicatorStarved 完全拿不到数据时才显示"重连中…"。
func TestRealtimeIndicatorStarved(t *testing.T) {
	_, tt, notified := newTestApp(t, &sseFailBackend{stubBackend: stubBackend{failures: 3}})
	waitNotify(t, notified, "overview fetch failure")
	tt.Frame()

	if !tt.HasText("重连中…") {
		t.Error("indicator should show 重连中… when no data can be fetched")
	}
}
