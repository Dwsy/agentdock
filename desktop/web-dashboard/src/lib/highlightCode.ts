import { codeToTokens, isSupportedLanguage } from '@pierre/highlights';
import light from '@pierre/highlights/themes/github-light-high-contrast';
import dark from '@pierre/highlights/themes/github-dark';

export function highlightCode(text: string, language: string) {
  if (!isSupportedLanguage(language) || language === 'ansi') return undefined;
  return codeToTokens(text, {
    lang: language,
    themes: { light, dark },
    defaultColor: false,
    tokenizeMaxLineLength: 64 * 1024,
  }).tokens;
}
