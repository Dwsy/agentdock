import test from 'node:test';
import assert from 'node:assert/strict';
import {
  classifyPreviewKind,
  formatJsonPreview,
  isLikelyBinaryBytes,
  parseDelimitedTable,
  previewCategory,
  remainingTimeLabel,
} from './filePreview.ts';

test('classifyPreviewKind 能准确区分常见格式并受 archive 标记约束', () => {
  const cases: [string, string, boolean, string, string][] = [
    ['shot.png', 'image/png', false, 'image', 'image'],
    ['diagram.svg', 'application/octet-stream', false, 'svg', 'image'],
    ['notes.md', 'text/plain; charset=utf-8', false, 'markdown', 'doc'],
    ['data.json', 'application/json', false, 'json', 'doc'],
    ['events.jsonl', 'application/octet-stream', false, 'json', 'doc'],
    ['report.csv', 'text/plain', false, 'csv', 'doc'],
    ['table.tsv', 'text/tab-separated-values', false, 'csv', 'doc'],
    ['manual.pdf', 'application/pdf', false, 'pdf', 'doc'],
    ['demo.mp4', 'video/mp4', false, 'video', 'media'],
    ['voice.mp3', 'audio/mpeg', false, 'audio', 'media'],
    ['main.ts', 'application/octet-stream', false, 'code', 'doc'],
    ['README', 'text/plain; charset=utf-8', false, 'code', 'doc'],
    ['bundle.tar.gz', 'application/gzip', true, 'binary', 'other'],
    ['archive.zip', 'application/zip', false, 'binary', 'other'],
    ['unknown.bin', 'application/octet-stream', false, 'binary', 'other'],
  ];
  for (const [name, mime, archive, wantKind, wantCat] of cases) {
    const kind = classifyPreviewKind(name, mime, archive);
    assert.equal(kind, wantKind, `${name} (${mime})`);
    assert.equal(previewCategory(kind), wantCat, `${name} category`);
  }
});

test('isLikelyBinaryBytes 检测空字节而不误杀 UTF-8 中文文本', () => {
  const utf8Text = new TextEncoder().encode('# 中文标题\nHello world\n');
  assert.equal(isLikelyBinaryBytes(utf8Text), false);

  const bin = new Uint8Array([0x50, 0x4b, 0x03, 0x04, 0x00, 0x10]);
  assert.equal(isLikelyBinaryBytes(bin), true);
  assert.equal(isLikelyBinaryBytes(bin, 4), false);
});

test('parseDelimitedTable 支持引号转义、单元格换行与行数上限', () => {
  const csv = '\uFEFFname,note,score\n"Alice","line1\nline2",95\n"Bob ""B""",ok,88\n';
  const table = parseDelimitedTable(csv, ',', 1);
  assert.deepEqual(table.headers, ['name', 'note', 'score']);
  assert.equal(table.totalRows, 2);
  assert.equal(table.truncated, true);
  assert.deepEqual(table.rows, [['Alice', 'line1\nline2', '95']]);

  const tsv = parseDelimitedTable('col1\tcol2\nv1\tv2\n', '\t');
  assert.equal(tsv.truncated, false);
  assert.deepEqual(tsv.rows, [['v1', 'v2']]);
});

test('formatJsonPreview 格式化合法 JSON/JSONL 并在截断时回退原文', () => {
  const okJson = formatJsonPreview('{"a":1,"b":[2,3]}', 'cfg.json');
  assert.equal(okJson.valid, true);
  assert.ok(okJson.formatted.includes('\n  "a": 1'));

  const okJsonl = formatJsonPreview('{"id":1}\n{"id":2}\n', 'trace.jsonl');
  assert.equal(okJsonl.valid, true);
  assert.ok(okJsonl.formatted.includes('"id": 2'));

  const broken = formatJsonPreview('{"a":1', 'broken.json');
  assert.equal(broken.valid, false);
  assert.equal(broken.formatted, '{"a":1');
});

test('remainingTimeLabel 正确计算剩余时间、即将过期与已过期状态', () => {
  const now = Date.UTC(2026, 10, 18, 12, 0, 0);
  assert.deepEqual(remainingTimeLabel(new Date(now - 1000).toISOString(), now), {
    label: '已过期',
    expired: true,
    soon: false,
  });
  assert.deepEqual(remainingTimeLabel(new Date(now + 45 * 60_000).toISOString(), now), {
    label: '45 分钟',
    expired: false,
    soon: true,
  });
  assert.deepEqual(remainingTimeLabel(new Date(now + 5 * 3_600_000 + 20 * 60_000).toISOString(), now), {
    label: '5 小时 20 分',
    expired: false,
    soon: false,
  });
  assert.deepEqual(remainingTimeLabel(new Date(now + 2 * 86_400_000 + 3 * 3_600_000).toISOString(), now), {
    label: '2 天 3 小时',
    expired: false,
    soon: false,
  });
});
