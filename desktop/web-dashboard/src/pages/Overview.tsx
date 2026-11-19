import { useMemo, type ElementType } from 'react';
import {
  Activity,
  AlertTriangle,
  Blocks,
  Bot,
  Boxes,
  Database,
  Gauge,
  ListChecks,
  Plug,
  ScrollText,
  Terminal,
  Wifi,
  CircleSlash,
  Zap,
} from 'lucide-react';
import type { Alerts, Analytics, Overview as OverviewData, Status, Tasks } from '../api';
import { Badge, Panel, compact, duration, bytes, fmt, relTime } from '../ui';

type Props = {
  status: Status;
  overview: OverviewData;
  analytics: Analytics;
  alerts: Alerts;
  tasks: Tasks;
};

type Tone = 'default' | 'green' | 'amber' | 'red' | 'violet' | 'sky';

/** iOS 小组件式的彩色图标块 + 同色数值，靠色彩建立层级而非加大字号。 */
const tileTone: Record<Tone, { icon: string; value: string }> = {
  default: { icon: 'bg-primary/10 text-primary', value: 'text-foreground' },
  green: { icon: 'bg-emerald-500/12 text-emerald-500', value: 'text-emerald-500' },
  amber: { icon: 'bg-amber-500/12 text-amber-500', value: 'text-amber-500' },
  red: { icon: 'bg-red-500/12 text-red-500', value: 'text-red-500' },
  violet: { icon: 'bg-violet-500/12 text-violet-500', value: 'text-violet-500' },
  sky: { icon: 'bg-sky-500/12 text-sky-500', value: 'text-sky-500' },
};

/** 紧凑指标小组件：图标带圆角色块，数值克制，去掉松散的大 padding。 */
function MetricTile({
  icon: Icon,
  label,
  value,
  sub,
  tone = 'default',
  live = false,
}: {
  icon: ElementType;
  label: string;
  value: string | number;
  sub?: string;
  tone?: Tone;
  live?: boolean;
}) {
  const t = tileTone[tone];
  return (
    <Panel className="animate-float-in relative overflow-hidden p-3.5 sm:p-4">
      <div className="flex items-start justify-between gap-2">
        <div className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-[11px] ${t.icon}`}>
          <Icon size={18} strokeWidth={2} />
        </div>
        {live && (
          <span className="mt-0.5 flex items-center gap-1 text-[10px] font-semibold tracking-wide text-emerald-500/90">
            <i className="h-1.5 w-1.5 animate-pulse rounded-full bg-emerald-500" /> LIVE
          </span>
        )}
      </div>
      <div className="mt-3 text-[11px] font-medium text-muted-foreground">{label}</div>
      <div className={`tabular mt-0.5 text-[22px] font-semibold leading-tight tracking-tight ${t.value}`}>{value}</div>
      {sub && <div className="mt-1 truncate text-[11px] text-muted-foreground/80" title={sub}>{sub}</div>}
    </Panel>
  );
}

/** 小组件标题：图标底块 + 紧凑行距，替代全局 SectionTitle 的松散间距。 */
function WidgetTitle({ icon: Icon, title, hint, action }: { icon: ElementType; title: string; hint?: string; action?: React.ReactNode }) {
  return (
    <div className="mb-3 flex items-center gap-2">
      <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-muted/60 text-muted-foreground">
        <Icon size={14} />
      </div>
      <span className="text-[13px] font-semibold tracking-wide">{title}</span>
      {hint && <span className="truncate text-[11px] text-muted-foreground">{hint}</span>}
      {action && <div className="ml-auto">{action}</div>}
    </div>
  );
}

export function TokenChart({ data }: { data: Analytics['daily_tokens'] }) {
  const rows = useMemo(() => [...data].reverse().slice(-14), [data]);
  const w = 720;
  const h = 190;
  const pad = { l: 8, r: 8, t: 12, b: 22 };
  const max = Math.max(1, ...rows.map((d) => d.input_tokens + d.output_tokens));
  const step = (w - pad.l - pad.r) / Math.max(1, rows.length);
  const bw = Math.max(4, step * 0.58);
  return (
    <div className="w-full overflow-hidden">
      <svg viewBox={`0 0 ${w} ${h}`} className="w-full" style={{ height: 180 }} role="img" aria-label="每日 Token 用量">
        {[0.25, 0.5, 0.75, 1].map((p) => (
          <line key={p} x1={pad.l} x2={w - pad.r} y1={pad.t + (h - pad.t - pad.b) * (1 - p)} y2={pad.t + (h - pad.t - pad.b) * (1 - p)} className="stroke-border" strokeWidth={1} strokeDasharray="3 4" opacity={0.6} />
        ))}
        {rows.map((d, i) => {
          const total = d.input_tokens + d.output_tokens;
          const bh = ((h - pad.t - pad.b) * total) / max;
          const oh = ((h - pad.t - pad.b) * d.output_tokens) / max;
          const ih = bh - oh;
          const x = pad.l + i * step + (step - bw) / 2;
          const y = h - pad.b - bh;
          return (
            <g key={d.date}>
              <title>{`${d.date}\n输入 ${fmt.format(d.input_tokens)} · 输出 ${fmt.format(d.output_tokens)}`}</title>
              <rect x={x} y={y} width={bw} height={ih} rx={2} className="fill-chart-4" opacity={0.85} />
              <rect x={x} y={y + ih} width={bw} height={oh} rx={2} className="fill-chart-1" opacity={0.9} />
              {i % 2 === 0 && <text x={x + bw / 2} y={h - 6} textAnchor="middle" className="fill-muted-foreground" fontSize={10}>{d.date.slice(5)}</text>}
            </g>
          );
        })}
      </svg>
      <div className="mt-1 flex gap-5 text-[11px] text-muted-foreground">
        <span className="flex items-center gap-1.5"><i className="inline-block h-2 w-2 rounded-sm bg-chart-1" /> 输出 Token</span>
        <span className="flex items-center gap-1.5"><i className="inline-block h-2 w-2 rounded-sm bg-chart-4" /> 输入 Token</span>
      </div>
    </div>
  );
}

export function ToolBars({ stats }: { stats: Analytics['tool_stats'] }) {
  const rows = useMemo(() => [...stats].sort((a, b) => b.count - a.count).slice(0, 8), [stats]);
  const max = Math.max(1, ...rows.map((r) => r.count));
  return (
    <div className="space-y-2.5">
      {rows.map((r) => (
        <div key={r.tool} className="grid grid-cols-[minmax(0,1fr)_1fr_42px] items-center gap-2.5">
          <span className="truncate font-mono text-[11px] text-muted-foreground" title={r.tool}>{r.tool}</span>
          <div className="h-1.5 overflow-hidden rounded-full bg-muted/60">
            <div className="h-full rounded-full bg-chart-2 transition-all" style={{ width: `${(r.count / max) * 100}%` }} />
          </div>
          <span className="tabular text-right text-[11px] font-medium">{fmt.format(r.count)}</span>
        </div>
      ))}
      {rows.length === 0 && <div className="text-sm text-muted-foreground">暂无调用数据</div>}
    </div>
  );
}

const statusMeta: Record<string, { label: string; cls: string }> = {
  completed: { label: '已完成', cls: 'bg-emerald-500/10 text-emerald-500' },
  active: { label: '进行中', cls: 'bg-sky-500/10 text-sky-500' },
  blocked: { label: '阻塞', cls: 'bg-amber-500/10 text-amber-500' },
  pending: { label: '待处理', cls: 'bg-muted/20 text-muted-foreground' },
};

export default function Overview({ status, overview, analytics, alerts, tasks }: Props) {
  const errorRate = analytics.total_calls ? ((analytics.total_errors / analytics.total_calls) * 100).toFixed(2) : '0';
  const activeAlerts = alerts.alerts.filter((a) => a.active).length;
  const recentTasks = useMemo(
    () => [...tasks.tasks].sort((a, b) => +new Date(b.updated_at) - +new Date(a.updated_at)).slice(0, 8),
    [tasks.tasks],
  );

  return (
    <div className="space-y-3">
      {/* 指标带：统一尺寸的小组件，彩色图标块建立层级 */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 xl:grid-cols-6">
        <MetricTile icon={Gauge} label="服务状态" value={status.ok ? '在线' : '离线'} sub={`${status.tool_count} 个工具可用`} tone={status.ok ? 'green' : 'red'} live={status.ok} />
        <MetricTile icon={ListChecks} label="活跃任务" value={overview.tasks.active} sub={`24h 内 +${overview.tasks.active_recent_24h}`} tone="violet" />
        <MetricTile icon={CircleSlash} label="阻塞任务" value={overview.tasks.blocked} sub={`已完成 ${overview.tasks.completed}`} tone="amber" />
        <MetricTile icon={Activity} label="总调用" value={compact(analytics.total_calls)} sub={`错误率 ${errorRate}%`} tone="sky" />
        <MetricTile icon={Zap} label="输出 Token" value={compact(analytics.total_output_tokens)} sub={`输入 ${compact(analytics.total_input_tokens)}`} tone="violet" />
        <MetricTile icon={AlertTriangle} label="活跃告警" value={activeAlerts} sub={`已解决 ${alerts.resolved_count}`} tone={activeAlerts ? 'red' : 'green'} />
      </div>

      {/* 图表 + 排行：主次 8:4，紧凑内边距 */}
      <div className="grid gap-3 lg:grid-cols-12">
        <Panel className="p-4 lg:col-span-8">
          <WidgetTitle icon={Database} title="每日 Token 用量" hint="最近 14 天" />
          <TokenChart data={analytics.daily_tokens} />
        </Panel>
        <Panel className="p-4 lg:col-span-4">
          <WidgetTitle icon={Terminal} title="工具调用排行" hint="Top 8" />
          <ToolBars stats={analytics.tool_stats} />
        </Panel>
      </div>

      {/* 任务 + 告警：7:5 */}
      <div className="grid gap-3 lg:grid-cols-12">
        <Panel className="p-4 lg:col-span-7">
          <WidgetTitle icon={ScrollText} title="最近任务" hint={`${tasks.count} 条`} />
          <div className="scrollbar-thin max-h-[380px] space-y-2 overflow-y-auto pr-1">
            {recentTasks.map((t) => {
              const meta = statusMeta[t.status] || { label: t.status, cls: 'bg-muted/20 text-muted-foreground' };
              return (
                <div key={t.id} className="rounded-xl border border-border/60 bg-card/40 p-3 transition-colors hover:bg-card/70">
                  <div className="flex items-start gap-2.5">
                    <Badge className={meta.cls}>{meta.label}</Badge>
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-[13px] font-medium" title={t.title}>{t.title}</div>
                      {t.project && <div className="mt-0.5 truncate text-[11px] text-muted-foreground">{t.project}</div>}
                    </div>
                    <span className="shrink-0 text-[11px] text-muted-foreground">{relTime(t.updated_at)}</span>
                  </div>
                  {t.summary && <p className="mt-1.5 line-clamp-2 text-[11px] leading-relaxed text-muted-foreground">{t.summary}</p>}
                </div>
              );
            })}
          </div>
        </Panel>
        <Panel className="p-4 lg:col-span-5">
          <WidgetTitle icon={AlertTriangle} title="告警" hint={`${activeAlerts} 条活跃`} />
          <div className="scrollbar-thin max-h-[380px] space-y-2 overflow-y-auto pr-1">
            {alerts.alerts.filter((a) => a.active).slice(0, 12).map((a) => (
              <div key={a.id} className="rounded-xl border border-amber-500/25 bg-amber-500/5 p-3">
                <div className="text-[13px]">{a.message}</div>
                <div className="mt-1 text-[11px] leading-relaxed text-muted-foreground">{a.detail}</div>
                <div className="mt-1 text-[10px] text-muted-foreground">{relTime(a.last_seen)}更新</div>
              </div>
            ))}
            {activeAlerts === 0 && (
              <div className="flex flex-col items-center justify-center gap-2 py-12 text-center">
                <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-emerald-500/10">
                  <Bot size={24} className="text-emerald-500" />
                </div>
                <div className="text-[13px] font-medium">一切正常</div>
              </div>
            )}
          </div>
        </Panel>
      </div>

      {/* 三个信息小组件：等宽三列 */}
      <div className="grid gap-3 lg:grid-cols-3">
        <Panel className="p-4">
          <WidgetTitle icon={Blocks} title="进程健康" />
          <div className="grid grid-cols-3 gap-x-3 gap-y-3.5">
            {[
              ['运行时间', duration(analytics.process.uptime_ms)],
              ['Goroutines', fmt.format(analytics.process.goroutines)],
              ['堆内存分配', bytes(analytics.process.heap_alloc_bytes)],
              ['GC 次数', fmt.format(analytics.process.gc_cycles)],
              ['窗口调用', fmt.format(analytics.window_calls)],
              ['活跃调用', fmt.format(analytics.active_calls)],
            ].map(([k, v]) => (
              <div key={k} className="min-w-0">
                <div className="truncate text-[11px] text-muted-foreground" title={k}>{k}</div>
                <div className="tabular mt-1 truncate text-base font-semibold" title={v}>{v}</div>
              </div>
            ))}
          </div>
        </Panel>
        <Panel className="p-4">
          <WidgetTitle icon={Boxes} title="能力概览" />
          <div className="grid grid-cols-3 gap-2.5">
            {[
              [Boxes, overview.skills.count, 'Skills'],
              [Plug, overview.plugins.count, 'Plugins'],
              [Wifi, overview.mcp.count, 'MCP'],
            ].map(([Icon, val, label]: any) => (
              <div key={label} className="rounded-xl bg-muted/40 p-2.5 text-center">
                <Icon size={17} className="mx-auto text-muted-foreground" />
                <div className="tabular mt-1.5 text-lg font-semibold">{val as number}</div>
                <div className="text-[10px] text-muted-foreground">{label as string}</div>
              </div>
            ))}
          </div>
          <div className="mt-3 flex flex-wrap gap-x-3.5 gap-y-1.5 text-[11px] text-muted-foreground">
            {[
              ['鉴权', status.auth_enabled],
              ['Nexus', status.nexus_enabled],
              ['Memory', status.memory_enabled],
              ['Browser', status.browser_enabled],
            ].map(([k, v]: any) => (
              <span key={k} className="flex items-center gap-1.5">
                <i className={`inline-block h-1.5 w-1.5 rounded-full ${v ? 'bg-emerald-500' : 'bg-muted-foreground/40'}`} />
                {k} {v ? '开启' : '关闭'}
              </span>
            ))}
          </div>
        </Panel>
        <Panel className="p-4">
          <WidgetTitle icon={Terminal} title="工具清单" hint={`${status.tools.length} 个`} />
          <div className="scrollbar-thin flex max-h-[180px] flex-wrap content-start gap-1.5 overflow-y-auto pr-1">
            {status.tools.map((t) => (
              <span key={t} className="glass-control rounded-md px-2 py-1 font-mono text-[10px] text-muted-foreground">{t}</span>
            ))}
          </div>
        </Panel>
      </div>
    </div>
  );
}
