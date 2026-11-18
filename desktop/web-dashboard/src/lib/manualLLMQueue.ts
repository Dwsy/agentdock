import type { ManualLLMQueueEntry } from '../api.ts';

export type QueueFilter = 'all' | 'active' | 'finished';

const ACTIVE_STATUSES = new Set(['queued', 'claimed', 'streaming']);

export function isManualEntryActive(status: string): boolean {
  return ACTIVE_STATUSES.has(status);
}

export function filterManualEntries(entries: ManualLLMQueueEntry[], filter: QueueFilter): ManualLLMQueueEntry[] {
  if (filter === 'all') return entries;
  if (filter === 'active') return entries.filter((item) => isManualEntryActive(item.status));
  return entries.filter((item) => !isManualEntryActive(item.status));
}

export function statusMeta(status: string): { label: string; className: string } {
  switch (status) {
    case 'queued':
      return { label: '待领取', className: 'bg-amber-500/15 text-amber-500' };
    case 'claimed':
      return { label: '已领取', className: 'bg-violet-500/15 text-violet-500' };
    case 'streaming':
      return { label: '输出中', className: 'bg-sky-500/15 text-sky-500' };
    case 'completed':
      return { label: '已完成', className: 'bg-emerald-500/15 text-emerald-500' };
    case 'failed':
      return { label: '失败', className: 'bg-red-500/15 text-red-500' };
    case 'cancelled':
      return { label: '已取消', className: 'bg-muted text-muted-foreground' };
    default:
      return { label: status || '-', className: 'bg-muted text-muted-foreground' };
  }
}

export function resolveFullEndpoint(origin: string, path: string): string {
  const cleanOrigin = origin.replace(/\/+$/, '');
  const cleanPath = path.startsWith('/') ? path : `/${path}`;
  return `${cleanOrigin}${cleanPath}`;
}
