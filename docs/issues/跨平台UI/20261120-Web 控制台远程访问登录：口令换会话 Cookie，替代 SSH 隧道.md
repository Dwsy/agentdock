---
id: "2026-11-20-Web 控制台远程访问登录：口令换会话 Cookie，替代 SSH 隧道"
title: "Web 控制台远程访问登录：口令换会话 Cookie，替代 SSH 隧道"
status: "done"
created: "2026-11-20"
updated: "2026-11-20"
category: "跨平台UI"
tags: ["workhub", "web-dashboard", "remote-access", "auth", "session-cookie"]
---

# Issue: Web 控制台远程访问登录

## Goal

公网浏览器打开 `https://<域名>/dashboard/`，用口令登录后即可安全使用只读控制台，不再需要为每台客户端建 SSH 隧道。

## 背景/问题

`/dashboard/api/*` 代理要求 `isDirectLoopbackRequest`：回环对端、回环 Host、无任何代理头、同源 Origin。cloudflared / frp / nginx 必然违反这几条，因此公网反代一律 403；唯一可用路径是 SSH 隧道（把代理特征换成回环特征）。这不是“远程访问登录”。

## 验收标准 (Acceptance Criteria)

- [x] WHEN `AGENTDOCK_REMOTE_AUTH_ENABLED=true` 且配置合法，系统 SHALL 在 `GET /dashboard/` 未登录时 303 到 `/dashboard/login?next=`
- [x] WHEN 提交正确口令，系统 SHALL 下发 session Cookie（`Secure`/`SameSite=Strict`/`Path=/dashboard`/host-only），并跳回 `next` 指定页
- [x] WHEN 携带有效会话 Cookie 访问 `/dashboard/api/*`，系统 SHALL 返回 200 数据（含代理头与非回环 Host）
- [x] WHEN 会话 Cookie 过期、异键签名、被篡改，或 Origin 异源 / `Sec-Fetch-Site: cross-site`，系统 SHALL 返回 401 `SESSION_REQUIRED`
- [x] WHERE 未启用远程登录，`/dashboard/*` SHALL 维持仅本机直连（403 `LOCAL_ACCESS_REQUIRED`），静态页不跳登录
- [x] WHERE 会话访问 `POST /internal/runtime/remote/*` 或 `/internal/runtime/config/restart`，系统 SHALL 返回 403 `SESSION_SCOPE_DENIED`
- [x] IF 配置缺失/口令过短/未配 `AGENTDOCK_AUTH_TOKEN`/公网 URL 非 https，THEN 启动期 SHALL fail-fast 并指名环境变量
- [x] 本机 loopback 访问、`/mcp`、MCP OAuth 流程 SHALL 不受影响
- [x] `internal/httpx/runtime_api.go` SHALL 零改动

## 实施阶段

### Phase 1: 规划和准备
- [x] 阅读 `runtime_analytics_access.go` / `dashboard.go` / `auth` / `config` 确认真实边界
- [x] 比较放宽回环判断 / Basic / OAuth 复用 / 会话 Cookie 四种方案

### Phase 2: 执行
- [x] `internal/auth/console_session.go`：HMAC 会话 + `session.key` 原子创建
- [x] `internal/config`：`AGENTDOCK_REMOTE_AUTH_*` + `validateRemoteAuth`
- [x] `internal/httpx/dashboard_login.go`：登录/登出路由、限速、`next` 白名单、Cookie 属性
- [x] `internal/httpx/dashboard_session.go`：授权判定、Origin 校验、会话写入边界
- [x] `internal/httpx/dashboard.go`：代理层接受会话并改写成本机直连形态

### Phase 3: 验证
- [x] `internal/auth`、`internal/config`、`internal/httpx` 单元测试
- [x] `make check`（fmt / test / test-scripts / vet / build）
- [x] 真实二进制 + curl 端到端（tmux 起服务，13 项手工验证）

### Phase 4: 交付
- [x] `docs/dashboard-remote-access-login.md`
- [x] `.env.example` 示例
- [ ] PR（未提交，按要求仅本地改动）

## 关键决策

| 决策 | 理由 |
|------|------|
| 口令登录页 + HMAC 会话 Cookie，不用 Bearer / Basic / OAuth | 会话可登出、可 TTL、不进浏览器历史与 Referer；OAuth 授权码流程对浏览器人机交互过重 |
| 会话无服务端存储 | Core 重启不掉线、无过期清理；代价是只能靠换 `session.key` 全量吊销 |
| 登录口令与 `AGENTDOCK_AUTH_TOKEN` 分立 | 人类会话永远走脱敏代理，拿不到机器凭证；代价是开启远程登录要求已配置 token |
| 会话禁写 `/remote/*` 与 `config/restart` | 沿用既有“凭据写入与隧道配置不具备公网写能力”边界，防口令泄漏后自我锁死 |
| 不修改 `runtime_api.go` | 回环约束、DNS rebinding 防御、JSON Content-Type 强制全部保留；远程会话只在代理层改写成回环形态 |
| 不修改前端 | 前端继续请求 `/dashboard/api/*`，Cookie 由浏览器自动携带 |

## 遇到的错误

| 日期 | 错误 | 解决方案 |
|------|------|---------|
| 2026-11-20 | `next=/dashboard/../etc/passwd` 被 `http.Redirect` 清理成 `/etc/passwd`，逃出控制台 | `dashboardSafeNext` 用 `path.Clean` 比对，路径不规范一律回落到 `/dashboard/` |
| 2026-11-20 | 静态资源匿名请求也带 `next`，登录后会被送到 `.js` 文件 | `dashboardNextParam` 仅对 `Sec-Fetch-Dest` 为 `document`/`iframe`/`empty` 的导航带 `next` |
| 2026-11-20 | curl `-b cookies.txt` 未生效（`--resolve` 域名与 cookie 域不一致），误判会话失效 | 从响应头直接取 `Set-Cookie` 值手工带上，验证 200 |

## 相关资源

- [x] 设计说明: `docs/dashboard-remote-access-login.md`
- [x] 现有边界: `docs/runtime-access-and-recovery.md`（HTTP Runtime API 一节）
- [x] 相关 Issue: `docs/issues/跨平台UI/20261118-Web 仪表盘新增远程访问页…md`（凭据写入的同级信任边界）

## Notes

- 未部署到 115，未 git commit。115 配置与验证步骤见设计说明第 8 节。
- 前端若要显示“远程会话 / 退出登录”入口，需要单独 issue；本轮零前端改动。

---

## Status 更新日志

- **2026-11-20 13:20**: 状态变更 → done，备注: 代码 + 测试 + 设计说明完成，`make check` 全绿
