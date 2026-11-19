import { useEffect, useMemo, useState } from 'react';
import {
  AlertCircle,
  Check,
  Code2,
  Copy,
  Download,
  ExternalLink,
  FileArchive,
  FileCode2,
  FileSpreadsheet,
  FileText,
  Image as ImageIcon,
  Music,
  RefreshCw,
  Table as TableIcon,
  WrapText,
} from 'lucide-react';
import { api, type ArtifactItem } from '../api';
import { bytes } from '../ui';
import { usePersistedState } from '../lib/usePersistedState';
import {
  classifyPreviewKind,
  formatJsonPreview,
  isLikelyBinaryBytes,
  parseDelimitedTable,
  type PreviewKind,
} from '../lib/filePreview';
import MarkdownView from './MarkdownView';

const PREVIEW_MAX_BYTES = 256 * 1024;
const SVG_MAX_BYTES = 512 * 1024;

function bytesToBase64(data: Uint8Array): string {
  const chunkSize = 0x8000;
  let binary = '';
  for (let i = 0; i < data.length; i += chunkSize) {
    binary += String.fromCharCode(...data.subarray(i, i + chunkSize));
  }
  return btoa(binary);
}

function CodeLines({ content, wrap }: { content: string; wrap: boolean }) {
  const lines = useMemo(() => {
    const split = content ? content.split(/\r?\n/) : [];
    return split.length > 1 && split[split.length - 1] === '' ? split.slice(0, -1) : split;
  }, [content]);
  const gutter = String(Math.max(lines.length, 1)).length;

  if (lines.length === 0) {
    return <div className="rounded-lg border border-border/60 bg-muted/20 p-3 text-xs text-muted-foreground">文件内容为空</div>;
  }

  return (
    <div className="scrollbar-thin max-h-[540px] overflow-auto rounded-lg border border-border/60 bg-muted/20 py-2">
      <div className={`${wrap ? 'w-full' : 'min-w-max'} font-mono text-[11.5px] leading-[1.6]`}>
        {lines.map((line, i) => (
          <div key={i} className="flex hover:bg-muted/25">
            <span
              style={{ width: `${gutter + 2}ch` }}
              className="shrink-0 select-none pr-3 text-right tabular text-muted-foreground/45"
            >
              {i + 1}
            </span>
            <span className={`${wrap ? 'min-w-0 flex-1 whitespace-pre-wrap break-all' : 'whitespace-pre'} pr-4`}>
              {line || ' '}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}

function CopyTextButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      onClick={() => {
        navigator.clipboard?.writeText(text);
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      }}
      className="glass-control flex items-center gap-1 rounded-md px-2 py-1 text-[11px] text-muted-foreground hover:text-foreground"
      title="复制预览内容"
    >
      {copied ? <Check size={11} className="text-emerald-500" /> : <Copy size={11} />}
      {copied ? '已复制' : '复制内容'}
    </button>
  );
}

function DownloadFallback({
  item,
  reason,
}: {
  item: ArtifactItem;
  reason: string;
}) {
  const downloadUrl = api.artifactContentUrl(item.artifact_id, true);
  return (
    <div className="flex flex-col items-center justify-center gap-3 rounded-xl border border-border/60 bg-muted/15 px-6 py-12 text-center">
      <FileArchive size={30} className="text-muted-foreground" />
      <div className="text-sm font-medium">{item.filename}</div>
      <div className="max-w-md text-xs text-muted-foreground">{reason}</div>
      <div className="font-mono text-[11px] text-muted-foreground/75">
        {item.mime_type || 'application/octet-stream'} · {bytes(item.size_bytes)}
      </div>
      <a
        href={downloadUrl}
        download={item.filename}
        className="glass-primary mt-1 inline-flex items-center gap-1.5 rounded-lg px-4 py-2 text-xs font-medium text-primary-foreground"
      >
        <Download size={13} /> 下载到本地
      </a>
    </div>
  );
}

type TextLoadState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'empty' }
  | { status: 'binary' }
  | { status: 'too_large' }
  | { status: 'error'; message: string }
  | { status: 'ready'; text: string; svgDataUrl?: string; truncated: boolean };

function shouldLoadText(kind: PreviewKind): boolean {
  return kind === 'svg' || kind === 'markdown' || kind === 'json' || kind === 'csv' || kind === 'code';
}

export default function ArtifactPreview({ item }: { item: ArtifactItem }) {
  const kind = useMemo(
    () => classifyPreviewKind(item.filename, item.mime_type, item.archive),
    [item.filename, item.mime_type, item.archive],
  );
  const contentUrl = api.artifactContentUrl(item.artifact_id);
  const downloadUrl = api.artifactContentUrl(item.artifact_id, true);

  const [imgStatus, setImgStatus] = useState<'loading' | 'ok' | 'error'>('loading');
  const [textState, setTextState] = useState<TextLoadState>({ status: 'idle' });
  const [viewMode, setViewMode] = usePersistedState<'rendered' | 'source'>('artifact-view-mode', 'rendered');
  const [wrapLines, setWrapLines] = useState(false);

  useEffect(() => {
    setImgStatus('loading');
    setViewMode('rendered');
    setWrapLines(false);

    if (!shouldLoadText(kind)) {
      setTextState({ status: 'idle' });
      return;
    }
    if (item.size_bytes === 0) {
      setTextState({ status: 'empty' });
      return;
    }
    if (kind === 'svg' && item.size_bytes > SVG_MAX_BYTES) {
      setTextState({ status: 'too_large' });
      return;
    }

    const ctrl = new AbortController();
    const limit = kind === 'svg' ? SVG_MAX_BYTES : PREVIEW_MAX_BYTES;
    const isPartialRequest = kind !== 'svg' && item.size_bytes > PREVIEW_MAX_BYTES;
    setTextState({ status: 'loading' });

    fetch(contentUrl, {
      cache: 'no-store',
      signal: ctrl.signal,
      headers: isPartialRequest ? { Range: `bytes=0-${limit - 1}` } : undefined,
    })
      .then(async (res) => {
        if (!res.ok && res.status !== 206) {
          let errMsg = `HTTP ${res.status}`;
          try {
            const payload = await res.json();
            if (payload?.error) errMsg = payload.error;
          } catch {
            /* ignore non-JSON error */
          }
          throw new Error(errMsg);
        }
        const buf = await res.arrayBuffer();
        let raw = new Uint8Array(buf);
        let truncated = res.status === 206 || isPartialRequest;
        if (raw.byteLength > limit) {
          raw = raw.subarray(0, limit);
          truncated = true;
        }
        if (isLikelyBinaryBytes(raw)) {
          if (!ctrl.signal.aborted) setTextState({ status: 'binary' });
          return;
        }
        let text = new TextDecoder('utf-8', { fatal: false }).decode(raw);
        if (truncated) {
          // 按字节切片可能把多字节 UTF-8 尾部劈开，剥掉末尾替换字符以免出现问号菱形。
          text = text.replace(/\uFFFD+$/, '');
        }
        const svgDataUrl =
          kind === 'svg' ? `data:image/svg+xml;base64,${bytesToBase64(raw)}` : undefined;
        if (!ctrl.signal.aborted) {
          setTextState({ status: 'ready', text, svgDataUrl, truncated });
        }
      })
      .catch((err: any) => {
        if (ctrl.signal.aborted || err?.name === 'AbortError') return;
        setTextState({ status: 'error', message: err?.message || String(err) });
      });

    return () => ctrl.abort();
  }, [item.artifact_id, item.size_bytes, kind, contentUrl]);

  if (kind === 'image') {
    return (
      <div className="space-y-2">
        <div className="flex items-center justify-between text-xs text-muted-foreground">
          <span className="flex items-center gap-1.5">
            <ImageIcon size={13} /> 图片预览
            {item.width && item.height ? ` · ${item.width} × ${item.height}` : ''}
          </span>
          <a
            href={contentUrl}
            target="_blank"
            rel="noopener noreferrer"
            className="glass-control inline-flex items-center gap-1 rounded-md px-2 py-1 text-[11px] hover:text-foreground"
          >
            <ExternalLink size={11} /> 原图打开
          </a>
        </div>
        {imgStatus === 'error' ? (
          <DownloadFallback item={item} reason="图片解码失败或浏览器不支持该图像格式，可下载到本地查看。" />
        ) : (
          <div className="relative flex min-h-[220px] items-center justify-center rounded-xl border border-border/60 bg-muted/20 p-3">
            {imgStatus === 'loading' && (
              <div className="absolute inset-0 flex items-center justify-center text-xs text-muted-foreground">
                <RefreshCw size={14} className="mr-2 animate-spin" /> 加载图片…
              </div>
            )}
            <a href={contentUrl} target="_blank" rel="noopener noreferrer" className="block max-w-full cursor-zoom-in" title="点击在新标签查看原图">
              <img
                key={item.artifact_id}
                src={contentUrl}
                alt={item.filename}
                onLoad={() => setImgStatus('ok')}
                onError={() => setImgStatus('error')}
                className="max-h-[540px] max-w-full rounded-lg object-contain"
              />
            </a>
          </div>
        )}
      </div>
    );
  }

  if (kind === 'pdf') {
    return (
      <div className="space-y-2">
        <div className="flex items-center justify-between text-xs text-muted-foreground">
          <span className="flex items-center gap-1.5"><FileText size={13} /> PDF 预览</span>
          <a
            href={contentUrl}
            target="_blank"
            rel="noopener noreferrer"
            className="glass-control inline-flex items-center gap-1 rounded-md px-2 py-1 text-[11px] hover:text-foreground"
          >
            <ExternalLink size={11} /> 新标签打开
          </a>
        </div>
        <iframe
          key={item.artifact_id}
          src={contentUrl}
          title={item.filename}
          className="h-[580px] w-full rounded-xl border border-border/60 bg-muted/20"
        />
      </div>
    );
  }

  if (kind === 'video') {
    return (
      <div className="space-y-2">
        <div className="text-xs text-muted-foreground">视频预览（支持进度条拖动）</div>
        <video
          key={item.artifact_id}
          controls
          preload="metadata"
          src={contentUrl}
          className="max-h-[540px] w-full rounded-xl border border-border/60 bg-black/50"
        />
      </div>
    );
  }

  if (kind === 'audio') {
    return (
      <div className="space-y-3 rounded-xl border border-border/60 bg-muted/15 p-5">
        <div className="flex items-center gap-3">
          <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-sky-500/15 text-sky-500">
            <Music size={18} />
          </div>
          <div className="min-w-0 flex-1">
            <div className="truncate text-sm font-medium">{item.filename}</div>
            <div className="text-xs text-muted-foreground">{item.mime_type} · {bytes(item.size_bytes)}</div>
          </div>
        </div>
        <audio key={item.artifact_id} controls preload="metadata" src={contentUrl} className="w-full" />
      </div>
    );
  }

  if (kind === 'binary') {
    const reason = item.archive
      ? '目录发布已打包为 .tar.gz 归档文件，请下载到本地后解压查看。'
      : '该文件为压缩包或二进制格式，不适合内联预览，请下载到本地打开。';
    return <DownloadFallback item={item} reason={reason} />;
  }

  if (textState.status === 'loading' || textState.status === 'idle') {
    return (
      <div className="flex items-center justify-center rounded-xl border border-border/60 bg-muted/15 py-16 text-xs text-muted-foreground">
        <RefreshCw size={14} className="mr-2 animate-spin" /> 正在读取文件内容…
      </div>
    );
  }

  if (textState.status === 'empty') {
    return (
      <div className="rounded-xl border border-border/60 bg-muted/15 p-8 text-center text-xs text-muted-foreground">
        文件内容为空（0 B）
      </div>
    );
  }

  if (textState.status === 'too_large') {
    return <DownloadFallback item={item} reason="SVG 矢量文件超过 512 KB 安全预览上限，请下载到本地查看。" />;
  }

  if (textState.status === 'binary') {
    return <DownloadFallback item={item} reason="检测到二进制字节内容，已阻止按文本渲染，请下载到本地查看。" />;
  }

  if (textState.status === 'error') {
    return (
      <div className="flex items-center gap-2 rounded-xl border border-red-500/30 bg-red-500/10 px-4 py-3 text-xs text-red-500">
        <AlertCircle size={14} /> 预览加载失败：{textState.message}
      </div>
    );
  }

  const { text, svgDataUrl, truncated } = textState;
  const isTsv = /\.tsv$/i.test(item.filename) || item.mime_type.toLowerCase().includes('tab-separated-values');

  if (kind === 'svg') {
    return (
      <div className="space-y-2">
        <div className="flex flex-wrap items-center gap-2">
          <button
            type="button"
            onClick={() => setViewMode('rendered')}
            className={`glass-control flex items-center gap-1 rounded-md px-2.5 py-1 text-xs ${viewMode === 'rendered' ? 'text-foreground' : 'text-muted-foreground'}`}
          >
            <ImageIcon size={12} /> 图形预览
          </button>
          <button
            type="button"
            onClick={() => setViewMode('source')}
            className={`glass-control flex items-center gap-1 rounded-md px-2.5 py-1 text-xs ${viewMode === 'source' ? 'text-foreground' : 'text-muted-foreground'}`}
          >
            <Code2 size={12} /> SVG 源码
          </button>
          <div className="ml-auto flex items-center gap-1.5">
            <CopyTextButton text={text} />
          </div>
        </div>
        {viewMode === 'rendered' && svgDataUrl ? (
          <div className="flex min-h-[220px] items-center justify-center rounded-xl border border-border/60 bg-muted/20 p-4">
            <img src={svgDataUrl} alt={item.filename} className="max-h-[520px] max-w-full object-contain" />
          </div>
        ) : (
          <CodeLines content={text} wrap={wrapLines} />
        )}
      </div>
    );
  }

  if (kind === 'markdown') {
    return (
      <div className="space-y-2">
        <div className="flex flex-wrap items-center gap-2">
          <button
            type="button"
            onClick={() => setViewMode('rendered')}
            className={`glass-control flex items-center gap-1 rounded-md px-2.5 py-1 text-xs ${viewMode === 'rendered' ? 'text-foreground' : 'text-muted-foreground'}`}
          >
            <FileText size={12} /> 渲染预览
          </button>
          <button
            type="button"
            onClick={() => setViewMode('source')}
            className={`glass-control flex items-center gap-1 rounded-md px-2.5 py-1 text-xs ${viewMode === 'source' ? 'text-foreground' : 'text-muted-foreground'}`}
          >
            <Code2 size={12} /> 源码
          </button>
          <div className="ml-auto flex items-center gap-1.5">
            {viewMode === 'source' && (
              <button
                type="button"
                onClick={() => setWrapLines((v) => !v)}
                className={`glass-control flex items-center gap-1 rounded-md px-2 py-1 text-[11px] ${wrapLines ? 'text-foreground' : 'text-muted-foreground'}`}
              >
                <WrapText size={11} /> 换行
              </button>
            )}
            <CopyTextButton text={text} />
          </div>
        </div>
        {truncated && (
          <div className="rounded-lg bg-amber-500/10 px-3 py-1.5 text-[11px] text-amber-600 dark:text-amber-400">
            仅预览前 256 KB 内容；完整文件请点击上方「本机下载」。
          </div>
        )}
        {viewMode === 'rendered' ? (
          <div className="scrollbar-thin max-h-[560px] overflow-auto rounded-xl border border-border/60 bg-muted/20 p-4">
            <MarkdownView content={text} />
          </div>
        ) : (
          <CodeLines content={text} wrap={wrapLines} />
        )}
      </div>
    );
  }

  if (kind === 'json') {
    const parsed = formatJsonPreview(text, item.filename);
    const activeText = viewMode === 'rendered' && parsed.valid ? parsed.formatted : text;
    return (
      <div className="space-y-2">
        <div className="flex flex-wrap items-center gap-2">
          {parsed.valid && (
            <>
              <button
                type="button"
                onClick={() => setViewMode('rendered')}
                className={`glass-control flex items-center gap-1 rounded-md px-2.5 py-1 text-xs ${viewMode === 'rendered' ? 'text-foreground' : 'text-muted-foreground'}`}
              >
                <FileCode2 size={12} /> 格式化
              </button>
              <button
                type="button"
                onClick={() => setViewMode('source')}
                className={`glass-control flex items-center gap-1 rounded-md px-2.5 py-1 text-xs ${viewMode === 'source' ? 'text-foreground' : 'text-muted-foreground'}`}
              >
                <Code2 size={12} /> 原始文本
              </button>
            </>
          )}
          <div className="ml-auto flex items-center gap-1.5">
            <button
              type="button"
              onClick={() => setWrapLines((v) => !v)}
              className={`glass-control flex items-center gap-1 rounded-md px-2 py-1 text-[11px] ${wrapLines ? 'text-foreground' : 'text-muted-foreground'}`}
            >
              <WrapText size={11} /> 换行
            </button>
            <CopyTextButton text={activeText} />
          </div>
        </div>
        {(truncated || !parsed.valid) && (
          <div className="rounded-lg bg-amber-500/10 px-3 py-1.5 text-[11px] text-amber-600 dark:text-amber-400">
            {truncated
              ? '文件超过 256 KB，仅展示前缀片段；已回退为原始文本。'
              : 'JSON 解析未完整通过（可能含注释或片段），以原始文本展示。'}
          </div>
        )}
        <CodeLines content={activeText} wrap={wrapLines} />
      </div>
    );
  }

  if (kind === 'csv') {
    const table = parseDelimitedTable(text, isTsv ? '\t' : ',', 200);
    return (
      <div className="space-y-2">
        <div className="flex flex-wrap items-center gap-2">
          <button
            type="button"
            onClick={() => setViewMode('rendered')}
            className={`glass-control flex items-center gap-1 rounded-md px-2.5 py-1 text-xs ${viewMode === 'rendered' ? 'text-foreground' : 'text-muted-foreground'}`}
          >
            <TableIcon size={12} /> 表格预览
          </button>
          <button
            type="button"
            onClick={() => setViewMode('source')}
            className={`glass-control flex items-center gap-1 rounded-md px-2.5 py-1 text-xs ${viewMode === 'source' ? 'text-foreground' : 'text-muted-foreground'}`}
          >
            <Code2 size={12} /> 原始文本
          </button>
          {table.headers.length > 0 && (
            <span className="text-[11px] text-muted-foreground">
              <FileSpreadsheet size={11} className="mr-1 inline" />
              {table.headers.length} 列 · {table.totalRows} 行{table.truncated ? '（显示前 200 行）' : ''}
            </span>
          )}
          <div className="ml-auto flex items-center gap-1.5">
            <CopyTextButton text={text} />
          </div>
        </div>
        {truncated && (
          <div className="rounded-lg bg-amber-500/10 px-3 py-1.5 text-[11px] text-amber-600 dark:text-amber-400">
            仅读取前 256 KB 数据；完整表格请下载文件查看。
          </div>
        )}
        {viewMode === 'rendered' && table.headers.length > 0 ? (
          <div className="scrollbar-thin max-h-[540px] overflow-auto rounded-xl border border-border/60 bg-muted/15">
            <table className="w-full border-collapse text-left text-xs">
              <thead className="sticky top-0 z-10 bg-card/95 backdrop-blur">
                <tr className="border-b border-border/60">
                  <th className="w-10 border-r border-border/40 px-2.5 py-2 text-right font-mono text-[10px] text-muted-foreground">#</th>
                  {table.headers.map((h, i) => (
                    <th key={i} className="whitespace-nowrap border-r border-border/40 px-3 py-2 font-semibold last:border-r-0">
                      {h || `列 ${i + 1}`}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {table.rows.map((row, rIdx) => (
                  <tr key={rIdx} className="border-b border-border/40 hover:bg-muted/25 last:border-b-0">
                    <td className="border-r border-border/40 px-2.5 py-1.5 text-right font-mono text-[10px] text-muted-foreground/60">
                      {rIdx + 1}
                    </td>
                    {table.headers.map((_, cIdx) => (
                      <td key={cIdx} className="max-w-[280px] whitespace-pre-wrap break-words border-r border-border/40 px-3 py-1.5 font-mono text-[11px] last:border-r-0">
                        {row[cIdx] ?? ''}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <CodeLines content={text} wrap={wrapLines} />
        )}
      </div>
    );
  }

  // 默认代码 / 纯文本 / 配置 / 日志预览
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <span className="flex items-center gap-1.5">
          <FileCode2 size={13} /> 文本 / 代码预览
        </span>
        <div className="ml-auto flex items-center gap-1.5">
          <button
            type="button"
            onClick={() => setWrapLines((v) => !v)}
            className={`glass-control flex items-center gap-1 rounded-md px-2 py-1 text-[11px] ${wrapLines ? 'text-foreground' : 'text-muted-foreground'}`}
          >
            <WrapText size={11} /> 换行
          </button>
          <CopyTextButton text={text} />
        </div>
      </div>
      {truncated && (
        <div className="rounded-lg bg-amber-500/10 px-3 py-1.5 text-[11px] text-amber-600 dark:text-amber-400">
          仅预览前 256 KB 内容；完整文件请点击「本机下载」。
        </div>
      )}
      <CodeLines content={text} wrap={wrapLines} />
      <div className="text-right text-[11px] text-muted-foreground/70">
        如需本地打开完整原始文件，可使用{' '}
        <a href={downloadUrl} download={item.filename} className="text-sky-500 hover:underline">
          下载副本
        </a>
      </div>
    </div>
  );
}
