import { useMemo } from 'react';
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
import { Badge, Panel, SectionTitle, StatCard, compact, duration, bytes, fmt, relTime } from '../ui';

type Props = {
  status: Status;
  overview: OverviewData;
  analytics: Analytics;
  alerts: Alerts;
  tasks: Tasks;
};

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
      <svg viewBox={`0 0 ${w} ${h}`} className="w-full" style={{ height: 190 }} role="img" aria-label="每日 Token 用量">
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
      <div className="mt-1 flex gap-5 text-xs text-muted-foreground">
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
    <div className="space-y-3">
      {rows.map((r) => (
        <div key={r.tool} className="grid grid-cols-[110px_1fr_48px] items-center gap-3">
          <span className="truncate font-mono text-xs text-muted-foreground" title={r.tool}>{r.tool}</span>
          <div className="h-2 overflow-hidden rounded-full bg-muted/60">
            <div className="h-full rounded-full bg-chart-2 transition-all" style={{ width: `${(r.count / max) * 100}%` }} />
          </div>
          <span className="tabular text-right text-xs font-medium">{fmt.format(r.count)}</span>
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
    <div className="space-y-5">
      <div className="grid grid-cols-2 gap-4 md:grid-cols-3 xl:grid-cols-6">
        <StatCard icon={Gauge} label="服务状态" value={status.ok ? '在线' : '离线'} sub={`${status.tool_count} 个工具可用`} tone="green" />
        <StatCard icon={ListChecks} label="活跃任务" value={overview.tasks.active} sub={`24h 内 +${overview.tasks.active_recent_24h}`} tone="violet" />
        <StatCard icon={CircleSlash} label="阻塞任务" value={overview.tasks.blocked} sub={`已完成 ${overview.tasks.completed}`} tone="amber" />
        <StatCard icon={Activity} label="总调用" value={compact(analytics.total_calls)} sub={`错误率 ${errorRate}%`} />
        <StatCard icon={Zap} label="输出 Token" value={compact(analytics.total_output_tokens)} sub={`输入 ${compact(analytics.total_input_tokens)}`} tone="violet" />
        <StatCard icon={AlertTriangle} label="活跃告警" value={activeAlerts} sub={`已解决 ${alerts.resolved_count}`} tone={activeAlerts ? 'red' : 'green'} />
      </div>

      <div className="grid gap-5 lg:grid-cols-3">
        <Panel className="p-5 lg:col-span-2">
          <SectionTitle icon={Database} title="每日 Token 用量" hint="最近 14 天" />
          <TokenChart data={analytics.daily_tokens} />
        </Panel>
        <Panel className="p-5">
          <SectionTitle icon={Terminal} title="工具调用排行" hint="Top 8" />
          <ToolBars stats={analytics.tool_stats} />
        </Panel>
      </div>

      <div className="grid gap-5 lg:grid-cols-2">
        <Panel className="p-5">
          <SectionTitle icon={ScrollText} title="最近任务" hint={`${tasks.count} 条`} />
          <div className="scrollbar-thin max-h-[420px] space-y-2 overflow-y-auto pr-1">
            {recentTasks.map((t) => {
              const meta = statusMeta[t.status] || { label: t.status, cls: 'bg-muted/20 text-muted-foreground' };
              return (
                <div key={t.id} className="rounded-xl border border-border/60 bg-card/40 p-3.5 transition-colors hover:bg-card/70">
                  <div className="flex items-start gap-3">
                    <Badge className={meta.cls}>{meta.label}</Badge>
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-sm font-medium" title={t.title}>{t.title}</div>
                      {t.project && <div className="mt-0.5 text-[11px] text-muted-foreground">{t.project}</div>}
                    </div>
                    <span className="shrink-0 text-[11px] text-muted-foreground">{relTime(t.updated_at)}</span>
                  </div>
                  {t.summary && <p className="mt-2 line-clamp-2 text-xs leading-relaxed text-muted-foreground">{t.summary}</p>}
                </div>
              );
            })}
          </div>
        </Panel>
        <Panel className="p-5">
          <SectionTitle icon={AlertTriangle} title="告警" hint={`${activeAlerts} 条活跃`} />
          <div className="scrollbar-thin max-h-[420px] space-y-2 overflow-y-auto pr-1">
            {alerts.alerts.filter((a) => a.active).slice(0, 12).map((a) => (
              <div key={a.id} className="rounded-xl border border-amber-500/25 bg-amber-500/5 p-3.5">
                <div className="text-sm">{a.message}</div>
                <div className="mt-1 text-xs leading-relaxed text-muted-foreground">{a.detail}</div>
                <div className="mt-1.5 text-[11px] text-muted-foreground">{relTime(a.last_seen)}更新</div>
              </div>
            ))}
            {activeAlerts === 0 && (
              <div className="flex flex-col items-center justify-center gap-2 py-12 text-center">
                <Bot size={28} className="text-emerald-500" />
                <div className="text-sm font-medium">一切正常</div>
              </div>
            )}
          </div>
        </Panel>
      </div>

      <div className="grid gap-5 lg:grid-cols-3">
        <Panel className="p-5">
          <SectionTitle icon={Blocks} title="进程健康" />
          <div className="grid grid-cols-2 gap-4">
            {[
              ['运行时间', duration(analytics.process.uptime_ms)],
              ['Goroutines', fmt.format(analytics.process.goroutines)],
              ['堆内存分配', bytes(analytics.process.heap_alloc_bytes)],
              ['GC 次数', fmt.format(analytics.process.gc_cycles)],
              ['窗口调用', fmt.format(analytics.window_calls)],
              ['活跃调用', fmt.format(analytics.active_calls)],
            ].map(([k, v]) => (
              <div key={k}>
                <div className="text-xs text-muted-foreground">{k}</div>
                <div className="tabular mt-1 text-lg font-semibold">{v}</div>
              </div>
            ))}
          </div>
        </Panel>
        <Panel className="p-5">
          <SectionTitle icon={Boxes} title="能力概览" />
          <div className="grid grid-cols-3 gap-3">
            {[
              [Boxes, overview.skills.count, 'Skills'],
              [Plug, overview.plugins.count, 'Plugins'],
              [Wifi, overview.mcp.count, 'MCP'],
            ].map(([Icon, val, label]: any) => (
              <div key={label} className="rounded-xl bg-card/40 p-3 text-center">
                <Icon size={18} className="mx-auto text-muted-foreground" />
                <div className="tabular mt-2 text-xl font-semibold">{val as number}</div>
                <div className="text-[11px] text-muted-foreground">{label as string}</div>
              </div>
            ))}
          </div>
          <div className="mt-4 flex flex-wrap gap-x-4 gap-y-1.5 text-xs text-muted-foreground">
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
        <Panel className="p-5">
          <SectionTitle icon={Terminal} title="工具清单" hint={`${status.tools.length} 个`} />
          <div className="scrollbar-thin flex max-h-[220px] flex-wrap content-start gap-1.5 overflow-y-auto pr-1">
            {status.tools.map((t) => (
              <span key={t} className="glass-control rounded-md px-2 py-1 font-mono text-[11px] text-muted-foreground">{t}</span>
            ))}
          </div>
        </Panel>
      </div>
    </div>
  );
}
