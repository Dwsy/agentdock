import { useMemo, useState } from 'react';
import { Columns2, Rows3, Copy, Check } from 'lucide-react';
import { usePersistedState } from '../lib/usePersistedState';
import { fileDiffPreview, type DiffLine } from '../lib/diffPreview';
import { CodeTokens, fileLanguage, useCodeTokens } from './CodeSource';
import type { ThemedToken } from '@pierre/highlights';

type Pair = { left: DiffLine | null; right: DiffLine | null };

/** 把线性 diff 行配对成左右两列：连续的 del 块和 add 块对齐到同一行。 */
function pairLines(lines: DiffLine[]): Pair[] {
  const pairs: Pair[] = [];
  let i = 0;
  while (i < lines.length) {
    const cur = lines[i];
    if (cur.type === 'del') {
      const dels: DiffLine[] = [];
      while (i < lines.length && lines[i].type === 'del') dels.push(lines[i++]);
      const adds: DiffLine[] = [];
      while (i < lines.length && lines[i].type === 'add') adds.push(lines[i++]);
      const n = Math.max(dels.length, adds.length);
      for (let k = 0; k < n; k++) {
        pairs.push({ left: dels[k] ?? null, right: adds[k] ?? null });
      }
    } else {
      pairs.push({ left: cur.type === 'add' ? null : cur, right: cur });
      i++;
    }
  }
  return pairs;
}

function LineNo({ n, kind }: { n?: number; kind: DiffLine['type'] }) {
  return <span className={`w-9 shrink-0 select-none pr-2 text-right text-[10px] tabular ${kind === 'del' ? 'text-red-500/50' : kind === 'add' ? 'text-emerald-500/50' : 'text-muted-foreground/40'}`}>{n ?? ''}</span>;
}

const rowBg: Record<string, string> = {
  add: 'bg-emerald-500/12',
  del: 'bg-red-500/12',
  hunk: 'bg-sky-500/10 text-sky-500',
  meta: 'text-muted-foreground',
  ctx: '',
};
const sign: Record<string, string> = { add: '+', del: '-', ctx: ' ', hunk: '', meta: '' };

function CodeLine({ line, side, tokens }: { line: DiffLine; side?: 'old' | 'new'; tokens?: ThemedToken[] }) {
  return (
    <div className={`flex font-mono text-[11px] leading-[1.55] ${rowBg[line.type] || ''}`}>
      {side !== 'new' && <LineNo n={line.oldNo} kind={line.type} />}
      {side !== 'old' && <LineNo n={line.newNo} kind={line.type} />}
      <span className="w-3 shrink-0 select-none text-center opacity-60">{sign[line.type]}</span>
      <span className={`min-w-0 flex-1 whitespace-pre-wrap break-all pr-2 ${line.type === 'add' ? 'text-emerald-600 dark:text-emerald-400' : line.type === 'del' ? 'text-red-600 dark:text-red-400' : ''}`}>
        <CodeTokens text={line.text || ' '} tokens={tokens} />
      </span>
    </div>
  );
}

type DiffViewerProps = {
  diff: string;
  filePath?: string;
  /** move 的目标路径：只有源分节时也能把目标分节一起并入预览。 */
  moveTo?: string;
  /** 记录保存的逐文件计数，在只能看到片段时作为参考。 */
  insertions?: number;
  deletions?: number;
  truncated?: boolean;
};

export default function DiffViewer({ diff, filePath, moveTo, insertions, deletions, truncated }: DiffViewerProps) {
  const [mode, setMode] = usePersistedState<'split' | 'unified'>('diff-mode', 'split');
  const [copied, setCopied] = useState(false);
  const { text, lines, matched } = useMemo(
    () => fileDiffPreview(diff, { path: filePath, moveTo }),
    [diff, filePath, moveTo],
  );
  const pairs = useMemo(() => pairLines(lines), [lines]);
  // Keep each side's lexical state independent and align tokens with the existing preview rows.
  const source = useMemo(() => ({
    indices: new Map(lines.map((line, i) => [line, i])),
    old: lines.map((line) => line.type === 'del' || line.type === 'ctx' ? line.text : '').join('\n'),
    new: lines.map((line) => line.type === 'add' || line.type === 'ctx' ? line.text : '').join('\n'),
  }), [lines]);
  const oldTokens = useCodeTokens(source.old, fileLanguage(filePath));
  const newTokens = useCodeTokens(source.new, fileLanguage(moveTo || filePath));

  function renderLine(line: DiffLine | null, side?: 'old' | 'new', key?: number) {
    if (!line) return null;
    const i = source.indices.get(line)!;
    const tokens = side === 'old' || line.type === 'del' ? oldTokens : newTokens;
    return <CodeLine key={key} line={line} side={side} tokens={tokens?.[i]} />;
  }

  if (lines.length === 0) {
    const hint = !diff.trim()
      ? '暂无此文件的 diff'
      : filePath && !matched
        ? truncated ? '已取得的 diff 片段没有这个文件的分节' : '未能从 diff 中定位该文件的分节'
        : '该文件在 diff 中没有可显示的行';
    return <div className="px-2 py-3 text-[11px] text-muted-foreground" title={filePath}>{hint}</div>;
  }

  const visibleAdds = lines.filter((l) => l.type === 'add').length;
  const visibleDels = lines.filter((l) => l.type === 'del').length;
  const hasTotals = insertions !== undefined || deletions !== undefined;

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // 剪贴板写入被拒绝或不可用时保持未复制状态，不能谎报成功。
    }
  };

  return (
    <div>
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <span className="tabular text-[11px] text-muted-foreground">
          {truncated ? '片段' : '增删'} <span className="text-emerald-500">+{visibleAdds}</span> <span className="text-red-500">−{visibleDels}</span>
        </span>
        {truncated && hasTotals && (
          <span className="tabular text-[11px] text-muted-foreground" title="此记录保存的增删行数">
            记录 <span className="text-emerald-500">+{insertions ?? 0}</span> <span className="text-red-500">−{deletions ?? 0}</span>
          </span>
        )}
        <div className="ml-auto flex gap-1">
          <button onClick={() => setMode('split')} className={`glass-control flex items-center gap-1 rounded-md px-2 py-1 text-[11px] ${mode === 'split' ? 'text-foreground' : 'text-muted-foreground'}`} title="左右对比">
            <Columns2 size={12} /> 左右
          </button>
          <button onClick={() => setMode('unified')} className={`glass-control flex items-center gap-1 rounded-md px-2 py-1 text-[11px] ${mode === 'unified' ? 'text-foreground' : 'text-muted-foreground'}`} title="统一视图">
            <Rows3 size={12} /> 统一
          </button>
          <button onClick={copy} className="glass-control flex items-center gap-1 rounded-md px-2 py-1 text-[11px] text-muted-foreground" title="复制 diff">
            {copied ? <Check size={12} /> : <Copy size={12} />}
          </button>
        </div>
      </div>

      {truncated && <p className="mb-2 text-[11px] text-amber-500">当前只取得部分 diff。</p>}

      <div className="code-surface scrollbar-thin max-h-[520px] overflow-auto rounded-lg border border-border/60">
        {mode === 'unified' ? (
          <div className="min-w-full py-1">
            {lines.map((line, i) => renderLine(line, undefined, i))}
          </div>
        ) : (
          <div className="min-w-[720px] py-1">
            <div className="sticky top-0 z-10 grid grid-cols-2 border-b border-border/60 bg-card/90 text-[10px] font-medium text-muted-foreground backdrop-blur">
              <div className="border-r border-border/60 px-2 py-1 text-center">原始</div>
              <div className="px-2 py-1 text-center">修改后</div>
            </div>
            {pairs.map((p, i) => (
              <div key={i} className="grid grid-cols-2">
                <div className="border-r border-border/60">{renderLine(p.left, 'old')}</div>
                <div>{renderLine(p.right, 'new')}</div>
              </div>
            ))}
          </div>
        )}
      </div>
      {(filePath || moveTo) && (
        <div className="mt-1.5 truncate font-mono text-[10px] text-muted-foreground" title={filePath}>
          {filePath}{moveTo && moveTo !== filePath ? ` → ${moveTo}` : ''}
        </div>
      )}
    </div>
  );
}
