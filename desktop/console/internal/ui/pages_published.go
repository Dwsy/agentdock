package ui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/uvwt/agentdock/desktop/console/internal/runtimeapi"
	"github.com/uvwt/agentdock/desktop/console/internal/ui/kit"
)

// minExtendHours/maxExtendHours 是产物延期的允许范围（小时）。
const (
	minExtendHours = 1
	maxExtendHours = 720
)

// pagePublished 渲染发布产物页：未配置公网时给 EmptyState 说明；
// 否则 Table（文件名/类型/大小/创建/过期）+ 选中详情含 URL（可复制/可打开）。
func (a *App) pagePublished(c *ui.Context) {
	a.consumePubActionToast(c)
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
	a.artifactActionRow(c, art)
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

// consumePubActionToast 取走产物操作的成功反馈并以 Toast 展示。
func (a *App) consumePubActionToast(c *ui.Context) {
	a.mu.Lock()
	msg := a.pubActionMsg
	a.pubActionMsg = ""
	a.mu.Unlock()
	if msg != "" {
		c.Toast(msg)
	}
}

// artifactActionRow 渲染产物详情顶部操作行：撤销（AlertDialog 二次确认）与
// 延期（TextInput 输入小时数 + 延期按钮，1..720 校验）；结果 Toast/ErrorState 反馈。
func (a *App) artifactActionRow(c *ui.Context, art runtimeapi.Artifact) {
	ui.Text(c, "操作").Bold().FontSize(13)
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		revokeBtn := ui.Button(c, "").Children(func() {
			ui.Text(c, "撤销")
		})
		if revokeBtn.Clicked() {
			a.pubRevokeAsk = true
		}
		ui.Text(c, "延期小时").FontSize(12).TextColor(c.Theme().TextMuted)
		ui.TextInput(c, &a.pubExtendInput).Width(90).Label("延期小时")
		extendBtn := ui.Button(c, "").Children(func() {
			ui.Text(c, "延期")
		})
		if extendBtn.Clicked() {
			hours, err := parseExtendHours(a.pubExtendInput)
			if err != nil {
				a.setPubActionErr(err)
			} else {
				a.extendArtifact(art.ArtifactID, hours)
			}
		}
	})

	// 撤销二次确认：默认按钮“确认撤销”，Esc/取消 不执行。
	switch ui.AlertDialog(c, &a.pubRevokeAsk, "撤销产物", "撤销后链接立即失效，确定撤销吗？", "取消", "确认撤销") {
	case 1:
		a.revokeArtifact(art.ArtifactID)
	}

	a.mu.Lock()
	actionErr := a.pubActionErr
	a.mu.Unlock()
	if actionErr != nil {
		kit.ErrorState(c, "操作失败："+actionErr.Error())
	}
}

// setPubActionErr 同步写入操作错误并请求重绘（如延期小时数校验失败）。
func (a *App) setPubActionErr(err error) {
	a.mu.Lock()
	a.pubActionErr = err
	a.mu.Unlock()
	a.notify()
}

// parseExtendHours 解析并校验延期小时数（整数，1..720）。
func parseExtendHours(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, errors.New("延期小时数需为整数")
	}
	if n < minExtendHours || n > maxExtendHours {
		return 0, fmt.Errorf("延期小时数需在 %d 到 %d 之间", minExtendHours, maxExtendHours)
	}
	return n, nil
}

// revokeArtifact 后台撤销产物；成功后 Toast 反馈并刷新列表。
func (a *App) revokeArtifact(id string) {
	a.spawn(func() {
		err := a.backend.ArtifactRevoke(id)
		a.mu.Lock()
		if err != nil {
			a.pubActionErr = err
		} else {
			a.pubActionMsg = "产物已撤销"
			a.pubActionErr = nil
		}
		a.mu.Unlock()
		if err == nil {
			a.reload(pagePublished)
		}
		a.notify()
	})
}

// extendArtifact 后台延期产物；成功后 Toast 反馈并刷新列表（过期时间更新）。
func (a *App) extendArtifact(id string, hours int) {
	a.spawn(func() {
		_, err := a.backend.ArtifactExtend(id, hours)
		a.mu.Lock()
		if err != nil {
			a.pubActionErr = err
		} else {
			a.pubActionMsg = fmt.Sprintf("产物已延期 %d 小时", hours)
			a.pubActionErr = nil
		}
		a.mu.Unlock()
		if err == nil {
			a.reload(pagePublished)
		}
		a.notify()
	})
}
