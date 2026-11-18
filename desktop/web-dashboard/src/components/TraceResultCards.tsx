import { useEffect, useState, type ReactNode } from 'react';
import { FileText, Image as ImageIcon, AlertTriangle } from 'lucide-react';
import { api, type TraceRecord } from '../api';
import { bytes } from '../ui';

/** read_file 结果（internal/tool/file/runtime_files.go）。 */
export type ReadFileResult = {
  path?: string;
  content?: string;
  start_line?: number;
  end_line?: number;
  total_lines?: number;
  size_bytes?: number;
  truncated?: boolean;
  next_start_line?: number;
  truncated_reason?: string;
};

/** view_image 结果（internal/tool/media/view_image.go）；base64 已从 trace 里剥离。 */
type ImageMetadata = { filename?: string; mime_type?: string; size_bytes?: number; width?: number; height?: number };
type ImageSource = { type?: string; path?: string; url?: string; artifact_id?: string; filename?: string; size_bytes?: number };
export type ViewImageResult = {
  source?: ImageSource;
  image?: ImageMetadata;
  original?: ImageMetadata;
  resized?: boolean;
  warnings?: string[];
  image_bytes_omitted?: number;
};

/** 预览被截断时 JSON 不再合法，解析失败即回退通用渲染，不猜半个对象。 */
export function parseTraceResult<T>(text?: string): T | null {
  if (!text) return null;
  try {
    const parsed = JSON.parse(text);
    return parsed && typeof parsed === 'object' ? (parsed as T) : null;
  } catch {
    return null;
  }
}

function MetaBadge({ children }: { children: ReactNode }) {
  return <span className="rounded bg-muted/20 px-1.5 py-0.5 text-[10px] text-muted-foreground">{children}</span>;
}

function MetadataRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline gap-2 text-[11px]">
      <span className="w-14 shrink-0 text-muted-foreground">{label}</span>
      <span className="min-w-0 flex-1 break-all font-mono">{value}</span>
    </div>
  );
}

/** read_file 详情：路径 + 行范围 + 带行号的正文，替代裸 JSON。 */
export function ReadFileCard({ data }: { data: ReadFileResult }) {
  const content = data.content ?? '';
  const start = Math.max(1, data.start_line ?? 1);
  const lines = content ? content.split('\n') : [];
  // 末尾空行只来自文件结尾换行，不占一条行号，否则总行数会虚高一行。
  const shown = lines.length && lines[lines.length - 1] === '' ? lines.slice(0, -1) : lines;
  const gutter = String(start + Math.max(shown.length, 1) - 1).length;

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-1.5">
        <FileText size={13} className="text-muted-foreground" />
        <span className="min-w-0 break-all font-mono text-xs" title={data.path}>{data.path || '未知路径'}</span>
        {shown.length > 0 && <MetaBadge>{`L${start}–L${start + shown.length - 1}`}</MetaBadge>}
        {data.total_lines != null && <MetaBadge>{`共 ${data.total_lines} 行`}</MetaBadge>}
        {data.size_bytes != null && <MetaBadge>{bytes(data.size_bytes)}</MetaBadge>}
        {data.truncated && (
          <span className="rounded bg-amber-500/10 px-1.5 py-0.5 text-[10px] text-amber-600 dark:text-amber-400">
            已截断{data.next_start_line ? ` · 从 L${data.next_start_line} 继续` : ''}
          </span>
        )}
      </div>
      {shown.length > 0 ? (
        <div className="scrollbar-thin max-h-[520px] overflow-auto rounded-lg border border-border/60 bg-muted/20 py-1.5">
          <div className="min-w-max font-mono text-[11px] leading-[1.55]">
            {shown.map((line, i) => (
              <div key={i} className="flex">
                <span style={{ width: `${gutter + 2}ch` }} className="shrink-0 select-none pr-3 text-right tabular text-muted-foreground/40">{start + i}</span>
                <span className="whitespace-pre pr-3">{line || ' '}</span>
              </div>
            ))}
          </div>
        </div>
      ) : (
        <div className="rounded-lg border border-border/60 bg-muted/20 p-3 text-[11px] text-muted-foreground">文件内容为空</div>
      )}
    </div>
  );
}

/** view_image 详情：图片字节走私有 sidecar，打开详情时才拉取。 */
export function ViewImageCard({ traceId, data, preview }: { traceId: number; data: ViewImageResult; preview?: TraceRecord['image_preview'] }) {
  const [src, setSrc] = useState<string | null>(null);
  const [unavailable, setUnavailable] = useState(false);
  const mime = data.image?.mime_type || preview?.mime_type || '';
  const previewSize = preview?.size_bytes;

  useEffect(() => {
    if (!preview) {
      setUnavailable(true);
      return;
    }
    let alive = true;
    setUnavailable(false);
    api
      .traceImage(traceId)
      .then((res) => {
        if (alive) setSrc(`data:${res.mime_type};base64,${res.data}`);
      })
      .catch(() => {
        if (alive) setUnavailable(true);
      });
    return () => {
      alive = false;
    };
  }, [traceId, previewSize, mime]);

  const source = data.source;
  const sourceValue = [source?.path, source?.url, source?.artifact_id, source?.filename, source?.type].find((v) => v && v.trim());

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-1.5">
        <ImageIcon size={13} className="text-muted-foreground" />
        <span className="text-xs font-medium">{data.image?.filename || '图片'}</span>
        {mime && <MetaBadge>{mime}</MetaBadge>}
        {data.image?.width && data.image?.height ? <MetaBadge>{`${data.image.width} × ${data.image.height}`}</MetaBadge> : null}
        {data.image?.size_bytes ? <MetaBadge>{bytes(data.image.size_bytes)}</MetaBadge> : null}
        {data.resized && <span className="rounded bg-amber-500/10 px-1.5 py-0.5 text-[10px] text-amber-600 dark:text-amber-400">已缩放</span>}
      </div>

      {src ? (
        <button onClick={() => window.open(src, '_blank', 'noopener')} className="block w-full cursor-zoom-in" title="点击查看原图">
          <img src={src} alt={data.image?.filename || 'view_image 结果'} className="max-h-[520px] max-w-full rounded-lg border border-border/60 bg-muted/20 object-contain" />
        </button>
      ) : (
        <div className="rounded-lg border border-border/60 bg-muted/20 p-3 text-[11px] text-muted-foreground">
          {unavailable
            ? `图片字节不在调用追踪里${data.image_bytes_omitted ? `（原始 ${bytes(data.image_bytes_omitted)} 已省略）` : ''}`
            : '正在读取图片…'}
        </div>
      )}

      {sourceValue && <MetadataRow label="来源" value={sourceValue} />}
      {data.original && data.resized && (
        <MetadataRow
          label="原始"
          value={[data.original.width && data.original.height ? `${data.original.width} × ${data.original.height}` : '', data.original.size_bytes ? bytes(data.original.size_bytes) : ''].filter(Boolean).join(' · ') || '-'}
        />
      )}
      {data.warnings?.map((warning) => (
        <div key={warning} className="flex items-start gap-1.5 text-[11px] text-amber-600 dark:text-amber-400">
          <AlertTriangle size={11} className="mt-0.5 shrink-0" />
          <span className="min-w-0 break-all">{warning}</span>
        </div>
      ))}
    </div>
  );
}
