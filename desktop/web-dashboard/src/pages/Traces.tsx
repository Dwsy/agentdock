import { useMemo, useState } from 'react';
import { Search, ScrollText, CheckCircle2, XCircle, Clock3, Terminal, ListFilter, CheckCircle, Circle, Wrench, FilePenLine, Tag, X, FolderGit2, ChevronDown, ChevronRight } from 'lucide-react';
import type { Traces as TracesData, TraceRecord } from '../api';
import { Panel, SectionTitle, ms, relTime } from '../ui';
import TraceDetail from '../components/TraceDetail';
import { groupTracesByProject, type TraceProjectGroup } from '../lib/traceProjects';

type StatusFilter = 'all' | 'ok' | 'fail';

/** 分组维度：内置「本地 UA」读 user_agent，其余读 upstream_headers 里同名的头。 */
type HeaderDimension = { key: string; label: string; read: (r: TraceRecord) => string };

/** 没有该头的记录归到这一组，避免它们从统计里静默消失。 */
const MISSING_VALUE = '未提供';
const LOCAL_UA_DIMENSION = '__local_ua';

/** 左侧列表的两种视图：按时间平铺，或按调用路径分组。 */
type ListMode = 'time' | 'project';

/** 一条调用在左侧列表里的行：两种视图共用，避免两处渲染漂移。 */
function TraceRow({ record, selected, onSelect }: { record: TraceRecord; selected: boolean; onSelect: () => void }) {
  return (
    <button
      onClick={onSelect}
      className={`flex w-full items-center gap-2 rounded-lg p-2 text-left transition-colors ${selected ? 'glass-chosen' : 'hover:bg-card/60'}`}
    >
      {record.success ? <CheckCircle2 size={14} className="shrink-0 text-emerald-500" /> : <XCircle size={14} className="shrink-0 text-red-500" />}
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1.5">
          <span className="truncate font-mono text-xs">{record.tool}</span>
          {record.command && <span title="命令"><Terminal size={11} className="shrink-0 text-amber-500" /></span>}
          {record.file_changes && record.file_changes.length > 0 && <span title="文件变更"><FilePenLine size={11} className="shrink-0 text-violet-500" /></span>}
        </div>
        <div className="mt-0.5 flex items-center gap-2 text-[10px] text-muted-foreground">
          <span className="flex items-center gap-0.5"><Clock3 size={10} /> {ms(record.duration_ms)}</span>
          <span>{relTime(record.started_at)}</span>
        </div>
      </div>
      <span className="shrink-0 font-mono text-[10px] text-muted-foreground/60">#{record.id}</span>
    </button>
  );
}

/** 一个项目分组：头可折叠，下面是该项目下的调用。 */
function ProjectBlock({
  group,
  selectedId,
  onSelect,
}: {
  group: TraceProjectGroup;
  selectedId: number | null;
  onSelect: (record: TraceRecord) => void;
}) {
  const [open, setOpen] = useState(true);
  return (
    <div className="mb-1">
      <button
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-1 rounded-md px-1.5 py-1 text-left text-[11px] text-muted-foreground hover:bg-muted/40"
        title={group.full || group.name}
      >
        {open ? <ChevronDown size={13} className="shrink-0" /> : <ChevronRight size={13} className="shrink-0" />}
        <FolderGit2 size={13} className={`shrink-0 ${group.unclassified ? 'text-muted-foreground/50' : 'text-amber-400/80'}`} />
        <span className={`min-w-0 truncate font-medium ${group.unclassified ? 'italic' : ''}`}>{group.name}</span>
        <span className="ml-auto flex shrink-0 items-center gap-1.5 tabular text-[10px] text-muted-foreground/60">
          <span>{group.count}</span>
          {group.errors > 0 && <span className="text-red-500">{group.errors} 失败</span>}
          <span>{ms(group.avgMs)}</span>
        </span>
      </button>
      {open && group.records.map((record) => (
        <TraceRow key={record.id} record={record} selected={selectedId === record.id} onSelect={() => onSelect(record)} />
      ))}
    </div>
  );
}

export default function Traces({ data }: { data: TracesData }) {
  const [tool, setTool] = useState('all');
  const [status, setStatus] = useState<StatusFilter>('all');
  const [q, setQ] = useState('');
  const [selectedId, setSelectedId] = useState<number | null>(null);
  // 左侧列表视图切换：时间平铺 / 按项目分组（分组是展示投影，筛选轴不变）。
  const [listMode, setListMode] = useState<ListMode>('time');
  // 请求头分组是第三条筛选轴，和工具/状态/搜索共用同一条 rows 管道。
  const [dimKey, setDimKey] = useState<string | null>(null);
  const [headerValue, setHeaderValue] = useState<string | null>(null);

  // 工具分类（按出现次数排序）
  const toolCounts = useMemo(() => {
    const c: Record<string, number> = {};
    data.records.forEach((r) => (c[r.tool] = (c[r.tool] || 0) + 1));
    return Object.entries(c).sort((a, b) => b[1] - a[1]);
  }, [data.records]);

  // 维度列表按当前这批记录里真实出现过的头上发现，避免给空维度造死按钮。
  const headerDims = useMemo<HeaderDimension[]>(() => {
    const seen = new Set<string>();
    data.records.forEach((r) => Object.keys(r.upstream_headers || {}).forEach((k) => seen.add(k)));
    return [
      { key: LOCAL_UA_DIMENSION, label: '本地 UA', read: (r: TraceRecord) => r.user_agent || '' },
      ...[...seen].sort().map((k) => ({ key: k, label: `上游 ${k}`, read: (r: TraceRecord) => r.upstream_headers?.[k] || '' })),
    ];
  }, [data.records]);

  const dim = dimKey ? headerDims.find((d) => d.key === dimKey) : undefined;

  // 每组给次数、失败数与平均耗时；按次数降序，最吵的来源排最前。
  const groups = useMemo(() => {
    if (!dim) return [];
    const buckets = new Map<string, { count: number; errors: number; totalMs: number }>();
    data.records.forEach((r) => {
      const value = dim.read(r) || MISSING_VALUE;
      const bucket = buckets.get(value) || { count: 0, errors: 0, totalMs: 0 };
      bucket.count += 1;
      if (!r.success) bucket.errors += 1;
      bucket.totalMs += r.duration_ms;
      buckets.set(value, bucket);
    });
    return [...buckets.entries()]
      .map(([value, b]) => ({ value, count: b.count, errors: b.errors, avgMs: b.totalMs / b.count }))
      .sort((a, b) => b.count - a.count || a.value.localeCompare(b.value));
  }, [data.records, dim]);

  const rows = useMemo(
    () =>
      data.records.filter((r) => {
        if (tool !== 'all' && r.tool !== tool) return false;
        if (status === 'ok' && !r.success) return false;
        if (status === 'fail' && r.success) return false;
        if (dim && headerValue !== null && (dim.read(r) || MISSING_VALUE) !== headerValue) return false;
        if (!q) return true;
        return r.tool.toLowerCase().includes(q.toLowerCase()) || (r.result || '').toLowerCase().includes(q.toLowerCase()) || String(r.id).includes(q);
      }),
    [data.records, tool, status, q, dim, headerValue],
  );

  // 项目分组只对已筛选的行做投影：先走工具/状态/搜索/头值，再分项目。
  const projectGroups = useMemo(() => (listMode === 'project' ? groupTracesByProject(rows) : []), [listMode, rows]);

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

      {/* 请求头分组：选一个维度看聚合数字，点某组按该值筛选 */}
      <div className="mb-3 space-y-1.5">
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="flex items-center gap-1 text-[11px] text-muted-foreground"><Tag size={11} /> 按请求头分组</span>
          {headerDims.map((d) => (
            <button
              key={d.key}
              onClick={() => {
                setDimKey(dimKey === d.key ? null : d.key);
                setHeaderValue(null);
              }}
              className={`glass-control rounded-md px-2 py-0.5 font-mono text-[11px] ${dimKey === d.key ? 'text-foreground' : 'text-muted-foreground'}`}
            >
              {d.label}
            </button>
          ))}
          {dim && headerValue !== null && (
            <button onClick={() => setHeaderValue(null)} className="glass-control flex items-center gap-1 rounded-md px-2 py-0.5 text-[11px] text-foreground" title="清除头值筛选">
              <X size={10} /> {headerValue}
            </button>
          )}
        </div>
        {dim && (
          <div className="scrollbar-thin max-h-44 overflow-y-auto rounded-xl border border-border/60 bg-muted/10 p-1.5">
            <div className="flex items-center gap-2 px-2 py-1 text-[10px] text-muted-foreground">
              <span className="min-w-0 flex-1 font-mono">{dim.label}</span>
              <span className="w-12 text-right">次数</span>
              <span className="w-12 text-right">失败</span>
              <span className="w-16 text-right">平均耗时</span>
            </div>
            {groups.map((g) => (
              <button
                key={g.value}
                onClick={() => setHeaderValue(headerValue === g.value ? null : g.value)}
                className={`flex w-full items-center gap-2 rounded-lg px-2 py-1 text-left text-[11px] ${headerValue === g.value ? 'glass-chosen' : 'hover:bg-card/60'}`}
              >
                <span className="min-w-0 flex-1 truncate font-mono" title={g.value}>{g.value}</span>
                <span className="tabular w-12 text-right">{g.count}</span>
                <span className={`tabular w-12 text-right ${g.errors ? 'text-red-500' : 'text-muted-foreground/60'}`}>{g.errors}</span>
                <span className="tabular w-16 text-right text-muted-foreground">{ms(g.avgMs)}</span>
              </button>
            ))}
          </div>
        )}
      </div>

      <div className="grid gap-4 lg:grid-cols-[340px_1fr]">
        {/* 左：列表（时间平铺 / 按项目分组） */}
        <div className="rounded-xl border border-border/60 bg-muted/10 p-1.5">
          <div className="mb-1 flex items-center gap-1 px-1">
            {([['time', '按时间', Clock3], ['project', '按项目', FolderGit2]] as const).map(([key, label, Icon]) => (
              <button
                key={key}
                onClick={() => setListMode(key)}
                aria-pressed={listMode === key}
                className={`glass-control flex items-center gap-1 rounded-md px-2 py-1 text-[11px] ${listMode === key ? 'text-foreground' : 'text-muted-foreground'}`}
              >
                <Icon size={12} /> {label}
              </button>
            ))}
            <span className="ml-auto tabular text-[11px] text-muted-foreground/60">{rows.length}</span>
          </div>
          <div className="scrollbar-thin max-h-[645px] space-y-1 overflow-y-auto pr-1">
            {listMode === 'time'
              ? rows.map((r) => (
                <TraceRow key={r.id} record={r} selected={selected?.id === r.id} onSelect={() => setSelectedId(r.id)} />
              ))
              : projectGroups.map((group) => (
                <ProjectBlock key={group.key || '__no_project__'} group={group} selectedId={selected?.id ?? null} onSelect={(record) => setSelectedId(record.id)} />
              ))}
            {rows.length === 0 && <div className="py-16 text-center text-sm text-muted-foreground">没有匹配的调用</div>}
          </div>
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
