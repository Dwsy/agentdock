import test from 'node:test';
import assert from 'node:assert/strict';
import type { Task } from '../api.ts';
import {
  ALL_PROJECTS,
  TASK_FETCH_LIMIT,
  buildProjectGroups,
  classifyStatus,
  countTasks,
  isTaskListCapped,
  normalizeProject,
  projectBasename,
  resolveSelectedProjectKey,
  scopeTasks,
  taskMatchesQuery,
  UNCLASSIFIED_LABEL,
} from './taskProjects.ts';

function task(overrides: Partial<Task> & Pick<Task, 'id'>): Task {
  return {
    title: overrides.title ?? overrides.id,
    goal: overrides.goal ?? '',
    status: overrides.status ?? 'active',
    phase: overrides.phase ?? '',
    review_status: overrides.review_status ?? 'not_started',
    created_at: overrides.created_at ?? '2026-11-17T08:00:00Z',
    updated_at: overrides.updated_at ?? '2026-11-17T09:00:00Z',
    step_count: overrides.step_count ?? 0,
    completed_step_count: overrides.completed_step_count ?? 0,
    event_count: overrides.event_count ?? 0,
    ...overrides,
    id: overrides.id,
  };
}

test('normalizeProject 统一 trim、分隔符、重复斜杠与尾斜杠', () => {
  assert.equal(normalizeProject('  /Users/me//repo/  '), '/Users/me/repo');
  assert.equal(normalizeProject('C:\\work\\repo\\'), 'C:/work/repo');
  assert.equal(normalizeProject('   '), '');
  assert.equal(normalizeProject('/'), '/');
});

test('UNC、POSIX 与盘符根保持不同身份和可见文本', () => {
  assert.equal(normalizeProject(' \\\\server\\share\\repo\\ '), '//server/share/repo');
  assert.equal(normalizeProject('//server//share/repo/'), '//server/share/repo');
  assert.equal(normalizeProject('/server//share/repo/'), '/server/share/repo');
  assert.equal(normalizeProject('C:\\'), 'C:/');
  assert.equal(normalizeProject('C:////'), 'C:/');
  assert.equal(normalizeProject('C:'), 'C:');
  assert.equal(projectBasename('C:/'), 'C:/');
  const groups = buildProjectGroups([
    task({ id: 'unc', project: '//server/share/repo' }),
    task({ id: 'posix', project: '/server/share/repo' }),
    task({ id: 'root', project: 'C:/' }),
    task({ id: 'relative', project: 'C:' }),
  ]);
  assert.equal(groups.length, 4);
  assert.equal(groups.find((group) => group.key === '//server/share/repo')?.name, '//server/share/repo');
  assert.equal(groups.find((group) => group.key === '/server/share/repo')?.name, '/server/share/repo');
  assert.equal(new Set(groups.map((group) => group.name)).size, 4);
});

test('重名移动端文本使用最短可区分父路径后缀并保留真实项目', () => {
  const projects = ['/home/alice/work/repo', '/home/bob/work/repo', '/home/other/repo', 'repo'];
  const groups = buildProjectGroups(projects.map((project) => task({ id: project, project })));
  const byKey = new Map(groups.map((group) => [group.key, group]));
  assert.equal(byKey.get(projects[0])?.name, 'alice/work/repo');
  assert.equal(byKey.get(projects[1])?.name, 'bob/work/repo');
  assert.equal(byKey.get(projects[2])?.name, 'other/repo');
  assert.equal(byKey.get('repo')?.name, 'repo');
  assert.equal(new Set(groups.map((group) => group.name)).size, 4);
  for (const project of projects) assert.equal(byKey.get(project)?.full, project);
});

test('原 slug 选择在刷新合并后迁移到路径，筛选仍保留任务', () => {
  const original = task({ id: 'slug', project: 'repo' });
  const before = buildProjectGroups([original]);
  const selected = resolveSelectedProjectKey(before, 'repo');
  const after = buildProjectGroups([original, task({ id: 'path', project: '/work/repo' })]);
  const migrated = resolveSelectedProjectKey(after, selected);
  assert.equal(migrated, '/work/repo');
  assert.deepEqual(scopeTasks(after, { projectKey: migrated, query: '', status: 'all' }).visible.map((row) => row.id).sort(), ['path', 'slug']);
  assert.equal(resolveSelectedProjectKey(after, '/work/repo'), '/work/repo');
  assert.equal(resolveSelectedProjectKey(after, ALL_PROJECTS), ALL_PROJECTS);
  assert.equal(resolveSelectedProjectKey(after, 'missing'), ALL_PROJECTS);
  assert.equal(resolveSelectedProjectKey(buildProjectGroups([task({ id: 'path', project: '/work/repo' })]), 'repo'), '/work/repo');
});

test('选择迁移不猜同名路径或 worktree 根归属，并保留独立 slug 选择', () => {
  const tasks = [
    task({ id: 'a', project: '/one/repo' }),
    task({ id: 'b', project: '/two/repo' }),
    task({ id: 'tree', project: '/work/root/worktrees/feat' }),
  ];
  const groups = buildProjectGroups(tasks);
  assert.equal(resolveSelectedProjectKey(groups, 'repo'), ALL_PROJECTS);
  assert.equal(resolveSelectedProjectKey(groups, '/work/root'), ALL_PROJECTS);
  assert.equal(resolveSelectedProjectKey(groups, 'root'), ALL_PROJECTS);
  assert.equal(resolveSelectedProjectKey(buildProjectGroups([...tasks, task({ id: 'slug', project: 'repo' })]), 'repo'), 'repo');
});

test('slug 只在唯一对应完整路径时合并', () => {
  const groups = buildProjectGroups([
    task({ id: 'a', project: 'agentdock' }),
    task({ id: 'b', project: '/Users/me/work/agentdock' }),
  ]);
  assert.equal(groups.length, 1);
  assert.equal(groups[0].key, '/Users/me/work/agentdock');
  assert.equal(groups[0].full, '/Users/me/work/agentdock');
  assert.equal(groups[0].name, 'agentdock');
  assert.equal(groups[0].counts.total, 2);
});

test('同 basename 的多条路径不合并，slug 也不并入任何一条', () => {
  const groups = buildProjectGroups([
    task({ id: 'a', project: '/Users/me/work/agentdock' }),
    task({ id: 'b', project: '/Users/me/other/agentdock' }),
    task({ id: 'c', project: 'agentdock' }),
  ]);
  assert.equal(groups.length, 3);
  assert.deepEqual(
    groups.map((group) => group.key).sort(),
    ['/Users/me/other/agentdock', '/Users/me/work/agentdock', 'agentdock'],
  );
  assert.ok(groups.every((group) => group.counts.total === 1));
});

test('worktree、review 与子目录保持独立项目', () => {
  const groups = buildProjectGroups([
    task({ id: 'a', project: '/repo/agentdock' }),
    task({ id: 'b', project: '/repo/agentdock/worktrees/feat' }),
    task({ id: 'c', project: '/repo/agentdock/review/pr-1' }),
    task({ id: 'd', project: '/repo/agentdock/internal/app' }),
  ]);
  assert.equal(groups.length, 4);
  assert.deepEqual(
    groups.map((group) => group.name).sort(),
    ['agentdock', 'app', 'feat', 'pr-1'],
  );
});

test('空 project 归未分类并排在最后', () => {
  const groups = buildProjectGroups([
    task({ id: 'a', project: '' }),
    task({ id: 'b', project: '   ' }),
    task({ id: 'c', project: '/repo/done', status: 'completed' }),
  ]);
  const unclassified = groups[groups.length - 1];
  assert.equal(unclassified.name, UNCLASSIFIED_LABEL);
  assert.equal(unclassified.unclassified, true);
  assert.equal(unclassified.counts.total, 2);
  assert.equal(groups[0].key, '/repo/done');
});

test('状态计数与状态筛选同源且 active 不等同 pending', () => {
  const tasks = [
    task({ id: 'a', status: 'active' }),
    task({ id: 'b', status: 'pending' }),
    task({ id: 'c', status: 'blocked' }),
    task({ id: 'd', status: 'completed' }),
    task({ id: 'e', status: 'in_progress' }),
  ];
  const groups = buildProjectGroups(tasks);
  const scope = scopeTasks(groups, { projectKey: ALL_PROJECTS, query: '', status: 'all' });
  assert.deepEqual(scope.statusCounts, countTasks(tasks));
  assert.equal(scope.statusCounts.active, 1);
  assert.equal(scope.statusCounts.pending, 1);
  assert.equal(scope.statusCounts.unknown, 1);
  for (const kind of ['active', 'pending', 'blocked', 'completed', 'unknown'] as const) {
    const filtered = scopeTasks(groups, { projectKey: ALL_PROJECTS, query: '', status: kind });
    assert.equal(filtered.visible.length, scope.statusCounts[kind], `${kind} 计数与筛选结果一致`);
    assert.equal(filtered.groups.flatMap((entry) => entry.tasks).length, filtered.visible.length);
  }
  assert.equal(scope.visible.length, tasks.length);
});

test('项目选择后搜索只影响该项目的状态计数与列表', () => {
  const groups = buildProjectGroups([
    task({ id: 'a', project: '/repo/one', status: 'active', summary: '隧道重连' }),
    task({ id: 'b', project: '/repo/one', status: 'completed', summary: '隧道文档' }),
    task({ id: 'c', project: '/repo/two', status: 'active', summary: '隧道重连' }),
  ]);
  const scope = scopeTasks(groups, { projectKey: '/repo/one', query: '隧道', status: 'all' });
  assert.equal(scope.scoped.length, 2);
  assert.equal(scope.searched.length, 2);
  assert.equal(scope.statusCounts.total, 2);
  assert.equal(scope.statusCounts.active, 1);
  const active = scopeTasks(groups, { projectKey: '/repo/one', query: '隧道', status: 'active' });
  assert.deepEqual(active.visible.map((item) => item.id), ['a']);
});

test('搜索覆盖标题、摘要、目标与原始 project 路径', () => {
  const row = task({
    id: 'a',
    project: '/Users/me/work/agentdock',
    title: '修复隧道',
    summary: '重连',
    goal: '恢复连接',
  });
  assert.equal(taskMatchesQuery(row, 'agentdock'), true);
  assert.equal(taskMatchesQuery(row, '/users/me/work'), true);
  assert.equal(taskMatchesQuery(row, '重连'), true);
  assert.equal(taskMatchesQuery(row, '恢复'), true);
  assert.equal(taskMatchesQuery(row, '不存在'), false);
  assert.equal(taskMatchesQuery(row, '   '), true);
});

test('做任务的项目优先于仅完成/其他项目', () => {
  const groups = buildProjectGroups([
    task({ id: 'a', project: '/repo/done', status: 'completed' }),
    task({ id: 'b', project: '/repo/active', status: 'active' }),
    task({ id: 'c', project: '/repo/blocked', status: 'blocked' }),
    task({ id: 'd', project: '' }),
  ]);
  assert.deepEqual(groups.map((group) => group.name), ['active', 'blocked', 'done', UNCLASSIFIED_LABEL]);
});

test('未知状态保留原文作为标签', () => {
  assert.deepEqual(classifyStatus('in_progress'), { kind: 'unknown', label: 'in_progress' });
  assert.deepEqual(classifyStatus('  '), { kind: 'unknown', label: '未知' });
  assert.deepEqual(classifyStatus('Active'), { kind: 'active', label: '进行中' });
});

test('达到接口上限时标记为最近 N 条', () => {
  assert.equal(isTaskListCapped(TASK_FETCH_LIMIT), true);
  assert.equal(isTaskListCapped(TASK_FETCH_LIMIT - 1), false);
});
