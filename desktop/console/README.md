# AgentDock Console（跨平台控制台）

AgentDock 控制面板的跨平台版本，基于 [MyGo](https://github.com/egoist/mygo)
native UI（GPU 自绘，无 HTML/WebView），单二进制、`CGO_ENABLED=0`、
可交叉编译 macOS / Windows / Linux。

与主模块解耦：独立 Go 模块（`github.com/uvwt/agentdock/desktop/console`），
数据层只消费本机 Runtime HTTP API（`/internal/runtime/*`），
不 import Core 的 `internal/` 包。

## 运行

```sh
cd desktop/console
go run ./cmd/console
```

配置（环境变量优先，其次 agentdock.env，最后默认值）：

| 变量 | 默认 | 说明 |
|------|------|------|
| `AGENTDOCK_HOST` | `127.0.0.1` | Core 地址 |
| `AGENTDOCK_PORT` | `8765` | Core 端口 |
| `AGENTDOCK_AUTH_TOKEN` | 空 | Bearer 鉴权 |
| `AGENTDOCK_ENV_FILE` | `~/Library/Application Support/AgentDock/agentdock.env` | 配置文件 |

Core 未运行时页面显示错误态并可重试，不影响启动。

## 页面

概览 / 能力目录 / 任务 / 文件变更 / 发布 / 追踪 / 统计 —— 侧栏分组导航。
只读观测 + 受控写操作（任务 checkpoint·complete·delete、发布撤销·延期、
trace 导出·清空，均带二次确认）。

- ⌘K 全局搜索（分组结果、跳页定位）
- 追踪页 5s 自动轮询（可暂停）；SSE 断线时自动降级轮询
- 任务详情关联"本任务的调用与文件"（task_id 贯穿 trace）
- 告警横幅（错误率 / trace 磁盘 / 任务阻塞三规则）

## 结构

```text
cmd/console/        入口（窗口装配）
internal/runtimeapi/ 数据层：Backend 接口 + Client + DTO（契约测试）
internal/ui/         页面与 App 状态机（ui.NewTester 无窗口测试）
internal/ui/kit/     自研领域组件（StatusChip/MetricCard/DiffText…）+ Lucide 图标
```

## 开发

```sh
SHELL=/bin/bash go build ./... && SHELL=/bin/bash go vet ./...
SHELL=/bin/bash go test ./... -count=1   # ui.NewTester 无窗口渲染测试
gofmt -l .                                # 应为空
```

注意（MyGo 即时模式）：

- 值绑定（SearchField/Select/Segmented）直写指针，后台读取须绑局部副本并在
  `Changed()` 锁内写回，否则 `-race` 报警。
- 轮询/时间戳用 `c.Now()`（Tester 里是虚拟时钟，`tt.Advance` 推进）。
- 窗口级快捷键用根 view 的 `Context.OnShortcut`。

## 设计文档

- ADR：`docs/architecture/20261029-mygo-cross-platform-ui-adr.md`
- UI/UX：`docs/architecture/20261029-mygo-console-uiux-design.md`
