---
id: "2026-10-29-console-data-layer"
title: "Console 数据层：Runtime API 客户端与页面状态机"
status: "done"
created: "2026-10-29"
updated: "2026-10-29"
category: "跨平台UI"
tags: ["mygo", "runtime-api", "go", "contract"]
---

# Issue: Console 数据层：Runtime API 客户端与页面状态机

## Goal

把现有 Swift 侧（`ServiceController.swift`）的 Runtime API 访问与刷新状态机，抽成平台无关的 Go 包，供 MyGo Console 使用；不修改服务端契约。

## 背景

当前 18 个 runtime 端点、4 种轮询节奏（2.5s / 5s / 5s / 20s）都写在 Swift 里。直接照搬会让新 Console 重复实现超时、降级、节流与错误处理。

## 验收标准

- [ ] 新增 Go 包（建议 `console/runtimeclient`）覆盖只读端点：status / overview / analytics / catalog / skills / plugins / tasks / task detail / traces / file-changes / artifacts
- [ ] 统一超时、Bearer 注入、HTTP 错误到领域错误的映射
- [ ] 节流策略集中配置（目录 20s、trace/任务 5s、health 2.5s），页面不再各写循环
- [ ] JSON 解码有契约测试（与现有 `TestRuntimeTraceAPIsJSONShape` 同源字段）
- [ ] Core 未运行时返回可区分的「不可用」状态，不 panic、不阻塞 UI

## 实施阶段

### Phase 1: 客户端骨架
- [ ] 端点表 + 错误模型
- [ ] 本地回环校验（只允许 `127.0.0.1` / `::1`，与现有 Swift 一致）

### Phase 2: 状态机
- [ ] 刷新调度器（可按页面注册）
- [ ] 变更检测（仅数据变化时通知视图）

### Phase 3: 契约
- [ ] Go 测试覆盖每个 DTO 的关键字段
- [ ] 与 `internal/runtimeapi/dispatch_test.go` 的路径表对齐

## 关键决策

| 决策 | 理由 |
|---|---|
| 客户端放 Go，不放 MyGo 页面内 | 三平台共用，且可单测 |
| 先轮询，不先做 SSE | SSE 是独立 issue，数据层先保证可用 |
| 只读端点先做 | 写操作依赖功能 issue 与审批语义 |

## 依赖

- 无（可与 spike 并行）

## 相关资源

- 现有实现：`desktop/macos/AgentDockApp/Sources/ServiceController.swift`
- 契约：`internal/runtimeapi/dispatch.go`

---

## Status 更新日志

- **2026-10-29**: 创建
- **2026-10-29**: 状态补记 → done，备注: 经 `mygo-console-implementation` 交付（`desktop/console/internal/runtimeapi/` client + 契约测试）
