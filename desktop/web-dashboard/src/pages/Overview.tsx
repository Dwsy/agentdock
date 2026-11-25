import { useMemo, useState, type ElementType, type ReactNode } from 'react';
import {
  Activity,
  AlertTriangle,
  ArrowUpRight,
  Blocks,
  Bot,
  Boxes,
  CircleSlash,
  Clock3,
  Gauge,
  Info,
  ListChecks,
  Plug,
  ScrollText,
  Terminal,
  Wifi,
  Zap,
} from 'lucide-react';
import type { AlertRule, Alerts, Analytics, Overview as OverviewData, Status, Tasks } from '../api';
import { Badge, EmptyState, Panel, compact, duration, bytes, fmt, relTime } from '../ui';
import HistoryChart from '../components/HistoryChart';
import { DEFAULT_HISTORY_SELECTION, historyBounds, historyDateLabel, localHistoryDate } from '../lib/historyStats';

type Props = {
  status: Status;
  overview: OverviewData;
  analytics: Analytics;
  alerts: Alerts;
  tasks: Tasks;
  onOpenAnalytics: () => void;
};

type Tone = 'default' | 'green' | 'amber' | 'red' | 'violet' | 'sky';

const DASH = '—';
/** iOS 小组件式的彩色图标块 + 同色数值，靠色彩建立层级而非加大字号。 */
const tileTone: Record<Tone, { icon: string; value: string }> = {
  default: { icon: 'bg-primary/10 text-primary', value: 'text-foreground' },
  green: { icon: 'bg-emerald-500/12 text-emerald-500', value: 'text-emerald-500' },
  amber: { icon: 'bg-amber-500/12 text-amber-500', value: 'text-amber-500' },
  red: { icon: 'bg-red-500/12 text-red-500', value: 'text-red-500' },
  violet: { icon: 'bg-violet-500/12 text-violet-500', value: 'text-violet-500' },
  sky: { icon: 'bg-sky-500/12 text-sky-500', value: 'text-sky-500' },
};

function isNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value);
}

/** 缺失字段显示破折号，不用 0 冒充真实计数。 */
function countText(value: unknown, format: (value: number) => string = fmt.format): string {
  return isNumber(value) ? format(value) : DASH;
}

/** 服务端生成时间按浏览器本地时区展示；跨天时带日期，缺失时明确显示未知。 */
function serverTime(iso?: string): string {
  if (!iso) return DASH;
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return DASH;
  const sameDay = date.toDateString() === new Date().toDateString();
  return sameDay
    ? date.toLocaleTimeString('zh-CN', { hour12: false })
    : date.toLocaleString('zh-CN', { hour12: false });
}

function serverTimeTitle(label: string, iso?: string): string {
  const date = iso ? new Date(iso) : null;
  if (!date || Number.isNaN(date.getTime())) {
    return `${label}：接口未提供生成时间（不会用页面刷新时间代替）`;
  }
  return `${label}：${date.toLocaleString('zh-CN', { hour12: false })}（服务端生成，和右上角页面刷新时间不同）`;
}

type WindowCoverage = { state: 'complete' | 'partial' | 'unknown'; text: string };

/**
 * 窗口统计只来自当前 Core 的内存记录。用 started_at、generated_at 与
 * window_minutes 判断本进程是否已经覆盖完整窗口；任一字段缺失都按未知处理。
 */
function windowCoverage(analytics: Analytics): WindowCoverage {
  if (analytics.window_limited) {
    return { state: 'partial', text: '较早明细已因容量上限淘汰，实际覆盖起点未提供' };
  }
  const started = analytics.started_at ? new Date(analytics.started_at) : null;
  const generated = analytics.generated_at ? new Date(analytics.generated_at) : null;
  const minutes = analytics.window_minutes;
  if (
    !started || Number.isNaN(started.getTime()) ||
    !generated || Number.isNaN(generated.getTime()) ||
    !isNumber(minutes) || minutes <= 0
  ) {
    return { state: 'unknown', text: '覆盖起点未知（缺少 started_at、generated_at 或 window_minutes）' };
  }
  const windowStart = generated.getTime() - minutes * 60_000;
  if (started.getTime() <= windowStart) {
    return { state: 'complete', text: `已覆盖完整 ${minutes} 分钟窗口` };
  }
  const covered = Math.max(0, generated.getTime() - started.getTime());
  return {
    state: 'partial',
    text: `进程启动未满 ${minutes} 分钟，仅覆盖启动后的 ${duration(covered)}（自 ${serverTime(analytics.started_at)} 起）`,
  };
}

/** 紧凑指标小组件：图标带圆角色块，数值克制，去掉松散的大 padding。 */
function MetricTile({
  icon: Icon,
  label,
  value,
  sub,
  tone = 'default',
  basis,
}: {
  icon: ElementType;
  label: string;
  value: string | number;
  sub?: string;
  tone?: Tone;
  basis?: string;
}) {
  const t = tileTone[tone];
  return (
    <Panel className="animate-float-in relative overflow-hidden p-3.5 sm:p-4">
      <div className="flex items-start justify-between gap-2">
        <div className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-[11px] ${t.icon}`}>
          <Icon size={18} strokeWidth={2} />
        </div>
      </div>
      <div className="mt-3 flex items-center gap-1 text-[11px] font-medium text-muted-foreground" title={basis}>
        <span className="truncate">{label}</span>
        {basis && <Info size={10} className="shrink-0 opacity-60" aria-hidden="true" />}
      </div>
      <div className={`tabular mt-0.5 text-[22px] font-semibold leading-tight tracking-tight ${t.value}`}>{value}</div>
      {sub && <div className="mt-1 truncate text-[11px] text-muted-foreground/80" title={sub}>{sub}</div>}
    </Panel>
  );
}

/** 小组件标题：图标底块 + 紧凑行距，替代全局 SectionTitle 的松散间距。 */
function WidgetTitle({
  icon: Icon,
  title,
  hint,
  hintTitle,
  action,
}: {
  icon: ElementType;
  title: string;
  hint?: string;
  hintTitle?: string;
  action?: ReactNode;
}) {
  return (
    <div className="mb-3 flex items-center gap-2">
      <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-muted/60 text-muted-foreground">
        <Icon size={14} />
      </div>
      <span className="text-[13px] font-semibold tracking-wide">{title}</span>
      {hint && (
        <span className="truncate text-[11px] text-muted-foreground" title={hintTitle ?? hint}>
          {hint}
        </span>
      )}
      {action && <div className="ml-auto">{action}</div>}
    </div>
  );
}

/** 首页各接口的服务端生成时间。它和右上角的页面刷新时间不是同一个量。 */
function StatTimes({ items }: { items: { label: string; iso?: string }[] }) {
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-xl border border-border/60 bg-card/40 px-3 py-2 text-[11px] text-muted-foreground">
      <span className="flex items-center gap-1 font-medium text-foreground/80">
        <Clock3 size={12} aria-hidden="true" /> 服务端统计时间
      </span>
      {items.map((item) => (
        <span key={item.label} title={serverTimeTitle(item.label, item.iso)}>
          {item.label} <span className="tabular text-foreground/80">{serverTime(item.iso)}</span>
        </span>
      ))}
      <span className="text-muted-foreground/70 sm:ml-auto">与右上角页面刷新时间不同</span>
    </div>
  );
}

function BasisBlock({ title, source, children }: { title: string; source: string; children: ReactNode }) {
  return (
    <div>
      <div className="flex flex-wrap items-baseline gap-1.5 text-[12px] font-medium text-foreground/90">
        {title}
        <span className="font-mono text-[10px] font-normal text-muted-foreground">{source}</span>
      </div>
      <div className="mt-1 space-y-1 leading-relaxed">{children}</div>
    </div>
  );
}

const ruleLabels: Record<string, string> = {
  error_rate: '窗口错误率',
  trace_disk: 'trace 磁盘占用',
  task_blocked: '任务阻塞时长',
};

function ruleValue(rule: AlertRule): string {
  if (!isNumber(rule.threshold)) return '阈值未提供';
  switch (rule.kind) {
    case 'error_rate':
      return `${fmt.format(Math.round(rule.threshold * 1000) / 10)}%`;
    case 'trace_disk':
      return `${fmt.format(rule.threshold)} MB`;
    case 'task_blocked':
      return `${fmt.format(rule.threshold)} 小时`;
    default:
      return String(rule.threshold);
  }
}

export function ToolBars({
  stats,
  state,
  windowMinutes,
  windowLimited,
}: {
  stats: Analytics['tool_stats'];
  state: 'ok' | 'unavailable' | 'missing';
  windowMinutes?: number;
  windowLimited: boolean;
}) {
  if (state === 'unavailable') return <div className="py-6 text-center text-sm text-muted-foreground">调用统计暂不可用</div>;
  if (state === 'missing') return <div className="py-6 text-center text-sm text-muted-foreground">工具统计字段未提供</div>;
  const all = Array.isArray(stats) ? stats : [];
  const rows = [...all].sort((a, b) => b.count - a.count).slice(0, 8);
  if (rows.length === 0) {
    return (
      <div className="py-6 text-center text-sm text-muted-foreground">
        {isNumber(windowMinutes) ? `最近 ${windowMinutes} 分钟暂无调用` : '窗口内暂无调用'}
      </div>
    );
  }
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
      <div className="pt-0.5 text-[10px] text-muted-foreground/80">
        窗口内共 {all.length} 个工具{all.length > 8 ? ' · 显示调用数前 8' : ''}
        {windowLimited ? ' · 明细已达上限，计数可能偏低' : ''}
      </div>
    </div>
  );
}

const statusMeta: Record<string, { label: string; cls: string }> = {
  completed: { label: '已完成', cls: 'bg-emerald-500/10 text-emerald-500' },
  active: { label: '进行中', cls: 'bg-sky-500/10 text-sky-500' },
  blocked: { label: '阻塞', cls: 'bg-amber-500/10 text-amber-500' },
  pending: { label: '待处理', cls: 'bg-muted/20 text-muted-foreground' },
};

export default function Overview({ status, overview, analytics, alerts, tasks, onOpenAnalytics }: Props) {
  const statusAvailable = status.ok === true;
  const overviewAvailable = overview.ok === true;
  const analyticsAvailable = analytics.ok === true;
  const alertsAvailable = alerts.ok === true;
  const tasksAvailable = tasks.ok === true;

  const taskScope = overviewAvailable ? overview.tasks?.scope : undefined;
  const recentHours = taskScope?.recent_window_hours;
  const taskLimit = taskScope?.limit;
  const tasksMatched = taskScope?.matched;
  const taskTruncated = taskScope?.truncated === true;

  const activeTasks = overviewAvailable ? countText(overview.tasks?.active) : DASH;
  const activeRecent = countText(overview.tasks?.active_recent_24h);
  const activeSub = overviewAvailable
    ? `${isNumber(recentHours) ? `活跃且 ${recentHours}h 内更新` : '近期有更新'} ${activeRecent}${taskTruncated ? ' · 已达统计上限' : ''}`
    : '任务概览暂不可用';

  const blockedTasks = overviewAvailable ? countText(overview.tasks?.blocked) : DASH;
  const blockedSub = overviewAvailable ? `已完成 ${countText(overview.tasks?.completed)}` : '任务概览暂不可用';

  const totalCalls = analyticsAvailable ? countText(analytics.total_calls, compact) : DASH;
  const callsSub = !analyticsAvailable
    ? '调用统计暂不可用'
    : !isNumber(analytics.total_calls)
      ? '错误率 —'
      : analytics.total_calls === 0
        ? '暂无调用'
        : isNumber(analytics.total_errors)
          ? `错误率 ${((analytics.total_errors / analytics.total_calls) * 100).toFixed(2)}%`
          : '错误率 —';

  const outputTokens = analyticsAvailable ? countText(analytics.total_output_tokens, compact) : DASH;
  const tokenSub = analyticsAvailable ? `输入 ${countText(analytics.total_input_tokens, compact)}` : '调用统计暂不可用';

  const alertListPresent = Array.isArray(alerts.alerts);
  const alertList = alertListPresent ? alerts.alerts : [];
  const activeAlertEntries = alertsAvailable && alertListPresent ? alertList.filter((alert) => alert.active) : [];
  const activeAlerts = alertsAvailable ? (alertListPresent ? activeAlertEntries.length : undefined) : undefined;
  const alertsSub = !alertsAvailable
    ? '告警接口暂不可用'
    : alertListPresent
      ? `已恢复 ${countText(alerts.resolved_count)}`
      : '告警列表字段未提供';
  const alertsHint = !alertsAvailable
    ? '告警接口暂不可用'
    : !alertListPresent
      ? '告警列表字段未提供'
      : `${activeAlerts} 条活跃 · 评估于 ${serverTime(alerts.generated_at)}`;

  const tasksListPresent = Array.isArray(tasks.tasks);
  const recentTasks = useMemo(() => {
    if (!tasks.ok || !tasksListPresent) return [];
    return [...tasks.tasks].sort((a, b) => +new Date(b.updated_at) - +new Date(a.updated_at)).slice(0, 8);
  }, [tasks.ok, tasks.tasks, tasksListPresent]);
  const loadedTaskCount = isNumber(tasks.count) ? tasks.count : tasksListPresent ? tasks.tasks.length : undefined;
  const tasksHint = !tasksAvailable
    ? '任务列表暂不可用'
    : !tasksListPresent
      ? '任务列表字段未提供'
      : `${countText(loadedTaskCount)} 条已载入${isNumber(tasks.limit) && loadedTaskCount === tasks.limit ? ' · 已达单次载入上限' : ''}`;

  const [tokenSelection, setTokenSelection] = useState(DEFAULT_HISTORY_SELECTION);
  const dailyTokens = analyticsAvailable && Array.isArray(analytics.daily_tokens) ? analytics.daily_tokens : undefined;
  const tokenPoints = useMemo(() => (dailyTokens ?? [])
    .filter(({ date }) => /^\d{4}-\d{2}-\d{2}$/.test(date) && Number.isFinite(Date.parse(date)))
    .map((day) => ({ date: day.date, first: day.input_tokens, second: day.output_tokens, count: 1 })), [dailyTokens]);
  const tokenDates = useMemo(() => tokenPoints.map(({ date }) => date), [tokenPoints]);
  const today = analytics.local_date || localHistoryDate();
  const tokenBounds = useMemo(() => historyBounds(tokenSelection.range, today, tokenDates), [tokenSelection.range, today, tokenDates]);
  const tokenCoverage = useMemo(() => historyBounds('all', today, tokenDates), [today, tokenDates]);
  const retentionDays = analytics.daily_token_retention_days;

  const toolStats = Array.isArray(analytics.tool_stats) ? analytics.tool_stats : undefined;
  const toolState: 'ok' | 'unavailable' | 'missing' = !analyticsAvailable
    ? 'unavailable'
    : toolStats
      ? 'ok'
      : 'missing';
  const windowMinutes = isNumber(analytics.window_minutes) ? analytics.window_minutes : undefined;
  const windowHistoryMinutes = isNumber(analytics.window_history_minutes) ? analytics.window_history_minutes : undefined;
  const coverage = windowCoverage(analytics);
  const toolHint = toolState === 'unavailable'
    ? '数据暂不可用'
    : toolState === 'missing'
      ? '工具统计字段未提供'
      : `${isNumber(windowMinutes) ? `最近 ${windowMinutes} 分钟` : '窗口长度未知'} · 仅当前 Core 内存记录${analytics.window_limited ? ' · 已达明细上限' : ''}`;

  const process = analyticsAvailable ? analytics.process : undefined;
  const processHint = !analyticsAvailable
    ? '调用统计暂不可用'
    : process
      ? `瞬时快照 · ${serverTime(analytics.generated_at)}`
      : '进程指标字段未提供';
  const processValue = (value: unknown, format: (value: number) => string) => (isNumber(value) ? format(value) : DASH);
  const windowLabel = isNumber(windowMinutes) ? `窗口调用（${windowMinutes} 分钟）` : '窗口调用';
  const processRows: [string, string, string][] = [
    ['进程运行时间', processValue(process?.uptime_ms, duration), 'Recorder 创建至今，约等于 Core 进程启动至今'],
    ['Goroutines', processValue(process?.goroutines, fmt.format), '本次快照时刻的 goroutine 数'],
    ['堆内存分配', processValue(process?.heap_alloc_bytes, bytes), '本次快照时刻的 Go 堆分配字节数'],
    ['GC 次数', processValue(process?.gc_cycles, fmt.format), 'Core 进程启动以来的 GC 周期数'],
    [windowLabel, analyticsAvailable ? countText(analytics.window_calls) : DASH, `最近窗口内完成且仍在明细中的调用数；仅当前 Core 内存记录；${coverage.text}`],
    ['活跃调用', analyticsAvailable ? countText(analytics.active_calls) : DASH, '快照时刻正在执行、尚未结束的工具调用'],
  ];

  const skillsCount = overviewAvailable ? countText(overview.skills?.count) : DASH;
  const pluginsCount = overviewAvailable
    ? overview.plugins?.available === false
      ? '不可用'
      : countText(overview.plugins?.count)
    : DASH;
  const mcpCount = overviewAvailable ? countText(overview.mcp?.count) : DASH;

  const toolsPresent = statusAvailable && Array.isArray(status.tools);
  const tools = toolsPresent ? status.tools : [];
  const toolsHint = !statusAvailable
    ? '状态接口暂不可用'
    : !toolsPresent
      ? '工具列表字段未提供'
      : `${tools.length} 个 · 当前进程注册表`;
  const capabilityItems = [
    { icon: Boxes, value: skillsCount, label: 'Skills', basis: '已安装 Skill 索引 + 已启用插件内的 Skill 组件' },
    { icon: Plug, value: pluginsCount, label: 'Plugins', basis: '已安装插件（含停用）；不可用表示插件索引读取失败' },
    { icon: Wifi, value: mcpCount, label: 'MCP', basis: '已配置的动态 MCP 服务器（含停用）' },
  ];
  const flags = [
    { label: '鉴权', on: status.auth_enabled, basis: '已配置访问令牌或启用 OAuth' },
    { label: 'Nexus', on: status.nexus_enabled, basis: '已配置 Nexus 端点' },
    { label: 'Memory', on: status.memory_enabled, basis: '与 Nexus 同源：Nexus 端点已配置' },
    { label: 'Browser', on: status.browser_enabled, basis: '浏览器工具能力开关' },
  ];

  return (
    <div className="space-y-3">
      <StatTimes
        items={[
          { label: '状态', iso: status.generated_at },
          { label: '任务概览', iso: overview.generated_at },
          { label: '调用', iso: analytics.generated_at },
          { label: '任务列表', iso: tasks.generated_at },
          { label: '告警', iso: alerts.generated_at },
        ]}
      />

      {/* 指标带：统一尺寸的小组件，彩色图标块建立层级 */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 xl:grid-cols-6">
        <MetricTile
          icon={Gauge}
          label="服务状态"
          value={statusAvailable ? '在线' : '未知'}
          sub={statusAvailable ? `${countText(status.tool_count)} 个工具可用` : '状态未知（接口请求未成功）'}
          tone={statusAvailable ? 'green' : 'default'}
          basis="来源 /status：仅请求成功且 ok=true 时显示在线；请求失败只代表状态未知。工具数 = 当前进程已注册的工具名数量。"
        />
        <MetricTile
          icon={ListChecks}
          label="活跃任务"
          value={activeTasks}
          sub={activeSub}
          tone="violet"
          basis={`来源 /overview：本地任务索引按 updated_at 倒序统计${isNumber(taskLimit) ? `前 ${fmt.format(taskLimit)} 条` : '前若干条（上限未提供）'}${taskTruncated ? '（已触及上限）' : ''}；近期活跃只计 active 且窗口内有更新的任务。`}
        />
        <MetricTile
          icon={CircleSlash}
          label="阻塞任务"
          value={blockedTasks}
          sub={blockedSub}
          tone="amber"
          basis="来源 /overview：任务状态为 blocked 的计数；同一取样范围内按 updated_at 倒序取前若干条。"
        />
        <MetricTile
          icon={Activity}
          label="总调用"
          value={totalCalls}
          sub={callsSub}
          tone="sky"
          basis={`来源 /analytics：本机已完成工具调用（含失败）的累计值；${analytics.totals_restored === true ? '启动时已从本地状态文件恢复（含此前进程已落盘部分）' : analytics.totals_restored === false ? '仅本次 Core 启动以来' : '累计起点接口未提供'}。`}
        />
        <MetricTile
          icon={Zap}
          label="累计输出 Token"
          value={outputTokens}
          sub={tokenSub}
          tone="violet"
          basis="来源 /analytics：入参与结果序列化文本的估算 token 数，按调用完成时刻累加。"
        />
        <MetricTile
          icon={AlertTriangle}
          label="活跃告警"
          value={activeAlerts ?? DASH}
          sub={alertsSub}
          tone={alertsAvailable ? (activeAlerts ? 'red' : 'green') : 'default'}
          basis="来源 /alerts：请求时惰性评估当前规则；已恢复计数只统计本次 Core 进程内观察到的恢复。"
        />
      </div>

      {/* 图表 + 排行：主次 8:4，紧凑内边距 */}
      <div className="grid gap-3 lg:grid-cols-12">
        <Panel className="p-4 lg:col-span-8">
          <HistoryChart title="Token 用量" points={tokenPoints} bounds={tokenBounds}
            selection={tokenSelection} onSelection={setTokenSelection}
            series={[{ label: '输入 Token', color: 'bg-indigo-500' }, { label: '输出 Token', color: 'bg-sky-500' }]}
            note={tokenCoverage && `Core 自然日记录 ${historyDateLabel(tokenCoverage)} · 估算 Token${analytics.local_date || tokenSelection.range === 'all' ? '' : ' · 快捷范围按浏览器日期'}`}
            empty={!analyticsAvailable ? "调用统计暂不可用" : !dailyTokens ? "当前 Core 未返回按日数据" : "该时段没有 Token 日记录"} />
        </Panel>
        <Panel className="p-4 lg:col-span-4">
          <WidgetTitle
            icon={Terminal}
            title="工具调用排行"
            hint={toolHint}
            hintTitle={`${toolHint}；统计来自 /analytics 的窗口明细，按调用数取前 8 个工具；${coverage.text}`}
            action={
              <button type="button" onClick={onOpenAnalytics} className="flex items-center gap-1 whitespace-nowrap text-xs text-primary hover:underline">
                调用分析 <ArrowUpRight size={13} aria-hidden="true" />
              </button>
            }
          />
          <ToolBars
            stats={toolStats}
            state={toolState}
            windowMinutes={windowMinutes}
            windowLimited={analytics.window_limited === true}
          />
        </Panel>
      </div>

      {/* 任务 + 告警：7:5 */}
      <div className="grid gap-3 lg:grid-cols-12">
        <Panel className="min-w-0 p-4 lg:col-span-7">
          <WidgetTitle
            icon={ScrollText}
            title="最近任务"
            hint={tasksHint}
            hintTitle={`来源 /tasks：按 updated_at 倒序，本次载入 ${countText(loadedTaskCount)} 条${isNumber(tasks.limit) ? `（请求上限 ${tasks.limit}）` : ''}；页面展示最近 8 条`}
          />
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
            {tasksAvailable && tasksListPresent && recentTasks.length === 0 && <EmptyState icon={ScrollText} title="暂无任务" />}
            {tasksAvailable && !tasksListPresent && <EmptyState icon={ScrollText} title="任务列表字段未提供" hint="接口成功返回，但没有可用的 tasks 数组" />}
            {!tasksAvailable && <EmptyState icon={ScrollText} title="任务数据暂不可用" hint="接口未返回可用数据，数字不会用 0 代替" />}
          </div>
        </Panel>
        <Panel className="p-4 lg:col-span-5">
          <WidgetTitle
            icon={AlertTriangle}
            title="告警"
            hint={alertsHint}
            hintTitle={`来源 /alerts：${serverTimeTitle('本次评估', alerts.generated_at)}；已恢复计数仅统计本次 Core 进程内的恢复`}
          />
          <div className="scrollbar-thin max-h-[380px] space-y-2 overflow-y-auto pr-1">
            {activeAlertEntries.slice(0, 12).map((a) => (
              <div key={a.id} className={`rounded-xl border p-3 ${a.severity === 'danger' ? 'border-red-500/25 bg-red-500/5' : 'border-amber-500/25 bg-amber-500/5'}`}>
                <div className="text-[13px]">{a.message}</div>
                <div className="mt-1 text-[11px] leading-relaxed text-muted-foreground">{a.detail}</div>
                <div className="mt-1 text-[10px] text-muted-foreground">
                  最近评估 {relTime(a.last_seen)}{a.first_seen ? ` · 首次观测 ${serverTime(a.first_seen)}` : ''}
                </div>
              </div>
            ))}
            {alertsAvailable && alertListPresent && activeAlertEntries.length === 0 && (
              <div className="flex flex-col items-center justify-center gap-2 py-12 text-center">
                <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-emerald-500/10">
                  <Bot size={24} className="text-emerald-500" />
                </div>
                <div className="text-[13px] font-medium">一切正常</div>
              </div>
            )}
            {alertsAvailable && !alertListPresent && (
              <EmptyState icon={AlertTriangle} title="告警列表字段未提供" hint="接口成功返回，但没有可用的 alerts 数组，不能据此判断“一切正常”" />
            )}
            {!alertsAvailable && <EmptyState icon={AlertTriangle} title="告警数据暂不可用" hint="接口未返回可用数据，不能据此判断“一切正常”" />}
          </div>
        </Panel>
      </div>

      {/* 三个信息小组件：等宽三列 */}
      <div className="grid gap-3 lg:grid-cols-3">
        <Panel className="p-4">
          <WidgetTitle
            icon={Blocks}
            title="进程健康"
            hint={processHint}
            hintTitle="来源 /analytics.process：各值只在本次快照时刻有效；运行时间从 Recorder 创建开始计算"
          />
          <div className="grid grid-cols-3 gap-x-3 gap-y-3.5">
            {processRows.map(([label, value, basis]) => (
              <div key={label} className="min-w-0" title={basis}>
                <div className="truncate text-[11px] text-muted-foreground" title={label}>{label}</div>
                <div className="tabular mt-1 truncate text-base font-semibold" title={value}>{value}</div>
              </div>
            ))}
          </div>
        </Panel>
        <Panel className="p-4">
          <WidgetTitle
            icon={Boxes}
            title="能力概览"
            hint={overviewAvailable ? `本地索引 · ${serverTime(overview.generated_at)}` : '能力概览暂不可用'}
            hintTitle="来源 /overview + /status：只统计本机索引与配置开关，不做 Skill 包校验或文件遍历"
          />
          <div className="grid grid-cols-3 gap-2.5">
            {capabilityItems.map(({ icon: Icon, value, label, basis }) => (
              <div key={label} className="rounded-xl bg-muted/40 p-2.5 text-center" title={basis}>
                <Icon size={17} className="mx-auto text-muted-foreground" />
                <div className="tabular mt-1.5 text-lg font-semibold">{value}</div>
                <div className="text-[10px] text-muted-foreground">{label}</div>
              </div>
            ))}
          </div>
          <div className="mt-3 flex flex-wrap gap-x-3.5 gap-y-1.5 text-[11px] text-muted-foreground">
            {statusAvailable ? (
              flags.map(({ label, on, basis }) => (
                <span key={label} className="flex items-center gap-1.5" title={basis}>
                  <i className={`inline-block h-1.5 w-1.5 rounded-full ${on ? 'bg-emerald-500' : 'bg-muted-foreground/40'}`} />
                  {label} {on ? '开启' : '关闭'}
                </span>
              ))
            ) : (
              <span>状态接口暂不可用，开关状态未知</span>
            )}
          </div>
        </Panel>
        <Panel className="p-4">
          <WidgetTitle
            icon={Terminal}
            title="工具清单"
            hint={toolsHint}
            hintTitle="来源 /status.tools：当前 Core 进程实际注册的工具名，不是历史累计"
          />
          {tools.length > 0 ? (
            <div className="scrollbar-thin flex max-h-[180px] flex-wrap content-start gap-1.5 overflow-y-auto pr-1">
              {tools.map((t) => (
                <span key={t} className="glass-control rounded-md px-2 py-1 font-mono text-[10px] text-muted-foreground">{t}</span>
              ))}
            </div>
          ) : (
            <div className="py-8 text-center text-[11px] text-muted-foreground">
              {!statusAvailable ? '工具列表暂不可用' : !toolsPresent ? '工具列表字段未提供' : '当前进程未注册工具'}
            </div>
          )}
        </Panel>
      </div>

      {/* 口径说明：默认折叠，避免挤占指标区；每个数字都能在这里找到来源、范围与缺失语义 */}
      <Panel className="p-4">
        <details>
          <summary className="flex cursor-pointer list-none flex-wrap items-center gap-2 text-[13px] font-semibold [&::-webkit-details-marker]:hidden">
            <Info size={15} className="text-muted-foreground" aria-hidden="true" />
            统计口径与数据来源
            <span className="text-[11px] font-normal text-muted-foreground">展开查看定义、取样范围与缺失状态</span>
          </summary>
          <div className="mt-4 grid gap-4 border-t border-border/60 pt-4 text-[11px] text-muted-foreground md:grid-cols-2">
            <BasisBlock title="服务状态" source={`/status · ${status.source || 'source 未提供'}`}>
              <p>仅请求成功且 ok=true 时显示在线；请求失败只代表状态未知，不判定为离线。工具数 = 当前进程已注册的工具名数量。统计生成于 {serverTime(status.generated_at)}。</p>
            </BasisBlock>
            <BasisBlock title="任务" source="/overview + /tasks">
              <p>
                「活跃 / 阻塞 / 已完成」扫描本地任务索引，按 updated_at 倒序取
                {isNumber(taskLimit) ? `前 ${fmt.format(taskLimit)} 条` : '若干条（上限未提供）'}
                {isNumber(tasksMatched) ? `（本次扫描 ${fmt.format(tasksMatched)} 条）` : ''}
                {taskTruncated ? '，已触及取样上限，计数可能偏低' : ''}。
                「近期活跃」只计 active 且最近 {isNumber(recentHours) ? `${recentHours} 小时` : '后端未提供窗口'}内有更新的任务。
              </p>
              <p>「最近任务」来自 /tasks，本次载入 {countText(loadedTaskCount)} 条，按更新时间倒序展示前 8 条。</p>
            </BasisBlock>
            <BasisBlock title="调用与 Token" source={`/analytics · ${analytics.source || 'source 未提供'}`}>
              <p>
                累计值来自本机 Recorder 已完成的工具调用（含失败）：
                {analytics.totals_restored === true
                  ? '启动时已从本地状态文件恢复（含此前进程已落盘部分）。'
                  : analytics.totals_restored === false
                    ? '仅含本次 Core 启动以来的累计。'
                    : '累计起点接口未提供。'}
              </p>
              <p>
                窗口调用与工具排行只使用当前 Core 的内存记录（不跨重启）；{coverage.text}。
                窗口长度为最近 {isNumber(windowMinutes) ? `${windowMinutes} 分钟` : '（窗口长度接口未提供）'}的滚动窗口；
                明细环保留最近 {isNumber(windowHistoryMinutes) ? `${windowHistoryMinutes} 分钟` : '（历史长度接口未提供）'}内的最多
                {isNumber(analytics.window_capacity) ? ` ${fmt.format(analytics.window_capacity)} 条` : '（条数上限接口未提供）'}记录，超限时淘汰最旧记录
                {analytics.window_limited ? '。当前窗口内已因上限丢弃过明细，窗口统计可能偏低' : ''}。
              </p>
              <p>
                Token 是入参与结果序列化文本的估算值，按调用完成时刻计入；每日桶按 Core 本机时区自然日划分，
                保留 {isNumber(retentionDays) ? `${retentionDays} 天（超期桶在下一次 token 写入时淘汰）` : '天数接口未提供'}。
                started_at 是进程启动时间，统计计算时间看 generated_at。
              </p>
            </BasisBlock>
            <BasisBlock title="告警" source={`/alerts · ${alerts.source || 'source 未提供'}`}>
              <p>
                接口按请求惰性评估当前规则，生成于 {serverTime(alerts.generated_at)}；
                已恢复计数只统计本次 Core 进程内观察到的恢复，重启后清零。
              </p>
              {Array.isArray(alerts.rules) && alerts.rules.length > 0 && (
                <ul className="space-y-0.5">
                  {alerts.rules.map((rule, index) => (
                    <li key={rule.kind ?? String(index)}>
                      {rule.kind ? ruleLabels[rule.kind] ?? rule.kind : '未知规则'} · {ruleValue(rule)} ·{' '}
                      {rule.enabled === true ? '启用' : rule.enabled === false ? '停用' : '状态未提供'}
                      {isNumber(rule.min_calls) ? ` · 窗口调用数 ≥ ${rule.min_calls} 才评估` : ''}
                    </li>
                  ))}
                </ul>
              )}
            </BasisBlock>
            <BasisBlock title="能力概览" source="/overview + /status">
              <p>
                Skills = 已安装 Skill 索引 + 已启用插件内的 Skill 组件；Plugins = 已安装插件（含停用，available=false 表示索引读取失败）；
                MCP = 已配置动态 MCP 服务器（含停用）；Memory 与 Nexus 同源，均为 Nexus 端点是否配置。
              </p>
            </BasisBlock>
            <BasisBlock title="缺失与过期" source="响应字段语义">
              <p>
                接口失败时页面保留上次成功数据并显示刷新失败提示；从未成功时数字显示「—」，不用 0 冒充。
                generated_at 缺失时显示「—」，不回退到页面刷新时间。
              </p>
            </BasisBlock>
          </div>
        </details>
      </Panel>
    </div>
  );
}
