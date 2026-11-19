import { useEffect, useMemo, useState } from 'react';
import {
  AlertTriangle,
  ChevronDown,
  ChevronRight,
  FolderGit2,
  Inbox,
  ListChecks,
  ListOrdered,
  Search,
  ShieldAlert,
  X,
} from 'lucide-react';
import type { Tasks as TasksData, Alerts as AlertsData, Task } from '../api';
import { Badge, Panel, fmt, relTime } from '../ui';
import { usePersistedState } from '../lib/usePersistedState';
import Modal from '../components/Modal';
import TaskDetailPage from './TaskDetailPage';
import {
  ALL_PROJECTS,
  buildProjectGroups,
  classifyStatus,
  normalizeProject,
  projectBasename,
  resolveSelectedProjectKey,
  scopeTasks,
  type StatusFilter,
  type TaskStatusKind,
} from '../lib/taskProjects';

const STATUS_UI: Record<TaskStatusKind, { badge: string; dot: string }> = {
  active: { badge: 'bg-sky-500/10 text-sky-500', dot: 'bg-sky-500' },
  blocked: { badge: 'bg-amber-500/10 text-amber-500', dot: 'bg-amber-500' },
  pending: { badge: 'bg-violet-500/10 text-violet-500', dot: 'bg-violet-500' },
  completed: { badge: 'bg-emerald-500/10 text-emerald-500', dot: 'bg-emerald-500' },
  unknown: { badge: 'bg-muted/20 text-muted-foreground', dot: 'bg-muted-foreground/60' },
};

/** 四个常驻分段；待处理与其他只在有任务（或正被筛选）时出现。 */
const BASE_SEGMENTS: { kind: StatusFilter; label: string }[] = [
  { kind: 'all', label: '全部' },
  { kind: 'active', label: '进行中' },
  { kind: 'blocked', label: '阻塞' },
  { kind: 'completed', label: '已完成' },
];

function fullTime(iso: string): string {
  const date = new Date(iso);
  return Number.isNaN(date.getTime()) ? '-' : date.toLocaleString('zh-CN');
}

function updatedLabel(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return '-';
  return `${relTime(iso)} · ${date.toLocaleDateString('zh-CN', { month: '2-digit', day: '2-digit' })}`;
}

function TaskRow({
  task,
  projectName,
  onOpen,
}: {
  task: Task;
  projectName: string | null;
  onOpen: (task: Task) => void;
}) {
  const status = classifyStatus(task.status);
  const ui = STATUS_UI[status.kind];
  const total = Math.max(task.step_count, 0);
  const done = Math.min(Math.max(task.completed_step_count, 0), total || task.completed_step_count);
  const percent = total > 0 ? Math.round((done / total) * 100) : null;
  const stepLabel = task.current_step?.title
    ? `当前：${task.current_step.title}`
    : total > 0
      ? `步骤 ${done}/${total}`
      : '未拆步骤';

  return (
    <button
      onClick={() => onOpen(task)}
      className="group w-full rounded-xl border border-border/60 bg-card/40 p-3 text-left transition-colors hover:bg-card/70 focus-visible:outline-2 focus-visible:outline-offset-2"
    >
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span className="min-w-0 truncate text-sm font-medium">{task.title}</span>
            <Badge className={ui.badge}>{status.label}</Badge>
          </div>
          <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
            {projectName && (
              <span className="inline-flex min-w-0 items-center gap-1" title={normalizeProject(task.project)}>
                <FolderGit2 size={12} className="shrink-0" />
                <span className="truncate">{projectName}</span>
              </span>
            )}
            <span className="inline-flex min-w-0 items-center gap-1">
              <ListOrdered size={12} className="shrink-0" />
              <span className="truncate">{stepLabel}</span>
            </span>
            {task.phase && <span>阶段 {task.phase}</span>}
          </div>
          {task.summary && (
            <p className="mt-2 line-clamp-2 text-xs leading-relaxed text-muted-foreground">{task.summary}</p>
          )}
        </div>
        <div className="flex shrink-0 flex-col items-end gap-1.5">
          <span className="whitespace-nowrap text-xs text-muted-foreground" title={fullTime(task.updated_at)}>
            {updatedLabel(task.updated_at)}
          </span>
          <ChevronRight size={15} className="text-muted-foreground transition-transform group-hover:translate-x-0.5" />
        </div>
      </div>
      {percent !== null && (
        <div className="mt-2 flex items-center gap-2">
          <div
            className="h-1 flex-1 overflow-hidden rounded-full bg-muted/30"
            role="progressbar"
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={percent}
            aria-label="步骤进度"
          >
            <div className="h-full rounded-full bg-sky-500/70" style={{ width: `${percent}%` }} />
          </div>
          <span className="tabular w-9 text-right text-[11px] text-muted-foreground">{percent}%</span>
        </div>
      )}
    </button>
  );
}

export default function Tasks({ data, alerts }: { data: TasksData; alerts: AlertsData }) {
  const [q, setQ] = useState('');
  const [status, setStatus] = usePersistedState<StatusFilter>('tasks-status', 'all');
  const [projectKey, setProjectKey] = usePersistedState<string>('tasks-project-key', ALL_PROJECTS);
  const [collapsed, setCollapsed] = usePersistedState<Record<string, boolean>>('tasks-collapsed', {});
  const [doneOpen, setDoneOpen] = usePersistedState<Record<string, boolean>>('tasks-done-open', {});
  const [detailTask, setDetailTask] = useState<Task | null>(null);
  const [alertsOpen, setAlertsOpen] = useState(false);

  const groups = useMemo(() => buildProjectGroups(data.tasks), [data.tasks]);
  const scope = useMemo(() => scopeTasks(groups, { projectKey, query: q, status }), [groups, projectKey, q, status]);
  const activeAlerts = alerts.alerts.filter((alert) => alert.active);
  const selectedGroup = groups.find((group) => group.key === projectKey) ?? null;
  const showProjectName = projectKey === ALL_PROJECTS;

  useEffect(() => {
    // 刷新后选择要跟随合并后的真实项目；消失或不唯一时才回到全部。
    if (projectKey === ALL_PROJECTS) return;
    const next = resolveSelectedProjectKey(groups, projectKey);
    if (next !== projectKey) setProjectKey(next);
  }, [groups, projectKey]);


  const selectProject = (key: string) => {
    setProjectKey(key);
    if (key !== ALL_PROJECTS) setCollapsed((prev) => ({ ...prev, [key]: false }));
  };

  const clearFilters = () => {
    setQ('');
    setStatus('all');
    setProjectKey(ALL_PROJECTS);
  };

  const segments = [
    BASE_SEGMENTS[0],
    BASE_SEGMENTS[1],
    BASE_SEGMENTS[2],
    ...(scope.statusCounts.pending > 0 || status === 'pending'
      ? [{ kind: 'pending' as const, label: '待处理' }]
      : []),
    BASE_SEGMENTS[3],
    ...(scope.statusCounts.unknown > 0 || status === 'unknown'
      ? [{ kind: 'unknown' as const, label: '其他' }]
      : []),
  ];
  const countFor = (kind: StatusFilter) => (kind === 'all' ? scope.statusCounts.total : scope.statusCounts[kind]);
  const visibleLabel = scope.visible.length === scope.searched.length
    ? `${fmt.format(scope.searched.length)} 条`
    : `${fmt.format(scope.visible.length)} / ${fmt.format(scope.searched.length)} 条`;

  return (
    <>
    <div className="task-workspace animate-float-in space-y-3">
      {/* 紧凑分段统计：点击即状态筛选，计数与右侧列表同源 */}
      <Panel className="p-1.5">
        <div className="flex items-stretch gap-1 overflow-x-auto scrollbar-thin">
          {segments.map((segment) => {
            const selected = status === segment.kind;
            return (
              <button
                key={segment.kind}
                onClick={() => setStatus(segment.kind)}
                aria-pressed={selected}
                className={`u-press flex min-w-[86px] flex-1 flex-col items-start rounded-xl px-3 py-2 text-left transition-colors ${
                  selected ? 'glass-chosen text-foreground' : 'text-muted-foreground hover:bg-card/50 hover:text-foreground'
                }`}
              >
                <span className="text-[11px]">{segment.label}</span>
                <span className="tabular mt-0.5 text-lg font-semibold leading-none">{fmt.format(countFor(segment.kind))}</span>
              </button>
            );
          })}
          <div className="ml-auto hidden shrink-0 flex-col justify-center px-3 text-right sm:flex">
            <span className="text-xs text-muted-foreground">全量 {fmt.format(data.tasks.length)} 条</span>
            <span className="text-[11px] text-muted-foreground">计数基于全部任务</span>
          </div>
        </div>
        <div className="px-3 pb-1 pt-2 text-[11px] text-muted-foreground sm:hidden">
          已加载全部 {fmt.format(data.tasks.length)} 条任务；计数基于全部任务。
        </div>
      </Panel>

      {/* 全局告警是辅助区：默认折叠，计数常驻标题，展开后才占空间 */}
      <Panel className="px-3 py-2">
        <button
          onClick={() => setAlertsOpen((value) => !value)}
          aria-expanded={alertsOpen}
          className="u-press flex w-full items-center gap-2 text-left"
        >
          <ShieldAlert size={16} className={activeAlerts.length > 0 ? 'text-amber-500' : 'text-muted-foreground'} />
          <span className="text-sm font-semibold">全局告警</span>
          <Badge className={activeAlerts.length > 0 ? 'bg-amber-500/10 text-amber-500' : 'bg-muted/20 text-muted-foreground'}>
            {activeAlerts.length} 条活跃
          </Badge>
          <ChevronDown
            size={15}
            className={`ml-auto shrink-0 text-muted-foreground transition-transform ${alertsOpen ? '' : '-rotate-90'}`}
          />
        </button>
        <p className="mt-1 pl-6 text-[11px] text-muted-foreground">全局告警流 · 与项目筛选无归属关联</p>
        {alertsOpen && (
          activeAlerts.length > 0 ? (
            <div className="scrollbar-thin mt-3 grid max-h-64 gap-2 overflow-y-auto md:grid-cols-2">
              {activeAlerts.map((alert) => (
                <div key={alert.id} className="rounded-xl border border-amber-500/25 bg-amber-500/5 p-3">
                  <div className="text-xs font-medium">{alert.message}</div>
                  {alert.detail && <div className="mt-1 text-xs leading-relaxed text-muted-foreground">{alert.detail}</div>}
                </div>
              ))}
            </div>
          ) : (
            <p className="mt-3 text-xs text-muted-foreground">当前没有活跃告警。</p>
          )
        )}
      </Panel>

      <div className="grid gap-4 lg:grid-cols-[220px_minmax(0,1fr)] xl:grid-cols-[240px_minmax(0,1fr)]">
        {/* 桌面项目导航；移动端改用 select */}
        <aside className="hidden lg:block">
          <div className="task-project-nav">
            <Panel className="p-2">
              <div className="flex items-center justify-between px-2 py-1.5">
                <span className="text-[11px] font-semibold uppercase tracking-widest text-muted-foreground/70">项目</span>
                <span className="tabular text-[11px] text-muted-foreground">{groups.length}</span>
              </div>
              <button
                onClick={() => selectProject(ALL_PROJECTS)}
                aria-pressed={projectKey === ALL_PROJECTS}
                className={`u-press flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-xs ${
                  projectKey === ALL_PROJECTS ? 'glass-chosen text-foreground' : 'text-muted-foreground hover:bg-card/50 hover:text-foreground'
                }`}
              >
                <Inbox size={14} className="shrink-0" />
                <span className="min-w-0 flex-1 truncate">全部项目</span>
                <span className="tabular text-[11px] text-muted-foreground">{fmt.format(data.tasks.length)}</span>
              </button>
              <div className="scrollbar-thin mt-1 max-h-[calc(100dvh-15rem)] space-y-0.5 overflow-y-auto">
                {groups.map((group) => {
                  const basename = projectBasename(group.full);
                  // 重名项目只在第二行补最短父路径后缀，主行始终是 basename。
                  const parentSuffix = group.name.endsWith(`/${basename}`)
                    ? group.name.slice(0, -(basename.length + 1))
                    : '';
                  return (
                    <button
                      key={group.key || 'unclassified'}
                      onClick={() => selectProject(group.key)}
                      aria-pressed={projectKey === group.key}
                      title={group.full || '未分类'}
                      className={`u-press flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-xs ${
                        projectKey === group.key ? 'glass-chosen text-foreground' : 'text-muted-foreground hover:bg-card/50 hover:text-foreground'
                      }`}
                    >
                      <FolderGit2 size={14} className="shrink-0" />
                      <span className="min-w-0 flex-1">
                        <span className="block truncate font-medium">{basename}</span>
                        {parentSuffix && (
                          <span className="mt-0.5 block truncate text-[11px] text-muted-foreground">{parentSuffix}</span>
                        )}
                      </span>
                      {(group.counts.active > 0 || group.counts.blocked > 0) && (
                        <span
                          className={`h-1.5 w-1.5 shrink-0 rounded-full ${group.counts.active > 0 ? STATUS_UI.active.dot : STATUS_UI.blocked.dot}`}
                          title={[group.counts.active > 0 ? `进行中 ${group.counts.active}` : '', group.counts.blocked > 0 ? `阻塞 ${group.counts.blocked}` : ''].filter(Boolean).join(' · ')}
                        />
                      )}
                      <span className="tabular w-5 shrink-0 text-right text-[11px] text-muted-foreground">{group.counts.total}</span>
                    </button>
                  );
                })}
              </div>
            </Panel>
          </div>
        </aside>

        <div className="min-w-0 space-y-3">
          <label className="block lg:hidden">
            <span className="sr-only">选择项目</span>
            <select
              value={projectKey}
              onChange={(event) => selectProject(event.target.value)}
              className="glass-control w-full rounded-lg bg-transparent px-3 py-2 text-sm"
            >
              <option value={ALL_PROJECTS}>全部项目（{data.tasks.length}）</option>
              {groups.map((group) => (
                <option key={group.key || 'unclassified'} value={group.key}>
                  {group.name}（{group.counts.total}）
                </option>
              ))}
            </select>
          </label>

          <div className="flex flex-wrap items-center gap-2">
            <ListChecks size={15} className="text-muted-foreground" />
            <h2 className="min-w-0 break-words text-sm font-semibold">
              {selectedGroup ? <><span className="lg:hidden">任务</span><span className="hidden lg:inline">{selectedGroup.name}</span></> : '任务'}
            </h2>
            <span className="text-xs text-muted-foreground">{visibleLabel}</span>
            <div className="glass-control ml-auto flex w-full min-w-0 items-center gap-2 rounded-lg px-3 py-1.5 sm:w-72">
              <Search size={14} className="shrink-0 text-muted-foreground" />
              <input
                value={q}
                onChange={(event) => setQ(event.target.value)}
                placeholder="搜索标题、摘要、目标或项目"
                aria-label="搜索任务"
                className="min-w-0 flex-1 bg-transparent text-xs outline-none placeholder:text-muted-foreground"
              />
              {q && (
                <button onClick={() => setQ('')} aria-label="清空搜索" className="u-press shrink-0 text-muted-foreground hover:text-foreground">
                  <X size={13} />
                </button>
              )}
            </div>
          </div>

          <div className="space-y-3">
            {scope.groups.map(({ group, tasks }) => {
              const isCollapsed = showProjectName && collapsed[group.key] === true;
              const openTasks = tasks.filter((task) => classifyStatus(task.status).kind !== 'completed');
              const doneTasks = tasks.filter((task) => classifyStatus(task.status).kind === 'completed');
              const isDoneOpen = doneOpen[group.key] ?? (status === 'completed');
              return (
                <Panel key={group.key || 'unclassified'} className="overflow-hidden">
                  {showProjectName && <button
                    onClick={() => setCollapsed((prev) => ({ ...prev, [group.key]: !isCollapsed }))}
                    aria-expanded={!isCollapsed}
                    className="u-press flex w-full items-center gap-2 px-4 py-3 text-left"
                  >
                    <ChevronDown
                      size={15}
                      className={`shrink-0 text-muted-foreground transition-transform ${isCollapsed ? '-rotate-90' : ''}`}
                    />
                    <span className="text-sm font-medium" title={group.full || '未分类'}>{group.name}</span>
                    {group.unclassified && (
                      <span className="rounded bg-muted/20 px-1.5 py-0.5 text-[10px] text-muted-foreground">未填 project</span>
                    )}
                    <span className="ml-auto shrink-0 text-xs text-muted-foreground">
                      {tasks.length === group.counts.total ? `${tasks.length} 条` : `${tasks.length} / ${group.counts.total} 条`}
                    </span>
                  </button>}
                  {!isCollapsed && (
                    <div className={`space-y-2 p-2.5 ${showProjectName ? 'border-t border-border/40' : ''}`}>
                      {openTasks.map((task) => (
                        <TaskRow
                          key={task.id}
                          task={task}
                          projectName={showProjectName ? projectBasename(group.full) : null}
                          onOpen={setDetailTask}
                        />
                      ))}
                      {doneTasks.length > 0 && (
                        <div className={openTasks.length > 0 ? 'pt-1' : ''}>
                          <button
                            onClick={() => setDoneOpen((prev) => ({ ...prev, [group.key]: !isDoneOpen }))}
                            aria-expanded={isDoneOpen}
                            className="u-press flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-xs text-muted-foreground hover:bg-card/50 hover:text-foreground"
                          >
                            <ChevronDown
                              size={14}
                              className={`shrink-0 transition-transform ${isDoneOpen ? '' : '-rotate-90'}`}
                            />
                            <span>已完成 {doneTasks.length}</span>
                            <span className="ml-auto text-[11px]">{isDoneOpen ? '收起' : '展开'}</span>
                          </button>
                          {isDoneOpen && (
                            <div className="mt-2 space-y-2">
                              {doneTasks.map((task) => (
                                <TaskRow
                                  key={task.id}
                                  task={task}
                                  projectName={showProjectName ? projectBasename(group.full) : null}
                                  onOpen={setDetailTask}
                                />
                              ))}
                            </div>
                          )}
                        </div>
                      )}
                    </div>
                  )}
                </Panel>
              );
            })}
            {scope.groups.length === 0 && (
              <Panel className="p-10 text-center">
                <AlertTriangle size={24} className="mx-auto text-muted-foreground" />
                <p className="mt-3 text-sm font-medium">没有匹配的任务</p>
                <p className="mt-1 text-xs text-muted-foreground">当前项目、搜索与状态条件下没有结果。</p>
                <button
                  onClick={clearFilters}
                  className="u-press glass-control mt-4 rounded-lg px-3 py-1.5 text-xs text-foreground"
                >
                  清除搜索与筛选
                </button>
              </Panel>
            )}
          </div>
        </div>
      </div>
    </div>

    <Modal
      open={!!detailTask}
      onClose={() => setDetailTask(null)}
      title={detailTask ? `任务详情 · ${detailTask.title}` : '任务详情'}
      wide
    >
      {detailTask && (
        <TaskDetailPage id={detailTask.id} fallback={detailTask} onClose={() => setDetailTask(null)} />
      )}
    </Modal>
    </>
  );
}
