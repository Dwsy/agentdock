import { useMemo, useState } from 'react';
import { Search, ScrollText, CheckCircle2, XCircle, Clock3, Terminal, ListFilter, CheckCircle, Circle, Wrench, FilePenLine } from 'lucide-react';
import type { Traces as TracesData, TraceRecord } from '../api';
import { Panel, SectionTitle, ms, relTime } from '../ui';
import TraceDetail from '../components/TraceDetail';

type StatusFilter = 'all' | 'ok' | 'fail';

export default function Traces({ data }: { data: TracesData }) {
  const [tool, setTool] = useState('all');
  const [status, setStatus] = useState<StatusFilter>('all');
  const [q, setQ] = useState('');
  const [selectedId, setSelectedId] = useState<number | null>(null);

  // 工具分类（按出现次数排序）
  const toolCounts = useMemo(() => {
    const c: Record<string, number> = {};
    data.records.forEach((r) => (c[r.tool] = (c[r.tool] || 0) + 1));
    return Object.entries(c).sort((a, b) => b[1] - a[1]);
  }, [data.records]);

  const rows = useMemo(
    () =>
      data.records.filter((r) => {
        if (tool !== 'all' && r.tool !== tool) return false;
        if (status === 'ok' && !r.success) return false;
        if (status === 'fail' && r.success) return false;
        if (!q) return true;
        return r.tool.toLowerCase().includes(q.toLowerCase()) || (r.result || '').toLowerCase().includes(q.toLowerCase()) || String(r.id).includes(q);
      }),
    [data.records, tool, status, q],
  );

  const selected = rows.find((r) => r.id === selectedId) || rows[0];
  const errCount = data.records.filter((r) => !r.success).length;

  return (
    <Panel className="p-5">
      <SectionTitle
        icon={ScrollText}
        title="调用追踪"
        hint={`${data.count} 条 · ${errCount} 失败 · 点左侧看详情`}
        action={
          <div className="flex flex-wrap items-center gap-2">
            <div className="glass-control flex items-center gap-2 rounded-lg px-3 py-1.5">
              <Search size={14} className="text-muted-foreground" />
              <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜索工具/结果/ID…" className="w-40 bg-transparent text-xs outline-none placeholder:text-muted-foreground" />
            </div>
            <div className="flex gap-1">
              {([['all', '全部', ListFilter], ['ok', '成功', CheckCircle], ['fail', '失败', XCircle]] as const).map(([k, label, Icon]) => (
                <button key={k} onClick={() => setStatus(k)} className={`glass-control flex items-center gap-1 rounded-md px-2 py-1 text-[11px] ${status === k ? 'text-foreground' : 'text-muted-foreground'}`}>
                  <Icon size={12} /> {label}
                </button>
              ))}
            </div>
          </div>
        }
      />

      {/* 工具分类 chips */}
      <div className="mb-3 flex flex-wrap gap-1.5">
        <button onClick={() => setTool('all')} className={`glass-control rounded-md px-2.5 py-1 text-[11px] ${tool === 'all' ? 'text-foreground' : 'text-muted-foreground'}`}>
          全部 <span className="ml-1 tabular text-muted-foreground/60">{data.records.length}</span>
        </button>
        {toolCounts.map(([t, n]) => (
          <button key={t} onClick={() => setTool(t)} className={`glass-control flex items-center gap-1 rounded-md px-2.5 py-1 font-mono text-[11px] ${tool === t ? 'text-foreground' : 'text-muted-foreground'}`}>
            <Wrench size={11} /> {t} <span className="tabular text-muted-foreground/60">{n}</span>
          </button>
        ))}
      </div>

      <div className="grid gap-4 lg:grid-cols-[340px_1fr]">
        {/* 左：列表 */}
        <div className="scrollbar-thin max-h-[680px] space-y-1 overflow-y-auto rounded-xl border border-border/60 bg-muted/10 p-1.5 pr-1">
          {rows.map((r) => (
            <button
              key={r.id}
              onClick={() => setSelectedId(r.id)}
              className={`flex w-full items-center gap-2 rounded-lg p-2 text-left transition-colors ${selected?.id === r.id ? 'glass-chosen' : 'hover:bg-card/60'}`}
            >
              {r.success ? <CheckCircle2 size={14} className="shrink-0 text-emerald-500" /> : <XCircle size={14} className="shrink-0 text-red-500" />}
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-1.5">
                  <span className="truncate font-mono text-xs">{r.tool}</span>
                  {r.command && <span title="命令"><Terminal size={11} className="shrink-0 text-amber-500" /></span>}
                  {r.file_changes && r.file_changes.length > 0 && <span title="文件变更"><FilePenLine size={11} className="shrink-0 text-violet-500" /></span>}
                </div>
                <div className="mt-0.5 flex items-center gap-2 text-[10px] text-muted-foreground">
                  <span className="flex items-center gap-0.5"><Clock3 size={10} /> {ms(r.duration_ms)}</span>
                  <span>{relTime(r.started_at)}</span>
                </div>
              </div>
              <span className="shrink-0 font-mono text-[10px] text-muted-foreground/60">#{r.id}</span>
            </button>
          ))}
          {rows.length === 0 && <div className="py-16 text-center text-sm text-muted-foreground">没有匹配的调用</div>}
        </div>

        {/* 右：详情 */}
        <div className="scrollbar-thin max-h-[680px] min-w-0 overflow-y-auto rounded-xl border border-border/60 bg-muted/10 p-3.5">
          {selected ? (
            <TraceDetail record={selected} />
          ) : (
            <div className="flex h-64 flex-col items-center justify-center gap-2 text-center">
              <ScrollText size={28} className="text-muted-foreground" />
              <div className="text-sm font-medium">选择一条调用</div>
              <div className="text-xs text-muted-foreground">左侧点开任意调用，查看完整命令、参数与结果</div>
            </div>
          )}
        </div>
      </div>
    </Panel>
  );
}
