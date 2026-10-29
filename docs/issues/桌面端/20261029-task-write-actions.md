---
id: "2026-10-29-task-write-actions"
title: "任务写操作 API（checkpoint / complete / delete）"
status: "done"
created: "2026-10-29"
updated: "2026-10-29"
category: "桌面端"
tags: ["tasks", "runtime-api", "write", "priority-p0"]
---

# Issue: 任务写操作 API（checkpoint / complete / delete）

## Goal

提供受控的任务写端点，让控制面板可以推进/关闭/删除任务，复用现有 `task_manage` 语义与状态校验。

## 背景

UI 目前只能看任务。`task_manage` 已实现 checkpoint/complete/delete 与状态机，但没有暴露给本地控制面（只有 `DELETE /tasks/{id}`）。

## 验收标准

- [ ] `POST /internal/runtime/tasks/{id}/actions`，action ∈ `checkpoint | complete | delete`
- [ ] checkpoint 支持 `completed_step_ids` / `current_step_id`
- [ ] 非法状态迁移返回 409 与可读原因（复用 taskstate 校验）
- [ ] 并发写同一任务只有一个成功，另一个得到明确冲突错误
- [ ] 写操作本身进入 trace（`source=internal`）
- [ ] 不绕过 `task_manage` 的审批/确认语义（如存在）

## 实施阶段

### Phase 1: 契约
- [ ] 定义请求/响应与错误码
- [ ] 复用 `internal/tool/task` 的校验，不复制状态机

### Phase 2: 实现
- [ ] dispatch + 路由
- [ ] 文件锁与并发测试

### Phase 3: 文档
- [ ] Runtime API 说明与 issue 更新

## 关键决策

| 决策 | 理由 |
|---|---|
| 用 actions 子资源而非多个动词端点 | 与现有 `task_manage` 的 action 模型一致 |
| 复用 taskstate 校验 | 避免 UI 接口形成第二套状态机 |

## 依赖

- 无（独立）

## 相关资源

- `internal/taskstate/store_transition.go`、`internal/tool/task/service.go`

---

## Status 更新日志

- **2026-10-29**: 创建
- **2026-10-29**: 状态变更 → done，备注: v2 交付（217a475）：POST /tasks/{id}/actions（checkpoint/complete/delete），409 INVALID_STATUS、并发 TryLock 单胜、复用 taskstate 状态机；控制台任务页操作按钮已上
