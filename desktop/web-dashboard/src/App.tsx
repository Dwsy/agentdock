import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Bot,
  Boxes,
  RefreshCw,
  Radio,
  WifiOff,
  Sun,
  Moon,
  LayoutDashboard,
  ChartNoAxesCombined,
  ScrollText,
  Layers,
  ListChecks,
  Menu,
  X,
  AlertTriangle,
  Settings2,
  Terminal,
  Share2,
  Globe,
} from 'lucide-react';
import {
  api,
  subscribeEvents,
  type Alerts,
  type Analytics,
  type Artifacts,
  type Catalog,
  type FileChanges,
  type ManualLLMQueue,
  type MCPs,
  type Overview,
  type Plugins,
  type RuntimeEvent,
  type Skills,
  type Status,
  type Tasks,
  type Traces,
} from './api';
import OverviewPage from './pages/Overview';
import AnalyticsPage from './pages/Analytics';
import LivePage from './pages/Live';
import TracesPage from './pages/Traces';
import FileChangesPage from './pages/FileChanges';
import TasksPage from './pages/Tasks';
import PublishedPage from './pages/Published';
import CapabilitiesPage from './pages/Capabilities';
import ConfigPage from './pages/Config';
import ManualLLMPage from './pages/ManualLLM';
import RemoteAccessPage from './pages/RemoteAccess';
import { Panel } from './ui';
import { useHashRoute } from './lib/useHashRoute';

type Snapshot = {
  status: Status;
  overview: Overview;
  analytics: Analytics;
  alerts: Alerts;
  tasks: Tasks;
  traces: Traces;
  fileChanges: FileChanges;
  artifacts: Artifacts;
  skills: Skills;
  plugins: Plugins;
  mcp: MCPs;
  catalog: Catalog;
  manualLLM: ManualLLMQueue;
};

/** 任务列表一次拉全量（接口硬上限 10000，实际任务量远小于此）。 */
const TASK_LIMIT = 10000;

const NAV = [
  { id: 'overview', label: '概览', icon: LayoutDashboard, group: '监控' },
  { id: 'analytics', label: '调用分析', icon: ChartNoAxesCombined, group: '监控' },
  { id: 'live', label: '实时事件', icon: Radio, group: '监控' },
  { id: 'traces', label: '调用追踪', icon: ScrollText, group: '监控' },
  { id: 'files', label: '文件变更', icon: Layers, group: '监控' },
  { id: 'tasks', label: '任务与告警', icon: ListChecks, group: '管理' },
  { id: 'published', label: '文件发布', icon: Share2, group: '管理' },
  { id: 'manual_llm', label: '手动推理', icon: Terminal, group: '管理' },
  { id: 'capabilities', label: '能力目录', icon: Boxes, group: '管理' },
  { id: 'config', label: '配置', icon: Settings2, group: '管理' },
  { id: 'remote', label: '远程访问', icon: Globe, group: '管理' },
] as const;
type PageId = (typeof NAV)[number]['id'];
const PAGE_IDS: readonly PageId[] = NAV.map((n) => n.id);

type SnapshotKey = keyof Snapshot;
const SHELL_KEYS: readonly SnapshotKey[] = ['status', 'alerts', 'manualLLM'];
const PAGE_KEYS: Record<PageId, readonly SnapshotKey[]> = {
  overview: ['overview', 'analytics', 'tasks'],
  analytics: [],
  live: [],
  traces: ['traces'],
  files: ['fileChanges', 'tasks'],
  tasks: ['tasks'],
  published: ['artifacts'],
  manual_llm: [],
  capabilities: ['catalog', 'skills', 'plugins', 'mcp'],
  config: [],
  remote: [],
};

const FETCHERS: { [K in SnapshotKey]: () => Promise<Snapshot[K]> } = {
  status: () => api.status(),
  overview: () => api.overview(),
  analytics: () => api.analytics(),
  alerts: () => api.alerts(),
  tasks: () => api.tasks(TASK_LIMIT),
  // 首屏只拉摘要，并显式 before_id=0 走 ID 降序游标：磁盘回放可能按
  // StartedAt 排序，旧顺序的首屏最小 ID 会让后续游标漏记录。
  traces: () => api.traces({ limit: 80, view: 'summary', beforeId: 0 }),
  fileChanges: () => api.fileChanges(80),
  artifacts: () => api.artifacts(),
  skills: () => api.skills(),
  plugins: () => api.plugins(),
  mcp: () => api.mcp(),
  catalog: () => api.catalog(),
  manualLLM: () => api.manualLLM(),
};

export default function App() {
  const [data, setData] = useState<Snapshot | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  // 初始值对齐 index.html 预执行脚本定好的 class（记忆 > 系统偏好）
  const [dark, setDark] = useState(() => document.documentElement.classList.contains('dark'));
  // 当前页写进 URL hash，刷新/前进后退都保持。
  const [page, setHashPage] = useHashRoute<PageId>(PAGE_IDS, 'overview');
  const pageRef = useRef<PageId>(page);
  pageRef.current = page;
  const inFlightRef = useRef(false);
  const pendingForceRef = useRef(false);
  // 浏览更早记录时暂停 traces 轮询，避免新记录把列表和选中项顶掉；
  // 返回最新时由页面显式刷新并恢复。
  const tracesPausedRef = useRef(false);
  const [analyticsRefresh, setAnalyticsRefresh] = useState(0);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [events, setEvents] = useState<RuntimeEvent[]>([]);
  const [connected, setConnected] = useState(false);
  const [paused, setPaused] = useState(false);
  const pausedRef = useRef(paused);
  pausedRef.current = paused;
  const menuButtonRef = useRef<HTMLButtonElement>(null);
  const closeButtonRef = useRef<HTMLButtonElement>(null);

  const load = useCallback(async (force = false) => {
    if (!force && document.visibilityState === 'hidden') return;
    if (inFlightRef.current) {
      if (force) pendingForceRef.current = true;
      return;
    }
    inFlightRef.current = true;
    if (pageRef.current === 'analytics') setAnalyticsRefresh((tick) => tick + 1);
    try {
      // 上游忙时个别接口会超时；按可见页按需拉取并用 allSettled 隔离单项超时。
      const keys = Array.from(new Set<SnapshotKey>([...SHELL_KEYS, ...PAGE_KEYS[pageRef.current]]))
        .filter((key) => !(key === 'traces' && tracesPausedRef.current));
      const results = await Promise.allSettled(keys.map((key) => FETCHERS[key]()));
      const got: Partial<Snapshot> = {};
      const failed: string[] = [];
      results.forEach((r, i) => {
        // 请求发出后用户可能已进入历史浏览：晚到的 traces 响应也要丢弃。
        if (keys[i] === 'traces' && tracesPausedRef.current) return;
        if (r.status === 'fulfilled') (got as Record<string, unknown>)[keys[i]] = r.value;
        else failed.push(keys[i]);
      });

      setData((prev) => {
        if (prev) return { ...prev, ...got } as Snapshot; // 保留上次成功的数据
        // 首次加载：缺失的用空壳补上，保证页面能渲染（status 失败也用兜底）
        return {
          status: got.status ?? { ok: false, service: 'agentdock', version: '-', agentdock_home: '-', agentdock_default_dir: '-', path_model: '-', auth_enabled: false, browser_enabled: false, memory_enabled: false, nexus_enabled: false, tool_count: 0, tools: [] },
          overview: got.overview ?? { ok: false, source: '', tasks: { active: 0, completed: 0, blocked: 0, active_recent_24h: 0 }, skills: { count: 0 }, plugins: { count: 0, available: false }, mcp: { count: 0 } },
          analytics: got.analytics ?? { ok: false, source: '', started_at: '', total_calls: 0, total_errors: 0, window_calls: 0, active_calls: 0, total_input_tokens: 0, total_output_tokens: 0, process: { goroutines: 0, heap_alloc_bytes: 0, heap_inuse_bytes: 0, heap_sys_bytes: 0, gc_cycles: 0, uptime_ms: 0 }, tool_stats: [], recent_calls: [], daily_tokens: [] },
          alerts: got.alerts ?? { ok: false, source: '', alerts: [], resolved_count: 0, rules: [] },
          tasks: got.tasks ?? { ok: false, source: '', action: '', count: 0, tasks: [] },
          traces: got.traces ?? { ok: false, source: '', count: 0, status: '', view: '', tools: [], records: [] },
          fileChanges: got.fileChanges ?? { ok: false, source: '', count: 0, status: '', changes: [] },
          artifacts: got.artifacts ?? { ok: false, source: '', public_base_url: '', configured: false, artifacts: [], count: 0 },
          skills: got.skills ?? { ok: false, source: '', action: '', count: 0, skills: [] },
          plugins: got.plugins ?? { ok: false, source: '', count: 0, plugins: [] },
          mcp: got.mcp ?? { ok: false, source: '', action: '', count: 0, servers: [] },
          catalog: got.catalog ?? { ok: false, source: '', tool_count: 0, workdir: '', workspace_root: '', tools: [], rules: [], common_skills: { root: '', total: 0, truncated: false, items: [] } },
          manualLLM: got.manualLLM ?? { ok: false, enabled: false, responses_path: '/v1/responses', mcp_endpoint: '/mcp/manual', idle_prompt: '', stats: { queued_count: 0, active_count: 0, completed_count: 0, sessions_count: 0 }, requests: [] },
        } as Snapshot;
      });
      setError(failed.length ? `部分接口暂时无响应：${failed.join('、')}（显示的是可用数据）` : null);
      if (got.status) setLastUpdated(new Date());
      setLoading(false);
    } finally {
      inFlightRef.current = false;
      if (pendingForceRef.current) {
        pendingForceRef.current = false;
        void load(true);
      }
    }
  }, []);

  useEffect(() => {
    void load(true);
    const id = setInterval(() => {
      void load(false);
    }, 10_000);
    const onVisibilityChange = () => {
      if (document.visibilityState === 'visible') {
        void load(false);
      }
    };
    document.addEventListener('visibilitychange', onVisibilityChange);
    return () => {
      clearInterval(id);
      document.removeEventListener('visibilitychange', onVisibilityChange);
    };
  }, [load]);

  useEffect(() => {
    const unsub = subscribeEvents(
      (e) => {
        if (e.kind === 'artifact' && pageRef.current === 'published') {
          void load(true);
        }
        if (pausedRef.current) return;
        setEvents((prev) => [e, ...prev].slice(0, 200));
      },
      setConnected,
    );
    return unsub;
  }, [load]);

  const setTracesHistoryMode = useCallback((paused: boolean) => {
    tracesPausedRef.current = paused;
  }, []);

  // 返回最新：清掉历史游标后立刻重拉，而不是等下一次 10s 轮询。
  const refreshTraces = useCallback(() => {
    tracesPausedRef.current = false;
    void load(true);
  }, [load]);

  useEffect(() => {
    document.documentElement.classList.toggle('dark', dark);
    document.querySelector('meta[name=color-scheme]')?.setAttribute('content', dark ? 'dark' : 'light');
    try {
      localStorage.setItem('agentdock-theme', dark ? 'dark' : 'light');
    } catch {
      // 隐私模式下 localStorage 可能不可写，忽略即可
    }
  }, [dark]);

  // 移动端抽屉：Esc 关闭、移焦到关闭按钮、关闭后把焦点还给触发按钮。
  useEffect(() => {
    if (!sidebarOpen) return;
    closeButtonRef.current?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        setSidebarOpen(false);
      }
    };
    document.addEventListener('keydown', onKeyDown);
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      document.removeEventListener('keydown', onKeyDown);
      document.body.style.overflow = previousOverflow;
      menuButtonRef.current?.focus();
    };
  }, [sidebarOpen]);

  const navigate = (id: PageId) => {
    pageRef.current = id;
    setHashPage(id);
    setSidebarOpen(false);
    void load(true);
  };

  // 浏览器前进/后退改了 page（非点击导航）时，补拉目标页数据。
  const prevPageRef = useRef(page);
  useEffect(() => {
    if (prevPageRef.current !== page) {
      prevPageRef.current = page;
      setSidebarOpen(false);
      void load(true);
    }
  }, [page, load]);

  /* ---------- render states ---------- */

  if (loading && !data) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <div className="flex items-center gap-3 text-muted-foreground">
          <RefreshCw size={18} className="animate-spin" />
          <span className="text-sm">正在连接 AgentDock…</span>
        </div>
      </div>
    );
  }

  if (!data) {
    return (
      <div className="flex min-h-screen items-center justify-center p-6">
        <Panel className="max-w-md p-8 text-center">
          <WifiOff size={32} className="mx-auto text-red-500" />
          <h1 className="mt-4 text-lg font-semibold">连接失败</h1>
          <p className="mt-2 text-sm text-muted-foreground">{error}</p>
          <button onClick={() => void load(true)} className="glass-primary mt-6 rounded-lg px-4 py-2 text-sm font-medium text-primary-foreground">重试</button>
        </Panel>
      </div>
    );
  }

  const { status, overview, analytics, alerts, tasks, traces, fileChanges, artifacts, skills, plugins, mcp, catalog, manualLLM } = data;
  const activeAlerts = alerts.alerts.filter((a) => a.active).length;
  const activeManualCount = (manualLLM.stats?.queued_count ?? 0) + (manualLLM.stats?.active_count ?? 0);

  const content = (() => {
    switch (page) {
      case 'overview':
        return <OverviewPage status={status} overview={overview} analytics={analytics} alerts={alerts} tasks={tasks} onOpenAnalytics={() => navigate('analytics')} />;
      case 'analytics':
        return <AnalyticsPage refreshKey={analyticsRefresh} onOpenTraces={() => navigate('traces')} />;
      case 'live':
        return (
          <LivePage
            events={events}
            connected={connected}
            paused={paused}
            onTogglePause={() => setPaused((v) => !v)}
            onClear={() => setEvents([])}
          />
        );
      case 'traces':
        return <TracesPage data={traces} onHistoryModeChange={setTracesHistoryMode} onRefreshLatest={refreshTraces} />;
      case 'files':
        return <FileChangesPage data={fileChanges} tasks={tasks} />;
      case 'tasks':
        return <TasksPage data={tasks} alerts={alerts} />;
      case 'published':
        return <PublishedPage data={artifacts} onRefresh={() => load(true)} />;
      case 'manual_llm':
        return <ManualLLMPage data={manualLLM} onRefresh={() => void load(true)} onOpenConfig={() => navigate('config')} />;
      case 'capabilities':
        return <CapabilitiesPage catalog={catalog} skills={skills} plugins={plugins} mcp={mcp} status={status} />;
      case 'config':
        return <ConfigPage />;
      case 'remote':
        return <RemoteAccessPage />;
    }
  })();

  const groups = ['监控', '管理'];

  return (
    <div className="app-shell min-h-dvh">
      {/* 移动端遮罩：抽屉浮在页面之上，点击空白处关闭 */}
      {sidebarOpen && (
        <div
          className="app-overlay fixed inset-0 z-30 bg-black/35 backdrop-blur-[2px] lg:hidden"
          onClick={() => setSidebarOpen(false)}
          aria-hidden="true"
        />
      )}

      {/* 悬浮侧栏：216–224px 宽、留外距、24px 圆角 */}
      <aside
        id="app-sidebar"
        aria-label="主导航"
        className={`app-sidebar glass-panel fixed z-40 flex flex-col overflow-hidden rounded-[24px] p-3 ${sidebarOpen ? 'is-open' : ''}`}
      >
        <div className="mb-4 flex items-center gap-2.5 px-1">
          <div className="glass-primary flex h-8 w-8 shrink-0 items-center justify-center rounded-xl text-primary-foreground"><Bot size={16} /></div>
          <div className="min-w-0 flex-1">
            <div className="truncate text-sm font-semibold">AgentDock</div>
            <div className="text-[11px] text-muted-foreground">远程控制台</div>
          </div>
          <button
            ref={closeButtonRef}
            onClick={() => setSidebarOpen(false)}
            aria-label="关闭导航"
            className="u-press glass-control flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 lg:hidden"
          >
            <X size={15} />
          </button>
        </div>

        <nav className="flex-1 space-y-4 overflow-y-auto scrollbar-thin">
          {groups.map((group) => (
            <div key={group}>
              <div className="mb-1.5 px-2 text-[11px] font-semibold uppercase tracking-widest text-muted-foreground/70">{group}</div>
              <div className="space-y-0.5">
                {NAV.filter((item) => item.group === group).map((item) => {
                  const Icon = item.icon;
                  const active = page === item.id;
                  return (
                    <button
                      key={item.id}
                      onClick={() => navigate(item.id)}
                      aria-current={active ? 'page' : undefined}
                      className={`u-press flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-sm transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 ${
                        active ? 'glass-chosen text-foreground' : 'text-muted-foreground hover:bg-card/50 hover:text-foreground'
                      }`}
                    >
                      <Icon size={16} className="shrink-0" />
                      <span className="whitespace-nowrap">{item.label}</span>
                      {item.id === 'live' && <span className={`ml-auto h-1.5 w-1.5 rounded-full ${connected ? 'bg-emerald-500' : 'bg-muted-foreground/40'}`} />}
                      {item.id === 'tasks' && activeAlerts > 0 && <span className="ml-auto rounded bg-amber-500/15 px-1.5 text-[11px] text-amber-500">{activeAlerts}</span>}
                      {item.id === 'manual_llm' && activeManualCount > 0 && <span className="ml-auto rounded bg-sky-500/15 px-1.5 text-[11px] text-sky-500">{activeManualCount}</span>}
                    </button>
                  );
                })}
              </div>
            </div>
          ))}
        </nav>

        <div className="mt-auto space-y-1.5 border-t border-border/50 pt-3 text-[11px] text-muted-foreground">
          <div className="flex items-center justify-between"><span>服务</span><span className={status.ok ? 'text-emerald-500' : 'text-muted-foreground'}>{status.ok ? '在线' : '未知'}</span></div>
          <div className="flex min-w-0 items-center justify-between gap-2"><span className="shrink-0">版本</span><span className="truncate font-mono" title={`v${status.version}`}>v{status.version}</span></div>
          <div className="flex items-center justify-between"><span>工具</span><span className="tabular">{status.tool_count}</span></div>
        </div>
      </aside>

      {/* 主内容：与悬浮侧栏留出同一外距 */}
      <div className="app-main min-w-0">
        <header className="app-topbar glass-panel flex items-center gap-2 px-3 sm:px-4">
          <button
            ref={menuButtonRef}
            onClick={() => setSidebarOpen(true)}
            aria-label="打开导航"
            aria-expanded={sidebarOpen}
            aria-controls="app-sidebar"
            className="u-press glass-control flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 lg:hidden"
          >
            <Menu size={18} />
          </button>
          <div className="flex min-w-0 items-center gap-2">
            <h1 className="truncate text-sm font-semibold">{NAV.find((n) => n.id === page)?.label}</h1>
            <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${status.ok ? 'bg-emerald-500' : 'bg-muted-foreground/40'}`} />
            <span className="hidden whitespace-nowrap text-xs text-muted-foreground sm:inline" title={status.ok ? '服务在线' : '状态未知：状态接口请求未成功'}>
              {status.ok ? '在线' : '未知'}
            </span>
          </div>
          <div className="ml-auto flex items-center gap-1.5">
            <span className="hidden items-center gap-1.5 text-xs text-muted-foreground md:flex">
              {error ? <><WifiOff size={13} className="text-red-500" /> 刷新失败</> : <><Radio size={13} className="text-emerald-500" /> 10s</>}
            </span>
            {lastUpdated && (
              <span
                className="tabular hidden text-xs text-muted-foreground md:inline"
                title="页面刷新时间（不是服务端统计生成时间）"
              >
                {lastUpdated.toLocaleTimeString('zh-CN')}
              </span>
            )}
            <button
              onClick={() => void load(true)}
              aria-label="立即刷新"
              title="立即刷新"
              className="u-press glass-control flex h-8 w-8 items-center justify-center rounded-lg text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2"
            >
              <RefreshCw size={15} className={loading ? 'animate-spin' : ''} />
            </button>
            <button
              onClick={() => setDark((v) => !v)}
              aria-label={dark ? '切换到浅色主题' : '切换到深色主题'}
              title={dark ? '切换到浅色主题' : '切换到深色主题'}
              className="u-press glass-control flex h-8 w-8 items-center justify-center rounded-lg text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2"
            >
              {dark ? <Sun size={16} /> : <Moon size={16} />}
            </button>
          </div>
        </header>

        <main className="pb-10 pt-4">
          {error && (
            <div className="mb-4 flex items-center gap-2 rounded-xl border border-red-500/30 bg-red-500/10 px-4 py-2.5 text-sm text-red-500">
              <AlertTriangle size={15} /> 最近一次刷新失败：{error}（展示的是上次成功的数据）
            </div>
          )}
          {content}
        </main>
      </div>
    </div>
  );
}
