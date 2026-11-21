import { useEffect, useMemo, useRef, useState } from 'react';
import { Activity, AlertCircle, ArrowUpRight, Clock3, Code2, Gauge, Inbox, Layers3, LoaderCircle, X } from 'lucide-react';
import { api, DashboardRequestError, type CallAnalytics, type CallFilterKey, type CallFilters, type CallRange } from '../api';
import { recordDateTime } from '../lib/recordTime';
import { EmptyState, Panel, StatCard, fmt, ms } from '../ui';
import { CallBreakdown, RecentCalls, countLabel, errorRate, sourceLabel, type GroupDimension, type GroupSort } from './AnalyticsDetails';
import AnalyticsTrend from './AnalyticsTrend';

type Props = { refreshKey: number; onOpenTraces: () => void };
type CallRequest = { query: string; scope: string; refreshKey: number; retry: number };
type RequestState = { request: CallRequest; data?: CallAnalytics; error?: string };
type CallPage = { page: number; to: string };

const ranges = [['5m', '5 分钟'], ['1h', '1 小时'], ['24h', '24 小时']] as const;
const statusLabels: Record<string, string> = { success: '成功', error: '失败' };
const filterLabels: Record<CallFilterKey, string> = {
  tool: '工具', source: '来源', status: '结果', user_agent: 'User-Agent', task_id: '任务',
  error_code: '错误码', error_category: '错误分类', stage: '执行阶段',
};

function FacetSelect({ label, value, options, onChange, format = (option) => option }: {
  label: string; value: string; options: string[]; onChange: (value: string) => void; format?: (option: string) => string;
}) {
  const choices = value && !options.includes(value) ? [value, ...options] : options;
  return (
    <label className="grid min-w-0 gap-1 text-xs text-muted-foreground">
      {label}
      <select className="glass-control h-9 w-full min-w-0 rounded-lg bg-card px-2 text-sm text-foreground" value={value}
        onChange={(event) => onChange(event.target.value)}>
        <option value="">全部</option>
        {choices.map((choice) => <option key={choice} value={choice}>{format(choice)}</option>)}
      </select>
    </label>
  );
}

const coverageReasons = {
  restart: 'Core 启动前没有明细',
  capacity: '较早明细已因容量上限淘汰',
  retention: '较早明细已超过保留期',
};

function Coverage({ window }: { window: CallAnalytics['window'] }) {
  const from = Date.parse(window.from);
  const to = Date.parse(window.to);
  const coveredFrom = Date.parse(window.covered_from);
  return (
    <div className="flex min-w-0 items-start gap-2 text-xs leading-relaxed text-muted-foreground">
      <Clock3 size={14} className="mt-0.5 shrink-0" aria-hidden="true" />
      <div className="space-y-0.5">
        <p><span className="font-medium text-foreground">统计时段</span> {recordDateTime(window.from)} 至 {recordDateTime(window.to)}</p>
        {coveredFrom > from && (
          <p className="text-foreground">
            {coveredFrom >= to ? '此时段的明细已不可用' : `实际覆盖 ${recordDateTime(window.covered_from)} 至 ${recordDateTime(window.to)}`}
            {window.limited_by && ` · ${coverageReasons[window.limited_by]}`}
          </p>
        )}
        <p>按调用完成时间统计 · 浏览器本地时间 · 当前 Core 内存</p>
      </div>
    </div>
  );
}

function requestMessage(error: unknown) {
  if (error instanceof DashboardRequestError && error.status === 404) return '当前 Core 版本未提供调用分析接口。';
  if (error instanceof Error) return error.name === 'AbortError' ? '请求超时' : error.message;
  return '请求失败';
}

export default function Analytics({ refreshKey, onOpenTraces }: Props) {
  const [range, setRange] = useState<CallRange>('1h');
  const [filters, setFilters] = useState<CallFilters>({});
  const [retry, setRetry] = useState(0);
  const [paging, setPaging] = useState<CallPage>();
  const [dimension, setDimension] = useState<GroupDimension>('by_tool');
  const [metricIndex, setMetricIndex] = useState(0);
  const [sort, setSort] = useState<GroupSort>('count');
  const [state, setState] = useState<RequestState>();
  const inFlight = useRef<{ controller: AbortController; promise: Promise<CallAnalytics> } | null>(null);
  const scope = useMemo(() => {
    const params = new URLSearchParams({ range });
    for (const [key, value] of Object.entries(filters)) if (value) params.set(key, value);
    params.sort();
    return params.toString();
  }, [range, filters]);
  const query = useMemo(() => {
    const params = new URLSearchParams(scope);
    params.set('page', String(paging?.page ?? 1));
    if (paging) params.set('to', paging.to);
    return params.toString();
  }, [scope, paging]);
  const refresh = paging ? 0 : refreshKey;
  const request = useMemo(() => ({ query, scope, refreshKey: refresh, retry }), [query, scope, refresh, retry]);

  useEffect(() => () => {
    inFlight.current?.controller.abort();
    inFlight.current = null;
  }, [query]);

  useEffect(() => {
    // 轮询复用同一查询的在途请求，避免慢请求被下一次刷新反复取消。
    if (!inFlight.current) {
      const controller = new AbortController();
      const promise = api.callAnalytics(request.query, controller.signal).finally(() => {
        if (inFlight.current?.controller === controller) inFlight.current = null;
      });
      inFlight.current = { controller, promise };
    }
    let active = true;
    inFlight.current.promise.then(
      (data) => {
        if (active) setState({ request, data });
      },
      (error: unknown) => {
        if (!active) return;
        setState((previous) => ({ request, data: previous?.request.scope === request.scope ? previous.data : undefined, error: requestMessage(error) }));
      },
    );
    return () => { active = false; };
  }, [request]);

  const data = state?.request.scope === scope ? state.data : undefined;
  const error = state?.request === request ? state.error : undefined;
  const facets = state?.data?.facets;
  const pending = state?.request !== request;
  const active = Object.entries(filters).filter(([, value]) => value) as [CallFilterKey, string][];
  const applyFilters = (patch: CallFilters) => {
    setPaging(undefined);
    setFilters((previous) => ({ ...previous, ...patch }));
  };
  const setFilter = (key: CallFilterKey, value: string) => applyFilters({ [key]: value });
  const changePage = (page: number) => {
    if (!data) return;
    setPaging({ page, to: paging?.to ?? data.window.to });
  };

  return (
    <div className="min-w-0 space-y-3">
      <Panel className="space-y-3 p-3.5 sm:p-4">
        <div className="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-end gap-3 xl:grid-cols-[auto_minmax(0,1fr)_auto]">
          <div role="group" aria-label="统计窗口" className="glass-control flex w-fit gap-0.5 rounded-lg p-0.5">
            {ranges.map(([option, label]) => (
              <button key={option} type="button" aria-pressed={range === option} onClick={() => { setPaging(undefined); setRange(option); }}
                className={`min-h-8 whitespace-nowrap rounded-md px-2 text-xs font-semibold ${range === option ? 'glass-chosen text-foreground' : 'text-muted-foreground hover:bg-accent'}`}>
                {label}
              </button>
            ))}
          </div>
          <div className="col-span-2 row-start-2 grid min-w-0 grid-cols-3 gap-2 sm:gap-3 xl:col-span-1 xl:col-start-2 xl:row-start-1">
            <FacetSelect label="工具" value={filters.tool ?? ''} options={facets?.tools ?? []} onChange={(value) => setFilter('tool', value)} />
            <FacetSelect label="来源" value={filters.source ?? ''} options={facets?.sources ?? []} format={sourceLabel} onChange={(value) => setFilter('source', value)} />
            <FacetSelect label="结果" value={filters.status ?? ''} options={Object.keys(statusLabels)} format={(value) => statusLabels[value]} onChange={(value) => setFilter('status', value)} />
          </div>
          <button type="button" onClick={onOpenTraces} className="glass-control col-start-2 row-start-1 inline-flex min-h-9 items-center gap-1.5 whitespace-nowrap rounded-lg px-2.5 text-xs font-medium hover:bg-accent xl:col-start-3">
            调用追踪 <ArrowUpRight size={13} aria-hidden="true" />
          </button>
        </div>
        {active.length > 0 && (
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            {active.map(([key, value]) => {
              const text = key === 'status' ? statusLabels[value] : key === 'source' ? sourceLabel(value) : value;
              const label = `${filterLabels[key]} · ${text}`;
              return (
                <button key={key} type="button" title={label} aria-label={`移除筛选 ${label}`} onClick={() => setFilter(key, '')}
                  className="inline-flex min-h-8 max-w-full items-center gap-1.5 rounded-lg bg-primary/10 px-2 text-xs text-primary hover:bg-primary/20">
                  <span className="min-w-0 truncate">{label}</span><X size={13} className="shrink-0" aria-hidden="true" />
                </button>
              );
            })}
            <button type="button" onClick={() => { setPaging(undefined); setFilters({}); }} className="min-h-8 rounded-lg px-2 text-xs text-muted-foreground hover:bg-accent">清除筛选</button>
          </div>
        )}
        {data && (
          <div className="flex flex-wrap items-start justify-between gap-2 border-t border-border/60 pt-3">
            <Coverage window={data.window} />
            <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
              {paging && (
                <button type="button" onClick={() => setPaging(undefined)} className="glass-control min-h-9 rounded-lg px-2.5 font-medium text-foreground hover:bg-accent">
                  返回最新
                </button>
              )}
              {pending && <p role="status" className="flex items-center gap-1.5"><LoaderCircle size={13} className="motion-safe:animate-spin" />{paging ? `正在查询第 ${paging.page} 页` : '正在刷新'}</p>}
            </div>
          </div>
        )}
      </Panel>
      {pending && !data && (
        <Panel className="flex justify-center px-3 py-12 text-sm text-muted-foreground">
          <p role="status" className="flex items-center gap-2"><LoaderCircle size={16} className="motion-safe:animate-spin" />正在加载调用分析…</p>
        </Panel>
      )}
      {error && (
        <Panel className="flex flex-wrap items-center justify-between gap-3 p-4">
          <div role="alert" className="flex min-w-0 items-start gap-2 text-sm">
            <AlertCircle size={16} className="mt-0.5 shrink-0 text-destructive" />
            <p className="break-words">{error}{data && '。以下是上次成功刷新时的数据。'}</p>
          </div>
          <button type="button" className="glass-control min-h-9 rounded-lg px-3 text-xs font-medium hover:bg-accent" onClick={() => setRetry((value) => value + 1)}>重试</button>
        </Panel>
      )}
      {data && data.totals.count === 0 && (
        <Panel className="px-3">
          <EmptyState icon={Inbox} title={active.length ? '没有匹配的调用' : '所选窗口暂无调用'}
            hint={active.length ? '调整筛选条件或扩大时间窗口。' : undefined} />
        </Panel>
      )}
      {data && data.totals.count > 0 && (
        <>
          <div className="grid min-w-0 grid-cols-2 gap-2.5 min-[380px]:grid-cols-3 xl:grid-cols-6">
            <StatCard dense icon={Activity} label="调用次数" value={countLabel(data.totals.count)} />
            <StatCard dense icon={AlertCircle} label="失败率" value={errorRate(data.totals.error_count, data.totals.count)} sub={`${fmt.format(data.totals.error_count)} 次失败`} tone={data.totals.error_count ? 'red' : 'default'} />
            <StatCard dense icon={Gauge} label="P95 耗时" value={ms(data.totals.p95_duration_ms)} sub={`P50 ${ms(data.totals.p50_duration_ms)} · P99 ${ms(data.totals.p99_duration_ms)}`} />
            <StatCard dense icon={Layers3} label="输入 Token" value={countLabel(data.totals.input_tokens)} />
            <StatCard dense icon={Layers3} label="输出 Token" value={countLabel(data.totals.output_tokens)} />
            <StatCard dense icon={Code2} label="代码变更行数" value={countLabel(data.totals.insertions + data.totals.deletions)} sub={`+${fmt.format(data.totals.insertions)} / −${fmt.format(data.totals.deletions)}`} />
          </div>
          <AnalyticsTrend buckets={data.buckets} window={data.window} metricIndex={metricIndex} onMetric={setMetricIndex} />
          <CallBreakdown data={data} filters={filters} applyFilters={applyFilters}
            dimension={dimension} onDimension={setDimension} sort={sort} onSort={setSort} />
        </>
      )}
      {data && (data.totals.count > 0 || paging) && (
        <RecentCalls data={data} pending={pending} onPageChange={changePage} />
      )}
    </div>
  );
}
