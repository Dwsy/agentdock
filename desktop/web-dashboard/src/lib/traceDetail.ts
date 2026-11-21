import type { TraceDetail, TraceRecord } from '../api';

export function isTraceTextTruncated(text?: string) {
  return text?.endsWith('…(truncated)') ?? false;
}

export function hasTruncatedPreview(record: TraceRecord) {
  const cmd = record.command;
  return record.truncated || cmd?.command_preview_truncated || cmd?.stdout_preview_truncated ||
    cmd?.stderr_preview_truncated || [record.arguments, record.result, cmd?.command, cmd?.stdout, cmd?.stderr].some(isTraceTextTruncated);
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
