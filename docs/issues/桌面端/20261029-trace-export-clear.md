---
id: "2026-10-29-trace-export-clear"
title: "Trace 导出与清空"
status: "done"
created: "2026-10-29"
updated: "2026-10-29"
category: "桌面端"
tags: ["trace", "export", "write", "priority-p1"]
---

# Issue: Trace 导出与清空

## Goal

把当前筛选结果导出为 JSON/CSV，支持一键清空（内存与磁盘），让排障证据可以带走。

## 背景

trace 目前只能看。磁盘 JSONL 保留 3 天/2000 条，用户无法归档，也无法在敏感调用后立即清除。

## 验收标准

- [ ] `GET /internal/runtime/traces/export?format=json|csv&<同筛选参数>`，返回文件流
- [ ] 导出字段与 UI 一致（tool/source/UA/时间/耗时/成功/错误码/入参/出参/diff）
- [ ] CSV 对含换行/逗号的 payload 正确转义
- [ ] `DELETE /internal/runtime/traces` 清空内存索引与磁盘文件（需二次确认）
- [ ] 清空后文件历史（依赖 trace）同步清空，UI 明确提示
- [ ] 导出与清空都进入审计（清空记录写入一次独立日志）

## 实施阶段

### Phase 1: 导出
- [ ] JSON/CSV 编码
- [ ] 大结果流式写出，不一次性进内存

### Phase 2: 清空
- [ ] 内存索引与磁盘目录清理
- [ ] 与 file-changes 派生数据的一致性

## 关键决策

| 决策 | 理由 |
|---|---|
| 导出走同一筛选契约 | 避免「导出的不是看到的」 |
| 清空必须显式确认 | 不可恢复 |

## 依赖

- 无

## 相关资源

- `internal/observability/trace.go`、`internal/app/runtime_trace_api.go`

---

## Status 更新日志

- **2026-10-29**: 创建
- **2026-10-29**: 状态变更 → done，备注: v2 交付（217a475）：GET /traces/export?format=json|csv（CSV 标准转义、payload 换行转 \n）、DELETE /traces（内存+磁盘一致清空，返回 cleared）；控制台导出/清空已上
