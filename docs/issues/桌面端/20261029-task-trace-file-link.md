---
id: "2026-10-29-task-trace-file-link"
title: "任务 ↔ 调用 ↔ 文件关联（task_id 贯穿 trace）"
status: "todo"
created: "2026-10-29"
updated: "2026-10-29"
category: "桌面端"
tags: ["observability", "tasks", "trace", "priority-p1"]
---

# Issue: 任务 ↔ 调用 ↔ 文件关联（task_id 贯穿 trace）

## Goal

每条工具调用记录当前活动任务 ID；Tasks 详情展示「本任务触发的调用 / 改过的文件」，让任务、trace、文件历史三条线真正连起来。

## 背景

现有 trace 有 tool/source/UA/文件变更，但没有 task_id。用户在 Tasks 页看到阻塞任务，无法回答「它到底调用了什么、改了哪些文件」。

## 验收标准

- [ ] `TraceRecord` 与 `ExecutionRecord` 增加 `task_id`（可空）
- [ ] 调用发生时若有「当前任务」上下文，自动写入；无则留空
- [ ] `GET /internal/runtime/tasks/{id}/activity` 返回该任务的调用与文件变更摘要
- [ ] Tasks 详情新增两个区块：调用列表（时间/工具/耗时/状态）、文件变更列表（路径/+/-）
- [ ] 历史 trace（无 task_id）不受影响，UI 显示「未关联」
- [ ] 跨任务并发时 task_id 不串（有并发测试）

## 实施阶段

### Phase 1: 上下文定义
- [ ] 定义「当前任务」来源（Runtime 内存？最近一次 task_create 的 task_id？显式 `X-AgentDock-Task` 头？）
- [ ] 决定是否允许 MCP 客户端显式传 task_id

### Phase 2: 采集与投影
- [ ] observeToolCall 写入 task_id
- [ ] 任务活动聚合端点（调用 + 文件）

### Phase 3: UI
- [ ] Tasks 详情区块
- [ ] 空态与截断策略

## 关键决策

| 决策 | 理由 |
|---|---|
| task_id 随 context 传递 | 与 Source/UA 同一路径，不扩工具参数 |
| 聚合端点只读 | 不引入新的任务写语义 |

## 风险

- 「当前任务」若靠进程级状态，多客户端并发会串任务；必须用请求级上下文。

## 依赖

- 无（可独立开始，收益最高）

## 相关资源

- `internal/observability/context.go`、`internal/taskstate/model.go`

---

## Status 更新日志

- **2026-10-29**: 创建
