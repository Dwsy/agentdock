package ui

import (
	"github.com/egoist/mygo/ui"

	"github.com/uvwt/agentdock/desktop/console/internal/runtimeapi"
	"github.com/uvwt/agentdock/desktop/console/internal/ui/kit"
)

// pagePublished 渲染发布产物页：未配置公网时给 EmptyState 说明；
// 否则 Table（文件名/类型/大小/创建/过期）+ 选中详情含 URL（可复制/可打开）。
func (a *App) pagePublished(c *ui.Context) {
	a.mu.Lock()
	loading, loaded, err := a.pubLoad.loading, a.pubLoad.loaded, a.pubLoad.err
	data := a.pubData
	a.mu.Unlock()

	kit.SectionHeader(c, "发布产物", "公网可分享的文件产物")

	hasData := data != nil
	if !a.loadBanner(c, "发布产物", loading, loaded, err, hasData,
		"暂无发布产物", "尚未发布任何文件", func() { a.reload(pagePublished) }) {
		return
	}

	if !data.Configured {
		kit.EmptyState(c, "未配置公网发布", "配置公网基址后，此处展示可分享的产物链接")
		return
	}

	arts := data.Artifacts
	if a.pubSel >= len(arts) {
		a.pubSel = -1
	}

	ui.Split(c, &a.pubSplit, func() {
		cols := []ui.TableColumn{
			{Title: "文件名"},
			{Title: "类型", Width: 150},
			{Title: "大小", Width: 90, Align: ui.End},
			{Title: "创建", Width: 150},
			{Title: "过期", Width: 150},
		}
		ui.Table(c, &ui.ListState{Selected: &a.pubSel}, cols, len(arts), func(row, col int) {
			art := arts[row]
			switch col {
			case 0:
				ui.Text(c, art.Filename).SingleLine()
			case 1:
				ui.Text(c, art.MimeType).SingleLine()
			case 2:
				ui.Text(c, kit.FormatBytes(art.SizeBytes))
			case 3:
				ui.Text(c, kit.FormatTime(art.CreatedAt)).SingleLine()
			case 4:
				ui.Text(c, kit.FormatTime(art.ExpiresAt)).SingleLine()
			}
		}).Fill()
	}, func() {
		ui.Scroll(c).Fill().Padding(4).Gap(8).Children(func() {
			a.artifactDetail(c, arts)
		})
	}).Fill()
}

// artifactDetail 渲染右侧产物详情：URL 可复制（mygo clipboard）与打开。
func (a *App) artifactDetail(c *ui.Context, arts []runtimeapi.Artifact) {
	if a.pubSel < 0 || a.pubSel >= len(arts) {
		kit.EmptyState(c, "未选择产物", "从左侧表格选择一行查看链接")
		return
	}
	art := arts[a.pubSel]
	kit.SectionHeader(c, art.Filename, art.MimeType)
	kit.KV(c, "大小", kit.FormatBytes(art.SizeBytes))
	kit.KV(c, "创建", kit.FormatTime(art.CreatedAt))
	kit.KV(c, "过期", kit.FormatTime(art.ExpiresAt))
	ui.Text(c, "链接").Bold().FontSize(13)
	kit.MonospaceBlock(c, art.URL)
	ui.Row(c).Gap(8).Children(func() {
		copyBtn := ui.Button(c, "").Children(func() {
			ui.Icon(c, kit.IconCopy())
			ui.Text(c, "复制链接")
		})
		if copyBtn.Clicked() {
			c.WriteClipboard(art.URL)
		}
		openBtn := ui.Button(c, "").Children(func() {
			ui.Icon(c, kit.IconExternalLink())
			ui.Text(c, "打开链接")
		})
		if openBtn.Clicked() {
			c.OpenURL(art.URL)
		}
	})
}
