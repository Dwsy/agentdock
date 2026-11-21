import { useEffect, useMemo, useRef, useState } from 'react';
import { Search, ScrollText, CheckCircle2, XCircle, Clock3, Terminal, ListFilter, CheckCircle, Circle, Wrench, FilePenLine, Tag, X, FolderGit2, ChevronDown, ChevronRight, RefreshCw } from 'lucide-react';
import { api, type Traces as TracesData, type TraceRecord } from '../api';
import { Panel, SectionTitle, fmt, ms, relTime } from '../ui';
import TraceDetail from '../components/TraceDetail';
import { groupTracesByProject, type TraceProjectGroup } from '../lib/traceProjects';
import { usePersistedState } from '../lib/usePersistedState';

type StatusFilter = 'all' | 'ok' | 'fail';

/** 首屏与游标页共用的摘要页大小；正文只走详情接口按需读取。 */
const TRACE_PAGE_SIZE = 80;

/** 游标页按 ID 去重合并；有更早页时用 ID 降序给列表一个稳定总序。 */
function mergeTraceRecords(latest: TraceRecord[], older: TraceRecord[]): TraceRecord[] {
  if (older.length === 0) return latest;
  const seen = new Set<number>();
  const merged: TraceRecord[] = [];
  for (const record of [...latest, ...older]) {
    if (seen.has(record.id)) continue;
    seen.add(record.id);
    merged.push(record);
  }
  return merged.sort((a, b) => b.id - a.id);
}

function traceModeLabel(mode: string): string {
  switch (mode) {
    case 'off': return '未开启';
    case 'memory': return '内存';
    case 'disk': return '磁盘';
    default: return mode || '未知';
  }
}

/** 分组维度：内置「本地 UA」读 user_agent，其余读 upstream_headers 里同名的头。 */
type HeaderDimension = { key: string; label: string; read: (r: TraceRecord) => string };

/** 没有该头的记录归到这一组，避免它们从统计里静默消失。 */
const MISSING_VALUE = '未提供';
const LOCAL_UA_DIMENSION = '__local_ua';

/** 左侧列表的两种视图：按时间平铺，或按调用路径分组。 */
type ListMode = 'time' | 'project';

/** 一条调用在左侧列表里的行：两种视图共用，避免两处渲染漂移。 */
function TraceRow({ record, project, selected, onSelect }: {
  record: TraceRecord;
  project?: Pick<TraceProjectGroup, 'name' | 'full'>;
  selected: boolean;
  onSelect: () => void;
}) {
  return (
    <button
      onClick={onSelect}
      aria-current={selected ? true : undefined}
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
          <span className="flex shrink-0 items-center gap-0.5"><Clock3 size={10} /> {ms(record.duration_ms)}</span>
          <span className="shrink-0">{relTime(record.started_at)}</span>
          {project && (
            <span className="flex min-w-0 items-center gap-1" title={project.full || project.name}>
              <FolderGit2 size={11} className="shrink-0" />
              <span className="truncate">{project.name}</span>
            </span>
          )}
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

export default function Traces({ data, onHistoryModeChange, onRefreshLatest }: {
  data: TracesData;
  onHistoryModeChange: (paused: boolean) => void;
  onRefreshLatest: () => void;
}) {
  const [tool, setTool] = usePersistedState('traces-tool', 'all');
  const [status, setStatus] = usePersistedState<StatusFilter>('traces-status', 'all');
  const [q, setQ] = useState('');
  const [selectedId, setSelectedId] = useState<number | null>(null);
  // 左侧列表视图切换：时间平铺 / 按项目分组（分组是展示投影，筛选轴不变）。
  const [listMode, setListMode] = usePersistedState<ListMode>('traces-list-mode', 'time');
  // 请求头分组是第三条筛选轴，和工具/状态/搜索共用同一条 rows 管道。
  const [dimKey, setDimKey] = usePersistedState<string | null>('traces-dim', null);
  const [headerValue, setHeaderValue] = usePersistedState<string | null>('traces-header-value', null);
  // 更早页单独存放：首屏 80 条来自 App 的轮询快照，历史页只在用户点击后追加。
  const [older, setOlder] = useState<TraceRecord[]>([]);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [hasMore, setHasMore] = useState<boolean | undefined>(undefined);
  const [historyBase, setHistoryBase] = useState<TracesData | null>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const [olderError, setOlderError] = useState<string | null>(null);

  // 页面卸载时恢复轮询，避免停留在别的页面时 traces 一直不刷新。
  useEffect(() => () => onHistoryModeChange(false), [onHistoryModeChange]);

  const base = historyBase ?? data;
  const loadedRecords = useMemo(() => mergeTraceRecords(base.records, older), [base.records, older]);
  const statusInfo = typeof base.status === 'object' && base.status ? base.status : null;
  const mode = statusInfo?.mode ?? base.mode ?? (typeof base.status === 'string' ? base.status : '');
  const retained = statusInfo?.count ?? (mode === 'off' ? 0 : undefined);
  const retentionDays = statusInfo?.retention_days;

  // 工具分类（按当前已加载记录的出现次数排序）
  const toolCounts = useMemo(() => {
    const c: Record<string, number> = {};
    loadedRecords.forEach((r) => (c[r.tool] = (c[r.tool] || 0) + 1));
    return Object.entries(c).sort((a, b) => b[1] - a[1]);
  }, [loadedRecords]);

  // 维度列表按当前已加载记录里真实出现过的头上发现，避免给空维度造死按钮。
  const headerDims = useMemo<HeaderDimension[]>(() => {
    const seen = new Set<string>();
    loadedRecords.forEach((r) => Object.keys(r.upstream_headers || {}).forEach((k) => seen.add(k)));
    return [
      { key: LOCAL_UA_DIMENSION, label: '本地 UA', read: (r: TraceRecord) => r.user_agent || '' },
      ...[...seen].sort().map((k) => ({ key: k, label: `上游 ${k}`, read: (r: TraceRecord) => r.upstream_headers?.[k] || '' })),
    ];
  }, [loadedRecords]);

  const dim = dimKey ? headerDims.find((d) => d.key === dimKey) : undefined;

  // 每组给次数、失败数与平均耗时；按次数降序，最吵的来源排最前。
  const groups = useMemo(() => {
    if (!dim) return [];
    const buckets = new Map<string, { count: number; errors: number; totalMs: number }>();
    loadedRecords.forEach((r) => {
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
  }, [loadedRecords, dim]);

  const needle = q.trim().toLowerCase();
  const rows = useMemo(
    () =>
      loadedRecords.filter((r) => {
        if (tool !== 'all' && r.tool !== tool) return false;
        if (status === 'ok' && !r.success) return false;
        if (status === 'fail' && r.success) return false;
        if (dim && headerValue !== null && (dim.read(r) || MISSING_VALUE) !== headerValue) return false;
        return !needle || r.tool.toLowerCase().includes(needle) || String(r.id).includes(needle);
      }),
    [loadedRecords, tool, status, needle, dim, headerValue],
  );

  // 项目分组只对已筛选的行做投影：先走工具/状态/搜索/头值，再分项目。
  const projectGroups = useMemo(() => groupTracesByProject(rows), [rows]);
  const projectByTraceId = useMemo(() => {
    const projects = new Map<number, TraceProjectGroup>();
    for (const group of projectGroups) {
      for (const record of group.records) projects.set(record.id, group);
    }
    return projects;
  }, [projectGroups]);

  const selected = rows.find((r) => r.id === selectedId) || rows[0];
  const errCount = rows.filter((r) => !r.success).length;
  const cursor = loadedRecords.reduce((min, r) => Math.min(min, r.id), Infinity);
  const moreAvailable = hasMore ?? base.has_more;

  const loadOlder = async () => {
    if (loadingOlder || !moreAvailable || !Number.isFinite(cursor)) return;
    // 固定发出请求时的首屏，晚到的轮询结果不会改动这次翻页的起点。
    if (!historyBase) {
      setHistoryBase(data);
      onHistoryModeChange(true);
    }
    setLoadingOlder(true);
    setOlderError(null);
    try {
      const page = await api.traces({ limit: TRACE_PAGE_SIZE, beforeId: cursor, view: 'summary' });
      setOlder((prev) => mergeTraceRecords(prev, page.records));
      setHasMore(page.has_more ?? false);
    } catch (error) {
      setOlderError(`加载失败：${error instanceof Error ? error.message : String(error)}`);
    } finally {
      setLoadingOlder(false);
    }
  };

  // 返回最新：丢掉历史页并立刻重拉首屏，同时恢复 App 的 traces 轮询。
  const returnLatest = () => {
    setOlder([]);
    setHasMore(undefined);
    setHistoryBase(null);
    setSelectedId(null);
    setOlderError(null);
    listRef.current?.scrollTo(0, 0);
    onRefreshLatest();
  };

  return (
    <Panel className="p-5">
      <SectionTitle
        icon={ScrollText}
        title="调用追踪"
        hint={`${fmt.format(rows.length)} 条筛选结果 · ${errCount} 失败 · 点左侧看详情`}
        action={
          <div className="flex flex-wrap items-center gap-2">
            <div className="glass-control flex items-center gap-2 rounded-lg px-3 py-1.5">
              <Search size={14} className="text-muted-foreground" />
              <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜索工具或 ID…" className="w-40 bg-transparent text-xs outline-none placeholder:text-muted-foreground" />
            </div>
            <div className="flex gap-1">
              {([['all', '全部', ListFilter], ['ok', '成功', CheckCircle], ['fail', '失败', XCircle]] as const).map(([k, label, Icon]) => (
                <button key={k} onClick={() => setStatus(k)} aria-pressed={status === k} className={`glass-control flex items-center gap-1 rounded-md px-2 py-1 text-[11px] ${status === k ? 'text-foreground' : 'text-muted-foreground'}`}>
                  <Icon size={12} /> {label}
                </button>
              ))}
            </div>
          </div>
        }
      />

      {/* 三个数分开显示：保留量来自 trace 窗口，已加载含游标页，筛选是当前视图。 */}
      <div className="mb-3 flex flex-wrap items-center gap-x-4 gap-y-1 rounded-lg border border-border/50 bg-muted/10 px-3 py-2 text-[11px] text-muted-foreground">
        <span>保留 <b className="tabular text-foreground">{retained === undefined ? '未知' : `${fmt.format(retained)} 条`}</b></span>
        <span>已加载 <b className="tabular text-foreground">{fmt.format(loadedRecords.length)}</b> 条</span>
        <span>当前筛选 <b className="tabular text-foreground">{fmt.format(rows.length)}</b> 条</span>
        <span>模式 <b className="text-foreground">{traceModeLabel(mode)}</b></span>
        {retentionDays ? <span>保留期 {retentionDays} 天</span> : null}
        {mode === 'off' && <span className="text-amber-500">调用追踪未开启，开启后这里才会记录调用</span>}
        {historyBase && <span className="text-amber-500">历史浏览中 · 自动刷新已暂停</span>}
      </div>

      {/* 工具分类 chips */}
      <div className="mb-3 flex flex-wrap gap-1.5">
        <button onClick={() => setTool('all')} aria-pressed={tool === 'all'} className={`glass-control rounded-md px-2.5 py-1 text-[11px] ${tool === 'all' ? 'text-foreground' : 'text-muted-foreground'}`}>
          全部 <span className="ml-1 tabular text-muted-foreground/60">{loadedRecords.length}</span>
        </button>
        {toolCounts.map(([t, n]) => (
          <button key={t} onClick={() => setTool(t)} aria-pressed={tool === t} className={`glass-control flex items-center gap-1 rounded-md px-2.5 py-1 font-mono text-[11px] ${tool === t ? 'text-foreground' : 'text-muted-foreground'}`}>
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
              aria-pressed={dimKey === d.key}
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
                aria-pressed={headerValue === g.value}
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

      <div className="grid gap-4 lg:grid-cols-[minmax(15rem,32%)_minmax(0,1fr)]">
        {/* 左：列表（时间平铺 / 按项目分组） */}
        <div className="min-w-0 rounded-xl border border-border/60 bg-muted/10 p-1.5">
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
          <div className="mb-1.5 flex flex-wrap items-center gap-2 border-b border-border/50 px-1 pb-2">
            <button
              onClick={() => void loadOlder()}
              disabled={loadingOlder || !moreAvailable || !Number.isFinite(cursor)}
              className="glass-control flex items-center gap-1 rounded-md px-2 py-1 text-[11px] text-muted-foreground disabled:opacity-40"
            >
              <RefreshCw size={11} className={loadingOlder ? 'animate-spin' : ''} /> 加载更早
            </button>
            {historyBase && (
              <button onClick={returnLatest} disabled={loadingOlder} className="glass-control rounded-md px-2 py-1 text-[11px] text-foreground disabled:opacity-40">
                返回最新
              </button>
            )}
            <span className="ml-auto text-[10px] text-muted-foreground/70" aria-live="polite">
              {moreAvailable === false && loadedRecords.length > 0 ? '已到最早' : moreAvailable ? `每页 ${TRACE_PAGE_SIZE} 条摘要` : ''}
            </span>
          </div>
          {olderError && <div role="alert" className="px-1 pb-1 text-[10px] text-amber-500">{olderError}</div>}
          <div ref={listRef} className="scrollbar-thin max-h-[645px] space-y-1 overflow-y-auto pr-1">
            {listMode === 'time'
              ? rows.map((r) => (
                <TraceRow key={r.id} record={r} project={projectByTraceId.get(r.id)} selected={selected?.id === r.id} onSelect={() => setSelectedId(r.id)} />
              ))
              : projectGroups.map((group) => (
                <ProjectBlock key={group.key || '__no_project__'} group={group} selectedId={selected?.id ?? null} onSelect={(record) => setSelectedId(record.id)} />
              ))}
            {rows.length === 0 && <div className="py-16 text-center text-sm text-muted-foreground">没有匹配的调用</div>}
          </div>
        </div>

        {/* 右：详情 */}
        <div className="bg-trace-reading scrollbar-thin max-h-[680px] min-w-0 overflow-y-auto rounded-xl border border-border/60 p-3.5">
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
