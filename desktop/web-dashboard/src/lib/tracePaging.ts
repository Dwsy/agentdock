import type { TraceRecord } from '../api.ts';

export const TRACE_PAGE_SIZE = 30;

// ID follows completion order. An older page can include a call that started later.
export function pageTimeRange(records: TraceRecord[]): { from: string; to: string } | null {
  let min = Infinity;
  let max = -Infinity;
  let from = '';
  let to = '';
  for (const { started_at } of records) {
    const timestamp = Date.parse(started_at);
    if (!Number.isFinite(timestamp)) continue;
    if (timestamp < min) {
      min = timestamp;
      from = started_at;
    }
    if (timestamp > max) {
      max = timestamp;
      to = started_at;
    }
  }
  return from ? { from, to } : null;
}

export function nextBeforeId(records: TraceRecord[]): number | undefined {
  const cursor = Math.min(...records.map(({ id }) => id));
  return Number.isSafeInteger(cursor) && cursor > 0 ? cursor : undefined;
}
