package scripts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 托盘面板必须能看到后端阈值告警与代码行数变动：
// 告警由 /internal/runtime/alerts 提供（后端惰性评估并去重），
// 代码变动来自 analytics 的 tool_stats。macOS 侧没有 Swift 单测框架，
// 这里锁住端点与展示层的接线，防止回归成"只有服务状态"。
func TestMacOSTraySurfacesAlertsAndCodeChanges(t *testing.T) {
	root := filepath.Join("..", "..", "desktop", "macos", "AgentDockApp", "Sources")

	service, err := os.ReadFile(filepath.Join(root, "ServiceController.swift"))
	if err != nil {
		t.Fatalf("read ServiceController.swift: %v", err)
	}
	for _, want := range []string{
		`path: "/internal/runtime/alerts"`,
		"struct RuntimeAlertsPayload: Decodable",
		"case resolvedCount = \"resolved_count\"",
	} {
		if !strings.Contains(string(service), want) {
			t.Fatalf("macOS runtime client must keep %q", want)
		}
	}

	tray, err := os.ReadFile(filepath.Join(root, "TrayPopoverController.swift"))
	if err != nil {
		t.Fatalf("read TrayPopoverController.swift: %v", err)
	}
	for _, want := range []string{
		`L10n.text("Alerts")`,
		`L10n.text("Code changes")`,
		"codeInsertions",
		"codeDeletions",
		"stat.insertions",
		"stat.deletions",
		"onOpenAlertDetail",
		"alertTarget(",
	} {
		if !strings.Contains(string(tray), want) {
			t.Fatalf("macOS tray panel must keep %q", want)
		}
	}

	panel, err := os.ReadFile(filepath.Join(root, "NativeControlPanelWindowController.swift"))
	if err != nil {
		t.Fatalf("read NativeControlPanelWindowController.swift: %v", err)
	}
	for _, want := range []string{
		"enum ControlPanelTarget",
		"func present(target: ControlPanelTarget",
	} {
		if !strings.Contains(string(panel), want) {
			t.Fatalf("macOS control panel must keep %q", want)
		}
	}

	delegate, err := os.ReadFile(filepath.Join(root, "AppDelegate.swift"))
	if err != nil {
		t.Fatalf("read AppDelegate.swift: %v", err)
	}
	for _, want := range []string{
		"service.runtimeAlerts(",
		"refreshStatusItemBadge()",
	} {
		if !strings.Contains(string(delegate), want) {
			t.Fatalf("macOS app delegate must keep %q", want)
		}
	}
}
