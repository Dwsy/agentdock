import test from 'node:test';
import assert from 'node:assert/strict';
import { historyBounds, historyBuckets, inHistoryBounds, sumHistory } from './historyStats.ts';

const points = [
  { date: '2026-10-31', first: 90, second: 10, count: 1 },
  { date: '2026-11-01', first: 200, second: 0, count: 2 },
  { date: '2026-11-03', first: 31, second: 7, count: 1 },
  { date: '2026-11-03', first: 9, second: 8, count: 1 },
];

test('统计范围按自然日包含两端，全部使用可用数据首尾', () => {
  const dates = points.map(({ date }) => date);
  assert.deepEqual(historyBounds('today', '2026-11-03', dates), { from: '2026-11-03', to: '2026-11-03' });
  assert.deepEqual(historyBounds('7d', '2026-11-03', dates), { from: '2026-10-28', to: '2026-11-03' });
  assert.deepEqual(historyBounds('30d', '2024-03-01', dates), { from: '2024-02-01', to: '2024-03-01' });
  assert.deepEqual(historyBounds('all', '2026-11-22', dates), { from: '2026-10-31', to: '2026-11-03' });
  assert.equal(historyBounds('all', '2026-11-22', []), undefined);
  assert.equal(inHistoryBounds('2026-11-04', historyBounds('today', '2026-11-03', dates)), false);
});

test('日、周、月汇总使用相同的筛选记录，保留没有数据的日期', () => {
  const bounds = { from: '2026-10-31', to: '2026-11-03' };
  const daily = historyBuckets(points, bounds, 'day');
  assert.deepEqual(daily.map(({ from, count }) => [from, count]), [
    ['2026-10-31', 1], ['2026-11-01', 2], ['2026-11-02', 0], ['2026-11-03', 2],
  ]);
  const weekly = historyBuckets(points, bounds, 'week');
  const monthly = historyBuckets(points, bounds, 'month');
  assert.deepEqual(weekly.map(({ from, to }) => [from, to]), [['2026-10-31', '2026-11-01'], ['2026-11-02', '2026-11-03']]);
  assert.deepEqual(monthly.map(({ from, to }) => [from, to]), [['2026-10-31', '2026-10-31'], ['2026-11-01', '2026-11-03']]);
  for (const buckets of [daily, weekly, monthly]) assert.deepEqual(sumHistory(buckets), { first: 330, second: 25, count: 5 });
});
