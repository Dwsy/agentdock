import assert from 'node:assert/strict';
import test from 'node:test';
import type { FileChange, TraceDetail, TraceRecord } from '../api';
import { fileDiffPreview } from './diffPreview.ts';
import { hasTruncatedPreview, loadTraceDetail, traceChangeDiff, traceFileChangeCounts, traceFileDiff } from './traceDetail.ts';

const summary: TraceRecord = {
  id: 17, tool: 'generic_tool', source: 'test', started_at: '', duration_ms: 0, success: true,
};
const raw = '{ "id": 90071992547409931234, "id": "[REDACTED]" }';
const preview: TraceDetail = {
  ok: true, source: 'test', record: { ...summary, arguments: '{ "id":…(truncated)' },
};
const full: TraceDetail = { ...preview, full: { arguments: raw }, full_available: true };

for (const [name, record, expected] of [
  ['summary retains truncation for a generic tool', { ...summary, truncated: true }, [true]],
  ['legacy detail has only a text truncation marker', summary, [false, true]],
] as const) {
  test(name, async () => {
    const calls: boolean[] = [];
    const response = await loadTraceDetail(record.id, hasTruncatedPreview(record), async (id, wantFull) => {
      assert.equal(id, record.id);
      calls.push(wantFull);
      return wantFull ? full : preview;
    }, new AbortController().signal);

    assert.deepEqual(calls, expected);
    assert.equal(response.detail.full?.arguments, raw);
    assert.equal(response.fullFailed, false);
  });
}

test('missing sidecar and failed full request both preserve preview with distinct status', async () => {
  const missing: TraceDetail = { ...preview, full: null, full_available: false };
  const signal = new AbortController().signal;
  assert.deepEqual(await loadTraceDetail(summary.id, true, async () => missing, signal), {
    detail: missing, fullFailed: false,
  });
  for (const firstFull of [true, false]) {
    assert.deepEqual(await loadTraceDetail(summary.id, firstFull, async (_id, wantFull) => {
      if (wantFull) throw new Error('full request failed');
      return preview;
    }, signal), { detail: preview, fullFailed: true });
  }
});

const firstFile = '--- a/first.txt\n+++ b/first.txt\n@@ -1 +1,2 @@\n-old\n+first\n+next\n';
const secondFile = '--- a/second.txt\n+++ b/second.txt\n@@ -1 +1 @@\n-old\n+later file\n';
const fullDiff = firstFile + secondFile;
const edit: TraceRecord = {
  ...summary,
  tool: 'file_edit',
  file_changes: [
    { path: '/repo/first.txt', action: 'edit', insertions: 2, deletions: 1, truncated: true },
    { path: '/repo/second.txt', action: 'edit', insertions: 1, deletions: 1, truncated: true },
  ] as FileChange[],
};
const editDetail: TraceDetail = { ok: true, source: 'test', record: edit };

test('a truncated file edit automatically loads the sidecar and recovers later file sections', async () => {
  const calls: boolean[] = [];
  const response = await loadTraceDetail(edit.id, hasTruncatedPreview(edit), async (_id, wantFull) => {
    calls.push(wantFull);
    return { ...editDetail, full: wantFull ? { file_diff: fullDiff } : null };
  }, new AbortController().signal);
  assert.deepEqual(calls, [true]);
  const changedFile = edit.file_changes![1];
  const loaded = traceChangeDiff({ ...changedFile, diff: firstFile }, traceFileDiff(response.detail));
  assert.equal(loaded.truncated, false);
  assert.equal(fileDiffPreview(loaded.text, { path: changedFile.path }).text, secondFile);
});

test('legacy edit prefers a complete result or applied unified patch over its partial diff', () => {
  assert.deepEqual(traceFileDiff({ ...editDetail, full: {
    result: JSON.stringify({ _file_edit_history: { diff_preview: fullDiff, truncated: false } }),
  } }), { text: fullDiff, truncated: false });
  assert.deepEqual(traceFileDiff({ ...editDetail, full: {
    result: JSON.stringify({ diff_preview: firstFile, truncated: true }),
    arguments: JSON.stringify({ action: 'patch', patch: fullDiff }),
  } }), { text: fullDiff, truncated: false });
  assert.equal(traceFileDiff({ ...editDetail, full: {
    arguments: JSON.stringify({ action: 'patch', dry_run: true, patch: fullDiff }),
  } }), undefined);
});

test('unavailable and capped sidecars preserve the larger preview and its partial status', () => {
  const change = { diff: firstFile, truncated: true };
  const capped = { ...editDetail, full: { file_diff: fullDiff, file_diff_truncated: true } };
  assert.deepEqual(traceChangeDiff(change, traceFileDiff(capped)), { text: fullDiff, truncated: true });
  assert.deepEqual(traceChangeDiff(change, traceFileDiff({ ...editDetail, full: null })), { text: firstFile, truncated: true });
  const short = { ...editDetail, full: { result: JSON.stringify({ diff_preview: '--- a/first', truncated: true }) } };
  assert.deepEqual(traceChangeDiff(change, traceFileDiff(short)), { text: firstFile, truncated: true });
});

test('left list sums the recorded edits across files and preserves an omitted zero', () => {
  assert.deepEqual(traceFileChangeCounts(edit.file_changes), { insertions: 3, deletions: 2 });
  assert.deepEqual(traceFileChangeCounts([{ ...edit.file_changes![0], deletions: undefined }]), { insertions: 2, deletions: 0 });
  assert.equal(traceFileChangeCounts(undefined), undefined);
});
