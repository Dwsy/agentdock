---
id: "2026-10-29-mygo-console-v2"
title: "MyGo 控制台 v2：写操作闭环、任务关联、轮询与全局搜索"
status: "done"
created: "2026-10-29"
updated: "2026-10-29"
category: "跨平台UI"
tags: ["mygo", "console", "write-ops", "pr"]
---

# PR: MyGo 控制台 v2（写操作闭环、任务关联、轮询与全局搜索）

## 变更概述

后端（主模块）补齐任务写操作、发布撤销/延期、trace 导出/清空、
task_id 贯穿；控制台（desktop/console）补齐写操作页、任务关联区块、
追踪页自动轮询与 ⌘K 全局搜索。

## 后端（internal/）

- **task_id 贯穿**：`observability.WithTaskID/TaskIDFromContext`（128 字节
  规范化）、`ExecutionRecord/TraceRecord/磁盘 JSONL` 增 `task_id,omitempty`、
  搜索字段纳入、HTTP 头 `X-AgentDock-Task` 注入（mcp_endpoint）。
- **任务活动**：`GET /internal/runtime/tasks/{id}/activity`——按 task_id
  聚合调用与文件变更（off 模式返回空数组 + mode）。
- **任务写操作**：`POST /internal/runtime/tasks/{id}/actions`
  （checkpoint/complete/delete）；非法状态迁移 409 INVALID_STATUS、
  并发写单胜 CONFLICT；复用 taskstate 状态机不复制。
- **发布物**：`DELETE /internal/runtime/artifacts/{id}`（Remove+ErrNotFound）、
  `POST /internal/runtime/artifacts/{id}/extend`（hours 1..720、
  MaxRetention 钳制、重签 URL）。
- **trace 导出/清空**：`GET /internal/runtime/traces/export?format=json|csv`
  （CSV 标准转义、payload 换行转 `\n`）、`DELETE /internal/runtime/traces`
  （Clear 清内存+删磁盘、返回 cleared）。

## 控制台（desktop/console）

- runtimeapi 客户端扩 6 方法（TaskActions/TaskActivity/ArtifactRevoke/
  ArtifactExtend/TracesExport/TracesClear）+ 契约测试；Backend 接口同步扩。
- 任务页：详情"本任务的调用与文件"区块（含 trace off 空态）、
  Checkpoint/Complete/Delete 操作（二次确认）。
- 发布页：撤销（二次确认）、延期（1..720 校验）。
- 追踪页：导出 JSON/CSV（保存到 exports 目录 + 路径可复制）、清空
  （提示文件历史同步清空）、**5s 自动轮询可暂停**（ui.Switch + 上次刷新时间）。
- **⌘K 全局搜索**（search.go）：浮层分组（工具/任务/文件/发布/追踪/技能/规则）
  过滤 + 跳页定位；Esc 关闭。

## 测试矩阵

| 检查 | 结果 |
|------|------|
| 主模块 `go test ./... -count=1`（58 包回归） | 全绿 |
| console `go test ./... -count=1` | **103 项全绿**（v1 76 → +27） |
| console `go vet ./...` / `gofmt -l` | 通过 / 空 |
| 三平台交叉编译（darwin/windows/linux，CGO_ENABLED=0） | 通过 |

## 已知边界

- 写操作仅限任务/发布/trace 三域；MCP/插件生命周期仍归工具层。
- 全局搜索为客户端聚合；后端 search 端点留 P1 备选。
- SSE 事件流、多设备视图、告警为 P2（路线图 B6/B7）。

## 关联

- Issue: `docs/issues/跨平台UI/20261029-mygo-console-v2-write-loop.md`
- 前序: `docs/pr/跨平台UI/20261029-mygo-console-v1.md`
- 路线: `docs/architecture/20261029-issue-roadmap.md`
