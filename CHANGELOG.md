# Changelog / 变更日志

All notable changes to this project are documented here. / 本项目所有重要变更记录于此。

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); dates use `YYYY-MM-DD`.
格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，日期采用 `YYYY-MM-DD`。

## [Unreleased]

### Added / 新增

- **调用追踪按项目分组**：Web 仪表盘调用追踪页左侧新增「按时间 / 按项目」视图切换。按项目视图取每条调用自身的路径信号分组——优先命令工作目录 `command.workdir`，其次文件变更探测到的 `project_root`，因此没有关联任务的自由调用也能进组；组头聚合次数、失败数与平均耗时，同名不同父路径用最短可区分后缀命名，无路径信号的落「未分类」并固定排最后。分组只是展示投影，工具 chips、请求头分组、成功/失败与搜索的筛选语义不变。新增 `src/lib/traceProjects.ts` 与 6 个 `node:test` 用例。

## [2026-11-17] - Web Dashboard / Web 控制台

### Added / 新增

- **只读 Web 仪表盘**（`desktop/web-dashboard/`）：React 19 + Tailwind 4 + Solid Glass 设计系统，六页控制台——概览、实时事件（SSE）、调用追踪、文件变更、任务与告警、能力目录。数据全部来自本机 Runtime API，只读观测。
- **嵌入 Core 分发**：仪表盘 dist 通过 `go:embed` 打进 Core 二进制（`internal/httpx/dashboard.go`），托盘右键菜单新增「打开 Web 仪表盘」，在浏览器打开 `http://127.0.0.1:<port>/dashboard/`。dist 随仓库提交，任何 `go build` 都自带控制台。
- **同形 API 代理**：`/dashboard/api/*` 在生产由 Core、在开发由 Vite 中间件代理到 `/internal/runtime/*`，服务端注入 token；对非 SSE 响应 JSON 递归脱敏（Authorization/Cookie/token 等），静态页带 CSP。
- **配置页**（查看 + 修改 + 一键重启）：`ConfigRuntime`（`internal/app/runtime_config.go`）提供 `GET/POST /internal/runtime/config` 与 `POST /internal/runtime/config/restart`。白名单 13 项可编辑配置，敏感项（token/OAuth/设备身份）不回显不可改；写入用 `envstore` + `atomicfile`（0600，保留未知 key）；重启改为异步触发，先回响应再 kickstart，避免切断 HTTP 连接。
- **调用追踪详情**：左右布局，按工具分类筛选 + 成功/失败 + 搜索；`file_edit` 用结构化 `file_changes` 渲染 unified diff（左右对比/统一视图切换），`.md` 结果可 Markdown 渲染，命令类展示完整命令/工作目录/stdout/stderr。
- **任务详情页**：点击任务卡片进入完整详情，展示目标/步骤/完成条件/事件时间线/摘要 + 关联调用与文件变更（diff 可展开）。
- **文件变更页**：左侧目录树、右侧详情，diff 左右对比 + Markdown 预览。
- **构建链路**：新增 `make dashboard` 与 `packaging/macos/build-dashboard.sh`（已登记脚本治理清单）；`build-local.sh` 与 release workflow 的 macOS job 在编译 Core 前先构建 dashboard；`AGENTS.md` 补充仪表盘与配置 API 契约。

### Changed / 调整

- **控制台外壳**：侧栏改为悬浮玻璃板（220px 宽、12–16px 外距、24px 圆角），顶栏降为 48px 单行工具栏并移除两行路径副标题；移动端侧栏变成带安全区的抽屉，支持 Esc / 遮罩关闭、关闭按钮与触发按钮间移焦。外壳复用 Solid Glass 变量，并加入按钮按下、侧栏滑入、任务页首次进入三处克制动效（`prefers-reduced-motion` 下禁用位移）。
- **任务页按项目分组**：桌面为 220–240px 项目导航 + 右侧列表，移动端改用项目 select；项目按 trim / 路径分隔符 / 尾斜杠归一化，slug 只在唯一对应完整路径时合并，同 basename 的多路径、worktree、review 与子目录保持独立。项目导航显示短名并在悬停时给出完整路径，重复 basename 使用最短可区分父路径后缀，空 project 归“未分类”且排在最后。
- **任务列表与筛选同源**：项目选择、搜索（标题/摘要/目标/原始 project）、状态筛选与计数走同一条派生链路；项目计数基于当前加载集合，选项状态覆盖 active / completed / blocked / pending 与未知原文兜底。四个统计卡改为紧凑分段，卡片展示真实当前步骤或步骤进度（无步骤不伪造百分比），已完成任务默认折叠但计数常驻。全局告警作为可折叠辅助区，明确与项目筛选无归属关联。
- **任务数据契约**：列表一次取 200 条，达到上限时提示“最近 200 条，非全量”；`Task` 类型补齐 `current_step`、`device`、`blocker` 等实际字段，`summary` 按后端 omitempty 语义改为可选；返回详情保留项目、搜索、状态与滚动位置。
- **纯逻辑测试**：新增 `src/lib/taskProjects.ts` 与 15 个 `node:test` 用例，覆盖 slug 歧义路径、同 basename 多路径、worktree/子目录、未分类、选择迁移、状态计数与筛选一致性、搜索字段与 200 条上限判定。
- 仪表盘 dist 通过 `make dashboard` 重新生成并同步到 `internal/httpx/dashboard/dist`，随 `go:embed` 进入 Core。

### Fixed / 修复

- 修复 SSE 事件流中途断开时代理重复写响应头导致 Vite dev server 崩溃的问题。
- 前端请求与代理加超时容错：概览数据用 `Promise.allSettled` 逐接口拉取，个别超时不再让整页卡死；任务详情用列表项兜底。
- 配置写入校验失败返回 400 `CONFIG_INVALID`；敏感 key 修改被拒。
