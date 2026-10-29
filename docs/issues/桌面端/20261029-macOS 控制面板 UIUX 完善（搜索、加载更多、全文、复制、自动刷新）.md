---
id: "2026-10-29-macOS 控制面板 UIUX 完善（搜索、加载更多、全文、复制、自动刷新）"
title: "macOS 控制面板 UIUX 完善（搜索、加载更多、全文、复制、自动刷新）"
status: "done"
created: "2026-10-29"
updated: "2026-10-29"
category: "桌面端"
tags: ["workhub", "macos", "uiux", "traces", "observability"]
---

# Issue: macOS 控制面板 UIUX 完善（搜索、加载更多、全文、复制、自动刷新）

## Goal

统一并补齐 macOS 控制面板各页面的四类 UIUX 能力：搜索、滚动加载更多、加载被截断内容（trace 全文）、复制按钮、列表页 5s 自动刷新（可暂停）。

## 背景/问题

- 各页面搜索框形态不一，Published 无搜索；复制入口只零散存在于 Trace 详情与设置页。
- 长列表（Activity / Analytics 明细）只有「Show more」按钮，Traces/Files/Tasks 一次全量渲染，大列表滚动卡顿。
- TraceStore 落盘时就把 payload 截断到 8 KiB（脱敏预览），被截断的内容**磁盘上不存在**，UI 无法"加载更多"。
- 刷新节奏不统一：Activity/Analytics 是 5s 轮询，Tasks/Traces/Files/Published 只有手动 Refresh。

## 关键决策

| 决策 | 理由 |
|------|------|
| 全文走旁路文件，不动 8 KiB 预览契约 | 内存环（2000 条驻留）与 summary 传输不能被大 payload 拖垮；预览契约是既有安全设计 |
| 全文单条上限 256 KiB/侧 + 全量脱敏 | 脱敏正则约 65 ms/MiB，上限控制 Record 路径成本；超限打 `full_truncated`，绝不落未脱敏字节 |
| 全文与预览同为按天 JSONL（`full/YYYY-MM-DD.jsonl`） | 追加写、按天清理复用现有 retention/clear 逻辑 |
| 只给会被截断的记录写全文 | 小 payload 的预览即全文，重复落盘只浪费磁盘 |
| 加载更多是 UI 分批渲染 + 滚动触发，不做后端 offset 分页 | 列表端点已有 500 条上限，分页协议是 YAGNI；先解决渲染卡顿 |
| 自动刷新抽成统一 modifier（5s + 可暂停） | 六个列表页各写一遍 while 循环会漂移；与 MyGo 控制台追踪页语义一致 |

## 验收标准 (Acceptance Criteria)

- [ ] WHEN 打开 Traces 详情且记录 `truncated`，系统 SHALL 提供「加载全文」；全文来自旁路文件，缺失时明确降级为预览
- [ ] WHEN 全文超过 256 KiB/侧，系统 SHALL 截断并标记 `full_truncated`，UI 如实标注
- [ ] WHEN Record 全文，系统 SHALL 与预览一样全量脱敏（KEY=value、Authorization、JSON token、URL 内嵌凭据）
- [ ] WHERE 清空/过期清理，系统 SHALL 同步删除旁路全文文件
- [ ] WHEN 任一列表页搜索框输入，系统 SHALL 即时过滤并提供一键清空；Published 页 SHALL 有搜索
- [ ] WHEN 长列表滚动接近底部，系统 SHALL 自动渲染下一批（每批 50 条），不卡顿
- [ ] WHEN 列表页开启自动刷新，系统 SHALL 每 5s 轮询且可暂停；刷新 SHALL 保留选中项与滚动位置
- [ ] WHEN Trace 详情/Tasks 详情/Files/Published/Analytics 行，系统 SHALL 提供对应复制按钮（payload、id、路径、链接、工具名）
- [ ] `make check`、Swift typecheck、`check-macos-i18n.py`、`go test ./scripts/test` 全部通过

## 实施阶段

### Phase 1: 规划和准备
- [x] 盘点各页面搜索/列表/刷新/复制现状
- [x] 确认全文不存在于磁盘，选定旁路文件方案（与用户确认）

### Phase 2: 执行
- [x] S1 Go：TraceStore 全文旁路（Record 写 `full/`、FullRecord 读取、清理/保留同步）
- [x] S2 Go：`/internal/runtime/traces/detail?id=&full=1` 全文字段 + 校验 + 测试
- [x] S3 Swift：全文加载 UI（TraceRenderView 加载全文/预览切换、复制全文）
- [x] S4 Swift：搜索统一（SearchField 组件、Published 补搜索、清空按钮）
- [x] S5 Swift：滚动加载更多（长列表分批渲染 + 滚动触发）
- [x] S6 Swift：复制按钮补齐（Trace/Tasks/Files/Published/Analytics）
- [x] S7 Swift：自动刷新统一（5s 轮询 + 可暂停 modifier，接入列表页）

### Phase 3: 验证
- [x] `make check`（Go 全量）
- [x] Swift typecheck（旧 SDK 回退）+ `check-macos-i18n.py` + `go test ./scripts/test`
- [x] AGENTS.md 调用追踪契约补全文旁路说明

### Phase 4: 交付
- [x] PR 记录
- [ ] 真机冒烟

## 遇到的错误

| 日期 | 错误 | 解决方案 |
|------|------|---------|
| 2026-10-29 | workhub `create issue` 标题含 `/` 导致路径 ENOENT | 标题改用全角标点，避免路径分隔符 |
| 2026-10-29 | `tracePreviewWith` 扫描窗口写死 16 KiB，全文被隐式钉在 16 KiB | 扫描窗口改为 `max(16 KiB, 2×limit)`，预览行为不变 |
| 2026-10-29 | 全文副本 ID 在锁内 ID 分配前捕获，恒为 0 | `full.ID = record.ID` 移到锁内 ID 分配之后 |
| 2026-10-29 | `make check` 脚本治理失败：`build-local.sh` 注释里 `$TMPDIR` 后跟全角括号 | 注释改 `${TMPDIR}`（存量问题，非本次改动引入） |
| 2026-10-29 | `scoped_env_test` 两条用例在 fish shell 下失败（`${...}` 语法） | 环境问题，`SHELL=/bin/bash` 下通过；未改代码 |

## 相关资源

- 契约：AGENTS.md「调用追踪契约」（summary/full 读写与脱敏截断点）
- 前置：`docs/issues/桌面端/20261029-macOS 任务矩阵与调用追踪.md`、`20261029-trace-export-clear.md`

## Notes

- 全文加载只在 detail 按需触发；列表/summary 契约不变，CSV 导出仍导出预览。
- 全文文件权限与预览一致（0600，位于 trace root 下）。

---

## Status 更新日志

- **2026-10-29**: 状态变更 → in_progress，备注: 需求 2 立项，方案经用户确认（旁路全文文件 + 5s 可暂停轮询 + UI 分批加载）
- **2026-10-29**: 状态变更 → done，备注: S1~S7 全部落地；`make check`、typecheck、i18n、`go test ./scripts/test` 通过；待真机冒烟
