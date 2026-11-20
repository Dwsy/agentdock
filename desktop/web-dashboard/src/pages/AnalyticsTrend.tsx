import { useState } from 'react';
import { ChartNoAxesCombined } from 'lucide-react';
import type { CallAnalytics, CallBucket } from '../api';
import { Panel, SectionTitle, ms } from '../ui';
import { countLabel, errorRate, windowTime } from './AnalyticsDetails';

type Metric = {
  label: string;
  color: string;
  value: (bucket: CallBucket) => number | null;
  format: (value: number) => string;
  secondary?: (bucket: CallBucket) => number;
  scale: (peak: number) => number;
};

const wholeScale = (peak: number) => Math.max(2, 2 * Math.ceil(peak / 2));
const metrics: Metric[] = [
  { label: '调用量', color: 'fill-sky-600 dark:fill-sky-400', value: (row) => row.count, format: countLabel, scale: wholeScale },
  { label: '失败率', color: 'fill-destructive', value: (row) => row.count ? row.error_count / row.count : null, format: (value) => errorRate(value, 1), scale: () => 1 },
  { label: 'P95 耗时', color: 'fill-violet-600 dark:fill-violet-400', value: (row) => row.count ? row.p95_duration_ms : null, format: ms, scale: (peak) => peak || 1 },
  { label: 'Token', color: 'fill-sky-600 dark:fill-sky-400', value: (row) => row.input_tokens, secondary: (row) => row.output_tokens, format: countLabel, scale: wholeScale },
];

const axisTime = new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' });
const plotWidth = 900;
const plotHeight = 140;

export default function AnalyticsTrend({ buckets, window, metricIndex, onMetric }: {
  buckets: CallBucket[];
  window: CallAnalytics['window'];
  metricIndex: number;
  onMetric: (index: number) => void;
}) {
  const [bucketsBack, setBucketsBack] = useState<number | null>(null);
  const metric = metrics[metricIndex];
  const from = Date.parse(window.from);
  const to = Date.parse(window.to);
  const coveredFrom = Math.max(from, Date.parse(window.covered_from));
  const x = (timestamp: number) => plotWidth * (timestamp - from) / (to - from);
  const intervals = buckets.flatMap((bucket) => {
    const start = Math.max(coveredFrom, Date.parse(bucket.start));
    const end = Math.min(to, Date.parse(bucket.end));
    return start < end ? [{ bucket, start, end, value: metric.value(bucket), secondary: metric.secondary?.(bucket) ?? 0 }] : [];
  });
  const newestWithCalls = intervals.map(({ bucket }) => bucket.count > 0).lastIndexOf(true);
  const focusedIndex = bucketsBack === null ? Math.max(0, newestWithCalls) : Math.max(0, intervals.length - 1 - bucketsBack);
  const focused = intervals[focusedIndex];
  const max = metric.scale(Math.max(0, ...intervals.map(({ value, secondary }) => (value ?? 0) + secondary)));
  const formatTime = (time: number) => windowTime(new Date(time).toISOString());

  return (
    <Panel className="min-w-0 p-3.5 sm:p-5">
      <SectionTitle icon={ChartNoAxesCombined} title="调用趋势" />
      <div className="mb-4 flex flex-wrap gap-1" role="group" aria-label="趋势指标">
        {metrics.map(({ label }, index) => (
          <button key={label} type="button" aria-pressed={metricIndex === index} onClick={() => onMetric(index)}
            className={`min-h-9 rounded-lg px-2.5 text-xs font-medium ${metricIndex === index ? 'glass-chosen text-foreground' : 'text-muted-foreground hover:bg-accent'}`}>
            {label}
          </button>
        ))}
      </div>
      {focused ? (
        <>
          <div className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)] items-stretch gap-x-3 gap-y-1">
            <div aria-hidden="true" className="tabular flex flex-col justify-between py-px text-right text-[11px] text-muted-foreground">
              {[max, max / 2, 0].map((tick) => <span key={tick}>{metric.format(tick)}</span>)}
            </div>
            <div className="relative min-w-0">
              <svg role="img" aria-label={`${metric.label}趋势。每个柱形代表一个时段，可用下方时段选择查看数值。`}
                viewBox={`0 0 ${plotWidth} ${plotHeight}`} preserveAspectRatio="none"
                className="block h-36 w-full cursor-crosshair overflow-hidden rounded-sm sm:h-44"
                onClick={(event) => {
                  const rect = event.currentTarget.getBoundingClientRect();
                  const time = from + (to - from) * (event.clientX - rect.left) / rect.width;
                  const index = intervals.findIndex(({ start, end }) => start <= time && time < end);
                  if (index >= 0) setBucketsBack(intervals.length - 1 - index);
                }}>
                {coveredFrom > from && <rect width={x(coveredFrom)} height={plotHeight} className="fill-muted-foreground/12" />}
                {[0, 0.5, 1].map((fraction) => (
                  <line key={fraction} x2={plotWidth} y1={0.5 + (plotHeight - 1) * fraction} y2={0.5 + (plotHeight - 1) * fraction}
                    className="stroke-border" strokeWidth={1} vectorEffect="non-scaling-stroke" />
                ))}
                {intervals.map(({ bucket, start, end, value, secondary }) => {
                  const left = x(start);
                  const width = x(end) - left;
                  const inset = Math.min(3, width * 0.12);
                  const primaryHeight = plotHeight * (value ?? 0) / max;
                  const secondaryHeight = plotHeight * secondary / max;
                  return (
                    <g key={bucket.start}>
                      <title>{`${formatTime(start)} – ${formatTime(end)} · ${metric.secondary ? `输入 ${countLabel(value ?? 0)} / 输出 ${countLabel(secondary)}` : value === null ? '无调用' : metric.format(value)}`}</title>
                      {bucket === focused.bucket && <rect x={left} width={width} height={plotHeight} className="fill-sky-500/15" />}
                      {primaryHeight > 0 && <rect x={left + inset} y={plotHeight - primaryHeight} width={width - 2 * inset} height={primaryHeight} className={metric.color} />}
                      {secondaryHeight > 0 && <rect x={left + inset} y={plotHeight - primaryHeight - secondaryHeight} width={width - 2 * inset} height={secondaryHeight} className="fill-violet-600 dark:fill-violet-400" />}
                    </g>
                  );
                })}
              </svg>
              {coveredFrom - from > (to - from) * 0.22 && (
                <span aria-hidden="true" className="pointer-events-none absolute top-2 left-2 text-[11px] text-muted-foreground">未覆盖</span>
              )}
            </div>
            <div className="tabular col-start-2 flex justify-between gap-2 text-[11px] text-muted-foreground" aria-hidden="true">
              {[from, (from + to) / 2, to].map((time) => <time key={time} dateTime={new Date(time).toISOString()}>{axisTime.format(time)}</time>)}
            </div>
          </div>
          <div className="mt-3 flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-2 border-t border-border/60 pt-3">
            <label className="flex min-w-0 flex-wrap items-center gap-2 text-xs text-muted-foreground">
              时段
              <select value={focusedIndex} onChange={(event) => setBucketsBack(intervals.length - 1 - Number(event.target.value))}
                className="glass-control h-9 min-w-0 max-w-full rounded-lg bg-card px-2 text-xs text-foreground">
                {intervals.map(({ bucket, start, end }, index) => (
                  <option key={bucket.start} value={index}>{formatTime(start)} – {formatTime(end)}</option>
                ))}
              </select>
            </label>
            <p className="tabular flex flex-wrap gap-3 text-xs font-medium">
              {metric.secondary ? (
                <>
                  <span className="inline-flex items-center gap-1.5"><i className="size-2 rounded-sm bg-sky-600 dark:bg-sky-400" />输入 {countLabel(focused.value ?? 0)}</span>
                  <span className="inline-flex items-center gap-1.5"><i className="size-2 rounded-sm bg-violet-600 dark:bg-violet-400" />输出 {countLabel(focused.secondary)}</span>
                </>
              ) : <span>{metric.label} {focused.value === null ? '—' : metric.format(focused.value)}{metricIndex === 0 ? ' 次' : ''}</span>}
            </p>
          </div>
          {(coveredFrom > from || metric.secondary) && (
            <p className="mt-2 text-xs leading-relaxed text-muted-foreground">
              {coveredFrom > from && '阴影区域暂无数据。'}{metric.secondary && 'Token 按工具入参和返回结果估算。'}
            </p>
          )}
        </>
      ) : <p className="text-sm text-muted-foreground">所选窗口暂无可用时段。</p>}
    </Panel>
  );
}
