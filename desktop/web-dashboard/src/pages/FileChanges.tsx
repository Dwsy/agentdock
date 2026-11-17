import { useMemo, useState } from 'react';
import { FolderTree, Search, GitCompare, FileText, Columns2, ChevronDown, Clock3, Copy, Check } from 'lucide-react';
import type { FileChanges as FileChangesData, FileChange } from '../api';
import { Panel, SectionTitle, Badge, fmt, relTime, shortPath } from '../ui';
import FileTree from '../components/FileTree';
import DiffViewer from '../components/DiffViewer';
import MarkdownView, { markdownFromDiff } from '../components/MarkdownView';

function isMarkdown(path: string) {
  return /\.(md|mdx)$/i.test(path);
}

function FileDetail({ change }: { change: FileChange }) {
  const [tab, setTab] = useState<'diff' | 'render'>('diff');
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

export default function FileChanges({ data }: { data: FileChangesData }) {
  const [q, setQ] = useState('');
  const [selectedId, setSelectedId] = useState<string | number | null>(null);
  const filtered = useMemo(() => data.changes.filter((c) => !q || c.path.toLowerCase().includes(q.toLowerCase())), [data.changes, q]);
  const selected = filtered.find((c) => String(c.id) === String(selectedId)) || filtered[0];
  const totalIns = data.changes.reduce((s, c) => s + (c.insertions || 0), 0);
  const totalDel = data.changes.reduce((s, c) => s + (c.deletions || 0), 0);

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
        {/* 左：文件树 */}
        <div className="rounded-xl border border-border/60 bg-muted/10 p-2">
          <div className="mb-2 flex items-center gap-1.5 px-2 pt-1 text-[11px] font-medium text-muted-foreground">
            <FolderTree size={13} /> 目录树 <span className="ml-auto tabular text-muted-foreground/60">{filtered.length}</span>
          </div>
          <FileTree changes={filtered} selected={selected ? String(selected.id) : undefined} onSelect={(c) => setSelectedId(c.id)} />
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
