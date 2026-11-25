import { useCallback, useEffect, useLayoutEffect, useRef, useState, type FormEvent, type RefObject } from 'react';
import { Upload, X } from 'lucide-react';
import { loadWallpaper, parseWallpaperURL, readWallpaper, writeWallpaper, type LoadedWallpaper, type Wallpaper } from '../lib/wallpaper';

const ENABLED_KEY = 'agentdock-dashboard-background';
const secondaryButton = 'glass-control rounded-lg px-3 py-2 text-sm font-medium disabled:opacity-50';

function readEnabled() {
  try {
    return localStorage.getItem(ENABLED_KEY) !== 'off';
  } catch {
    return true;
  }
}

export default function DashboardBackground({ dialogRef }: { dialogRef: RefObject<HTMLDialogElement | null> }) {
  const [enabled, setEnabled] = useState(readEnabled);
  const [url, setURL] = useState('');
  const [pending, setPending] = useState('正在读取背景设置…');
  const [error, setError] = useState('');
  const [toggleError, setToggleError] = useState('');
  const fileRef = useRef<HTMLInputElement>(null);
  const wallpaperRef = useRef<HTMLDivElement>(null);
  const activeImage = useRef<LoadedWallpaper | undefined>(undefined);
  const lifetime = useRef<AbortSignal | null>(null);

  const display = useCallback((wallpaper?: LoadedWallpaper) => {
    wallpaperRef.current?.replaceChildren(...(wallpaper ? [wallpaper.image] : []));
    activeImage.current?.release();
    activeImage.current = wallpaper;
  }, []);

  useLayoutEffect(() => {
    const root = document.documentElement;
    root.toggleAttribute('data-wallpaper-off', !enabled);
    return () => root.removeAttribute('data-wallpaper-off');
  }, [enabled]);

  useEffect(() => {
    const controller = new AbortController();
    const { signal } = controller;
    lifetime.current = signal;
    async function restore() {
      try {
        const stored = await readWallpaper();
        if (!stored || signal.aborted) return;
        if (typeof stored === 'string') setURL(stored);
        const loaded = await loadWallpaper(stored, signal);
        if (signal.aborted) loaded.release();
        else display(loaded);
      } catch {
        if (!signal.aborted) setError('无法恢复已保存的背景，当前显示默认背景。');
      } finally {
        if (!signal.aborted) setPending('');
      }
    }
    void restore();
    return () => {
      controller.abort();
      display();
    };
  }, [display]);

  async function applyWallpaper(wallpaper?: Wallpaper) {
    const signal = lifetime.current;
    if (pending || !signal || signal.aborted) return;
    setError('');
    setPending(wallpaper ? '正在加载图片…' : '正在恢复默认背景…');
    let loaded: LoadedWallpaper | undefined;
    try {
      if (wallpaper !== undefined) {
        try {
          loaded = await loadWallpaper(wallpaper, signal);
        } catch (cause) {
          if (!signal.aborted) setError(cause instanceof Error ? cause.message : '无法加载图片。');
          return;
        }
      }
      if (signal.aborted) return;
      try {
        await writeWallpaper(wallpaper);
      } catch {
        if (!signal.aborted) setError('无法在当前浏览器保存背景。请检查浏览器存储权限或可用空间。');
        return;
      }
      if (signal.aborted) return;
      display(loaded);
      loaded = undefined;
      setURL(typeof wallpaper === 'string' ? wallpaper : '');
    } finally {
      loaded?.release();
      if (!signal.aborted) setPending('');
    }
  }

  function submitURL(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending) return;
    try {
      void applyWallpaper(parseWallpaperURL(url));
    } catch (cause) {
      setError((cause as Error).message);
    }
  }

  function toggleBackground(checked: boolean) {
    setEnabled(checked);
    try {
      localStorage.setItem(ENABLED_KEY, checked ? 'on' : 'off');
      setToggleError('');
    } catch {
      setToggleError('当前浏览器无法保存开关状态。');
    }
  }

  return (
    <>
      <div className="dashboard-wallpaper" aria-hidden="true">
        <div ref={wallpaperRef} className="dashboard-wallpaper-image" />
      </div>
      <dialog
        ref={dialogRef}
        className="background-dialog bg-popover text-popover-foreground"
        aria-labelledby="background-title"
        aria-describedby="background-storage-note"
      >
        <div className="flex items-start justify-between gap-3">
          <div>
            <h2 id="background-title" className="text-base font-semibold">背景设置</h2>
            <p id="background-storage-note" className="mt-1 text-sm text-muted-foreground">设置仅保存在当前浏览器。</p>
          </div>
          <button
            type="button"
            autoFocus
            aria-label="关闭背景设置"
            onClick={() => dialogRef.current?.close()}
            className="glass-control grid size-9 shrink-0 place-items-center rounded-lg"
          ><X size={17} aria-hidden="true" /></button>
        </div>

        <label className="flex min-h-11 items-center justify-between gap-3 rounded-xl border border-border px-3 text-sm font-medium">
          液态玻璃背景
          <input
            type="checkbox"
            role="switch"
            checked={enabled}
            onChange={(event) => toggleBackground(event.currentTarget.checked)}
            className="size-5 shrink-0 accent-current"
          />
        </label>
        {toggleError && <p role="alert" className="text-sm text-destructive">{toggleError}</p>}

        <form onSubmit={submitURL} className="space-y-2">
          <label htmlFor="background-url" className="block text-sm font-medium">图片链接</label>
          <div className="flex flex-wrap gap-2">
            <input
              id="background-url"
              type="text"
              inputMode="url"
              autoComplete="off"
              autoCapitalize="none"
              spellCheck={false}
              placeholder="https://example.com/image.webp"
              value={url}
              onChange={(event) => setURL(event.currentTarget.value)}
              className="min-w-0 flex-[1_1_11rem] rounded-lg border border-input bg-background px-3 py-2 text-base placeholder:text-muted-foreground"
            />
            <button type="submit" disabled={!!pending} className="glass-primary rounded-lg px-4 py-2 text-sm font-medium text-primary-foreground disabled:opacity-50">应用链接</button>
          </div>
        </form>

        <div className="flex flex-wrap gap-2">
          <input
            ref={fileRef}
            type="file"
            accept="image/*"
            hidden
            onChange={(event) => {
              const file = event.currentTarget.files?.[0];
              event.currentTarget.value = '';
              if (file) void applyWallpaper(file);
            }}
          />
          <button type="button" disabled={!!pending} onClick={() => fileRef.current?.click()} className={`${secondaryButton} inline-flex items-center gap-2`}>
            <Upload size={15} aria-hidden="true" /> 选择本地图片
          </button>
          <button type="button" disabled={!!pending} onClick={() => void applyWallpaper()} className={secondaryButton}>恢复默认</button>
        </div>
        {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
        <p role="status" className="empty:hidden text-sm text-muted-foreground">{pending}</p>
      </dialog>
    </>
  );
}
