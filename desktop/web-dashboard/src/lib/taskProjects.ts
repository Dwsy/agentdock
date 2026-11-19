import type { Task } from '../api';

/** 任务列表一次拉全量对应的接口硬上限（与后端 taskstate.MaxListLimit 一致）。 */
export const TASK_FETCH_LIMIT = 10000;

export const ALL_PROJECTS = '__all_projects__';
export const UNCLASSIFIED_PROJECT = '';
export const UNCLASSIFIED_LABEL = '未分类';

export type TaskStatusKind = 'active' | 'completed' | 'blocked' | 'pending' | 'unknown';
export type StatusFilter = TaskStatusKind | 'all';

export type TaskCounts = {
  total: number;
  active: number;
  completed: number;
  blocked: number;
  pending: number;
  unknown: number;
};

export type ProjectGroup = {
  key: string;
  name: string;
  full: string;
  isPath: boolean;
  unclassified: boolean;
  tasks: Task[];
  counts: TaskCounts;
  /** 0 = 有进行中，1 = 有阻塞，2 = 其余；未分类固定排最后。 */
  priority: number;
};

export type TaskFilters = {
  projectKey: string;
  query: string;
  status: StatusFilter;
};

export type TaskScope = {
  scoped: Task[];
  /** 项目 + 搜索后的集合，状态计数与状态筛选都以它为同一来源。 */
  searched: Task[];
  visible: Task[];
  statusCounts: TaskCounts;
  groups: { group: ProjectGroup; tasks: Task[] }[];
};

/**
 * 状态只认后端约定的四个值。其余值保留原文展示，避免把未知状态
 * 硬塞进 pending 或 active 而误导执行中的判断。
 */
export function classifyStatus(raw: string | undefined): { kind: TaskStatusKind; label: string } {
  const text = (raw ?? '').trim();
  switch (text.toLowerCase()) {
    case 'active':
      return { kind: 'active', label: '进行中' };
    case 'completed':
      return { kind: 'completed', label: '已完成' };
    case 'blocked':
      return { kind: 'blocked', label: '阻塞' };
    case 'pending':
      return { kind: 'pending', label: '待处理' };
    default:
      return { kind: 'unknown', label: text || '未知' };
  }
}

/** trim、统一路径分隔符、折叠重复分隔符、去掉尾斜杠（根路径除外）。 */
export function normalizeProject(raw: string | undefined): string {
  let value = (raw ?? '').trim().replace(/\\/g, '/');
  if (!value) return '';
  const unc = value.startsWith('//') && !value.startsWith('///');
  value = value.replace(/\/{2,}/g, '/');
  if (unc) value = `/${value}`;
  if (!/^(?:\/{1,2}|[A-Za-z]:\/)$/.test(value)) value = value.replace(/\/+$/, '');
  return value;
}

export function projectBasename(value: string): string {
  if (!value) return UNCLASSIFIED_LABEL;
  if (value === '/' || value === '//' || /^[A-Za-z]:\/$/.test(value)) return value;
  const index = value.lastIndexOf('/');
  return index >= 0 ? value.slice(index + 1) : value;
}

export function countTasks(tasks: Task[]): TaskCounts {
  const counts: TaskCounts = { total: 0, active: 0, completed: 0, blocked: 0, pending: 0, unknown: 0 };
  for (const task of tasks) {
    counts.total += 1;
    counts[classifyStatus(task.status).kind] += 1;
  }
  return counts;
}

function projectPriority(counts: TaskCounts): number {
  if (counts.active > 0) return 0;
  if (counts.blocked > 0) return 1;
  return 2;
}

const TASK_STATUS_RANK: Record<TaskStatusKind, number> = {
  active: 0,
  blocked: 1,
  pending: 2,
  unknown: 3,
  completed: 4,
};

function updatedAt(task: Task): number {
  const value = Date.parse(task.updated_at);
  return Number.isNaN(value) ? 0 : value;
}

function sortTasks(tasks: Task[]): Task[] {
  return [...tasks].sort((a, b) => {
    const rank = TASK_STATUS_RANK[classifyStatus(a.status).kind] - TASK_STATUS_RANK[classifyStatus(b.status).kind];
    if (rank !== 0) return rank;
    const time = updatedAt(b) - updatedAt(a);
    if (time !== 0) return time;
    return a.title.localeCompare(b.title, 'zh-CN');
  });
}

/**
 * 按 project 分组。只有“slug 与唯一一条完整路径的 basename 相同”时才合并；
 * 同一 basename 的多条路径、worktree/review 等子目录都保持各自独立，
 * 避免把同名项目或同一仓库的不同工作树误并成一个。
 */
export function buildProjectGroups(tasks: Task[]): ProjectGroup[] {
  type Bucket = { key: string; value: string; isPath: boolean; tasks: Task[] };
  const buckets = new Map<string, Bucket>();

  for (const task of tasks) {
    const value = normalizeProject(task.project);
    const key = value || UNCLASSIFIED_PROJECT;
    const existing = buckets.get(key);
    if (existing) existing.tasks.push(task);
    else buckets.set(key, { key, value, isPath: value.includes('/'), tasks: [task] });
  }

  const pathsByBasename = new Map<string, Set<string>>();
  for (const bucket of buckets.values()) {
    if (!bucket.isPath) continue;
    const name = projectBasename(bucket.value);
    const paths = pathsByBasename.get(name);
    if (paths) paths.add(bucket.value);
    else pathsByBasename.set(name, new Set([bucket.value]));
  }

  for (const bucket of [...buckets.values()]) {
    if (bucket.isPath || !bucket.value) continue;
    const candidates = pathsByBasename.get(bucket.value);
    if (!candidates || candidates.size !== 1) continue;
    const target = buckets.get([...candidates][0]);
    if (!target) continue;
    target.tasks.push(...bucket.tasks);
    buckets.delete(bucket.key);
  }

  const groups: ProjectGroup[] = [...buckets.values()].map((bucket) => {
    const counts = countTasks(bucket.tasks);
    return {
      key: bucket.key,
      name: bucket.value ? projectBasename(bucket.value) : UNCLASSIFIED_LABEL,
      full: bucket.value,
      isPath: bucket.isPath,
      unclassified: !bucket.value,
      tasks: sortTasks(bucket.tasks),
      counts,
      priority: projectPriority(counts),
    };
  });

  for (const group of groups) {
    // 非路径项目（bare slug）保留 basename 即可，父路径后缀只用于区分路径项目。
    if (group.unclassified || !group.isPath) continue;
    const basename = projectBasename(group.full);
    const peers = groups.filter((other) => !other.unclassified && projectBasename(other.full) === basename);
    if (peers.length < 2) continue;
    const parts = group.full.split('/');
    for (let depth = 2; depth <= parts.length; depth += 1) {
      const suffix = parts.slice(-depth).join('/');
      if (depth === parts.length || peers.every((other) => other === group || !other.isPath || other.full.split('/').slice(-depth).join('/') !== suffix)) {
        group.name = suffix;
        break;
      }
    }
  }

  return groups.sort((a, b) => {
    if (a.unclassified !== b.unclassified) return a.unclassified ? 1 : -1;
    if (a.priority !== b.priority) return a.priority - b.priority;
    if (b.counts.total !== a.counts.total) return b.counts.total - a.counts.total;
    return a.name.localeCompare(b.name, 'zh-CN');
  });
}

/** 刷新后保留选择；slug 合并进唯一项目路径时跟随其所属组。 */
export function resolveSelectedProjectKey(groups: ProjectGroup[], oldKey: string): string {
  if (oldKey === ALL_PROJECTS) return ALL_PROJECTS;
  const key = normalizeProject(oldKey);
  const existing = groups.find((group) => group.key === key);
  if (existing) return existing.key;

  const aliases = groups.filter((group) => group.tasks.some((task) => normalizeProject(task.project) === key));
  if (aliases.length === 1) return aliases[0].key;
  if (key && !key.includes('/')) {
    const candidates = groups.filter((group) => group.isPath && projectBasename(group.full) === key);
    if (candidates.length === 1) return candidates[0].key;
  }
  return ALL_PROJECTS;
}

export function taskMatchesQuery(task: Task, query: string): boolean {
  const needle = query.trim().toLowerCase();
  if (!needle) return true;
  return [task.title, task.summary, task.goal, task.project, normalizeProject(task.project)].some((value) =>
    (value ?? '').toLowerCase().includes(needle),
  );
}

/**
 * 项目选择、搜索、状态筛选走同一条链路：项目决定范围，搜索决定状态计数的
 * 分母，状态只裁列表。这样计数、筛选按钮和列表不会互相漂移。
 */
export function scopeTasks(groups: ProjectGroup[], filters: TaskFilters): TaskScope {
  const selected =
    filters.projectKey === ALL_PROJECTS ? groups : groups.filter((group) => group.key === filters.projectKey);
  const searchedByGroup = selected.map((group) => ({
    group,
    tasks: group.tasks.filter((task) => taskMatchesQuery(task, filters.query)),
  }));
  const searched = searchedByGroup.flatMap((entry) => entry.tasks);
  const statusCounts = countTasks(searched);
  const visible = filters.status === 'all'
    ? searched
    : searched.filter((task) => classifyStatus(task.status).kind === filters.status);

  const groupsOut = searchedByGroup
    .map((entry) => ({
      group: entry.group,
      tasks: filters.status === 'all'
        ? entry.tasks
        : entry.tasks.filter((task) => classifyStatus(task.status).kind === filters.status),
    }))
    .filter((entry) => entry.tasks.length > 0);

  return { scoped: selected.flatMap((group) => group.tasks), searched, visible, statusCounts, groups: groupsOut };
}

/** 列表是否达到接口硬上限（正常全量场景不会触发）。 */
export function isTaskListCapped(loaded: number, limit = TASK_FETCH_LIMIT): boolean {
  return loaded >= limit;
}
