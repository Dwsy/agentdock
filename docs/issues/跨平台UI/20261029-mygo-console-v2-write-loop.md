---
id: "2026-10-29-mygo-console-v2-write-loop"
title: "MyGo 控制台 v2：写操作闭环、任务关联与全局搜索"
status: "done"
created: "2026-10-29"
updated: "2026-10-29"
category: "跨平台UI"
tags: ["mygo", "console", "write-ops", "task-link", "search"]
---

# Issue: MyGo 控制台 v2（写操作闭环、任务关联与全局搜索）

## Goal

把 MyGo 控制台从只读观测台推进到操作台：后端补齐任务写操作、
发布撤销/延期、trace 导出/清空、task_id 贯穿；前端补追踪页轮询、
⌘K 全局搜索与写操作页。

## 批次（PM 排期，子代理执行）

### 批次 1（并行）
- [x] E1 `backend_task`：task_id 贯穿 trace + `GET /tasks/{id}/activity`
      + `POST /tasks/{id}/actions`（checkpoint/complete/delete，409 冲突语义）
- [x] E3 `console_poll`：追踪页 5s 自动轮询（可暂停）+ ⌘K 全局搜索（客户端聚合）

### 批次 2（E1 落地后，避免 dispatch.go 冲突）
- [x] E2 `backend_ops`：发布撤销 `DELETE /artifacts/{id}` +
      延期 `POST /artifacts/{id}/extend` + `GET /traces/export?format=json|csv` +
      `DELETE /traces`（清空）

### 批次 3（后端契约齐后）
- [x] E4 `console_write`：任务页操作（二次确认）、发布页撤销/延期、
      追踪页导出/清空、任务详情关联区块（本任务的调用与文件变更）

### 收尾
- [x] 全量回归（主模块 58 包 + console 三包）、三平台交叉编译
- [x] workhub 文档更新、分批提交推送

## 验收标准

- [x] trace 记录带 task_id（HTTP 头 `X-AgentDock-Task` 注入），可搜索、随 JSONL 持久化
- [x] 任务详情展示"本任务的调用 / 文件变更"（activity 端点）
- [x] checkpoint/complete/delete：非法迁移 409、并发写单胜、删除与现有 DELETE 等价
- [x] 发布撤销后链接立即失效；延期受 MaxRetention 上限约束
- [x] trace 导出 JSON/CSV 正确转义；清空后内存与磁盘一致、UI 提示文件历史同步清空
- [x] 追踪页 5s 轮询可暂停；⌘K 跨 7 类数据搜索并跳页定位
- [x] 所有写操作进 trace（source=internal）

## 关键决策

| 决策 | 理由 |
|------|------|
| task_id 走 HTTP 头注入 context | 与 Source/UA 同路径，零工具签名变更，无并发串号 |
| 后端两批串行（E1→E2） | dispatch.go 路由表是共享文件，避免编辑冲突 |
| 全局搜索先客户端聚合 | 现有 11 端点数据已够，后端 search 端点留 P1 备选 |
| 导出走同筛选契约 | "导出的即看到的" |

## 相关资源

- 前序：`2026-10-29-mygo-console-implementation.md`（v1，已交付）
- 路线：`docs/architecture/20261029-issue-roadmap.md`（B1/B2/B3/B4/B5 的落地）

---

## Status 更新日志

- **2026-10-29**: 状态变更 → in_progress，备注: 批次 1 两个执行器并行开工
- **2026-10-29**: 状态变更 → done，备注: 四个执行器交付；主模块回归全绿、console 103 测试全绿、三平台交叉编译通过
