package ui

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"github.com/uvwt/agentdock/desktop/console/internal/runtimeapi"
	"github.com/uvwt/agentdock/desktop/console/internal/ui/kit"
)

// pageTraces 渲染调用追踪页：SearchField + tool/source/status 筛选（ui.Select）
// + 追踪表格，右侧详情显示 arguments/result/error 与 FileChanges（可点开 diff）。
func (a *App) pageTraces(c *ui.Context) {
	a.mu.Lock()
	loading, loaded, err := a.traceLoad.loading, a.traceLoad.loaded, a.traceLoad.err
	data := a.traceData
	a.mu.Unlock()

	kit.SectionHeader(c, "调用追踪", "工具调用的完整执行记录")
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		if ui.SearchField(c, &a.traceQuery).Label("搜索追踪").Width(220).Changed() {
			a.reload(pageTraces)
		}
		if ui.Select(c, &a.traceTool, traceToolOptions(data)).Label("工具").Width(140).Changed() {
			a.reload(pageTraces)
		}
		if ui.Select(c, &a.traceSource, traceSourceOptions(data)).Label("来源").Width(120).Changed() {
			a.reload(pageTraces)
		}
		if ui.Select(c, &a.traceStatus, []string{"全部", "成功", "失败"}).Label("状态").Width(110).Changed() {
			a.reload(pageTraces)
		}
	})

	hasData := data != nil
	if !a.loadBanner(c, "追踪记录", loading, loaded, err, hasData,
		"暂无追踪记录", "工具调用的追踪记录会显示在这里", func() { a.reload(pageTraces) }) {
		return
	}

	recs := filterTraces(data.Records, a.traceQuery, a.traceTool, a.traceSource, a.traceStatus)
	if a.traceSel >= len(recs) {
		a.traceSel = -1
	}

	ui.Split(c, &a.traceSplit, func() {
		cols := []ui.TableColumn{
			{Title: "时间", Width: 150},
			{Title: "工具", Width: 140},
			{Title: "来源", Width: 90},
			{Title: "UA"},
			{Title: "耗时", Width: 90, Align: ui.End},
			{Title: "成功", Width: 80},
		}
		ui.Table(c, &ui.ListState{Selected: &a.traceSel}, cols, len(recs), func(row, col int) {
			rec := recs[row]
			switch col {
			case 0:
				ui.Text(c, kit.FormatTime(rec.StartedAt)).SingleLine()
			case 1:
				ui.Text(c, rec.Tool).SingleLine()
			case 2:
				ui.Text(c, rec.Source).SingleLine()
			case 3:
				ui.Text(c, kit.TruncateMiddle(rec.UserAgent, 40)).SingleLine().FontSize(11)
			case 4:
				ui.Text(c, kit.FormatDuration(rec.DurationMS))
			case 5:
				kit.StatusChip(c, successLabel(rec.Success), successTone(rec.Success))
			}
		}).Fill()
	}, func() {
		ui.Scroll(c).Fill().Padding(4).Gap(8).Children(func() {
			a.traceDetail(c, recs)
		})
	}).Fill()
}

// traceDetail 渲染右侧追踪详情：参数/结果/错误 + 文件变更 diff。
func (a *App) traceDetail(c *ui.Context, recs []runtimeapi.TraceRecord) {
	if a.traceSel < 0 || a.traceSel >= len(recs) {
		kit.EmptyState(c, "未选择记录", "从左侧表格选择一行查看完整追踪")
		return
	}
	rec := recs[a.traceSel]
	kit.SectionHeader(c, rec.Tool, rec.TraceID)
	kit.KV(c, "时间", kit.FormatTime(rec.StartedAt))
	kit.KV(c, "耗时", kit.FormatDuration(rec.DurationMS))
	kit.KV(c, "来源", rec.Source)
	kit.KV(c, "UA", kit.TruncateMiddle(rec.UserAgent, 60))
	kit.KV(c, "结果", successLabel(rec.Success))
	if rec.ErrorCode != "" {
		kit.KV(c, "错误码", rec.ErrorCode)
	}

	ui.Text(c, "参数").Bold().FontSize(13)
	kit.MonospaceBlock(c, rec.Arguments)
	ui.Text(c, "结果").Bold().FontSize(13)
	kit.MonospaceBlock(c, rec.Result)
	ui.Text(c, "错误").Bold().FontSize(13)
	kit.MonospaceBlock(c, rec.Error)

	ui.Text(c, "文件变更").Bold().FontSize(13)
	if len(rec.FileChanges) == 0 {
		ui.Text(c, "（无）").TextColor(c.Theme().TextMuted)
		return
	}
	for i, fc := range rec.FileChanges {
		key := fmt.Sprintf("%d/%d", rec.ID, i)
		open := a.traceDiffOpen[key]
		label := fmt.Sprintf("%s  %s  +%d/-%d", fc.Path, fc.Action, fc.Insertions, fc.Deletions)
		ui.Collapsible(c, label, &open, func() {
			kit.DiffText(c, fc.Diff)
		})
		a.traceDiffOpen[key] = open
	}
}

// traceToolOptions 构造工具过滤下拉选项。
func traceToolOptions(data *runtimeapi.Traces) []string {
	opts := []string{"全部"}
	if data != nil {
		opts = append(opts, data.Tools...)
	}
	return opts
}

// traceSourceOptions 从当前记录归纳来源过滤选项。
func traceSourceOptions(data *runtimeapi.Traces) []string {
	opts := []string{"全部"}
	seen := map[string]bool{}
	if data != nil {
		for _, rec := range data.Records {
			if rec.Source != "" && !seen[rec.Source] {
				seen[rec.Source] = true
				opts = append(opts, rec.Source)
			}
		}
	}
	return opts
}

// filterTraces 在展示层再按查询与筛选收敛结果（后端已过滤，这里保证
// 输入在途时列表与当前筛选一致）。
func filterTraces(recs []runtimeapi.TraceRecord, query, tool, source, status string) []runtimeapi.TraceRecord {
	want := tracesStatusQuery(status)
	var out []runtimeapi.TraceRecord
	for _, rec := range recs {
		if selectFilter(tool) != "" && rec.Tool != selectFilter(tool) {
			continue
		}
		if selectFilter(source) != "" && rec.Source != selectFilter(source) {
			continue
		}
		if want == "success" && !rec.Success {
			continue
		}
		if want == "error" && rec.Success {
			continue
		}
		if !matchQuery(query, rec.Tool, rec.Source, rec.UserAgent, rec.Arguments, rec.Error) {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// successLabel 把成功标志渲染为文本。
func successLabel(ok bool) string {
	if ok {
		return "成功"
	}
	return "失败"
}
