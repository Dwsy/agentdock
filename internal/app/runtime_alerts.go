package app

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/uvwt/agentdock/internal/observability"
	"github.com/uvwt/agentdock/internal/taskstate"
)

// 阈值告警规则的默认阈值与环境变量覆盖键。
const (
	alertEnvErrorRate    = "AGENTDOCK_ALERT_ERROR_RATE"
	alertEnvTraceDiskMB  = "AGENTDOCK_ALERT_TRACE_DISK_MB"
	alertEnvBlockedTaskH = "AGENTDOCK_ALERT_BLOCKED_TASK_H"

	defaultAlertErrorRate    = 0.1
	defaultAlertTraceDiskMB  = 512.0
	defaultAlertBlockedTaskH = 24.0

	// alertErrorRateMinWindow 是错误率规则的评估下限：调用数不足时错误率噪声太大。
	alertErrorRateMinWindow = 20
	// alertDangerMultiplier 决定 severity=danger 的升级倍数（error_rate 专用）。
	alertDangerMultiplier = 2.0
)

// 告警种类与去重键。
const (
	alertKindErrorRate   = "error_rate"
	alertKindTraceDisk   = "trace_disk"
	alertKindTaskBlocked = "task_blocked"

	alertKeyErrorRate = "error_rate:window"
	alertKeyTraceDisk = "trace_disk:root"
)

// alertThresholds 是本期三条固定规则的阈值集合，可被环境变量覆盖。
type alertThresholds struct {
	errorRate    float64
	traceDiskMB  float64
	blockedTaskH float64
}

func alertThresholdsFromEnv() alertThresholds {
	return alertThresholds{
		errorRate:    alertEnvFloat(alertEnvErrorRate, defaultAlertErrorRate, func(v float64) bool { return v > 0 && v <= 1 }),
		traceDiskMB:  alertEnvFloat(alertEnvTraceDiskMB, defaultAlertTraceDiskMB, positiveAlertValue),
		blockedTaskH: alertEnvFloat(alertEnvBlockedTaskH, defaultAlertBlockedTaskH, positiveAlertValue),
	}
}

func positiveAlertValue(v float64) bool { return v > 0 && !math.IsInf(v, 0) }

// alertEnvFloat 读取环境变量阈值；缺失或非法时回退默认值，告警评估不能被坏配置打断。
func alertEnvFloat(key string, fallback float64, valid func(float64) bool) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || !valid(value) {
		return fallback
	}
	return value
}

// alertRule 是响应里的规则投影。
type alertRule struct {
	Kind      string  `json:"kind"`
	Threshold float64 `json:"threshold"`
	Enabled   bool    `json:"enabled"`
}

// alertEntry 是一条活跃告警。ID 形如 "<kind>:<key>"，是去重表的键。
type alertEntry struct {
	ID        string    `json:"id"`
	Severity  string    `json:"severity"`
	Kind      string    `json:"kind"`
	Message   string    `json:"message"`
	Detail    string    `json:"detail"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Active    bool      `json:"active"`
}

// alertTracker 是进程内告警去重表：同 kind+key 只保留一条，first_seen 不变、
// last_seen 刷新；恢复后移除并累计 resolved_count。惰性评估，无后台 goroutine。
type alertTracker struct {
	mu            sync.Mutex
	active        map[string]alertEntry
	resolvedCount uint64
}

// update 用本次评估结果刷新去重表，返回当前活跃告警（按 kind、id 稳定排序）与累计恢复数。
func (t *alertTracker) update(current []alertEntry, now time.Time) ([]alertEntry, uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.active == nil {
		t.active = make(map[string]alertEntry)
	}
	for _, entry := range current {
		entry.LastSeen = now
		entry.Active = true
		if existing, ok := t.active[entry.ID]; ok {
			entry.FirstSeen = existing.FirstSeen
		} else {
			entry.FirstSeen = now
		}
		t.active[entry.ID] = entry
	}
	for id := range t.active {
		if !alertEntryPresent(current, id) {
			delete(t.active, id)
			t.resolvedCount++
		}
	}
	active := make([]alertEntry, 0, len(t.active))
	for _, entry := range t.active {
		active = append(active, entry)
	}
	sort.Slice(active, func(i, j int) bool { return alertEntryLess(active[i], active[j]) })
	return active, t.resolvedCount
}

func alertEntryPresent(entries []alertEntry, id string) bool {
	for _, entry := range entries {
		if entry.ID == id {
			return true
		}
	}
	return false
}

func alertEntryLess(left, right alertEntry) bool {
	if left.Kind != right.Kind {
		return left.Kind < right.Kind
	}
	return left.ID < right.ID
}

// RuntimeAlerts 惰性评估阈值告警：计算当前活跃告警并刷新进程内去重表。
// 响应契约：{"ok","source","alerts","resolved_count","rules"}。
func (r *Runtime) RuntimeAlerts() (Result, error) {
	if r == nil {
		return Result{"ok": true, "source": runtimeAPISource, "alerts": []alertEntry{}, "resolved_count": 0, "rules": []alertRule{}}, nil
	}
	thresholds := alertThresholdsFromEnv()
	now := time.Now().UTC()
	current := make([]alertEntry, 0, 3)

	// 规则一：analytics 窗口错误率。窗口调用数不足下限时不评估。
	var windowCalls, windowErrors int
	var windowRate float64
	if r != nil && r.observer != nil {
		for _, record := range r.observer.RecentCalls() {
			windowCalls++
			if !record.Success {
				windowErrors++
			}
		}
	}
	if windowCalls >= alertErrorRateMinWindow {
		windowRate = float64(windowErrors) / float64(windowCalls)
		if windowRate > thresholds.errorRate {
			severity := "warning"
			if windowRate > alertDangerMultiplier*thresholds.errorRate {
				severity = "danger"
			}
			current = append(current, alertEntry{
				ID:       alertKeyErrorRate,
				Severity: severity,
				Kind:     alertKindErrorRate,
				Message:  fmt.Sprintf("错误率 %s 超过阈值 %s", formatAlertPercent(windowRate), formatAlertPercent(thresholds.errorRate)),
				Detail:   fmt.Sprintf("最近 %d 次工具调用中 %d 次失败，窗口错误率 %s 超过阈值 %s", windowCalls, windowErrors, formatAlertPercent(windowRate), formatAlertPercent(thresholds.errorRate)),
			})
		}
	}

	// 规则二：trace 磁盘占用。memory/off 模式不评估。
	traceDiskEnabled := r != nil && r.traces != nil && r.traces.Mode() == observability.TraceModeDisk
	if traceDiskEnabled {
		usedMB := float64(r.traces.DiskBytes()) / (1 << 20)
		if usedMB > thresholds.traceDiskMB {
			current = append(current, alertEntry{
				ID:       alertKeyTraceDisk,
				Severity: "warning",
				Kind:     alertKindTraceDisk,
				Message:  fmt.Sprintf("trace 磁盘占用 %sMB 超过阈值 %sMB", formatAlertNumber(usedMB), formatAlertNumber(thresholds.traceDiskMB)),
				Detail:   fmt.Sprintf("trace 保留目录 JSONL 总大小 %sMB 超过阈值 %sMB", formatAlertNumber(usedMB), formatAlertNumber(thresholds.traceDiskMB)),
			})
		}
	}

	// 规则三：blocked 任务停留时长。
	if r != nil && r.taskTools != nil {
		tasks, err := r.taskTools.BlockedTasks()
		if err != nil {
			return nil, err
		}
		for _, task := range tasks {
			blockedSince := taskBlockedSince(task)
			blockedH := now.Sub(blockedSince).Hours()
			if blockedH > thresholds.blockedTaskH {
				current = append(current, alertEntry{
					ID:       alertKeyTaskBlocked(task.ID),
					Severity: "warning",
					Kind:     alertKindTaskBlocked,
					Message: fmt.Sprintf("任务 %s 阻塞停留 %sh 超过阈值 %sh",
						task.ID, formatAlertNumber(blockedH), formatAlertNumber(thresholds.blockedTaskH)),
					Detail: fmt.Sprintf("任务 %q（%s）自 %s 起处于 blocked，已停留 %s，超过阈值 %sh",
						task.Title, task.ID, blockedSince.UTC().Format(time.RFC3339), formatAlertDuration(now.Sub(blockedSince)), formatAlertNumber(thresholds.blockedTaskH)),
				})
			}
		}
	}

	alerts, resolved := r.alerts.update(current, now)
	rules := []alertRule{
		{Kind: alertKindErrorRate, Threshold: thresholds.errorRate, Enabled: true},
		{Kind: alertKindTraceDisk, Threshold: thresholds.traceDiskMB, Enabled: traceDiskEnabled},
		{Kind: alertKindTaskBlocked, Threshold: thresholds.blockedTaskH, Enabled: true},
	}
	return Result{
		"ok":             true,
		"source":         runtimeAPISource,
		"alerts":         alerts,
		"resolved_count": resolved,
		"rules":          rules,
	}, nil
}

func alertKeyTaskBlocked(taskID string) string {
	return alertKindTaskBlocked + ":" + taskID
}

// taskBlockedSince 取 blocked 相关事件时间戳；缺失时回退任务 UpdatedAt。
func taskBlockedSince(task taskstate.Task) time.Time {
	for i := len(task.Events) - 1; i >= 0; i-- {
		if task.Events[i].Type == "blocked" && !task.Events[i].CreatedAt.IsZero() {
			return task.Events[i].CreatedAt
		}
	}
	return task.UpdatedAt
}

// formatAlertPercent 把比率格式化成百分比文本：0.25 → "25%"。
func formatAlertPercent(rate float64) string {
	return formatAlertNumber(rate*100) + "%"
}

// formatAlertNumber 保留至多一位小数并去掉尾随零：600.50 → "600.5"。
func formatAlertNumber(value float64) string {
	return strconv.FormatFloat(math.Round(value*10)/10, 'f', -1, 64)
}

func formatAlertDuration(d time.Duration) string {
	return formatAlertNumber(d.Hours()) + "h"
}
