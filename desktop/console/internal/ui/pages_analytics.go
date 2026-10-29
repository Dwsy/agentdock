package ui

import (
	"fmt"
	"sort"

	"github.com/egoist/mygo/ui"

	"github.com/uvwt/agentdock/desktop/console/internal/runtimeapi"
	"github.com/uvwt/agentdock/desktop/console/internal/ui/kit"
)

// analyticsSortLabels 是 ToolStats 排序 Segmented 的选项。
var analyticsSortLabels = []string{"调用次数", "错误率", "耗时 P95"}

// pageAnalytics 渲染统计页：MetricCard 行 + ToolStats 表（可切换排序）
// + RecentCalls 表。
func (a *App) pageAnalytics(c *ui.Context) {
	a.mu.Lock()
	loading, loaded, err := a.anLoad.loading, a.anLoad.loaded, a.anLoad.err
	an := a.anData
	a.mu.Unlock()

	kit.SectionHeader(c, "统计分析", "工具调用的聚合指标与最近调用")

	hasData := an != nil
	if !a.loadBanner(c, "统计", loading, loaded, err, hasData,
		"暂无统计数据", "发生工具调用后此处展示统计", func() { a.reload(pageAnalytics) }) {
		return
	}

	ui.Scroll(c).Grow(1).Gap(14).Children(func() {
		ui.Row(c).Gap(12).Children(func() {
			kit.MetricCard(c, "总调用", fmt.Sprintf("%d", an.TotalCalls), "累计工具调用次数").Grow(1)
			kit.MetricCard(c, "错误", fmt.Sprintf("%d", an.TotalErrors), "累计失败次数").Grow(1)
			kit.MetricCard(c, "活跃", fmt.Sprintf("%d", an.ActiveCalls), "进行中的调用").Grow(1)
			kit.MetricCard(c, "窗口", fmt.Sprintf("%d", an.WindowCalls), "近期窗口内调用").Grow(1)
		})
		ui.Row(c).Gap(12).Children(func() {
			kit.MetricCard(c, "进程 goroutine", fmt.Sprintf("%d", an.Process.Goroutines), "当前进程").Grow(1)
			kit.MetricCard(c, "堆内存", kit.FormatBytes(int64(an.Process.HeapAllocBytes)), "HeapAlloc").Grow(1)
			kit.MetricCard(c, "最近调用容量", fmt.Sprintf("%d", an.RecentCapacity), "环形缓冲").Grow(1)
		})

		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "工具统计").Bold().FontSize(13).Grow(1)
			ui.Segmented(c, &a.anSort, analyticsSortLabels...).Label("排序")
		})

		stats := sortedToolStats(an.ToolStats, a.anSort)
		if len(stats) == 0 {
			kit.EmptyState(c, "暂无工具统计", "工具调用后此处展示分工具指标")
		} else {
			cols := []ui.TableColumn{
				{Title: "工具"},
				{Title: "调用", Width: 80, Align: ui.End},
				{Title: "错误", Width: 80, Align: ui.End},
				{Title: "错误率", Width: 90, Align: ui.End},
				{Title: "P50", Width: 90, Align: ui.End},
				{Title: "P95", Width: 90, Align: ui.End},
				{Title: "P99", Width: 90, Align: ui.End},
			}
			ui.Table(c, &ui.ListState{}, cols, len(stats), func(row, col int) {
				s := stats[row]
				switch col {
				case 0:
					ui.Text(c, s.Tool).SingleLine()
				case 1:
					ui.Textf(c, "%d", s.Count)
				case 2:
					ui.Textf(c, "%d", s.ErrorCount)
				case 3:
					ui.Textf(c, "%.1f%%", s.ErrorRate*100)
				case 4:
					ui.Text(c, kit.FormatDuration(s.P50DurationMS))
				case 5:
					ui.Text(c, kit.FormatDuration(s.P95DurationMS))
				case 6:
					ui.Text(c, kit.FormatDuration(s.P99DurationMS))
				}
			}).Height(260)
		}

		ui.Text(c, "最近调用").Bold().FontSize(13)
		recs := an.RecentCalls
		if len(recs) == 0 {
			kit.EmptyState(c, "暂无最近调用", "工具调用后此处展示最近的执行记录")
			return
		}
		cols := []ui.TableColumn{
			{Title: "时间", Width: 150},
			{Title: "工具"},
			{Title: "来源", Width: 100},
			{Title: "耗时", Width: 90, Align: ui.End},
			{Title: "结果", Width: 80},
			{Title: "错误码", Width: 130},
		}
		ui.Table(c, &ui.ListState{}, cols, len(recs), func(row, col int) {
			rec := recs[row]
			switch col {
			case 0:
				ui.Text(c, kit.FormatTime(rec.StartedAt)).SingleLine()
			case 1:
				ui.Text(c, rec.Tool).SingleLine()
			case 2:
				ui.Text(c, rec.Source).SingleLine()
			case 3:
				ui.Text(c, kit.FormatDuration(rec.DurationMS))
			case 4:
				kit.StatusChip(c, successLabel(rec.Success), successTone(rec.Success))
			case 5:
				ui.Text(c, rec.ErrorCode).SingleLine()
			}
		}).Height(300)
	})
}

// sortedToolStats 按选定指标降序返回工具统计的副本。
func sortedToolStats(stats []runtimeapi.ToolStats, by int) []runtimeapi.ToolStats {
	out := append([]runtimeapi.ToolStats(nil), stats...)
	sort.SliceStable(out, func(i, j int) bool {
		switch by {
		case 1: // 错误率
			if out[i].ErrorRate != out[j].ErrorRate {
				return out[i].ErrorRate > out[j].ErrorRate
			}
		case 2: // 耗时 P95
			if out[i].P95DurationMS != out[j].P95DurationMS {
				return out[i].P95DurationMS > out[j].P95DurationMS
			}
		}
		return out[i].Count > out[j].Count
	})
	return out
}
