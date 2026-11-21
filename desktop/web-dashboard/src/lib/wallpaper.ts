export type Wallpaper = string | Blob;

const STORE = 'appearance';
const WALLPAPER_KEY = 'wallpaper';

export function parseWallpaperURL(input: string): string {
  const value = input.trim();
  try {
    if (/^https?:\/\//i.test(value)) {
      const url = new URL(value);
      if (!url.username && !url.password) return url.href;
    }
  } catch {
    // Use the same message for malformed and unsupported URLs.
  }
  throw new Error('请输入完整的 HTTP(S) 图片链接，链接中不能含用户名或密码。');
}

function openWallpaperDB(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open('agentdock-dashboard', 1);
    let blocked = false;
    request.onupgradeneeded = () => request.result.createObjectStore(STORE);
    request.onerror = () => reject(request.error);
    request.onblocked = () => {
      blocked = true;
      reject(new Error('Wallpaper database is blocked'));
    };
    request.onsuccess = () => {
      const db = request.result;
      if (blocked) db.close();
      else resolve(db);
    };
  });
}

async function wallpaperTransaction<T>(
  mode: IDBTransactionMode,
  operation: (store: IDBObjectStore) => Pick<IDBRequest<T>, 'result'>,
): Promise<T> {
  const db = await openWallpaperDB();
  try {
    return await new Promise<T>((resolve, reject) => {
      const transaction = db.transaction(STORE, mode);
      const request = operation(transaction.objectStore(STORE));
      // A request can succeed before the transaction fails, including on quota errors.
      transaction.oncomplete = () => resolve(request.result);
      transaction.onabort = () => reject(transaction.error ?? new Error('Wallpaper transaction was aborted'));
    });
  } finally {
    db.close();
  }
}

export async function readWallpaper(): Promise<Wallpaper | undefined> {
  const stored: unknown = await wallpaperTransaction('readonly', (store) => store.get(WALLPAPER_KEY));
  return typeof stored === 'string' ? parseWallpaperURL(stored) : stored instanceof Blob ? stored : undefined;
}

export async function writeWallpaper(wallpaper?: Wallpaper): Promise<void> {
  await wallpaperTransaction('readwrite', (store) => {
    if (wallpaper === undefined) return store.delete(WALLPAPER_KEY);
    return store.put(wallpaper, WALLPAPER_KEY);
  });
}

export type LoadedWallpaper = { image: HTMLImageElement; release: () => void };

export async function loadWallpaper(wallpaper: Wallpaper, signal: AbortSignal): Promise<LoadedWallpaper> {
  signal.throwIfAborted();
  const objectURL = typeof wallpaper === 'string' ? null : URL.createObjectURL(wallpaper);
  const image = new Image();
  image.alt = '';
  image.referrerPolicy = 'no-referrer';
  const release = () => {
    image.removeAttribute('src');
    if (objectURL) URL.revokeObjectURL(objectURL);
  };

  try {
    await new Promise<void>((resolve, reject) => {
      const fail = () => finish(new Error('无法加载图片。请检查链接或选择浏览器支持的图片。'));
      const abort = () => finish(signal.reason);
      const timer = window.setTimeout(() => finish(new Error('图片加载超时，请重试。')), 15000);
      function finish(error?: unknown) {
        window.clearTimeout(timer);
        signal.removeEventListener('abort', abort);
        image.onload = null;
        image.onerror = null;
        if (error) reject(error);
        else resolve();
      }
      image.onload = () => image.naturalWidth > 0 ? finish() : fail();
      image.onerror = fail;
      signal.addEventListener('abort', abort, { once: true });
      image.src = typeof wallpaper === 'string' ? wallpaper : objectURL!;
    });
    return { image, release };
  } catch (error) {
    release();
    throw error;
  }
}
