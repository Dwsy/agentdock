import { useState } from 'react';
import { Terminal, Copy, Check, ChevronDown, Folder, Clock3, TerminalSquare } from 'lucide-react';
import type { TraceRecord } from '../api';
import { ms, bytes } from '../ui';
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
  const stopReason = c.timed_out || c.status === 'timeout' ? '已超时' : c.status === 'killed' ? '已终止' : undefined;
  const hasMetadata = stopReason || c.execution_mode || c.workdir || c.session_id || c.exit_code != null || c.elapsed_ms != null || c.timeout_ms != null;

  return (
    <div className="space-y-2">
      {c.command && <HighlightedBlock text={c.command} label="命令" />}
      {!c.command && !c.stdout && !c.stderr && c.summary && (
        <div className="flex items-start gap-2 rounded-lg bg-muted/30 p-2 font-mono text-[11px]">
          <Terminal size={14} className="mt-0.5 shrink-0 text-muted-foreground" />
          <span className="whitespace-pre-wrap break-all">{c.summary}</span>
        </div>
      )}
      {hasMetadata && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
          {stopReason && <span className="text-red-500">{stopReason}</span>}
          {c.exit_code != null && <span>exit <span className="font-mono text-foreground">{c.exit_code}</span></span>}
          {c.execution_mode && <span>模式 {c.execution_mode}</span>}
          {c.elapsed_ms != null && <span className="flex items-center gap-1" title="命令耗时"><Clock3 size={11} aria-hidden="true" /> {ms(c.elapsed_ms)}</span>}
          {c.workdir && <span className="flex min-w-0 items-center gap-1 break-all" title={c.workdir}><Folder size={11} className="shrink-0" aria-hidden="true" /> {c.workdir}</span>}
          {c.session_id && <span className="flex min-w-0 items-center gap-1 break-all" title={c.session_id}><TerminalSquare size={11} className="shrink-0" aria-hidden="true" /> {c.session_id}</span>}
          {c.timeout_ms != null && <span>超时限制 {ms(c.timeout_ms)}</span>}
        </div>
      )}
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
    </div>
  );
}
