---
id: "2026-10-29-mygo-cross-platform-ui-adr"
title: "ADR：控制面板跨平台 UI 采用 MyGo（增量并存）"
status: "proposed"
created: "2026-10-29"
updated: "2026-10-29"
tags: ["adr", "macos", "windows", "linux", "mygo", "ui"]
---

# ADR：控制面板跨平台 UI 采用 MyGo（增量并存）

## 状态

Proposed（待评审；本 ADR 只定方向，不承诺实现时间）

## 背景

当前桌面控制面板按平台各写一套：

| 平台 | 技术 | 规模 | 能力 |
|---|---|---|---|
| macOS | SwiftUI + AppKit（`swiftc` 直编） | `NativeControlPanelWindowController.swift` 4114 行 + `ServiceController.swift` 2135 行 | 10 个页面、托盘 popover、服务/权限/更新/隧道 |
| Windows | WinUI3 / .NET 8 + Go tray | 26 个 C#/XAML 文件 | 安装向导、托盘、服务 |
| Linux | 无 GUI | — | 只有 systemd + 网页状态页 |

上一轮新增的 7 组功能全部落在 macOS 单文件里，Windows/Linux 完全看不到。继续分平台实现会让「观测台」能力永远只有 1/3 平台可用，并且 UI 文件已经大到阻碍改动。

## 候选方案

| 方案 | 做法 | 成本 | 风险 |
|---|---|---|---|
| A 全量迁移 | 用 MyGo 替换 macOS/Windows 原生壳 | 高 | 安装器、服务、权限、公证全部重做；双平台用户可感知回归 |
| **B 增量并存（推荐）** | 新增 MyGo「控制面板」，承载只读观测 + 轻写；原生壳保留系统集成（服务生命周期、权限、安装/更新、托盘） | 中 | 双壳并存期的维护成本；托盘能力待验证 |
| C 继续分平台 | macOS 拆分文件，Windows 另写一套 WebView | 中高 | 三套 UI 长期漂移，正是当前问题 |

## 决策

采用 **B：增量并存**。

1. 新建跨平台 MyGo 应用（native UI，Go 包 `ui`），作为「AgentDock Console」。
2. 它只依赖本机 Runtime HTTP API（`/internal/runtime/*`），不与 Core 共享进程状态。
3. 原生壳保留：服务注册/启停、权限申请、安装与更新、托盘、登录项、隧道 supervisor。
4. 观测与写操作页面（能力目录、Tasks、Files、Traces、Analytics、Published、Settings 中的只读/轻写部分）由 MyGo Console 统一提供；macOS 原生壳逐步下线这些页面，保留系统集成页。
5. 迁移顺序：spike → 数据层 → 只读页 → 写操作 → 系统集成 → 打包分发。

## 理由（有据）

- MyGo 产物是单个几 MB Go 二进制，`CGO_ENABLED=0` + purego，可交叉编译三平台（`concept-no-cgo-and-size`、`mygo/docs/architecture.md`）。
- native UI 有 69 个组件，已覆盖本项目的页面骨架：`Sidebar`、`SearchField`、`Router`、`Table`、`Split`、`Accordion`、`ContextMenu`、`MenuButton`、`Icon`，以及 `ui/chart`（`widget-sidebar`、`widget-table`、`widget-chart`、`index-components`）。
- 自带打包与更新：`mygo build` 产 app bundle/DMG（macOS）、NSIS 安装器（Windows）、desktop entry + 归档（Linux）；`mygo.Updater` 读签名发布（`recipe-ship`、`doc-updates`、`doc-distribution`）。
- 现有 Go 侧 `desktopruntime` / `desktopcontrol` / `installer` / `selfupdate` 已经是平台能力层，MyGo Console 不需要重写它们。

## 明确不做

- 不在 MyGo 里重写安装器、服务注册、权限审批、代码签名流程。
- 不迁移 Windows 安装向导（Inno Setup）与 macOS 登录辅助进程到 MyGo。
- 不在第一阶段引入 webview 前端；先走 native UI，避免再引入一层 TS 构建。

## 未验证项（必须先 spike）

1. **系统托盘 / 菜单栏常驻**：MyGo 文档与条目中未检索到 tray 能力；若缺失，托盘继续由原生壳提供，Console 只做普通窗口。
2. 多窗口与单实例：Console 是否需要单窗口 + 深链（`agentdock://`）待验证。
3. macOS 原生观感：native UI 与现有 SwiftUI 页面的一致性需要原型对比。
4. 输入法、无障碍、键盘导航在 native UI 下的实际表现。

## 后果

正向：

- 一套 UI 代码覆盖 macOS/Windows/Linux，观测能力一次到位。
- 新功能只写一次，避免三份漂移。
- 二进制小、无前端构建链。

负向：

- 迁移期同时维护两套壳（建议设 2 个版本的窗口期）。
- 托盘、系统集成仍需原生侧，产品上会出现「两个入口」的过渡态。
- macOS 用户可能感知视觉变化。

## 关联

- Issues：`docs/issues/跨平台UI/`
- 上游依据：`~/.agents/skills/mygo-guide`（条目 `concept-no-cgo-and-size`、`widget-sidebar`、`widget-table`、`widget-chart`、`recipe-ship`）
