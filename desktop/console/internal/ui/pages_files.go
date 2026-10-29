package ui

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"github.com/uvwt/agentdock/desktop/console/internal/runtimeapi"
	"github.com/uvwt/agentdock/desktop/console/internal/ui/kit"
)

// pageFiles 渲染文件变更页：SearchField + 左侧变更列表（路径/动作/±行数）
// + 右侧 kit.DiffText 展示选中变更的 diff。
func (a *App) pageFiles(c *ui.Context) {
	a.mu.Lock()
	loading, loaded, err := a.fileLoad.loading, a.fileLoad.loaded, a.fileLoad.err
	data := a.fileData
	query := a.fileQuery
	a.mu.Unlock()

	kit.SectionHeader(c, "文件变更", "工具调用产生的文件改动")
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		// 路径查询会被后台拉取（fetchFiles）读取：绑定局部副本，提交时写回锁内。
		if ui.SearchField(c, &query).Label("搜索路径").Width(260).Changed() {
			a.mu.Lock()
			a.fileQuery = query
			a.mu.Unlock()
			a.reload(pageFiles)
		}
	})

	hasData := data != nil
	if !a.loadBanner(c, "文件变更", loading, loaded, err, hasData,
		"暂无文件变更", "最近没有工具产生的文件改动", func() { a.reload(pageFiles) }) {
		return
	}

	rows := filterFileChanges(data.Changes, query)
	if a.fileSel >= len(rows) {
		a.fileSel = -1
	}

	ui.Split(c, &a.fileSplit, func() {
		ui.Column(c).Fill().Gap(2).Children(func() {
			for i := range rows {
				ch := rows[i]
				i := i
				row := ui.ButtonBase(c).
					Fill().
					Padding(6, 8).
					Gap(8).
					AlignItems(ui.Center).
					Justify(ui.Start).
					Radius(6)
				row.Children(func() {
					ui.Text(c, kit.TruncateMiddle(ch.Path, 48)).Grow(1).SingleLine().Font("monospace").FontSize(12)
					kit.StatusChip(c, ch.Action, actionTone(ch.Action))
					ui.Text(c, fmt.Sprintf("+%d/-%d", ch.Insertions, ch.Deletions)).
						FontSize(11).TextColor(c.Theme().TextMuted).SingleLine()
				})
				if i == a.fileSel {
					row.Background(c.Theme().Accent)
				}
				if row.Clicked() {
					a.fileSel = i
				}
			}
			if len(rows) == 0 {
				kit.EmptyState(c, "没有匹配项", "换一个关键词试试")
			}
		})
	}, func() {
		ui.Scroll(c).Fill().Padding(4).Gap(8).Children(func() {
			a.fileDetail(c, rows)
		})
	}).Grow(1) // 占据内容列剩余高度（Fill 的 height:100% 会以整列高为高）
}

// fileDetail 渲染右侧 diff 详情。
func (a *App) fileDetail(c *ui.Context, rows []runtimeapi.FileChangeEntry) {
	if a.fileSel < 0 || a.fileSel >= len(rows) {
		kit.EmptyState(c, "未选择变更", "从左侧列表选择一项查看 diff")
		return
	}
	ch := rows[a.fileSel]
	kit.SectionHeader(c, "变更详情", ch.Path)
	kit.KV(c, "路径", ch.Path)
	kit.KV(c, "动作", ch.Action)
	kit.KV(c, "增删", fmt.Sprintf("+%d / -%d", ch.Insertions, ch.Deletions))
	kit.KV(c, "工具", ch.Tool)
	kit.KV(c, "时间", kit.FormatTime(ch.StartedAt))
	ui.Text(c, "Diff").Bold().FontSize(13)
	kit.DiffText(c, ch.Diff)
}

// actionTone 把变更动作映射为 kit 色调。
func actionTone(action string) kit.Tone {
	switch action {
	case "created", "create", "write":
		return kit.ToneSuccess
	case "deleted", "delete":
		return kit.ToneDanger
	case "renamed", "rename":
		return kit.ToneAccent
	}
	return kit.ToneNeutral
}

// filterFileChanges 按搜索词（路径）过滤变更列表。
func filterFileChanges(changes []runtimeapi.FileChangeEntry, query string) []runtimeapi.FileChangeEntry {
	var rows []runtimeapi.FileChangeEntry
	for _, ch := range changes {
		if matchQuery(query, ch.Path) {
			rows = append(rows, ch)
		}
	}
	return rows
}
