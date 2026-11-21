import { useEffect, useState } from 'react';
import {
  Target, ListOrdered, GitBranch, Radio, AlertTriangle, FileText,
  CheckCircle2, Circle, XCircle, Clock3, Activity, FolderTree, RefreshCw, Hash, Copy, Check,
} from 'lucide-react';
import { api, type Task, type TaskDetail, type TaskActivity } from '../api';
import { Panel, Badge, ms, relTime, shortPath } from '../ui';
import DiffViewer from '../components/DiffViewer';

const statusMeta: Record<string, { label: string; cls: string }> = {
  completed: { label: '已完成', cls: 'bg-emerald-500/10 text-emerald-500' },
  active: { label: '进行中', cls: 'bg-sky-500/10 text-sky-500' },
  blocked: { label: '阻塞', cls: 'bg-amber-500/10 text-amber-500' },
  in_progress: { label: '进行中', cls: 'bg-sky-500/10 text-sky-500' },
  pending: { label: '待处理', cls: 'bg-muted/20 text-muted-foreground' },
};
function statusOf(s: string) { return statusMeta[s] || { label: s, cls: 'bg-muted/20 text-muted-foreground' }; }

function fullTime(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso || '-';
  return date.toLocaleString('zh-CN');
}

/** 自包含 Markdown 交接简报：粘贴给任意 Agent 即可领取并继续任务，格式与 macOS 原生端一致。 */
function buildHandoffText(t: TaskDetail['task']): string {
  const lines: string[] = [];
  lines.push(`# 任务交接: ${t.title}`);
  lines.push('');
  lines.push(`- 任务 ID: ${t.id}`);
  if (t.project) lines.push(`- 项目: ${t.project}`);
  lines.push(`- 状态: ${t.status} / ${t.phase}`);
  lines.push(`- 更新于: ${fullTime(t.updated_at)}`);

  if (t.goal) {
    lines.push('', '## 目标', t.goal);
  }
  if (t.conditions.length > 0) {
    lines.push('', '## 完成条件');
    for (const c of t.conditions) lines.push(`- ${c.text}`);
  }
  if (t.steps.length > 0) {
    lines.push('', '## 步骤');
    for (const s of t.steps) {
      const mark = s.status === 'completed' ? '[x]' : s.status === 'in_progress' || s.status === 'active' ? '[>]' : '[ ]';
      lines.push(`- ${mark} ${s.title} (${s.status})`);
    }
  }
  if (t.blocker) {
    lines.push('', '## 阻塞', t.blocker);
  }
  if (t.summary) {
    lines.push('', '## 摘要', t.summary);
  }
  if (t.events.length > 0) {
    lines.push('', '## 最近事件');
    for (const e of t.events.slice(-10)) lines.push(`- ${fullTime(e.created_at)} ${e.summary}`);
  }
  lines.push('', '## 如何继续');
  lines.push('领取本任务后用 task_manage 继续推进：调用 get 加载完整状态，checkpoint 更新步骤进度，block 记录阻塞，resume 在阻塞解除后继续，final_review 记录验收评审，complete 关闭任务。');
  return lines.join('\n');
}

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

export default function TaskDetailPage({ id, fallback, onClose }: { id: string; fallback?: Task; onClose: () => void }) {
  const [detail, setDetail] = useState<TaskDetail | null>(() => fallback ? fallbackDetail(fallback) : null);
  const [activity, setActivity] = useState<TaskActivity | null>(null);
  const [loading, setLoading] = useState(true);
  const [partialError, setPartialError] = useState<string | null>(null);
  const [expandedDiff, setExpandedDiff] = useState<number | null>(null);
  const [copied, setCopied] = useState(false);

  const copyHandoff = async () => {
    try {
      await navigator.clipboard.writeText(buildHandoffText(detail!.task));
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch { /* 剪贴板被拒绝时静默 */ }
  };

  const load = () => {
    setLoading(true);
    // 展开状态用数组索引：重新拉取活动后列表可能变化，先折叠避免指向别的条目。
    setExpandedDiff(null);
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
      <div className="p-8 text-center">
        <AlertTriangle size={28} className="mx-auto text-red-500" />
        <div className="mt-3 text-sm">加载失败：{partialError}</div>
        <button onClick={onClose} className="glass-control mt-4 rounded-lg px-4 py-2 text-sm">关闭</button>
      </div>
    );
  }

  const t = detail.task;
  const sm = statusOf(t.status);
  const doneSteps = t.steps.filter((s) => s.status === 'completed').length;
  const callCount = activity?.call_count ?? 0;
  const changeCount = activity?.change_count ?? 0;

  return (
    <div className="space-y-3">
      {partialError && (
        <div className="flex items-center gap-2 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-500">
          <AlertTriangle size={13} /> {partialError} · <button onClick={load} className="underline">重试</button>
        </div>
      )}

      {/* 标题 + 全部徽标一行，元信息压成一行，整体收紧 */}
      <div>
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="font-mono text-[11px] text-muted-foreground">{t.id}</span>
          {t.project && <Badge className="bg-violet-500/10 text-violet-500">{t.project}</Badge>}
          <Badge className={sm.cls}>{sm.label}</Badge>
          {t.phase && <Badge className="bg-sky-500/10 text-sky-500">{t.phase}</Badge>}
          {t.review_status && <Badge className="bg-muted/20 text-muted-foreground">评审 {t.review_status}</Badge>}
          <button
            onClick={copyHandoff}
            title="复制任务交接简报，粘贴给 Agent 即可领取并继续任务"
            className="u-press glass-control ml-auto flex items-center gap-1.5 rounded-lg px-2.5 py-1 text-xs text-muted-foreground hover:text-foreground"
          >
            {copied ? <Check size={12} className="text-emerald-500" /> : <Copy size={12} />}
            {copied ? '已复制交接简报' : '复制任务交接'}
          </button>
        </div>
        <h2 className="mt-1.5 text-base font-semibold leading-snug">{t.title}</h2>
        <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-muted-foreground">
          <span>创建 {new Date(t.created_at).toLocaleString('zh-CN')}</span>
          <span>更新 {relTime(t.updated_at)}</span>
          {t.completed_at && <span>完成 {new Date(t.completed_at).toLocaleString('zh-CN')}</span>}
          {t.steps.length > 0 && <span>步骤 {doneSteps}/{t.steps.length}</span>}
        </div>
      </div>

      {t.blocker && (
        <div className="flex items-start gap-2 rounded-xl border border-amber-500/30 bg-amber-500/10 px-3 py-2.5 text-xs text-amber-500">
          <AlertTriangle size={15} className="mt-0.5 shrink-0" />
          <div><span className="font-medium">阻塞原因：</span><span className="text-amber-500/90">{t.blocker}</span></div>
        </div>
      )}

      {/* 单列按重要性排序：步骤最前并突出，目标/摘要/条件/时间线依次 */}

      {t.steps.length > 0 && (
        <Panel className="p-3.5">
          <Section icon={ListOrdered} title="步骤" hint={`${doneSteps}/${t.steps.length}`}>
            <div className="mb-2.5 h-1.5 overflow-hidden rounded-full bg-muted/60">
              <div className="h-full rounded-full bg-sky-500 transition-all" style={{ width: `${(doneSteps / t.steps.length) * 100}%` }} />
            </div>
            <div className="space-y-1.5">
              {t.steps.map((s) => {
                const ss = statusOf(s.status);
                return (
                  <div key={s.id} className="flex items-start gap-2.5 rounded-lg border border-border/50 bg-card/40 px-2.5 py-2">
                    {s.status === 'completed' ? <CheckCircle2 size={14} className="mt-0.5 shrink-0 text-emerald-500" /> : <Circle size={14} className="mt-0.5 shrink-0 text-sky-500" />}
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2"><span className="font-mono text-[10px] text-muted-foreground">{s.id}</span><Badge className={ss.cls}>{ss.label}</Badge></div>
                      <div className="mt-0.5 text-xs leading-relaxed">{s.title}</div>
                    </div>
                  </div>
                );
              })}
            </div>
          </Section>
        </Panel>
      )}

      {t.goal && (
        <Panel className="p-3.5">
          <Section icon={Target} title="目标"><p className="rounded-lg bg-muted/30 p-2.5 text-xs leading-relaxed text-muted-foreground">{t.goal}</p></Section>
        </Panel>
      )}

      {t.summary && (
        <Panel className="p-3.5">
          <Section icon={FileText} title="摘要"><p className="text-xs leading-relaxed text-muted-foreground">{t.summary}</p></Section>
        </Panel>
      )}

      {t.conditions.length > 0 && (
        <Panel className="p-3.5">
          <Section icon={GitBranch} title="完成条件" hint={`${t.conditions.length} 项`}>
            <div className="space-y-1.5">
              {t.conditions.map((c) => (
                <div key={c.id} className="flex items-start gap-2 rounded-lg border border-border/50 bg-card/40 px-2.5 py-2 text-xs">
                  <Hash size={12} className="mt-0.5 shrink-0 font-mono text-muted-foreground" />
                  <div className="min-w-0 flex-1"><span className="mr-1.5 font-mono text-muted-foreground">{c.id}</span>{c.text}</div>
                </div>
              ))}
            </div>
          </Section>
        </Panel>
      )}

      {t.events.length > 0 && (
        <Panel className="p-3.5">
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

      {/* 关联区块：只有真的有数据时才显示，避免常年挂两个「0 / 无」的空壳 */}
      {callCount > 0 && (
        <Panel className="p-3.5">
          <Section icon={Activity} title="关联调用" hint={`${callCount} 次`}>
            <div className="scrollbar-thin max-h-56 space-y-1.5 overflow-y-auto pr-1">
              {activity?.calls.map((c) => (
                <div key={c.id} className="flex items-center gap-2 rounded-lg border border-border/50 bg-card/40 p-2 text-xs">
                  {c.success ? <CheckCircle2 size={13} className="shrink-0 text-emerald-500" /> : <XCircle size={13} className="shrink-0 text-red-500" />}
                  <span className="truncate font-mono">{c.tool}</span>
                  <span className="ml-auto flex items-center gap-1 text-[10px] text-muted-foreground"><Clock3 size={10} /> {ms(c.duration_ms)}</span>
                </div>
              ))}
            </div>
          </Section>
        </Panel>
      )}

      {changeCount > 0 && (
        <Panel className="p-3.5">
          <Section icon={FolderTree} title="关联文件变更" hint={`${changeCount} 处`}>
            <div className="scrollbar-thin max-h-72 space-y-1.5 overflow-y-auto pr-1">
              {/* 活动投影没有稳定的逐文件 id（trace_id 只到调用级），用数组索引作为展开状态与 key。 */}
              {activity?.file_changes.map((c, index) => (
                <div key={index} className="rounded-lg border border-border/50 bg-card/40">
                  <button onClick={() => setExpandedDiff(expandedDiff === index ? null : index)} className="flex w-full items-center gap-2 p-2 text-left text-xs">
                    <Badge className={c.action === 'create' ? 'bg-sky-500/10 text-sky-500' : 'bg-violet-500/10 text-violet-500'}>{c.action}</Badge>
                    <span className="min-w-0 flex-1 truncate font-mono" title={c.path}>{shortPath(c.path)}</span>
                    <span className="text-[10px] text-emerald-500">+{c.insertions || 0}</span>
                  </button>
                  {expandedDiff === index && (
                    <div className="border-t border-border/50 p-2">
                      <DiffViewer
                        diff={c.diff || ''}
                        filePath={c.path}
                        moveTo={c.move_to}
                        insertions={c.insertions}
                        deletions={c.deletions}
                        truncated={c.truncated}
                      />
                    </div>
                  )}
                </div>
              ))}
            </div>
          </Section>
        </Panel>
      )}
    </div>
  );
}
