---
id: "2026-10-29-console-readonly-pages"
title: "Console 只读页面迁移（6 页功能对等）"
status: "todo"
created: "2026-10-29"
updated: "2026-10-29"
category: "跨平台UI"
tags: ["mygo", "ui", "migration"]
---

# Issue: Console 只读页面迁移（6 页功能对等）

## Goal

用 MyGo native UI 实现 6 个只读页面，功能与 macOS 现有实现逐项对等，并在三平台产出同一个二进制。

## 页面与对等清单

| 页面 | 必须保留 |
|---|---|
| Catalog | Sidebar 分组（Tools/Skills/Common/Rules/Plugins）、工具 schema、注解、插件 inspect 详情 |
| Tasks | 搜索、状态筛选、项目分组折叠、状态统计可点击、详情（条件/步骤/事件/评审） |
| Files | 路径树、公共根、变更 chip、diff 着色、搜索 |
| Published | 列表、打开/复制链接、未配置公网提示、过期过滤 |
| Traces | 搜索/工具/状态筛选、UA、入参出参、来源 |
| Analytics | 指标卡、条形图（`ui/chart`）、明细表、搜索、三种排序 |

## 验收标准

- [ ] 六页功能对等表逐项通过（人工 + 截图对比）
- [ ] 同一份源码 `mygo build` 产出 macOS/Windows/Linux 三平台可运行产物
- [ ] Core 未运行时每页显示不可用说明，不空白、不崩溃
- [ ] 键盘可达：搜索框、列表选择、详情展开均可用键盘完成
- [ ] 大列表（32 工具 / 200 任务 / 500 变更）滚动不卡顿

## 实施阶段

### Phase 1: 骨架
- [ ] `ui.Split` + `ui.Sidebar` + `ui.Router` 页面框架
- [ ] 复用数据层（见 `console-data-layer`）

### Phase 2: 页面
- [ ] Catalog（含 `ui.Table` schema 展示）
- [ ] Tasks（`ui.Table` + 折叠分组）
- [ ] Files / Traces / Published / Analytics

### Phase 3: 对等验收
- [ ] 逐页对照 macOS 现有行为
- [ ] 无障碍与键盘检查
- [ ] 性能基线记录

## 关键决策

| 决策 | 理由 |
|---|---|
| 先只读，后写 | 只读页无审批/回滚语义，风险最低 |
| 用 `ui/chart` 画 Analytics | 已有组件，避免自绘 |
| macOS 原生壳本期不删除旧页 | 双壳窗口期，先并行验证 |

## 风险

- native UI 的表格/树在 500 行量级下的性能需实测。
- diff 长文本需要虚拟滚动或截断策略。

## 依赖

- `console-data-layer`
- `mygo-shell-spike`（组件观感与性能结论）

## 相关资源

- 上游：`widget-sidebar`、`widget-table`、`widget-chart`、`widget-split`

---

## Status 更新日志

- **2026-10-29**: 创建
