import { Fragment, useEffect, useState, type CSSProperties } from 'react';
import type { ThemedToken } from '@pierre/highlights';

export function fileLanguage(path?: string) {
  const name = path?.split(/[\\/]/).pop()?.toLowerCase();
  return name?.match(/[^.]+$/)?.[0];
}

export function useCodeTokens(text: string, language?: string) {
  const [highlight, setHighlight] = useState<{ text: string; language: string; lines: ThemedToken[][] }>();

  useEffect(() => {
    if (!text || !language) return;
    let active = true;
    import('../lib/highlightCode').then(({ highlightCode }) => {
      if (!active) return;
      const lines = highlightCode(text, language);
      if (lines) setHighlight({ text, language, lines });
    }).catch(() => { /* Keep the original plaintext if the highlighter is unavailable. */ });
    return () => { active = false; };
  }, [text, language]);

  return highlight?.text === text && highlight.language === language ? highlight.lines : undefined;
}

export function CodeTokens({ text, tokens }: { text: string; tokens?: ThemedToken[] }) {
  // The engine omits CR from CRLF. Keep line endings and fall back if tokens ever omit other text.
  const ending = text.endsWith('\r') ? '\r' : '';
  if (!tokens?.length || tokens.map(({ content }) => content).join('') + ending !== text) return text;
  return <>{tokens.map(({ content, offset, htmlStyle }) => (
    <span key={offset} className="code-token" style={{
      '--hls-light': htmlStyle?.['--hls-light'],
      '--hls-dark': htmlStyle?.['--hls-dark'],
    } as CSSProperties}>{content}</span>
  ))}{ending}</>;
}

export function CodeSource({ text, language, className }: { text: string; language?: string; className: string }) {
  const tokens = useCodeTokens(text, language);
  return (
    <pre className={className}><code>{tokens ? text.split('\n').map((line, i) => (
      <Fragment key={i}>{i > 0 && '\n'}<CodeTokens text={line} tokens={tokens[i]} /></Fragment>
    )) : text}</code></pre>
  );
}
