package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/observability"
	"github.com/uvwt/agentdock/internal/taskstate"
	tooltask "github.com/uvwt/agentdock/internal/tool/task"
)

// recordCalls 直接驱动 recorder 热路径，构造可控的 analytics 窗口。
func recordCalls(rt *Runtime, total, failures int) {
	for index := 0; index < total; index++ {
		success := index >= failures
		errorCode, errorCategory := "", ""
		if !success {
			errorCode, errorCategory = "TIMEOUT", "runtime"
		}
		rt.observer.BeginTool()
		rt.observer.EndTool(observability.CallMeta{
			Tool:          "tool",
			Source:        observability.SourceInternal,
			StartedAt:     time.Now(),
			Duration:      time.Millisecond,
			Success:       success,
			ErrorCode:     errorCode,
			ErrorCategory: errorCategory,
		})
	}
}

func mustAlerts(t *testing.T, rt *Runtime) ([]alertEntry, uint64, []alertRule) {
	t.Helper()
	result, err := rt.RuntimeAlerts()
	if err != nil {
		t.Fatal(err)
	}
	if result["ok"] != true || result["source"] != "agentdock-api" {
		t.Fatalf("alert response envelope = %#v", result)
	}
	alerts, ok := result["alerts"].([]alertEntry)
	if !ok {
		t.Fatalf("alerts = %#v", result["alerts"])
	}
	resolved, ok := result["resolved_count"].(uint64)
	if !ok {
		t.Fatalf("resolved_count = %#v", result["resolved_count"])
	}
	rules, ok := result["rules"].([]alertRule)
	if !ok || len(rules) != 3 {
		t.Fatalf("rules = %#v", result["rules"])
	}
	return alerts, resolved, rules
}

func requireSingleAlert(t *testing.T, alerts []alertEntry, id, kind, severity string) alertEntry {
	t.Helper()
	if len(alerts) != 1 {
		t.Fatalf("alerts = %#v, want exactly one", alerts)
	}
	entry := alerts[0]
	if entry.ID != id || entry.Kind != kind || entry.Severity != severity {
		t.Fatalf("alert = %#v, want id %q kind %q severity %q", entry, id, kind, severity)
	}
	if !entry.Active || entry.FirstSeen.IsZero() || entry.LastSeen.IsZero() {
		t.Fatalf("alert timestamps/active = %#v", entry)
	}
	return entry
}

func TestRuntimeAlertsErrorRateHitsWithWarningAndDanger(t *testing.T) {
	// 20 次调用 3 次失败 = 15% > 10%，但未到 2 倍阈值 → warning。
	warningRT := &Runtime{observer: observability.NewRecorder(100)}
	recordCalls(warningRT, 20, 3)
	alerts, resolved, rules := mustAlerts(t, warningRT)
	entry := requireSingleAlert(t, alerts, "error_rate:window", "error_rate", "warning")
	if entry.Message != "错误率 15% 超过阈值 10%" {
		t.Fatalf("message = %q", entry.Message)
	}
	if resolved != 0 {
		t.Fatalf("resolved_count = %d", resolved)
	}
	if rules[0] != (alertRule{Kind: "error_rate", Threshold: 0.1, Enabled: true}) {
		t.Fatalf("error_rate rule = %#v", rules[0])
	}

	// 20 次调用 5 次失败 = 25% > 2×10% → danger。
	dangerRT := &Runtime{observer: observability.NewRecorder(100)}
	recordCalls(dangerRT, 20, 5)
	alerts, _, _ = mustAlerts(t, dangerRT)
	requireSingleAlert(t, alerts, "error_rate:window", "error_rate", "danger")
}

func TestRuntimeAlertsErrorRateSkipsSmallWindowAndLowRate(t *testing.T) {
	// 窗口调用数 19 < 20：即使全失败也不评估。
	small := &Runtime{observer: observability.NewRecorder(100)}
	recordCalls(small, 19, 19)
	alerts, _, _ := mustAlerts(t, small)
	if len(alerts) != 0 {
		t.Fatalf("small window alerts = %#v", alerts)
	}

	// 30 次调用 1 次失败 ≈ 3.3% ≤ 10%：不命中。
	quiet := &Runtime{observer: observability.NewRecorder(100)}
	recordCalls(quiet, 30, 1)
	alerts, _, _ = mustAlerts(t, quiet)
	if len(alerts) != 0 {
		t.Fatalf("low error rate alerts = %#v", alerts)
	}
}

func TestRuntimeAlertsErrorRateThresholdEnvOverride(t *testing.T) {
	t.Setenv("AGENTDOCK_ALERT_ERROR_RATE", "0.4")
	// 25% 超过默认 10% 但不超过覆盖后的 40%。
	rt := &Runtime{observer: observability.NewRecorder(100)}
	recordCalls(rt, 20, 5)
	alerts, _, rules := mustAlerts(t, rt)
	if len(alerts) != 0 {
		t.Fatalf("alerts with raised threshold = %#v", alerts)
	}
	if rules[0].Threshold != 0.4 {
		t.Fatalf("rule threshold = %#v, want 0.4", rules[0])
	}

	hot := &Runtime{observer: observability.NewRecorder(100)}
	recordCalls(hot, 20, 11) // 55% > 40%，未到 2 倍阈值 → warning。
	alerts, _, _ = mustAlerts(t, hot)
	requireSingleAlert(t, alerts, "error_rate:window", "error_rate", "warning")

	// 20 次调用 19 次失败 = 95% > 2×40% → danger。
	danger := &Runtime{observer: observability.NewRecorder(100)}
	recordCalls(danger, 20, 19)
	alerts, _, _ = mustAlerts(t, danger)
	requireSingleAlert(t, alerts, "error_rate:window", "error_rate", "danger")
}

func TestRuntimeAlertsTraceDiskRule(t *testing.T) {
	root := t.TempDir()
	store, err := observability.NewTraceStore(observability.TraceOptions{
		Mode: observability.TraceModeDisk,
		Root: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	payload := bytes.Repeat([]byte("x"), 1<<20) // 1MB
	if err := os.WriteFile(filepath.Join(root, "2026-01-01.jsonl"), payload, 0o600); err != nil {
		t.Fatal(err)
	}

	// 默认阈值 512MB：1MB 不命中，且规则处于启用态。
	rt := &Runtime{traces: store}
	alerts, _, rules := mustAlerts(t, rt)
	if len(alerts) != 0 {
		t.Fatalf("trace disk alerts = %#v", alerts)
	}
	if rules[1] != (alertRule{Kind: "trace_disk", Threshold: 512, Enabled: true}) {
		t.Fatalf("trace_disk rule = %#v", rules[1])
	}

	// 环境变量覆盖 0.5MB：命中。
	t.Setenv("AGENTDOCK_ALERT_TRACE_DISK_MB", "0.5")
	alerts, _, _ = mustAlerts(t, rt)
	entry := requireSingleAlert(t, alerts, "trace_disk:root", "trace_disk", "warning")
	if entry.Message == "" || entry.Detail == "" {
		t.Fatalf("trace disk alert text = %#v", entry)
	}

	// 清空保留文件后恢复。
	if err := os.Remove(filepath.Join(root, "2026-01-01.jsonl")); err != nil {
		t.Fatal(err)
	}
	alerts, resolved, _ := mustAlerts(t, rt)
	if len(alerts) != 0 || resolved != 1 {
		t.Fatalf("after cleanup alerts = %#v resolved = %d", alerts, resolved)
	}
}

func TestRuntimeAlertsTraceDiskSkipsMemoryMode(t *testing.T) {
	store, err := observability.NewTraceStore(observability.TraceOptions{Mode: observability.TraceModeMemory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	rt := &Runtime{traces: store}
	alerts, _, rules := mustAlerts(t, rt)
	if len(alerts) != 0 {
		t.Fatalf("memory mode alerts = %#v", alerts)
	}
	if rules[1].Enabled {
		t.Fatalf("trace_disk rule must be disabled off disk mode: %#v", rules[1])
	}
}

// newBlockedTaskRuntime 建一个真实 blocked 任务并把状态文件时间戳回拨 age。
func newBlockedTaskRuntime(t *testing.T, age time.Duration) (*Runtime, string) {
	t.Helper()
	root := t.TempDir()
	tasks, err := taskstate.New(filepath.Join(root, "tasks"))
	if err != nil {
		t.Fatal(err)
	}
	task, err := tasks.Create("Blocked rollout", "waiting on external review", []string{"review passes"},
		[]taskstate.TaskStepInput{{ID: "review", Title: "Review"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Block(task.ID, "reviewer unavailable"); err != nil {
		t.Fatal(err)
	}
	ageTaskStateFile(t, tasks.Root(), task.ID, age)
	service := tooltask.New(func() config.Config { return config.Config{} }, tasks)
	return &Runtime{taskTools: service}, task.ID
}

// ageTaskStateFile 把任务的 blocked 事件与 updated_at 回拨到 now-age。
func ageTaskStateFile(t *testing.T, root, id string, age time.Duration) {
	t.Helper()
	path := filepath.Join(root, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var task taskstate.Task
	if err := json.Unmarshal(data, &task); err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-age)
	task.UpdatedAt = past
	for index := range task.Events {
		if task.Events[index].Type == "blocked" {
			task.Events[index].CreatedAt = past
		}
	}
	encoded, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeAlertsTaskBlockedRule(t *testing.T) {
	rt, taskID := newBlockedTaskRuntime(t, 48*time.Hour)
	alerts, _, rules := mustAlerts(t, rt)
	entry := requireSingleAlert(t, alerts, "task_blocked:"+taskID, "task_blocked", "warning")
	if entry.Message == "" || entry.Detail == "" {
		t.Fatalf("blocked alert text = %#v", entry)
	}
	if rules[2] != (alertRule{Kind: "task_blocked", Threshold: 24, Enabled: true}) {
		t.Fatalf("task_blocked rule = %#v", rules[2])
	}
}

func TestRuntimeAlertsTaskBlockedSkipsFreshBlocksAndHonorsEnv(t *testing.T) {
	// 刚阻塞 1 小时：低于默认 24 小时阈值。
	fresh, _ := newBlockedTaskRuntime(t, time.Hour)
	alerts, _, _ := mustAlerts(t, fresh)
	if len(alerts) != 0 {
		t.Fatalf("fresh block alerts = %#v", alerts)
	}

	// 环境变量覆盖 96 小时：48 小时的阻塞不再命中。
	t.Setenv("AGENTDOCK_ALERT_BLOCKED_TASK_H", "96")
	aged, _ := newBlockedTaskRuntime(t, 48*time.Hour)
	alerts, _, rules := mustAlerts(t, aged)
	if len(alerts) != 0 {
		t.Fatalf("alerts with raised blocked threshold = %#v", alerts)
	}
	if rules[2].Threshold != 96 {
		t.Fatalf("task_blocked threshold = %#v, want 96", rules[2])
	}
}

func TestRuntimeAlertsDedupKeepsFirstSeenStable(t *testing.T) {
	rt := &Runtime{observer: observability.NewRecorder(100)}
	recordCalls(rt, 20, 5)

	firstAlerts, _, _ := mustAlerts(t, rt)
	first := requireSingleAlert(t, firstAlerts, "error_rate:window", "error_rate", "danger")

	time.Sleep(5 * time.Millisecond)
	secondAlerts, _, _ := mustAlerts(t, rt)
	second := requireSingleAlert(t, secondAlerts, "error_rate:window", "error_rate", "danger")

	// 同 kind+key 只保留一条：first_seen 不变，last_seen 刷新。
	if !second.FirstSeen.Equal(first.FirstSeen) {
		t.Fatalf("first_seen changed: %v -> %v", first.FirstSeen, second.FirstSeen)
	}
	if !second.LastSeen.After(first.LastSeen) {
		t.Fatalf("last_seen not refreshed: %v -> %v", first.LastSeen, second.LastSeen)
	}
}

func TestRuntimeAlertsRecoveryRemovesAlertAndCountsResolved(t *testing.T) {
	rt := &Runtime{observer: observability.NewRecorder(25)}
	recordCalls(rt, 21, 21)
	alerts, resolved, _ := mustAlerts(t, rt)
	requireSingleAlert(t, alerts, "error_rate:window", "error_rate", "danger")

	// 25 次成功调用把失败调用挤出窗口：告警恢复。
	recordCalls(rt, 25, 0)
	alerts, resolved, _ = mustAlerts(t, rt)
	if len(alerts) != 0 {
		t.Fatalf("alerts after recovery = %#v", alerts)
	}
	if resolved != 1 {
		t.Fatalf("resolved_count = %d, want 1", resolved)
	}

	// 恢复后再次评估保持稳定，resolved_count 不重复累计。
	alerts, resolved, _ = mustAlerts(t, rt)
	if len(alerts) != 0 || resolved != 1 {
		t.Fatalf("post-recovery alerts = %#v resolved = %d", alerts, resolved)
	}
}

func TestRuntimeAlertsEmptyArrayAndContractShape(t *testing.T) {
	rt := &Runtime{observer: observability.NewRecorder(100)}
	recordCalls(rt, 5, 0)
	result, err := rt.RuntimeAlerts()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["ok"] != true || decoded["source"] != "agentdock-api" || decoded["resolved_count"] != float64(0) {
		t.Fatalf("response envelope = %s", encoded)
	}
	if string(mustJSON(t, decoded["alerts"])) != "[]" {
		t.Fatalf("alerts must serialize as empty array: %s", encoded)
	}
	rulesJSON := mustJSON(t, decoded["rules"])
	var gotRules []alertRule
	if err := json.Unmarshal(rulesJSON, &gotRules); err != nil {
		t.Fatal(err)
	}
	wantRules := []alertRule{
		{Kind: "error_rate", Threshold: 0.1, Enabled: true},
		{Kind: "trace_disk", Threshold: 512, Enabled: false},
		{Kind: "task_blocked", Threshold: 24, Enabled: true},
	}
	if !reflect.DeepEqual(gotRules, wantRules) {
		t.Fatalf("rules = %s, want %s", rulesJSON, mustJSON(t, wantRules))
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
