# Changelog / 变更日志

All notable changes to this project are documented here. / 本项目所有重要变更记录于此。

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); dates use `YYYY-MM-DD`.
格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，日期采用 `YYYY-MM-DD`。

## [Unreleased]

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

### Fixed / 修复

- 修复 SSE 事件流中途断开时代理重复写响应头导致 Vite dev server 崩溃的问题。
- 前端请求与代理加超时容错：概览数据用 `Promise.allSettled` 逐接口拉取，个别超时不再让整页卡死；任务详情用列表项兜底。
- 配置写入校验失败返回 400 `CONFIG_INVALID`；敏感 key 修改被拒。
