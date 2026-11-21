import { useEffect, useId, useState } from 'react';
import { FileJson, FileText, Code2, RefreshCw, AlertCircle, FilePenLine, Tag, ChevronDown, Copy, Check } from 'lucide-react';
import { api, type TraceRecord } from '../api';
import { ms, shortPath } from '../ui';
import { usePersistedState } from '../lib/usePersistedState';
import { hasTruncatedPreview, isTraceTextTruncated, loadTraceDetail } from '../lib/traceDetail';
import { CodeSource } from './CodeSource';
import CommandBlock from './CommandBlock';
import MarkdownView from './MarkdownView';
import DiffViewer from './DiffViewer';
import { ReadFileCard, ViewImageCard, parseTraceResult, type ReadFileResult, type ViewImageResult } from './TraceResultCards';

const sourceClass = 'code-surface scrollbar-thin overflow-auto whitespace-pre-wrap break-all font-mono text-[11px] leading-relaxed';

function RawArguments({ text, expanded, notice, empty }: { text?: string; expanded: boolean; notice?: string; empty: string }) {
  const id = useId();
  const [openOverride, setOpenOverride] = useState<boolean>();
  const [copiedText, setCopiedText] = useState<string>();
  const open = openOverride ?? expanded;

  return (
    <div className="rounded-xl border border-border/60 bg-card/40">
      <div className="flex items-center gap-2 px-3 py-2">
        <button type="button" aria-expanded={open} aria-controls={id} onClick={() => setOpenOverride(!open)}
          className="flex min-w-0 flex-1 items-center gap-1.5 text-left text-xs font-medium hover:text-foreground">
          <ChevronDown size={12} className={open ? '' : '-rotate-90'} />
          <Code2 size={13} /> 原始参数
        </button>
        {text && <button type="button" onClick={async () => {
          try {
            await navigator.clipboard.writeText(text);
            setCopiedText(text);
          } catch { setCopiedText(undefined); }
        }} className="glass-control rounded px-1.5 py-1 text-muted-foreground hover:text-foreground"
          title={copiedText === text ? '已复制' : '复制原始参数'} aria-label={copiedText === text ? '已复制' : '复制原始参数'}>
          {copiedText === text ? <Check size={12} /> : <Copy size={12} />}
        </button>}
      </div>
      {notice && <p role="status" className="px-3 pb-2 text-[11px] text-muted-foreground">{notice}</p>}
      <div id={id} hidden={!open}>
        {open && (text
          ? <CodeSource text={text} language="json" className={`${sourceClass} mx-3 mb-3 max-h-64 rounded-lg bg-muted/30 p-2.5`} />
          : <p className="px-3 pb-3 text-[11px] text-muted-foreground">{empty}</p>)}
      </div>
    </div>
  );
}

function formatResult(text: string): string {
  try { return JSON.stringify(JSON.parse(text), null, 2); } catch { return text; }
}

function extractMarkdown(s?: string): string | null {
  if (!s) return null;
  try {
    const obj = JSON.parse(s);
    const content = obj?.content;
    if (typeof content === 'string' && (content.includes('\n#') || content.includes('---\n') || content.includes('\n- '))) return content;
  } catch { /* not json */ }
  return null;
}

export default function TraceDetail({ record }: { record: TraceRecord }) {
  return <TraceDetailBody key={record.id} record={record} />;
}

/** 单条调用详情：优先用详情接口拉全文，失败则退回列表记录。 */
function TraceDetailBody({ record }: { record: TraceRecord }) {
  const [request, setRequest] = useState<Awaited<ReturnType<typeof loadTraceDetail>> | 'failed'>();
  const [view, setView] = usePersistedState<'auto' | 'source'>('trace-detail-view', 'auto');
  const [openFile, setOpenFile] = useState<number>(0);
  const [showRawResult, setShowRawResult] = useState(false);
  const { id, tool } = record;
  const wantFull = tool === 'read_file' || hasTruncatedPreview(record);

  useEffect(() => {
    const controller = new AbortController();
    loadTraceDetail(id, wantFull, api.traceDetail, controller.signal)
      .then((response) => { if (!controller.signal.aborted) setRequest(response); })
      .catch(() => { if (!controller.signal.aborted) setRequest('failed'); });
    return () => controller.abort();
  }, [id, wantFull]);

  const loading = request === undefined;
  const detail = request === 'failed' ? undefined : request?.detail;
  const r = detail?.record ?? record;
  const full = detail?.full;
  const cmd = r.command || full?.command ? { ...r.command, ...full?.command } : undefined;
  const argumentsText = full?.arguments ?? r.arguments;
  const resultText = full?.result ?? r.result;
  const mdFromResult = extractMarkdown(resultText) || extractMarkdown(r.result);
  const truncatedPreview = hasTruncatedPreview(r) || hasTruncatedPreview(record);
  let notice: string | undefined;
  if (request === 'failed') {
    notice = '调用详情读取失败，显示列表中可用的信息。';
  } else if (request?.fullFailed) {
    notice = '全文读取失败，显示已取得的预览。';
  } else if (isTraceTextTruncated(argumentsText)) {
    notice = full?.arguments !== undefined ? '原始参数达到全文上限，已截断。' : '原始参数预览已截断，未取得全文。';
  } else if (full?.full_truncated) {
    notice = '部分调用正文达到全文上限，已截断。';
  } else if (detail && truncatedPreview && !full) {
    notice = '部分调用正文预览已截断，未取得全文。';
  } else if (detail?.full_available && !full) {
    notice = '未取得全文，显示已取得的预览。';
  }
  // 详情接口的 record 未必带 file_changes，列表记录才是它的来源。
  const fileChanges = r.file_changes || record.file_changes || [];
  // 上游客户端头（Nexus 转发）与本地连接头无关，单独展示来源信息。
  const upstreamHeaders = Object.entries(r.upstream_headers || {});
  // 已知工具的专用卡片：read_file 渲染带行号的正文，view_image 走图片 sidecar。
  // 解析失败（预览被截断成非法 JSON）时退回通用渲染，不展示半个对象。
  const readFileResult = r.tool === 'read_file' ? parseTraceResult<ReadFileResult>(resultText) : null;
  const viewImageResult = r.tool === 'view_image' ? parseTraceResult<ViewImageResult>(resultText) : null;
  const hasTypedCard = Boolean(readFileResult || viewImageResult);

  return (
    <div className="animate-fade-in space-y-3">
      {/* 头部元信息 */}
      <div className="flex flex-wrap items-center gap-2 rounded-xl border border-border/60 bg-card/40 p-3.5">
        <span className={`h-2 w-2 rounded-full ${r.success ? 'bg-emerald-500' : 'bg-red-500'}`} />
        <span className="font-mono text-sm font-medium">{r.tool}</span>
        <span className="rounded bg-muted/20 px-1.5 py-0.5 text-[11px] text-muted-foreground">{r.source}</span>
        <span className="text-[11px] text-muted-foreground">ID {r.id}</span>
        {r.error_code && <span className="ml-auto flex items-center gap-1 text-[11px] text-red-500"><AlertCircle size={12} /> {r.error_code} {r.error_category && `(${r.error_category})`}</span>}
        <div className="ml-auto flex items-center gap-3 text-[11px] text-muted-foreground">
          <span>{new Date(r.started_at).toLocaleString('zh-CN')}</span>
          <span>耗时 {ms(r.duration_ms)}</span>
          {loading && <RefreshCw size={12} className="animate-spin" />}
        </div>
      </div>

      {upstreamHeaders.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5 rounded-xl border border-border/60 bg-card/40 p-2.5">
          <span className="flex items-center gap-1 text-[11px] text-muted-foreground"><Tag size={11} /> 上游请求头</span>
          {upstreamHeaders.map(([name, value]) => (
            <span key={name} className="rounded bg-muted/20 px-1.5 py-0.5 font-mono text-[11px]" title={`${name}: ${value}`}>
              <span className="text-muted-foreground">{name}</span>=<span className="break-all">{value}</span>
            </span>
          ))}
        </div>
      )}

      <RawArguments
        text={argumentsText}
        expanded={!fileChanges.length && !cmd && !hasTypedCard && !(loading && (r.tool === 'read_file' || r.tool === 'view_image'))}
        notice={notice}
        empty={loading ? '正在读取原始参数…' : request === 'failed' ? '未能读取原始参数。' : '此调用未返回原始参数。'}
      />

      {fileChanges.length > 0 ? (
        // file_edit / file_write：直接渲染结构化 diff，不做 JSON 平铺。
        <div className="space-y-2">
          <div className="flex items-center gap-1.5 text-[11px] font-medium text-muted-foreground"><FilePenLine size={13} /> 文件变更（{fileChanges.length}）</div>
          {fileChanges.map((fc, i) => (
            <div key={i} className="rounded-xl border border-border/60 bg-card/40">
              <button onClick={() => setOpenFile(openFile === i ? -1 : i)} className="flex w-full items-center gap-2 p-3 text-left text-xs hover:bg-card/60">
                <span className={`rounded px-1.5 py-0.5 text-[10px] ${fc.action === 'create' ? 'bg-sky-500/10 text-sky-500' : 'bg-violet-500/10 text-violet-500'}`}>{fc.action}</span>
                <span className="min-w-0 flex-1 truncate font-mono" title={fc.path}>{shortPath(fc.path)}</span>
                <span className="shrink-0 text-[10px] text-emerald-500">+{fc.insertions || 0}</span>
                {fc.deletions != null && <span className="shrink-0 text-[10px] text-red-500">-{fc.deletions}</span>}
              </button>
              {openFile === i && (
                <div className="border-t border-border/60 p-3">
                  <DiffViewer
                    diff={fc.diff || ''}
                    filePath={fc.path}
                    moveTo={fc.move_to}
                    insertions={fc.insertions}
                    deletions={fc.deletions}
                    truncated={fc.truncated}
                  />
                </div>
              )}
            </div>
          ))}
        </div>
      ) : cmd ? (
        <CommandBlock record={{ ...r, command: cmd }} />
      ) : (
        <>
          {readFileResult && <ReadFileCard data={readFileResult} />}
          {viewImageResult && <ViewImageCard traceId={r.id} data={viewImageResult} preview={r.image_preview} />}
          {hasTypedCard && resultText && (
            <details onToggle={(event) => setShowRawResult(event.currentTarget.open)} className="rounded-lg border border-border/60 bg-muted/20 px-2.5 py-1.5">
              <summary className="cursor-pointer text-[11px] text-muted-foreground">原始结果 JSON</summary>
              {showRawResult && <CodeSource text={formatResult(resultText)} language="json" className={`${sourceClass} mt-1.5 max-h-64`} />}
            </details>
          )}
          {resultText && !hasTypedCard && (
            <div>
              <div className="mb-1 flex items-center justify-between">
                <span className="flex items-center gap-1 text-[11px] font-medium text-muted-foreground"><FileJson size={12} /> 结果</span>
                {mdFromResult && (
                  <div className="flex gap-1">
                    <button onClick={() => setView('auto')} className={`glass-control rounded px-1.5 py-0.5 text-[10px] ${view === 'auto' ? 'text-foreground' : 'text-muted-foreground'}`}><FileText size={10} className="mr-0.5 inline" />渲染</button>
                    <button onClick={() => setView('source')} className={`glass-control rounded px-1.5 py-0.5 text-[10px] ${view === 'source' ? 'text-foreground' : 'text-muted-foreground'}`}><Code2 size={10} className="mr-0.5 inline" />源码</button>
                  </div>
                )}
              </div>
              {mdFromResult && view === 'auto' ? (
                <div className="scrollbar-thin max-h-[520px] overflow-auto rounded-lg border border-border/60 bg-muted/20 p-3"><MarkdownView content={mdFromResult} /></div>
              ) : (
                <CodeSource text={formatResult(resultText)} language="json" className={`${sourceClass} max-h-[520px] rounded-lg bg-muted/30 p-2.5`} />
              )}
            </div>
          )}
        </>
      )}
    </div>
  );
}
