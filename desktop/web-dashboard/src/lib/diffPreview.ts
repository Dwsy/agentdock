export type DiffLine = {
  type: 'add' | 'del' | 'ctx' | 'hunk' | 'meta';
  oldNo?: number;
  newNo?: number;
  text: string;
};

export type DiffPreviewOptions = {
  /** 展示的文件路径；为空时返回整段 diff（通用视图）。 */
  path?: string;
  /** move 的目标路径：源分节和目标分节都算命中。 */
  moveTo?: string;
};

export type DiffPreview = {
  /** 选中分节的原始文本，用于复制。 */
  text: string;
  /** 选中分节的解析行，用于渲染。 */
  lines: DiffLine[];
  /** 给了路径但批量 diff 里找不到对应分节时为 false；无路径的通用视图恒为 true。 */
  matched: boolean;
};

type Section = { start: number; end: number; paths: string[]; lines: DiffLine[] };
const hunkHeader = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/;

function normalizePath(path: string): string {
  return (/^(?:[a-z]:[\\/]|\\\\)/i.test(path) ? path.replace(/\\/g, '/') : path).replace(/^\.\//, '');
}

const gitEscapes: Record<string, string> = {
  a: '\x07', b: '\b', f: '\f', n: '\n', r: '\r', t: '\t', v: '\v', '"': '"', '\\': '\\',
};

/** 只解码 Git C 风格引用路径；前缀剥离与分隔符归一由调用方决定。 */
function decodePath(field: string): string {
  let path = field;
  if (path === '/dev/null') return '';
  if (path.startsWith('"')) {
    if (!/^"(?:[^"\\]|\\(?:[0-3][0-7]{2}|[abfnrtv"\\]))*"$/.test(path)) return '';
    try {
      path = path.slice(1, -1).replace(/(?:\\[0-3][0-7]{2})+|\\(.)/g, (escapes, char: string | undefined) => {
        if (char) return gitEscapes[char];
        const bytes = Uint8Array.from(escapes.match(/[0-7]{3}/g) ?? [], (octal) => parseInt(octal, 8));
        return new TextDecoder('utf-8', { fatal: true }).decode(bytes);
      });
    } catch {
      return '';
    }
  }
  return path;
}

/** `---`/`+++` 头带 a/ b/ 前缀和可选时间戳；先剥前缀再归一，a/C:\ 这类旧路径才能识别盘符。 */
function headerPath(field: string): string {
  return normalizePath(decodePath(field.split('\t', 1)[0]).replace(/^[ab]\//, ''));
}

/** `rename from`/`rename to` 是裸路径：a/、b/ 可能是真实目录，不能当 Git 前缀剥掉。 */
function renamePath(line: string, prefix: string): string {
  return normalizePath(decodePath(line.slice(prefix.length)));
}

function uniquePaths(paths: string[]): string[] {
  return paths.filter((path, index) => path !== '' && paths.indexOf(path) === index);
}

/** `diff --git a/old b/new` 同时带源和目标路径：move 的任一侧都要能命中同一分节。 */
function gitHeaderPaths(line: string): string[] {
  // The paired headers supply the paths when unquoted spaces make this line ambiguous.
  const match = line.match(/^diff --git (a\/[^\s"]+|"a\/(?:[^"\\]|\\.)*") (b\/[^\s"]+|"b\/(?:[^"\\]|\\.)*")$/);
  return match ? uniquePaths([headerPath(match[1]), headerPath(match[2])]) : [];
}

function addPaths(section: Section, paths: string[]) {
  for (const path of paths) {
    if (!section.paths.includes(path)) section.paths.push(path);
  }
}

function isRelativePath(path: string): boolean {
  return path !== '' && !/^(?:\/|[a-z]:)/i.test(path);
}

/**
 * 对单个目标独立应用优先级：严格整路径命中优先；零严格时才尝试唯一后缀。
 * 任一层命中多个分节都视为歧义，该端不选——但另一端确认的分节仍然保留。
 */
function pickSectionForTarget(sections: Section[], target: string): Section | null {
  const strict = sections.filter((section) => section.paths.includes(target));
  if (strict.length === 1) return strict[0];
  if (strict.length > 1) return null;
  const suffix = sections.filter((section) => section.paths.some((path) => (
    isRelativePath(path) && target.endsWith(`/${path}`)
  )));
  return suffix.length === 1 ? suffix[0] : null;
}

/** Select the sections for a file from old records that stored the same batch preview on every row. */
export function fileDiffPreview(diff: string, options: DiffPreviewOptions = {}): DiffPreview {
  const rawLines = diff.match(/[^\n]*\n|[^\n]+$/g) ?? [];
  const textLines = rawLines.map((line) => line.replace(/\r?\n$/, ''));
  const sections: Section[] = [];
  let section: Section = { start: 0, end: rawLines.length, paths: [], lines: [] };
  const preamble = section;
  let awaitingGitHeaders = false;
  let oldNo = 0;
  let newNo = 0;
  let oldRemaining = 0;
  let newRemaining = 0;

  const startSection = (start: number, paths: string[]) => {
    section.end = start;
    section = { start, end: rawLines.length, paths: uniquePaths(paths), lines: [] };
    sections.push(section);
    oldNo = newNo = oldRemaining = newRemaining = 0;
  };

  for (let i = 0; i < textLines.length; i++) {
    const line = textLines[i];
    const next = textLines[i + 1];
    const hunk = line.match(hunkHeader);
    const inHunk = oldRemaining > 0 || newRemaining > 0;
    const isCode = inHunk || sections.length === 0;

    if (line.startsWith('diff --git ')) {
      // A truncated last line may have lost half of the path; never trust it.
      startSection(i, rawLines[i].endsWith('\n') ? gitHeaderPaths(line) : []);
      awaitingGitHeaders = true;
      section.lines.push({ type: 'meta', text: line });
    } else if (!inHunk && line.startsWith('--- ') && next?.startsWith('+++ ')) {
      const paths = rawLines[i].endsWith('\n') && rawLines[i + 1].endsWith('\n')
        ? uniquePaths([headerPath(line.slice(4)), headerPath(next.slice(4))])
        : [];
      if (awaitingGitHeaders) addPaths(section, paths);
      else startSection(i, paths);
      awaitingGitHeaders = false;
      section.lines.push({ type: 'meta', text: line }, { type: 'meta', text: next });
      i++;
    } else if (hunk) {
      awaitingGitHeaders = false;
      oldNo = Number(hunk[1]);
      newNo = Number(hunk[3]);
      oldRemaining = Number(hunk[2] ?? 1);
      newRemaining = Number(hunk[4] ?? 1);
      section.lines.push({ type: 'hunk', text: line });
    } else if (awaitingGitHeaders && line.startsWith('rename from ')) {
      // 纯元数据 rename 没有 ---/+++ 头，路径只能从这两行补齐（可含未引用空格）。
      // 无 LF 的末行可能被截断，仍然显示但绝不作为匹配路径。
      if (rawLines[i].endsWith('\n')) addPaths(section, [renamePath(line, 'rename from ')]);
      section.lines.push({ type: 'meta', text: line });
    } else if (awaitingGitHeaders && line.startsWith('rename to ')) {
      if (rawLines[i].endsWith('\n')) addPaths(section, [renamePath(line, 'rename to ')]);
      section.lines.push({ type: 'meta', text: line });
    } else if (!inHunk && sections.length > 0 && line.startsWith('---') && !awaitingGitHeaders) {
      // A batch preview can end halfway through the next file header.
      startSection(i, []);
      section.lines.push({ type: 'meta', text: line });
    } else if (isCode && line.startsWith('+')) {
      section.lines.push({ type: 'add', newNo: newNo++, text: line.slice(1) });
      newRemaining--;
    } else if (isCode && line.startsWith('-')) {
      section.lines.push({ type: 'del', oldNo: oldNo++, text: line.slice(1) });
      oldRemaining--;
    } else if ((isCode && line.startsWith(' ')) || (inHunk && line === '')) {
      section.lines.push({ type: 'ctx', oldNo: oldNo++, newNo: newNo++, text: line.slice(1) });
      oldRemaining--;
      newRemaining--;
    } else if (line && !line.startsWith('\\ No newline at end of file')) {
      section.lines.push({ type: 'meta', text: line });
    }
  }

  if (!options.path || sections.length === 0) {
    return { text: diff, lines: [preamble, ...sections].flatMap((part) => part.lines), matched: true };
  }

  // move 的源与目标各自独立匹配；同一 git rename 分节被两侧同时命中时按位置去重。
  const targets = uniquePaths([
    normalizePath(options.path),
    options.moveTo ? normalizePath(options.moveTo) : '',
  ]);
  const picked = new Map<number, Section>();
  for (const target of targets) {
    const section = pickSectionForTarget(sections, target);
    if (section) picked.set(section.start, section);
  }
  const selected = [...picked.values()].sort((a, b) => a.start - b.start);
  if (selected.length === 0) {
    return { text: '', lines: [], matched: false };
  }

  return {
    text: selected.map((part) => rawLines.slice(part.start, part.end).join('')).join(''),
    lines: selected.flatMap((part) => part.lines),
    matched: true,
  };
}
