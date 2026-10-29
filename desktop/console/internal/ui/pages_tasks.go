package ui

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"github.com/uvwt/agentdock/desktop/console/internal/runtimeapi"
	"github.com/uvwt/agentdock/desktop/console/internal/ui/kit"
)

// taskStatusLabels 是状态 Segmented 的选项：全部/active/blocked/completed。
var taskStatusLabels = []string{"全部", "active", "blocked", "completed"}

// pageTasks 渲染任务页：SearchField + 状态 Segmented + 任务表格，
// 选中行右侧显示详情（目标、条件、步骤、事件、阻塞、最终评审）。
func (a *App) pageTasks(c *ui.Context) {
	a.consumeTaskActionToast(c)
	a.mu.Lock()
	loading, loaded, err := a.taskLoad.loading, a.taskLoad.loaded, a.taskLoad.err
	list := a.taskList
	a.mu.Unlock()

	kit.SectionHeader(c, "任务", "任务列表与执行详情")
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.SearchField(c, &a.taskQuery).Label("搜索任务").Width(240)
		// 状态会被后台拉取（fetchTasks）读取：绑定局部副本，提交时写回锁内。
		status := a.taskStatus
		if ui.Segmented(c, &status, taskStatusLabels...).Label("状态").Changed() {
			a.mu.Lock()
			a.taskStatus = status
			a.mu.Unlock()
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
		stateBox(c, func() { kit.EmptyState(c, "暂无任务", "当前筛选条件下没有任务记录") })
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
	}).Grow(1) // 占据内容列剩余高度（Fill 的 height:100% 会以整列高为高）
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
	a.taskActionRow(c)
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
	} else {
		kit.KV(c, "评审状态", fr.Status)
		kit.KV(c, "评审摘要", fr.Summary)
		kit.KV(c, "评审时间", kit.FormatTime(fr.ReviewedAt))
		reviewList(c, "已验证事实", fr.VerifiedFacts)
		reviewList(c, "开放风险", fr.OpenRisks)
		reviewList(c, "缺失检查", fr.MissingChecks)
	}

	a.taskActivitySection(c)
}

// consumeTaskActionToast 取走任务操作的成功反馈并以 Toast 展示（每帧至多一条）。
func (a *App) consumeTaskActionToast(c *ui.Context) {
	a.mu.Lock()
	msg := a.taskActionMsg
	a.taskActionMsg = ""
	a.mu.Unlock()
	if msg != "" {
		c.Toast(msg)
	}
}

// taskActionRow 渲染详情顶部操作按钮行：Checkpoint / Complete / Delete；
// Delete 用 AlertDialog 二次确认；操作结果 Toast（成功）或 ErrorState（失败）反馈。
func (a *App) taskActionRow(c *ui.Context) {
	a.mu.Lock()
	actionErr := a.taskActionErr
	a.mu.Unlock()

	ui.Row(c).Gap(8).Children(func() {
		checkBtn := ui.Button(c, "").Children(func() {
			ui.Icon(c, kit.IconCheck())
			ui.Text(c, "Checkpoint")
		})
		if checkBtn.Clicked() {
			a.runTaskAction("checkpoint")
		}
		doneBtn := ui.Button(c, "").Children(func() {
			ui.Icon(c, kit.IconCheck())
			ui.Text(c, "Complete")
		})
		if doneBtn.Clicked() {
			a.runTaskAction("complete")
		}
		delBtn := ui.Button(c, "").Children(func() {
			ui.Text(c, "Delete")
		})
		if delBtn.Clicked() {
			a.taskDeleteAsk = true
		}
	})

	// 删除二次确认：默认按钮“确认删除”，Esc/取消 不执行。
	switch ui.AlertDialog(c, &a.taskDeleteAsk, "删除任务", "删除后不可恢复，确定删除该任务吗？", "取消", "确认删除") {
	case 1:
		a.runTaskAction("delete")
	}

	if actionErr != nil {
		kit.ErrorState(c, "操作失败："+actionErr.Error())
	}
}

// runTaskAction 执行任务操作（checkpoint/complete/delete）：后台完成后写反馈，
// 成功后刷新任务列表（delete 另丢弃详情缓存，checkpoint/complete 重拉详情与活动）。
func (a *App) runTaskAction(action string) {
	a.mu.Lock()
	id := a.taskDetailID
	a.mu.Unlock()
	if id == "" {
		return
	}
	a.spawn(func() {
		_, err := a.backend.TaskActions(id, runtimeapi.TaskActionsRequest{Action: action})
		a.mu.Lock()
		if err != nil {
			a.taskActionErr = err
		} else {
			a.taskActionMsg = taskActionDoneText(action)
			a.taskActionErr = nil
		}
		a.mu.Unlock()
		if err == nil {
			if action == "delete" {
				a.dropTaskDetail()
			} else {
				a.reloadTaskDetail(id)
			}
			a.reload(pageTasks)
		}
		a.notify()
	})
}

// taskActionDoneText 返回任务操作成功的 Toast 文案。
func taskActionDoneText(action string) string {
	switch action {
	case "checkpoint":
		return "检查点已保存"
	case "complete":
		return "任务已完成"
	case "delete":
		return "任务已删除"
	}
	return "操作已完成"
}

// taskActivitySection 渲染详情底部“本任务的调用与文件”：调用表（时间/工具/来源/UA 截断/耗时/成功）
// 与文件变更列表（路径/±行数，点击展开 diff）；trace 未开启（mode=off）时给 EmptyState。
func (a *App) taskActivitySection(c *ui.Context) {
	a.mu.Lock()
	load := a.taskActLoad
	act := a.taskActivity
	actID := a.taskActID
	a.mu.Unlock()

	ui.Text(c, "本任务的调用与文件").Bold().FontSize(13)
	switch {
	case act == nil && load.loading:
		kit.LoadingState(c, "正在加载本任务的调用与文件…")
		return
	case act == nil && load.err != nil:
		kit.ErrorState(c, "任务活动加载失败："+load.err.Error())
		ui.Row(c).Children(func() {
			btn := ui.Button(c, "").Children(func() {
				ui.Icon(c, kit.IconRefresh())
				ui.Text(c, "重试")
			})
			if btn.Clicked() {
				a.loadTaskActivity(actID)
			}
		})
		return
	case act == nil:
		kit.EmptyState(c, "暂无活动数据", "本任务暂无关联的工具调用与文件变更")
		return
	}
	if act.Mode == "off" {
		kit.EmptyState(c, "trace 未开启", "开启 trace 后此处显示本任务的调用与文件变更")
		return
	}

	// 调用表。
	ui.Row(c).Gap(8).Children(func() {
		ui.Text(c, "时间").Bold().Width(118)
		ui.Text(c, "工具").Bold().Width(88)
		ui.Text(c, "来源").Bold().Width(56)
		ui.Text(c, "UA").Bold().Grow(1)
		ui.Text(c, "耗时").Bold().Width(58)
		ui.Text(c, "成功").Bold().Width(44)
	})
	if len(act.Calls) == 0 {
		ui.Text(c, "（无调用）").TextColor(c.Theme().TextMuted)
	}
	for _, call := range act.Calls {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, kit.FormatTime(call.StartedAt)).Width(118).SingleLine().FontSize(11)
			ui.Text(c, call.Tool).Width(88).SingleLine().FontSize(11)
			ui.Text(c, call.Source).Width(56).SingleLine().FontSize(11)
			ui.Text(c, kit.TruncateMiddle(call.UserAgent, 40)).Grow(1).SingleLine().FontSize(11)
			ui.Text(c, kit.FormatDuration(call.DurationMS)).Width(58).FontSize(11)
			kit.StatusChip(c, successLabel(call.Success), successTone(call.Success))
		})
	}

	// 文件变更列表。
	ui.Text(c, "文件变更").Bold().FontSize(12)
	if len(act.FileChanges) == 0 {
		ui.Text(c, "（无）").TextColor(c.Theme().TextMuted)
		return
	}
	for i, fc := range act.FileChanges {
		c.Key(fmt.Sprintf("task-act-diff-%d", i))
		key := fmt.Sprintf("%d/%s/%s", i, fc.TraceID, fc.Path)
		open := a.actDiffOpen[key]
		label := fmt.Sprintf("%s  %s  +%d/-%d", fc.Path, fc.Action, fc.Insertions, fc.Deletions)
		ui.Collapsible(c, label, &open, func() {
			kit.DiffText(c, fc.Diff)
		})
		a.actDiffOpen[key] = open
	}
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
