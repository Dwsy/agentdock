---
id: "2026-10-29-macOS 任务交接复制：让 Agent 领取并继续任务"
title: "macOS 任务交接复制：让 Agent 领取并继续任务"
status: "done"
created: "2026-10-29"
updated: "2026-10-29"
category: "桌面端"
tags: ["workhub", "macos", "tasks", "uiux"]
---

# Issue: macos 任务交接复制：让 Agent 领取并继续任务

## Goal

任务详情可一键复制「任务交接简报」：粘贴给任意 Agent 即可领取任务并继续推进，无需再回控制面板翻上下文。

## 背景/问题

任务详情只能看，没法把任务交给别的 Agent 继续：目标、验收条件、步骤进度、blocker、最新进展分散在多个卡片里，手工拼接成本高；`task_manage` 的生命周期动作（get/checkpoint/block/resume/final_review/complete）接手方也不知道怎么用。

## 关键决策

| 决策 | 理由 |
|------|------|
| 交接文本 = 自包含 Markdown 简报（含 ID/项目/设备/状态/目标/条件/步骤/阻塞/进展/最近事件） | 接手方零上下文可开工 |
| 结尾附 task_manage 生命周期操作指引 | 领取后如何登记进度是契约的一部分，不能靠口口相传 |
| 步骤用 `[x]/[>]/[ ]` 标记完成/进行中/待办 | 一眼看到从哪继续 |
| 只读现有详情数据拼文本，不加后端 | 数据已齐，纯展示层缺口 |

## 验收标准 (Acceptance Criteria)

- [x] WHEN 任务详情页，SHALL 提供「复制任务交接」按钮（含 tooltip 说明用途）
- [x] WHEN 复制，剪贴板 SHALL 获得自包含 Markdown 简报（元数据/目标/条件/步骤/阻塞/进展/事件）
- [x] WHEN 有 blocker，简报 SHALL 单列「阻塞」段
- [x] 简报结尾 SHALL 说明 task_manage 领取与推进方式
- [x] 双语文案齐备，i18n 校验通过

## 实施阶段

- [x] TaskDetailContent 增加 handoffText 组装 + 复制按钮
- [x] Localizable.strings 双语补 9 条文案
- [x] Swift typecheck + `check-macos-i18n.py` + `go test ./scripts/test` 通过

## 遇到的错误

| 日期 | 错误 | 解决方案 |
|------|------|---------|
| — | — | — |

## 相关资源

- 契约：`internal/tool/task/contract.go`（task_manage：create/list/get/checkpoint/block/resume/final_review/complete）

---

## Status 更新日志

- **2026-10-29**: 状态变更 → done，备注: 需求 4 落地；纯展示层改动，验证通过
