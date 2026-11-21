import { useMemo, useState } from 'react';
import { ChevronRight, ChevronDown, Folder, FolderOpen, FileCode2, FileText, File } from 'lucide-react';
import type { FileChange } from '../api';
import { fileChangeKey } from '../lib/changeProjects';
import { relTime } from '../ui';

type Node = {
  name: string;
  path: string;
  dir: boolean;
  children: Map<string, Node>;
  changes?: FileChange[];
};

function buildTree(changes: FileChange[], getDisplayPath: (c: FileChange) => string): Node {
  const root: Node = { name: '', path: '', dir: true, children: new Map() };
  for (const c of changes) {
    const dp = getDisplayPath(c);
    const parts = dp.split('/').filter(Boolean);
    let cur = root;
    let acc = '';
    parts.forEach((part, i) => {
      acc += (acc ? '/' : '') + part;
      const isFile = i === parts.length - 1;
      if (!cur.children.has(part)) {
        cur.children.set(part, { name: part, path: acc, dir: !isFile, children: new Map() });
      }
      cur = cur.children.get(part)!;
      if (isFile) {
        (cur.changes ||= []).push(c);
        cur.dir = false;
      }
    });
  }
  return root;
}

function compactDir(node: Node): { name: string; path: string; inner: Node } {
  let name = node.name;
  let path = node.path;
  let inner = node;
  // 折叠连续单子目录，减少逐级点击进入项目的成本。
  while (inner.dir && inner.children.size === 1) {
    const [only] = [...inner.children.values()];
    if (!only.dir) break;
    name = name ? `${name}/${only.name}` : only.name;
    path = only.path;
    inner = only;
  }
  return { name, path, inner };
}

function fileIcon(name: string) {
  if (/\.(md|mdx|txt)$/i.test(name)) return <FileText size={14} className="text-sky-400" />;
  if (/\.(ts|tsx|js|jsx|vue|go|rs|py|java|kt|swift|json|scss|css|html)$/i.test(name)) return <FileCode2 size={14} className="text-violet-400" />;
  return <File size={14} className="text-muted-foreground" />;
}

function sumStats(changes: FileChange[]) {
  let ins = 0;
  let del = 0;
  for (const c of changes) {
    ins += c.insertions || 0;
    del += c.deletions || 0;
  }
  return { ins, del };
}

function ChangeSubRow({ change, depth, selected, onSelect }: { change: FileChange; depth: number; selected?: string; onSelect: (c: FileChange) => void }) {
  const active = selected === fileChangeKey(change);
  return (
    <button
      onClick={() => onSelect(change)}
      className={`flex w-full items-center gap-1.5 rounded-md py-0.5 pr-2 text-left text-[11px] ${active ? 'glass-chosen text-foreground' : 'text-muted-foreground hover:bg-muted/40'}`}
      style={{ paddingLeft: depth * 14 + 4 }}
      title={`${change.path}\n${change.tool} · ${relTime(change.started_at)}`}
    >
      <span className="w-[13px] shrink-0" />
      <span className="shrink-0 text-[10px] text-muted-foreground/70">{relTime(change.started_at)}</span>
      <span className="min-w-0 truncate text-[10px] opacity-70">{change.tool}</span>
      <span className="ml-auto flex shrink-0 items-center gap-1 text-[10px] tabular">
        {(change.insertions || 0) > 0 && <span className="text-emerald-500">+{change.insertions}</span>}
        {change.deletions ? <span className="text-red-500">-{change.deletions}</span> : null}
      </span>
    </button>
  );
}

function FileNodeRow({ node, depth, selected, onSelect }: { node: Node; depth: number; selected?: string; onSelect: (c: FileChange) => void }) {
  const items = node.changes || [];
  const [open, setOpen] = useState(false);

  if (items.length <= 1) {
    const change = items[0];
    const active = change && selected === fileChangeKey(change);
    return (
      <button
        onClick={() => change && onSelect(change)}
        className={`flex w-full items-center gap-1 rounded-md py-1 pr-2 text-left text-xs ${active ? 'glass-chosen text-foreground' : 'text-muted-foreground hover:bg-muted/40'}`}
        style={{ paddingLeft: depth * 14 + 4 }}
        title={node.path}
      >
        <span className="w-[13px] shrink-0" />
        {fileIcon(node.name)}
        <span className="truncate">{node.name}</span>
        {change && (
          <span className="ml-auto flex shrink-0 items-center gap-1 text-[10px] tabular">
            {(change.insertions || 0) > 0 && <span className="text-emerald-500">+{change.insertions}</span>}
            {change.deletions ? <span className="text-red-500">-{change.deletions}</span> : null}
          </span>
        )}
      </button>
    );
  }

  const { ins, del } = sumStats(items);
  const hasActive = items.some((c) => selected === fileChangeKey(c));
  return (
    <div>
      <button
        onClick={() => setOpen((v) => !v)}
        className={`flex w-full items-center gap-1 rounded-md py-1 pr-2 text-left text-xs ${hasActive ? 'text-foreground' : 'text-muted-foreground'} hover:bg-muted/40`}
        style={{ paddingLeft: depth * 14 + 4 }}
        title={node.path}
      >
        {open ? <ChevronDown size={13} className="shrink-0" /> : <ChevronRight size={13} className="shrink-0" />}
        {fileIcon(node.name)}
        <span className="truncate">{node.name}</span>
        <span className="ml-1 shrink-0 rounded bg-muted px-1 text-[10px] text-muted-foreground" title={`${items.length} 次修改`}>×{items.length}</span>
        <span className="ml-auto flex shrink-0 items-center gap-1 text-[10px] tabular">
          {ins > 0 && <span className="text-emerald-500">+{ins}</span>}
          {del > 0 && <span className="text-red-500">-{del}</span>}
        </span>
      </button>
      {open && items.map((change) => (
        <ChangeSubRow key={fileChangeKey(change)} change={change} depth={depth + 1} selected={selected} onSelect={onSelect} />
      ))}
    </div>
  );
}

function DirRow({ node, depth, selected, onSelect }: { node: Node; depth: number; selected?: string; onSelect: (c: FileChange) => void }) {
  const { name, inner } = compactDir(node);
  const [open, setOpen] = useState(depth < 2);
  const kids = [...inner.children.values()].sort((a, b) => (a.dir === b.dir ? a.name.localeCompare(b.name) : a.dir ? -1 : 1));
  return (
    <div>
      <button
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-1 rounded-md py-1 pr-2 text-left text-xs text-muted-foreground hover:bg-muted/40"
        style={{ paddingLeft: depth * 14 + 4 }}
        title={inner.path}
      >
        {open ? <ChevronDown size={13} className="shrink-0" /> : <ChevronRight size={13} className="shrink-0" />}
        {open ? <FolderOpen size={14} className="shrink-0 text-amber-400/80" /> : <Folder size={14} className="shrink-0 text-amber-400/80" />}
        <span className="truncate">{name}</span>
        <span className="ml-auto text-[10px] tabular text-muted-foreground/60">{countFiles(inner)}</span>
      </button>
      {open && kids.map((k) => <TreeRow key={k.path} node={k} depth={depth + 1} selected={selected} onSelect={onSelect} />)}
    </div>
  );
}

function TreeRow({ node, depth, selected, onSelect }: { node: Node; depth: number; selected?: string; onSelect: (c: FileChange) => void }) {
  if (!node.dir) return <FileNodeRow node={node} depth={depth} selected={selected} onSelect={onSelect} />;
  return <DirRow node={node} depth={depth} selected={selected} onSelect={onSelect} />;
}

function countFiles(n: Node): number {
  if (!n.dir) return (n.changes?.length ? 1 : 0);
  let c = 0;
  n.children.forEach((k) => (c += countFiles(k)));
  return c;
}

export default function FileTree({
  changes,
  selected,
  onSelect,
  getDisplayPath,
  className,
}: {
  changes: FileChange[];
  selected?: string;
  onSelect: (c: FileChange) => void;
  getDisplayPath?: (c: FileChange) => string;
  className?: string;
}) {
  const getPath = getDisplayPath ?? ((c: FileChange) => c.path);
  const tree = useMemo(() => buildTree(changes, getPath), [changes, getPath]);
  const roots = useMemo(() => [...tree.children.values()].sort((a, b) => (a.dir === b.dir ? a.name.localeCompare(b.name) : a.dir ? -1 : 1)), [tree]);
  return (
    <div className={className ?? 'scrollbar-thin max-h-[640px] overflow-auto pr-1'}>
      {roots.map((r) => <TreeRow key={r.path} node={r} depth={0} selected={selected} onSelect={onSelect} />)}
      {roots.length === 0 && <div className="py-12 text-center text-sm text-muted-foreground">没有文件</div>}
    </div>
  );
}
