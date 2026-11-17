import { useMemo, useState } from 'react';
import { Search, ListChecks, ScrollText, ChevronRight } from 'lucide-react';
import type { Tasks as TasksData, Alerts as AlertsData, Task } from '../api';
import { Panel, SectionTitle, Badge, fmt, relTime } from '../ui';
import TaskDetailPage from './TaskDetailPage';

const statusMeta: Record<string, { label: string; cls: string }> = {
  completed: { label: '已完成', cls: 'bg-emerald-500/10 text-emerald-500' },
  active: { label: '进行中', cls: 'bg-sky-500/10 text-sky-500' },
  blocked: { label: '阻塞', cls: 'bg-amber-500/10 text-amber-500' },
  pending: { label: '待处理', cls: 'bg-muted/20 text-muted-foreground' },
};

export default function Tasks({ data, alerts }: { data: TasksData; alerts: AlertsData }) {
  const [q, setQ] = useState('');
  const [status, setStatus] = useState('all');
  const [detailTask, setDetailTask] = useState<Task | null>(null);

  const rows = useMemo(
    () =>
      data.tasks.filter((t) => {
        if (status !== 'all' && t.status !== status) return false;
        if (!q) return true;
        return (t.title + t.summary + (t.project || '')).toLowerCase().includes(q.toLowerCase());
      }),
    [data.tasks, q, status],
  );
  const statusCounts = useMemo(() => {
    const c: Record<string, number> = {};
    data.tasks.forEach((t) => (c[t.status] = (c[t.status] || 0) + 1));
    return c;
  }, [data.tasks]);
  const activeAlerts = alerts.alerts.filter((a) => a.active);

  // 进入详情
  if (detailTask) {
    return <TaskDetailPage id={detailTask.id} fallback={detailTask} onBack={() => setDetailTask(null)} />;
  }

  return (
    <div className="space-y-5">
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        {[
          ['全部任务', data.count],
          ['进行中', statusCounts.active || 0],
          ['已完成', statusCounts.completed || 0],
          ['阻塞', statusCounts.blocked || 0],
        ].map(([label, val]) => (
          <Panel key={label as string} className="p-5">
            <div className="text-xs text-muted-foreground">{label as string}</div>
            <div className="tabular mt-3 text-3xl font-semibold">{fmt.format(val as number)}</div>
          </Panel>
        ))}
      </div>

      <div className="grid gap-5 lg:grid-cols-3">
        <Panel className="p-5 lg:col-span-2">
          <SectionTitle
            icon={ListChecks}
            title="任务列表"
            hint={`${rows.length} 条 · 点进去看完整详情`}
            action={
              <div className="glass-control flex items-center gap-2 rounded-lg px-3 py-1.5">
                <Search size={14} className="text-muted-foreground" />
                <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜索标题或摘要…" className="w-44 bg-transparent text-xs outline-none placeholder:text-muted-foreground" />
              </div>
            }
          />
          <div className="mb-3 flex gap-2">
            {['all', 'active', 'completed', 'blocked'].map((s) => (
              <button key={s} onClick={() => setStatus(s)} className={`glass-control rounded-md px-2.5 py-1 text-xs ${status === s ? 'text-foreground' : 'text-muted-foreground'}`}>
                {s === 'all' ? '全部' : s === 'active' ? '进行中' : s === 'completed' ? '已完成' : '阻塞'}
              </button>
            ))}
          </div>
          <div className="scrollbar-thin max-h-[620px] space-y-2 overflow-y-auto pr-1">
            {rows.map((t) => {
              const m = statusMeta[t.status] || { label: t.status, cls: 'bg-muted/20 text-muted-foreground' };
              return (
                <button key={t.id} onClick={() => setDetailTask(t)} className="w-full rounded-xl border border-border/60 bg-card/40 p-3.5 text-left transition-colors hover:bg-card/70">
                  <div className="flex items-start gap-3">
                    <Badge className={m.cls}>{m.label}</Badge>
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-sm font-medium">{t.title}</div>
                      <div className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-[11px] text-muted-foreground">
                        {t.project && <span>{t.project}</span>}
                        {t.phase && <span>阶段 {t.phase}</span>}
                        <span>步骤 {t.completed_step_count}/{t.step_count}</span>
                        <span>事件 {t.event_count}</span>
                      </div>
                    </div>
                    <span className="shrink-0 text-[11px] text-muted-foreground">{relTime(t.updated_at)}</span>
                    <ChevronRight size={15} className="shrink-0 text-muted-foreground" />
                  </div>
                  {t.summary && <p className="mt-2 line-clamp-2 text-xs leading-relaxed text-muted-foreground">{t.summary}</p>}
                </button>
              );
            })}
            {rows.length === 0 && <div className="py-16 text-center text-sm text-muted-foreground">没有匹配的任务</div>}
          </div>
        </Panel>

        <Panel className="p-5">
          <SectionTitle icon={ScrollText} title="阻塞告警" hint={`${activeAlerts.length} 条`} />
          <div className="scrollbar-thin max-h-[700px] space-y-2 overflow-y-auto pr-1">
            {activeAlerts.slice(0, 20).map((a) => (
              <div key={a.id} className="rounded-xl border border-amber-500/25 bg-amber-500/5 p-3.5">
                <div className="text-sm">{a.message}</div>
                <div className="mt-1 text-xs leading-relaxed text-muted-foreground">{a.detail}</div>
              </div>
            ))}
            {activeAlerts.length === 0 && <div className="py-16 text-center text-sm text-muted-foreground">没有活跃告警</div>}
          </div>
        </Panel>
      </div>
    </div>
  );
}
