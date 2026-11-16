---
id: "2026-10-30-调用分析增加 token 统计（tiktoken 计数与半小时区间累计）"
title: "调用分析增加 token 统计（tiktoken 计数与半小时区间累计）"
status: "done"
created: "2026-10-30"
updated: "2026-10-30"
category: "桌面端"
tags: ["workhub", "macos", "analytics", "token", "tiktoken"]
---

# Issue: 调用分析增加 token 统计（tiktoken 计数与半小时区间累计）

## Goal

调用分析页展示 token 统计：入参、结果分开计数（tiktoken 估算口径），一次调用只计数一次，随后在半小时滚动区间内持续累加。

## 背景/问题

调用分析页有调用次数、错误、延迟分位和代码行数变动，但看不到 token 量，无法判断工具调用的上下文开销。`docs/audits/mcp-tool-token-efficiency-audit-2026-10-29.md` 明确提出"采集工具输出字节数、截断状态及估算 token 数"的度量需求，此前没有实现。计数用 `github.com/tiktoken-go/tokenizer`（cl100k_base）对序列化文本完成，不保存任何正文。

## 验收标准 (Acceptance Criteria)

- [x] WHEN 一次工具调用结束，analytics SHALL 记录入参与结果的 token 数（分开计数）
- [x] WHEN Core 健康，页面 SHALL 展示最近 30 分钟的入参/结果 token 两个指标卡
- [x] WHEN 存在 tool_stats，明细表 SHALL 展示每个工具的入参/结果 token 列
- [x] WHEN 超过 30 分钟，过期分钟桶 SHALL 淘汰出滚动区间，但仍留在进程总量
- [x] IF 大文本超过采样上限，THEN 按字节比例外推（估算口径），不让计数拖慢工具返回路径
- [x] IF trace 关闭，THEN token 统计依然成立（不依赖 trace 采集）
- [x] WHERE Windows / Linux / Web 控制台，系统 SHALL 无行为变化

## 实施阶段

### Phase 1: 规划和准备
- [x] 确认计数对象：入参（args JSON）与结果（result JSON）序列化文本，分开计数
- [x] 确认聚合口径：recorder（调用分析的唯一数据源），分钟桶 × 30 覆盖半小时滚动区间

### Phase 2: 执行
- [x] `observability`：`tokens.go` 用 tiktoken-go/cl100k_base 计数，128 KiB 采样 + 字节比例外推
- [x] `observability`：CallMeta / ExecutionRecord / ToolStats / Snapshot 增加 input_tokens、output_tokens 与区间累计
- [x] `observability`：recorder 写入路径按分钟桶累加并淘汰超窗桶
- [x] `app`：`toolTokenCounts` 在调用结束时计数，`/internal/runtime/analytics` 增加 total/window token 字段
- [x] `macos`：指标卡（30 分钟区间）+ 明细表列 + 中英文案

### Phase 3: 验证
- [x] `go test ./internal/observability ./internal/app`
- [x] `make check`（SHELL=/bin/bash）
- [x] `xcrun swiftc -typecheck` + `check-macos-i18n.py`

### Phase 4: 交付
- [x] 更新文档
- [ ] 创建 PR
- [ ] 合并主分支

## 关键决策

| 决策 | 理由 |
|------|------|
| 计数对象是序列化文本而非字节数 | 用户指定 tiktoken 口径；字节数只能给量级，token 数才能对照宿主上下文 |
| 入参、结果分开计 | 能区分"请求重"还是"返回重"，对齐 token 效率审计的度量需求 |
| 128 KiB 采样 + 字节比例外推 | BPE 在几 MiB 命令输出上会拖慢工具返回路径；统计只要数量级准确 |
| 分钟桶 × 30 的滚动区间，总量另计 | 一次调用只计数一次；区间累加与进程总量（与 total_calls 同口径）各取所需 |
| 指标卡展示区间值而非总量 | 用户核心诉求是半小时区间；总量仍在 API 负载里 |
| 不给 Web 控制台/托盘同步 | 用户选定范围为后端 + macOS 控制面板；Web 端 ToolStats 是独立副本，后续单独开工 |

## 遇到的错误

| 日期 | 错误 | 解决方案 |
|------|------|---------|
| 2026-10-30 | `internal/app` scoped_env 测试 exit 127 | 与本次改动无关：登录 shell 是 fish；`SHELL=/bin/bash` 全绿 |
| 2026-10-30 | recorder 测试假设 ToolStats 按插入顺序排列 | 排序键是（调用次数, 工具名），同次数时按名字序；改为按工具名查找断言 |

## 相关资源

- [x] 相关 Issue: `docs/issues/桌面端/20261029-macOS 工具调用统计仪表盘.md`、`docs/issues/桌面端/20261030-macOS 调用分析增加代码行数变动统计.md`
- [x] 相关审计: `docs/audits/mcp-tool-token-efficiency-audit-2026-10-29.md`
- [x] 相关接口: `GET /internal/runtime/analytics`

## Notes

- token 数是 cl100k_base 估算口径，不是宿主模型的真实计费 token；超 128 KiB 的文本按字节比例外推。
- `ExecutionRecord.input_tokens` / `output_tokens` 用 `omitempty`：没有计数的调用不会多出字段；ToolStats 与 Snapshot 字段恒定输出。
- 半小时区间按记账时刻分桶，跨半小时的长调用在结束时计入当前区间。

---

## Status 更新日志

- **2026-10-30 14:50**: 状态变更 → done，备注: 后端计数、区间累计、API 字段、macOS 指标卡与明细列均已实现并通过 make check
- **2026-10-30 16:10**: 口径修正（用户要求）：
  1. 半小时滚动区间改为固定累计段——段按钟表半点对齐，段内持续累加，跨段清零重计（`recorder.go` 分钟桶 map 替换为单段 `segmentIndex/segmentInput/segmentOutput`）。
  2. 新增按年月日（本地日期）token 累计桶 `daily_tokens`（保留 30 天），API `GET /internal/runtime/analytics` 新增 `daily_tokens`、`token_segment_started_at` 字段；macOS 新增「按日 Token 统计」卡片。
  3. 累计统计持久化：总调用/错误/代码行变动/token 总量/按日桶/当前段落盘到 `~/.agentdock/analytics-state.json`（atomicfile 原子写），Core 重启后恢复；最近调用环与 tool_stats 是窗口视图，不持久化。
  4. macOS 大数字用 K/M/B 缩写（`analyticsTokenCount`），指标卡 caption 展示当前段的本地时间窗口（如 `14:00-14:30`）。
  验证：`go test ./internal/observability ./internal/app`、`make check`、`swiftc -typecheck`（旧 SDK 回退）、`check-macos-i18n.py` 全部通过。
