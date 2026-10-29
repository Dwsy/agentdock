---
id: "2026-10-29-runtime-events-alerts"
title: "运行时事件流（SSE）与阈值告警"
status: "done"
created: "2026-10-29"
updated: "2026-10-29"
category: "跨平台UI"
tags: ["sse", "alerts", "events", "console", "realtime", "pr"]
---

# PR: 运行时事件流（SSE）与阈值告警

## 变更概述

后端（主模块 internal/）新增事件总线与 SSE 订阅端点，把 call/task/artifact
生命周期事件实时推给控制台；新增三规则阈值告警端点（错误率 / trace 磁盘占用 /
任务阻塞），带去重与恢复计数。控制台（desktop/console/）接入 SSE 实时流
（断线指数退避重连 + 轮询降级），新增告警横幅、实时指示灯与概览页事件时间线；
顺带修复 v2 遗留的 TracesExport 响应包封解析契约。

## 后端（internal/）

### 事件总线（observability/events.go）

- `EventBus` 进程内发布/订阅：`Publish` 非阻塞广播，每订阅者有界缓冲
  （深度 64），缓冲满丢弃最旧事件并累加 dropped 计数（`Dropped()` 可查），
  慢消费者不会阻塞 recorder 热路径；`Subscribe/Unsubscribe` 断开即退订。
- 事件覆盖 call / task / artifact 生命周期，写操作与工具调用同样产生事件
  （source=internal 的既有记录语义不变）。

### SSE 端点（app/runtime_events.go + httpx/runtime_events.go）

- 路由：`GET /internal/runtime/events`（dispatch.go 转发至 app 处理器）。
- 帧格式：`event: <kind>\ndata: <json>\n\n`；每 15s 发 `event: ping\ndata: {}`
  保活；`Content-Type: text/event-stream`，逐帧 Flush。
- request context 取消即停止写流并自动退订（goroutine 不泄漏）。

### 阈值告警（app/runtime_alerts.go）

- 路由：`GET /internal/runtime/alerts`，惰性评估（GET 时计算，无常驻
  评估器 goroutine）。
- 三规则：
  | 规则 | 默认阈值 | 环境变量 |
  |------|----------|----------|
  | error_rate | 0.1 | `AGENTDOCK_ALERT_ERROR_RATE` |
  | trace_disk | 512 MB | `AGENTDOCK_ALERT_TRACE_DISK_MB` |
  | task_blocked | 24 h | `AGENTDOCK_ALERT_BLOCKED_TASK_H` |
- 去重：同 kind+key 仅保留一条，`first_seen` 保持稳定不刷新；
  恢复计数（已恢复条目单独计数）；severity 分级——error_rate 达
  2 倍阈值升级 `danger`，其余 `warning`。

## 控制台（desktop/console/）

- **SSE 客户端**（runtimeapi.OpenEvents/Alerts）：信封解析（`event: kind` +
  `data: json`），读取失败包装 `ErrSSEDisconnected` 供上层重连；流式会话
  走 `EventsOpener` 接口便于测试桩替换。
- **断线重连**（ui/realtime.go）：指数退避 1s→2s→…→30s 封顶，重连成功后
  全量刷新一次页面缓存。
- **轮询降级**：SSE 正常时追踪页停止 5s 轮询；断开/未接入时轮询兜底，
  观测不中断。
- **实时指示灯**：顶栏三态——"实时"（SSE 已连接）/ "重连中…"（连接后断开）/
  "轮询"（未接入 SSE）。
- **告警横幅**（ui/alerts.go）：severity 着色（warning/danger）、点击跳对应页、
  显示已恢复计数、无告警不渲染。
- **概览页事件时间线**：最近 10 条事件。
- **TracesExport 包封修复**：后端 `/internal/runtime/traces/export` 返回
  JSON 包封（`TracesExportEnvelope`，body 为导出原文字符串），v2 的客户端
  按原始流解析导致漂移；本次改为解析包封后再落盘/展示，契约测试对齐。

## 测试矩阵（2026-10-29 终验复跑）

| 检查项 | 命令/范围 | 结果 |
|--------|-----------|------|
| 主模块构建+静态检查 | `SHELL=/bin/bash go build ./... && go vet ./internal/...` | ✅ 通过 |
| 主模块测试 | `SHELL=/bin/bash go test ./... -count=1` | ✅ 全绿 |
| observability+app 测试 | `go test ./internal/observability/ ./internal/app/ -count=1` | ✅ 301（含子测试；顶层 Test 函数 200） |
| console 构建+静态检查 | `SHELL=/bin/bash go build ./... && go vet ./...` | ✅ 通过 |
| console 测试 | `SHELL=/bin/bash go test ./... -count=1` | ✅ 113 全绿（含子测试；顶层 Test 函数 85） |
| 交叉编译 | `GOOS=windows/linux/darwin`（amd64/arm64）`go build ./...` | ✅ 三平台通过 |
| 格式 | `gofmt -l`（internal/ cmd/ 与 console 模块） | ✅ 空输出 |

新测试覆盖：事件总线非阻塞/丢弃计数/订阅退订、SSE 帧格式与 ping、断开退订、
三规则阈值与环境变量覆盖、去重 first_seen 稳定、恢复计数、severity 升级、
SSE 客户端信封解析与断线错误、重连退避、横幅渲染/跳页、轮询降级、
TracesExport 包封解析。

## 已知边界

- **B7 多设备聚合视图暂缓**：本地 nexusbridge 只有单配对 identity、无节点
  注册表，真实 fleet 数据在远程 Nexus 侧，本环境无法验证（见路线图）。
- **无 TLS 远程订阅**：SSE 端点仅限本地 runtime API（loopback 语义），
  不做跨机 TLS 远程订阅；跨机实时需走远程 Nexus 通道，另行立项。

## 关联

- 前序 PR：`docs/pr/跨平台UI/20261029-mygo-console-v1.md`（只读台 v1）、
  `docs/pr/跨平台UI/20261029-mygo-console-v2.md`（写操作闭环 v2，本次修复其
  TracesExport 解析遗留）。
- Issue：`docs/issues/跨平台UI/20261029-runtime-events-alerts.md`
- 路线图：`docs/architecture/20261029-issue-roadmap.md`（B6）
