---
id: "2026-10-29-artifact-revoke-extend"
title: "发布物撤销与延长有效期"
status: "done"
created: "2026-10-29"
updated: "2026-10-29"
category: "桌面端"
tags: ["artifacts", "runtime-api", "write", "priority-p0"]
---

# Issue: 发布物撤销与延长有效期

## Goal

Published 页面支持立即撤销链接与延长有效期；撤销后链接必须立刻失效。

## 背景

发布物是唯一对外暴露的资产，目前只能等过期。`publicartifacts` 有目录与 metadata，但没有删除/更新 API。

## 验收标准

- [ ] `DELETE /internal/runtime/artifacts/{id}` 删除目录，链接立即 404
- [ ] `POST /internal/runtime/artifacts/{id}/extend` 延长有效期（上限沿用 `MaxRetention`），签名链接随之更新
- [ ] 撤销/延长有二次确认与结果反馈
- [ ] 操作进入 trace（`source=internal`）
- [ ] 撤销不存在的 id 返回 404，不静默成功

## 实施阶段

### Phase 1: store 能力
- [ ] `Store.Remove(id)`、`Store.Extend(id, duration)`
- [ ] 路径校验复用 `resolveArtifact`（防穿越）

### Phase 2: API 与 UI
- [ ] runtime 路由
- [ ] Published 行内操作

## 关键决策

| 决策 | 理由 |
|---|---|
| 延长只改 metadata.ExpiresAt 并重签 | 与 Publish 的 URL 生成共用 `publicURL` |
| 不提供「永久」 | 与现有 MaxRetention 策略一致 |

## 风险

- 已分发出去的旧链接在延长后签名不一致（旧 expires）；需在 UI 说明「只影响新链接」。

## 依赖

- 无（Published 页已存在）

## 相关资源

- `internal/publicartifacts/store.go`、`internal/app/runtime_artifacts.go`

---

## Status 更新日志

- **2026-10-29**: 创建
- **2026-10-29**: 状态变更 → done，备注: v2 交付（217a475）：DELETE /artifacts/{id}（ErrNotFound）、POST /artifacts/{id}/extend（1..720h、MaxRetention 钳制、重签 URL、旧链即失效）；控制台发布页操作已上
