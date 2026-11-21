import { useState } from 'react';
import { Terminal, Copy, Check, ChevronDown, Folder, Clock3, TerminalSquare } from 'lucide-react';
import type { TraceRecord } from '../api';
import { Badge, ms, bytes } from '../ui';
import { CodeTokens, useCodeTokens } from './CodeSource';

function HighlightedBlock({ text, label, tone = 'default' }: { text: string; label?: string; tone?: 'default' | 'error' }) {
  const [copied, setCopied] = useState(false);
  const lines = text.split('\n');
  const tokens = useCodeTokens(text, label ? 'bash' : undefined);
  return (
    <div>
      {label && (
        <div className="mb-1.5 flex items-center justify-between">
          <span className={`text-[11px] font-medium ${tone === 'error' ? 'text-red-500' : 'text-muted-foreground'}`}>{label}</span>
          <button onClick={() => { navigator.clipboard?.writeText(text); setCopied(true); setTimeout(() => setCopied(false), 1500); }} className="text-muted-foreground hover:text-foreground" title="复制">
            {copied ? <Check size={12} /> : <Copy size={12} />}
          </button>
        </div>
      )}
      <div className="code-surface scrollbar-thin max-h-64 overflow-auto rounded-lg border border-border/60 py-1.5">
        {lines.map((l, i) => (
          <div key={i} className="flex font-mono text-[11px] leading-[1.55] hover:bg-muted/30">
            <span className="w-9 shrink-0 select-none pr-2 text-right text-[10px] tabular text-muted-foreground/40">{i + 1}</span>
            <span className={`min-w-0 flex-1 whitespace-pre-wrap break-all pr-2 ${tone === 'error' ? 'text-red-400/90' : ''}`}>
              <CodeTokens text={l || ' '} tokens={tokens?.[i]} />
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}

/** 完整命令展示：对齐 Swift 版 TraceCommandCard + TraceOutputSection。 */
export default function CommandBlock({ record }: { record: TraceRecord }) {
  const c = record.command;
  const [showStdout, setShowStdout] = useState(true);
  const [showStderr, setShowStderr] = useState(true);
  if (!c) return null;
  const ok = c.command_ok ?? c.exit_code === 0;

  return (
    <div className="animate-fade-in space-y-3 border-t border-border/60 px-3 py-3">
      <div className="flex flex-wrap items-center gap-2">
        <Badge className={ok ? 'bg-emerald-500/10 text-emerald-500' : 'bg-red-500/10 text-red-500'}>{ok ? '成功' : '失败'}</Badge>
        {c.status && <Badge className="bg-muted/20 text-muted-foreground">{c.status}</Badge>}
        {c.exit_code != null && <span className="text-[11px] text-muted-foreground">exit <span className={c.exit_code === 0 ? 'text-emerald-500' : 'text-red-500'}>{c.exit_code}</span></span>}
        {c.execution_mode && <span className="text-[11px] text-muted-foreground">模式 {c.execution_mode}</span>}
        {c.elapsed_ms != null && <span className="flex items-center gap-1 text-[11px] text-muted-foreground"><Clock3 size={11} /> {ms(c.elapsed_ms)}</span>}
      </div>
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-muted-foreground">
        {c.workdir && <span className="flex items-center gap-1"><Folder size={11} /> {c.workdir}</span>}
        {c.session_id && <span className="flex items-center gap-1"><TerminalSquare size={11} /> {c.session_id}</span>}
        {c.timeout_ms != null && <span>超时 {ms(c.timeout_ms)}</span>}
      </div>

      {c.command && <HighlightedBlock text={c.command} label="命令" />}
      {c.stdout && (
        <div>
          <button onClick={() => setShowStdout((v) => !v)} className="mb-1.5 flex items-center gap-1 text-[11px] font-medium text-muted-foreground hover:text-foreground">
            <ChevronDown size={12} className={`transition-transform ${showStdout ? '' : '-rotate-90'}`} />
            标准输出 {c.stdout_bytes != null && `· ${bytes(c.stdout_bytes)}`}
          </button>
          {showStdout && <HighlightedBlock text={c.stdout} />}
        </div>
      )}
      {c.stderr && (
        <div>
          <button onClick={() => setShowStderr((v) => !v)} className="mb-1.5 flex items-center gap-1 text-[11px] font-medium text-red-500 hover:text-red-400">
            <ChevronDown size={12} className={`transition-transform ${showStderr ? '' : '-rotate-90'}`} />
            标准错误 {c.stderr_bytes != null && `· ${bytes(c.stderr_bytes)}`}
          </button>
          {showStderr && <HighlightedBlock text={c.stderr} tone="error" />}
        </div>
      )}
      {!c.command && !c.stdout && !c.stderr && c.summary && (
        <div className="flex items-start gap-2 rounded-lg bg-muted/30 p-3 font-mono text-[11px]">
          <Terminal size={14} className="mt-0.5 shrink-0 text-muted-foreground" />
          <span className="whitespace-pre-wrap break-all">{c.summary}</span>
        </div>
      )}
    </div>
  );
}
