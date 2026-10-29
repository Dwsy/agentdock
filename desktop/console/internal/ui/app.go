// Package ui 是 AgentDock Console 的页面层：用 MyGo native UI（github.com/egoist/mygo/ui）
// 组装各页面，只读消费 internal/runtimeapi.Backend 的数据契约。
//
// 架构约定：
//   - App 持有页面导航、各页缓存数据、加载状态与筛选/选中索引；
//   - 渲染只读缓存，缺数据时通过 ensureLoaded 触发后台拉取（refresh），
//     拉取完成后写缓存并调用 invalidate 请求重绘（main 绑定 window.Invalidate）；
//   - 每页三态齐全：loading → kit.LoadingState，错误 → kit.ErrorState（含重试），
//     空数据 → kit.EmptyState。
package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/uvwt/agentdock/desktop/console/internal/runtimeapi"
	"github.com/uvwt/agentdock/desktop/console/internal/ui/kit"
)

// 页面导航 id。
const (
	pageOverview  = "overview"
	pageAnalytics = "analytics"
	pageTraces    = "traces"
	pageCatalog   = "catalog"
	pageFiles     = "files"
	pagePublished = "published"
	pageTasks     = "tasks"
)

// loadState 跟踪一页数据的加载生命周期。
type loadState struct {
	loading bool // 有拉取在途
	loaded  bool // 至少成功/失败拉取过一次
	pending bool // 在途时又请求了刷新
	err     error
}

// App 是页面层根组件：左侧侧边栏导航 + 右侧按 page 分发的页面。
type App struct {
	backend    runtimeapi.Backend
	invalidate func() // 由 main 注入 window.Invalidate

	mu   sync.Mutex // 保护后台拉取写入的缓存字段
	page string     // 导航选择："overview"/"catalog"/"tasks"/"files"/"published"/"traces"/"analytics"

	// 侧边栏分组展开状态。
	secObs, secAssets, secWork bool

	// 概览页：Status + Overview + Analytics。
	ovLoad      loadState
	status      *runtimeapi.Status
	overview    *runtimeapi.Overview
	ovAnalytics *runtimeapi.Analytics

	// 能力目录页。
	catLoad  loadState
	catData  *runtimeapi.Catalog
	catQuery string
	catSel   int // 左侧过滤后扁平列表的选中索引
	catSplit float32

	// 任务页。
	taskLoad       loadState
	taskList       *runtimeapi.TaskList
	taskQuery      string
	taskStatus     int // Segmented 索引：全部/active/blocked/completed
	taskSel        int // 表格选中行
	taskSplit      float32
	taskDetailLoad loadState
	taskDetail     *runtimeapi.TaskDetail
	taskDetailID   string

	// 任务详情关联活动（本任务的调用与文件）。
	taskActLoad  loadState
	taskActivity *runtimeapi.TaskActivity
	taskActID    string
	actDiffOpen  map[string]bool // 活动文件变更 diff 展开状态

	// 任务操作（checkpoint/complete/delete）反馈；msg/err 由后台 goroutine 写入、渲染期取走。
	taskDeleteAsk bool // 删除二次确认（AlertDialog 开关，仅 UI 线程）
	taskActionMsg string
	taskActionErr error

	// 文件变更页。
	fileLoad  loadState
	fileData  *runtimeapi.FileChanges
	fileQuery string
	fileSel   int
	fileSplit float32

	// 发布产物页。
	pubLoad  loadState
	pubData  *runtimeapi.Artifacts
	pubSel   int
	pubSplit float32

	// 发布产物操作（撤销/延期）反馈；Input/Ask 仅 UI 线程，msg/err 后台写入。
	pubRevokeAsk   bool // 撤销二次确认（AlertDialog 开关）
	pubExtendInput string
	pubActionMsg   string
	pubActionErr   error

	// 调用追踪页。
	traceLoad     loadState
	traceData     *runtimeapi.Traces
	traceQuery    string
	traceTool     string // Select 显示值，"全部" 表示不过滤
	traceSource   string
	traceStatus   string
	traceSel      int
	traceSplit    float32
	traceDiffOpen map[string]bool // FileChanges 展开状态

	// 调用追踪页 5s 自动轮询。
	traceAuto        bool      // "自动刷新"开关（默认开），关闭后停止轮询
	tracePollAt      time.Time // 上次触发轮询拉取的帧时钟（c.Now()），时间戳去重
	traceRefreshedAt time.Time // 上次拉取完成的墙钟时间，工具条展示

	// 追踪页导出/清空；Ask 仅 UI 线程，路径/msg/err 后台写入。
	traceClearAsk   bool // 清空二次确认（AlertDialog 开关）
	traceExportPath string
	traceActionMsg  string
	traceActionErr  error

	// ⌘K 全局搜索。
	searchOpen    bool
	searchQuery   string
	searchSel     int  // 扁平结果列表的选中索引
	searchAggOnce bool // 缓存聚合拉取已发起过（缺失时只拉一次）

	// 统计页。
	anLoad loadState
	anData *runtimeapi.Analytics
	anSort int // ToolStats 排序：0 调用次数 / 1 错误率 / 2 P95 耗时

	// 实时事件流（SSE）：backend 实现 EventsOpener（即 runtimeapi.Client）时启用，
	// 断线指数退避重连；Close/Stop 收尾，后台 goroutine 统一走 bgWG 生命周期。
	openEvents  func(context.Context) (runtimeapi.Events, error)
	rtCtx       context.Context
	rtCancel    context.CancelFunc
	closeOnce   sync.Once
	bgWG        sync.WaitGroup // 后台 goroutine（SSE/拉取/告警）生命周期
	realtime    bool           // SSE 连接正常；false = 重连中或未接入（轮询兜底）
	rtConnected bool           // 曾成功连接过（重连成功后全量刷新的判定）
	rtPingAt    time.Time      // 最近一次 ping 保活的墙钟时间
	events      []rtEvent      // 实时事件时间线（最新在前，最多 rtEventLimit 条）

	// 顶部告警横幅：进入页面/收到事件时节流拉取 Alerts()（alertsFetchEvery 一次）。
	alerts          *runtimeapi.AlertsResponse
	alertsFetchedAt time.Time // 上次告警拉取完成的墙钟时间
	alertsFetching  bool      // 告警拉取在途
}

// New 创建页面层根组件。backend 实现 runtimeapi.EventsOpener（即 Client）时
// 同时启动实时事件流后台循环；测试/收尾用 Close（或 Stop）停掉并等待退出。
func New(backend runtimeapi.Backend) *App {
	rtCtx, rtCancel := context.WithCancel(context.Background())
	a := &App{
		backend:       backend,
		rtCtx:         rtCtx,
		rtCancel:      rtCancel,
		page:          pageOverview,
		secObs:        true,
		secAssets:     true,
		secWork:       true,
		catSel:        -1,
		taskSel:       -1,
		fileSel:       -1,
		pubSel:        -1,
		traceSel:      -1,
		catSplit:      320,
		taskSplit:     560,
		fileSplit:     360,
		pubSplit:      520,
		traceSplit:    560,
		traceTool:     "全部",
		traceSource:   "全部",
		traceStatus:   "全部",
		traceDiffOpen: map[string]bool{},
		actDiffOpen:   map[string]bool{},
		traceAuto:     true,
	}
	if opener, ok := backend.(runtimeapi.EventsOpener); ok {
		a.openEvents = opener.OpenEvents
		a.bgWG.Add(1)
		go a.runEvents()
	}
	return a
}

// Close 停止实时事件流并等待后台 goroutine 退出（供 main 与测试收尾）；幂等。
// 适合绑定窗口 OnClosed / 进程退出路径。
func (a *App) Close() {
	if a.rtCancel != nil {
		a.closeOnce.Do(a.rtCancel)
	}
	a.bgWG.Wait()
}

// Stop 是 Close 的别名（停止后台活动）。
func (a *App) Stop() {
	a.Close()
}

// spawn 启动一个受 Close 生命周期管理的后台 goroutine。
func (a *App) spawn(fn func()) {
	a.bgWG.Add(1)
	go func() {
		defer a.bgWG.Done()
		fn()
	}()
}

// SetInvalidator 注入重绘回调（main 里绑定 window.Invalidate）。
func (a *App) SetInvalidator(fn func()) {
	a.mu.Lock()
	a.invalidate = fn
	a.mu.Unlock()
}

// notify 请求一次重绘。
func (a *App) notify() {
	a.mu.Lock()
	fn := a.invalidate
	a.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// ---------- 页面骨架 ----------

// View 是根视图：左侧侧边栏（观测/资产/工作 三组）+ 右侧工具条与页面主体；
// 根上挂 ⌘K 窗口级快捷键打开全局搜索浮层。
func (a *App) View(c *ui.Context) {
	c.OnShortcut(ui.Cmd, ui.KeyK, func() {
		a.searchOpen = true
		a.searchSel = 0
	})
	ui.Row(c).Fill().Children(func() {
		a.sidebar(c)
		ui.Column(c).Grow(1).Padding(16).Gap(12).Children(func() {
			a.toolbar(c)
			a.renderPage(c)
		})
	})
	a.renderSearch(c)
}

// sidebar 是左侧导航：三组页面，SidebarItem 带 kit 图标。
func (a *App) sidebar(c *ui.Context) {
	ui.Sidebar(c, &a.page, func() {
		ui.SidebarSection(c, "观测", &a.secObs, func() {
			ui.SidebarItem(c, pageOverview, kit.IconGauge(), "概览")
			ui.SidebarItem(c, pageAnalytics, kit.IconChartBar(), "统计")
			ui.SidebarItem(c, pageTraces, kit.IconActivity(), "追踪")
		})
		ui.SidebarSection(c, "资产", &a.secAssets, func() {
			ui.SidebarItem(c, pageCatalog, kit.IconPuzzle(), "能力")
			ui.SidebarItem(c, pageFiles, kit.IconFileDiff(), "文件")
			ui.SidebarItem(c, pagePublished, kit.IconShare(), "发布")
		})
		ui.SidebarSection(c, "工作", &a.secWork, func() {
			ui.SidebarItem(c, pageTasks, kit.IconListChecks(), "任务")
		})
	}).Width(216).Shrink(0)
}

// toolbar 是页面顶部工具条：页面标题 + 实时连接指示灯 + 手动刷新按钮；
// 追踪页另有 "自动刷新"开关与上次刷新时间（该页 5s 自动轮询）。
func (a *App) toolbar(c *ui.Context) {
	ui.Row(c).Fill().AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, a.pageTitle()).Bold().FontSize(16).Grow(1)
		label, tone := a.realtimeIndicator()
		kit.StatusChip(c, label, tone)
		if a.page == pageTraces {
			a.traceAutoControls(c)
		}
		btn := ui.Button(c, "").Children(func() {
			ui.Icon(c, kit.IconRefresh())
			ui.Text(c, "刷新")
		})
		if btn.Clicked() {
			a.reload(a.page)
		}
	})
}

// renderPage 按导航选择分发到各页渲染；顶部先渲染活跃告警横幅，
// 并在进入页面时节流拉取告警。
func (a *App) renderPage(c *ui.Context) {
	a.ensureLoaded(a.page)
	a.maybeFetchAlerts()
	a.renderAlertBanner(c)
	switch a.page {
	case pageOverview:
		a.pageOverview(c)
	case pageAnalytics:
		a.pageAnalytics(c)
	case pageTraces:
		a.pageTraces(c)
	case pageCatalog:
		a.pageCatalog(c)
	case pageFiles:
		a.pageFiles(c)
	case pagePublished:
		a.pagePublished(c)
	case pageTasks:
		a.pageTasks(c)
	default:
		kit.EmptyState(c, "未知页面", "请从左侧边栏选择一个页面")
	}
}

// pageTitle 返回当前页的工具条标题。
func (a *App) pageTitle() string {
	switch a.page {
	case pageOverview:
		return "概览"
	case pageAnalytics:
		return "统计分析"
	case pageTraces:
		return "调用追踪"
	case pageCatalog:
		return "能力目录"
	case pageFiles:
		return "文件变更"
	case pagePublished:
		return "发布产物"
	case pageTasks:
		return "任务"
	}
	return "AgentDock Console"
}

// ---------- 加载生命周期 ----------

// loadOf 返回页面的加载状态（调用方需持有 a.mu）。
func (a *App) loadOf(page string) *loadState {
	switch page {
	case pageOverview:
		return &a.ovLoad
	case pageAnalytics:
		return &a.anLoad
	case pageTraces:
		return &a.traceLoad
	case pageCatalog:
		return &a.catLoad
	case pageFiles:
		return &a.fileLoad
	case pagePublished:
		return &a.pubLoad
	case pageTasks:
		return &a.taskLoad
	}
	return nil
}

// ensureLoaded 在该页尚未加载过且没有在途拉取时触发首次拉取（渲染期调用）。
func (a *App) ensureLoaded(page string) {
	a.mu.Lock()
	ls := a.loadOf(page)
	if ls == nil || ls.loading || ls.loaded {
		a.mu.Unlock()
		return
	}
	ls.loading = true
	a.mu.Unlock()
	a.spawn(func() { a.refresh(page) })
}

// reload 手动刷新一页；若已有拉取在途则排队一次补拉。
func (a *App) reload(page string) {
	a.mu.Lock()
	ls := a.loadOf(page)
	if ls == nil {
		a.mu.Unlock()
		return
	}
	if ls.loading {
		ls.pending = true
		a.mu.Unlock()
		return
	}
	ls.loading = true
	ls.err = nil
	a.mu.Unlock()
	a.spawn(func() { a.refresh(page) })
}

// refresh 在后台拉取一页数据，锁内写缓存后 invalidate()。页面渲染只读缓存。
func (a *App) refresh(page string) {
	switch page {
	case pageOverview:
		a.fetchOverview()
	case pageAnalytics:
		a.fetchAnalytics()
	case pageTraces:
		a.fetchTraces()
	case pageCatalog:
		a.fetchCatalog()
	case pageFiles:
		a.fetchFiles()
	case pagePublished:
		a.fetchArtifacts()
	case pageTasks:
		a.fetchTasks()
	}

	// 完成后如在途期间又请求过刷新，补拉一次。
	a.mu.Lock()
	ls := a.loadOf(page)
	again := ls != nil && ls.pending
	if ls != nil {
		ls.pending = false
	}
	a.mu.Unlock()
	if again {
		a.reload(page)
	}
	a.notify()
}

// done 一页拉取完成：写状态并在成功时发布数据。
func (a *App) done(page string, publish func(), err error) {
	a.mu.Lock()
	ls := a.loadOf(page)
	if ls != nil {
		ls.loading = false
		ls.loaded = true
		ls.err = err
	}
	if err == nil && publish != nil {
		publish()
	}
	a.mu.Unlock()
}

func (a *App) fetchOverview() {
	st, errSt := a.backend.Status()
	ov, errOv := a.backend.Overview()
	an, errAn := a.backend.Analytics()
	err := errSt
	if err == nil {
		err = errOv
	}
	if err == nil {
		err = errAn
	}
	a.done(pageOverview, func() {
		a.status, a.overview, a.ovAnalytics = st, ov, an
	}, err)
}

func (a *App) fetchAnalytics() {
	an, err := a.backend.Analytics()
	a.done(pageAnalytics, func() { a.anData = an }, err)
}

func (a *App) fetchCatalog() {
	cat, err := a.backend.Catalog()
	a.done(pageCatalog, func() { a.catData = cat }, err)
}

func (a *App) fetchTasks() {
	a.mu.Lock()
	status := taskStatusQuery(a.taskStatus)
	a.mu.Unlock()
	list, err := a.backend.Tasks(status, 200)
	a.done(pageTasks, func() { a.taskList = list }, err)
}

func (a *App) fetchFiles() {
	a.mu.Lock()
	query := a.fileQuery
	a.mu.Unlock()
	changes, err := a.backend.FileChanges(query, 200)
	a.done(pageFiles, func() { a.fileData = changes }, err)
}

func (a *App) fetchArtifacts() {
	arts, err := a.backend.Artifacts()
	a.done(pagePublished, func() { a.pubData = arts }, err)
}

func (a *App) fetchTraces() {
	a.mu.Lock()
	query := a.traceQuery
	tool, source, status := a.traceTool, a.traceSource, a.traceStatus
	a.mu.Unlock()
	recs, err := a.backend.Traces(query, selectFilter(tool), selectFilter(source), tracesStatusQuery(status), 200)
	a.done(pageTraces, func() { a.traceData = recs }, err)
	a.mu.Lock()
	a.traceRefreshedAt = time.Now()
	a.mu.Unlock()
}

// loadTaskDetail 拉取选中任务的详情与关联活动（切换选中行时触发）；
// 活动拉取独立缓存（taskActID/taskActLoad），互不阻塞。
func (a *App) loadTaskDetail(id string) {
	if id == "" {
		return
	}
	a.mu.Lock()
	if a.taskDetailID == id && (a.taskDetailLoad.loading || (a.taskDetailLoad.loaded && a.taskDetailLoad.err == nil)) {
		a.mu.Unlock()
	} else {
		a.taskDetailID = id
		a.taskDetail = nil
		a.taskDetailLoad = loadState{loading: true}
		a.mu.Unlock()
		a.spawn(func() {
			d, err := a.backend.Task(id)
			a.mu.Lock()
			if a.taskDetailID == id {
				a.taskDetail = d
				a.taskDetailLoad = loadState{loaded: true, err: err}
			}
			a.mu.Unlock()
			a.notify()
		})
	}
	a.loadTaskActivity(id)
}

// loadTaskActivity 拉取任务关联的调用与文件变更（详情底部区块）。
func (a *App) loadTaskActivity(id string) {
	if id == "" {
		return
	}
	a.mu.Lock()
	if a.taskActID == id && (a.taskActLoad.loading || (a.taskActLoad.loaded && a.taskActLoad.err == nil)) {
		a.mu.Unlock()
		return
	}
	a.taskActID = id
	a.taskActivity = nil
	a.taskActLoad = loadState{loading: true}
	a.mu.Unlock()
	a.spawn(func() {
		act, err := a.backend.TaskActivity(id)
		a.mu.Lock()
		if a.taskActID == id {
			a.taskActivity = act
			a.taskActLoad = loadState{loaded: true, err: err}
		}
		a.mu.Unlock()
		a.notify()
	})
}

// reloadTaskDetail 操作成功后强制重拉详情与活动缓存。
func (a *App) reloadTaskDetail(id string) {
	a.mu.Lock()
	if a.taskDetailID == id {
		a.taskDetailLoad = loadState{}
	}
	if a.taskActID == id {
		a.taskActLoad = loadState{}
	}
	a.mu.Unlock()
	a.loadTaskDetail(id)
}

// dropTaskDetail 丢弃任务详情缓存（任务删除后不再展示）。
func (a *App) dropTaskDetail() {
	a.mu.Lock()
	a.taskDetailID = ""
	a.taskDetail = nil
	a.taskDetailLoad = loadState{}
	a.taskActID = ""
	a.taskActivity = nil
	a.taskActLoad = loadState{}
	a.mu.Unlock()
	a.taskSel = -1
}

// ---------- 三态与小工具 ----------

// loadBanner 渲染某页的加载/错误/空态；返回 true 表示应继续渲染数据内容。
// hasData 表示缓存里已有可用数据（后台刷新期间继续展示旧数据）；retry 是错误态的重试动作。
func (a *App) loadBanner(c *ui.Context, what string, loading, loaded bool, err error, hasData bool, emptyTitle, emptyHint string, retry func()) bool {
	switch {
	case hasData:
		return true
	case loading && !loaded:
		kit.LoadingState(c, "正在加载"+what+"…")
		return false
	case err != nil:
		kit.ErrorState(c, what+"加载失败："+err.Error())
		ui.Row(c).Children(func() {
			btn := ui.Button(c, "").Children(func() {
				ui.Icon(c, kit.IconRefresh())
				ui.Text(c, "重试")
			})
			if btn.Clicked() {
				retry()
			}
		})
		return false
	default:
		kit.EmptyState(c, emptyTitle, emptyHint)
		return false
	}
}

// taskStatusQuery 把任务页 Segmented 索引映射为 Tasks() 的 status 参数。
func taskStatusQuery(sel int) string {
	switch sel {
	case 1:
		return "active"
	case 2:
		return "blocked"
	case 3:
		return "completed"
	}
	return ""
}

// selectFilter 把 Select 的显示值映射为过滤参数（"全部" 不过滤）。
func selectFilter(v string) string {
	if v == "" || v == "全部" {
		return ""
	}
	return v
}

// tracesStatusQuery 把追踪页状态 Select 映射为 Traces() 的 status 参数。
func tracesStatusQuery(v string) string {
	switch v {
	case "成功":
		return "success"
	case "失败":
		return "error"
	}
	return ""
}

// jsonText 输出缩进 JSON，供 kit.MonospaceBlock 展示 Schema。
func jsonText(v any) string {
	if v == nil {
		return "{}"
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(data)
}

// matchQuery 不区分大小写子串匹配。
func matchQuery(query string, fields ...string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return true
	}
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}

// taskTone 把任务状态映射为 kit 色调。
func taskTone(status string) kit.Tone {
	switch status {
	case "active":
		return kit.ToneAccent
	case "completed":
		return kit.ToneSuccess
	case "blocked":
		return kit.ToneDanger
	}
	return kit.ToneNeutral
}

// successTone 成功/失败的 kit 色调。
func successTone(ok bool) kit.Tone {
	if ok {
		return kit.ToneSuccess
	}
	return kit.ToneDanger
}

// boolText 把开关渲染为文本。
func boolText(on bool) string {
	if on {
		return "开"
	}
	return "关"
}
