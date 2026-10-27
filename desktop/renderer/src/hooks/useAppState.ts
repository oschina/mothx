// React ↔ 中央状态桥:useSyncExternalStore 订阅 core/state 的版本号。
// 组件在渲染期直接读取 state 字段(与旧的 renderAll 全量刷新语义一致)。

import { useCallback, useRef, useSyncExternalStore } from 'react';

import { getStateVersion, hasFeature, state, subscribe, type AppState } from '@/core/state';

export type StateSelector<T> = (snapshot: AppState) => T;

// useAppState keeps the original coarse subscription: it re-renders whenever
// any part of the app state changes. Streaming views (chat transcript) use it;
// shell/list components should prefer useAppSelector so a token chunk does not
// re-render the sidebar, status bar, or composer.
export function useAppState(): AppState {
  useSyncExternalStore(subscribe, getStateVersion, getStateVersion);
  return state;
}

export function useFeature(key: string): boolean {
  useAppState();
  return hasFeature(key);
}

export function shallowEqual<T>(a: T, b: T): boolean {
  if (Object.is(a, b)) return true;
  if (!a || !b || typeof a !== 'object' || typeof b !== 'object') return false;
  const left = a as Record<string, unknown>;
  const right = b as Record<string, unknown>;
  const leftKeys = Object.keys(left);
  if (leftKeys.length !== Object.keys(right).length) return false;
  for (const key of leftKeys) {
    if (!Object.is(left[key], right[key])) return false;
  }
  return true;
}

// useAppSelector subscribes to the shared version but only re-renders when the
// selected value changes (Object.is by default, shallowEqual for objects).
// State writes stay synchronous; the coalesced notification in core/state
// decides when React is allowed to read them.
export function useAppSelector<T>(selector: StateSelector<T>, isEqual: (a: T, b: T) => boolean = Object.is): T {
  const selectorRef = useRef(selector);
  selectorRef.current = selector;
  const isEqualRef = useRef(isEqual);
  isEqualRef.current = isEqual;
  const cacheRef = useRef<{ version: number; value: T } | null>(null);

  const getSnapshot = useCallback(() => {
    const currentVersion = getStateVersion();
    const cached = cacheRef.current;
    if (cached && cached.version === currentVersion) return cached.value;
    const next = selectorRef.current(state);
    // Keep the previous value identity so useSyncExternalStore can skip the
    // re-render. Strings/numbers are already identical via Object.is.
    if (cached && isEqualRef.current(cached.value, next)) {
      cached.version = currentVersion;
      return cached.value;
    }
    cacheRef.current = { version: currentVersion, value: next };
    return next;
  }, []);

  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}
