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
| `make docker-build` | 构建 runtime 镜像（另有 `dev`/`browser` target） |

浏览器集成测试需真实 Chrome：`go test -race -tags browser_integration ./internal/tool/browser ./internal/app`。

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

## Commit & Pull Request Guidelines

- 格式：`type(scope): 中文描述 (#PR)`，例如 `fix(windows): 恢复升级后的隧道 (#251)`。
- `type` 用 `feat|fix|refactor|perf|chore|docs`；`scope` 用平台或模块：`windows`、`macos`、`release`、`mcp`、`desktop`、`installer`、`tunnel`、`skill`。
- PR 必须通过 `make check`；只改了单平台时明确写出未验证平台。
- 改动更新/安装链路时，说明 rollback 路径；不要提交新的版本号特判。

## Security & Configuration Tips

- 不提交 token、配对码、设备身份；配置样例只改 `.env.example`。
- `/internal/runtime/*` 未启用认证时仅允许回环访问；公网必须 Bearer 或 OAuth。
- 不可信路径用 `os.OpenRoot` 约束；权限校验用 `internal/fs/securepath`，写入用 `internal/fs/atomicfile`，不要手工拼接后直接 `os.ReadFile`。
- 自进化裁决只属本地 AgentDock；Nexus 侧只存储 Recall/Workflow，不要在 Core 加模型 Provider 调用。
