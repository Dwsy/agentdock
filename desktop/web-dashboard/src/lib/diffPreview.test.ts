import assert from 'node:assert/strict';
import test from 'node:test';
import { fileDiffPreview } from './diffPreview.ts';

const first = '--- a//repo/first.ts\n+++ b//repo/first.ts\n@@ -1 +1 @@\n-first old\n+first new\n';
const second = '--- a/src/second file.ts\told timestamp\n+++ b/src/second file.ts\tnew timestamp\n@@ -8 +9 @@\n-second old\n+second new\n';

test('从旧的批量预览选择文件，复制文本与解析行都只包含该分节', () => {
  const batch = first + second;
  assert.equal(fileDiffPreview(batch, { path: '/repo/first.ts' }).text, first);
  const preview = fileDiffPreview(batch, { path: '/repo/src/second file.ts' });
  assert.equal(preview.text, second);
  assert.deepEqual(preview.lines.slice(-2), [
    { type: 'del', oldNo: 8, text: 'second old' },
    { type: 'add', newNo: 9, text: 'second new' },
  ]);
});

test('缺少对应文件、路径冲突或批量补丁被截断时，返回空预览', () => {
  const sameName = second.replaceAll('src/', 'other/');
  const ambiguous = second + second.replaceAll('src/', '');
  assert.deepEqual(fileDiffPreview(first, { path: '/repo/second.ts' }), { text: '', lines: [], matched: false });
  assert.equal(fileDiffPreview(second + sameName, { path: '/repo/unknown/second file.ts' }).text, '');
  assert.equal(fileDiffPreview(ambiguous, { path: '/repo/src/second file.ts' }).text, '');
  assert.equal(fileDiffPreview(ambiguous, { path: 'src/second file.ts' }).text, second);
  const truncated = first + '--- a/src/second file.ts\n+++ b/src/second';
  assert.equal(fileDiffPreview(truncated, { path: '/repo/src/second' }).text, '');
  assert.equal(fileDiffPreview(truncated, { path: '/repo/first.ts' }).text, first);
  assert.equal(fileDiffPreview(first + '--- a/src/second', { path: '/repo/first.ts' }).text, first);
});

test('兼容已经分节的预览、无文件头的 legacy diff 和无路径的通用视图', () => {
  assert.equal(fileDiffPreview(second, { path: 'src/second file.ts' }).text, second);
  const legacy = '@@ -3 +6 @@\n-before\n+after';
  assert.deepEqual(fileDiffPreview(legacy, { path: '/repo/any.ts' }), fileDiffPreview(legacy));
  assert.equal(fileDiffPreview(first + second).text, first + second);
  assert.deepEqual(fileDiffPreview('', { path: '/repo/any.ts' }), { text: '', lines: [], matched: true });
});

test('Git 和纯 unified 分节可混用，新增与删除通过非 /dev/null 侧确认路径', () => {
  const added = 'diff --git a/created b/created\nnew file mode 100644\nindex 0000000..8be3f18\n--- /dev/null\n+++ b/created\n@@ -0,0 +1 @@\n+added\n';
  const deleted = '--- a/removed\n+++ /dev/null\tstamp\n@@ -1 +0,0 @@\n-removed\n';
  const binary = 'diff --git a/asset.png b/asset.png\nBinary files a/asset.png and b/asset.png differ\n';
  const batch = added + deleted + binary;
  assert.equal(fileDiffPreview(batch, { path: '/repo/created' }).text, added);
  assert.equal(fileDiffPreview(batch, { path: '/repo/removed' }).text, deleted);
  assert.ok(fileDiffPreview(batch, { path: '/repo/asset.png' }).lines.every((line) => line.type === 'meta'));
});

test('move 允许源和目标同时命中：git rename 单分节去重，删除+新增双分节按序拼接', () => {
  const rename = 'diff --git a/old.md b/new.md\nsimilarity index 90%\nrename from old.md\nrename to new.md\n--- a/old.md\n+++ b/new.md\n@@ -1 +1 @@\n-before\n+after\n';
  const fromSource = fileDiffPreview(rename, { path: '/repo/old.md', moveTo: '/repo/new.md' });
  assert.equal(fromSource.text, rename);
  assert.equal(fromSource.lines.filter((line) => line.type === 'add').length, 1);
  assert.equal(fileDiffPreview(rename, { path: '/repo/new.md', moveTo: '/repo/old.md' }).text, rename);

  const pair = '--- a/old.md\n+++ /dev/null\n@@ -1 +0,0 @@\n-before\n--- /dev/null\n+++ b/new.md\n@@ -0,0 +1 @@\n+after\n';
  const moved = fileDiffPreview(pair, { path: '/repo/old.md', moveTo: '/repo/new.md' });
  assert.equal(moved.text, pair);
  assert.deepEqual(moved.lines.filter((line) => line.type === 'add'), [{ type: 'add', newNo: 1, text: 'after' }]);
});

test('move 匹配同样严格优先：整路径命中后不再并入同名后缀分节', () => {
  const nested = '--- a//repo/src/old.md\n+++ b//repo/src/old.md\n@@ -1 +1 @@\n-a\n+b\n';
  const shallow = '--- a/old.md\n+++ b/old.md\n@@ -1 +1 @@\n-c\n+d\n';
  const preview = fileDiffPreview(shallow + nested, { path: '/repo/src/old.md', moveTo: '/repo/new.md' });
  assert.equal(preview.text, nested);
  assert.equal(preview.lines.filter((line) => line.type === 'add').length, 1);
});

test('move 每个目标独立应用优先级：一侧 exact、另一侧唯一后缀时两侧都保留', () => {
  const sourceSection = '--- a//repo/old.md\n+++ /dev/null\n@@ -1 +0,0 @@\n-before\n';
  const destSection = '--- /dev/null\n+++ b/new.md\n@@ -0,0 +1 @@\n+after\n';
  const preview = fileDiffPreview(sourceSection + destSection, { path: '/repo/old.md', moveTo: '/repo/new.md' });
  assert.equal(preview.text, sourceSection + destSection);
  assert.deepEqual(preview.lines.filter((line) => line.type === 'add'), [{ type: 'add', newNo: 1, text: 'after' }]);
});

test('一端后缀歧义时只放弃该端，另一端确认的分节仍保留', () => {
  const sourceSection = '--- a//repo/old.md\n+++ /dev/null\n@@ -1 +0,0 @@\n-before\n';
  const destSection = '--- /dev/null\n+++ b/new.md\n@@ -0,0 +1 @@\n+after\n';
  const ambiguousDest = '--- /dev/null\n+++ b/repo/new.md\n@@ -0,0 +1 @@\n+other\n';
  assert.equal(
    fileDiffPreview(sourceSection + destSection + ambiguousDest, { path: '/repo/old.md', moveTo: '/repo/new.md' }).text,
    sourceSection,
  );

  const ambiguousSource = '--- a/repo/old.md\n+++ /dev/null\n@@ -1 +0,0 @@\n-before\n--- a/old.md\n+++ /dev/null\n@@ -1 +0,0 @@\n-older\n';
  assert.equal(
    fileDiffPreview(ambiguousSource + destSection, { path: '/repo/old.md', moveTo: '/repo/new.md' }).text,
    destSection,
  );
});

test('纯元数据 Git rename 读取 rename from/to：未引用空格路径可匹配，真实 a/、b/ 目录不被剥掉', () => {
  const spaced = 'diff --git a/old file.md b/new file.md\nsimilarity index 100%\nrename from old file.md\nrename to new file.md\n';
  assert.equal(fileDiffPreview(spaced, { path: '/repo/old file.md', moveTo: '/repo/new file.md' }).text, spaced);

  const realDirs = 'diff --git x y\nsimilarity index 100%\nrename from a/real.md\nrename to b/real.md\n';
  const preview = fileDiffPreview(realDirs, { path: '/repo/a/real.md', moveTo: '/repo/b/real.md' });
  assert.equal(preview.text, realDirs);
  assert.deepEqual(preview.lines.map((line) => line.type), ['meta', 'meta', 'meta', 'meta']);

  // 末行无 LF 说明 rename to 的路径可能被截断：只显示，不参与匹配。
  const truncated = 'diff --git a/old file.md b/new file.md\nsimilarity index 100%\nrename from old file.md\nrename to new';
  assert.equal(fileDiffPreview(truncated, { path: '/repo/old file.md', moveTo: '/repo/new' }).text, truncated);
  assert.equal(fileDiffPreview(truncated, { path: '/repo/new' }).text, '');
});

test('保留 Git 引用、Windows 路径和 CRLF 的原始复制内容', () => {
  const quoted = String.raw`diff --git "a/\346\226\207.txt" "b/\346\226\207.txt"` + '\nold mode 100644\nnew mode 100755\n';
  assert.equal(fileDiffPreview(quoted, { path: '/repo/文.txt' }).text, quoted);
  const windows = first.replaceAll('/repo/first.ts', 'C:/repo/first.ts').replaceAll('\n', '\r\n');
  const preview = fileDiffPreview(windows, { path: 'C:\\repo\\first.ts' });
  assert.equal(preview.text, windows);
  assert.equal(preview.lines.at(-1)?.text, 'first new');
});

test('hunk 内伪文件头按代码计数，两侧行号独立，元数据和尾换行不消耗行号', () => {
  const patch = [
    '--- a/example', '+++ b/example', '@@ -4,3 +7,3 @@ first hunk',
    '--- before', '+++ after', ' keep', ' ',
    '@@ -20 +30 @@ second hunk', '-old', '\\ No newline at end of file', '+new', '',
  ].join('\n');
  assert.deepEqual(fileDiffPreview(patch, { path: 'example' }).lines, [
    { type: 'meta', text: '--- a/example' },
    { type: 'meta', text: '+++ b/example' },
    { type: 'hunk', text: '@@ -4,3 +7,3 @@ first hunk' },
    { type: 'del', oldNo: 4, text: '-- before' },
    { type: 'add', newNo: 7, text: '++ after' },
    { type: 'ctx', oldNo: 5, newNo: 8, text: 'keep' },
    { type: 'ctx', oldNo: 6, newNo: 9, text: '' },
    { type: 'hunk', text: '@@ -20 +30 @@ second hunk' },
    { type: 'del', oldNo: 20, text: 'old' },
    { type: 'add', newNo: 30, text: 'new' },
  ]);
});
