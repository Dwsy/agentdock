import { useMemo, useState } from 'react';
import { ChevronDown, ChevronRight, Layers3, List } from 'lucide-react';
import type { AnalyticsCall, CallAnalytics, CallFilters, CallGroup, CallMetrics } from '../api';
import ListPagination from '../components/ListPagination';
import { recordDateTime } from '../lib/recordTime';
import { Badge, Panel, SectionTitle, compact, fmt, ms } from '../ui';

export const countLabel = (count: number) => count < 100_000 ? fmt.format(count) : compact(count);
export function errorRate(errors: number, count: number) {
  if (!count) return '—';
  const percent = 100 * errors / count;
  return percent > 0 && percent < 0.1 ? '<0.1%' : `${percent.toFixed(1)}%`;
}

const dateTime = new Intl.DateTimeFormat('zh-CN', {
  month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23',
});
export function windowTime(value: string) {
  const timestamp = Date.parse(value);
  return Number.isFinite(timestamp) ? dateTime.format(timestamp) : '—';
}

const sources: Record<string, string> = { mcp: 'MCP', nexus: 'Nexus', internal: '内部' };
const errorCategories: Record<string, string> = { runtime: '运行时', validation: '参数校验', not_found: '未找到' };
export const sourceLabel = (source: string) => sources[source] ?? source;

const dimensions = [
  { field: 'by_tool', label: '工具', select: (row: CallGroup) => ({ tool: row.key }) },
  { field: 'by_source', label: '来源', select: (row: CallGroup) => ({ source: row.key }) },
  { field: 'by_client', label: 'User-Agent', select: (row: CallGroup) => ({ user_agent: row.key }) },
  { field: 'by_task', label: '任务关联', select: (row: CallGroup) => ({ task_id: row.key }) },
  { field: 'by_error', label: '错误码与分类', select: (row: CallGroup) => ({ error_code: row.key, error_category: row.category ?? '' }) },
  { field: 'by_stage', label: '执行阶段', select: (row: CallGroup) => ({ stage: row.key }) },
] as const satisfies { field: keyof CallAnalytics; label: string; select: (row: CallGroup) => CallFilters }[];

const sortOptions = {
  count: { label: '次数', value: (row: CallMetrics) => row.count },
  failures: { label: '失败次数', value: (row: CallMetrics) => row.error_count },
  rate: { label: '失败率', value: (row: CallMetrics) => row.count ? row.error_count / row.count : 0 },
  input: { label: "输入 Token", value: (row: CallMetrics) => row.input_tokens },
  output: { label: "输出 Token", value: (row: CallMetrics) => row.output_tokens },
  changes: { label: "代码变更行数", value: (row: CallMetrics) => row.insertions + row.deletions },
  p95: { label: 'P95', value: (row: CallMetrics) => row.p95_duration_ms },
} as const;

export type GroupDimension = typeof dimensions[number]['field'];
export type GroupSort = keyof typeof sortOptions;

export function CallBreakdown({ data, filters, applyFilters, dimension, onDimension, sort, onSort }: {
  data: CallAnalytics;
  filters: CallFilters;
  applyFilters: (patch: CallFilters) => void;
  dimension: GroupDimension;
  onDimension: (dimension: GroupDimension) => void;
  sort: GroupSort;
  onSort: (sort: GroupSort) => void;
}) {
  const current = dimensions.find(({ field }) => field === dimension) ?? dimensions[0];
  const rows = useMemo(() => {
    const value = sortOptions[sort].value;
    return [...data[dimension]].sort((a, b) => value(b) - value(a) || b.count - a.count || a.key.localeCompare(b.key));
  }, [data, dimension, sort]);
  const [expanded, setExpanded] = useState(false);
  const limit = 12;
  const isStage = dimension === "by_stage";
  const isError = dimension === "by_error";
  const isLimited = dimension === "by_client" || dimension === "by_task" || isError;
  return (
    <Panel className="min-w-0 p-3.5 sm:p-5">
      <SectionTitle icon={Layers3} title="调用分组" hint="点选分组筛选" />
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <div role="group" aria-label="分组维度" className="flex flex-wrap gap-1">
          {dimensions.map(({ field, label }) => (
            <button key={field} type="button" aria-pressed={field === dimension} onClick={() => { onDimension(field); setExpanded(false); if (field === "by_stage" && ["input", "output", "changes"].includes(sort)) onSort("count"); }}
              className={`min-h-8 rounded-lg px-2.5 text-xs font-medium ${field === dimension ? 'bg-primary/12 text-primary' : 'text-muted-foreground hover:bg-accent'}`}>
              {label}
            </button>
          ))}
        </div>
        <label className="flex items-center gap-2 text-xs text-muted-foreground">
          降序
          <select value={sort} onChange={(event) => onSort(event.target.value as GroupSort)}
            className="glass-control h-9 rounded-lg bg-card px-2 text-xs text-foreground">
            {Object.entries(sortOptions).filter(([key]) => !isStage || !["input", "output", "changes"].includes(key)).map(([key, option]) => <option key={key} value={key}>{option.label}</option>)}
          </select>
        </label>
      </div>
      {isStage && <p className="mb-2 text-xs text-muted-foreground">按阶段执行次数统计。点选阶段会筛出包含它的整次调用。</p>}
      {isLimited && rows.length === 20 && <p className="mb-2 text-xs text-muted-foreground">显示调用次数最多的 20 组。</p>}
      {dimension === "by_client" && <p className="mb-2 text-xs text-muted-foreground">User-Agent 取自调用入口，可能来自中继。</p>}
      {rows.length ? (
        <>
          <ul className="divide-y divide-border/60">
            {(expanded ? rows : rows.slice(0, limit)).map((row) => {
              const patch = current.select(row);
              const entries = Object.entries(patch) as [keyof CallFilters, string][];
              const selected = entries.some(([, value]) => value) && entries.every(([key, value]) => (filters[key] ?? '') === value);
              const label = (dimension === "by_source" ? sourceLabel(row.key) : row.key) || (dimension === 'by_task' ? '未关联任务' : '未提供');
              const content = (
                <>
                  <span className="min-w-0 break-all text-sm font-medium">
                    {label}
                    {dimension === 'by_error' && <> <span className="ml-1 text-xs font-normal text-muted-foreground">{errorCategories[row.category ?? ""] ?? (row.category || "未分类")}</span></>}
                  </span>
                  <span className="tabular flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
                    <span>{countLabel(row.count)} 次</span>
                    {isError ? <span>占失败 {errorRate(row.count, data.totals.error_count)}</span> :
                      <span className={row.error_count ? "text-destructive" : undefined}>{countLabel(row.error_count)} 失败 · {errorRate(row.error_count, row.count)}</span>}
                    <span title={`P50 ${ms(row.p50_duration_ms)} · P99 ${ms(row.p99_duration_ms)} · 平均 ${ms(row.avg_duration_ms)}`}>P95 {ms(row.p95_duration_ms)}</span>
                    {!isStage && <span>Token {countLabel(row.input_tokens)} 入 / {countLabel(row.output_tokens)} 出</span>}
                    {!isStage && (row.insertions > 0 || row.deletions > 0 || sort === "changes") && <span>+{countLabel(row.insertions)} / −{countLabel(row.deletions)} 行</span>}
                  </span>
                </>
              );
              return (
                <li key={JSON.stringify([row.key, row.category])}>
                  {entries.some(([, value]) => value) ? (
                    <button type="button" aria-pressed={selected} onClick={() => applyFilters(selected ? Object.fromEntries(entries.map(([key]) => [key, ''])) : patch)}
                      title={selected ? "取消筛选" : `筛选${label}`}
                      className={`relative grid w-full min-w-0 gap-x-3 gap-y-1 rounded-lg py-2.5 pr-7 pl-2 text-left sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center ${selected ? 'bg-primary/10 text-primary' : 'hover:bg-accent'}`}>
                      {content}
                      <ChevronRight size={13} aria-hidden="true" className="absolute top-1/2 right-2 -translate-y-1/2 text-muted-foreground" />
                    </button>
                  ) : <div className="grid min-w-0 gap-x-3 gap-y-1 px-2 py-2.5 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center">{content}</div>}
                </li>
              );
            })}
          </ul>
          {rows.length > limit && (
            <button type="button" aria-expanded={expanded} onClick={() => setExpanded((value) => !value)} className="mt-2 min-h-8 rounded-lg px-2 text-xs font-medium text-primary hover:bg-accent">
              {expanded ? '收起分组' : `展开全部 ${fmt.format(rows.length)} 个分组`}
            </button>
          )}
        </>
      ) : <p className="text-sm text-muted-foreground">当前筛选下没有{current.label}数据。</p>}
    </Panel>
  );
}

export function RecentCalls({ data, pending, onPageChange }: {
  data: CallAnalytics; pending: boolean; onPageChange: (page: number) => void;
}) {
  const { calls, totals, pagination } = data;
  const page = pagination?.page ?? 1;
  const pageCount = pagination ? Math.ceil(totals.count / pagination.page_size) : 1;
  return (
    <Panel className="min-w-0 p-3.5 sm:p-5">
      <SectionTitle icon={List} title="调用明细" hint={`共 ${fmt.format(totals.count)} 条匹配 · 本页 ${fmt.format(calls.length)} 条`}
        action={pagination && (
          <ListPagination label="调用明细分页" page={page} pageCount={page <= pageCount ? pageCount : undefined}
            canNext={page < pageCount} disabled={pending}
            onPrevious={() => onPageChange(Math.min(page - 1, Math.max(pageCount, 1)))} onNext={() => onPageChange(page + 1)} />
        )} />
      {!pagination && totals.count > calls.length && (
        <p className="mb-2 text-xs text-muted-foreground">当前 Core 只返回最近 {fmt.format(calls.length)} 条明细。更新 Core 后可翻页。</p>
      )}
      {calls.length ? (
        <ul className="divide-y divide-border/60" aria-busy={pending}>
          {calls.map((call) => <RecentCallRow key={call.id} call={call} />)}
        </ul>
      ) : <p className="text-sm text-muted-foreground">本页没有可用的调用明细。</p>}
    </Panel>
  );
}

function ResultBadge({ success }: { success: boolean }) {
  return <Badge className={success ? 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-400' : 'bg-destructive/10 text-destructive'}>{success ? '成功' : '失败'}</Badge>;
}

function RecentCallRow({ call }: { call: AnalyticsCall }) {
  const details = [
    ['开始时间', recordDateTime(call.started_at)],
    ['完成时间', recordDateTime(call.completed_at)],
    ['输入 Token', call.input_tokens === undefined ? '—' : fmt.format(call.input_tokens)],
    ['输出 Token', call.output_tokens === undefined ? '—' : fmt.format(call.output_tokens)],
    ['代码变更', call.insertions === undefined && call.deletions === undefined ? '—' : `+${fmt.format(call.insertions ?? 0)} / −${fmt.format(call.deletions ?? 0)}`],
    ["User-Agent", call.user_agent],
    ['任务 ID', call.task_id],
    ['追踪 ID', call.trace_id],
    ['Span ID', call.span_id],
    ['错误码', call.error_code],
    ['错误分类', call.error_category],
  ];
  return (
    <li>
      <details className="group">
        <summary className="flex min-w-0 cursor-pointer list-none items-start gap-2 rounded-lg py-2.5 pr-1 hover:bg-accent/60 [&::-webkit-details-marker]:hidden">
          <ChevronRight size={15} className="mt-0.5 shrink-0 text-muted-foreground group-open:hidden" />
          <ChevronDown size={15} className="mt-0.5 hidden shrink-0 text-muted-foreground group-open:block" />
          <div className="grid min-w-0 flex-1 gap-1 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center">
            <span className="min-w-0 break-all text-sm font-medium">{call.tool}</span>
            <span className="tabular flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
              <ResultBadge success={call.success} />
              <span className="break-all">{sourceLabel(call.source)}</span>
              <span>{ms(call.duration_ms)}</span>
              <time dateTime={call.completed_at}>{windowTime(call.completed_at)}</time>
            </span>
          </div>
        </summary>
        <div className="ml-5 min-w-0 space-y-3 pb-3">
          <dl className="grid min-w-0 grid-cols-2 gap-x-3 gap-y-2 rounded-lg bg-muted/35 p-2.5 text-xs sm:grid-cols-3">
            {details.map(([label, value]) => value && (
              <div key={label} className="min-w-0"><dt className="text-muted-foreground">{label}</dt><dd className="tabular mt-0.5 break-all">{value}</dd></div>
            ))}
          </dl>
          <h4 className="text-xs font-semibold">执行阶段</h4>
          {call.stages?.length ? (
            <ul className="space-y-1.5">
              {call.stages.map((stage, index) => (
                <li key={`${stage.name}-${index}`} className="grid min-w-0 gap-1 rounded-lg bg-muted/35 px-2.5 py-2 text-xs sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center">
                  <span className="break-all font-medium">{stage.name}</span>
                  <span className="tabular flex flex-wrap items-center gap-x-2 gap-y-1 text-muted-foreground">
                    <span>开始 +{ms(stage.started_offset_ms)}</span><span>耗时 {ms(stage.duration_ms)}</span><ResultBadge success={stage.success} />
                  </span>
                </li>
              ))}
            </ul>
          ) : <p className="text-xs text-muted-foreground">此调用没有阶段记录。</p>}
        </div>
      </details>
    </li>
  );
}
