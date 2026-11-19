import { useEffect, useState, type Dispatch, type SetStateAction } from 'react';

/** 视图配置的 localStorage 前缀，避免和主题等其它键冲突。 */
const PREFIX = 'agentdock:view:';

/**
 * 与 useState 同签名，但把值持久化到 localStorage，刷新后自动恢复。
 * 用于「点击切换后应长期记住」的视图开关：分组/目录、diff 模式、tab、筛选等。
 * 搜索输入这类临时状态不要用它。
 */
export function usePersistedState<T>(key: string, initial: T | (() => T)): [T, Dispatch<SetStateAction<T>>] {
  const storageKey = PREFIX + key;

  const [value, setValue] = useState<T>(() => {
    try {
      const raw = localStorage.getItem(storageKey);
      if (raw !== null) return JSON.parse(raw) as T;
    } catch {
      // 存储不可用或值损坏时回退默认值
    }
    return typeof initial === 'function' ? (initial as () => T)() : initial;
  });

  useEffect(() => {
    try {
      localStorage.setItem(storageKey, JSON.stringify(value));
    } catch {
      // 隐私模式下 localStorage 可能不可写，忽略即可
    }
  }, [storageKey, value]);

  return [value, setValue];
}
