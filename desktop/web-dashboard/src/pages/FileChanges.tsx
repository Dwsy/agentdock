import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { FolderTree, FolderGit2, Search, GitCompare, FileText, Columns2, ChevronDown, ChevronRight, Clock3, Copy, Check, ChartColumn } from 'lucide-react';
import { api, type FileChanges as FileChangesData, type FileChange, type Tasks as TasksData } from '../api';
import { Panel, SectionTitle, Badge, fmt, relTime } from '../ui';
import HistoryChart from '../components/HistoryChart';
import Modal from '../components/Modal';
import { DEFAULT_HISTORY_SELECTION, HISTORY_RANGES, historyBounds, historyDateLabel, inHistoryBounds, localHistoryDate } from '../lib/historyStats';
import FileTree from '../components/FileTree';
import { buildProjectGroups } from '../lib/taskProjects';
import { changeDisplayPath, fileChangeKey, groupChangesByProject, type ChangeProjectGroup } from '../lib/changeProjects';
import DiffViewer from '../components/DiffViewer';
import MarkdownView from '../components/MarkdownView';
import { fileDiffPreview } from '../lib/diffPreview';
import { loadTraceDetail, traceChangeDiff, traceFileDiff } from '../lib/traceDetail';
import { usePersistedState } from '../lib/usePersistedState';

function isMarkdown(path: string) {
  return /\.(md|mdx)$/i.test(path);
}

function FileDetail({ change }: { change: FileChange }) {
  const [tab, setTab] = usePersistedState<'diff' | 'render'>('file-detail-tab', 'diff');
  const [copied, setCopied] = useState(false);
  const markdown = isMarkdown(change.path);
  // 批量预览在旧记录里每行都可能是同一段多文件 diff；Markdown 正文只能取
  // 当前选中文件的分节，否则会把别的文件新增行拼进来。
  const preview = useMemo(
    () => fileDiffPreview(change.diff || '', { path: change.path, moveTo: change.move_to }),
    [change.diff, change.path, change.move_to],
  );
  const md = useMemo(
    () => preview.lines.filter((line) => line.type === 'add').map((line) => line.text).join('\n'),
    [preview.lines],
  );
  // 渲染标签的持久化状态跨文件共享：切到非 Markdown 文件时必须回到 Diff。
  const activeTab = markdown ? tab : 'diff';
  const markdownHint = !(change.diff || '').trim()
    ? '暂无此文件的 diff 预览，无法还原 Markdown 正文'
    : !preview.matched
      ? '未能从 diff 预览中定位该文件的分节，无法还原 Markdown 正文'
      : md.trim()
        ? ''
        : '该文件的 diff 分节中没有新增行，无法还原 Markdown 正文';

  const copyPath = async () => {
    try {
      await navigator.clipboard.writeText(change.path);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // 剪贴板写入被拒绝时保持未复制状态，不能谎报成功。
    }
  };

  return (
    <div className="animate-fade-in">
      <div className="mb-3 flex flex-wrap items-start gap-3 rounded-xl border border-border/60 bg-card/40 p-3.5">
        <Badge className={change.action === 'create' ? 'bg-sky-500/10 text-sky-500' : change.action === 'delete' ? 'bg-red-500/10 text-red-500' : 'bg-violet-500/10 text-violet-500'}>{change.action}</Badge>
        <div className="min-w-0 flex-1">
          <div className="break-all font-mono text-xs" title={change.path}>{change.path}</div>
          <div className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-[11px] text-muted-foreground">
            <span className="flex items-center gap-1"><Clock3 size={11} /> {relTime(change.started_at)}</span>
            <span>{change.tool}</span>
            <span className="text-emerald-500">+{change.insertions || 0}</span>
            {change.deletions != null && <span className="text-red-500">-{change.deletions}</span>}
          </div>
        </div>
        <button onClick={copyPath} className="glass-control rounded-md p-1.5 text-muted-foreground" title="复制路径">
          {copied ? <Check size={13} /> : <Copy size={13} />}
        </button>
      </div>

      <div className="mb-2 flex flex-wrap gap-1">
        <button onClick={() => setTab('diff')} className={`glass-control flex items-center gap-1 rounded-md px-2.5 py-1 text-xs ${activeTab === 'diff' ? 'text-foreground' : 'text-muted-foreground'}`}>
          <GitCompare size={12} /> Diff
        </button>
        {markdown && (
          <button onClick={() => setTab('render')} className={`glass-control flex items-center gap-1 rounded-md px-2.5 py-1 text-xs ${activeTab === 'render' ? 'text-foreground' : 'text-muted-foreground'}`}>
            <FileText size={12} /> Markdown 渲染
          </button>
        )}
      </div>

      {activeTab === 'diff' ? (
        <DiffViewer
          diff={change.diff || ''}
          filePath={change.path}
          moveTo={change.move_to}
          insertions={change.insertions}
          deletions={change.deletions}
          truncated={change.truncated}
        />
      ) : markdownHint ? (
        <div className="rounded-lg border border-border/60 bg-muted/20 px-4 py-8 text-center text-xs text-muted-foreground">{markdownHint}</div>
      ) : (
        <div className="scrollbar-thin max-h-[520px] overflow-auto rounded-lg border border-border/60 bg-muted/20 p-4">
          <MarkdownView content={md} />
        </div>
      )}
    </div>
  );
}

function FileChangeDetail({ change, summary }: { change: FileChange; summary: boolean }) {
  const [detail, setDetail] = useState<{ response?: Awaited<ReturnType<typeof loadTraceDetail>>; error?: string }>();
  const [retry, setRetry] = useState(0);
  const wantFull = change.truncated === true;
  useEffect(() => {
    if (!summary && !wantFull) return;
    const controller = new AbortController();
    loadTraceDetail(change.id, wantFull, api.traceDetail, controller.signal).then(
      (response) => {
        if (!controller.signal.aborted) setDetail({ response });
      },
      (error: unknown) => {
        if (!controller.signal.aborted) setDetail({ error: error instanceof Error ? error.message : String(error) });
      },
    );
    return () => controller.abort();
  }, [change.id, retry, summary, wantFull]);

  const trace = detail?.response?.detail;
  const fileDiff = useMemo(() => trace && traceFileDiff(trace), [trace]);
  if (!summary && !wantFull) return <FileDetail change={change} />;
  if (!detail) return <div role="status" className="py-8 text-center text-sm text-muted-foreground">正在读取文件差异…</div>;

  const record = trace?.record.file_changes?.find((item) => item.path === change.path && item.action === change.action && item.move_to === change.move_to);
  const file = record ? { ...change, ...record } : change;
  const diff = traceChangeDiff(file, fileDiff);
  const retryDetail = () => {
    setDetail(undefined);
    setRetry((value) => value + 1);
  };
  const fetchError = detail.error || (detail.response?.fullFailed && diff.truncated ? '未能读取完整 diff。' : undefined);
  const hasFile = Boolean(record || diff.text || !summary);
  return (
    <>
      {(fetchError || !hasFile) && (
        <div role="alert" className="mb-2 space-y-2 rounded-lg border border-border/60 p-4 text-sm text-muted-foreground">
          <p>{fetchError ?? '这条追踪没有对应的文件差异。'}</p>
          {fetchError && <button type="button" className="glass-control rounded-lg px-2.5 py-1.5 text-foreground" onClick={retryDetail}>重试</button>}
        </div>
      )}
      {hasFile && <FileDetail change={{ ...file, diff: diff.text, truncated: diff.truncated }} />}
    </>
  );
}

/** 项目分组：项目头可折叠，内部按文件树展示并合并同一文件的多次修改。 */
function ProjectGroupBlock({
  group,
  selectedId,
  onSelect,
}: {
  group: ChangeProjectGroup;
  selectedId: string | null;
  onSelect: (change: FileChange) => void;
}) {
  const [open, setOpen] = useState(true);
  const getPath = useCallback((c: FileChange) => changeDisplayPath(c, group), [group]);
  return (
    <div>
      <button
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-1 rounded-md py-1 pr-2 text-left text-xs text-muted-foreground hover:bg-muted/40"
        title={group.full || group.name}
      >
        {open ? <ChevronDown size={13} className="shrink-0" /> : <ChevronRight size={13} className="shrink-0" />}
        <FolderGit2 size={13} className={`shrink-0 ${group.matched ? 'text-amber-400/80' : 'text-muted-foreground/50'}`} />
        <span className={`min-w-0 truncate font-medium ${group.matched ? '' : 'italic'}`}>{group.name}</span>
        <span className="ml-auto flex shrink-0 items-center gap-1 text-[10px] tabular text-muted-foreground/60">
          <span>{group.changes.length}</span>
          {group.insertions > 0 && <span className="text-emerald-500">+{group.insertions}</span>}
          {group.deletions > 0 && <span className="text-red-500">-{group.deletions}</span>}
        </span>
      </button>
      {open && (
        group.changes.length === 0
          ? <div className="py-1 pl-7 text-[11px] text-muted-foreground">没有文件</div>
          : <div className="pl-3 pt-0.5">
              <FileTree
                changes={group.changes}
                selected={selectedId ?? undefined}
                onSelect={onSelect}
                getDisplayPath={getPath}
                className="pr-1"
              />
            </div>
      )}
    </div>
  );
}

export default function FileChanges({ data, tasks }: { data: FileChangesData; tasks: TasksData }) {
  const [q, setQ] = useState('');
  // 两种分组只是左侧选择器的视图切换；选中项、搜索、右侧详情共用一套状态。
  const [mode, setMode] = usePersistedState<'tree' | 'project'>('files-mode', 'tree');
  const [selection, setSelection] = useState(DEFAULT_HISTORY_SELECTION);
  const [statsOpen, setStatsOpen] = useState(false);
  const statsButton = useRef<HTMLButtonElement>(null);
  const closeStats = useCallback(() => {
    setStatsOpen(false);
    statsButton.current?.focus();
  }, []);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const today = localHistoryDate();
  const dated = useMemo(() => data.changes.map((change) => ({ change, date: localHistoryDate(change.started_at) })), [data.changes]);
  const dates = useMemo(() => dated.map(({ date }) => date), [dated]);
  const bounds = useMemo(() => selection.custom ?? historyBounds(selection.range, today, dates), [selection.custom, selection.range, today, dates]);
  const coverage = useMemo(() => historyBounds('all', today, dates), [today, dates]);
  const rangeLabel = selection.custom ? historyDateLabel(selection.custom) : HISTORY_RANGES.find(([range]) => range === selection.range)?.[1];
  const needle = q.trim().toLowerCase();
  const matching = useMemo(() => dated.filter(({ change, date }) =>
    inHistoryBounds(date, bounds) && (!needle || change.path.toLowerCase().includes(needle) || change.move_to?.toLowerCase().includes(needle))), [dated, bounds, needle]);
  const filtered = useMemo(() => matching.map(({ change }) => change), [matching]);
  const points = useMemo(() => matching.map(({ change, date }) => ({
    date, first: change.insertions ?? 0, second: change.deletions ?? 0, count: 1,
  })), [matching]);
  const projectGroups = useMemo(() => buildProjectGroups(tasks.tasks), [tasks.tasks]);
  const grouped = useMemo(() => groupChangesByProject(filtered, projectGroups), [filtered, projectGroups]);
  const selected = filtered.find((c) => fileChangeKey(c) === selectedId) || filtered[0];
  const selectedKey = selected ? fileChangeKey(selected) : null;
  const status = typeof data.status === 'object' ? data.status : undefined;
  const summary = data.view === 'summary' || data.has_more !== undefined;
  const traceMode = data.mode ?? status?.mode ?? data.status;
  const note = [
    '浏览器本地日期',
    coverage && selection.range !== 'all' && `可查询记录 ${historyDateLabel(coverage)}`,
    status && status.max_records > 0 && status.count >= status.max_records && '追踪索引已达上限',
    data.has_more && `仅统计最新 ${fmt.format(data.changes.length)} 条文件变更`,
    data.ok && traceMode !== 'off' && !summary && '旧版 Core 仅提供最近样本',
    needle && '已应用路径搜索',
  ].filter(Boolean).join(' · ');

  return (
    <>
      <Panel className="min-w-0 p-3.5 sm:p-5">
        <SectionTitle
          icon={FolderTree}
          title="文件变更明细"
          action={
            <div className="flex flex-wrap items-center gap-2">
              <div className="glass-control flex min-w-0 flex-1 items-center gap-2 rounded-lg px-2.5 py-1.5">
                <Search size={14} aria-hidden="true" className="shrink-0 text-muted-foreground" />
                <input value={q} onChange={(e) => setQ(e.target.value)} aria-label="搜索文件路径" placeholder="搜索路径…" className="w-32 min-w-0 bg-transparent text-xs outline-none placeholder:text-muted-foreground sm:w-40" />
              </div>
              <button ref={statsButton} type="button" aria-haspopup="dialog" onClick={() => setStatsOpen(true)}
                className="glass-control inline-flex shrink-0 items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-xs font-medium hover:bg-accent focus-visible:outline-2 focus-visible:outline-offset-2">
                <ChartColumn size={14} aria-hidden="true" /> 编辑统计
              </button>
            </div>
          }
        />
        <div className="grid min-w-0 gap-4 lg:grid-cols-[minmax(0,300px)_minmax(0,1fr)]">
          {/* 左：目录树 / 按任务项目分组 */}
          <div className="rounded-xl border border-border/60 bg-muted/10 p-2">
            <div className="mb-2 flex flex-wrap items-center gap-1 px-1 pt-1">
              {([['tree', '目录树', FolderTree], ['project', '按项目', FolderGit2]] as const).map(([key, label, Icon]) => (
                <button
                  key={key}
                  onClick={() => setMode(key)}
                  aria-pressed={mode === key}
                  className={`glass-control flex items-center gap-1 rounded-md px-2 py-1 text-[11px] ${mode === key ? 'text-foreground' : 'text-muted-foreground'}`}
                >
                  <Icon size={12} /> {label}
                </button>
              ))}
              <span className="ml-auto tabular text-[11px] text-muted-foreground" title={bounds && historyDateLabel(bounds)}>
                {rangeLabel} · {fmt.format(filtered.length)} 条
              </span>
            </div>
            {mode === 'tree' ? (
              <FileTree changes={filtered} selected={selectedKey ?? undefined} onSelect={(c) => setSelectedId(fileChangeKey(c))} />
            ) : (
              <div className="scrollbar-thin max-h-[640px] space-y-1 overflow-auto pr-1">
                {grouped.map((group) => (
                  <ProjectGroupBlock key={group.key || '__unclassified__'} group={group} selectedId={selectedKey} onSelect={(c) => setSelectedId(fileChangeKey(c))} />
                ))}
                {grouped.length === 0 && <div className="py-12 text-center text-sm text-muted-foreground">没有文件</div>}
              </div>
            )}
          </div>
          {/* 右：详情 */}
          <div className="min-w-0">
            {selected ? (
              <FileChangeDetail key={selected.id} change={selected} summary={summary} />
            ) : (
              <div className="flex h-64 flex-col items-center justify-center gap-2 text-center">
                <Columns2 size={28} className="text-muted-foreground" />
                <div className="text-sm font-medium">选择一个文件</div>
                <div className="text-xs text-muted-foreground">左侧目录树中点开文件，查看左右对比 diff 或 Markdown 渲染</div>
              </div>
            )}
          </div>
        </div>
      </Panel>
      <Modal open={statsOpen} onClose={closeStats} title="文件编辑统计">
        <HistoryChart points={points} bounds={bounds} selection={selection} onSelection={setSelection}
          series={[{ label: '新增行', color: 'bg-emerald-500' }, { label: '删除行', color: 'bg-rose-500' }]}
          countLabel="文件变更条数" note={note}
          onFilterPeriod={(custom) => {
            setSelection((previous) => ({ ...previous, custom }));
            closeStats();
          }}
          empty={!data.ok ? '文件历史暂不可用' : traceMode === 'off' ? '调用追踪未开启，暂无文件历史' : '当前筛选没有文件变更'} />
      </Modal>
    </>
  );
}
