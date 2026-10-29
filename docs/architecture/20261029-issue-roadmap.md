---
id: "2026-10-29-issue-roadmap"
title: "Issue 索引与执行顺序"
status: "active"
created: "2026-10-29"
updated: "2026-10-29"
tags: ["roadmap", "workhub"]
---

# Issue 索引

> 生成于 2026-10-29。所有条目均为**方案文档**，尚未实现。
> 决策依据见 `docs/architecture/20261029-mygo-cross-platform-ui-adr.md`。

## 1. 总览

```text
                     ┌───────────────────────────────┐
                     │ ADR: MyGo 跨平台（增量并存）    │
                     └───────────────┬───────────────┘
                                     │
        ┌────────────────────────────┼────────────────────────────┐
        ▼                            ▼                            ▼
  mygo-shell-spike          console-data-layer          task-trace-file-link ★
        │                            │                     （独立，收益最高）
        ├──────────────┐             │
        ▼              ▼             ▼
 system-integration  readonly-pages ──► write-pages
        │              │                 ▲
        │              ▼                 │
        │          packaging      task-write-actions
        │                         artifact-revoke-extend
        │                         trace-export-clear
        ▼
 events-and-alerts ◄── console-data-layer
 fleet-view      ◄── console-data-layer
 global-search   ◄── 无（独立）
```

★ = 建议第一个做。

## 2. 迁移（跨平台UI）

| # | Issue | 优先级 | 依赖 | 交付 |
|---|---|---|---|---|
| A1 | `20261029-mygo-shell-spike` | P0 | — | 托盘/多窗口/组件/性能结论 |
| A2 | `20261029-console-data-layer` | P0 | — | Go Runtime API 客户端 + 刷新状态机 |
| A3 | `20261029-console-readonly-pages` | P0 | A1, A2 | 6 页功能对等，三平台同源 |
| A4 | `20261029-console-write-pages` | P1 | A3, B2/B3/B4 | 受控写操作 UI |
| A5 | `20261029-console-system-integration` | P1 | A1 | 托盘/服务/权限边界 |
| A6 | `20261029-console-packaging` | P1 | A3 | 签名公证、三平台产物、更新通道 |

## 3. 功能缺口（桌面端）

| # | Issue | 优先级 | 说明 |
|---|---|---|---|
| B1 | `20261029-task-trace-file-link` | **P1（建议先做）** | task_id 贯穿 trace，任务详情关联调用与文件 |
| B2 | `20261029-task-write-actions` | P0 | checkpoint / complete / delete |
| B3 | `20261029-artifact-revoke-extend` | P0 | 撤销链接、延长有效期 |
| B4 | `20261029-trace-export-clear` | P1 | 导出 JSON/CSV、清空 |
| B5 | `20261029-global-search` | P1 | 跨 7 类数据搜索 |
| B6 | `20261029-events-and-alerts` | P2 | SSE + 阈值告警 |
| B7 | `20261029-fleet-view` | P2 | 多设备聚合视图（**暂缓**：本地 nexusbridge 只有单配对 identity，无节点注册表；真实 fleet 数据在远程 Nexus 侧，本环境无法验证，只写 mock 测试意义有限——待 Nexus 集成里程碑一起做） |

## 4. 推荐执行顺序

```text
第 1 步  B1 task_id 贯穿        ✅ 已交付（217a475）
第 2 步  B2 + B3 写操作闭环     ✅ 已交付（217a475）
第 3 步  A1+A2+A3 MyGo 只读台   ✅ 已交付（0033232）
第 4 步  B4/B5 导出/搜索        ✅ 已交付（217a475）
第 5 步  B6 事件流+告警         ✅ 已交付
第 6 步  B7 多机                ⏸ 暂缓（见表内说明）
```

## 5. 原则

- 迁移期（两个版本窗口）：macOS 原生壳只修 bug、不加功能；新功能只进 Console。
- 写操作一律走后端 API，UI 不直接改文件/状态。
- 任何新端点先加契约测试，再写 UI。
- 不做：文件回滚（trace 无 old content）、通用规则引擎、跨设备写。
