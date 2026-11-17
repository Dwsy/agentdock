import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Activity,
  Bot,
  Boxes,
  RefreshCw,
  Radio,
  WifiOff,
  Sun,
  Moon,
  LayoutDashboard,
  ScrollText,
  Layers,
  ListChecks,
  Menu,
  X,
  AlertTriangle,
  Settings2,
} from 'lucide-react';
import {
  api,
  subscribeEvents,
  type Alerts,
  type Analytics,
  type Catalog,
  type FileChanges,
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
import LivePage from './pages/Live';
import TracesPage from './pages/Traces';
import FileChangesPage from './pages/FileChanges';
import TasksPage from './pages/Tasks';
import CapabilitiesPage from './pages/Capabilities';
import ConfigPage from './pages/Config';
import { Panel } from './ui';

type Snapshot = {
  status: Status;
  overview: Overview;
  analytics: Analytics;
  alerts: Alerts;
  tasks: Tasks;
  traces: Traces;
  fileChanges: FileChanges;
  skills: Skills;
  plugins: Plugins;
  mcp: MCPs;
  catalog: Catalog;
};

const NAV = [
  { id: 'overview', label: '概览', icon: LayoutDashboard, group: '监控' },
  { id: 'live', label: '实时事件', icon: Radio, group: '监控' },
  { id: 'traces', label: '调用追踪', icon: ScrollText, group: '监控' },
  { id: 'files', label: '文件变更', icon: Layers, group: '监控' },
  { id: 'tasks', label: '任务与告警', icon: ListChecks, group: '管理' },
  { id: 'capabilities', label: '能力目录', icon: Boxes, group: '管理' },
  { id: 'config', label: '配置', icon: Settings2, group: '管理' },
] as const;
type PageId = (typeof NAV)[number]['id'];

export default function App() {
  const [data, setData] = useState<Snapshot | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  // 初始值对齐 index.html 预执行脚本定好的 class（记忆 > 系统偏好）
  const [dark, setDark] = useState(() => document.documentElement.classList.contains('dark'));
  const [page, setPage] = useState<PageId>('overview');
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [events, setEvents] = useState<RuntimeEvent[]>([]);
  const [connected, setConnected] = useState(false);
  const [paused, setPaused] = useState(false);
  const pausedRef = useRef(paused);
  pausedRef.current = paused;

  const load = useCallback(async () => {
    // 上游忙时个别接口会超时；用 allSettled 让单个失败不拖垮整页。
    const keys = ['status', 'overview', 'analytics', 'alerts', 'tasks', 'traces', 'fileChanges', 'skills', 'plugins', 'mcp', 'catalog'] as const;
    const results = await Promise.allSettled([
      api.status(), api.overview(), api.analytics(), api.alerts(), api.tasks(50),
      api.traces(80), api.fileChanges(80), api.skills(), api.plugins(), api.mcp(), api.catalog(),
    ]);
    const got: Partial<Snapshot> = {};
    const failed: string[] = [];
    results.forEach((r, i) => {
      if (r.status === 'fulfilled') (got as any)[keys[i]] = r.value;
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
        skills: got.skills ?? { ok: false, source: '', action: '', count: 0, skills: [] },
        plugins: got.plugins ?? { ok: false, source: '', count: 0, plugins: [] },
        mcp: got.mcp ?? { ok: false, source: '', action: '', count: 0, servers: [] },
        catalog: got.catalog ?? { ok: false, source: '', tool_count: 0, workdir: '', workspace_root: '', tools: [], rules: [], common_skills: { root: '', total: 0, truncated: false, items: [] } },
      } as Snapshot;
    });
    setError(failed.length ? `部分接口暂时无响应：${failed.join('、')}（显示的是可用数据）` : null);
    if (got.status) setLastUpdated(new Date());
    setLoading(false);
  }, []);

  useEffect(() => {
    load();
    const id = setInterval(load, 10_000);
    return () => clearInterval(id);
  }, [load]);

  useEffect(() => {
    const unsub = subscribeEvents(
      (e) => {
        if (pausedRef.current) return;
        setEvents((prev) => [e, ...prev].slice(0, 200));
      },
      setConnected,
    );
    return unsub;
  }, []);

  useEffect(() => {
    document.documentElement.classList.toggle('dark', dark);
    document.querySelector('meta[name=color-scheme]')?.setAttribute('content', dark ? 'dark' : 'light');
    try {
      localStorage.setItem('agentdock-theme', dark ? 'dark' : 'light');
    } catch {
      // 隐私模式下 localStorage 可能不可写，忽略即可
    }
  }, [dark]);

  const navigate = (id: PageId) => {
    setPage(id);
    setSidebarOpen(false);
  };

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
          <button onClick={load} className="glass-primary mt-6 rounded-lg px-4 py-2 text-sm font-medium text-primary-foreground">重试</button>
        </Panel>
      </div>
    );
  }

  const { status, overview, analytics, alerts, tasks, traces, fileChanges, skills, plugins, mcp, catalog } = data;
  const activeAlerts = alerts.alerts.filter((a) => a.active).length;

  const content = (() => {
    switch (page) {
      case 'overview':
        return <OverviewPage status={status} overview={overview} analytics={analytics} alerts={alerts} tasks={tasks} />;
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
        return <TracesPage data={traces} />;
      case 'files':
        return <FileChangesPage data={fileChanges} />;
      case 'tasks':
        return <TasksPage data={tasks} alerts={alerts} />;
      case 'capabilities':
        return <CapabilitiesPage catalog={catalog} skills={skills} plugins={plugins} mcp={mcp} status={status} />;
      case 'config':
        return <ConfigPage />;
    }
  })();

  const groups = ['监控', '管理'];

  return (
    <div className="min-h-screen lg:flex">
      {/* sidebar */}
      {sidebarOpen && <div className="fixed inset-0 z-20 bg-black/50 lg:hidden" onClick={() => setSidebarOpen(false)} />}
      <aside className={`glass-panel fixed inset-y-0 left-0 z-30 flex w-60 flex-col border-r border-border/60 p-4 transition-transform lg:sticky lg:top-0 lg:h-screen lg:translate-x-0 ${sidebarOpen ? 'translate-x-0' : '-translate-x-full'}`}>
        <div className="mb-6 flex items-center gap-3 px-1">
          <div className="glass-primary flex h-9 w-9 items-center justify-center rounded-lg text-primary-foreground"><Bot size={18} /></div>
          <div className="min-w-0">
            <div className="truncate text-sm font-semibold">AgentDock</div>
            <div className="text-[11px] text-muted-foreground">远程控制台</div>
          </div>
          <button className="ml-auto text-muted-foreground lg:hidden" onClick={() => setSidebarOpen(false)}><X size={18} /></button>
        </div>

        <nav className="flex-1 space-y-5">
          {groups.map((g) => (
            <div key={g}>
              <div className="mb-1.5 px-2 text-[10px] font-semibold uppercase tracking-widest text-muted-foreground/70">{g}</div>
              <div className="space-y-1">
                {NAV.filter((n) => n.group === g).map((n) => {
                  const Icon = n.icon;
                  const active = page === n.id;
                  return (
                    <button
                      key={n.id}
                      onClick={() => navigate(n.id)}
                      className={`flex w-full items-center gap-2.5 rounded-lg px-3 py-2 text-sm transition-colors ${active ? 'glass-chosen text-foreground' : 'text-muted-foreground hover:bg-card/50 hover:text-foreground'}`}
                    >
                      <Icon size={16} />
                      <span>{n.label}</span>
                      {n.id === 'live' && <span className={`ml-auto h-1.5 w-1.5 rounded-full ${connected ? 'bg-emerald-500' : 'bg-muted-foreground/40'}`} />}
                      {n.id === 'tasks' && activeAlerts > 0 && <span className="ml-auto rounded bg-amber-500/15 px-1.5 text-[10px] text-amber-500">{activeAlerts}</span>}
                    </button>
                  );
                })}
              </div>
            </div>
          ))}
        </nav>

        <div className="mt-auto space-y-2 border-t border-border/60 pt-4 text-[11px] text-muted-foreground">
          <div className="flex items-center justify-between"><span>服务</span><span className={status.ok ? 'text-emerald-500' : 'text-red-500'}>{status.ok ? '在线' : '离线'}</span></div>
          <div className="flex items-center justify-between"><span>版本</span><span className="font-mono">v{status.version}</span></div>
          <div className="flex items-center justify-between"><span>工具</span><span className="tabular">{status.tool_count}</span></div>
        </div>
      </aside>

      {/* main */}
      <div className="min-w-0 flex-1">
        <header className="sticky top-0 z-10 mb-5 flex items-center gap-4 border-b border-border/50 glass-panel px-4 py-3 sm:px-6">
          <button className="text-muted-foreground lg:hidden" onClick={() => setSidebarOpen(true)}><Menu size={20} /></button>
          <div className="min-w-0">
            <h1 className="truncate text-base font-semibold">{NAV.find((n) => n.id === page)?.label}</h1>
            <p className="truncate text-[11px] text-muted-foreground">{status.service} · {status.path_model} 模式 · {status.agentdock_home}</p>
          </div>
          <div className="ml-auto flex items-center gap-3">
            <span className="hidden items-center gap-1.5 text-xs text-muted-foreground sm:flex">
              {error ? <><WifiOff size={14} className="text-red-500" /> 连接中断</> : <><Radio size={14} className="text-emerald-500" /> 自动刷新 10s</>}
            </span>
            {lastUpdated && <span className="hidden text-xs text-muted-foreground md:inline">{lastUpdated.toLocaleTimeString('zh-CN')}</span>}
            <button onClick={load} className="glass-control flex h-9 w-9 items-center justify-center rounded-lg text-muted-foreground hover:text-foreground" title="立即刷新">
              <RefreshCw size={15} className={loading ? 'animate-spin' : ''} />
            </button>
            <button onClick={() => setDark((v) => !v)} className="glass-control flex h-9 w-9 items-center justify-center rounded-lg text-muted-foreground hover:text-foreground" title={dark ? '浅色' : '深色'}>
              {dark ? <Sun size={16} /> : <Moon size={16} />}
            </button>
          </div>
        </header>

        <main className="px-4 pb-10 sm:px-6">
          {error && (
            <div className="mb-5 flex items-center gap-2 rounded-xl border border-red-500/30 bg-red-500/10 px-4 py-2.5 text-sm text-red-500">
              <AlertTriangle size={15} /> 最近一次刷新失败：{error}（展示的是上次成功的数据）
            </div>
          )}
          {content}
          <footer className="mt-8 pb-4 text-center text-xs text-muted-foreground">
            Solid Glass 设计系统 · 数据来自本机 AgentDock Runtime API · 只读观测
          </footer>
        </main>
      </div>
    </div>
  );
}
