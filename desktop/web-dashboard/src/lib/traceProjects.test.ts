import test from 'node:test';
import assert from 'node:assert/strict';
import type { TraceRecord } from '../api.ts';
import { groupTracesByProject, traceProjectPath } from './traceProjects.ts';

function rec(overrides: Partial<TraceRecord> & Pick<TraceRecord, 'id'>): TraceRecord {
  return {
    tool: 'exec_command',
    source: 'mcp',
    started_at: '2026-01-01T00:00:00Z',
    duration_ms: 100,
    success: true,
    ...overrides,
  };
}

test('exec_command 用工作目录作项目路径', () => {
  assert.equal(traceProjectPath(rec({ id: 1, command: { workdir: '/Users/me/Dev/AI/agentdock' } })), '/Users/me/Dev/AI/agentdock');
  // 尾斜杠与重复分隔符归一化，避免同一项目裂成两组
  assert.equal(traceProjectPath(rec({ id: 2, command: { workdir: '/Users/me//repo/' } })), '/Users/me/repo');
});

test('file_edit 用 project_root 兜底，命令优先', () => {
  const fileOnly = rec({ id: 1, tool: 'file_edit', file_changes: [
    { id: 1, tool: 'file_edit', source: 'mcp', started_at: '', success: true, path: 'a/b.go', action: 'update', project_root: '/Users/me/proj', insertions: 1, diff: '' },
  ] });
  assert.equal(traceProjectPath(fileOnly), '/Users/me/proj');

  const both = rec({ id: 2, command: { workdir: '/Users/me/cmd' }, file_changes: [
    { id: 2, tool: 'file_edit', source: 'mcp', started_at: '', success: true, path: 'a/b.go', action: 'update', project_root: '/Users/me/file', insertions: 1, diff: '' },
  ] });
  assert.equal(traceProjectPath(both), '/Users/me/cmd');
});

test('没有路径信号的调用落未分类并排最后', () => {
  const groups = groupTracesByProject([
    rec({ id: 1, tool: 'read_file' }),
    rec({ id: 2, command: { workdir: '/Users/me/proj' } }),
    rec({ id: 3, tool: 'read_file' }),
  ]);
  assert.equal(groups.length, 2);
  assert.equal(groups[0].full, '/Users/me/proj');
  assert.equal(groups[0].name, 'proj');
  assert.equal(groups[0].count, 1);
  assert.equal(groups[1].unclassified, true);
  assert.equal(groups[1].name, '未分类');
  assert.equal(groups[1].count, 2);
});

test('同一路径归一组并统计次数、失败数、平均耗时', () => {
  const groups = groupTracesByProject([
    rec({ id: 1, command: { workdir: '/w/proj' }, duration_ms: 100 }),
    rec({ id: 2, command: { workdir: '/w/proj/' }, duration_ms: 300, success: false }),
  ]);
  assert.equal(groups.length, 1);
  assert.equal(groups[0].count, 2);
  assert.equal(groups[0].errors, 1);
  assert.equal(groups[0].avgMs, 200);
});

test('同名路径用多段后缀区分，不同名保持末段', () => {
  const groups = groupTracesByProject([
    rec({ id: 1, command: { workdir: '/a/work/agentdock' } }),
    rec({ id: 2, command: { workdir: '/b/oss/agentdock' } }),
    rec({ id: 3, command: { workdir: '/c/unique' } }),
  ]);
  const names = groups.map((g) => g.name).sort();
  assert.deepEqual(names, ['oss/agentdock', 'unique', 'work/agentdock']);
});

test('按次数降序排列，未分类固定最后', () => {
  const groups = groupTracesByProject([
    rec({ id: 1, tool: 'read_file' }),
    rec({ id: 2, command: { workdir: '/a/one' } }),
    rec({ id: 3, command: { workdir: '/b/two' } }),
    rec({ id: 4, command: { workdir: '/b/two' } }),
  ]);
  assert.deepEqual(groups.map((g) => g.full), ['/b/two', '/a/one', '']);
});
