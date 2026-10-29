# Repository Guidelines

AgentDock 通过 MCP 把 ChatGPT / Claude / Codex 接到真实设备。改动前先读 `docs/` 中的设计基线，尤其是 `release-distribution.md`、`runtime-access-and-recovery.md`、`self-evolution-architecture.md`。

## Project Structure & Module Organization

```text
cmd/            agentdock(核心) · arbiter(更新仲裁) · shim/tray(Windows) · wsl-helper
internal/       app(ToolSpec 注册表 + Runtime) · tool/<capability> · mcp · httpx · auth
                acp · plugin · skill · taskstate · evolution · nexusbridge
                installer · updateengine · selfupdate · desktopruntime
core-skills/    随包分发的内置 Skill
desktop/        macOS(SwiftUI+AppKit) · Windows(WinUI3/.NET8 + Go tray)
scripts/        install/ 公开安装入口 · test/ 脚本与 UI 回归 · governance/ 脚本清单
```

- 一个 capability 一个包；测试与被测代码同目录（`*_test.go`）。
- 平台差异用构建标签文件（`*_windows.go`、`*_unix.go`、`*_other.go`），不要写运行时 `GOOS` 分支。
- 工具 Schema 归 capability 包或 `agentdock-protocol/mcpcontract` 所有，注册只改 `internal/app/specs_<capability>.go`。

## Build, Test, and Development Commands

| 命令 | 作用 |
| --- | --- |
| `make check` | CI 等价：gofmt 校验 → 单测 → 脚本治理 → vet → build |
| `make build` | 产出 `bin/agentdock`，带 version/commit ldflags |
| `make run` | 本地起服务，默认 `127.0.0.1:8765` |
| `go test ./internal/app -run TestName` | 单包定位调试 |
| `make race` | `go test -race ./...`，CI 在 Linux 执行 |
| `make test-scripts` | shell/PowerShell 语法与 `scripts/test` 治理测试 |
| `packaging/macos/build-local.sh [版本] [--build-only]` | 一键构建 macOS App：校验 → 载荷 → `.app/.zip/.dmg` → 安装 → 启动 Core 与 App |
| `make docker-build` | 构建 runtime 镜像（另有 `dev`/`browser` target） |

浏览器集成测试需真实 Chrome：`go test -race -tags browser_integration ./internal/tool/browser ./internal/app`。

### macOS 本地构建

`packaging/macos/build-local.sh` 是本地改完桌面端后的唯一推荐入口（CI 仍走 `build-app.sh` + Release 载荷）：i18n 与 `swiftc -typecheck` 先失败 → 编译 Core 与 core-skills 载荷 → `build-app.sh` 产出 `.app/.zip/.dmg` → 安装到 `/Applications` → 加载 Core → 启动 App。用 `--build-only` 只出包不安装。

只有 Command Line Tools、没有完整 Xcode 时：

- 当前 SDK 的 SwiftUI `@State` 由 `SwiftUIMacros` 宏实现，该插件只随 Xcode 分发。脚本会自动回退到仍用 `propertyWrapper` 的旧 SDK；也可用 `AGENTDOCK_MACOS_SDK_PATH` 指定。
- ad-hoc 签名每次重建都会改变 cdhash，SMAppService 记录的 LWCR 不匹配时 launchd 会以 `OS_REASON_CODESIGNING` 拒绝启动 Core。脚本用 `launchctl bootstrap` 直接加载 Core 绕过该约束；**机器重启后必须重跑一次脚本**。要彻底去掉这层绕过，配置稳定的代码签名身份即可（`AGENTDOCK_CODESIGN_IDENTITY`，自签名证书也满足 LWCR）。
- 桌面端保存配置只写入 `agentdock.env`，**不自动重启 Core**：避免打断正在运行的任务，新配置在下次 Core 启动时生效。

### 桌面端界面约定

- 侧栏只承载页面导航。工具/Skill/规则/插件这类目录内容走「能力 → 目录 → 可搜索列表 → 详情」三级，不要把条目平铺到侧栏。
- 分栏列表用 `.listStyle(.inset)`；`.listStyle(.sidebar)` 只给主侧栏，否则内容区会被染成侧栏灰底。
- 每个页面正文保留 `.padding(.top, 16)`（`TestMacOSPagesUseConsistentCompactTopSpacing` 会断言数量与页数一致）。
- 窗口尺寸由代码控制（`hosting.sizingOptions = []` + 显式 `setFrame`/`minSize`）。不要放开 SwiftUI 内容驱动窗口尺寸，带 `List`/`ScrollView` 的页面会把窗口拉到屏幕高度。
- 服务控制类按钮不要顺带重启 Core；重启必须是用户显式动作。

### 调用追踪契约

- 读取：`GET /internal/runtime/traces?view=summary|full`，以及 `GET /internal/runtime/traces/detail?id=`。列表默认走 `summary`（只带 `command.summary` 与字节数），详情按需拉完整记录。
- 写入：`Arguments`/`Result`/`command.command`/`stdout`/`stderr` 统一在 `observability.TraceStore.Record` 处先脱敏（16 KiB 扫描窗口）再截断到 8 KiB，超限打 `truncated` 标记。不要在采集侧另开截断点。
- 改 trace 字段必须同步三处：Swift 解码（`TraceRenderView.swift` / `ServiceController.swift`）、CSV 导出列、`runtime_trace*_test.go` 的字段断言。

## Coding Style & Naming Conventions

- 必须 `gofmt`（`make fmt`），`go vet` 零告警；无 golangci-lint。
- 包名小写单词；导出符号必须有注释；错误用 `fmt.Errorf("verb subject: %w", err)` 保留链。
- 注释解释**为什么**（协议约束、失败模式、历史包袱），不描述代码做了什么。
- 单函数只做一件事；边界处校验并 fail fast，不做防御性冗余分支。

## Testing Guidelines

- 标准库 `testing`，无断言库；优先表驱动与 `t.Helper()`。
- 契约不可漂移：改 Input/Output Schema 必须同步跑 `contract_drift_test`、`output_contract_coverage_test`。
- CI 覆盖 Linux/macOS/Windows；Windows 测试后**工作树必须干净**，测试不得残留临时文件。
- 新增脚本须登记 `scripts/governance/inventory.yaml`；超过 300 行需写 `oversize_justification`。
- 桌面端改动至少跑 `go test ./scripts/test`（侧栏、间距、i18n、应用菜单等静态断言都在这里）与 `python3 scripts/test/check-macos-i18n.py`。

## Commit & Pull Request Guidelines

- 格式：`type(scope): 中文描述 (#PR)`，例如 `fix(windows): 恢复升级后的隧道 (#251)`。
- `type` 用 `feat|fix|refactor|perf|chore|docs`；`scope` 用平台或模块：`windows`、`macos`、`release`、`mcp`、`desktop`、`installer`、`tunnel`、`skill`。
- PR 必须通过 `make check`；只改了单平台时明确写出未验证平台。
- 改动更新/安装链路时，说明 rollback 路径；不要提交新的版本号特判。

## Security & Configuration Tips

- 不提交 token、配对码、设备身份；配置样例只改 `.env.example`。
- 调用追踪会把 `exec_command` 的命令与 stdout/stderr 落盘（默认脱敏 `KEY=value`、`Authorization:`/`X-Api-Key:`、JSON token、URL 内嵌凭据），但仍可能残留自由文本里的密钥：不要把 trace 内容原样贴到公开 issue/PR。
- `/internal/runtime/*` 未启用认证时仅允许回环访问；公网必须 Bearer 或 OAuth。
- 不可信路径用 `os.OpenRoot` 约束；权限校验用 `internal/fs/securepath`，写入用 `internal/fs/atomicfile`，不要手工拼接后直接 `os.ReadFile`。
- 自进化裁决只属本地 AgentDock；Nexus 侧只存储 Recall/Workflow，不要在 Core 加模型 Provider 调用。
