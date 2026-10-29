package ui

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"github.com/uvwt/agentdock/desktop/console/internal/ui/kit"
)

// pageOverview 渲染概览页：Status + Overview + Analytics 的汇总视图。
// 结构：调用指标 MetricCard 行 → 任务四态卡 → 能力计数 → 服务信息 KV。
func (a *App) pageOverview(c *ui.Context) {
	a.mu.Lock()
	loading, loaded, err := a.ovLoad.loading, a.ovLoad.loaded, a.ovLoad.err
	st, ov, an := a.status, a.overview, a.ovAnalytics
	a.mu.Unlock()

	kit.SectionHeader(c, "概览", "Runtime 状态、任务与调用指标")

	hasData := st != nil || ov != nil || an != nil
	if !a.loadBanner(c, "概览", loading, loaded, err, hasData,
		"暂无概览数据", "Runtime 暂未返回状态与指标数据", func() { a.reload(pageOverview) }) {
		return
	}

	ui.Scroll(c).Grow(1).Gap(14).Children(func() {
		if an != nil {
			ui.Text(c, "调用指标").Bold().FontSize(13)
			ui.Row(c).Gap(12).Children(func() {
				kit.MetricCard(c, "总调用", fmt.Sprintf("%d", an.TotalCalls), "累计工具调用次数").Grow(1)
				kit.MetricCard(c, "错误", fmt.Sprintf("%d", an.TotalErrors), "累计失败次数").Grow(1)
				kit.MetricCard(c, "活跃", fmt.Sprintf("%d", an.ActiveCalls), "进行中的调用").Grow(1)
				kit.MetricCard(c, "窗口", fmt.Sprintf("%d", an.WindowCalls), "近期窗口内调用").Grow(1)
			})
		}

		if ov != nil {
			ui.Text(c, "任务状态").Bold().FontSize(13)
			t := ov.Tasks
			ui.Row(c).Gap(12).Children(func() {
				kit.MetricCard(c, "进行中", fmt.Sprintf("%d", t.Active), "active").Grow(1)
				kit.MetricCard(c, "已完成", fmt.Sprintf("%d", t.Completed), "completed").Grow(1)
				kit.MetricCard(c, "阻塞", fmt.Sprintf("%d", t.Blocked), "blocked").Grow(1)
				kit.MetricCard(c, "24 小时活跃", fmt.Sprintf("%d", t.ActiveRecent24h), "近 24 小时活动任务").Grow(1)
			})

			ui.Text(c, "能力计数").Bold().FontSize(13)
			ui.Row(c).Gap(12).Children(func() {
				tools := 0
				if st != nil {
					tools = st.ToolCount
				}
				kit.MetricCard(c, "工具", fmt.Sprintf("%d", tools), "已注册工具").Grow(1)
				kit.MetricCard(c, "技能", fmt.Sprintf("%d", ov.Skills.Count), "技能索引").Grow(1)
				kit.MetricCard(c, "插件", fmt.Sprintf("%d", ov.Plugins.Count), "插件包").Grow(1)
				kit.MetricCard(c, "MCP", fmt.Sprintf("%d", ov.MCP.Count), "MCP 服务").Grow(1)
			})
		}

		if st != nil {
			ui.Text(c, "服务信息").Bold().FontSize(13)
			kit.KV(c, "版本", st.Version)
			kit.KV(c, "首页", st.AgentDockHome)
			kit.KV(c, "路径模型", st.PathModel)
			kit.KV(c, "开关", fmt.Sprintf("auth=%s browser=%s memory=%s nexus=%s",
				boolText(st.AuthEnabled), boolText(st.BrowserEnabled),
				boolText(st.MemoryEnabled), boolText(st.NexusEnabled)))
		}

		a.recentEvents(c)
	})
}

// recentEvents 渲染概览页"实时事件"小节：最近 rtEventLimit 条
// call/task/artifact 事件时间线（时间 + kind + 摘要），无事件时提示等待。
func (a *App) recentEvents(c *ui.Context) {
	a.mu.Lock()
	events := append([]rtEvent(nil), a.events...)
	a.mu.Unlock()
	ui.Text(c, "实时事件").Bold().FontSize(13)
	if len(events) == 0 {
		ui.Text(c, "等待实时事件…").FontSize(12).TextColor(c.Theme().TextMuted)
		return
	}
	for _, ev := range events {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, kit.FormatTime(ev.At)).Width(150).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine()
			kit.StatusChip(c, ev.Kind, eventTone(ev.Kind))
			ui.Text(c, ev.Summary).Grow(1).FontSize(12).Wrap()
		})
	}
}
