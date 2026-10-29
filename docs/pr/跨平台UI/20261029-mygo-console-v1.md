---
id: "2026-10-29-mygo-console-v1"
title: "MyGo 跨平台控制台 v1：数据层、自研组件库与 7 页只读 UI"
status: "done"
created: "2026-10-29"
updated: "2026-10-29"
category: "跨平台UI"
tags: ["mygo", "console", "cross-platform", "pr"]
---

# PR: MyGo 跨平台控制台 v1（数据层、自研组件库与 7 页只读 UI）

## 变更概述

新增 `desktop/console/` 独立 Go 模块：AgentDock Console 的跨平台 MyGo 版本
第一阶段（只读观测台）。不触碰主模块与既有三平台控制面板。

## 变更内容

### 模块与数据层（internal/runtimeapi）

- 新增 `types.go`：`Backend` 接口 + 11 个端点的 DTO，JSON 标签与 Core
  （observability/taskstate/publicartifacts/agentinstructions/tool/*）逐字段对齐。
- 新增 `client.go`：`Client` 实现 `Backend`，Bearer 鉴权、5s 超时、
  `ok:false` 与 4xx/5xx → `*APIError`（code/message/status_code），
  `LoadConfig()` 读 AGENTDOCK_* 环境变量与 agentdock.env。
- 契约测试 28 项（httptest）：解码、查询参数、鉴权头、错误映射、路径转义。

### 组件库决策与实现（internal/ui/kit）

- 决策（详见设计文档 §6）：**MyGo 内置 ui + 自研领域薄组件**，
  否决 onevra（0.x 快速迭代、上游补齐即移除、依赖面过大）与
  MujicaUI（宫廷哥特视觉调性与运维台不符）。
- 自研 10 个组件：StatusChip（语义色调）、SectionHeader、KV、MetricCard、
  MonospaceBlock、EmptyState/LoadingState/ErrorState 三态、DiffText
  （unified diff 逐行着色）、TruncateMiddle/FormatBytes/FormatDuration/FormatTime。
- 内嵌 12 个 Lucide 图标（ISC，注明出处），sync.Once 惰性解析缓存。
- 测试 42 项（ui.NewTester 无窗口渲染 + 纯函数表驱动）。

### UI 层（internal/ui、cmd/console）

- `app.go`：App 状态机（页面选择/缓存/loading/error/筛选/选中）、
  三组侧栏导航（观测/资产/工作）、goroutine 取数 + `Invalidate()`、
  页切换自动拉取 + 手动刷新。
- 7 页：概览（指标卡+任务四态+能力计数+服务信息）、能力目录
  （工具/规则/共享技能分组 + schema 详情）、任务（搜索+状态筛选+表格+详情）、
  文件变更（列表+DiffText）、发布（签名链接/未配置提示）、追踪
  （tool/source/status 筛选 + 入参出参 + UA + 文件变更）、统计
  （指标卡 + ToolStats 排序表 + 最近调用）。
- 每页三态齐全：LoadingState / ErrorState（含重试）/ EmptyState。
- `cmd/console/main.go`：1180x740 窗口装配；配置缺失降级默认地址。
- 测试 6 项（ui.NewTester）：默认渲染、导航、空态、错误态、能力目录。

### 文档

- 新增 `docs/architecture/20261029-mygo-console-uiux-design.md`：
  设计目标、布局线框、逐页组件映射、设计令牌、交互规范、
  组件来源决策表、可访问性、非目标。
- 新增 `docs/issues/跨平台UI/20261029-mygo-console-implementation.md`。

## 测试矩阵

| 检查 | 结果 |
|------|------|
| `go build ./...`（console 模块） | 通过 |
| `go vet ./...` | 通过 |
| `go test ./... -count=1` | 76 项全绿 |
| 交叉编译 darwin/arm64、windows/amd64、linux/amd64（CGO_ENABLED=0） | 通过 |
| 主模块 `go test ./...` 回归 | 全绿（未受影响） |

## 已知边界

- 只读页面；写操作依赖后端 issue（task-write-actions、artifact-revoke-extend）。
- 追踪页轮询、全局搜索、多设备视图为 P1/P2（设计文档 §8）。
- 真机窗口冒烟未做（约束：不启动 App）；ui.NewTester 为内存渲染验证。

## 关联

- ADR: `docs/architecture/20261029-mygo-cross-platform-ui-adr.md`
- 设计: `docs/architecture/20261029-mygo-console-uiux-design.md`
- Issue: `docs/issues/跨平台UI/20261029-mygo-console-implementation.md`
