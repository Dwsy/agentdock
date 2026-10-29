---
id: "2026-10-29-console-system-integration"
title: "Console 系统集成边界（托盘/服务/权限/更新）"
status: "todo"
created: "2026-10-29"
updated: "2026-10-29"
category: "跨平台UI"
tags: ["mygo", "tray", "service", "permissions"]
---

# Issue: Console 系统集成边界（托盘/服务/权限/更新）

## Goal

明确 Console 与原生壳的职责边界：Console 不复制服务生命周期、权限、安装与更新逻辑；需要时通过既有 CLI 或原生壳完成。

## 背景

现有平台能力已经在 Go 侧：`internal/desktopruntime`（服务/隧道）、`internal/desktopcontrol`（控制管道）、`internal/installer`、`internal/selfupdate`。Console 若重写这些，会形成第二套生命周期入口。

## 验收标准

- [ ] Console 只通过 `agentdock` CLI 或 HTTP 读取状态，不直接操作 launchd/systemd/Task Scheduler
- [ ] 托盘入口归属明确：MyGo 有 tray 则由 Console 提供；没有则继续由原生壳提供
- [ ] 权限检查复用现有 CLI（`agentdock` 诊断命令），不在 Console 内重复实现
- [ ] 更新入口只打开原生更新流程，不在 Console 内下载/替换二进制
- [ ] 单实例与深链行为已定义并测试

## 实施阶段

### Phase 1: 边界文档
- [ ] 能力矩阵：Console / 原生壳 / CLI 各负责什么
- [ ] 与 `mygo-shell-spike` 的托盘结论对齐

### Phase 2: 集成
- [ ] 状态读取（服务/隧道/更新）
- [ ] 托盘或菜单栏入口
- [ ] 深链打开指定页面

## 关键决策

| 决策 | 理由 |
|---|---|
| 系统集成留在原生/Go CLI | 已在生产验证，重写只有风险 |
| Console 可被独立启动 | 不依赖原生 App 运行，便于排障 |

## 依赖

- `mygo-shell-spike`

## 相关资源

- `docs/runtime-access-and-recovery.md`
- `internal/desktopcontrol`、`internal/desktopruntime`

---

## Status 更新日志

- **2026-10-29**: 创建
