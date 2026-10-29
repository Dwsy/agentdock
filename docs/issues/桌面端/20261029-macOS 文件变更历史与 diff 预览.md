---
id: "2026-10-29-macOS 文件变更历史与 diff 预览"
title: "macOS 文件创建/编辑历史（项目 tree + diff 预览）"
status: "done"
created: "2026-10-29"
updated: "2026-10-29"
category: "桌面端"
tags: ["workhub", "macos", "files", "diff", "trace"]
---

# Issue: macOS 文件创建/编辑历史（项目 tree + diff 预览）

## Goal

在 macOS 控制面板新增 Files 页面：以项目目录树展示 `file_edit` 产生的文件创建/编辑历史，选中文件可查看每次变更的 unified diff。数据完全来自工具调用持久化（trace），不新增第二套文件快照。

## 背景/问题

排障时经常需要回答「刚才那次修改动了哪些文件、改了什么」。当前只有 trace 的入参/出参原文，没有按文件聚合的视图；`file_edit` 返回的 `diff_preview` 被埋在单条 trace 里，无法按项目浏览。

需求明确要求数据源是工具调用持久化，因此不能靠重新读磁盘或 git 状态推断历史。

## 验收标准 (Acceptance Criteria)

- [x] WHEN trace 开启且发生过成功的 `file_edit`，Files 页面 SHALL 按路径树展示文件
- [x] WHEN 树中选中文件，右侧 SHALL 列出该文件的全部变更（时间、+/-、action）
- [x] WHEN 选中某次变更，右侧 SHALL 显示 unified diff，并按新增/删除/上下文着色
- [x] WHEN 搜索路径，树 SHALL 只保留匹配文件（目录节点保留祖先链）
- [x] WHEN trace 为 `off`，页面 SHALL 提示开启方式，而不是显示空树
- [x] WHEN 磁盘模式重启后，历史 SHALL 仍可读取（JSONL 回放）
- [x] WHERE Windows / Linux 桌面端，系统 SHALL 无行为变化

## 实施阶段

### Phase 1: 规划和准备
- [x] 确认 `file_edit` 结果的 `diff_preview`/`insertions`/`deletions`/`affected_files` 字段
- [x] 决定不新增存储：把文件变更嵌入 `TraceRecord.file_changes`

### Phase 2: 执行
- [x] Go：`observability.FileChange` / `FileChangeEntry` / `TraceStore.FileChanges`
- [x] Go：`Runtime.Call` 采集时从结果提取变更（每文件一条，diff 限 8 KiB）
- [x] Go：`GET /internal/runtime/file-changes?q=&limit=`
- [x] Swift：Files 页面（tree + 变更 chip + diff 着色）
- [x] 中英文案

### Phase 3: 验证
- [x] `go test ./internal/observability ./internal/app ./internal/runtimeapi`
- [x] 磁盘模式持久化断言（重启后 `FileChanges` 仍存在）
- [x] `go test ./...`（58 包）
- [x] `xcrun swiftc -parse`、`plutil -lint`、`go test ./scripts/test`

### Phase 4: 交付
- [x] PR 记录更新
- [ ] 合并到 fork main

## 关键决策

| 决策 | 理由 |
|------|------|
| 变更嵌入 trace 记录，不建独立快照库 | 需求指定数据源；重放磁盘 JSONL 即可恢复历史，避免两套一致性 |
| 只采集成功调用 | 失败调用的 diff 不可信，且工具可能未写入 |
| 每文件一条，多文件 patch 共享 diff | patch 的 unified diff 本身跨文件；拆条保留时间与来源，便于按文件检索 |
| diff 再截断到 8 KiB | 工具上限 64 KiB；历史视图只需要定位改动的片段 |
| 树从公共目录前缀开始 | 绝对路径前缀（`/Users/...`）对项目浏览无意义 |

## 遇到的错误

| 日期 | 错误 | 解决方案 |
|------|------|---------|
| 2026-10-29 | 测试比较路径失败（`/var` vs `/private/var`） | 用 `filepath.EvalSymlinks` 对齐真实路径 |
| 2026-10-29 | 失败编辑也可能带部分 result，曾被误采集 | `traceFileChanges` 增加 `success` 门控并补回归测试 |
| 2026-10-29 | 绝对路径树根出现 `//a/b` | 单独拼接 pathComponents 的前导 `/` |

## 相关资源

- 端点：`GET /internal/runtime/file-changes`
- 代码：`internal/app/runtime_file_changes.go`、`internal/observability/trace.go`
- 依赖：`docs/issues/桌面端/20261029-macOS 任务矩阵与调用追踪.md`（trace 持久化）

## Notes

- 历史窗口与 trace 相同：内存上限 `AGENTDOCK_TRACE_MAX_RECORDS`，磁盘保留 `AGENTDOCK_TRACE_RETENTION_DAYS`。
- 未覆盖 `exec_command` 间接改文件（如 `mv`/`sed -i`）：那不是 `file_edit` 调用，按需求不从命令输出推断。

---

## Status 更新日志

- **2026-10-29 10:10**: 状态变更 → done，备注: 后端投影与 Mac 视图完成，全量测试通过
