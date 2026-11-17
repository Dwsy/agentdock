import { lazy, Suspense } from 'react';

/** 从 markdown 文件的新增 diff 行还原正文，用于渲染“变更后的 markdown 长什么样”。 */
export function markdownFromDiff(diff: string): string {
  return diff
    .split('\n')
    .filter((l) => l.startsWith('+') && !l.startsWith('+++'))
    .map((l) => l.slice(1))
    .join('\n');
}

// react-markdown + remark-gfm + rehype-highlight 体积较大，按需加载，
// 首屏（概览/实时/任务/能力）不加载这一坨。
const HeavyMarkdown = lazy(() => import('./MarkdownBody'));

export default function MarkdownView({ content, className = '' }: { content: string; className?: string }) {
  return (
    <div className={`markdown-body text-[13px] leading-relaxed ${className}`}>
      <Suspense fallback={<div className="py-8 text-center text-xs text-muted-foreground">加载 Markdown 渲染器…</div>}>
        <HeavyMarkdown content={content} />
      </Suspense>
    </div>
  );
}
