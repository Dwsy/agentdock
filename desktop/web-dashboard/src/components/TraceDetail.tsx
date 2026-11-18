import { useEffect, useState } from 'react';
import { FileJson, FileText, Code2, RefreshCw, AlertCircle, FilePenLine, Tag } from 'lucide-react';
import { api, type TraceRecord, type TraceDetail as TraceDetailData } from '../api';
import { ms, shortPath } from '../ui';
import CommandBlock from './CommandBlock';
import MarkdownView from './MarkdownView';
import DiffViewer from './DiffViewer';
import { ReadFileCard, ViewImageCard, parseTraceResult, type ReadFileResult, type ViewImageResult } from './TraceResultCards';

function pretty(s?: string): string {
  if (!s) return '';
  try { return JSON.stringify(JSON.parse(s), null, 2); } catch { return s; }
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

/** 单条调用详情：优先用详情接口拉全文，失败则退回列表记录。 */
export default function TraceDetail({ record }: { record: TraceRecord }) {
  const [detail, setDetail] = useState<TraceDetailData | null>(null);
  const [loading, setLoading] = useState(true);
  const [view, setView] = useState<'auto' | 'source'>('auto');
  const [openFile, setOpenFile] = useState<number>(0);

  useEffect(() => {
    let alive = true;
    setLoading(true);
    setDetail(null);
    // read_file 的正文常超过预览上限（8 KiB），被截断后 JSON 不再合法，
    // 卡片就解析不出来。这类工具直接要旁路全文；小记录没有旁路时自动退回预览。
    api.traceDetail(record.id, record.tool === 'read_file').then((d) => { if (alive) setDetail(d); }).catch(() => {}).finally(() => { if (alive) setLoading(false); });
    return () => { alive = false; };
  }, [record.id, record.tool]);

  const r = detail?.record ?? record;
  const full = detail?.full;
  const cmd = r.command || full?.command;
  const argumentsText = full?.arguments || r.arguments;
  const resultText = full?.result || r.result;
  const mdFromResult = extractMarkdown(resultText) || extractMarkdown(r.result);
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
              {openFile === i && <div className="border-t border-border/60 p-3"><DiffViewer diff={fc.diff || ''} filePath={fc.path} /></div>}
            </div>
          ))}
        </div>
      ) : cmd ? (
        <CommandBlock record={{ ...r, command: cmd }} />
      ) : (
        <>
          {readFileResult && <ReadFileCard data={readFileResult} />}
          {viewImageResult && <ViewImageCard traceId={r.id} data={viewImageResult} preview={r.image_preview} />}
          {argumentsText && (
            <div>
              <div className="mb-1 flex items-center gap-1 text-[11px] font-medium text-muted-foreground"><Code2 size={12} /> 参数</div>
              <pre className="scrollbar-thin max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-muted/30 p-2.5 font-mono text-[11px] leading-relaxed">{pretty(argumentsText)}</pre>
            </div>
          )}
          {hasTypedCard && resultText && (
            <details className="rounded-lg border border-border/60 bg-muted/20 px-2.5 py-1.5">
              <summary className="cursor-pointer text-[11px] text-muted-foreground">原始结果 JSON</summary>
              <pre className="scrollbar-thin mt-1.5 max-h-64 overflow-auto whitespace-pre-wrap break-all font-mono text-[11px] leading-relaxed">{pretty(resultText)}</pre>
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
                <pre className="scrollbar-thin max-h-[520px] overflow-auto whitespace-pre-wrap break-all rounded-lg bg-muted/30 p-2.5 font-mono text-[11px] leading-relaxed">{pretty(resultText)}</pre>
              )}
            </div>
          )}
        </>
      )}
      {detail?.full_available && !full && <div className="text-[11px] text-muted-foreground">完整正文可用但旁路未返回，展示的是预览。</div>}
    </div>
  );
}
