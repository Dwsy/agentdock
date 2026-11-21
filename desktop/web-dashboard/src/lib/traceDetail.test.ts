import assert from 'node:assert/strict';
import test from 'node:test';
import type { TraceDetail, TraceRecord } from '../api';
import { hasTruncatedPreview, loadTraceDetail } from './traceDetail.ts';

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
