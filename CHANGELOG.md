# Changelog / 变更日志

All notable changes to this project are documented here. / 本项目所有重要变更记录于此。

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); dates use `YYYY-MM-DD`.
格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，日期采用 `YYYY-MM-DD`。

## [Unreleased]

## [2026-11-18] - Runtime Stability, Performance, Artifact Management & Manual LLM / 运行态稳定性、性能治理、文件发布管理与手动推理桥接

### Added / 新增

- **Web 仪表盘「文件发布」管理与多格式在线预览**（`33ac829`）：新增文件发布页（`Published.tsx` / `ArtifactPreview.tsx`），支持查看有效发布物、复制/打开公网签名链接、延期与撤销，支持从控制面按本机路径（文件或目录 `.tar.gz`）或单文件上传（≤ 20 MiB）主动发布（`POST /internal/runtime/artifacts`），并通过内容流接口（`GET/HEAD /internal/runtime/artifacts/{id}/content`，支持 Range、CSP 与代理直通）提供图片、SVG、Markdown、JSON/JSONL、CSV/TSV 表格、代码/文本、PDF 与音视频在线预览及下载。
- **可开关的渐进式手动 LLM 桥接模式与 Token 估算切换**（`93bbae0`, `f62c1e5`, `3bf00a0`）：新增 `/v1/responses` 与 `/mcp/manual` 手动推理桥接队列、Nexus 远程控制同步、会话上下文缓存纠偏、图片内存预算控制、Codex `additional_tools` 兼容，以及 `AGENTDOCK_USAGE_COUNT_MODE`（`tiktoken` 默认 `cl100k_base` 与 `chars` 按字符数 `/ 4` 切换）与 Web 控制台「手动推理」页和持久化配置项。
- **NexusDock Workers 发行版与协议版本识别**（`b8fda92`）：`nexusbridge`、`agentdock nexus pair|status`、`evolution` 生命周期客户端与 Web 控制台支持解析并持久化 NexusDock Workers 版本标识（`edition=workers`、`protocol_version=2`、`contract_version=0.9.0`），透传 `tool.call` 上游请求头并兼容结构化 `409 LIFECYCLE_REVISION_CONFLICT` 错误。
- **调用追踪按项目分组**（`b8fda92`）：Web 仪表盘调用追踪页左侧新增「按时间 / 按项目」视图切换。按项目视图取每条调用自身的路径信号分组——优先命令工作目录 `command.workdir`，其次文件变更探测到的 `project_root`，因此没有关联任务的自由调用也能进组；组头聚合次数、失败数与平均耗时，同名不同父路径用最短可区分后缀命名，无路径信号的落「未分类」并固定排最后。分组只是展示投影，工具 chips、请求头分组、成功/失败与搜索的筛选语义不变。新增 `src/lib/traceProjects.ts` 与 6 个 `node:test` 用例。

### Changed / 调整

- **全链路请求取消透传与状态码细分**（`33ac829`）：`runtimeapi.Dispatch` 向 `/alerts`、`/overview`、`/tasks`、`/skills` 透传请求 `ctx`；`taskstate.Store` 改用可取消信号量锁并在文件遍历循环内响应取消退出，消除客户端超时重试导致的锁护航堆积；HTTP 层将 `context.Canceled` 映射为 `499 REQUEST_CANCELED`、`context.DeadlineExceeded` 映射为 `504 RUNTIME_API_TIMEOUT`，并抑制对端断连触发的 `broken pipe` 告警刷屏。
- **Web 仪表盘按需轮询**（`33ac829`）：`desktop/web-dashboard/src/App.tsx` 由固定每 10s 全量并发拉取全部接口改为页面可见性感知（后台标签页暂停）+ 在途防重入锁 + 仅拉取顶栏摘要与当前激活页所需接口，切换页面、恢复可见或收到发布物事件时立即补刷。
- **macOS 菜单栏健康检查解耦与去抖**（`33ac829`）：`AppDelegate.refreshStatus()` 增加在途单飞合并并先行发布 `ServiceStatus`（不再串行等待 `/internal/runtime/alerts`）；`ServiceController` 优先探测 `/healthz`（失败时 `300ms` 重试 1 次）以省去健康态 `launchctl print` 子进程，并加入 1 周期瞬时超时去抖防止高负载抖动误报「服务异常」；`RuntimeTrace*` 纯解码线模型收敛至 `ServiceController.swift`。

### Performance / 性能

- **追踪脱敏、Token 采样与仪表盘代理热路径（第一轮）**（`55a90bf`）：`RedactSecrets` / `redactTracePayload` 零分配快路径、大结果 JSON Token 采样外推、Trace 环形缓冲双缓冲滑动复用、`/dashboard/api/*` 代理流式快速脱敏、MCP `tools/call` 单次序列化及 `rg --json` 流式解析。
- **任务索引、Skill 扫描与工具内核热路径（第二轮）**（`33ac829`）：`taskstate.Store` 新增基于 `os.SameFile + Size + ModTime` 的增量快照缓存与零深拷贝 `OverviewCountsContext`；`TraceStore.DiskBytes` 增加目录 `.jsonl` 指纹短缓存；`RuntimeSkills` 合并重复 `Acquire`/`WalkDir` 并按包目录树指纹缓存摘要，`ParseSkillMetadata` 免除 Markdown 正文分配，`~/.agents/skills` 增加逐文件元数据缓存；`SafeTruncateString`、`DiffSections`、`sliceText`、`.gitignore` 目录级匹配缓存、`fd` 可执行路径缓存及 `InputValidator` 归一化参数快路径全面降低分配。

### Fixed / 修复

- 修复 `read_file` 工具 `sliceText` 在 `start_line > end_line` 时触发 `slice bounds out of range` Panic 的问题（`33ac829`）。

### Removed / 移除

- 移除旧版实验性 MyGo 桌面控制台代码，统一收敛至随 Core 分发的 Web 控制台与原生桌面端（`b8fda92`）。

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

## [2026-11-16] - Token Analytics, Image Previews & Theme Mode / Token 统计、图片预览与外观模式

### Added / 新增

- **Token 用量统计与 5 分钟滚动窗口**（`ce54c4e`, `a709727`）：调用分析增加输入/输出 Token 统计，活跃统计窗口改为 5 分钟滚动，并在 macOS 控制面板展示 Token 用量。
- **调用追踪图片预览**（`ce54c4e`, `a709727`）：支持保存并按需读取工具调用产生的图片 sidecar 预览（磁盘私有 `images/` 目录与内存有界环），macOS 追踪详情支持直接查看图片。
- **外观模式切换**（`c276f0f`）：控制台支持浅色、深色与跟随系统三种外观模式。

### Fixed / 修复

- `file_edit` 提供逐文件增删行数（`insertions`/`deletions`），补齐文件变更统计粒度（`0edb43d`）。
- macOS 菜单栏状态项徽标不再渲染告警数字，保持简洁状态提示（`327979b`）。
- 开发构建身份（`0.0.0-dev`）不再被组件目录兼容区间误拦截（`02890ac`）。

## [2026-10-30] - Observability, Network Capture & macOS Control Panel / 可观测性、网络抓包与 macOS 控制面板

### Added / 新增

- **HTTP 与 Nexus 桥接网络抓包**（`cc64c9a`, `3f5e70a`）：新增内存环形网络抓包存储（支持按状态/来源筛选、详情按需拉取与清空）及 macOS 网络抓包面板与追踪结果卡片。
- **运行时 SSE 事件流、阈值告警与写操作 API**（`07d3d9d`, `f27d6fa`）：新增 `/internal/runtime/events` 实时事件流、错误率/阻塞任务/Trace 磁盘阈值告警、任务状态写操作、公开文件发布撤销/延期、Trace JSON/CSV 导出与清空及 `task_id` 关联。
- **调用追踪增强与代码行数统计**（`9c889af`, `d1dd669`, `5849840`）：记录调用来源 `User-Agent`、`exec_command` 结构化入参/出参及旁路全文副本（`full/`），并在调用分析统计代码增删行数、精简列表摘要体积。
- **`list_dir` 支持 `fd` 引擎**（`a93225b`）：优先调用本机 `fd` 加速目录遍历并统一内核错误分类。
- **macOS 原生控制面板重构与扩展**（`d027eb2`, `f882bfa`, `d16320b`, `f6e2f37`, `f572d1c`, `761f552`, `c1af3bb`, `80d49bf`, `501818b`, `3f5e70a`）：新增能力目录、任务矩阵与一键交接复制、工具调用统计仪表盘、项目级文件变更树与 diff 预览、已发布文件与签名链接管理、全局搜索/分批加载/自动刷新，以及托盘运行状态、相对时间与告警徽标；收敛侧栏导航与页面视觉层级。
- **构建脚本、组件升级与治理文档**（`82fcfe2`, `efd3be6`, `43b80c7`, `cbe7e9b`, `9f4d125`）：新增 `packaging/macos/build-local.sh` 一键构建脚本与构建约束说明、仓库贡献者指南、Token 效率审计，并升级 `cloudflared` 组件至 `2026.10.4`（`#254`, `#255`）。

### Fixed / 修复

- 修复 macOS 控制面板 Trace 开关、插件详情展示、文件编辑历史补全与脚本治理注释变量边界（`c91de86`, `0e05a15`, `2263b8e`）。

## [v1.1.1] - 2026-10-25

### Added / 新增

- **Nexus 独立 WebSocket 入口与自动回退**（`58ebdba`）：支持配置独立 WebSocket 桥接地址并在连接异常时自动回退至主地址。

### Changed / 调整

- **统一 JSON Schema 契约校验**（`0c6f6de`, `#248`）：收敛工具输入/输出校验实现并保持向后兼容边界。
- **`task_create` 独占实时任务卡片**（`d126288`, `#250`）：避免非创建类任务操作重复触发 MCP Apps 实时任务卡片。
- **资源库备份与 CLI 参数精简**（`acf41f6`, `954a9c1`）：简化 Skill/Plugin 备份链路、统一 Skill 根目录语义并简化 CLI 运行目录参数。

### Fixed / 修复

- **Windows 升级后隧道恢复与页面布局**（`9edf8c5`, `d3d0539`, `76971ae`, `#251`, `#252`）：安全恢复升级前运行中的公网隧道、加固运行态恢复重连，并修复 Windows 桌面端页面左对齐与横向滚动。
- **资源库与 Linux 隧道权限**（`8107019`, `d90df96`, `bdb12bc`）：提前拒绝歧义 Skill 包、完整备份 Skill/Plugin 并复用原生恢复校验、修复 Linux `cloudflared` 可执行权限。

## [v1.1.0] - 2026-10-13

### Added / 新增

- **便携式 Skill 与 Plugin 设备间迁移工作流**（`49f0801`, `#230`）：提供带安全校验的跨设备 Skill/Plugin 导出与恢复链路。
- **追踪式自更新下载与 R2 保留期维护**（`a19d133`, `fdc68f0`, `b9dc4c2`, `aa8e4db`, `#228`）：自更新通过可追踪分发入口下载，并基于已存 Release 自动推导与清理过期 R2 资产。

### Performance / 性能

- **并行加载上下文索引**（`6635865`, `#233`）：并行拉取 Workflow 与 Recall 索引以降低 `agentdock_context` 延迟。

### Fixed / 修复

- 安装阶段对 Windows 托盘 shim 短暂文件锁冲突进行重试（`dd79d37`, `#236`）。
- 修复 macOS 预发布版本（RC）升级校验，隐藏窗口标题栏并统一收紧页面顶部间距（`dd493a7`, `2711395`, `#232`, `#234`）。
- 保持 Linux 下 Nexus 配对身份与 Core 服务主目录一致（`63be28a`, `#231`）。

## [v1.0.1] - 2026-10-07

### Fixed / 修复

- 修复 Windows 当前用户 App Runtime 注册与安装器升级状态交接（`2550000`, `80fe2d6`）。

## [v1.0.0] - 2026-10-06

### Added / 新增

- **`cloudflared` R2 镜像托管与组件统一解析**（`243f777`, `d7944e4`, `#218`, `#219`）：通过 R2 托管 `cloudflared` 组件并统一跨平台安装器解析与进度反馈（`#216`）。

### Changed / 调整

- **平台安装器接管桌面更新与 GitHub Release 资产精选**（`c4c4d1b`, `ff1a994`, `6ec94c3`, `#211`）：桌面端更新统一移交平台原生安装器仲裁，版本号由 Git Tag 派生，GitHub Release 精选面向用户的安装包与 `SHA256SUMS.txt`。

### Fixed / 修复

- 统一安装更新健康验证、对齐 macOS Quick Tunnel 生命周期与非交互暂存校验，修复 Windows 停止态主页加载速度、Core 运行状态识别及托盘更新反馈（`eabe7ad`, `f11b9e5`, `048c482`, `039d1ec`, `6635d92`, `96b1d8c`, `33bb388`, `#209`, `#212`–`#215`, `#217`）。

## [v0.9.0 ~ v0.9.2] - 2026-09-24 ~ 2026-10-06

### Added / 新增

- **Plugin Core 体系与主流适配器**（`9071556`, `b20dc72`, `e121e69`, `#130`, `#131`, `#134`）：建立插件内核、事务化生命周期、运行时快照隔离与只读 Runtime API。
- **Remote MCP OAuth 与交互式 MCP Apps 卡片**（`c361052`, `3d4d0af`, `9145981`, `ee163e8`, `#146`, `#152`, `#153`, `#156`）：原生支持远程 MCP Server OAuth 授权，新增 Workspace Context 卡片、`view_image` 图片确认卡片与聊天卡片渲染模式。

### Fixed / 修复

- 收敛 Skill 内容模型与旧布局更新事务迁移、保留完整路由 `description`、对齐本地浏览器启动超时，加固 Windows 冷启动健康预算、旧版迁移进程竞态与临时目录清理、ARM64 `VersionInfo` 构建，并提供 `v0.9.x` 到安装器更新桥接（`#127`–`#129`, `#132`, `#133`, `#135`–`#137`, `#145`, `#147`, `#148`, `#150`, `#151`, `#158`, `#161`, `#162`, `f17cb01`）。

## [v0.8.0 ~ v0.8.3] - 2026-08-27 ~ 2026-09-09

### Added / 新增

- **中央工具公共契约与 MCP Apps 资源中继**（`bdfb0ac`, `5ad5ac3`, `50a6987`, `9b2033d`）：消费 `agentdock-protocol` 中央工具契约，支持 Nexus 桥接 MCP Apps UI 资源中继与紧凑/移动端界面优化。
- **结构化 `agentdock_context` 与原生 Recall 引用**（`014cf40`, `64a2b94`, `6507bf1`）：支持本地与 Nexus 上下文聚合，Recall 搜索优先返回原生文件引用。

## [v0.5.0 ~ v0.7.7] - 2026-07-15 ~ 2026-08-22

### Added / 新增

- **原生 ACP 客户端与桌面端 Coding Agent 预设**（`#10`, `400197c`, `628cc14`）：引入桌面端 ACP 配置与多 Agent 预设。
- **跨平台图形安装、管理员增强核心与内置自更新**（`0bb9fb8`, `d2a0563`, `9e55bb2`, `4132475`）：建立内置自更新机制、macOS 图形安装与 Skill 拆分、Windows 双架构离线安装包与管理员增强模式，以及 Cloudflare Quick Tunnel 临时地址自动刷新。

## [v0.1.0 ~ v0.4.4] - 2026-07-03 ~ 2026-07-14

### Added / 新增

- **核心运行时与跨平台底座**：发布 AgentDock 初始版本，提供本地文件/命令/浏览器/任务 MCP 工具集、Nexus 远程节点配对与桥接、私密笔记接口、Runtime MCP 管理接口（`#3`）、Docker Hub 镜像发布及 Linux（systemd / Alpine OpenRC）轻量二进制安装脚本，采用 Apache-2.0 许可证。
