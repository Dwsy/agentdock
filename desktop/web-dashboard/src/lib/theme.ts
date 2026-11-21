/**
 * 外观模式的纯逻辑：跟随系统 / 浅色 / 深色。
 *
 * 旧版只有一个布尔开关，把「当前渲染出来的是深还是浅」写进 localStorage 的
 * `agentdock-theme`。那不是用户的意图——系统在日落后转深色，它也会被写成
 * `dark`。所以这里改用独立的 `agentdock-theme-mode` 记录意图，旧键只在没有
 * 新键时清掉并回到跟随系统，避免老用户被历史渲染状态钉死在某个颜色上。
 */

export type ThemeMode = 'system' | 'light' | 'dark';

export const THEME_MODE_KEY = 'agentdock-theme-mode';

/** 旧布尔开关的键，只用于迁移清理。 */
export const LEGACY_THEME_KEY = 'agentdock-theme';

/** 点一下按钮的循环顺序：跟随系统 → 浅色 → 深色 → 跟随系统。 */
export const THEME_CYCLE: readonly ThemeMode[] = ['system', 'light', 'dark'];

/** 只接受已知取值，其余（含 null、损坏数据）一律回到跟随系统。 */
export function parseThemeMode(raw: string | null | undefined): ThemeMode {
  return raw === 'light' || raw === 'dark' || raw === 'system' ? raw : 'system';
}

/**
 * 读取用户选择的外观模式。
 * 传入 localStorage 或任意同形实现；不可用时按跟随系统处理。
 */
export function readThemeMode(
  storage: Pick<Storage, 'getItem' | 'removeItem'> | null | undefined,
): ThemeMode {
  if (!storage) return 'system';
  try {
    const stored = storage.getItem(THEME_MODE_KEY);
    if (storage.getItem(LEGACY_THEME_KEY) !== null) {
      // 旧值只有渲染结果、没有意图，一律清掉；没有新键时按跟随系统重新开始。
      storage.removeItem(LEGACY_THEME_KEY);
    }
    return parseThemeMode(stored);
  } catch {
    return 'system';
  }
}

/** 把模式解析成「现在该不该用深色」；只有跟随系统才看系统偏好。 */
export function resolveDark(mode: ThemeMode, systemDark: boolean): boolean {
  if (mode === 'light') return false;
  if (mode === 'dark') return true;
  return systemDark;
}

/** 循环到下一个模式，供单按钮切换用。 */
export function nextThemeMode(mode: ThemeMode): ThemeMode {
  const index = THEME_CYCLE.indexOf(mode);
  return THEME_CYCLE[(index + 1) % THEME_CYCLE.length];
}

/** 模式的中文名，用于按钮 title / aria-label。 */
export function themeModeLabel(mode: ThemeMode): string {
  if (mode === 'light') return '浅色';
  if (mode === 'dark') return '深色';
  return '跟随系统';
}
