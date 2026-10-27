// 侧边栏滚动条:默认隐藏,交互(指针进入/滚轮/滚动)时显示,随后快速自动隐藏。
// 隐藏只切换 thumb 的透明度,不改变宽度,因此不会触发布局。

import { useEffect, type RefObject } from 'react';

// Hide quickly after the last interaction. Keeping it visible for 2s made the
// sidebar feel sticky, especially when the pointer stayed inside the panel.
export const AUTO_HIDE_DELAY_MS = 600;
// Leaving the sidebar should clear it almost immediately.
export const LEAVE_HIDE_DELAY_MS = 200;

export function useAutoHideScrollbar(ref: RefObject<HTMLElement | null>): void {
  useEffect(() => {
    const container = ref.current;
    if (!container) return;
    let hideTimer: number | undefined;
    let dragging = false;

    const hide = () => {
      if (!dragging) container.classList.remove('scrolling');
    };

    const scheduleHide = (delay: number) => {
      window.clearTimeout(hideTimer);
      if (dragging) return;
      hideTimer = window.setTimeout(hide, delay);
    };

    const reveal = () => {
      container.classList.add('scrolling');
      scheduleHide(AUTO_HIDE_DELAY_MS);
    };

    const leave = () => scheduleHide(LEAVE_HIDE_DELAY_MS);

    // Dragging the thumb must keep it visible even if the pointer rests.
    const startDrag = () => {
      dragging = true;
      window.clearTimeout(hideTimer);
      container.classList.add('scrolling');
    };
    const endDrag = () => {
      if (!dragging) return;
      dragging = false;
      scheduleHide(AUTO_HIDE_DELAY_MS);
    };

    container.addEventListener('pointerenter', reveal);
    container.addEventListener('pointerleave', leave);
    container.addEventListener('pointerdown', startDrag);
    container.addEventListener('wheel', reveal, { passive: true });
    container.addEventListener('scroll', reveal, { passive: true });
    window.addEventListener('pointerup', endDrag);
    window.addEventListener('pointercancel', endDrag);
    return () => {
      window.clearTimeout(hideTimer);
      container.removeEventListener('pointerenter', reveal);
      container.removeEventListener('pointerleave', leave);
      container.removeEventListener('pointerdown', startDrag);
      container.removeEventListener('wheel', reveal);
      container.removeEventListener('scroll', reveal);
      window.removeEventListener('pointerup', endDrag);
      window.removeEventListener('pointercancel', endDrag);
    };
  }, [ref]);
}
