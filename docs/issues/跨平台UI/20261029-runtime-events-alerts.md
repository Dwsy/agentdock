---
id: "2026-10-29-runtime-events-alerts"
title: "运行时事件流（SSE）与阈值告警"
status: "done"
created: "2026-10-29"
updated: "2026-10-29"
category: "跨平台UI"
tags: ["sse", "alerts", "events", "console", "realtime"]
---

# Issue: 运行时事件流（SSE）与阈值告警

## Goal

以 SSE 事件流替代纯轮询，并在错误率 / trace 磁盘占用 / 任务阻塞超阈值时
在控制台横幅提醒；断线自动降级轮询。

## 验收标准

- [x] `GET /internal/runtime/events`（SSE）推送 call/task/artifact 事件
      + 15s ping 保活；发布非阻塞（有界缓冲、满丢弃计数）；断开退订
- [x] `GET /internal/runtime/alerts`：三规则（error_rate 默认 0.1、
      trace_disk 默认 512MB、task_blocked 默认 24h，环境变量可覆盖），
      去重（first_seen 稳定）、恢复计数、severity 分级（2 倍阈值 = danger）
- [x] 写操作与工具调用产生对应事件（source=internal 记录不变）
- [x] 控制台：SSE 订阅（指数退避重连 1s→30s），断线降级追踪页轮询，
      顶栏"实时/重连中/轮询"指示
- [x] 告警横幅：severity 着色、点击跳对应页、已恢复计数、无告警不渲染
- [x] 概览页最近 10 条事件时间线
- [x] 全部新逻辑有测试；goroutine 不泄漏（Close/Stop 收尾）

## 执行批次

- [x] E5 `backend_events`：事件总线 + SSE 端点 + 告警评估器——已交付（空报告，磁盘证据验证）
- [x] E6 `console_live`：SSE 客户端 + 横幅 + 指示灯 + 轮询降级——已交付（空报告，磁盘证据验证，
      含集成修复：TracesExport 响应包封解析对齐）
- [x] 集成验证 + 提交——已交付（空报告，磁盘证据验证，终验全绿后两笔提交推送）

## 集成发现（PM 关卡）

| 问题 | 处置 |
|------|------|
| v2 遗留：traces/export 后端返回 JSON 包封，console 按原始流解析 | 修复单派给 console_live（持有 client.go），解析改包封 |

## 关键决策

| 决策 | 理由 |
|------|------|
| SSE 而非 WebSocket | 单向推送足够、HTTP 语义简单、易代理 |
| 告警惰性评估（GET 时计算） | 无常驻评估器 goroutine，规则三条够用（YAGNI） |
| 轮询降级而非移除 | SSE 断线时观测不中断 |
| B7 多设备暂缓 | 本地 nexusbridge 无节点注册表，真实数据在远程 Nexus（见路线图） |

## 相关资源

- 前序：`20261029-mygo-console-v2-write-loop.md`
- 路线：`docs/architecture/20261029-issue-roadmap.md`（B6）

---

## Status 更新日志

- **2026-10-29**: 状态变更 → in_progress，备注: E5/E6 并行执行中，集成漂移修复单已下发
- **2026-10-29**: 状态变更 → done，备注: 终验复跑全绿——主模块 build/vet/test
  通过（observability+app 共 301 测试含子测试全绿）；console 113 测试全绿；
  gofmt 干净；console 三平台（windows/amd64、linux/amd64、darwin/arm64）交叉编译通过。
  路由：`GET /internal/runtime/events`（SSE，`event: <kind>` + `data: <json>`，15s ping）、
  `GET /internal/runtime/alerts`（error_rate/trace_disk/task_blocked 三规则）。
  文档：`docs/pr/跨平台UI/20261029-runtime-events-alerts.md`。
