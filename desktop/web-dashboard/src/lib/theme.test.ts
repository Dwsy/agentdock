import assert from 'node:assert/strict';
import test from 'node:test';
import {
  LEGACY_THEME_KEY,
  THEME_MODE_KEY,
  nextThemeMode,
  parseThemeMode,
  readThemeMode,
  resolveDark,
  themeModeLabel,
} from './theme.ts';

function fakeStorage(initial: Record<string, string>) {
  const data = new Map(Object.entries(initial));
  return {
    getItem: (key: string) => (data.has(key) ? (data.get(key) as string) : null),
    removeItem: (key: string) => {
      data.delete(key);
    },
    has: (key: string) => data.has(key),
  };
}

test('只接受三个已知模式，其余回到跟随系统', () => {
  assert.equal(parseThemeMode('system'), 'system');
  assert.equal(parseThemeMode('light'), 'light');
  assert.equal(parseThemeMode('dark'), 'dark');
  assert.equal(parseThemeMode(null), 'system');
  assert.equal(parseThemeMode(''), 'system');
  assert.equal(parseThemeMode('purple'), 'system');
});

test('显式模式不受系统偏好影响，跟随系统才看系统偏好', () => {
  assert.equal(resolveDark('light', true), false);
  assert.equal(resolveDark('dark', false), true);
  assert.equal(resolveDark('system', true), true);
  assert.equal(resolveDark('system', false), false);
});

test('没有存过模式时默认跟随系统', () => {
  assert.equal(readThemeMode(fakeStorage({})), 'system');
  assert.equal(readThemeMode(null), 'system');
});

test('旧布尔键被清掉，不会被当成用户意图', () => {
  const storage = fakeStorage({ [LEGACY_THEME_KEY]: 'dark' });
  assert.equal(readThemeMode(storage), 'system');
  assert.equal(storage.has(LEGACY_THEME_KEY), false);
});

test('新键优先于旧布尔键，且旧键同时被清掉', () => {
  const storage = fakeStorage({ [THEME_MODE_KEY]: 'dark', [LEGACY_THEME_KEY]: 'light' });
  assert.equal(readThemeMode(storage), 'dark');
  assert.equal(storage.has(LEGACY_THEME_KEY), false);
});

test('存储抛错时按跟随系统处理', () => {
  const broken = {
    getItem: () => {
      throw new Error('blocked');
    },
    removeItem: () => {},
  };
  assert.equal(readThemeMode(broken), 'system');
});

test('按钮循环顺序是 跟随系统 → 浅色 → 深色', () => {
  assert.equal(nextThemeMode('system'), 'light');
  assert.equal(nextThemeMode('light'), 'dark');
  assert.equal(nextThemeMode('dark'), 'system');
  assert.equal(themeModeLabel('system'), '跟随系统');
  assert.equal(themeModeLabel('light'), '浅色');
  assert.equal(themeModeLabel('dark'), '深色');
});
