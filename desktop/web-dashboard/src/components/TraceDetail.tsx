import { useEffect, useId, useMemo, useState } from 'react';
import { FileJson, FileText, Code2, RefreshCw, AlertCircle, FilePenLine, Info, ChevronDown, Copy, Check } from 'lucide-react';
import { api, type TraceRecord } from '../api';
import { ms, shortPath } from '../ui';
import { usePersistedState } from '../lib/usePersistedState';
import { traceOutcome } from '../lib/traceOutcome';
import { hasTruncatedPayload, hasTruncatedPreview, isTraceTextTruncated, loadTraceDetail, parseTraceResult, traceChangeDiff, traceFileDiff } from '../lib/traceDetail';
import { CodeSource } from './CodeSource';
import CommandBlock from './CommandBlock';
import MarkdownView from './MarkdownView';
import DiffViewer from './DiffViewer';
import { ReadFileCard, ViewImageCard, type ReadFileResult, type ViewImageResult } from './TraceResultCards';

const sourceClass = 'code-surface scrollbar-thin overflow-auto whitespace-pre-wrap break-all font-mono text-[11px] leading-relaxed';

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
  const argumentsId = useId();
  const [openArguments, setOpenArguments] = useState<boolean>();
  const [copiedArguments, setCopiedArguments] = useState<string>();
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
  // 兼容详情接口缺少 file_changes 的旧 Core。
  const fileChanges = r.file_changes ?? record.file_changes;
  const fileDiff = useMemo(() => {
    if (detail && fileChanges?.some((change) => change.truncated || !change.diff)) return traceFileDiff(detail);
  }, [detail, fileChanges]);
  const truncatedPreview = hasTruncatedPayload(r) || hasTruncatedPayload(record);
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
  // 上游客户端头（Nexus 转发）与本地连接头无关，单独展示来源信息。
  const upstreamHeaders = Object.entries(r.upstream_headers ?? record.upstream_headers ?? {});
  // 已知工具的专用卡片：read_file 渲染带行号的正文，view_image 走图片 sidecar。
  // 解析失败（预览被截断成非法 JSON）时退回通用渲染，不展示半个对象。
  const readFileResult = r.tool === 'read_file' ? parseTraceResult<ReadFileResult>(resultText) : null;
  const viewImageResult = r.tool === 'view_image' ? parseTraceResult<ViewImageResult>(resultText) : null;
  const hasTypedCard = Boolean(readFileResult || viewImageResult);
  const outcome = traceOutcome(r);
  const outcomeLabel = outcome === 'error' ? '失败' : outcome === 'running' ? '运行中' : '成功';
  const outcomeTone = outcome === 'error' ? 'text-red-500' : outcome === 'running' ? 'text-amber-500' : 'text-emerald-500';
  const hasCommandOutput = cmd?.command || cmd?.stdout || cmd?.stderr || cmd?.summary;
  const hasPrimaryContent = Boolean(fileChanges?.length || hasCommandOutput || resultText);
  const argumentsOpen = openArguments ?? (!loading && !hasPrimaryContent);
  const copyLabel = copiedArguments === argumentsText ? '已复制' : '复制原始参数';

  return (
    <div className="animate-fade-in space-y-2.5">
      <div className="border-b border-border/60 pb-2">
        <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
          <div className="flex min-w-0 items-center gap-2">
            <span className="truncate font-mono text-xs font-medium text-foreground" title={r.tool}>{r.tool}</span>
            <span className={`shrink-0 font-medium ${outcomeTone}`}>{outcomeLabel}</span>
            <span className="shrink-0 font-mono">#{r.id}</span>
            {loading && <span role="status" title="正在读取调用详情"><RefreshCw size={12} aria-hidden="true" className="animate-spin" /><span className="sr-only">正在读取调用详情</span></span>}
          </div>
          <button type="button" aria-expanded={argumentsOpen} aria-controls={argumentsId} onClick={() => setOpenArguments(!argumentsOpen)}
            className="glass-control ml-auto flex shrink-0 items-center gap-1 rounded-md px-2 py-1 hover:text-foreground">
            <Code2 size={12} aria-hidden="true" /> 原始参数
            <ChevronDown size={11} aria-hidden="true" className={argumentsOpen ? 'rotate-180' : ''} />
          </button>
        </div>
        {r.error_code && <p className="mt-1 flex items-center gap-1 break-all text-[11px] text-red-500"><AlertCircle size={12} aria-hidden="true" className="shrink-0" /> <span>{r.error_code} {r.error_category && `(${r.error_category})`}</span></p>}
        {notice && <p role="status" className="mt-1 text-[11px] text-muted-foreground">{notice}</p>}
        <div id={argumentsId} hidden={!argumentsOpen} className="relative">
          {argumentsOpen && (argumentsText ? (
            <>
              <CodeSource text={argumentsText} language="json" className={`${sourceClass} mt-2 max-h-64 rounded-lg bg-muted/30 py-2.5 ps-2.5 pe-10`} />
              <button type="button" onClick={async () => {
                try {
                  await navigator.clipboard.writeText(argumentsText);
                  setCopiedArguments(argumentsText);
                } catch { setCopiedArguments(undefined); }
              }} className="glass-control absolute top-2 right-2 rounded-md px-1.5 py-1 text-muted-foreground hover:text-foreground" title={copyLabel} aria-label={copyLabel}>
                {copiedArguments === argumentsText ? <Check size={12} aria-hidden="true" /> : <Copy size={12} aria-hidden="true" />}
              </button>
            </>
          ) : <p className="mt-2 text-[11px] text-muted-foreground">{loading ? '正在读取原始参数…' : request === 'failed' ? '未能读取原始参数。' : '此调用未返回原始参数。'}</p>)}
        </div>
      </div>

      {fileChanges?.length ? (
        // file_edit / file_write：直接渲染结构化 diff，不做 JSON 平铺。
        <div className="space-y-2">
          <div className="flex items-center gap-1.5 text-[11px] font-medium text-muted-foreground"><FilePenLine size={13} /> 文件变更（{fileChanges.length}）</div>
          {fileChanges.map((fc, i) => {
            const diff = traceChangeDiff(fc, fileDiff);
            return (
              <div key={i} className="rounded-xl border border-border/60 bg-card/40">
                <button onClick={() => setOpenFile(openFile === i ? -1 : i)} aria-expanded={openFile === i}
                  className="flex w-full items-center gap-2 p-3 text-left text-xs hover:bg-card/60">
                  <span className={`rounded px-1.5 py-0.5 text-[10px] ${fc.action === 'create' ? 'bg-sky-500/10 text-sky-500' : 'bg-violet-500/10 text-violet-500'}`}>{fc.action}</span>
                  <span className="min-w-0 flex-1 truncate font-mono" title={fc.path}>{shortPath(fc.path)}</span>
                  {(fc.insertions != null || fc.deletions != null) && (
                    <span className="flex shrink-0 gap-1.5 tabular text-[11px]" title="记录的增删行数">
                      <span className="text-emerald-500">+{fc.insertions ?? 0}</span>
                      <span className="text-red-500">−{fc.deletions ?? 0}</span>
                    </span>
                  )}
                </button>
                {openFile === i && (
                  <div className="border-t border-border/60 p-3">
                    {loading ? (
                      <p role="status" className="text-xs text-muted-foreground">{fc.truncated ? '正在读取完整 diff…' : '正在读取 diff…'}</p>
                    ) : (
                      <DiffViewer
                        diff={diff.text}
                        filePath={fc.path}
                        moveTo={fc.move_to}
                        insertions={fc.insertions}
                        deletions={fc.deletions}
                        truncated={diff.truncated}
                      />
                    )}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      ) : cmd && (hasCommandOutput || !resultText) ? (
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

      <details className="text-[11px] text-muted-foreground">
        <summary className="w-fit cursor-pointer rounded-md px-1 py-1 hover:text-foreground">
          <Info size={11} aria-hidden="true" className="mr-1 inline" />调用信息
        </summary>
        <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 px-1">
          {r.source && <span>来源 {r.source}</span>}
          <span>开始时间 <time dateTime={r.started_at}>{new Date(r.started_at).toLocaleString('zh-CN')}</time></span>
          <span>调用耗时 {ms(r.duration_ms)}</span>
        </div>
        {upstreamHeaders.length > 0 && (
          <div className="mt-1 flex flex-wrap items-center gap-1.5 px-1">
            <span>上游请求头</span>
            {upstreamHeaders.map(([name, value]) => (
              <span key={name} className="min-w-0 break-all rounded bg-muted/20 px-1.5 py-0.5 font-mono" title={`${name}: ${value}`}>
                {name}=<span className="text-foreground">{value}</span>
              </span>
            ))}
          </div>
        )}
      </details>
    </div>
  );
}
