---
id: "2026-10-29-mygo-shell-spike"
title: "MyGo 壳可行性 Spike（托盘/多窗口/组件/性能）"
status: "todo"
created: "2026-10-29"
updated: "2026-10-29"
category: "跨平台UI"
tags: ["mygo", "spike", "macos", "windows", "linux"]
---

# Issue: MyGo 壳可行性 Spike

## Goal

用最小原型验证 MyGo 能否承载控制面板窗口，并在 2 天内给出「能做/不能做/需要替代」的明确结论。

## 背景

ADR `docs/architecture/20261029-mygo-cross-platform-ui-adr.md` 选择增量并存，但以下能力尚未验证：系统托盘、多窗口/单实例、native 组件观感、冷启动与内存。

## 验收标准

- [ ] 原型能启动一个 native UI 窗口，左侧 `ui.Sidebar`、右侧 `ui.Table`，数据来自本机 `/internal/runtime/status`
- [ ] 结论明确：MyGo 是否提供托盘/菜单栏常驻（无则给出替代方案）
- [ ] 结论明确：单实例与深链（`agentdock://`）如何实现
- [ ] 记录冷启动时间、常驻内存、包体积（对比现有 macOS 壳）
- [ ] 结论写入 `docs/architecture/20261029-mygo-spike-report.md`

## 实施阶段

### Phase 1: 最小窗口
- [ ] `mygo init -template native`，窗口 + Sidebar + Table
- [ ] 拉取 `/internal/runtime/status` 并渲染 tool 数量

### Phase 2: 关键能力验证
- [ ] 托盘/菜单栏（查 `mygo/docs` 与源码，确认有无；无则记录替代品）
- [ ] 多窗口、单实例、深链
- [ ] 中文输入、键盘导航、无障碍基础检查

### Phase 3: 性能与结论
- [ ] 冷启动、内存、包体积
- [ ] 三平台交叉编译验证（`mygo build`）
- [ ] 输出 report，更新 ADR 状态

## 关键决策

| 决策 | 理由 |
|---|---|
| 先 native UI，不引入 webview | 避免再引入 TS/Vite 构建链；组件已覆盖现有页面 |
| 托盘归属单独结论 | 若无 tray，产品上必须保留原生壳入口 |

## 风险

- 若托盘缺失且无法替代，Console 只能做普通窗口，原生壳入口长期保留。
- native UI 与现有 SwiftUI 观感差异需要产品确认。

## 依赖

- 无（可独立开始）

## 相关资源

- ADR：`docs/architecture/20261029-mygo-cross-platform-ui-adr.md`
- 上游：`concept-no-cgo-and-size`、`widget-sidebar`、`widget-table`、`recipe-init-native`

---

## Status 更新日志

- **2026-10-29**: 创建
