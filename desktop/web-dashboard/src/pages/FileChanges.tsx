import { useCallback, useMemo, useState } from 'react';
import { FolderTree, FolderGit2, Search, GitCompare, FileText, Columns2, ChevronDown, ChevronRight, Clock3, Copy, Check } from 'lucide-react';
import type { FileChanges as FileChangesData, FileChange, Tasks as TasksData } from '../api';
import { Panel, SectionTitle, Badge, fmt, relTime } from '../ui';
import FileTree from '../components/FileTree';
import { buildProjectGroups } from '../lib/taskProjects';
import { changeDisplayPath, groupChangesByProject, type ChangeProjectGroup } from '../lib/changeProjects';
import DiffViewer from '../components/DiffViewer';
import MarkdownView, { markdownFromDiff } from '../components/MarkdownView';
import { usePersistedState } from '../lib/usePersistedState';

function isMarkdown(path: string) {
  return /\.(md|mdx)$/i.test(path);
}

function FileDetail({ change }: { change: FileChange }) {
  const [tab, setTab] = usePersistedState<'diff' | 'render'>('file-detail-tab', 'diff');
  const [copied, setCopied] = useState(false);
  const md = useMemo(() => markdownFromDiff(change.diff || ''), [change.diff]);
  const markdown = isMarkdown(change.path);

  return (
    <div className="animate-fade-in">
      <div className="mb-3 flex flex-wrap items-start gap-3 rounded-xl border border-border/60 bg-card/40 p-3.5">
        <Badge className={change.action === 'create' ? 'bg-sky-500/10 text-sky-500' : change.action === 'delete' ? 'bg-red-500/10 text-red-500' : 'bg-violet-500/10 text-violet-500'}>{change.action}</Badge>
        <div className="min-w-0 flex-1">
          <div className="break-all font-mono text-xs" title={change.path}>{change.path}</div>
          <div className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-[11px] text-muted-foreground">
            <span className="flex items-center gap-1"><Clock3 size={11} /> {relTime(change.started_at)}</span>
            <span>{change.tool}</span>
            <span className="text-emerald-500">+{change.insertions || 0}</span>
            {change.deletions != null && <span className="text-red-500">-{change.deletions}</span>}
          </div>
        </div>
        <button onClick={() => { navigator.clipboard?.writeText(change.path); setCopied(true); setTimeout(() => setCopied(false), 1500); }} className="glass-control rounded-md p-1.5 text-muted-foreground" title="复制路径">
          {copied ? <Check size={13} /> : <Copy size={13} />}
        </button>
      </div>

      <div className="mb-2 flex flex-wrap gap-1">
        <button onClick={() => setTab('diff')} className={`glass-control flex items-center gap-1 rounded-md px-2.5 py-1 text-xs ${tab === 'diff' ? 'text-foreground' : 'text-muted-foreground'}`}>
          <GitCompare size={12} /> Diff
        </button>
        {markdown && (
          <button onClick={() => setTab('render')} className={`glass-control flex items-center gap-1 rounded-md px-2.5 py-1 text-xs ${tab === 'render' ? 'text-foreground' : 'text-muted-foreground'}`}>
            <FileText size={12} /> Markdown 渲染
          </button>
        )}
      </div>

      {tab === 'diff' ? (
        <DiffViewer diff={change.diff || ''} filePath={change.path} />
      ) : (
        <div className="scrollbar-thin max-h-[520px] overflow-auto rounded-lg border border-border/60 bg-muted/20 p-4">
          <MarkdownView content={md} />
        </div>
      )}
    </div>
  );
}

/** 项目分组：项目头可折叠，内部按文件树展示并合并同一文件的多次修改。 */
function ProjectGroupBlock({
  group,
  selectedId,
  onSelect,
}: {
  group: ChangeProjectGroup;
  selectedId: string | null;
  onSelect: (change: FileChange) => void;
}) {
  const [open, setOpen] = useState(true);
  const getPath = useCallback((c: FileChange) => changeDisplayPath(c, group), [group]);
  return (
    <div>
      <button
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-1 rounded-md py-1 pr-2 text-left text-xs text-muted-foreground hover:bg-muted/40"
        title={group.full || group.name}
      >
        {open ? <ChevronDown size={13} className="shrink-0" /> : <ChevronRight size={13} className="shrink-0" />}
        <FolderGit2 size={13} className={`shrink-0 ${group.matched ? 'text-amber-400/80' : 'text-muted-foreground/50'}`} />
        <span className={`min-w-0 truncate font-medium ${group.matched ? '' : 'italic'}`}>{group.name}</span>
        <span className="ml-auto flex shrink-0 items-center gap-1 text-[10px] tabular text-muted-foreground/60">
          <span>{group.changes.length}</span>
          {group.insertions > 0 && <span className="text-emerald-500">+{group.insertions}</span>}
          {group.deletions > 0 && <span className="text-red-500">-{group.deletions}</span>}
        </span>
      </button>
      {open && (
        group.changes.length === 0
          ? <div className="py-1 pl-7 text-[11px] text-muted-foreground">没有文件</div>
          : <div className="pl-3 pt-0.5">
              <FileTree
                changes={group.changes}
                selected={selectedId ?? undefined}
                onSelect={onSelect}
                getDisplayPath={getPath}
                className="pr-1"
              />
            </div>
      )}
    </div>
  );
}

export default function FileChanges({ data, tasks }: { data: FileChangesData; tasks: TasksData }) {
  const [q, setQ] = useState('');
  // 两种分组只是左侧选择器的视图切换；选中项、搜索、右侧详情共用一套状态。
  const [mode, setMode] = usePersistedState<'tree' | 'project'>('files-mode', 'tree');
  const [selectedId, setSelectedId] = useState<string | number | null>(null);
  const filtered = useMemo(() => data.changes.filter((c) => !q || c.path.toLowerCase().includes(q.toLowerCase())), [data.changes, q]);
  const projectGroups = useMemo(() => buildProjectGroups(tasks.tasks), [tasks.tasks]);
  const grouped = useMemo(() => groupChangesByProject(filtered, projectGroups), [filtered, projectGroups]);
  const selected = filtered.find((c) => String(c.id) === String(selectedId)) || filtered[0];
  const totalIns = data.changes.reduce((s, c) => s + (c.insertions || 0), 0);
  const totalDel = data.changes.reduce((s, c) => s + (c.deletions || 0), 0);
  const selectedKey = selected ? String(selected.id) : null;

  return (
    <Panel className="p-5">
      <SectionTitle
        icon={FolderTree}
        title="文件变更"
        hint={`${data.count} 次 · +${fmt.format(totalIns)} / -${fmt.format(totalDel)}`}
        action={
          <div className="glass-control flex items-center gap-2 rounded-lg px-3 py-1.5">
            <Search size={14} className="text-muted-foreground" />
            <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜索路径…" className="w-44 bg-transparent text-xs outline-none placeholder:text-muted-foreground" />
          </div>
        }
      />
      <div className="grid gap-4 lg:grid-cols-[300px_1fr]">
        {/* 左：目录树 / 按任务项目分组 */}
        <div className="rounded-xl border border-border/60 bg-muted/10 p-2">
          <div className="mb-2 flex items-center gap-1 px-1 pt-1">
            {([['tree', '目录树', FolderTree], ['project', '按项目', FolderGit2]] as const).map(([key, label, Icon]) => (
              <button
                key={key}
                onClick={() => setMode(key)}
                aria-pressed={mode === key}
                className={`glass-control flex items-center gap-1 rounded-md px-2 py-1 text-[11px] ${mode === key ? 'text-foreground' : 'text-muted-foreground'}`}
              >
                <Icon size={12} /> {label}
              </button>
            ))}
            <span className="ml-auto tabular text-[11px] text-muted-foreground/60">{filtered.length}</span>
          </div>
          {mode === 'tree' ? (
            <FileTree changes={filtered} selected={selectedKey ?? undefined} onSelect={(c) => setSelectedId(c.id)} />
          ) : (
            <div className="scrollbar-thin max-h-[640px] space-y-1 overflow-auto pr-1">
              {grouped.map((group) => (
                <ProjectGroupBlock key={group.key || '__unclassified__'} group={group} selectedId={selectedKey} onSelect={(c) => setSelectedId(c.id)} />
              ))}
              {grouped.length === 0 && <div className="py-12 text-center text-sm text-muted-foreground">没有文件</div>}
            </div>
          )}
        </div>
        {/* 右：详情 */}
        <div className="min-w-0">
          {selected ? (
            <FileDetail change={selected} />
          ) : (
            <div className="flex h-64 flex-col items-center justify-center gap-2 text-center">
              <Columns2 size={28} className="text-muted-foreground" />
              <div className="text-sm font-medium">选择一个文件</div>
              <div className="text-xs text-muted-foreground">左侧目录树中点开文件，查看左右对比 diff 或 Markdown 渲染</div>
            </div>
          )}
        </div>
      </div>
    </Panel>
  );
}
