package ui

import (
	"github.com/egoist/mygo/ui"

	"github.com/uvwt/agentdock/desktop/console/internal/runtimeapi"
	"github.com/uvwt/agentdock/desktop/console/internal/ui/kit"
)

// taskStatusLabels 是状态 Segmented 的选项：全部/active/blocked/completed。
var taskStatusLabels = []string{"全部", "active", "blocked", "completed"}

// pageTasks 渲染任务页：SearchField + 状态 Segmented + 任务表格，
// 选中行右侧显示详情（目标、条件、步骤、事件、阻塞、最终评审）。
func (a *App) pageTasks(c *ui.Context) {
	a.mu.Lock()
	loading, loaded, err := a.taskLoad.loading, a.taskLoad.loaded, a.taskLoad.err
	list := a.taskList
	a.mu.Unlock()

	kit.SectionHeader(c, "任务", "任务列表与执行详情")
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.SearchField(c, &a.taskQuery).Label("搜索任务").Width(240)
		if ui.Segmented(c, &a.taskStatus, taskStatusLabels...).Label("状态").Changed() {
			a.reload(pageTasks)
		}
	})

	hasData := list != nil
	if !a.loadBanner(c, "任务", loading, loaded, err, hasData,
		"暂无任务", "当前筛选条件下没有任务记录", func() { a.reload(pageTasks) }) {
		return
	}

	rows := filterTasks(list.Tasks, a.taskQuery)
	if len(rows) == 0 {
		kit.EmptyState(c, "暂无任务", "当前筛选条件下没有任务记录")
		return
	}
	if a.taskSel >= len(rows) {
		a.taskSel = -1
	}

	ui.Split(c, &a.taskSplit, func() {
		cols := []ui.TableColumn{
			{Title: "标题"},
			{Title: "状态", Width: 82},
			{Title: "阶段", Width: 96},
			{Title: "步骤", Width: 70},
			{Title: "项目", Width: 110},
			{Title: "设备", Width: 90},
			{Title: "更新时间", Width: 140},
		}
		tbl := ui.Table(c, &ui.ListState{Selected: &a.taskSel}, cols, len(rows), func(row, col int) {
			t := rows[row]
			switch col {
			case 0:
				ui.Text(c, t.Title).SingleLine()
			case 1:
				kit.StatusChip(c, t.Status, taskTone(t.Status))
			case 2:
				ui.Text(c, t.Phase).SingleLine()
			case 3:
				ui.Textf(c, "%d/%d", t.CompletedStepCount, t.StepCount)
			case 4:
				ui.Text(c, t.Project).SingleLine()
			case 5:
				ui.Text(c, t.Device).SingleLine()
			case 6:
				ui.Text(c, kit.FormatTime(t.UpdatedAt)).SingleLine()
			}
		}).Fill()
		if tbl.Changed() && a.taskSel >= 0 && a.taskSel < len(rows) {
			a.loadTaskDetail(rows[a.taskSel].ID)
		}
	}, func() {
		ui.Scroll(c).Fill().Padding(4).Gap(8).Children(func() {
			a.taskDetailPanel(c, rows)
		})
	}).Fill()
}

// taskDetailPanel 渲染右侧任务详情。
func (a *App) taskDetailPanel(c *ui.Context, rows []runtimeapi.TaskListItem) {
	a.mu.Lock()
	detailLoad := a.taskDetailLoad
	detail := a.taskDetail
	a.mu.Unlock()

	if a.taskSel < 0 || a.taskSel >= len(rows) {
		kit.EmptyState(c, "未选择任务", "在左侧表格中选择一行查看详情")
		return
	}

	hasData := detail != nil && detail.Task != nil
	retryID := rows[a.taskSel].ID
	if !a.loadBanner(c, "任务详情", detailLoad.loading, detailLoad.loaded, detailLoad.err, hasData,
		"暂无详情", "该任务没有可展示的详情", func() { a.loadTaskDetail(retryID) }) {
		return
	}

	t := detail.Task
	kit.SectionHeader(c, "任务详情", t.Title)
	kit.KV(c, "ID", t.ID)
	kit.KV(c, "目标", t.Goal)
	kit.KV(c, "状态", t.Status)
	kit.KV(c, "阶段", t.Phase)
	kit.KV(c, "项目", t.Project)
	kit.KV(c, "设备", t.Device)
	kit.KV(c, "更新时间", kit.FormatTime(t.UpdatedAt))

	ui.Text(c, "条件").Bold().FontSize(13)
	if len(t.Conditions) == 0 {
		ui.Text(c, "（无）").TextColor(c.Theme().TextMuted)
	}
	for _, cond := range t.Conditions {
		ui.Text(c, "• "+cond.Text).Wrap()
	}

	ui.Text(c, "步骤").Bold().FontSize(13)
	if len(t.Steps) == 0 {
		ui.Text(c, "（无）").TextColor(c.Theme().TextMuted)
	}
	for i, s := range t.Steps {
		phase := ""
		if s.Phase != "" {
			phase = "（" + s.Phase + "）"
		}
		ui.Textf(c, "%d. %s%s", i+1, s.Title, phase).Wrap()
	}

	ui.Text(c, "事件").Bold().FontSize(13)
	if len(t.Events) == 0 {
		ui.Text(c, "（无）").TextColor(c.Theme().TextMuted)
	}
	for _, ev := range t.Events {
		ui.Textf(c, "%s  %s  %s", kit.FormatTime(ev.CreatedAt), ev.Type, ev.Summary).Wrap()
	}

	ui.Text(c, "阻塞").Bold().FontSize(13)
	if t.Blocker == "" {
		ui.Text(c, "无").TextColor(c.Theme().TextMuted)
	} else {
		ui.Text(c, t.Blocker).Wrap()
	}

	ui.Text(c, "最终评审").Bold().FontSize(13)
	fr := t.FinalReview
	if fr == nil {
		ui.Text(c, "（暂无）").TextColor(c.Theme().TextMuted)
		return
	}
	kit.KV(c, "评审状态", fr.Status)
	kit.KV(c, "评审摘要", fr.Summary)
	kit.KV(c, "评审时间", kit.FormatTime(fr.ReviewedAt))
	reviewList(c, "已验证事实", fr.VerifiedFacts)
	reviewList(c, "开放风险", fr.OpenRisks)
	reviewList(c, "缺失检查", fr.MissingChecks)
}

// reviewList 渲染最终评审中的字符串列表小节。
func reviewList(c *ui.Context, title string, items []string) {
	ui.Text(c, title).Bold().FontSize(12)
	if len(items) == 0 {
		ui.Text(c, "（无）").TextColor(c.Theme().TextMuted)
		return
	}
	for _, s := range items {
		ui.Text(c, "• "+s).Wrap()
	}
}

// filterTasks 按搜索词（标题/目标）过滤任务列表。
func filterTasks(tasks []runtimeapi.TaskListItem, query string) []runtimeapi.TaskListItem {
	var rows []runtimeapi.TaskListItem
	for _, t := range tasks {
		if matchQuery(query, t.Title, t.Goal) {
			rows = append(rows, t)
		}
	}
	return rows
}
