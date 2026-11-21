import type { FileChange } from '../api';
// 带 .ts 扩展名是为了 node --test 直跑源码（Node ESM 不做扩展名补全）；tsconfig 已开启 allowImportingTsExtensions。
import { normalizeProject, projectBasename, UNCLASSIFIED_LABEL, type ProjectGroup } from './taskProjects.ts';

/** 同一次调用可能产生多条文件变更，服务端 ID 是调用级的。 */
export function fileChangeKey(change: FileChange): string {
  return JSON.stringify([change.id, change.path, change.action]);
}

/** 一个项目下的文件变更汇总；key 与 taskProjects 的项目 key 同源，直接复用。 */
export type ChangeProjectGroup = {
  key: string;
  name: string;
  full: string;
  matched: boolean;
  changes: FileChange[];
  insertions: number;
  deletions: number;
};

/**
 * 路径比较一律折叠大小写：任务表里的 project 会被后端小写化，
 * 文件变更的 path / project_root 保留原样，直接比会全部落空。
 * （Linux 上同名不同大小写的目录会被并到一起，相对少见，可接受。）
 */
function foldPath(value: string | undefined): string {
  return normalizeProject(value).toLowerCase();
}

function exactRoot(change: FileChange, groups: ProjectGroup[]): ProjectGroup | null {
  const root = foldPath(change.project_root);
  if (!root) return null;
  return groups.find((group) => group.isPath && foldPath(group.full) === root) ?? null;
}

/** 文件路径可能落在项目子目录里，取最长前缀，避免父目录项目抢走子项目文件。 */
function longestPathPrefix(path: string, groups: ProjectGroup[]): ProjectGroup | null {
  const target = foldPath(path);
  if (!target) return null;
  let best: ProjectGroup | null = null;
  for (const group of groups) {
    if (!group.isPath || !group.full) continue;
    const prefix = foldPath(group.full);
    if (target !== prefix && !target.startsWith(`${prefix}/`)) continue;
    if (!best || prefix.length > foldPath(best.full).length) best = group;
  }
  return best;
}

/**
 * 兜底按项目末段名认领 project_root：文件路径是相对路径、项目是绝对路径时，
 * 前缀匹配天然对不上，但仓库根名字通常还对得上。同名项目多于一个就不认领，
 * 宁可落「未分类」也不猜。
 */
function uniqueRootBasename(change: FileChange, groups: ProjectGroup[]): ProjectGroup | null {
  const root = foldPath(change.project_root);
  if (!root) return null;
  const base = projectBasename(root);
  const candidates = groups.filter((group) => group.full && projectBasename(foldPath(group.full)) === base);
  return candidates.length === 1 ? candidates[0] : null;
}

function matchProject(change: FileChange, groups: ProjectGroup[]): ProjectGroup | null {
  return exactRoot(change, groups)
    ?? longestPathPrefix(change.path, groups)
    ?? uniqueRootBasename(change, groups);
}

function emptyGroup(key: string, name: string, full: string, matched: boolean): ChangeProjectGroup {
  return { key, name, full, matched, changes: [], insertions: 0, deletions: 0 };
}

function append(group: ChangeProjectGroup, change: FileChange): void {
  group.changes.push(change);
  group.insertions += change.insertions || 0;
  group.deletions += change.deletions || 0;
}

/**
 * 按任务项目路径给文件变更分组。项目清单来自任务列表（task.project），
 * 一条变更只归一个项目：project_root 精确相等 → 路径最长前缀 → slug 末段名。
 * 都匹配不上的落「未分类」，不能因为没项目就把记录藏起来。
 */
export function groupChangesByProject(changes: FileChange[], groups: ProjectGroup[]): ChangeProjectGroup[] {
  const buckets = new Map<string, ChangeProjectGroup>();
  const unclassified = emptyGroup('', UNCLASSIFIED_LABEL, '', false);

  for (const change of changes) {
    const project = matchProject(change, groups);
    if (!project) {
      append(unclassified, change);
      continue;
    }
    const bucket = buckets.get(project.key) ?? emptyGroup(project.key, project.name, project.full, true);
    append(bucket, change);
    buckets.set(project.key, bucket);
  }

  const result = [...buckets.values()].sort((a, b) => {
    if (b.changes.length !== a.changes.length) return b.changes.length - a.changes.length;
    return a.name.localeCompare(b.name, 'zh-CN');
  });
  if (unclassified.changes.length > 0) result.push(unclassified);
  return result;
}

/** 项目模式下显示相对项目根的路径，避免每行都重复同一段前缀。 */
export function changeDisplayPath(change: FileChange, group: ChangeProjectGroup): string {
  const path = normalizeProject(change.path);
  if (!group.full) return change.path;
  const folded = foldPath(path);
  const prefix = foldPath(group.full);
  if (folded === prefix) return change.path;
  if (folded.startsWith(`${prefix}/`)) return path.slice(prefix.length + 1) || change.path;

  // 变更路径是相对路径、项目是绝对路径时前缀对不上：按项目末段名找到那一段，
  // 只显示它下面的部分，避免每行都重复一遍项目名。
  const segments = path.split('/');
  const base = projectBasename(prefix);
  const index = base ? segments.findIndex((segment) => segment.toLowerCase() === base) : -1;
  if (index >= 0 && index < segments.length - 1) return segments.slice(index + 1).join('/');
  return change.path;
}
