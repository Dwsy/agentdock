---
id: "2026-10-29-fleet-view"
title: "多设备视图（Nexus fleet 聚合）"
status: "todo"
created: "2026-10-29"
updated: "2026-10-29"
category: "桌面端"
tags: ["nexus", "fleet", "multi-device", "priority-p2"]
---

# Issue: 多设备视图（Nexus fleet 聚合）

## Goal

控制面板支持切换/聚合多台已配对设备，查看各节点的任务、调用与发布记录。

## 背景

AgentDock 的核心卖点是多机，但桌面端完全看不到其他节点。`nexusbridge` 已能上报节点信息与调用，UI 没有消费。

## 验收标准

- [ ] 设备切换器（本机 + 已配对节点），显示在线/离线
- [ ] Tasks/Traces/Analytics 支持「当前设备 / 全部设备」两种范围
- [ ] 离线设备显示缓存数据并标注时间，不阻塞界面
- [ ] 跨节点结果带设备标识，避免同名混淆
- [ ] 无 Nexus 配对时隐藏入口，不报错

## 实施阶段

### Phase 1: 设备模型
- [ ] 设备列表与状态来源（本地 identity + Nexus hello）
- [ ] 聚合请求协议（现有 `AgentDockLocalContext` / fleet 聚合）

### Phase 2: UI
- [ ] 切换器与范围过滤
- [ ] 离线降级与时间标注

## 关键决策

| 决策 | 理由 |
|---|---|
| 先只读聚合，不做跨设备写 | 跨节点写需要冲突与审批语义 |
| 复用 Nexus Bridge，不新建通道 | 已有鉴权与重连 |

## 风险

- 跨节点延迟与部分失败会让列表不完整，需要明确「部分结果」提示。

## 依赖

- `console-data-layer`

## 相关资源

- `internal/nexusbridge/`、`docs/self-evolution-architecture.md`

---

## Status 更新日志

- **2026-10-29**: 创建
