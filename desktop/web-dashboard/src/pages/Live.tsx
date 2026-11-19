import { useState } from 'react';
import { Radio, CircleSlash, Zap, ScrollText, Package, Pause, Play } from 'lucide-react';
import type { RuntimeEvent } from '../api';
import { Badge, Panel, SectionTitle, fmt, ms, relTime } from '../ui';
import { usePersistedState } from '../lib/usePersistedState';

const MAX_EVENTS = 200;

const kindMeta: Record<string, { label: string; icon: any; cls: string }> = {
  call: { label: '调用', icon: Zap, cls: 'bg-sky-500/10 text-sky-500' },
  task: { label: '任务', icon: ScrollText, cls: 'bg-violet-500/10 text-violet-500' },
  artifact: { label: '发布物', icon: Package, cls: 'bg-emerald-500/10 text-emerald-500' },
};

function eventTitle(e: RuntimeEvent): string {
  const p = e.payload as any;
  if (e.kind === 'call') return `${p.tool || 'tool'} · ${p.success ? '成功' : '失败'}`;
  if (e.kind === 'task') return `${p.title || p.id || 'task'} · ${p.status || ''}`;
  if (e.kind === 'artifact') return `${p.action || 'artifact'} · ${p.artifact_id || ''}`;
  return e.kind;
}

function eventDetail(e: RuntimeEvent): string {
  const p = e.payload as any;
  if (e.kind === 'call') {
    const bits = [`来源 ${p.source || '-'}`];
    if (p.duration_ms != null) bits.push(`耗时 ${ms(Number(p.duration_ms))}`);
    if (p.error_code) bits.push(`错误 ${p.error_code}`);
    if (p.task_id) bits.push(`任务 ${p.task_id}`);
    return bits.join(' · ');
  }
  if (e.kind === 'task') return `阶段 ${p.phase || '-'} · 更新 ${p.updated_at ? relTime(String(p.updated_at)) : '-'}`;
  return JSON.stringify(p);
}

export default function Live({
  events,
  connected,
  paused,
  onTogglePause,
  onClear,
}: {
  events: RuntimeEvent[];
  connected: boolean;
  paused: boolean;
  onTogglePause: () => void;
  onClear: () => void;
}) {
  const [filter, setFilter] = usePersistedState('live-filter', 'all');
  const shown = filter === 'all' ? events : events.filter((e) => e.kind === filter);
  const counts = {
    call: events.filter((e) => e.kind === 'call').length,
    task: events.filter((e) => e.kind === 'task').length,
    artifact: events.filter((e) => e.kind === 'artifact').length,
  };

  return (
    <div className="space-y-5">
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        <Panel className="p-5">
          <div className="flex items-center justify-between">
            <span className="text-xs text-muted-foreground">连接状态</span>
            <Radio size={16} className={connected ? 'text-emerald-500' : 'text-red-500'} />
          </div>
          <div className={`mt-3 text-xl font-semibold ${connected ? 'text-emerald-500' : 'text-red-500'}`}>
            {connected ? '实时连接' : '已断开'}
          </div>
          <div className="mt-1 text-xs text-muted-foreground">SSE 自动重连</div>
        </Panel>
        <Panel className="p-5">
          <div className="flex items-center justify-between"><span className="text-xs text-muted-foreground">已接收事件</span><Zap size={16} className="text-muted-foreground" /></div>
          <div className="tabular mt-3 text-3xl font-semibold">{fmt.format(events.length)}</div>
          <div className="mt-1 text-xs text-muted-foreground">保留最近 {MAX_EVENTS} 条</div>
        </Panel>
        {Object.entries(kindMeta).map(([kind, meta]) => (
          <Panel key={kind} className="p-5">
            <div className="flex items-center justify-between"><span className="text-xs text-muted-foreground">{meta.label}事件</span><meta.icon size={16} className="text-muted-foreground" /></div>
            <div className="tabular mt-3 text-3xl font-semibold">{fmt.format(counts[kind as keyof typeof counts])}</div>
          </Panel>
        ))}
      </div>

      <Panel className="p-5">
        <SectionTitle
          icon={Radio}
          title="实时事件流"
          hint={`${shown.length} 条`}
          action={
            <div className="flex gap-2">
              {(['all', 'call', 'task', 'artifact'] as const).map((k) => (
                <button key={k} onClick={() => setFilter(k)} className={`glass-control rounded-md px-2.5 py-1 text-xs ${filter === k ? 'text-foreground' : 'text-muted-foreground'}`}>
                  {k === 'all' ? '全部' : kindMeta[k].label}
                </button>
              ))}
              <button onClick={onTogglePause} className="glass-control flex items-center gap-1 rounded-md px-2.5 py-1 text-xs text-muted-foreground" title={paused ? '继续' : '暂停'}>
                {paused ? <Play size={12} /> : <Pause size={12} />} {paused ? '继续' : '暂停'}
              </button>
              <button onClick={onClear} className="glass-control rounded-md px-2.5 py-1 text-xs text-muted-foreground">清空</button>
            </div>
          }
        />
        <div className="scrollbar-thin max-h-[560px] space-y-2 overflow-y-auto pr-1">
          {shown.length === 0 && (
            <div className="flex flex-col items-center justify-center gap-2 py-20 text-center">
              <Radio size={28} className="text-muted-foreground" />
              <div className="text-sm font-medium">等待事件…</div>
              <div className="text-xs text-muted-foreground">AgentDock 有新调用或任务状态变化时会实时出现在这里</div>
            </div>
          )}
          {shown.map((e) => {
            const meta = kindMeta[e.kind] || { label: e.kind, icon: CircleSlash, cls: 'bg-muted/20 text-muted-foreground' };
            return (
              <div key={`${e.kind}-${e.at}-${Math.random()}`} className="animate-fade-in flex items-start gap-3 rounded-xl border border-border/60 bg-card/40 p-3.5">
                <div className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-lg ${meta.cls}`}>
                  <meta.icon size={15} />
                </div>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <Badge className={meta.cls}>{meta.label}</Badge>
                    <span className="truncate text-sm font-medium">{eventTitle(e)}</span>
                  </div>
                  <div className="mt-1 font-mono text-xs text-muted-foreground">{eventDetail(e)}</div>
                </div>
                <span className="shrink-0 text-[11px] text-muted-foreground">{new Date(e.at).toLocaleTimeString('zh-CN')}</span>
              </div>
            );
          })}
        </div>
      </Panel>
    </div>
  );
}
