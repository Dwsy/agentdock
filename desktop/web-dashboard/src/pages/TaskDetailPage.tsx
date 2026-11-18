import { useEffect, useState } from 'react';
import {
  ArrowLeft, Target, ListOrdered, GitBranch, Radio, AlertTriangle, FileText,
  CheckCircle2, Circle, XCircle, Clock3, Activity, FolderTree, RefreshCw, Hash,
} from 'lucide-react';
import { api, type Task, type TaskDetail, type TaskActivity } from '../api';
import { Panel, SectionTitle, Badge, ms, relTime, shortPath } from '../ui';
import DiffViewer from '../components/DiffViewer';

const statusMeta: Record<string, { label: string; cls: string }> = {
  completed: { label: '已完成', cls: 'bg-emerald-500/10 text-emerald-500' },
  active: { label: '进行中', cls: 'bg-sky-500/10 text-sky-500' },
  blocked: { label: '阻塞', cls: 'bg-amber-500/10 text-amber-500' },
  in_progress: { label: '进行中', cls: 'bg-sky-500/10 text-sky-500' },
  pending: { label: '待处理', cls: 'bg-muted/20 text-muted-foreground' },
};
function statusOf(s: string) { return statusMeta[s] || { label: s, cls: 'bg-muted/20 text-muted-foreground' }; }

function Section({ icon: Icon, title, hint, children }: { icon: any; title: string; hint?: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="mb-2 flex items-center gap-1.5 text-[11px] font-semibold text-muted-foreground">
        <Icon size={13} /> {title} {hint && <span className="ml-1 tabular font-normal text-muted-foreground/60">{hint}</span>}
      </div>
      {children}
    </div>
  );
}

/** 用列表项构造一个最小详情兜底，避免详情接口超时时整页不可用。 */
function fallbackDetail(task: Task): TaskDetail {
  return {
    ok: false, source: 'list-fallback', action: 'get',
    task: {
      id: task.id, title: task.title, goal: task.goal, project: task.project,
      status: task.status, phase: task.phase, review_status: task.review_status,
      conditions: [], steps: [], events: [], summary: task.summary ?? '',
      created_at: task.created_at, updated_at: task.updated_at, completed_at: task.completed_at,
    },
  };
}

export default function TaskDetailPage({ id, fallback, onBack }: { id: string; fallback?: Task; onBack: () => void }) {
  const [detail, setDetail] = useState<TaskDetail | null>(() => fallback ? fallbackDetail(fallback) : null);
  const [activity, setActivity] = useState<TaskActivity | null>(null);
  const [loading, setLoading] = useState(true);
  const [partialError, setPartialError] = useState<string | null>(null);
  const [expandedDiff, setExpandedDiff] = useState<number | null>(null);

  const load = () => {
    setLoading(true);
    Promise.allSettled([api.taskDetail(id), api.taskActivity(id)]).then(([d, a]) => {
      if (d.status === 'fulfilled') setDetail(d.value);
      else if (!fallback) setPartialError(`详情：${d.reason?.message || d.reason}`);
      if (a.status === 'fulfilled') setActivity(a.value);
      const fails = [d, a].filter((r) => r.status === 'rejected');
      if (fails.length && detail) setPartialError(`${fails.length} 个详情接口暂时无响应，显示可用数据`);
      setLoading(false);
    });
  };
  useEffect(() => { load(); /* eslint-disable-next-line */ }, [id]);

  if (loading && !detail) {
    return <div className="flex items-center justify-center py-24 text-muted-foreground"><RefreshCw size={18} className="mr-3 animate-spin" /> 加载任务详情…</div>;
  }
  if (!detail) {
    return (
      <Panel className="p-8 text-center">
        <AlertTriangle size={28} className="mx-auto text-red-500" />
        <div className="mt-3 text-sm">加载失败：{partialError}</div>
        <button onClick={onBack} className="glass-control mt-4 rounded-lg px-4 py-2 text-sm">返回</button>
      </Panel>
    );
  }

  const t = detail.task;
  const sm = statusOf(t.status);
  const doneSteps = t.steps.filter((s) => s.status === 'completed').length;

  return (
    <div className="space-y-5">
      <button onClick={onBack} className="glass-control flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs text-muted-foreground hover:text-foreground">
        <ArrowLeft size={14} /> 返回任务列表
      </button>
      {partialError && (
        <div className="flex items-center gap-2 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-500">
          <AlertTriangle size={13} /> {partialError} · <button onClick={load} className="underline">重试</button>
        </div>
      )}

      {/* 标题区 */}
      <Panel className="p-6">
        <div className="flex flex-wrap items-start gap-3">
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-mono text-xs text-muted-foreground">{t.id}</span>
              {t.project && <Badge className="bg-violet-500/10 text-violet-500">{t.project}</Badge>}
            </div>
            <h2 className="mt-2 text-xl font-semibold">{t.title}</h2>
          </div>
          <div className="flex items-center gap-2">
            <Badge className={sm.cls}>{sm.label}</Badge>
            {t.phase && <Badge className="bg-sky-500/10 text-sky-500">{t.phase}</Badge>}
            {t.review_status && <Badge className="bg-muted/20 text-muted-foreground">评审 {t.review_status}</Badge>}
          </div>
        </div>

        <div className="mt-4 grid grid-cols-2 gap-3 border-t border-border/60 pt-4 text-xs md:grid-cols-4">
          <div><div className="text-muted-foreground">创建</div><div className="tabular mt-1">{new Date(t.created_at).toLocaleString('zh-CN')}</div></div>
          <div><div className="text-muted-foreground">更新</div><div className="tabular mt-1">{relTime(t.updated_at)}</div></div>
          {t.completed_at && <div><div className="text-muted-foreground">完成</div><div className="tabular mt-1">{new Date(t.completed_at).toLocaleString('zh-CN')}</div></div>}
          <div><div className="text-muted-foreground">步骤进度</div><div className="tabular mt-1">{doneSteps}/{t.steps.length}</div></div>
        </div>
      </Panel>

      {t.blocker && (
        <div className="flex items-start gap-2 rounded-xl border border-amber-500/30 bg-amber-500/10 p-4 text-sm text-amber-500">
          <AlertTriangle size={16} className="mt-0.5 shrink-0" />
          <div><div className="font-medium">阻塞原因</div><div className="mt-1 text-amber-500/90">{t.blocker}</div></div>
        </div>
      )}

      <div className="grid gap-5 lg:grid-cols-2">
        <div className="space-y-5">
          {t.goal && (
            <Panel className="p-5">
              <Section icon={Target} title="目标"><p className="rounded-lg bg-muted/30 p-3 text-sm leading-relaxed text-muted-foreground">{t.goal}</p></Section>
            </Panel>
          )}
          {t.summary && (
            <Panel className="p-5">
              <Section icon={FileText} title="摘要"><p className="text-sm leading-relaxed text-muted-foreground">{t.summary}</p></Section>
            </Panel>
          )}
          {t.conditions.length > 0 && (
            <Panel className="p-5">
              <Section icon={GitBranch} title="完成条件" hint={`${t.conditions.length} 项`}>
                <div className="space-y-2">
                  {t.conditions.map((c) => (
                    <div key={c.id} className="flex items-start gap-2 rounded-lg border border-border/50 bg-card/40 p-2.5 text-xs">
                      <Hash size={12} className="mt-0.5 shrink-0 font-mono text-muted-foreground" />
                      <div className="min-w-0 flex-1"><span className="mr-1.5 font-mono text-muted-foreground">{c.id}</span>{c.text}</div>
                    </div>
                  ))}
                </div>
              </Section>
            </Panel>
          )}
          {t.steps.length > 0 && (
            <Panel className="p-5">
              <Section icon={ListOrdered} title="步骤" hint={`${doneSteps}/${t.steps.length}`}>
                <div className="space-y-2">
                  {t.steps.map((s) => {
                    const ss = statusOf(s.status);
                    return (
                      <div key={s.id} className="flex items-start gap-2.5 rounded-lg border border-border/50 bg-card/40 p-2.5">
                        {s.status === 'completed' ? <CheckCircle2 size={15} className="mt-0.5 shrink-0 text-emerald-500" /> : <Circle size={15} className="mt-0.5 shrink-0 text-sky-500" />}
                        <div className="min-w-0 flex-1">
                          <div className="flex items-center gap-2"><span className="font-mono text-[11px] text-muted-foreground">{s.id}</span><Badge className={ss.cls}>{ss.label}</Badge></div>
                          <div className="mt-1 text-xs leading-relaxed">{s.title}</div>
                        </div>
                      </div>
                    );
                  })}
                </div>
              </Section>
            </Panel>
          )}
        </div>

        <div className="space-y-5">
          {t.events.length > 0 && (
            <Panel className="p-5">
              <Section icon={Radio} title="事件时间线" hint={`${t.events.length} 条`}>
                <div className="relative space-y-3 border-l border-border/60 pl-4">
                  {[...t.events].reverse().map((e, i) => (
                    <div key={i} className="relative">
                      <span className="absolute -left-[21px] top-1 h-2 w-2 rounded-full bg-primary ring-4 ring-background" />
                      <div className="flex items-center gap-2"><Badge className="bg-muted/20 text-muted-foreground">{e.type}</Badge><span className="text-[10px] text-muted-foreground">{new Date(e.created_at).toLocaleString('zh-CN')}</span></div>
                      <p className="mt-1 text-xs leading-relaxed text-muted-foreground">{e.summary}</p>
                    </div>
                  ))}
                </div>
              </Section>
            </Panel>
          )}

          <Panel className="p-5">
            <Section icon={Activity} title="关联调用" hint={`${activity?.call_count ?? '…'} 次`}>
              <div className="scrollbar-thin max-h-64 space-y-1.5 overflow-y-auto pr-1">
                {activity?.calls.map((c) => (
                  <div key={c.id} className="flex items-center gap-2 rounded-lg border border-border/50 bg-card/40 p-2 text-xs">
                    {c.success ? <CheckCircle2 size={13} className="shrink-0 text-emerald-500" /> : <XCircle size={13} className="shrink-0 text-red-500" />}
                    <span className="truncate font-mono">{c.tool}</span>
                    <span className="ml-auto flex items-center gap-1 text-[10px] text-muted-foreground"><Clock3 size={10} /> {ms(c.duration_ms)}</span>
                  </div>
                ))}
                {(activity?.call_count ?? 0) === 0 && <div className="py-6 text-center text-xs text-muted-foreground">无关联调用</div>}
                {!activity && <div className="py-6 text-center text-xs text-muted-foreground">加载中…</div>}
              </div>
            </Section>
          </Panel>

          <Panel className="p-5">
            <Section icon={FolderTree} title="关联文件变更" hint={`${activity?.change_count ?? '…'} 处`}>
              <div className="scrollbar-thin max-h-96 space-y-1.5 overflow-y-auto pr-1">
                {activity?.file_changes.map((c) => (
                  <div key={c.id} className="rounded-lg border border-border/50 bg-card/40">
                    <button onClick={() => setExpandedDiff(expandedDiff === c.id ? null : c.id)} className="flex w-full items-center gap-2 p-2 text-left text-xs">
                      <Badge className={c.action === 'create' ? 'bg-sky-500/10 text-sky-500' : 'bg-violet-500/10 text-violet-500'}>{c.action}</Badge>
                      <span className="min-w-0 flex-1 truncate font-mono" title={c.path}>{shortPath(c.path)}</span>
                      <span className="text-[10px] text-emerald-500">+{c.insertions || 0}</span>
                    </button>
                    {expandedDiff === c.id && <div className="border-t border-border/50 p-2"><DiffViewer diff={c.diff || ''} filePath={c.path} /></div>}
                  </div>
                ))}
                {(activity?.change_count ?? 0) === 0 && <div className="py-6 text-center text-xs text-muted-foreground">无关联文件变更</div>}
                {!activity && <div className="py-6 text-center text-xs text-muted-foreground">加载中…</div>}
              </div>
            </Section>
          </Panel>
        </div>
      </div>
    </div>
  );
}
