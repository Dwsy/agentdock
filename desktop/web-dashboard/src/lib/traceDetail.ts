import type { FileChange, TraceDetail, TraceRecord } from '../api';

export function isTraceTextTruncated(text?: string) {
  return text?.endsWith('…(truncated)') ?? false;
}

/** 预览截断后可能不再是合法 JSON，保留原文给调用方降级展示。 */
export function parseTraceResult<T>(text?: string): T | null {
  if (!text) return null;
  try {
    const parsed = JSON.parse(text);
    return parsed && typeof parsed === 'object' ? parsed as T : null;
  } catch {
    return null;
  }
}

export function hasTruncatedPayload(record: TraceRecord) {
  const cmd = record.command;
  return record.truncated || cmd?.command_preview_truncated || cmd?.stdout_preview_truncated ||
    cmd?.stderr_preview_truncated || [record.arguments, record.result, cmd?.command, cmd?.stdout, cmd?.stderr].some(isTraceTextTruncated);
}

export function hasTruncatedPreview(record: TraceRecord) {
  return hasTruncatedPayload(record) || (record.file_changes?.some((change) => change.truncated) ?? false);
}

type TraceFileDiff = { text: string; truncated: boolean };
type LegacyDiff = { diff_preview?: unknown; truncated?: unknown; dry_run?: boolean; changed?: boolean };
type LegacyEditResult = LegacyDiff & { _file_edit_history?: LegacyDiff };

export function traceFileDiff({ full, record }: TraceDetail): TraceFileDiff | undefined {
  if (full?.file_diff) {
    return { text: full.file_diff, truncated: full.file_diff_truncated === true || isTraceTextTruncated(full.file_diff) };
  }
  if (record.tool !== 'file_edit' || !record.success) return undefined;
  const args = parseTraceResult<{ action?: string; patch?: unknown; dry_run?: boolean }>(full?.arguments ?? record.arguments);
  if (args?.dry_run) return undefined;

  // 旧记录可能把完整 diff 留在原始结果，只裁掉了 file_changes 中的展示副本。
  let preview: TraceFileDiff | undefined;
  for (const value of [full?.result, record.result]) {
    const result = parseTraceResult<LegacyEditResult>(value);
    const history = result?._file_edit_history;
    if (result?.dry_run || result?.changed === false || history?.dry_run || history?.changed === false) return undefined;
    for (const diff of [history, result]) {
      const text = diff?.diff_preview;
      if (typeof text !== 'string' || !text) continue;
      if (diff.truncated === false && !isTraceTextTruncated(text)) return { text, truncated: false };
      if (!preview || text.length > preview.text.length) preview = { text, truncated: true };
    }
  }

  // 成功应用的 unified patch 本身就是差异。Envelope patch 则需要当时生成的 diff。
  if (args?.action === 'patch' && typeof args.patch === 'string' &&
    !args.patch.trimStart().startsWith('*** Begin Patch') && /^(?:diff --git |--- [^\n]+\n\+\+\+ )/m.test(args.patch)) {
    return { text: args.patch, truncated: false };
  }
  return preview;
}

export function traceChangeDiff(change: Pick<FileChange, 'diff' | 'truncated'>, loaded?: TraceFileDiff): TraceFileDiff {
  const text = change.diff ?? '';
  const truncated = change.truncated === true;
  if (loaded && (truncated || !text) && (!loaded.truncated || loaded.text.length > text.length)) return loaded;
  return { text, truncated };
}

export function traceFileChangeCounts(changes: TraceRecord['file_changes']) {
  if (!changes?.some((change) => change.insertions != null || change.deletions != null)) return undefined;
  return changes.reduce((total, change) => ({
    insertions: total.insertions + (change.insertions ?? 0),
    deletions: total.deletions + (change.deletions ?? 0),
  }), { insertions: 0, deletions: 0 });
}

type Request = (id: number, full: boolean, signal: AbortSignal) => Promise<TraceDetail>;

export async function loadTraceDetail(id: number, full: boolean, request: Request, signal: AbortSignal) {
  let detail: TraceDetail;
  try {
    detail = await request(id, full, signal);
  } catch (error) {
    if (!full || signal.aborted) throw error;
    // Older servers can reject the full option while still serving the preview.
    return { detail: await request(id, false, signal), fullFailed: true };
  }

  // Legacy summaries can omit the truncation flag even when detail has a truncated preview.
  if (!full && !signal.aborted && hasTruncatedPreview(detail.record)) {
    try {
      detail = await request(id, true, signal);
    } catch {
      return { detail, fullFailed: true };
    }
  }
  return { detail, fullFailed: false };
}
