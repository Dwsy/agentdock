import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import rehypeHighlight from 'rehype-highlight';

export default function MarkdownBody({ content }: { content: string }) {
  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      rehypePlugins={[rehypeHighlight]}
      components={{
        h1: ({ children }) => <h1 className="mb-3 mt-5 border-b border-border pb-2 text-xl font-semibold first:mt-0">{children}</h1>,
        h2: ({ children }) => <h2 className="mb-2 mt-5 border-b border-border/60 pb-1.5 text-lg font-semibold">{children}</h2>,
        h3: ({ children }) => <h3 className="mb-2 mt-4 text-base font-semibold">{children}</h3>,
        h4: ({ children }) => <h4 className="mb-1.5 mt-3 text-sm font-semibold">{children}</h4>,
        p: ({ children }) => <p className="my-2 text-muted-foreground">{children}</p>,
        ul: ({ children }) => <ul className="my-2 ml-5 list-disc space-y-1 text-muted-foreground">{children}</ul>,
        ol: ({ children }) => <ol className="my-2 ml-5 list-decimal space-y-1 text-muted-foreground">{children}</ol>,
        li: ({ children }) => <li className="leading-relaxed">{children}</li>,
        strong: ({ children }) => <strong className="font-semibold text-foreground">{children}</strong>,
        a: ({ children, href }) => <a href={href} className="text-sky-500 underline underline-offset-2" target="_blank" rel="noreferrer">{children}</a>,
        blockquote: ({ children }) => <blockquote className="my-3 border-l-2 border-primary/50 pl-3 text-muted-foreground">{children}</blockquote>,
        code: ({ className: cls, children }) =>
          cls?.includes('language-') ? (
            <code className={cls}>{children}</code>
          ) : (
            <code className="rounded bg-muted/60 px-1.5 py-0.5 font-mono text-[12px] text-foreground">{children}</code>
          ),
        pre: ({ children }) => (
          <pre className="scrollbar-thin my-3 overflow-auto rounded-lg border border-border/60 bg-muted/30 p-3 font-mono text-[11.5px] leading-relaxed">{children}</pre>
        ),
        table: ({ children }) => <div className="my-3 overflow-auto"><table className="w-full border-collapse text-xs">{children}</table></div>,
        th: ({ children }) => <th className="border border-border/60 bg-muted/40 px-2 py-1.5 text-left font-semibold">{children}</th>,
        td: ({ children }) => <td className="border border-border/60 px-2 py-1.5">{children}</td>,
        hr: () => <hr className="my-4 border-border/60" />,
      }}
    >
      {content}
    </ReactMarkdown>
  );
}
