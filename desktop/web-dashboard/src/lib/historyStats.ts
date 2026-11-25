export type HistoryRange = 'today' | '7d' | '30d' | 'all';
export type HistoryGroup = 'day' | 'week' | 'month';
export type HistoryBounds = { from: string; to: string };
export type HistorySelection = { range: HistoryRange; group: HistoryGroup; custom?: HistoryBounds };
export type HistoryValues = { first: number; second: number; count: number };
export type HistoryPoint = HistoryValues & { date: string };
export type HistoryBucket = HistoryBounds & HistoryValues;

export const HISTORY_RANGES = [['today', '今天'], ['7d', '近 7 天'], ['30d', '近 30 天'], ['all', '全部']] as const;
export const DEFAULT_HISTORY_SELECTION: HistorySelection = { range: '7d', group: 'day' };
const calendarDate = new Intl.DateTimeFormat('sv-SE', { year: 'numeric', month: '2-digit', day: '2-digit' });

export function localHistoryDate(value: Date | string = new Date()): string {
  const date = typeof value === 'string' ? new Date(value) : value;
  return Number.isNaN(date.getTime()) ? '' : calendarDate.format(date);
}

function offsetDay(date: string, days: number): string {
  const utc = new Date(`${date}T00:00:00Z`);
  utc.setUTCDate(utc.getUTCDate() + days);
  return utc.toISOString().slice(0, 10);
}

export function historyBounds(range: HistoryRange, today: string, dates: string[]): HistoryBounds | undefined {
  if (range === 'all') {
    let from = '';
    let to = '';
    for (const date of dates) {
      if (!date) continue;
      if (!from || date < from) from = date;
      if (date > to) to = date;
    }
    return from ? { from, to } : undefined;
  }
  const days = range === 'today' ? 1 : range === '7d' ? 7 : 30;
  return { from: offsetDay(today, 1 - days), to: today };
}

export function inHistoryBounds(date: string, bounds: HistoryBounds | undefined): boolean {
  return !!date && !!bounds && date >= bounds.from && date <= bounds.to;
}

function periodStart(date: string, group: HistoryGroup): string {
  if (group === 'month') return `${date.slice(0, 7)}-01`;
  if (group === 'week') {
    const weekday = new Date(`${date}T00:00:00Z`).getUTCDay();
    return offsetDay(date, -((weekday + 6) % 7));
  }
  return date;
}

function nextPeriod(date: string, group: HistoryGroup): string {
  if (group === 'month') {
    const utc = new Date(`${date}T00:00:00Z`);
    utc.setUTCMonth(utc.getUTCMonth() + 1);
    return utc.toISOString().slice(0, 10);
  }
  return offsetDay(date, group === 'week' ? 7 : 1);
}

export function sumHistory(points: HistoryValues[]): HistoryValues {
  return points.reduce((sum, point) => ({
    first: sum.first + point.first, second: sum.second + point.second, count: sum.count + point.count,
  }), { first: 0, second: 0, count: 0 });
}

export function historyBuckets(points: HistoryPoint[], bounds: HistoryBounds, group: HistoryGroup): HistoryBucket[] {
  const byPeriod = new Map<string, HistoryValues>();
  for (const point of points) {
    if (!inHistoryBounds(point.date, bounds)) continue;
    const key = periodStart(point.date, group);
    const current = byPeriod.get(key);
    byPeriod.set(key, current ? sumHistory([current, point]) : point);
  }
  const buckets: HistoryBucket[] = [];
  for (let start = periodStart(bounds.from, group); start <= bounds.to; ) {
    const next = nextPeriod(start, group);
    const end = offsetDay(next, -1);
    buckets.push({
      ...byPeriod.get(start) ?? { first: 0, second: 0, count: 0 },
      from: start < bounds.from ? bounds.from : start,
      to: end > bounds.to ? bounds.to : end,
    });
    start = next;
  }
  return buckets;
}

export function historyDateLabel(bounds: HistoryBounds): string {
  return bounds.from === bounds.to ? bounds.from : `${bounds.from} 至 ${bounds.to}`;
}
