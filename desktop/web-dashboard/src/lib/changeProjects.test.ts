import test from 'node:test';
import assert from 'node:assert/strict';
import type { FileChange } from '../api.ts';
import { buildProjectGroups } from './taskProjects.ts';
import { changeDisplayPath, groupChangesByProject } from './changeProjects.ts';

function change(overrides: Partial<FileChange> & Pick<FileChange, 'id' | 'path'>): FileChange {
  return {
    tool: 'file_edit',
    source: 'mcp',
    started_at: '2026-01-01T00:00:00Z',
    success: true,
    action: 'update',
    project_root: '',
    insertions: 1,
    deletions: 0,
    diff: '',
    ...overrides,
  };
}

function projects(...values: string[]) {
  return buildProjectGroups(values.map((project, index) => ({
    id: `tsk_${index}`,
    title: `任务 ${index}`,
    goal: '',
    status: 'active',
    phase: '',
    review_status: '',
    project,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    step_count: 0,
    completed_step_count: 0,
    event_count: 0,
  })));
}

test('project_root 精确命中任务项目路径', () => {
  const groups = projects('/Users/me/Dev/AI/agentdock');
  const out = groupChangesByProject([
    change({ id: 1, path: '/Users/me/Dev/AI/agentdock/internal/app/runtime.go', project_root: '/Users/me/Dev/AI/agentdock', insertions: 4, deletions: 2 }),
  ], groups);
  assert.equal(out.length, 1);
  assert.equal(out[0].full, '/Users/me/Dev/AI/agentdock');
  assert.equal(out[0].name, 'agentdock');
  assert.equal(out[0].insertions, 4);
  assert.equal(out[0].deletions, 2);
});

test('嵌套项目取最长前缀，父目录项目不抢子项目文件', () => {
  const groups = projects('/Users/me/Dev/AI', '/Users/me/Dev/AI/agentdock');
  const out = groupChangesByProject([
    change({ id: 1, path: '/Users/me/Dev/AI/agentdock/main.go' }),
    change({ id: 2, path: '/Users/me/Dev/AI/other/tool.go' }),
  ], groups);
  const byFull = Object.fromEntries(out.map((group) => [group.full, group.changes.map((c) => c.id)]));
  assert.deepEqual(byFull['/Users/me/Dev/AI/agentdock'], [1]);
  assert.deepEqual(byFull['/Users/me/Dev/AI'], [2]);
});

test('任务 project 被小写化、变更路径保留大小写时仍能归组', () => {
  // 真实后端会把 task.project 小写化，而文件路径原样保留。
  const groups = projects('/tmp/workspace/proja');
  const out = groupChangesByProject([
    change({ id: 1, path: 'projA/main.go', project_root: 'projA' }),
  ], groups);
  assert.equal(out[0].matched, true);
  assert.equal(out[0].full, '/tmp/workspace/proja');
  // 相对路径前缀对不上绝对项目路径，按末段名裁掉重复的项目名。
  assert.equal(changeDisplayPath(out[0].changes[0], out[0]), 'main.go');
});

test('slug 项目靠 project_root 末段名认领，同名多个项目时不认领', () => {
  const unique = projects('agentdock');
  const hit = groupChangesByProject([
    change({ id: 1, path: 'internal/app/runtime.go', project_root: '/Users/me/Dev/AI/agentdock' }),
  ], unique);
  assert.equal(hit[0].matched, true);
  assert.equal(hit[0].full, 'agentdock');
  assert.equal(changeDisplayPath(hit[0].changes[0], hit[0]), 'internal/app/runtime.go');

  const ambiguous = projects('/a/one', '/b/two');
  const miss = groupChangesByProject([
    change({ id: 2, path: 'x.go', project_root: '/Users/me/Dev/AI/agentdock' }),
  ], ambiguous);
  assert.equal(miss.length, 1);
  assert.equal(miss[0].matched, false);
});

test('匹配不上的变更落未分类并排在最后', () => {
  const groups = projects('/Users/me/Dev/AI/agentdock');
  const out = groupChangesByProject([
    change({ id: 1, path: 'scratch/notes.md', project_root: '.', insertions: 3, deletions: 1 }),
    change({ id: 2, path: '/Users/me/Dev/AI/agentdock/README.md', project_root: '/Users/me/Dev/AI/agentdock', insertions: 2 }),
  ], groups);
  assert.equal(out.length, 2);
  assert.equal(out[0].matched, true);
  assert.equal(out[1].matched, false);
  assert.equal(out[1].name, '未分类');
  assert.deepEqual(out[1].changes.map((c) => c.id), [1]);
  assert.equal(out[1].insertions, 3);
  assert.equal(out[1].deletions, 1);
});

test('没有任务项目时所有变更进未分类，不丢记录', () => {
  const out = groupChangesByProject([
    change({ id: 1, path: 'a/b.go' }),
    change({ id: 2, path: 'c/d.go' }),
  ], []);
  assert.equal(out.length, 1);
  assert.equal(out[0].changes.length, 2);
  assert.equal(out[0].matched, false);
});

test('相对路径前缀也能归到项目', () => {
  const groups = projects('/Users/me/proj');
  const hit = groupChangesByProject([
    change({ id: 1, path: '/Users/me/proj/web/src/App.tsx', project_root: '/Users/me/proj' }),
  ], groups);
  assert.equal(hit[0].matched, true);
  assert.equal(changeDisplayPath(hit[0].changes[0], hit[0]), 'web/src/App.tsx');
});
