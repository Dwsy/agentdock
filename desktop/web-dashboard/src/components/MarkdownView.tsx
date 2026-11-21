import { lazy, Suspense } from 'react';

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
