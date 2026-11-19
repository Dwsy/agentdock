import { useMemo, useState } from 'react';
import { Columns2, Rows3, Copy, Check } from 'lucide-react';
import { usePersistedState } from '../lib/usePersistedState';

type DiffLine = { type: 'add' | 'del' | 'ctx' | 'hunk' | 'meta'; oldNo?: number; newNo?: number; text: string };
type Pair = { left: DiffLine | null; right: DiffLine | null };

/** 解析 unified diff 成结构化行。 */
function parseDiff(diff: string): DiffLine[] {
  const lines = diff.split('\n');
  const out: DiffLine[] = [];
  let oldNo = 0;
  let newNo = 0;
  for (const line of lines) {
    if (line.startsWith('---') || line.startsWith('+++')) {
      out.push({ type: 'meta', text: line });
    } else if (line.startsWith('@@')) {
      const m = line.match(/@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
      if (m) { oldNo = +m[1]; newNo = +m[2]; }
      out.push({ type: 'hunk', text: line });
    } else if (line.startsWith('+')) {
      out.push({ type: 'add', newNo: newNo++, text: line.slice(1) });
    } else if (line.startsWith('-')) {
      out.push({ type: 'del', oldNo: oldNo++, text: line.slice(1) });
    } else if (line.startsWith('\\')) {
      // "\ No newline at end of file"
      continue;
    } else {
      out.push({ type: 'ctx', oldNo: oldNo++, newNo: newNo++, text: line.slice(1) });
    }
  }
  return out;
}

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
        pairs.push({ left: dels[k] || { type: 'ctx', text: '' }, right: adds[k] || { type: 'ctx', text: '' } });
      }
    } else {
      pairs.push({ left: cur, right: cur.type === 'ctx' || cur.type === 'hunk' || cur.type === 'meta' ? cur : null });
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

function CodeLine({ line }: { line: DiffLine }) {
  return (
    <div className={`flex font-mono text-[11px] leading-[1.55] ${rowBg[line.type] || ''}`}>
      <LineNo n={line.oldNo ?? line.newNo} kind={line.type} />
      <span className="w-3 shrink-0 select-none text-center opacity-60">{sign[line.type]}</span>
      <span className={`min-w-0 flex-1 whitespace-pre-wrap break-all pr-2 ${line.type === 'add' ? 'text-emerald-600 dark:text-emerald-400' : line.type === 'del' ? 'text-red-600 dark:text-red-400' : ''}`}>
        {line.text || ' '}
      </span>
    </div>
  );
}

export default function DiffViewer({ diff, filePath }: { diff: string; filePath?: string }) {
  const [mode, setMode] = usePersistedState<'split' | 'unified'>('diff-mode', 'split');
  const [copied, setCopied] = useState(false);
  const lines = useMemo(() => parseDiff(diff), [diff]);
  const pairs = useMemo(() => pairLines(lines), [lines]);
  const adds = lines.filter((l) => l.type === 'add').length;
  const dels = lines.filter((l) => l.type === 'del').length;

  const copy = () => {
    navigator.clipboard?.writeText(diff);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  return (
    <div>
      <div className="mb-2 flex items-center gap-2">
        <span className="text-[11px] text-emerald-500">+{adds}</span>
        <span className="text-[11px] text-red-500">-{dels}</span>
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

      <div className="scrollbar-thin max-h-[520px] overflow-auto rounded-lg border border-border/60 bg-muted/20">
        {mode === 'unified' ? (
          <div className="min-w-full py-1">
            {lines.map((l, i) => <CodeLine key={i} line={l} />)}
          </div>
        ) : (
          <div className="min-w-[720px] py-1">
            <div className="sticky top-0 z-10 grid grid-cols-2 border-b border-border/60 bg-card/90 text-[10px] font-medium text-muted-foreground backdrop-blur">
              <div className="border-r border-border/60 px-2 py-1 text-center">原始</div>
              <div className="px-2 py-1 text-center">修改后</div>
            </div>
            {pairs.map((p, i) => (
              <div key={i} className="grid grid-cols-2">
                <div className="border-r border-border/60">{p.left && <CodeLine line={p.left} />}</div>
                <div>{p.right && <CodeLine line={{ ...p.right }} />}</div>
              </div>
            ))}
          </div>
        )}
      </div>
      {filePath && <div className="mt-1.5 truncate font-mono text-[10px] text-muted-foreground" title={filePath}>{filePath}</div>}
    </div>
  );
}
