package ui

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"github.com/uvwt/agentdock/desktop/console/internal/runtimeapi"
	"github.com/uvwt/agentdock/desktop/console/internal/ui/kit"
)

// catalogItem 是左侧过滤后扁平列表的一行。
type catalogItem struct {
	kind  int // 0 工具 / 1 规则 / 2 常用技能
	title string
	sub   string
	tool  *runtimeapi.CatalogTool
	rule  *runtimeapi.InstructionFile
	skill *runtimeapi.CommonSkillItem
}

// pageCatalog 渲染能力目录页：左侧 Tools/Rules/Common skills 三段分组列表
// （SearchField 过滤）+ 右侧详情（工具 Schema/annotations、规则内容、技能索引）。
func (a *App) pageCatalog(c *ui.Context) {
	a.mu.Lock()
	loading, loaded, err := a.catLoad.loading, a.catLoad.loaded, a.catLoad.err
	cat := a.catData
	query := a.catQuery
	a.mu.Unlock()

	kit.SectionHeader(c, "能力目录", "工具 / 规则 / 常用技能")
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.SearchField(c, &a.catQuery).Label("搜索能力").Width(260)
	})

	hasData := cat != nil
	if !a.loadBanner(c, "能力目录", loading, loaded, err, hasData,
		"暂无能力", "目录中没有工具、规则或常用技能", func() { a.reload(pageCatalog) }) {
		return
	}

	items := filterCatalog(cat, query)
	if a.catSel >= len(items) {
		a.catSel = -1
	}

	ui.Split(c, &a.catSplit, func() {
		ui.Column(c).Fill().Gap(2).Children(func() {
			kindTitle := map[int]string{0: "工具", 1: "规则", 2: "常用技能"}
			lastKind := -1
			for i, it := range items {
				if it.kind != lastKind {
					lastKind = it.kind
					ui.Text(c, fmt.Sprintf("%s（%d）", kindTitle[it.kind], countKind(items, it.kind))).
						FontSize(12).Bold().Padding(6, 4, 2)
				}
				i, it := i, it
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
				if i == a.catSel {
					row.Background(c.Theme().Accent)
				}
				if row.Clicked() {
					a.catSel = i
				}
			}
			if len(items) == 0 {
				kit.EmptyState(c, "没有匹配项", "换一个关键词试试")
			}
		})
	}, func() {
		ui.Scroll(c).Fill().Padding(4).Gap(8).Children(func() {
			a.catalogDetail(c, items)
		})
	}).Grow(1) // 占据内容列剩余高度（Fill 的 height:100% 会以整列高为高）
}

// catalogDetail 渲染右侧详情面板。
func (a *App) catalogDetail(c *ui.Context, items []catalogItem) {
	if a.catSel < 0 || a.catSel >= len(items) {
		kit.EmptyState(c, "选择一项查看详情", "从左侧列表选择工具、规则或常用技能")
		return
	}
	it := items[a.catSel]
	switch it.kind {
	case 0:
		tool := it.tool
		kit.SectionHeader(c, tool.Name, tool.Title)
		ui.Text(c, tool.Description).Wrap()
		ui.Text(c, "输入 Schema").Bold().FontSize(13)
		kit.MonospaceBlock(c, jsonText(tool.InputSchema))
		ui.Text(c, "输出 Schema").Bold().FontSize(13)
		kit.MonospaceBlock(c, jsonText(tool.OutputSchema))
		if ann := tool.Annotations; ann != nil {
			ui.Text(c, "注解").Bold().FontSize(13)
			ui.Row(c).Gap(6).Wrap().Children(func() {
				if ann.ReadOnlyHint {
					ui.Badge(c, "只读")
				}
				if ann.IdempotentHint {
					ui.Badge(c, "幂等")
				}
				if ann.DestructiveHint != nil && *ann.DestructiveHint {
					ui.Badge(c, "破坏性")
				}
				if ann.OpenWorldHint != nil && *ann.OpenWorldHint {
					ui.Badge(c, "开放世界")
				}
			})
		}
	case 1:
		rule := it.rule
		kit.SectionHeader(c, rule.Path, rule.Scope)
		kit.KV(c, "路径", rule.Path)
		kit.KV(c, "状态", rule.Status)
		if rule.Reason != "" {
			kit.KV(c, "原因", rule.Reason)
		}
		ui.Text(c, "内容").Bold().FontSize(13)
		kit.MonospaceBlock(c, rule.Content)
	case 2:
		skill := it.skill
		kit.SectionHeader(c, skill.Name, skill.Description)
		kit.KV(c, "文件", skill.File)
		kit.KV(c, "技能引用", skill.SkillRef)
		kit.KV(c, "来源类型", skill.SourceType)
	}
}

// filterCatalog 按搜索词过滤目录，并按 Tools/Rules/Common skills 三段展开成扁平列表。
func filterCatalog(cat *runtimeapi.Catalog, query string) []catalogItem {
	if cat == nil {
		return nil
	}
	var items []catalogItem
	for i := range cat.Tools {
		t := &cat.Tools[i]
		if matchQuery(query, t.Name, t.Title, t.Description) {
			items = append(items, catalogItem{
				kind: 0, title: t.Name, sub: "工具", tool: t,
			})
		}
	}
	for i := range cat.Rules {
		r := &cat.Rules[i]
		if matchQuery(query, r.Path, r.Scope, r.Content) {
			items = append(items, catalogItem{
				kind: 1, title: r.Path, sub: "规则", rule: r,
			})
		}
	}
	for i := range cat.CommonSkills.Items {
		s := &cat.CommonSkills.Items[i]
		if matchQuery(query, s.Name, s.Description, s.File) {
			items = append(items, catalogItem{
				kind: 2, title: s.Name, sub: "常用技能", skill: s,
			})
		}
	}
	return items
}

// countKind 统计扁平列表中某段的条数。
func countKind(items []catalogItem, kind int) int {
	n := 0
	for _, it := range items {
		if it.kind == kind {
			n++
		}
	}
	return n
}
