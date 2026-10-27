// 列表 FLIP 动画:条目因插入/删除/排序发生位移时,用 WAAPI 做平滑的位移动画。
// 仅当列表签名(可见条目顺序)变化时测量与播放,流式更新期间的高频重渲染
// 不会触发额外布局测量。新条目自身的入场动画由 CSS animate-in 承担。

import { useCallback, useLayoutEffect, useRef } from 'react';

export function useFlipList(signature: string): (key: string) => (el: HTMLElement | null) => void {
  const elements = useRef(new Map<string, HTMLElement>());
  const callbacks = useRef(new Map<string, (el: HTMLElement | null) => void>());
  const previousRects = useRef(new Map<string, DOMRect>());
  const previousSignature = useRef('');

  useLayoutEffect(() => {
    if (signature === previousSignature.current) return;
    previousSignature.current = signature;
    const nextRects = new Map<string, DOMRect>();
    for (const [key, el] of elements.current) nextRects.set(key, el.getBoundingClientRect());
    for (const [key, el] of elements.current) {
      const before = previousRects.current.get(key);
      const after = nextRects.get(key);
      if (!before || !after) continue;
      const delta = before.top - after.top;
      if (Math.abs(delta) < 0.5) continue;
      el.animate(
        [{ transform: `translateY(${delta}px)` }, { transform: 'translateY(0px)' }],
        { duration: 220, easing: 'cubic-bezier(0.2, 0, 0, 1)' },
      );
    }
    previousRects.current = nextRects;
  }, [signature]);

  return useCallback((key: string) => {
    let callback = callbacks.current.get(key);
    if (!callback) {
      callback = (el: HTMLElement | null) => {
        if (el) {
          elements.current.set(key, el);
        } else {
          // Deleted sessions/projects must not leave a ref callback behind for
          // the lifetime of the window.
          elements.current.delete(key);
          callbacks.current.delete(key);
        }
      };
      callbacks.current.set(key, callback);
    }
    return callback;
  }, []);
}
