---
id: "2026-11-18-Web 仪表盘新增远程访问页：访问令牌轮换、Nexus 配对（含自建）、Cloudflare 隧道配置"
title: "Web 仪表盘新增远程访问页：访问令牌轮换、Nexus 配对（含自建）、Cloudflare 隧道配置"
status: "in-progress"
created: "2026-11-18"
updated: "2026-11-18"
category: "跨平台UI"
tags: ["workhub", "web-dashboard", "remote-access", "nexus", "cloudflare-tunnel"]
---

# Issue: Web 仪表盘新增远程访问页

## Goal

Web 仪表盘新增「远程访问」页，补齐桌面 App 已有但 Web 缺失的三块管理能力：AGENTDOCK_AUTH_TOKEN 轮换、Nexus 配对（官方或自建）、Cloudflare 隧道（none/quick/named）配置。

## 背景/问题

Web 仪表盘此前只有常规运行配置页（editableConfigKeys 白名单）；令牌、Nexus、隧道必须回桌面 App 或 CLI 配置。仪表盘 API 本就仅接受本机直连（隧道流量 403），把这三块搬进 Web 与桌面 App 同信任级，不扩大攻击面。

## 验收标准 (Acceptance Criteria)

- [x] WHEN 打开远程访问页，系统 SHALL 经 `GET /internal/runtime/remote` 展示 token 已配置与否、Nexus 配对元数据、隧道模式与公网地址，密钥值不回显
- [x] WHEN 提交新令牌（≥16 字符、无空白/控制字符），系统 SHALL 原子写入 `agentdock.env` 并保留未知键，返回重启提示
- [x] WHEN 提交 Nexus 地址与一次性配对码，系统 SHALL 经 `agentdock nexus pair` 子进程配对并返回 node_id/endpoint
- [x] WHEN 提交隧道模式（none/quick/named；named 需 Token+Origin），系统 SHALL 经 `agentdock tunnel configure` 子进程应用
- [x] WHERE 隧道流量访问 `/dashboard/api/*`，代理 SHALL 维持 403（本机直连边界不变）
- [x] IF 非法输入（短令牌、缺配对码、错误模式、named 缺 Token），系统 SHALL 返回 400 明确错误码
- [x] 密钥类写入 SHALL NOT 进入 `editableConfigKeys` 白名单（init 断言继续生效）

## 实施阶段

### Phase 1: 规划和准备
- [x] 摸清桌面端三条配置链路（env 写入 / nexus pair CLI / tunnel configure CLI）
- [x] 确认 dashboard 代理仅本机直连（isDirectLoopbackRequest 含代理头检查）
- [x] 确认无既有 rejected 提案

### Phase 2: 执行
- [x] `internal/app/runtime_remote_access.go`：GET 状态聚合 + token 轮换 + nexus pair/status + tunnel configure
- [x] `internal/runtimeapi`：`RemoteAccessRuntime` 可选接口、MethodAllowed/AllowHeader/Dispatch 路由
- [x] `internal/httpx`：路由注册、请求体放行、tunnel 两分钟超时预算
- [x] 前端：`api.ts` 类型与方法、`RemoteAccess.tsx` 页面、导航入口、Config 页脚注更新
- [x] `make dashboard` 重建并同步 embed dist

### Phase 3: 验证
- [x] `internal/app` 单测：token 校验/持久化/保留未知键、状态不回显密钥、tunnel/nexus 输入校验
- [x] `internal/runtimeapi` 单测：方法契约表 + 分发参数解码 + 不支持 runtime 报 REMOTE_UNSUPPORTED
- [x] `go vet` 零告警、`gofmt` 通过
- [ ] 本地重建安装后实测 GET/校验错误路径

### Phase 4: 交付
- [x] AGENTS.md 仪表盘契约段同步（只读→双写入页）
- [ ] 创建 PR（待用户确认提交）

## 关键决策

| 决策 | 理由 |
|------|------|
| 密钥写入走独立端点，不进 editableConfigKeys | 白名单 init 断言继续守住常规配置面；凭据写入是显式契约 |
| nexus/tunnel 经 CLI 子进程而非进程内调用 | app↔nexusbridge 存在导入环；且隧道写操作约定须由独立控制进程执行 |
| token 只报 configured、任何密钥不回显 | 延续既有安全红线（防截图/浏览器扩展泄露） |
| 隧道/nexus 配置 UI 的应用需用户显式重启 Core | 延续桌面端「保存不自动重启」约定 |

## 遇到的错误

| 日期 | 错误 | 解决方案 |
|------|------|---------|
| 2026-11-18 | app 导入 nexusbridge 报 import cycle（client.go 反向导入 app） | 改经 CLI 子进程 `nexus status --json` / `nexus pair`，契约镜像结构体解码 |
| 2026-11-18 | 单测 exec os.Executable() 套娃拉起整个测试套件（45s+ 挂死） | 抽 `execNexusStatusCommand` 执行缝，测试注入假响应 |

## 相关资源

- 相关代码: `internal/app/runtime_remote_access.go`、`internal/runtimeapi/dispatch.go`、`desktop/web-dashboard/src/pages/RemoteAccess.tsx`
- 相关文档: `AGENTS.md` 只读 Web 仪表盘契约段
- CLI 复用: `cmd/agentdock/command_nexus.go`、`internal/desktopruntime/tunnel_command.go`

## Notes

- 未做 nexus unpair（用户未要求，nexusbridge 亦无对应能力，需要时另立提案）。
- OAuth 设备密钥管理仍不在 Web 面（超出本次范围）。
- `scoped_env_test` 在本机 fish shell 下有两个环境性失败，`SHELL=/bin/zsh` 全绿，与本次改动无关。

---

## Status 更新日志

- **2026-11-18 14:40**: 状态变更 → in-progress，备注: 后端+前端实现完成，待本地实测与 PR
