package ui

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/uvwt/agentdock/desktop/console/internal/ui/kit"
)

// 全局搜索（⌘K）：根 view 用 ui.OnShortcut(ui.Cmd, ui.KeyK) 打开搜索浮层，
// 输入即时过滤各页已有缓存（工具/规则/技能来自能力目录，任务、文件路径、
// 发布产物、追踪工具名来自各自页面），分组展示结果；选中后跳转到对应页面
// 并定位，Esc 关闭。缓存缺失时 ensureSearchData 后台聚合拉取一次。

// searchGroupMax 是每个分组最多展示的结果条数。
const searchGroupMax = 8

// searchGroupOrder 是结果分组的展示顺序。
var searchGroupOrder = []string{"工具", "任务", "文件", "发布", "追踪", "技能", "规则"}

// searchItem 是一条搜索结果，带跳转定位信息。
type searchItem struct {
	title string
	sub   string
	page  string // 目标页面 id
	sel   int    // 目标页选中索引，-1 表示不定位
	id    string // 任务详情 id（任务）
}

// searchGroupView 是一组结果：组名、总匹配数与最多 searchGroupMax 条。
type searchGroupView struct {
	title string
	count int
	items []searchItem
}

// renderSearch 渲染 ⌘K 全局搜索浮层：Esc 或点击空白关闭。
func (a *App) renderSearch(c *ui.Context) {
	if !a.searchOpen {
		return
	}
	a.ensureSearchData()
	groups := a.searchResults(a.searchQuery)
	shown := flattenSearch(groups)
	ui.Overlay(c, func() {
		back := ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Center().Modal()
		if back.Clicked() || back.OverlayShortcut(0, ui.KeyEscape) {
			a.closeSearch()
		}
		back.Children(func() {
			ui.Column(c).Width(560).MaxHeight(440).Padding(12).Gap(8).
				Background(c.Theme().Background).Border(1, c.Theme().Border).Radius(10).
				Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.3)).
				Children(func() {
					a.searchInput(c, shown)
					a.searchListView(c, groups, shown)
				})
		})
	})
}

// searchInput 渲染搜索输入框：输入即时过滤，方向键移动选择，回车选中。
func (a *App) searchInput(c *ui.Context, shown []searchItem) {
	q := ui.SearchField(c, &a.searchQuery).Label("搜索").AutoFocus().Fill()
	if q.Changed() {
		a.searchSel = 0
	}
	if a.searchSel >= len(shown) {
		a.searchSel = 0
	}
	if len(shown) > 0 {
		if q.Shortcut(0, ui.KeyDown) {
			a.searchSel = min(a.searchSel+1, len(shown)-1)
		}
		if q.Shortcut(0, ui.KeyUp) {
			a.searchSel = max(a.searchSel-1, 0)
		}
	}
	if q.Submitted() && len(shown) > 0 {
		a.pickSearch(shown[a.searchSel])
	}
}

// searchListView 渲染结果列表：空查询提示、无结果空态、分组结果（每组
// 最多 searchGroupMax 条，组头显示总匹配数）。
func (a *App) searchListView(c *ui.Context, groups []searchGroupView, shown []searchItem) {
	if strings.TrimSpace(a.searchQuery) == "" {
		kit.EmptyState(c, "输入以搜索…", "搜索工具、任务、文件、发布、追踪、技能与规则")
		return
	}
	if len(shown) == 0 {
		kit.EmptyState(c, "没有匹配结果", "换一个关键词试试")
		return
	}
	ui.Scroll(c).Fill().Gap(2).Children(func() {
		flat := 0
		for _, g := range groups {
			ui.Text(c, fmt.Sprintf("%s（%d）", g.title, g.count)).
				FontSize(12).Bold().TextColor(c.Theme().TextMuted).Padding(8, 4, 2)
			for _, it := range g.items {
				idx := flat
				flat++
				row := ui.ButtonBase(c).
					Fill().
					Padding(6, 8).
					Gap(8).
					AlignItems(ui.Center).
					Justify(ui.Start).
					Radius(6)
				row.Children(func() {
					ui.Text(c, it.title).Grow(1).SingleLine()
					ui.Text(c, it.sub).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine()
				})
				if idx == a.searchSel {
					row.Background(c.Theme().Accent)
				}
				if row.Clicked() {
					a.pickSearch(it)
				}
			}
		}
	})
}

// closeSearch 关闭搜索浮层并复位输入。
func (a *App) closeSearch() {
	a.searchOpen = false
	a.searchQuery = ""
	a.searchSel = 0
}

// pickSearch 选中一条结果：跳转到对应页面并定位选中项（页内选中索引 /
// 详情 id），随后关闭浮层。
func (a *App) pickSearch(it searchItem) {
	a.searchOpen = false
	a.searchQuery = ""
	a.searchSel = 0
	a.page = it.page
	// 重置页内查询/筛选让选中索引与未过滤列表对齐；这些字段会被后台
	// 拉取读取，写回锁内。
	a.mu.Lock()
	switch it.page {
	case pageCatalog:
		a.catQuery = ""
	case pageTasks:
		a.taskQuery = ""
	case pageFiles:
		a.fileQuery = ""
	case pageTraces:
		a.traceQuery, a.traceTool, a.traceSource, a.traceStatus = "", "全部", "全部", "全部"
	}
	a.mu.Unlock()
	switch it.page {
	case pageCatalog:
		a.catSel = it.sel
	case pageTasks:
		a.taskSel = it.sel
	case pageFiles:
		a.fileSel = it.sel
	case pagePublished:
		a.pubSel = it.sel
	case pageTraces:
		a.traceSel = it.sel
	}
	if it.page == pageTasks && it.id != "" {
		a.loadTaskDetail(it.id)
	}
}

// flattenSearch 把分组结果摊平为键盘导航用的列表。
func flattenSearch(groups []searchGroupView) []searchItem {
	var out []searchItem
	for _, g := range groups {
		out = append(out, g.items...)
	}
	return out
}

// ensureSearchData 在缓存缺失时后台聚合拉取一次（只发起一次）。
func (a *App) ensureSearchData() {
	a.mu.Lock()
	missing := a.catData == nil || a.taskList == nil || a.fileData == nil ||
		a.pubData == nil || a.traceData == nil
	if !missing || a.searchAggOnce {
		a.mu.Unlock()
		return
	}
	a.searchAggOnce = true
	a.mu.Unlock()
	go a.fetchSearchData()
}

// fetchSearchData 聚合拉取搜索所需的各页缓存（只补缺失的），完成后
// 一次性 invalidate。
func (a *App) fetchSearchData() {
	a.mu.Lock()
	needCat, needTask := a.catData == nil, a.taskList == nil
	needFile, needPub, needTrace := a.fileData == nil, a.pubData == nil, a.traceData == nil
	a.mu.Unlock()
	store := func(set func()) {
		a.mu.Lock()
		set()
		a.mu.Unlock()
	}
	if needCat {
		if cat, err := a.backend.Catalog(); err == nil {
			store(func() {
				if a.catData == nil {
					a.catData = cat
				}
			})
		}
	}
	if needTask {
		if list, err := a.backend.Tasks("", 200); err == nil {
			store(func() {
				if a.taskList == nil {
					a.taskList = list
				}
			})
		}
	}
	if needFile {
		if changes, err := a.backend.FileChanges("", 200); err == nil {
			store(func() {
				if a.fileData == nil {
					a.fileData = changes
				}
			})
		}
	}
	if needPub {
		if arts, err := a.backend.Artifacts(); err == nil {
			store(func() {
				if a.pubData == nil {
					a.pubData = arts
				}
			})
		}
	}
	if needTrace {
		if recs, err := a.backend.Traces("", "", "", "", 200); err == nil {
			store(func() {
				if a.traceData == nil {
					a.traceData = recs
				}
			})
		}
	}
	a.notify()
}

// searchResults 按查询过滤各页缓存，按 searchGroupOrder 返回分组结果；
// 每组最多 searchGroupMax 条，count 为该组总匹配数。
func (a *App) searchResults(query string) []searchGroupView {
	a.mu.Lock()
	cat, tasks, files, arts, traces := a.catData, a.taskList, a.fileData, a.pubData, a.traceData
	a.mu.Unlock()

	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}
	byGroup := map[string][]searchItem{}

	if cat != nil {
		flat := filterCatalog(cat, "")
		flatIndex := func(kind int, title string) int {
			for i, it := range flat {
				if it.kind == kind && it.title == title {
					return i
				}
			}
			return -1
		}
		for _, t := range cat.Tools {
			if matchQuery(q, t.Name, t.Title, t.Description) {
				byGroup["工具"] = append(byGroup["工具"], searchItem{
					title: t.Name, sub: t.Title, page: pageCatalog, sel: flatIndex(0, t.Name),
				})
			}
		}
		for _, r := range cat.Rules {
			if matchQuery(q, r.Path, r.Scope, r.Content) {
				byGroup["规则"] = append(byGroup["规则"], searchItem{
					title: r.Path, sub: r.Scope, page: pageCatalog, sel: flatIndex(1, r.Path),
				})
			}
		}
		for _, s := range cat.CommonSkills.Items {
			if matchQuery(q, s.Name, s.Description, s.File) {
				byGroup["技能"] = append(byGroup["技能"], searchItem{
					title: s.Name, sub: s.File, page: pageCatalog, sel: flatIndex(2, s.Name),
				})
			}
		}
	}
	if tasks != nil {
		for i, t := range tasks.Tasks {
			if matchQuery(q, t.Title, t.Goal, t.ID) {
				byGroup["任务"] = append(byGroup["任务"], searchItem{
					title: t.Title, sub: t.Status, page: pageTasks, sel: i, id: t.ID,
				})
			}
		}
	}
	if files != nil {
		for i, ch := range files.Changes {
			if matchQuery(q, ch.Path, ch.Tool) {
				byGroup["文件"] = append(byGroup["文件"], searchItem{
					title: ch.Path, sub: ch.Action, page: pageFiles, sel: i,
				})
			}
		}
	}
	if arts != nil {
		for i, art := range arts.Artifacts {
			if matchQuery(q, art.Filename, art.MimeType) {
				byGroup["发布"] = append(byGroup["发布"], searchItem{
					title: art.Filename, sub: art.MimeType, page: pagePublished, sel: i,
				})
			}
		}
	}
	if traces != nil {
		// 工具名取 Tools 与记录里出现过的并集，定位到该工具的第一条记录。
		seen := map[string]bool{}
		var names []string
		addName := func(name string) {
			if name != "" && !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
		for _, name := range traces.Tools {
			addName(name)
		}
		for _, rec := range traces.Records {
			addName(rec.Tool)
		}
		for _, name := range names {
			if !matchQuery(q, name) {
				continue
			}
			sel, count := -1, 0
			for i, rec := range traces.Records {
				if rec.Tool == name {
					count++
					if sel < 0 {
						sel = i
					}
				}
			}
			byGroup["追踪"] = append(byGroup["追踪"], searchItem{
				title: name, sub: fmt.Sprintf("%d 次调用", count), page: pageTraces, sel: sel,
			})
		}
	}

	var out []searchGroupView
	for _, title := range searchGroupOrder {
		items := byGroup[title]
		g := searchGroupView{title: title, count: len(items)}
		if len(items) > searchGroupMax {
			items = items[:searchGroupMax]
		}
		g.items = items
		if g.count > 0 {
			out = append(out, g)
		}
	}
	return out
}
