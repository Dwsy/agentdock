# Web 控制台远程访问登录

> 状态：已实现（代码 + 测试同仓库）。替代“SSH 隧道才能打开 dashboard”的临时状态。
>
> 改动版本：无版本特判；所有行为由 `AGENTDOCK_REMOTE_AUTH_*` 控制，默认关闭。

## 1. 问题

`/dashboard/` 是调用追踪、任务、Skill、网络面板的只读控制台。它此前只能用 SSH 隧道访问：

```text
Mac localhost:18865 ──SSH──> 115 服务器 127.0.0.1:8765/dashboard
```

原因不是缺少登录方案，而是 `/dashboard/api/*` 代理只接受**本机直连**：

- `isDirectLoopbackRequest`：`RemoteAddr` 必须是回环、`Host` 必须是回环、不得携带任何 `X-Forwarded-*` / `CF-*` 代理头、`Origin` 必须同源。
- dashboard API 还要求 `AGENTDOCK_AUTH_TOKEN` 非空（`503 AUTH_TOKEN_REQUIRED`）。

cloudflared / frp / nginx 反代必然带代理头和非回环 Host，于是全部 403。SSH 隧道能绕过，只是**把反代特征变成了回环特征**，不是“远程访问登录”。

## 2. 方案选型

### 候选

| 方案 | 结论 |
| --- | --- |
| 放宽回环判断（信任 `CF-Connecting-IP` / `X-Forwarded-For`） | 拒绝。等于没有认证：任何能连到端口的客户端都能伪造这些头。 |
| HTTP Basic（浏览器弹原生框） | 拒绝。没有登出、没有 TTL、每个请求都回放口令，且无法与“只读会话”边界配合。 |
| 复用现有 OAuth 授权码流程给 dashboard | 拒绝。为 MCP 客户端设计的动态注册 + PKCE + redirect_uri 流程，让人在浏览器里走一遍是过度设计，且会给公网新增 token 端点。 |
| 口令登录页 + HMAC 签名会话 Cookie（**选中**） | 无服务端会话表，Core 重启不掉线；作用域可精确限定到 `/dashboard`；登出即删 Cookie。 |

### 实现结构

```
浏览器 ──GET /dashboard/─────────────┐
        └─无会话─► 303 /dashboard/login?next=…
                     │
        口令（表单提交） ───┴─► Set-Cookie agentdock_session=<HMAC 会话>
                     │
        ──GET /dashboard/api/status──┐
                                     ▼
                        dashboardAPIHandler（代理层）
                        1. 会话 Cookie 校验（含 Origin / Sec-Fetch-Site）
                        2. 会话不得写 /remote/*、/config/restart
                        3. 改写成“本机直连形态”：Host→127.0.0.1:port，
                           剥 Origin / Cookie / Referer / 代理头
                        4. 注入 Authorization: Bearer <AGENTDOCK_AUTH_TOKEN>
                                     │
                                     ▼
                        runtimeAPIHandler（一行未改）
```

关键点：**`internal/httpx/runtime_api.go` 一行都没改。** 回环约束、DNS rebinding 防御、JSON Content-Type 强制、OAuth 分支全部保留原样。远程会话在代理出一个“本机直连”的请求，仅此而已。

### 两把钥匙

| 凭证 | 使用者 | 存放 | 可见性 |
| --- | --- | --- | --- |
| `AGENTDOCK_REMOTE_AUTH_PASSWORD` | 人 | env / env-file | 只在服务端比较，永不回显 |
| `AGENTDOCK_AUTH_TOKEN` | 机器（代理） | env / env-file | 从不进浏览器、不进响应体 |

会话 Cookie 只有 `agentdock_session`，浏览器 JS 拿不到 Bearer；代理对 JSON 响应继续做既有脱敏（`sanitizeDashboardJSON`）。

## 3. 改动文件清单

| 文件 | 改动 |
| --- | --- |
| `internal/auth/console_session.go` | 新增。HMAC-SHA256 会话签发/校验；`session.key` 原子创建（硬链接不覆盖），0600/0700。 |
| `internal/auth/console_session_test.go` | 新增。签名、篡改、过期、缩短 TTL 后旧 Cookie 失效、密钥落盘复用与损坏拒绝。 |
| `internal/config/config.go` | 新增 `RemoteAuthEnabled/Password/SessionTTLSeconds/PublicURL` 与 `validateRemoteAuth()`；`ValidateAuth()` 调用它。 |
| `internal/config/config_test.go` | 新增启用条件、口令强度/空白、机器凭证缺失、公网 URL 校验、默认 TTL。 |
| `internal/httpx/dashboard_login.go` | 新增。登录/登出路由、限速、`next` 白名单、Cookie 属性、登录页渲染。 |
| `internal/httpx/dashboard_login_page.html` | 新增。自包含登录页（无脚本、无外部资源）。 |
| `internal/httpx/dashboard_session.go` | 新增。会话构造、授权判定、Origin 校验、会话写入边界。 |
| `internal/httpx/dashboard.go` | 代理层接受会话请求；会话请求改写成回环形态；静态页未登录跳登录路由；`registerDashboard` 增加 `console` 参数。 |
| `internal/httpx/server.go` | 构造会话（未启用返回 nil），启动日志打印控制台登录配置。 |
| `internal/httpx/dashboard_login_test.go` | 新增。见第 6 节覆盖表。 |
| `internal/httpx/dashboard_test.go` | 仅适配 `registerDashboard` 新签名。 |
| `.env.example` | 新增远程登录示例配置。 |

未改：`internal/httpx/runtime_api.go`、`internal/auth/oauth*.go`、`internal/mcp/*`、任何前端源码（`desktop/web-dashboard` 零改动）。

## 4. 配置

全部走 `AGENTDOCK_*` 环境变量 / `--env-file`：

| 变量 | 必填 | 默认 | 说明 |
| --- | --- | --- | --- |
| `AGENTDOCK_REMOTE_AUTH_ENABLED` | 开启时 `true` | `false` | 总开关。默认关闭，未开启时行为与改动前完全一致。 |
| `AGENTDOCK_REMOTE_AUTH_PASSWORD` | 开启时必填 | — | 登录口令，≥12 字符，禁止首尾空白。 |
| `AGENTDOCK_AUTH_TOKEN` | 开启时必填 | — | 机器凭证，代理注入 runtime API。dashboard 本来就要求它。 |
| `AGENTDOCK_REMOTE_AUTH_PUBLIC_URL` | 建议 | 空 | 公网 Origin（`https://…`）。用于同源 Origin 校验与 `Secure` Cookie 判定。 |
| `AGENTDOCK_REMOTE_AUTH_SESSION_TTL` | 否 | `12h` | 会话有效期，1m–720h，不支持 `never`。 |
| `AGENTDOCK_TRUSTED_PROXY_CIDRS` | 建议 | 空 | 反向代理在本机时配置，登录限速才能按真实客户端 IP 计数。 |

启动期 `ValidateAuth()` fail-fast，配置错误直接拒绝启动：

- 开了 `ENABLED` 却没口令 → 报 `AGENTDOCK_REMOTE_AUTH_PASSWORD`
- 设了口令却没开 `ENABLED` → 报 `AGENTDOCK_REMOTE_AUTH_ENABLED`（避免“以为开了”）
- 没有 `AGENTDOCK_AUTH_TOKEN` → 报缺机器凭证
- 公网 URL 非 https（非回环）→ 报错

## 5. 安全边界

**默认关闭。** 未设置 `AGENTDOCK_REMOTE_AUTH_ENABLED` 时：

- `/dashboard/*` 只允许本机直连（`isDirectLoopbackRequest` 原样）
- 静态页不会跳登录页（老行为）
- 不生成 `session.key`，不写任何磁盘文件

**会话作用域只到 `/dashboard`。**

- Cookie：`Path=/dashboard`、host-only（无 `Domain`）、`HttpOnly`、`SameSite=Strict`、按需 `Secure`。
- 代理放行后剥掉 `Cookie`，`/internal/runtime/*` 与 `/mcp` 完全看不到它。
- 会话 Cookie 打到 `/internal/runtime/status` 无效（实测 401）。

**会话不能改自己的入口。** 非 GET 请求命中 `/internal/runtime/remote` 与 `/internal/runtime/remote/*`、`/internal/runtime/config/restart` 时返回 `403 SESSION_SCOPE_DENIED`。理由：令牌轮换 / Nexus 配对 / 隧道配置 / 服务重启一旦被口令泄漏利用，设备就永久交出去了。只读的 `GET /remote` 仍放行，远程页能显示在线状态。本机桌面 App 的写能力不变。

**登录面。**

- 只接受 `POST` + `application/x-www-form-urlencoded`，请求体上限 4 KiB。
- 每源 IP 5 分钟 10 次（`fixedWindowLimiter`），超限 `429` + `Retry-After`。
- 口令用 `subtle.ConstantTimeCompare`，失败只记来源 IP，不记口令。
- `next` 只接受 `/dashboard/…` 内部路径；`//evil`、绝对 URL、`/dashboard/../x`（`http.Redirect` 会清理成 `/x`）一律回落到 `/dashboard/`。
- 登录页 `CSP: default-src 'none'`、无脚本、无外部资源、`Cache-Control: no-store`。

**会话密钥。** `<AGENTDOCK_HOME>/remote-auth/session.key`，32 字节随机，`0600`，目录 `0700`，硬链接原子发布。删除该文件 = 立刻吊销全部会话（无服务端会话表，这是唯一的吊销手段）。密钥独立随机，**不从口令派生**：Cookie 载荷是明文，从口令派生的 HMAC 会让截获的 Cookie 变成离线撞库样本。

**纵深防御。** `Sec-Fetch-Site: cross-site` 与不同源 `Origin` 一律 401。Origin 校验同时接受 `r.Host` 与配置的公网 URL 主机名——cloudflared quick tunnel 会改写 Host，只认 Host 会把正常用户挡在外面。

**反向代理必须留下痕迹。** 本机回环是允许免登录的（桌面、托盘、SSH 隧道都靠它）。因此如果反代既不保留真实 `Host`，也不设置任何 `X-Forwarded-*` / `CF-*` 头，它的流量在 Core 看来就与本机直连完全相同，会绕过登录。约束很直接：反代要么保留真实 Host（`proxy_set_header Host $host`），要么必须设 `X-Forwarded-*`。cloudflared / frp / Caddy 默认就满足。

## 6. 测试覆盖

`go test ./internal/httpx ./internal/auth ./internal/config` 全绿（`make check` 亦通过：fmt / test / test-scripts / vet / build）。

| 用例 | 断言 |
| --- | --- |
| `TestDashboardLoginIssuesSessionCookie` | 303、`next` 生效、Cookie `HttpOnly`/`Secure`/`Strict`/`Path`/host-only/TTL |
| `TestDashboardSessionReachesProtectedAPI` | 未登录 API 401；静态页 303 到登录页；登录后 API 200 且 JSON `ok`；静态页带原 CSP；响应体不含机器 token |
| `TestDashboardLoginRejectsWrongPassword` | 401、不发 Cookie、不回显口令 |
| `TestDashboardLoginRateLimitsAttempts` | 10 次失败后第 11 次 429 + `Retry-After` |
| `TestDashboardSessionRejectsBadCookieAndOrigin` | 过期 / 异键 / 篡改 / 跨站 / 异源 / 带路径 Origin 全 401；公网同源 200 |
| `TestDashboardSessionCannotManageRemoteAccessOrRestart` | 4 个写端点 `403 SESSION_SCOPE_DENIED`；`GET /remote` 仍 200 |
| `TestDashboardLoopbackAccessSurvivesRemoteLogin` | 启用远程登录后本机 loopback 仍 200（不被新逻辑破坏） |
| `TestDashboardSessionLoginDisabledKeepsLoopbackBoundary` | 未启用时远端仍 403 `LOCAL_ACCESS_REQUIRED`，静态页不老跳登录 |
| `TestDashboardLoginNextCannotEscapeConsole` | 5 种逃逸输入全回落到 `/dashboard/` |
| `TestDashboardLoginPageIsSelfContained` | 无 `<script>`、无 http(s) 链接、`default-src 'none'`、不发 Cookie、不落 SPA |
| `TestNewDashboardConsoleSessionHonoursEnableFlag` | 开关开/关的会话构造与 TTL |
| `TestConsoleSession*`（auth） | 签发/校验/篡改/缩短 TTL/禁用态/密钥复用/损坏拒绝 |

## 7. 取舍记录

1. **走登录页而不是纯 Bearer。** 纯 `?token=` 或 Basic 无法登出、无法 TTL、会进浏览器历史与 Referer。代价是多一个 HTML 页面（约 190 行自包含）。
2. **会话不存服务端。** 换来“重启不掉线、无清理逻辑”，失去“吊销单个会话”。补偿：换 `session.key` 吊销全部；`Path=/dashboard` 让别的端点拿不到。
3. **口令与机器 token 分开。** 人类会话永远走脱敏代理，拿不到 Bearer。代价是开启远程登录要求 `AGENTDOCK_AUTH_TOKEN` 已配置——但 dashboard 无 token 本来就 503，不是新增门槛。
4. **会话禁写 `/remote/*` 与重启。** 见第 5 节。取舍是“远程不能换自己的令牌”，需要改令牌时 SSH 进本机或用桌面 App。
5. **不改前端。** dashboard 前端继续请求 `/dashboard/api/*`，Cookie 由浏览器自动携带。若将来要显示“远程会话”角标或是退出按钮，再单独开 issue。
6. **不做版本号特判、不做迁移。** 新能力纯增量，灰度只需改 env。

## 8. 115 服务器配置与验证

线上形态：`systemd` 服务 `agentdock.service`，二进制 `/opt/agentdock/agentdock`，工作目录（`AGENTDOCK_HOME`）`/var/lib/agentdock`。Core 只听回环 `127.0.0.1:8765`，cloudflared 在本机终止 TLS 后以明文反代过去。

### 8.1 前置

1. 公网入口已存在：cloudflared 固定域名（或 nginx / frp）把 `https://<域名>` 指向 `127.0.0.1:8765`。反代必须保留 `Host`，并设置 `X-Forwarded-Proto: https`。
2. `AGENTDOCK_AUTH_TOKEN` 已配置（原本 dashboard 就要求它）。没有就先生成：

```bash
openssl rand -hex 32
```

### 8.2 写入配置

先问服务要真实路径，不要猜（安装脚本默认布局是 `/opt/agentdock` + `/etc/agentdock/agentdock.env` + `/srv/agentdock/.agentdock`，115 是自定义布局）：

```bash
systemctl cat agentdock.service | grep -E 'EnvironmentFile|Environment=|ExecStart|WorkingDirectory'
sudo systemctl show agentdock.service -p EnvironmentFiles -p ExecStart
```

把下面的行追加到 `EnvironmentFile` 指向的那个文件（没有就追加到 unit 的 `Environment=` 源文件；权限 `600`）：

```bash
sudo tee -a <上面查到的 env 文件> >/dev/null <<'EOF'

# Web 控制台远程登录
AGENTDOCK_REMOTE_AUTH_ENABLED=true
AGENTDOCK_REMOTE_AUTH_PASSWORD=<至少 12 字符的口令>
AGENTDOCK_REMOTE_AUTH_PUBLIC_URL=https://<你的域名>
AGENTDOCK_REMOTE_AUTH_SESSION_TTL=12h
# 反代在本机：登录限速按真实客户端 IP 计数（否则所有人共用一个 127.0.0.1 配额）
AGENTDOCK_TRUSTED_PROXY_CIDRS=127.0.0.1/32,::1/128
EOF
sudo chmod 600 <同一个 env 文件>
```

口令走 env 文件而不是 systemd 的 `Environment=` 行（后者会进 `systemctl show` 输出）。

### 8.3 重启并确认

```bash
sudo systemctl restart agentdock.service
sudo journalctl -u agentdock.service -n 30 --no-pager | grep -E "console login|error|listen"
```

期望看到：

```text
"msg":"console login enabled","path":"/dashboard/login","public_url":"https://<域名>","session_ttl":"12h0m0s"
```

若报错，配置校验在启动期就会失败（`validateRemoteAuth`），日志里是明确的环境变量名。

### 8.4 验证清单

在**外部网络**的浏览器（先不用 SSH 隧道）：

```bash
BASE=https://<你的域名>
# 1. 未登录：API 401，静态页 303 到登录页
curl -sS -o /dev/null -w '%{http_code}\n' "$BASE/dashboard/api/status"          # 401
curl -sS -o /dev/null -w '%{http_code} %{redirect_url}\n' "$BASE/dashboard/"   # 303 …/dashboard/login?next=…
# 2. 错误口令：401
curl -sS -o /dev/null -w '%{http_code}\n' -H 'Content-Type: application/x-www-form-urlencoded' \
  --data-urlencode 'password=wrong' "$BASE/dashboard/login"                     # 401
# 3. 正确口令：303 + Set-Cookie agentdock_session（HttpOnly Secure SameSite=Strict）
curl -sS -D - -o /dev/null -H 'Content-Type: application/x-www-form-urlencoded' \
  --data-urlencode 'password=<口令>' "$BASE/dashboard/login" | grep -i set-cookie
# 4. 带会话 Cookie：200
curl -sS -o /dev/null -w '%{http_code}\n' -b 'agentdock_session=<上一步的值>' \
  "$BASE/dashboard/api/status"                                                 # 200
# 5. 会话不能改令牌：403
curl -sS -o /dev/null -w '%{http_code}\n' -b 'agentdock_session=<值>' \
  -H 'Content-Type: application/json' -d '{}' "$BASE/dashboard/api/remote/token" # 403
# 6. 本机回环未受影响（服务器上执行）
curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8765/dashboard/api/status  # 200
```

浏览器侧：

1. 打开 `https://<域名>/dashboard/` → 落到登录页。
2. 输入口令 → 进入控制台，各页数据正常（调用追踪 / 任务 / Skill / 网络）。
3. 刷新页面仍免登录；关闭浏览器后重开仍在 TTL 内。
4. 访问 `https://<域名>/dashboard/logout` → 回登录页，之后 API 401。
5. 设备地址栏确认 Cookie 有 Secure 标记（域名是 https）。

### 8.5 回滚

删掉 8.2 追加的字段并 `sudo systemctl restart agentdock.service`。立即回到“只允许本机直连 + SSH 隧道”的旧行为，无需回退二进制。

需要**立刻**吊销所有已登录会话（不改配置、不重启）：删除密钥文件后重启。密钥在 `$AGENTDOCK_HOME/remote-auth/session.key`，先确认它的位置：

```bash
sudo systemctl show agentdock.service -p Environment | tr ' ' '\n' | grep AGENTDOCK_HOME
# 没有 AGENTDOCK_HOME 时是服务用户的 ~/.agentdock
```

```bash
sudo mv /path/to/agentdock-home/remote-auth/session.key /path/to/agentdock-home/remote-auth/session.key.old
sudo systemctl restart agentdock.service   # 启动时重建新密钥
```

### 8.6 注意事项

- `AGENTDOCK_REMOTE_AUTH_PASSWORD` 一旦泄漏等价于设备读写权限（除 8 节所列写端点）。定期轮换：改 env → restart。
- 反代必须验证 `X-Forwarded-Proto: https`；否则 Cookie 不带 `Secure`，公网明文链路会把会话 Cookie 暴露。
- 反代必须保留真实 `Host` 或设置 `X-Forwarded-*`。两者都不做的话，流量在 Core 看来与本机直连一样，会绕过登录（见第 5 节）。
- 会话可以改运行配置（`editableConfigKeys` 白名单），但改动要重启 Core 才生效，而 `config/restart` 对会话是 403。
- 若反代改写了 `Host`（cloudflared quick tunnel 常见），`AGENTDOCK_REMOTE_AUTH_PUBLIC_URL` 必须填真实公网 Origin，否则同源校验会失效。
- 未启用远程登录时无需设置任何新变量，行为与旧版本一致。
