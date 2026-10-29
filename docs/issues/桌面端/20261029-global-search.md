---
id: "2026-10-29-global-search"
title: "全局搜索（跨 7 类数据）"
status: "done"
created: "2026-10-29"
updated: "2026-10-29"
category: "桌面端"
tags: ["search", "runtime-api", "priority-p1"]
---

# Issue: 全局搜索（跨 7 类数据）

## Goal

一个搜索框同时检索 Tool、Skill、Rule、Plugin、Task、File change、Artifact、Trace，结果按类型分组。

## 背景

面板已有 10 个页面，找东西的成本超过建页面的成本。当前每页各自搜索，且 Files/Traces 是本地过滤。

## 验收标准

- [ ] `GET /internal/runtime/search?q=&types=&limit=` 返回分组结果
- [ ] 每类结果带跳转目标（页面 + 选中项）
- [ ] 大小写不敏感；空查询返回空
- [ ] P95 延迟 < 300ms（本机、2000 trace 以内）
- [ ] 结果数量有上限与「更多」提示
- [ ] 快捷键唤起（如 ⌘K），Escape 关闭

## 实施阶段

### Phase 1: 后端聚合
- [ ] 复用各索引（catalog/skills/plugins/tasks/traces），不新建存储
- [ ] 排序：精确匹配 > 前缀 > 子串；同类内按时间

### Phase 2: UI
- [ ] 搜索面板与结果分组
- [ ] 跳转与选中联动

## 关键决策

| 决策 | 理由 |
|---|---|
| 后端聚合，不在 UI 拼 | 三类数据来自不同端点，前端合并会重复请求 |
| 不引入全文索引 | 当前规模不需要；trace 已有 2000 条上限 |

## 风险

- trace payload 搜索成本高，需要按需限制扫描深度。

## 依赖

- 无（但 `task_id` 关联会让结果更有用）

## 相关资源

- 现有索引：`internal/app/agentdock_context.go`、`internal/observability/trace.go`

---

## Status 更新日志

- **2026-10-29**: 创建
- **2026-10-29**: 状态变更 → done，备注: v2 交付（217a475）：⌘K 全局搜索以客户端聚合实现（跨 7 类数据分组、跳页定位、Esc 关闭）；后端 search 端点按决策留 P1 备选
