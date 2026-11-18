export type PreviewKind =
  | 'image'
  | 'svg'
  | 'markdown'
  | 'json'
  | 'csv'
  | 'pdf'
  | 'video'
  | 'audio'
  | 'code'
  | 'binary';

export type PreviewCategory = 'image' | 'doc' | 'media' | 'other';

const BINARY_EXTS = new Set([
  'tar', 'gz', 'tgz', 'bz2', 'tbz2', 'xz', 'txz', 'zst', 'zip', 'rar', '7z',
  'dmg', 'pkg', 'iso', 'img', 'exe', 'dll', 'so', 'dylib', 'bin', 'wasm',
  'class', 'pyc', 'pyo', 'o', 'a', 'lib', 'woff', 'woff2', 'ttf', 'otf', 'eot',
  'doc', 'docx', 'xls', 'xlsx', 'ppt', 'pptx', 'sqlite', 'db',
]);

const IMAGE_EXTS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp', 'avif', 'ico', 'jfif']);
const VIDEO_EXTS = new Set(['mp4', 'webm', 'mov', 'm4v', 'ogv', 'mkv']);
const AUDIO_EXTS = new Set(['mp3', 'wav', 'ogg', 'oga', 'm4a', 'aac', 'flac', 'opus', 'weba']);
const MARKDOWN_EXTS = new Set(['md', 'mdx', 'markdown']);
const JSON_EXTS = new Set(['json', 'jsonl', 'ndjson', 'jsonc']);
const CSV_EXTS = new Set(['csv', 'tsv']);
const CODE_EXTS = new Set([
  'txt', 'log', 'out', 'err', 'conf', 'cfg', 'ini', 'env', 'toml', 'yaml', 'yml',
  'xml', 'xhtml', 'html', 'htm', 'css', 'scss', 'sass', 'less',
  'js', 'mjs', 'cjs', 'jsx', 'ts', 'mts', 'cts', 'tsx',
  'py', 'pyi', 'go', 'mod', 'sum', 'rs', 'java', 'kt', 'kts', 'swift',
  'c', 'h', 'cc', 'cpp', 'hpp', 'm', 'mm', 'cs', 'rb', 'php',
  'sh', 'bash', 'zsh', 'fish', 'ps1', 'psm1', 'bat', 'cmd',
  'sql', 'graphql', 'gql', 'proto', 'cmake', 'gradle', 'properties',
  'diff', 'patch', 'rtf', 'tex', 'vue', 'svelte', 'astro',
]);
const TEXT_BASENAMES = new Set([
  'readme', 'license', 'changelog', 'dockerfile', 'makefile',
  '.gitignore', '.gitattributes', '.editorconfig', '.dockerignore', '.env',
]);

function fileExtension(filename: string): string {
  const clean = filename.trim().toLowerCase().split(/[\\/]/).pop() || '';
  const idx = clean.lastIndexOf('.');
  if (idx <= 0 || idx === clean.length - 1) return '';
  return clean.slice(idx + 1);
}

function baseMimeType(mimeType: string): string {
  return mimeType.split(';', 1)[0].trim().toLowerCase();
}

export function classifyPreviewKind(filename: string, mimeType: string, archive = false): PreviewKind {
  if (archive) return 'binary';
  const baseName = (filename.trim().toLowerCase().split(/[\\/]/).pop() || '');
  const ext = fileExtension(filename);
  const mime = baseMimeType(mimeType);

  if (BINARY_EXTS.has(ext)) return 'binary';
  if (ext === 'svg' || mime === 'image/svg+xml') return 'svg';
  if (IMAGE_EXTS.has(ext) || mime.startsWith('image/')) return 'image';
  if (ext === 'pdf' || mime === 'application/pdf') return 'pdf';
  if (VIDEO_EXTS.has(ext) || mime.startsWith('video/')) return 'video';
  if (AUDIO_EXTS.has(ext) || mime.startsWith('audio/')) return 'audio';
  if (MARKDOWN_EXTS.has(ext) || mime === 'text/markdown' || mime === 'text/x-markdown') return 'markdown';
  if (JSON_EXTS.has(ext) || mime === 'application/json' || mime === 'application/ld+json' || mime === 'application/x-ndjson' || mime.endsWith('+json')) {
    return 'json';
  }
  if (CSV_EXTS.has(ext) || mime === 'text/csv' || mime === 'text/tab-separated-values' || mime === 'application/csv') {
    return 'csv';
  }
  if (
    CODE_EXTS.has(ext) ||
    TEXT_BASENAMES.has(baseName) ||
    mime.startsWith('text/') ||
    mime === 'application/javascript' ||
    mime === 'application/x-javascript' ||
    mime === 'application/typescript' ||
    mime === 'application/xml' ||
    mime === 'application/xhtml+xml' ||
    mime === 'application/x-sh' ||
    mime === 'application/x-yaml' ||
    mime === 'application/toml' ||
    mime === 'application/sql'
  ) {
    return 'code';
  }
  return 'binary';
}

export function previewCategory(kind: PreviewKind): PreviewCategory {
  switch (kind) {
    case 'image':
    case 'svg':
      return 'image';
    case 'video':
    case 'audio':
      return 'media';
    case 'binary':
      return 'other';
    default:
      return 'doc';
  }
}

export function isLikelyBinaryBytes(bytes: Uint8Array, sampleSize = 8192): boolean {
  const limit = Math.min(bytes.length, sampleSize);
  for (let i = 0; i < limit; i++) {
    if (bytes[i] === 0) return true;
  }
  return false;
}

export type DelimitedTable = {
  headers: string[];
  rows: string[][];
  truncated: boolean;
  totalRows: number;
};

export function parseDelimitedTable(text: string, delimiter: ',' | '\t', maxRows = 200): DelimitedTable {
  const clean = text.startsWith('\uFEFF') ? text.slice(1) : text;
  if (!clean.trim()) {
    return { headers: [], rows: [], truncated: false, totalRows: 0 };
  }

  const parsed: string[][] = [];
  let row: string[] = [];
  let cell = '';
  let inQuotes = false;

  for (let i = 0; i < clean.length; i++) {
    const ch = clean[i];
    if (inQuotes) {
      if (ch === '"') {
        if (clean[i + 1] === '"') {
          cell += '"';
          i++;
        } else {
          inQuotes = false;
        }
      } else {
        cell += ch;
      }
      continue;
    }
    if (ch === '"') {
      inQuotes = true;
    } else if (ch === delimiter) {
      row.push(cell);
      cell = '';
    } else if (ch === '\r' || ch === '\n') {
      if (ch === '\r' && clean[i + 1] === '\n') i++;
      row.push(cell);
      parsed.push(row);
      row = [];
      cell = '';
    } else {
      cell += ch;
    }
  }
  if (cell.length > 0 || row.length > 0) {
    row.push(cell);
    parsed.push(row);
  }

  while (parsed.length > 0) {
    const last = parsed[parsed.length - 1];
    if (last.length === 1 && last[0] === '') {
      parsed.pop();
    } else {
      break;
    }
  }
  if (parsed.length === 0) {
    return { headers: [], rows: [], truncated: false, totalRows: 0 };
  }

  const headers = parsed[0];
  const dataRows = parsed.slice(1);
  return {
    headers,
    rows: dataRows.slice(0, maxRows),
    truncated: dataRows.length > maxRows,
    totalRows: dataRows.length,
  };
}

export type JsonPreview = {
  formatted: string;
  valid: boolean;
  lineCount: number;
};

export function formatJsonPreview(text: string, filename: string): JsonPreview {
  const ext = fileExtension(filename);
  const rawLines = text ? text.split('\n').length : 0;
  if (ext === 'jsonl' || ext === 'ndjson') {
    const entries = text
      .split(/\r?\n/)
      .map((line) => line.trim())
      .filter(Boolean);
    if (entries.length === 0) {
      return { formatted: text, valid: false, lineCount: rawLines };
    }
    try {
      const formatted = entries.map((line) => JSON.stringify(JSON.parse(line), null, 2)).join('\n\n');
      return { formatted, valid: true, lineCount: formatted.split('\n').length };
    } catch {
      return { formatted: text, valid: false, lineCount: rawLines };
    }
  }
  try {
    const formatted = JSON.stringify(JSON.parse(text), null, 2);
    return { formatted, valid: true, lineCount: formatted.split('\n').length };
  } catch {
    return { formatted: text, valid: false, lineCount: rawLines };
  }
}

export type RemainingTime = {
  label: string;
  expired: boolean;
  soon: boolean;
};

export function remainingTimeLabel(expiresAtIso: string, nowMs = Date.now()): RemainingTime {
  const expiresMs = new Date(expiresAtIso).getTime();
  if (Number.isNaN(expiresMs)) {
    return { label: '-', expired: false, soon: false };
  }
  const diff = expiresMs - nowMs;
  if (diff <= 0) {
    return { label: '已过期', expired: true, soon: false };
  }
  const soon = diff < 2 * 3_600_000;
  if (diff < 60_000) {
    return { label: '不到 1 分钟', expired: false, soon };
  }
  if (diff < 3_600_000) {
    return { label: `${Math.floor(diff / 60_000)} 分钟`, expired: false, soon };
  }
  if (diff < 86_400_000) {
    const hours = Math.floor(diff / 3_600_000);
    const mins = Math.floor((diff % 3_600_000) / 60_000);
    return { label: mins > 0 ? `${hours} 小时 ${mins} 分` : `${hours} 小时`, expired: false, soon };
  }
  const days = Math.floor(diff / 86_400_000);
  const hours = Math.floor((diff % 86_400_000) / 3_600_000);
  return { label: hours > 0 ? `${days} 天 ${hours} 小时` : `${days} 天`, expired: false, soon };
}
