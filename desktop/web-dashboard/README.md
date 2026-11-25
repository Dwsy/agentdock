# AgentDock 远程控制台

用你 GitHub 最新收藏的组件库 [Solid Glass](https://github.com/deeplethe/solid-glass)（React 19 + Tailwind 4 的玻璃质感设计系统）搭的产品级只读监控台，远程查看本机 AgentDock 的运行状态。

## 页面

| 页面 | 内容 |
|---|---|
| 概览 | 服务/任务/调用/告警关键指标、14 天 Token 趋势、工具排行、进程健康、能力开关 |
| 调用分析 | 按调用完成时刻统计 5 分钟、1 小时、24 小时的调用量、失败率、耗时分位数、输入/输出 Token、代码增删。可按工具、来源、User-Agent、任务、错误和阶段查看分组，筛选结果同步更新趋势和最近 30 条明细。 |
| 实时事件 | SSE 订阅 AgentDock 事件总线，实时显示调用/任务/发布物事件，支持筛选、暂停、清空 |
| 调用追踪 | 左右布局：左侧可在「按时间」与「按项目」之间切换——按项目时用调用的路径信号（命令工作目录 `command.workdir`，其次文件变更的 `project_root`）分组，同名路径用多段后缀区分、无路径的落「未分类」并排最后，组头聚合次数/失败数/平均耗时；两种视图共用工具分类 chips + 请求头分组（按上游客户端头/本地 UA 聚合次数、失败数、平均耗时，点分组即按该值筛选）+ 成功/失败筛选 + 搜索，右侧单条详情；详情走 `/traces/detail` 接口，命令类展示完整命令/工作目录/stdout/stderr，`read_file` 渲染带行号的正文（超预览上限时走全文旁路），`view_image` 直接显示图片（字节走 `/traces/image` sidecar），其余结果里的 markdown 可切换渲染 |
| 文件变更 | 左右布局：左侧可在「目录树」（按路径层级）与「按项目」（按任务项目路径分组，未匹配的落「未分类」）之间切换，右侧详情；diff 支持「左右对比 / 统一视图」切换，`.md` 文件额外提供 Markdown 渲染标签 |
| 任务与告警 | 点任务卡片进入独立详情页：目标 / 步骤 / 完成条件 / 事件时间线 / 摘要 + 关联调用与文件变更（diff 可展开）；列表按状态筛选，另附阻塞告警 |
| 文件发布 | 查看有效发布产物，复制/打开公网签名链接、延长有效期与撤销；支持本机路径（文件或目录 `.tar.gz`）与单文件上传（≤ 20 MiB）主动发布；右侧支持常见格式在线预览（图片、SVG、Markdown、JSON/JSONL、CSV/TSV 表格、代码/文本、PDF、音视频）与本机下载 |
| 能力目录 | 工具/规则/已装 Skill/共享 Skill 四个标签页，支持搜索 |

顶部每 10 秒自动刷新一次快照，实时事件走独立 SSE 长连接。可手动刷新；主题按钮在「跟随系统 → 浅色 → 深色」间循环，存的是用户意图（`agentdock-theme-mode`），跟随系统时随系统深浅色实时切换，显式选定后不再被系统改动覆盖。页面脚本在首屏渲染前定好主题，无闪烁。监控视图保持只读；管理写操作仅限显式触发的配置编辑、文件发布管理与手动推理控制。

顶栏的「背景」可开关液态玻璃、应用 HTTP(S) 图片链接、选择本地图片或恢复内置山湖壁纸。背景设置保存在当前浏览器，本地图片保存在浏览器存储中。系统开启减少透明度时，控制台使用实色面板。

## 数据来源

统一走 `/dashboard/api/*`（而非直连 `/internal/runtime/*`），用到 status / overview / analytics / analytics/calls / alerts / tasks / traces / traces/detail / traces/image / file-changes / artifacts（含 `/artifacts/{id}/extend`、`/artifacts/{id}/content`）/ skills / plugins / mcp / catalog / config / manual-llm / events。

调用分析从当前 Core 进程内保留的最近 24 小时、最多 20,000 条调用元数据聚合。重启或容量淘汰后，页面会标出实际覆盖时段。Token 以工具入参和结果估算，概览中的历史按日累计继续从已有统计读取。

- **生产**：AgentDock Go core 用 go:embed 把本目录 `dist/` 打进二进制，托盘菜单打开 `http://127.0.0.1:<port>/dashboard/`。`/dashboard/api/*` 由 core 做 loopback-only 反向代理，内部注入 token 转发到 `/internal/runtime/*`。
- **开发**：Vite dev server 的中间件把 `/dashboard/api/*` 代理到本机 core，token 从 `agentdock.env` 读取后注入。

## 测试

纯函数逻辑用 Node 自带 runner 跑，不需要额外依赖：

```bash
node --test src/lib/*.test.ts
```

## 运行

```bash
pnpm install
pnpm dev          # http://localhost:5173（开发模式，需本机 core 在跑）
```

生产构建（由 `packaging/macos/build-dashboard.sh` 或 `make dashboard` 调用）：

```bash
pnpm build        # tsc + vite build，产出 dist/，base 为 /dashboard/
```

`dist/` 会被 `build-dashboard.sh` 同步到 `internal/httpx/dashboard/dist/`，再由 Go 的 `go:embed` 打进 core 二进制。该 embed 产物**不入库**（哈希文件名每次构建都变），仓库只留 `dist/.gitkeep` 占位，所以裸 clone 后要先跑一次 `make dashboard` 才有控制台；前端改动后同样跑它刷新（build-local.sh 和 release workflow 已自动串联）。

## 渲染实现

界面渲染参考 agentdock 自带的 macOS Swift 版（`desktop/macos/AgentDockApp/Sources/TraceRenderView.swift` / `TraceResultCards.swift`）的信息架构：完整命令分区、任务卡的目标/步骤/事件分区、轻量语法高亮。Web 版用 React 组件实现：

- `DiffViewer` — unified diff 解析成结构化行，左右两列按 del/add 块对齐，可切统一视图
- `MarkdownView` — react-markdown + remark-gfm + rehype-highlight，渲染 `.md` 变更后的正文
- `CommandBlock` — 命令原文 + 工作目录 + session + stdout/stderr，带行号和 shell 高亮
- `TraceDetail` — 走详情接口拉单条调用全文，与左侧列表联动
- `FileTree` — 从变更路径构建层级树，支持展开折叠
- `ArtifactPreview` — 已发布文件多格式在线预览（图片/SVG/Markdown/JSON/CSV 表格/代码带行号/PDF/音视频，超上限范围切片与二进制保护）
- `TaskDetailPage` — 任务详情页，目标/步骤/条件/事件时间线 + 关联调用与文件变更

Markdown 渲染器通过 `React.lazy` 按需加载：首屏主包 ~298KB（gzip 88KB），markdown 块 349KB 只在打开相关页面时才拉取。

## 健壮性

AgentDock 在跑大任务时 runtime API 会间歇性变慢甚至超时。控制台做了容错：

- 代理层和前端请求都有超时（30s / 25s），不会无限挂起
- 概览数据用 `Promise.allSettled` 逐接口拉取，个别失败只显示局部提示，保留上次成功的数据，不再整页卡死
- 任务详情用列表项做兜底，详情/活动接口失败时仍能渲染主体，并可一键重试
- 修复了 SSE 流中途断开时代理重复写响应头导致 dev server 崩溃的问题

## 鉴权与安全

```
浏览器 → /dashboard/api/* → (core loopback-only 代理，注入 token) → /internal/runtime/*
```

- 生产由 Go core 的 `internal/httpx/dashboard.go` 做代理；开发由 Vite 中间件做同形代理。
- `/dashboard/api/*` 只接受本机直连请求（复用 core 的 `isDirectLoopbackRequest`，拒绝跨域代理头），token 在服务端注入，**不写进前端代码或构建产物**。
- 直连 `/internal/runtime/*` 仍需 Bearer，鉴权边界没有被削弱。
- AgentDock 的 network capture 会把 `Authorization` 等请求头原文记进记录里。代理层对所有非 SSE 响应 JSON 做递归脱敏（Authorization/Cookie/Set-Cookie/token/password 等），避免凭据暴露到浏览器。
- SSE 事件流是摘要投影，不含参数或路径，代理层流式透传。
- 静态页带 CSP（default-src 'self'）、nosniff、no-referrer；SPA 路由回退 index.html；不用 `http.FileServer` 以避免 `/dashboard/` 的 301 跳转循环。

开发代理可用环境变量覆盖：`AGENTDOCK_ENV_FILE` / `AGENTDOCK_HOST` / `AGENTDOCK_PORT` / `AGENTDOCK_AUTH_TOKEN`。生产 token 直接用 core 的 `AGENTDOCK_AUTH_TOKEN`。

## 远程访问

dev server 监听 `0.0.0.0:5173`，同网段可通过 `http://<本机IP>:5173` 访问（已有多网卡地址显示在启动日志里）。生产环境建议在前面套一层反向代理 + HTTPS + 访问控制，不要裸奔到公网。
