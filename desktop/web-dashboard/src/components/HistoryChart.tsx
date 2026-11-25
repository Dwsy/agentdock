import { useMemo, useState, type ReactNode } from 'react';
import { compact, fmt } from '../ui';
import {
  HISTORY_RANGES, historyBuckets, historyDateLabel, sumHistory,
  type HistoryBounds, type HistoryPoint, type HistorySelection,
} from '../lib/historyStats';
const groups = [['day', '按天'], ['week', '按周'], ['month', '按月']] as const;

type Props = {
  title?: string;
  points: HistoryPoint[];
  bounds?: HistoryBounds;
  selection: HistorySelection;
  onSelection: (selection: HistorySelection) => void;
  series: readonly [{ label: string; color: string }, { label: string; color: string }];
  note?: ReactNode;
  empty: string;
  countLabel?: string;
  onFilterPeriod?: (bounds: HistoryBounds) => void;
};

export default function HistoryChart({ title, points, bounds, selection, onSelection, series, note, empty, countLabel, onFilterPeriod }: Props) {
  const [selected, setSelected] = useState('');
  const buckets = useMemo(() => bounds ? historyBuckets(points, bounds, selection.group) : [], [points, bounds, selection.group]);
  const measured = buckets.filter((bucket) => bucket.count > 0);
  const total = sumHistory(measured);
  const active = measured.find((bucket) => bucket.from === selected) ?? measured.at(-1);
  const maximum = measured.reduce((max, bucket) => Math.max(max, bucket.first + bucket.second), 1);
  const tickIndices = [...new Set([0, Math.floor((buckets.length - 1) / 2), buckets.length - 1])];

  return (
    <section aria-label={title} className="min-w-0 space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        {title && <h2 className="text-sm font-semibold">{title}</h2>}
        <div className="flex flex-wrap items-center gap-2">
          <div role="group" aria-label="统计范围" className="glass-control inline-flex rounded-lg p-0.5">
            {HISTORY_RANGES.map(([key, label]) => (
              <button key={key} type="button" aria-pressed={!selection.custom && selection.range === key} onClick={() => {
                setSelected('');
                onSelection({ range: key, group: selection.group });
              }} className={`min-h-8 whitespace-nowrap rounded-md px-2 text-xs font-medium ${!selection.custom && selection.range === key ? 'glass-chosen text-foreground' : 'text-muted-foreground hover:bg-accent'}`}>
                {label}
              </button>
            ))}
          </div>
          <select aria-label="汇总单位" value={selection.group} onChange={(event) => {
            setSelected('');
            onSelection({ ...selection, group: event.target.value as HistorySelection['group'] });
          }} className="glass-control h-9 rounded-lg bg-card px-2 text-xs text-foreground">
            {groups.map(([key, label]) => <option key={key} value={key}>{label}</option>)}
          </select>
        </div>
      </div>
      <div className="space-y-1 text-xs text-muted-foreground">
        {bounds && <p>统计范围 {historyDateLabel(bounds)}</p>}
        {note && <p>{note}</p>}
      </div>
      {active ? (
        <>
          <dl className="flex flex-wrap gap-x-6 gap-y-2 border-b border-border/60 pb-2.5">
            {series.map(({ label, color }, index) => (
              <div key={label} className="min-w-0">
                <dt className="flex items-center gap-1.5 text-xs text-muted-foreground"><i className={`size-2 rounded-sm ${color}`} />{label}</dt>
                <dd className="tabular text-lg font-semibold">{fmt.format(index === 0 ? total.first : total.second)}</dd>
              </div>
            ))}
            {countLabel && <div><dt className="text-xs text-muted-foreground">{countLabel}</dt><dd className="tabular text-lg font-semibold">{fmt.format(total.count)}</dd></div>}
          </dl>
          <div className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)] gap-x-2">
            <div aria-hidden="true" className="tabular flex h-24 flex-col justify-between text-right text-[10px] text-muted-foreground sm:h-28">
              <span>{compact(maximum)}</span><span>0</span>
            </div>
            <div className="relative flex h-24 min-w-0 border-y border-border/60 sm:h-28">
              <div aria-hidden="true" className="pointer-events-none absolute inset-x-0 top-1/2 border-t border-dashed border-border/60" />
              {buckets.map((bucket) => {
                if (!bucket.count) return <span key={bucket.from} aria-hidden="true" className="flex min-w-0 flex-1 items-end justify-center"><i className="mb-0.5 h-px w-1/2 rounded bg-muted-foreground/25" /></span>;
                const isActive = bucket === active;
                return (
                  <button key={bucket.from} type="button" aria-pressed={isActive} aria-label={historyDateLabel(bucket)} onClick={() => setSelected(bucket.from)}
                    title={`${historyDateLabel(bucket)} · ${series[0].label} ${fmt.format(bucket.first)} · ${series[1].label} ${fmt.format(bucket.second)}`}
                    className={`group relative flex min-w-0 flex-1 flex-col-reverse items-center rounded-t-sm px-px hover:bg-muted/40 focus-visible:outline-primary ${isActive ? 'bg-primary/10' : ''}`}>
                    <i aria-hidden="true" className={`block w-3/4 max-w-10 ${series[0].color}`} style={{ height: `${100 * bucket.first / maximum}%` }} />
                    <i aria-hidden="true" className={`block w-3/4 max-w-10 rounded-t-sm ${series[1].color}`} style={{ height: `${100 * bucket.second / maximum}%` }} />
                    {!bucket.first && !bucket.second && <i aria-hidden="true" className="h-0.5 w-3/4 max-w-10 rounded bg-muted-foreground/60" />}
                  </button>
                );
              })}
            </div>
            <div aria-hidden="true" className="tabular col-start-2 flex justify-between text-[10px] text-muted-foreground">
              {tickIndices.map((index) => <span key={index}>{buckets[index].from.slice(5)}</span>)}
            </div>
          </div>
          <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2 text-xs">
            <select aria-label="查看时段" value={active.from} onChange={(event) => setSelected(event.target.value)}
              className="glass-control h-8 max-w-full rounded-lg bg-card px-2 text-foreground">
              {measured.map((bucket) => <option key={bucket.from} value={bucket.from}>{historyDateLabel(bucket)}</option>)}
            </select>
            <p className="tabular flex flex-wrap gap-x-3 gap-y-1 text-muted-foreground" aria-live="polite">
              <span>{series[0].label} {fmt.format(active.first)}</span>
              <span>{series[1].label} {fmt.format(active.second)}</span>
              {countLabel && <span>{countLabel} {fmt.format(active.count)}</span>}
            </p>
            {onFilterPeriod && bounds && (active.from !== bounds.from || active.to !== bounds.to) && (
              <button type="button" onClick={() => onFilterPeriod({ from: active.from, to: active.to })}
                className="glass-control min-h-8 rounded-lg px-2 text-xs font-medium text-primary hover:bg-accent">查看此时段明细</button>
            )}
          </div>
        </>
      ) : <p role="status" className="rounded-lg bg-muted/30 px-3 py-8 text-center text-sm text-muted-foreground">{empty}</p>}
    </section>
  );
}
