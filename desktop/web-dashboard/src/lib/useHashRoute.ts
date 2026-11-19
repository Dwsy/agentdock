import { useCallback, useEffect, useState } from 'react';

/**
 * 用 URL hash 记住当前页：#overview、#files 等。
 * 刷新后 hash 还在，页面不变；浏览器前进/后退也能切页。
 */
export function useHashRoute<T extends string>(validIds: readonly T[], defaultId: T): [T, (id: T) => void] {
  const parse = useCallback((): T => {
    const raw = window.location.hash.replace(/^#\/?/, '');
    return (validIds as readonly string[]).includes(raw) ? (raw as T) : defaultId;
  }, [validIds, defaultId]);

  const [page, setPage] = useState<T>(parse);

  useEffect(() => {
    // 首次进入把当前页写进 hash（replace 而非 push，不污染后退历史）。
    if (!window.location.hash || parse() === defaultId) {
      const next = `#${page}`;
      if (window.location.hash !== next) {
        window.history.replaceState(null, '', next);
      }
    }
    const onChange = () => setPage(parse());
    window.addEventListener('hashchange', onChange);
    return () => window.removeEventListener('hashchange', onChange);
    // page 只在首次用于补 hash，后续切页由 navigate 负责
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [parse, defaultId]);

  const navigate = useCallback(
    (id: T) => {
      setPage(id);
      const next = `#${id}`;
      if (window.location.hash !== next) {
        window.location.hash = id;
      }
    },
    [],
  );

  return [page, navigate];
}
