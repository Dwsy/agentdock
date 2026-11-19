import { useEffect, useMemo, useRef, useState } from 'react';
import {
  AlertTriangle,
  Check,
  CheckCircle2,
  Clock3,
  Copy,
  Download,
  ExternalLink,
  FileArchive,
  FileCode2,
  FileText,
  FolderInput,
  Image as ImageIcon,
  Link2,
  Music,
  Plus,
  RefreshCw,
  Search,
  Share2,
  Trash2,
  Upload,
  Video,
  X,
} from 'lucide-react';
import { api, type ArtifactItem, type Artifacts as ArtifactsData } from '../api';
import { Badge, EmptyState, Panel, SectionTitle, bytes, relTime } from '../ui';
import { usePersistedState } from '../lib/usePersistedState';
import ArtifactPreview from '../components/ArtifactPreview';
import {
  classifyPreviewKind,
  previewCategory,
  remainingTimeLabel,
  type PreviewCategory,
} from '../lib/filePreview';

const MAX_UPLOAD_BYTES = 20 * 1024 * 1024;

const RETENTION_OPTIONS = [
  { label: '6 小时', seconds: 6 * 3600 },
  { label: '24 小时（默认）', seconds: 24 * 3600 },
  { label: '72 小时', seconds: 72 * 3600 },
  { label: '7 天（上限）', seconds: 7 * 24 * 3600 },
] as const;

type CategoryFilter = 'all' | PreviewCategory;

const CATEGORY_TABS: { key: CategoryFilter; label: string }[] = [
  { key: 'all', label: '全部' },
  { key: 'image', label: '图片' },
  { key: 'doc', label: '文档与代码' },
  { key: 'media', label: '音视频' },
  { key: 'other', label: '归档与其它' },
];

function KindIcon({ item, size = 15 }: { item: ArtifactItem; size?: number }) {
  const kind = classifyPreviewKind(item.filename, item.mime_type, item.archive);
  switch (kind) {
    case 'image':
    case 'svg':
      return <ImageIcon size={size} className="shrink-0 text-sky-500" />;
    case 'video':
      return <Video size={size} className="shrink-0 text-violet-500" />;
    case 'audio':
      return <Music size={size} className="shrink-0 text-emerald-500" />;
    case 'binary':
      return <FileArchive size={size} className="shrink-0 text-amber-500" />;
    case 'code':
    case 'json':
    case 'csv':
      return <FileCode2 size={size} className="shrink-0 text-indigo-400" />;
    default:
      return <FileText size={size} className="shrink-0 text-muted-foreground" />;
  }
}

function readFileAsBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const result = typeof reader.result === 'string' ? reader.result : '';
      const comma = result.indexOf(',');
      if (comma < 0) {
        reject(new Error('无法解析上传文件编码'));
        return;
      }
      resolve(result.slice(comma + 1));
    };
    reader.onerror = () => reject(new Error('读取本地文件失败'));
    reader.readAsDataURL(file);
  });
}

function formatDateTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '-';
  return d.toLocaleString('zh-CN');
}

export default function PublishedPage({
  data,
  onRefresh,
}: {
  data: ArtifactsData;
  onRefresh: () => void | Promise<void>;
}) {
  const [q, setQ] = useState('');
  const [category, setCategory] = usePersistedState<CategoryFilter>('published-category', 'all');
  const [selectedId, setSelectedId] = useState<string | null>(null);

  // 主动发布面板状态
  const [showPublish, setShowPublish] = useState(false);
  const [publishMode, setPublishMode] = useState<'path' | 'upload'>('path');
  const [localPath, setLocalPath] = useState('');
  const [uploadFile, setUploadFile] = useState<File | null>(null);
  const [dragActive, setDragActive] = useState(false);
  const [retentionSec, setRetentionSec] = useState<number>(24 * 3600);
  const [publishing, setPublishing] = useState(false);
  const [publishFeedback, setPublishFeedback] = useState<{
    type: 'ok' | 'err';
    text: string;
    url?: string;
  } | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  // 单条管理操作状态
  const [extendHours, setExtendHours] = useState('24');
  const [extending, setExtending] = useState(false);
  const [confirmRevokeId, setConfirmRevokeId] = useState<string | null>(null);
  const [revoking, setRevoking] = useState(false);
  const [actionFeedback, setActionFeedback] = useState<{ type: 'ok' | 'err'; text: string } | null>(null);
  const [copiedKey, setCopiedKey] = useState<string | null>(null);

  const items = data.artifacts ?? [];

  const categoryCounts = useMemo(() => {
    const counts: Record<CategoryFilter, number> = { all: items.length, image: 0, doc: 0, media: 0, other: 0 };
    for (const item of items) {
      const cat = previewCategory(classifyPreviewKind(item.filename, item.mime_type, item.archive));
      counts[cat] += 1;
    }
    return counts;
  }, [items]);

  const filtered = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return items.filter((item) => {
      if (category !== 'all') {
        const cat = previewCategory(classifyPreviewKind(item.filename, item.mime_type, item.archive));
        if (cat !== category) return false;
      }
      if (!needle) return true;
      return (
        item.filename.toLowerCase().includes(needle) ||
        item.mime_type.toLowerCase().includes(needle) ||
        item.artifact_id.toLowerCase().includes(needle)
      );
    });
  }, [items, category, q]);

  const selected = useMemo(
    () => filtered.find((item) => item.artifact_id === selectedId) || filtered[0] || null,
    [filtered, selectedId],
  );

  useEffect(() => {
    setConfirmRevokeId(null);
    setActionFeedback(null);
  }, [selected?.artifact_id]);

  const copyValue = (key: string, value: string) => {
    navigator.clipboard?.writeText(value);
    setCopiedKey(key);
    setTimeout(() => setCopiedKey((cur) => (cur === key ? null : cur)), 1500);
  };

  const handlePickFile = (file: File | null) => {
    if (!file) return;
    if (file.size === 0) {
      setPublishFeedback({ type: 'err', text: '空文件（0 B）暂不支持通过上传发布' });
      setUploadFile(null);
      return;
    }
    if (file.size > MAX_UPLOAD_BYTES) {
      setPublishFeedback({ type: 'err', text: `文件超过 20 MiB 上传上限（当前 ${bytes(file.size)}），请改用「本机路径发布」` });
      setUploadFile(null);
      return;
    }
    setPublishFeedback(null);
    setUploadFile(file);
  };

  const submitPublish = async (e: React.FormEvent) => {
    e.preventDefault();
    setPublishFeedback(null);
    setPublishing(true);
    try {
      let res;
      if (publishMode === 'path') {
        const trimmed = localPath.trim();
        if (!trimmed) {
          throw new Error('请输入本机文件或目录路径');
        }
        res = await api.artifactPublish({ path: trimmed, retention_seconds: retentionSec });
        setLocalPath('');
      } else {
        if (!uploadFile) {
          throw new Error('请先选择或拖入要发布的文件');
        }
        if (uploadFile.size === 0) {
          throw new Error('空文件（0 B）无法上传发布');
        }
        if (uploadFile.size > MAX_UPLOAD_BYTES) {
          throw new Error('文件超过 20 MiB 限制，请改用本机路径发布');
        }
        const data_base64 = await readFileAsBase64(uploadFile);
        res = await api.artifactPublish({
          filename: uploadFile.name,
          mime_type: uploadFile.type || undefined,
          data_base64,
          retention_seconds: retentionSec,
        });
        setUploadFile(null);
        if (fileInputRef.current) fileInputRef.current.value = '';
      }
      if (res.artifact?.artifact_id) {
        setSelectedId(res.artifact.artifact_id);
      }
      setPublishFeedback({
        type: 'ok',
        text: `已发布 ${res.artifact?.filename || '文件'}`,
        url: res.artifact?.url || undefined,
      });
      await onRefresh();
    } catch (err: any) {
      setPublishFeedback({ type: 'err', text: `发布失败：${err?.message || err}` });
    } finally {
      setPublishing(false);
    }
  };

  const handleExtend = async () => {
    if (!selected) return;
    const parsed = Number.parseInt(extendHours.trim(), 10);
    if (!Number.isFinite(parsed) || parsed < 1 || parsed > 720 || String(parsed) !== extendHours.trim()) {
      setActionFeedback({ type: 'err', text: '延期小时数须为 1 到 720 之间的整数' });
      return;
    }
    setExtending(true);
    setActionFeedback(null);
    try {
      await api.artifactExtend(selected.artifact_id, parsed);
      setActionFeedback({ type: 'ok', text: `已延长 ${parsed} 小时（受 7 天最长保留期上限约束），签名链接已更新` });
      await onRefresh();
    } catch (err: any) {
      setActionFeedback({ type: 'err', text: `延期失败：${err?.message || err}` });
    } finally {
      setExtending(false);
    }
  };

  const handleRevoke = async (id: string) => {
    setRevoking(true);
    setActionFeedback(null);
    try {
      await api.artifactRevoke(id);
      setConfirmRevokeId(null);
      if (selectedId === id) setSelectedId(null);
      setActionFeedback({ type: 'ok', text: '发布已撤销，公网下载链接已立即失效' });
      await onRefresh();
    } catch (err: any) {
      setActionFeedback({ type: 'err', text: `撤销失败：${err?.message || err}` });
    } finally {
      setRevoking(false);
    }
  };

  return (
    <div className="space-y-4">
      <Panel className="p-5">
        <SectionTitle
          icon={Share2}
          title="文件发布"
          hint={`${data.count} 个有效产物${data.public_base_url ? ` · ${data.public_base_url}` : ''}`}
          action={
            <div className="flex flex-wrap items-center gap-2">
              <div className="glass-control flex items-center gap-2 rounded-lg px-3 py-1.5">
                <Search size={14} className="text-muted-foreground" />
                <input
                  value={q}
                  onChange={(e) => setQ(e.target.value)}
                  placeholder="搜索文件名 / MIME / ID…"
                  className="w-44 bg-transparent text-xs outline-none placeholder:text-muted-foreground"
                />
              </div>
              <button
                type="button"
                onClick={() => setShowPublish((v) => !v)}
                className={`flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium ${
                  showPublish ? 'glass-control text-foreground' : 'glass-primary text-primary-foreground'
                }`}
              >
                {showPublish ? <X size={13} /> : <Plus size={13} />}
                {showPublish ? '收起发布' : '发布文件'}
              </button>
            </div>
          }
        />

        {!data.configured && (
          <div className="mb-4 flex items-center gap-2 rounded-xl border border-amber-500/30 bg-amber-500/10 px-3.5 py-2.5 text-xs text-amber-600 dark:text-amber-400">
            <AlertTriangle size={14} className="shrink-0" />
            <span>
              未配置公网基址（<code className="rounded bg-amber-500/15 px-1 font-mono">AGENTDOCK_SERVER_URL</code>），暂不生成公网签名链接；本机预览、管理与下载仍可正常使用。
            </span>
          </div>
        )}

        {showPublish && (
          <form onSubmit={submitPublish} className="mb-4 space-y-3 rounded-xl border border-border/60 bg-card/40 p-4">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="flex gap-1.5">
                <button
                  type="button"
                  onClick={() => { setPublishMode('path'); setPublishFeedback(null); }}
                  className={`glass-control flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs ${
                    publishMode === 'path' ? 'glass-chosen text-foreground' : 'text-muted-foreground'
                  }`}
                >
                  <FolderInput size={13} /> 本机路径发布
                </button>
                <button
                  type="button"
                  onClick={() => { setPublishMode('upload'); setPublishFeedback(null); }}
                  className={`glass-control flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs ${
                    publishMode === 'upload' ? 'glass-chosen text-foreground' : 'text-muted-foreground'
                  }`}
                >
                  <Upload size={13} /> 上传文件发布
                </button>
              </div>
              <div className="flex items-center gap-2 text-xs">
                <span className="text-muted-foreground">有效期</span>
                <select
                  value={retentionSec}
                  onChange={(e) => setRetentionSec(Number(e.target.value))}
                  className="glass-control rounded-md px-2.5 py-1 text-xs outline-none"
                >
                  {RETENTION_OPTIONS.map((opt) => (
                    <option key={opt.seconds} value={opt.seconds}>{opt.label}</option>
                  ))}
                </select>
              </div>
            </div>

            {publishMode === 'path' ? (
              <div className="flex flex-col gap-2 sm:flex-row">
                <input
                  value={localPath}
                  onChange={(e) => setLocalPath(e.target.value)}
                  placeholder="输入本机文件或目录路径（如 ~/Desktop/report.pdf、./dist 或绝对路径，目录自动打包 .tar.gz）"
                  className="glass-control min-w-0 flex-1 rounded-lg px-3 py-2 font-mono text-xs outline-none placeholder:text-muted-foreground"
                />
                <button
                  type="submit"
                  disabled={publishing || !localPath.trim()}
                  className="glass-primary flex shrink-0 items-center justify-center gap-1.5 rounded-lg px-4 py-2 text-xs font-medium text-primary-foreground disabled:opacity-40"
                >
                  {publishing ? <RefreshCw size={13} className="animate-spin" /> : <Share2 size={13} />}
                  {publishing ? '发布中…' : '立即发布'}
                </button>
              </div>
            ) : (
              <div className="space-y-2.5">
                <input
                  ref={fileInputRef}
                  type="file"
                  className="hidden"
                  onChange={(e) => handlePickFile(e.target.files?.[0] ?? null)}
                />
                <div
                  onDragOver={(e) => { e.preventDefault(); setDragActive(true); }}
                  onDragLeave={() => setDragActive(false)}
                  onDrop={(e) => {
                    e.preventDefault();
                    setDragActive(false);
                    handlePickFile(e.dataTransfer.files?.[0] ?? null);
                  }}
                  onClick={() => fileInputRef.current?.click()}
                  className={`flex cursor-pointer flex-col items-center justify-center gap-1.5 rounded-xl border border-dashed px-4 py-5 text-center transition-colors ${
                    dragActive ? 'border-sky-500 bg-sky-500/10' : 'border-border/70 bg-muted/15 hover:bg-muted/25'
                  }`}
                >
                  <Upload size={18} className="text-muted-foreground" />
                  {uploadFile ? (
                    <div className="text-xs">
                      <span className="font-medium text-foreground">{uploadFile.name}</span>
                      <span className="ml-2 text-muted-foreground">{bytes(uploadFile.size)}</span>
                    </div>
                  ) : (
                    <div className="text-xs text-muted-foreground">
                      点击选择文件或拖拽到此处（单文件最大 20 MiB；更大文件或目录请用「本机路径发布」）
                    </div>
                  )}
                </div>
                <div className="flex justify-end">
                  <button
                    type="submit"
                    disabled={publishing || !uploadFile}
                    className="glass-primary flex items-center gap-1.5 rounded-lg px-4 py-2 text-xs font-medium text-primary-foreground disabled:opacity-40"
                  >
                    {publishing ? <RefreshCw size={13} className="animate-spin" /> : <Upload size={13} />}
                    {publishing ? '上传发布中…' : '上传并发布'}
                  </button>
                </div>
              </div>
            )}

            {publishFeedback && (
              <div
                className={`flex flex-wrap items-center gap-2 rounded-lg px-3 py-2 text-xs ${
                  publishFeedback.type === 'ok' ? 'bg-emerald-500/10 text-emerald-500' : 'bg-red-500/10 text-red-500'
                }`}
              >
                {publishFeedback.type === 'ok' ? <CheckCircle2 size={14} /> : <AlertTriangle size={14} />}
                <span className="min-w-0 flex-1">{publishFeedback.text}</span>
                {publishFeedback.url && (
                  <button
                    type="button"
                    onClick={() => copyValue('publish-banner-url', publishFeedback.url!)}
                    className="glass-control flex items-center gap-1 rounded-md px-2 py-1 text-[11px] text-foreground"
                  >
                    {copiedKey === 'publish-banner-url' ? <Check size={11} /> : <Copy size={11} />}
                    {copiedKey === 'publish-banner-url' ? '已复制链接' : '复制公网链接'}
                  </button>
                )}
              </div>
            )}
          </form>
        )}

        <div className="grid gap-4 lg:grid-cols-[340px_1fr]">
          {/* 左侧：筛选与发布列表 */}
          <div className="rounded-xl border border-border/60 bg-muted/10 p-2.5">
            <div className="mb-2.5 flex flex-wrap gap-1">
              {CATEGORY_TABS.map((tab) => {
                const count = categoryCounts[tab.key];
                const active = category === tab.key;
                return (
                  <button
                    key={tab.key}
                    type="button"
                    onClick={() => setCategory(tab.key)}
                    className={`glass-control flex items-center gap-1 rounded-md px-2 py-1 text-[11px] ${
                      active ? 'glass-chosen text-foreground' : 'text-muted-foreground'
                    }`}
                  >
                    <span>{tab.label}</span>
                    <span className="tabular text-[10px] opacity-65">{count}</span>
                  </button>
                );
              })}
            </div>

            <div className="scrollbar-thin max-h-[680px] space-y-1 overflow-y-auto pr-0.5">
              {filtered.map((item) => {
                const active = selected?.artifact_id === item.artifact_id;
                const rem = remainingTimeLabel(item.expires_at);
                return (
                  <button
                    key={item.artifact_id}
                    type="button"
                    onClick={() => setSelectedId(item.artifact_id)}
                    className={`flex w-full items-start gap-2.5 rounded-lg p-2.5 text-left transition-colors ${
                      active ? 'glass-chosen' : 'hover:bg-card/60'
                    }`}
                  >
                    <div className="mt-0.5">
                      <KindIcon item={item} size={15} />
                    </div>
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-1.5">
                        <span className="min-w-0 truncate text-xs font-medium text-foreground" title={item.filename}>
                          {item.filename}
                        </span>
                        {item.archive && (
                          <Badge className="shrink-0 bg-amber-500/10 text-amber-500">归档</Badge>
                        )}
                      </div>
                      <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[10.5px] text-muted-foreground">
                        <span className="tabular">{bytes(item.size_bytes)}</span>
                        <span className="truncate max-w-[120px]">{item.mime_type}</span>
                        <span
                          className={`ml-auto flex items-center gap-0.5 tabular ${
                            rem.expired ? 'text-red-500' : rem.soon ? 'text-amber-500' : 'text-muted-foreground'
                          }`}
                        >
                          <Clock3 size={10} /> {rem.label}
                        </span>
                      </div>
                    </div>
                  </button>
                );
              })}

              {filtered.length === 0 && (
                <div className="py-14 text-center">
                  <div className="text-xs font-medium text-muted-foreground">
                    {items.length === 0 ? '暂无已发布文件' : '没有匹配的发布记录'}
                  </div>
                  {items.length === 0 && (
                    <button
                      type="button"
                      onClick={() => setShowPublish(true)}
                      className="glass-control mt-3 inline-flex items-center gap-1 rounded-lg px-3 py-1.5 text-xs text-foreground"
                    >
                      <Plus size={12} /> 立即发布一个文件
                    </button>
                  )}
                </div>
              )}
            </div>
          </div>

          {/* 右侧：详情、管理操作与常见格式预览 */}
          <div className="min-w-0">
            {selected ? (
              <div className="animate-fade-in space-y-3">
                <div className="space-y-3 rounded-xl border border-border/60 bg-card/40 p-4">
                  <div className="flex flex-wrap items-start gap-2.5">
                    <div className="mt-0.5">
                      <KindIcon item={selected} size={18} />
                    </div>
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <h3 className="break-all text-sm font-semibold">{selected.filename}</h3>
                        {selected.archive && <Badge className="bg-amber-500/10 text-amber-500">目录归档</Badge>}
                        {selected.width && selected.height ? (
                          <Badge className="bg-sky-500/10 text-sky-500">{selected.width} × {selected.height}</Badge>
                        ) : null}
                      </div>
                      <div className="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
                        <span className="tabular">{bytes(selected.size_bytes)}</span>
                        <span className="font-mono">{selected.mime_type || 'application/octet-stream'}</span>
                        <span title={formatDateTime(selected.created_at)}>发布于 {relTime(selected.created_at)}</span>
                        <span title={formatDateTime(selected.expires_at)}>
                          有效期剩余 {remainingTimeLabel(selected.expires_at).label}（{formatDateTime(selected.expires_at)}）
                        </span>
                      </div>
                    </div>
                    <a
                      href={api.artifactContentUrl(selected.artifact_id, true)}
                      download={selected.filename}
                      className="glass-primary flex shrink-0 items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium text-primary-foreground"
                    >
                      <Download size={13} /> 本机下载
                    </a>
                  </div>

                  {/* ID 与 SHA256 */}
                  <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px] text-muted-foreground">
                    <button
                      type="button"
                      onClick={() => copyValue(`id-${selected.artifact_id}`, selected.artifact_id)}
                      className="inline-flex items-center gap-1 font-mono hover:text-foreground"
                      title="点击复制 Artifact ID"
                    >
                      <span>ID: {selected.artifact_id}</span>
                      {copiedKey === `id-${selected.artifact_id}` ? <Check size={11} className="text-emerald-500" /> : <Copy size={11} />}
                    </button>
                    <button
                      type="button"
                      onClick={() => copyValue(`sha-${selected.artifact_id}`, selected.sha256)}
                      className="inline-flex items-center gap-1 font-mono hover:text-foreground"
                      title={`SHA-256: ${selected.sha256}`}
                    >
                      <span>SHA256: {selected.sha256.slice(0, 16)}…</span>
                      {copiedKey === `sha-${selected.artifact_id}` ? <Check size={11} className="text-emerald-500" /> : <Copy size={11} />}
                    </button>
                  </div>

                  {/* 公网签名链接 */}
                  {selected.url && (
                    <div className="flex flex-wrap items-center gap-2 rounded-lg border border-border/50 bg-muted/20 px-3 py-2">
                      <Link2 size={13} className="shrink-0 text-muted-foreground" />
                      <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground" title={selected.url}>
                        {selected.url}
                      </span>
                      <button
                        type="button"
                        onClick={() => copyValue(`url-${selected.artifact_id}`, selected.url)}
                        className="glass-control flex items-center gap-1 rounded-md px-2.5 py-1 text-[11px] text-foreground"
                      >
                        {copiedKey === `url-${selected.artifact_id}` ? <Check size={11} className="text-emerald-500" /> : <Copy size={11} />}
                        {copiedKey === `url-${selected.artifact_id}` ? '已复制' : '复制链接'}
                      </button>
                      <a
                        href={selected.url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="glass-control flex items-center gap-1 rounded-md px-2.5 py-1 text-[11px] text-foreground"
                      >
                        <ExternalLink size={11} /> 打开链接
                      </a>
                    </div>
                  )}

                  {/* 延期与撤销 */}
                  <div className="flex flex-wrap items-center gap-2 border-t border-border/50 pt-3">
                    <span className="text-xs text-muted-foreground">延长有效期</span>
                    {[24, 72, 168].map((h) => (
                      <button
                        key={h}
                        type="button"
                        onClick={() => setExtendHours(String(h))}
                        className={`glass-control rounded-md px-2 py-1 text-[11px] ${
                          extendHours === String(h) ? 'glass-chosen text-foreground' : 'text-muted-foreground'
                        }`}
                      >
                        +{h}h
                      </button>
                    ))}
                    <input
                      type="number"
                      min={1}
                      max={720}
                      value={extendHours}
                      onChange={(e) => setExtendHours(e.target.value)}
                      className="glass-control w-20 rounded-md px-2 py-1 font-mono text-xs outline-none"
                      aria-label="延期小时数"
                    />
                    <span className="text-[11px] text-muted-foreground">小时</span>
                    <button
                      type="button"
                      onClick={() => void handleExtend()}
                      disabled={extending}
                      className="glass-control flex items-center gap-1 rounded-md px-3 py-1 text-xs font-medium text-foreground disabled:opacity-40"
                    >
                      {extending && <RefreshCw size={11} className="animate-spin" />}
                      延期
                    </button>

                    <div className="ml-auto flex items-center gap-1.5">
                      {confirmRevokeId === selected.artifact_id ? (
                        <>
                          <span className="text-[11px] text-red-500">撤销后链接立即失效：</span>
                          <button
                            type="button"
                            onClick={() => void handleRevoke(selected.artifact_id)}
                            disabled={revoking}
                            className="rounded-md bg-red-500 px-2.5 py-1 text-xs font-medium text-white disabled:opacity-40"
                          >
                            {revoking ? '撤销中…' : '确认撤销'}
                          </button>
                          <button
                            type="button"
                            onClick={() => setConfirmRevokeId(null)}
                            className="glass-control rounded-md px-2.5 py-1 text-xs text-muted-foreground"
                          >
                            取消
                          </button>
                        </>
                      ) : (
                        <button
                          type="button"
                          onClick={() => setConfirmRevokeId(selected.artifact_id)}
                          className="glass-control flex items-center gap-1 rounded-md px-2.5 py-1 text-xs text-red-500 hover:bg-red-500/10"
                        >
                          <Trash2 size={12} /> 撤销发布
                        </button>
                      )}
                    </div>
                  </div>

                  <div className="text-[11px] text-muted-foreground/75">
                    提示：延期会按新有效期重新签名并仅影响新链接，已分发的旧链接将失效；总保留期不超过创建起 7 天。
                  </div>

                  {actionFeedback && (
                    <div
                      className={`flex items-center gap-2 rounded-lg px-3 py-2 text-xs ${
                        actionFeedback.type === 'ok' ? 'bg-emerald-500/10 text-emerald-500' : 'bg-red-500/10 text-red-500'
                      }`}
                    >
                      {actionFeedback.type === 'ok' ? <CheckCircle2 size={13} /> : <AlertTriangle size={13} />}
                      <span>{actionFeedback.text}</span>
                    </div>
                  )}
                </div>

                {/* 常见格式在线预览 */}
                <Panel className="p-4">
                  <ArtifactPreview key={selected.artifact_id} item={selected} />
                </Panel>
              </div>
            ) : (
              <EmptyState
                icon={Share2}
                title="选择一个已发布文件"
                hint="从左侧列表选择发布记录以查看链接与在线预览，或点击右上角「发布文件」主动分享本机文件"
              />
            )}
          </div>
        </div>
      </Panel>
    </div>
  );
}
