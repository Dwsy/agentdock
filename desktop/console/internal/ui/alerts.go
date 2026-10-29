package ui

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/uvwt/agentdock/desktop/console/internal/runtimeapi"
	"github.com/uvwt/agentdock/desktop/console/internal/ui/kit"
)

// 顶部告警横幅：每次进入页面/收到实时事件时节流拉取 Alerts()（alertsFetchEvery
// 一次），有活跃告警时页面顶部显示横幅——severity 着色（warning/danger）、
// message、detail、first_seen，点击横幅跳对应页（error_rate→统计、trace_disk→
// 追踪、task_blocked→任务）；无活跃告警不渲染。resolved_count>0 时横幅右侧
// 显示"已恢复 N 条"小字。

// alertsFetchEvery 是告警拉取的节流窗口（至少 30s 一次）。
const alertsFetchEvery = 30 * time.Second

// maybeFetchAlerts 节流拉取告警：窗口内重复触发（进入页面/收到事件）合并为
// 一次；结果的可见状态（活跃告警 + 已恢复计数）变化时才请求重绘，空告警期
// 不产生无效重绘。
func (a *App) maybeFetchAlerts() {
	a.mu.Lock()
	now := time.Now()
	if a.alertsFetching || (!a.alertsFetchedAt.IsZero() && now.Sub(a.alertsFetchedAt) < alertsFetchEvery) {
		a.mu.Unlock()
		return
	}
	a.alertsFetching = true
	a.mu.Unlock()
	a.spawn(func() {
		resp, err := a.backend.Alerts()
		a.mu.Lock()
		a.alertsFetching = false
		a.alertsFetchedAt = time.Now()
		changed := false
		if err == nil && resp != nil {
			changed = alertSig(resp) != alertSig(a.alerts)
			a.alerts = resp
		}
		a.mu.Unlock()
		if changed {
			a.notify()
		}
	})
}

// alertSig 是告警可见状态签名（活跃告警 + 已恢复计数），用于变化检测；
// 无活跃告警且无已恢复计数时与“尚未拉取”同签名（空告警期不产生重绘）。
func alertSig(resp *runtimeapi.AlertsResponse) string {
	if resp == nil {
		return ""
	}
	active := make([]runtimeapi.Alert, 0, len(resp.Alerts))
	for _, al := range resp.Alerts {
		if al.Active {
			active = append(active, al)
		}
	}
	if len(active) == 0 && resp.ResolvedCount == 0 {
		return ""
	}
	data, _ := json.Marshal(struct {
		Alerts        []runtimeapi.Alert `json:"alerts"`
		ResolvedCount int                `json:"resolved_count"`
	}{Alerts: active, ResolvedCount: resp.ResolvedCount})
	return string(data)
}

// renderAlertBanner 渲染页面顶部的活跃告警横幅；无活跃告警时不渲染任何内容。
func (a *App) renderAlertBanner(c *ui.Context) {
	a.mu.Lock()
	var active []runtimeapi.Alert
	resolved := 0
	if a.alerts != nil {
		resolved = a.alerts.ResolvedCount
		for _, al := range a.alerts.Alerts {
			if al.Active {
				active = append(active, al)
			}
		}
	}
	a.mu.Unlock()
	if len(active) == 0 {
		return
	}
	for _, al := range active {
		tone, sevLabel := alertTone(al.Severity)
		fg := alertBannerColor(c.Theme(), al.Severity)
		row := ui.ButtonBase(c).
			Fill().
			Padding(10, 12).
			Gap(10).
			AlignItems(ui.Center).
			Justify(ui.Start).
			Radius(8).
			Background(alertTint(fg, 0.12))
		row.Children(func() {
			kit.StatusChip(c, sevLabel, tone)
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, al.Message).Bold().FontSize(13)
				if al.Detail != "" {
					ui.Text(c, al.Detail).FontSize(11).TextColor(c.Theme().TextMuted).Wrap()
				}
				ui.Text(c, "首次出现 "+kit.FormatTime(al.FirstSeen)).FontSize(10).TextColor(c.Theme().TextMuted)
			})
			if resolved > 0 {
				ui.Text(c, fmt.Sprintf("已恢复 %d 条", resolved)).FontSize(10).TextColor(c.Theme().TextMuted)
			}
		})
		if row.Clicked() {
			if page := alertTargetPage(al.Kind); page != "" {
				a.page = page
			}
		}
	}
}

// alertTone 把告警 severity 映射为 kit 色调与横幅文案。
func alertTone(severity string) (kit.Tone, string) {
	switch severity {
	case runtimeapi.AlertSeverityDanger:
		return kit.ToneDanger, "严重"
	case runtimeapi.AlertSeverityWarning:
		return kit.ToneWarning, "警告"
	default:
		return kit.ToneWarning, severity
	}
}

// alertTargetPage 把告警 kind 映射为点击横幅跳转的目标页面：
// error_rate→统计、trace_disk→追踪、task_blocked→任务；其余不跳转。
func alertTargetPage(kind string) string {
	switch kind {
	case runtimeapi.AlertKindErrorRate:
		return pageAnalytics
	case runtimeapi.AlertKindTraceDisk:
		return pageTraces
	case runtimeapi.AlertKindTaskBlocked:
		return pageTasks
	}
	return ""
}

// alertBannerColor 返回告警 severity 的主题色（warning/danger 着色，缺失时
// 回退内置调色板）。
func alertBannerColor(t *ui.Theme, severity string) ui.Color {
	danger := severity == runtimeapi.AlertSeverityDanger
	var c ui.Color
	if danger {
		c = t.Danger
	} else {
		c = t.Warning
	}
	if c.A == 0 {
		if danger {
			return ui.RGBA(0xDC, 0x26, 0x26, 1)
		}
		return ui.RGBA(0xD9, 0x77, 0x06, 1)
	}
	return c
}

// alertTint 把告警色降为 alpha 透明度的背景色。
func alertTint(c ui.Color, alpha float32) ui.Color {
	return ui.RGBA(c.R, c.G, c.B, alpha)
}
