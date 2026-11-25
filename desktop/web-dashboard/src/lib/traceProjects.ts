import type { TraceRecord } from '../api';
// 带 .ts 扩展名是为了 node --test 直跑源码（Node ESM 不做扩展名补全）。
import { normalizeProject, projectBasename, UNCLASSIFIED_LABEL } from './taskProjects.ts';
import { traceOutcome } from './traceOutcome.ts';

/** 没有路径信号的调用归到这一组：不猜项目，也不让记录从列表里消失。 */
export const NO_PROJECT_PATH = '';

type TraceProjectPaths = Record<string, string>;

/** 旧 Core 的摘要只有命令和变更路径。 */
export function traceProjectPath(record: TraceRecord, paths?: TraceProjectPaths): string {
  if (paths) return normalizeProject(paths[String(record.id)]);
  const workdir = normalizeProject(record.command?.workdir);
  if (workdir) return workdir;
  for (const change of record.file_changes ?? []) {
    const root = normalizeProject(change.project_root);
    if (root) return root;
  }
  return NO_PROJECT_PATH;
}

export type TraceProjectGroup = {
  key: string;
  /** 显示名：路径末段；同名路径用多段后缀区分。 */
  name: string;
  /** 归一化后的完整路径，未分类时为空。 */
  full: string;
  unclassified: boolean;
  count: number;
  errors: number;
  avgMs: number;
  records: TraceRecord[];
};

/** 同名路径（不同父目录）用递增的路径后缀区分，避免两行都叫 agentdock。 */
function disambiguate(groups: TraceProjectGroup[]): void {
  const byBasename = new Map<string, TraceProjectGroup[]>();
  for (const group of groups) {
    if (group.unclassified) continue;
    const base = projectBasename(group.full);
    const peers = byBasename.get(base);
    if (peers) peers.push(group);
    else byBasename.set(base, [group]);
  }
  for (const peers of byBasename.values()) {
    if (peers.length < 2) continue;
    for (const group of peers) {
      const parts = group.full.split('/');
      for (let depth = 2; depth <= parts.length; depth += 1) {
        const suffix = parts.slice(-depth).join('/');
        const clash = peers.some((other) => other !== group && other.full.split('/').slice(-depth).join('/') === suffix);
        if (!clash) {
          group.name = suffix;
          break;
        }
      }
    }
  }
}

/**
 * 按项目路径给调用分组。同一路径的调用归一组，统计次数、失败数、平均耗时；
 * 按次数降序，未分类固定排最后。分组只做展示投影，不改动记录本身。
 */
export function groupTracesByProject(records: TraceRecord[], paths?: TraceProjectPaths): TraceProjectGroup[] {
  const buckets = new Map<string, TraceProjectGroup>();
  for (const record of records) {
    const full = traceProjectPath(record, paths);
    const key = full || NO_PROJECT_PATH;
    const errors = traceOutcome(record) === 'error' ? 1 : 0;
    const existing = buckets.get(key);
    if (existing) {
      existing.records.push(record);
      existing.count += 1;
      existing.errors += errors;
      existing.avgMs += record.duration_ms;
      continue;
    }
    buckets.set(key, {
      key,
      name: full ? projectBasename(full) : UNCLASSIFIED_LABEL,
      full,
      unclassified: !full,
      count: 1,
      errors,
      avgMs: record.duration_ms,
      records: [record],
    });
  }

  const groups = [...buckets.values()];
  for (const group of groups) group.avgMs = group.avgMs / group.count;
  disambiguate(groups);

  return groups.sort((a, b) => {
    if (a.unclassified !== b.unclassified) return a.unclassified ? 1 : -1;
    if (b.count !== a.count) return b.count - a.count;
    return a.name.localeCompare(b.name, 'zh-CN');
  });
}
