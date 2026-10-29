---
id: "2026-10-29-macOS 控制面板 UIUX 完善：搜索、加载更多、全文、复制、自动刷新"
title: "feat(desktop): macOS 控制面板 UIUX 完善：搜索、加载更多、全文、复制、自动刷新"
status: "draft"
created: "2026-10-29"
updated: "2026-10-29"
category: "桌面端"
tags: ["workhub", "pr", "macos", "uiux", "traces"]
---

# feat(desktop): macOS 控制面板 UIUX 完善：搜索、加载更多、全文、复制、自动刷新

## 背景与目的 (Why)

控制面板各页面的搜索、长列表、截断内容、复制入口与刷新节奏各做各的：搜索输入后要手动刷新、大列表一次性渲染、trace 正文被 8 KiB 预览截断后无法再看全文、复制入口零散、只有两个页面自动轮询。

## 变更内容概述 (What)

- **trace 全文旁路（Go）**：`TraceStore.Record` 只给会被截断的记录写脱敏全文副本（单侧上限 256 KiB，超限 `full_truncated`）到 `<root>/full/YYYY-MM-DD.jsonl`；`GET /internal/runtime/traces/detail?id=&full=1` 按需回读，缺失时 `full_available=false`；清理/保留期/磁盘统计与预览同步。
- **全文加载 UI**：Traces 详情在预览截断时提供「加载全文」，全文/预览自动切换并如实标注；失败降级为预览。
- **搜索统一**：新增 `SearchField`（放大镜 + 一键清空），接入目录/Tasks/Traces/Analytics/Files；Published、Activity 新增搜索；Traces 搜索输入 250ms 防抖后自动重查（API 侧过滤）。
- **滚动加载更多**：Traces/Activity/Published 首批渲染 50 条（Activity 20 条），滚动到末行自动渲染下一批；Traces 列表拉取上限提高到 500。
- **复制按钮**：trace 详情（payload/命令/输出/错误/工具名/trace ID）、任务详情（标题/goal/ID）、文件路径、Analytics/Traces 行右键复制工具名。
- **自动刷新**：新增 `autoRefresh` modifier（5s 轮询）+ 可暂停开关，接入 Tasks/Traces/Files/Published/Analytics/Activity；刷新保留选中项与滚动位置。

## 关联 Issue

- **Issues:** `docs/issues/桌面端/20261029-macOS 控制面板 UIUX 完善（搜索、加载更多、全文、复制、自动刷新）.md`

## 测试与验证结果 (Test Result)

- [x] 单元测试通过：`make check`（含 `internal/observability` 全文旁路 6 例、`internal/app` detail 全文 1 例、`internal/runtimeapi` full 参数分发）
- [x] 静态断言通过：`go test ./scripts/test`（`TestMacOSActivityOwnsRuntimeAnalytics` 断言范围扩到 `ListPageControls.swift`）
- [x] `check-macos-i18n.py` 通过（567 keys / 每语言 745 条目）
- [x] Swift typecheck 通过（本机无 SwiftUIMacros，按 `build-local.sh` 逻辑回退旧 SDK）
- [ ] 手动回归测试通过（真机冒烟待做）

## 风险与影响评估 (Risk Assessment)

- 全文旁路增加磁盘占用（仅截断记录、单条上限 512 KiB、随保留期清理），`DiskBytes` 告警口径已包含。
- `RuntimeTraceDetail` 签名加 `full` 参数（不保留旧签名）；`tracePreviewWith` 扫描窗口从固定 16 KiB 变为 `max(16 KiB, 2×limit)`，预览路径行为不变。
- 全文脱敏同步发生在 `Record` 路径，单侧被 256 KiB 上限钳制。

## 回滚方案 (Rollback Plan)

整体 revert 本次提交即可；`full/` 目录为新增旁路，旧版本不读取，残留文件可手动删除。API 只新增 `full` 查询参数与响应字段，旧客户端不受影响。

---

## 变更类型

- [ ] 🐛 Bug Fix
- [x] ✨ New Feature
- [ ] 📝 Documentation
- [x] 🚀 Refactoring
- [ ] ⚡ Performance
- [ ] 🔒 Security
- [x] 🧪 Testing

## 文件变更列表

| 文件 | 变更类型 | 描述 |
|------|---------|------|
| `internal/observability/trace_full.go` | 新增 | 全文旁路副本：构建/读取/上限/脱敏 |
| `internal/observability/trace_full_test.go` | 新增 | 旁路读写、截断、脱敏、清理、磁盘统计测试 |
| `internal/observability/trace.go` | 修改 | pendingFull 队列、flush/clear/cleanup/DiskBytes、扫描窗口按上限放大 |
| `internal/app/runtime_trace_api.go` | 修改 | `RuntimeTraceDetail(id, full)` 附带全文 |
| `internal/app/runtime_trace_full_test.go` | 新增 | detail 全文契约测试 |
| `internal/runtimeapi/dispatch.go` / `runtime.go` | 修改 | `full=1` 参数解析与接口签名 |
| `desktop/macos/.../ListPageControls.swift` | 新增 | SearchField / AutoRefreshToggle / CopyButton / ListChunk |
| `desktop/macos/.../TraceRenderView.swift` | 修改 | 全文模型解码、加载全文 UI、复制按钮 |
| `desktop/macos/.../ServiceController.swift` | 修改 | detail 请求 `full` 参数与载荷类型 |
| `desktop/macos/.../NativeControlPanelWindowController.swift` | 修改 | 各页面搜索/加载更多/自动刷新/复制接入 |
| `desktop/macos/.../Resources/*/Localizable.strings` | 修改 | 新增 12 条双语文案 |
| `scripts/test/macos_advanced_settings_window_test.go` | 修改 | 断言纳入 `ListPageControls.swift` |
| `AGENTS.md` / `docs/runtime-access-and-recovery.md` | 修改 | 调用追踪契约补全文旁路 |
| `packaging/macos/build-local.sh` | 修改 | （存量问题）注释 `${TMPDIR}` 边界修复，解锁 `make check` |

## 测试命令

```bash
make check
go test ./internal/observability ./internal/app ./internal/runtimeapi
python3 scripts/test/check-macos-i18n.py
bash packaging/macos/build-local.sh --build-only   # 含 swiftc typecheck
```

## 破坏性变更

**是否有破坏性变更？**

- [ ] 否
- [x] 是 - `RuntimeTraceDetail(id uint64)` → `RuntimeTraceDetail(id uint64, full bool)`（服务端接口与 dispatch stub 同步更新，HTTP 契约向后兼容）

## 性能影响

**是否有性能影响？**

- [ ] 无影响
- [x] 提升 - 长列表首批只渲染 50 行；列表响应仍走 summary
- [x] 下降 - `Record` 对截断记录多一次全文脱敏（单侧被 256 KiB 钳制，几十毫秒内）

## 依赖变更

**是否引入新的依赖？**

- [x] 否

## 安全考虑

**是否有安全影响？**

- [x] 否 - 全文与预览走同一套脱敏（含 JSON 键值与命令行凭据规则），绝不落未脱敏字节；单条上限防磁盘写爆

## 文档变更

**是否需要更新文档？**

- [x] 是 - `AGENTS.md` 调用追踪契约、`docs/runtime-access-and-recovery.md`

## 最终状态

- **合并时间:** 待定
- **合并人:** —
- **Commit Hash:** 待提交
- **部署状态:** 待部署
