---
id: "2026-10-29-console-packaging"
title: "Console 打包、签名与更新（三平台）"
status: "todo"
created: "2026-10-29"
updated: "2026-10-29"
category: "跨平台UI"
tags: ["mygo", "distribution", "signing", "release"]
---

# Issue: Console 打包、签名与更新（三平台）

## Goal

为 MyGo Console 建立三平台可分发产物与更新通道，且不破坏现有 AgentDock 安装/更新体系。

## 背景

MyGo 自带 `mygo build`（macOS DMG、Windows NSIS、Linux 归档）与 `mygo.Updater`（签名发布）。但 AgentDock 现有分发走 R2 `latest.json` + Setup/DMG + generation 布局，二者不能各说各话。

## 验收标准

- [ ] macOS 产物通过 codesign + notarytool 公证；Gatekeeper 首次打开无阻断
- [ ] Windows 安装器与现有 Inno Setup 共存或有明确替代结论
- [ ] Linux 提供 AppImage/tar.gz + desktop entry，能自动发现本机 Core
- [ ] 更新源与现有 `download.nexusdock.co` 的关系明确（复用或独立通道）
- [ ] CI 三平台构建、签名、上传；失败不产出半成品
- [ ] 版本号与 Core 版本解耦（Console 可独立升级）

## 实施阶段

### Phase 1: 产物
- [ ] macOS DMG/ZIP + 公证
- [ ] Windows NSIS
- [ ] Linux 归档 + desktop entry

### Phase 2: 更新
- [ ] 更新源决策（MyGo updater vs 现有 latest.json）
- [ ] 签名密钥与轮换策略

### Phase 3: CI
- [ ] 矩阵构建
- [ ] 发布 gate 与回滚

## 关键决策

| 决策 | 理由 |
|---|---|
| Console 独立通道 | 避免与 Core 的 generation/回滚事务耦合 |
| 复用现有代码签名账号 | 证书与公证流程已存在 |

## 风险

- 两个更新通道会让用户困惑，需要在产品上明确「Core 更新」与「Console 更新」。
- macOS 公证对 native UI 的 hardened runtime 要求需实测。

## 依赖

- `console-readonly-pages`（先有可交付产物）

## 相关资源

- `docs/release-distribution.md`
- 上游：`recipe-ship`、`doc-updates`、`doc-distribution`

---

## Status 更新日志

- **2026-10-29**: 创建
