package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/uvwt/agentdock/desktop/console/internal/runtimeapi"
	"github.com/uvwt/agentdock/desktop/console/internal/ui/kit"
)

// tracePollEvery 是追踪页自动轮询的间隔。
const tracePollEvery = 5 * time.Second

// userCacheDirFn 可注入的 os.UserCacheDir：导出文件写入其下
// agentdock-console/exports/（测试替换为临时目录）。
var userCacheDirFn = os.UserCacheDir

// pageTraces 渲染调用追踪页：SearchField + tool/source/status 筛选（ui.Select）
// + 追踪表格，右侧详情显示 arguments/result/error 与 FileChanges（可点开 diff）。
func (a *App) pageTraces(c *ui.Context) {
	a.pollTraces(c)
	a.mu.Lock()
	loading, loaded, err := a.traceLoad.loading, a.traceLoad.loaded, a.traceLoad.err
	data := a.traceData
	query, tool, source, status := a.traceQuery, a.traceTool, a.traceSource, a.traceStatus
	a.mu.Unlock()

	kit.SectionHeader(c, "调用追踪", "工具调用的完整执行记录")
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		// 筛选值会被后台拉取（fetchTraces）读取：绑定局部副本，提交时写回锁内。
		if ui.SearchField(c, &query).Label("搜索追踪").Width(220).Changed() {
			a.commitTraceFilters(query, tool, source, status)
		}
		if ui.Select(c, &tool, traceToolOptions(data)).Label("工具").Width(140).Changed() {
			a.commitTraceFilters(query, tool, source, status)
		}
		if ui.Select(c, &source, traceSourceOptions(data)).Label("来源").Width(120).Changed() {
			a.commitTraceFilters(query, tool, source, status)
		}
		if ui.Select(c, &status, []string{"全部", "成功", "失败"}).Label("状态").Width(110).Changed() {
			a.commitTraceFilters(query, tool, source, status)
		}
	})

	// 工具条第二行：导出/清空 + 状态栏（保存路径）。
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		jsonBtn := ui.Button(c, "").Children(func() {
			ui.Text(c, "导出 JSON")
		})
		if jsonBtn.Clicked() {
			a.exportTraces("json")
		}
		csvBtn := ui.Button(c, "").Children(func() {
			ui.Text(c, "导出 CSV")
		})
		if csvBtn.Clicked() {
			a.exportTraces("csv")
		}
		clearBtn := ui.Button(c, "").Children(func() {
			ui.Text(c, "清空")
		})
		if clearBtn.Clicked() {
			a.traceClearAsk = true
		}
		a.traceExportStatus(c)
	})

	// 清空二次确认：提示文件变更历史同步清空。
	switch ui.AlertDialog(c, &a.traceClearAsk, "清空追踪记录", "文件变更历史将同步清空，确定继续吗？", "取消", "确认清空") {
	case 1:
		a.clearTraces()
	}

	hasData := data != nil
	if !a.loadBanner(c, "追踪记录", loading, loaded, err, hasData,
		"暂无追踪记录", "工具调用的追踪记录会显示在这里", func() { a.reload(pageTraces) }) {
		return
	}

	recs := filterTraces(data.Records, query, tool, source, status)
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

// pollTraces 实现追踪页 5s 自动轮询：自动刷新开启时每帧用 c.After 安排
// 未来帧，渲染时以 tracePollAt（上次触发拉取的帧时钟）做时间戳去重，
// 过期则触发后台 refresh。只在追踪页可见时被调用，其他页不轮询。
func (a *App) pollTraces(c *ui.Context) {
	if !a.traceAuto {
		return
	}
	now := c.Now()
	c.After(tracePollEvery) // 每帧重新武装：tracePollEvery 后再来一帧
	a.mu.Lock()
	if a.tracePollAt.IsZero() {
		// 首帧只记时间戳：进入页面的首次拉取由 ensureLoaded 负责，
		// 避免打开页面时立刻重复拉取。
		a.tracePollAt = now
		a.mu.Unlock()
		return
	}
	stale := now.Sub(a.tracePollAt) >= tracePollEvery
	if !stale || a.traceLoad.loading {
		a.mu.Unlock()
		return
	}
	a.tracePollAt = now // 时间戳去重：每 5s 至多触发一次
	a.mu.Unlock()
	a.reload(pageTraces)
}

// commitTraceFilters 把筛选控件的绑定值写回缓存字段（后台拉取会读取它们）并重新拉取。
func (a *App) commitTraceFilters(query, tool, source, status string) {
	a.mu.Lock()
	a.traceQuery, a.traceTool, a.traceSource, a.traceStatus = query, tool, source, status
	a.mu.Unlock()
	a.reload(pageTraces)
}

// traceExportStatus 渲染导出/清空的状态栏：保存路径（可复制）与操作反馈。
func (a *App) traceExportStatus(c *ui.Context) {
	a.mu.Lock()
	path, msg, actionErr := a.traceExportPath, a.traceActionMsg, a.traceActionErr
	a.traceActionMsg = ""
	a.mu.Unlock()
	if path != "" {
		ui.Text(c, "已保存 "+path).FontSize(12).TextColor(c.Theme().TextMuted).Grow(1).SingleLine()
		copyBtn := ui.Button(c, "").Children(func() {
			ui.Icon(c, kit.IconCopy())
			ui.Text(c, "复制路径")
		})
		if copyBtn.Clicked() {
			c.WriteClipboard(path)
		}
	}
	if msg != "" {
		c.Toast(msg)
	}
	if actionErr != nil {
		ui.Text(c, "操作失败："+actionErr.Error()).FontSize(12).TextColor(c.Theme().Danger)
	}
}

// exportTraces 导出追踪记录为 json/csv 原始文件流并写入缓存目录，
// 成功后状态栏显示保存路径。
func (a *App) exportTraces(format string) {
	a.mu.Lock()
	query, tool, source, status := a.traceQuery, a.traceTool, a.traceSource, a.traceStatus
	a.mu.Unlock()
	go func() {
		data, _, filename, err := a.backend.TracesExport(format, query, selectFilter(tool), selectFilter(source), tracesStatusQuery(status), 200)
		var path string
		if err == nil {
			path, err = saveExportFile(format, filename, data)
		}
		a.mu.Lock()
		if err != nil {
			a.traceActionErr = err
		} else {
			a.traceExportPath = path
			a.traceActionMsg = "追踪已导出"
			a.traceActionErr = nil
		}
		a.mu.Unlock()
		a.notify()
	}()
}

// saveExportFile 把导出内容写入 os.UserCacheDir()/agentdock-console/exports/，
// 文件名取 Content-Disposition 的 filename（缺失时按时间生成），返回保存路径。
func saveExportFile(format, filename string, data []byte) (string, error) {
	base, err := userCacheDirFn()
	if err != nil {
		return "", fmt.Errorf("导出目录不可用：%w", err)
	}
	dir := filepath.Join(base, "agentdock-console", "exports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := filepath.Base(strings.TrimSpace(filename))
	if name == "." || name == "" {
		name = "traces-" + time.Now().Format("20060102-150405") + "." + format
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// clearTraces 后台清空追踪记录；成功后 Toast 反馈并刷新列表。
func (a *App) clearTraces() {
	go func() {
		n, err := a.backend.TracesClear()
		a.mu.Lock()
		if err != nil {
			a.traceActionErr = err
		} else {
			a.traceActionMsg = fmt.Sprintf("已清空 %d 条追踪记录", n)
			a.traceActionErr = nil
		}
		a.mu.Unlock()
		if err == nil {
			a.reload(pageTraces)
		}
		a.notify()
	}()
}

// traceAutoControls 渲染追踪页工具条上的"自动刷新"开关（ui.Switch，默认开）
// 与上次刷新时间。
func (a *App) traceAutoControls(c *ui.Context) {
	ui.Switch(c, &a.traceAuto).Label("自动刷新开关")
	ui.Text(c, "自动刷新").FontSize(12).TextColor(c.Theme().TextMuted)
	last := "—"
	a.mu.Lock()
	if !a.traceRefreshedAt.IsZero() {
		last = a.traceRefreshedAt.Format("15:04:05")
	}
	a.mu.Unlock()
	ui.Text(c, "上次刷新 "+last).FontSize(12).TextColor(c.Theme().TextMuted)
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
