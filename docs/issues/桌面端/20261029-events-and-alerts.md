---
id: "2026-10-29-events-and-alerts"
title: "事件流与阈值告警"
status: "todo"
created: "2026-10-29"
updated: "2026-10-29"
category: "桌面端"
tags: ["events", "sse", "alerts", "priority-p2"]
---

# Issue: 事件流与阈值告警

## Goal

用 SSE 事件流替代轮询，并在错误率/磁盘占用/任务阻塞超阈值时提醒（菜单栏徽标或系统通知）。

## 背景

当前 14 处固定间隔轮询（2.5s～20s），trace 变大后既有流量也有延迟。用户要的是「有事告诉我」，不是「我定时去看」。

## 验收标准

- [ ] `GET /internal/runtime/events`（SSE）推送：任务变更、trace 新增（可采样）、artifact 变更、服务状态
- [ ] UI 断线自动重连（指数退避），重连后补拉一次全量
- [ ] 告警规则可配置：错误率 > x%（窗口 N）、trace 目录 > y MB、任务阻塞 > 24h
- [ ] 同类告警去重（冷却期），状态恢复后自动清除
- [ ] 托盘/菜单栏显示未读数或状态点；点击进入对应页面
- [ ] 关闭告警不影响数据采集

## 实施阶段

### Phase 1: SSE
- [ ] 事件总线（进程内）+ 端点
- [ ] 客户端订阅与重连

### Phase 2: 告警
- [ ] 规则评估器（定时 + 事件驱动）
- [ ] 通知出口（macOS 通知中心 / 托盘）

## 关键决策

| 决策 | 理由 |
|---|---|
| SSE 而非 WebSocket | 单向推送足够，HTTP 语义简单，易代理 |
| 规则先内置三档，不做通用规则引擎 | YAGNI，先验证价值 |

## 风险

- trace 每调用一条事件会放大流量，需要采样与合并。

## 依赖

- `console-data-layer`（客户端侧）
- 托盘归属结论（`mygo-shell-spike`）

## 相关资源

- `internal/observability/recorder.go`、`internal/httpx/server.go`

---

## Status 更新日志

- **2026-10-29**: 创建
