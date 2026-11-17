import { useMemo, useState } from 'react';
import { ChevronRight, ChevronDown, Folder, FolderOpen, FileCode2, FileText, File } from 'lucide-react';
import type { FileChange } from '../api';

type Node = {
  name: string;
  path: string;
  dir: boolean;
  children: Map<string, Node>;
  change?: FileChange;
};

function buildTree(changes: FileChange[]): Node {
  const root: Node = { name: '', path: '', dir: true, children: new Map() };
  for (const c of changes) {
    const parts = c.path.split('/').filter(Boolean);
    let cur = root;
    let acc = '';
    parts.forEach((part, i) => {
      acc += (acc ? '/' : '') + part;
      const isFile = i === parts.length - 1;
      if (!cur.children.has(part)) {
        cur.children.set(part, { name: part, path: acc, dir: !isFile, children: new Map(), change: isFile ? c : undefined });
      }
      cur = cur.children.get(part)!;
      if (isFile) cur.change = c;
    });
  }
  return root;
}

function fileIcon(name: string) {
  if (/\.(md|mdx|txt)$/i.test(name)) return <FileText size={14} className="text-sky-400" />;
  if (/\.(ts|tsx|js|jsx|vue|go|rs|py|java|kt|swift|json|scss|css|html)$/i.test(name)) return <FileCode2 size={14} className="text-violet-400" />;
  return <File size={14} className="text-muted-foreground" />;
}

function TreeRow({ node, depth, selected, onSelect }: { node: Node; depth: number; selected?: string; onSelect: (c: FileChange) => void }) {
  const [open, setOpen] = useState(depth < 2);
  const isActive = node.change && selected === String(node.change.id);
  if (node.dir) {
    const kids = [...node.children.values()].sort((a, b) => (a.dir === b.dir ? a.name.localeCompare(b.name) : a.dir ? -1 : 1));
    return (
      <div>
        <button onClick={() => setOpen((v) => !v)} className="flex w-full items-center gap-1 rounded-md py-1 pr-2 text-left text-xs text-muted-foreground hover:bg-muted/40" style={{ paddingLeft: depth * 14 + 4 }}>
          {open ? <ChevronDown size={13} className="shrink-0" /> : <ChevronRight size={13} className="shrink-0" />}
          {open ? <FolderOpen size={14} className="shrink-0 text-amber-400/80" /> : <Folder size={14} className="shrink-0 text-amber-400/80" />}
          <span className="truncate">{node.name}</span>
          <span className="ml-auto text-[10px] tabular text-muted-foreground/60">{countFiles(node)}</span>
        </button>
        {open && kids.map((k) => <TreeRow key={k.path} node={k} depth={depth + 1} selected={selected} onSelect={onSelect} />)}
      </div>
    );
  }
  return (
    <button
      onClick={() => node.change && onSelect(node.change)}
      className={`flex w-full items-center gap-1 rounded-md py-1 pr-2 text-left text-xs ${isActive ? 'glass-chosen text-foreground' : 'text-muted-foreground hover:bg-muted/40'}`}
      style={{ paddingLeft: depth * 14 + 4 }}
      title={node.path}
    >
      <span className="w-[13px] shrink-0" />
      {fileIcon(node.name)}
      <span className="truncate">{node.name}</span>
      {node.change && <span className={`ml-auto shrink-0 text-[10px] ${node.change.insertions > 0 ? 'text-emerald-500' : 'text-muted-foreground'}`}>+{node.change.insertions || 0}</span>}
    </button>
  );
}

function countFiles(n: Node): number {
  if (!n.dir) return 1;
  let c = 0;
  n.children.forEach((k) => (c += countFiles(k)));
  return c;
}

export default function FileTree({ changes, selected, onSelect }: { changes: FileChange[]; selected?: string; onSelect: (c: FileChange) => void }) {
  const tree = useMemo(() => buildTree(changes), [changes]);
  const roots = useMemo(() => [...tree.children.values()].sort((a, b) => (a.dir === b.dir ? a.name.localeCompare(b.name) : a.dir ? -1 : 1)), [tree]);
  return (
    <div className="scrollbar-thin max-h-[640px] overflow-auto pr-1">
      {roots.map((r) => <TreeRow key={r.path} node={r} depth={0} selected={selected} onSelect={onSelect} />)}
      {roots.length === 0 && <div className="py-12 text-center text-sm text-muted-foreground">没有文件</div>}
    </div>
  );
}
