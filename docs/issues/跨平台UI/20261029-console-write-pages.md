---
id: "2026-10-29-console-write-pages"
title: "Console 写操作页面（任务/发布/trace）"
status: "todo"
created: "2026-10-29"
updated: "2026-10-29"
category: "跨平台UI"
tags: ["mygo", "ui", "write", "approval"]
---

# Issue: Console 写操作页面（任务/发布/trace）

## Goal

在 Console 中提供受控写操作：任务 checkpoint/complete/delete、发布撤销/延长、trace 清空/导出、trace 开关。每个写操作有二次确认与结果反馈。

## 背景

当前 macOS 面板除配置外全只读。看到异常只能回 MCP/CLI。写操作必须建立在独立后端 issue（`task-write-actions`、`artifact-revoke-extend`、`trace-export-clear`）之上。

## 验收标准

- [ ] 每个写操作有明确的确认对话框（destructive 用红字）
- [ ] 写操作结果进入 trace（`source=internal`，含 UA `agentdock-console`）
- [ ] 失败时展示可读错误，不静默
- [ ] 任务状态迁移非法时后端拒绝，UI 显示原因
- [ ] 发布撤销后链接立即 404；延长后链接签名更新
- [ ] trace 清空后 UI 立即反映，且磁盘文件按策略删除

## 实施阶段

### Phase 1: 任务
- [ ] checkpoint / complete / delete
- [ ] 与 `task-write-actions` 契约联调

### Phase 2: 发布与 trace
- [ ] 撤销 / 延长
- [ ] 导出 / 清空 / 开关

## 关键决策

| 决策 | 理由 |
|---|---|
| 写操作全部走后端 API，不在 UI 直接改文件 | 保持审计与校验唯一入口 |
| 破坏性操作二次确认 | 与现有 MCP 工具语义一致 |

## 依赖

- `task-write-actions`
- `artifact-revoke-extend`
- `trace-export-clear`
- `console-readonly-pages`

## 相关资源

- ADR：`docs/architecture/20261029-mygo-cross-platform-ui-adr.md`

---

## Status 更新日志

- **2026-10-29**: 创建
