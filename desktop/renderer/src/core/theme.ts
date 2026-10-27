// 主题与应用背景:Desktop 本地视觉偏好(主题/语言/背景图)只进入
// desktop-store.json;背景图通过 CSS 变量注入根壳层,不复制进会话或 ACP。

import { desktop } from './api';
import { t } from './i18n';
import { emit, state } from './state';
import { toast } from './ui-host';

export function applyTheme(theme: 'light' | 'dark'): void {
  state.store = { ...state.store, theme };
  if (typeof document !== 'undefined') {
    const root = document.documentElement;
    // A theme flip changes inherited colors on hundreds of nodes. Suppress
    // transitions for the next two frames so rows/cards do not start a
    // color/border/scrollbar-color transition storm.
    root.classList.add('theme-switching');
    root.dataset.theme = theme;
    const clear = () => root.classList.remove('theme-switching');
    if (typeof requestAnimationFrame === 'function') {
      requestAnimationFrame(() => requestAnimationFrame(clear));
    } else {
      setTimeout(clear, 0);
    }
  }
  void desktop.storeSet({ theme }).catch(() => undefined);
  emit();
}

export function applyLocalePreference(locale: 'zh' | 'en'): void {
  state.store = { ...state.store, locale };
  void desktop.storeSet({ locale }).catch(() => undefined);
  emit();
}

// 窗体样式只是渲染层投影与原生红绿灯可见性的开关;不重建议程,也不改变任何
// 会话/Run/安全边界。偏好由 main 进程持久化并同步应用。
export function applyWindowStyle(style: 'mac' | 'win'): void {
  state.store = { ...state.store, windowStyle: style };
  if (typeof document !== 'undefined') {
    document.documentElement.dataset.windowStyle = style;
  }
  void desktop.setWindowStyle(style).catch(() => undefined);
  emit();
}

export function applyWindowStyleClass(style: 'mac' | 'win'): void {
  if (typeof document !== 'undefined') {
    document.documentElement.dataset.windowStyle = style;
  }
}

function clampNumber(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, Math.round(value)));
}

function homeBackgroundErrorKey(reason: string): string {
  switch (reason) {
    case 'unsupported':
      return 'settings.homeBackgroundUnsupported';
    case 'oversized':
      return 'settings.homeBackgroundOversized';
    case 'unauthorized':
      return 'settings.homeBackgroundUnauthorized';
    default:
      return 'settings.homeBackgroundMissing';
  }
}

let backgroundImagePath = '';
let backgroundImageSource = '';
let backgroundImageErrorPath = '';

// Chromium silently drops CSS custom-property values larger than 2MiB, so a
// multi-megabyte base64 data URL injected into `--app-user-image` never reaches
// `background-image` (the home logo keeps working because <img src> has no such
// limit). Convert the authorized data URL into a blob: object URL: the CSS
// value stays a tiny string while the bytes live in memory, and revoke the
// previous object URL whenever the source is replaced or cleared.
export function dataUrlToObjectUrl(dataUrl: string): string {
  const comma = dataUrl.indexOf(',');
  const mime = /^data:([^;]+);base64$/.exec(dataUrl.slice(0, comma))?.[1] || 'application/octet-stream';
  const binary = atob(dataUrl.slice(comma + 1));
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i);
  return URL.createObjectURL(new Blob([bytes], { type: mime }));
}

function setBackgroundSource(next: string): void {
  if (backgroundImageSource === next) return;
  if (backgroundImageSource.startsWith('blob:')) URL.revokeObjectURL(backgroundImageSource);
  backgroundImageSource = next;
}

// Home imagery is a Desktop-only visual preference. Keep the file path in the
// local UI store rather than copying the image into session or ACP storage.
export function applyHomeBackground(root: HTMLElement | null): void {
  if (!root) return;
  const path = state.store.homeBackgroundImage.trim();
  if (!path) {
    backgroundImagePath = '';
    setBackgroundSource('');
    root.classList.remove('has-app-background');
    root.classList.remove('has-home-background');
    for (const property of ['--app-user-image', '--app-user-image-opacity', '--app-user-image-blur', '--app-user-image-size', '--app-user-image-repeat', '--app-user-image-position', '--app-background-veil', '--app-surface-veil', '--app-surface-blur']) root.style.removeProperty(property);
    return;
  }
  if (backgroundImagePath !== path) {
    backgroundImagePath = path;
    setBackgroundSource('');
    backgroundImageErrorPath = '';
    void desktop.homeBackgroundDataURL(path).then((result) => {
      // The selection may have changed while the privileged request was in
      // flight. Never apply stale image data to the current shell.
      if (backgroundImagePath !== path || state.store.homeBackgroundImage.trim() !== path || !root.isConnected) return;
      if (result.ok) {
        let source = '';
        try {
          source = dataUrlToObjectUrl(result.dataUrl);
        } catch {
          // Only reachable for malformed payloads; small images still render
          // through the raw data URL, large ones degrade as before.
          source = result.dataUrl;
        }
        setBackgroundSource(source);
        backgroundImageErrorPath = '';
      } else {
        if (backgroundImageErrorPath !== path) {
          backgroundImageErrorPath = path;
          toast(t(homeBackgroundErrorKey(result.reason), { s: '20MB' }));
        }
        setBackgroundSource('');
      }
      applyHomeBackground(root);
    }).catch(() => {
      if (backgroundImagePath === path && backgroundImageErrorPath !== path) {
        backgroundImageErrorPath = path;
        toast(t('settings.homeBackgroundMissing'));
      }
    });
  }
  const source = backgroundImageSource;
  if (!source) {
    root.classList.remove('has-app-background');
    root.classList.remove('has-home-background');
    return;
  }
  const fit = state.store.homeBackgroundFit;
  const positions: Record<typeof state.store.homeBackgroundPosition, string> = { center: 'center', left: 'left center', right: 'right center', top: 'center top', bottom: 'center bottom' };
  const opacity = clampNumber(state.store.homeBackgroundOpacity, 0, 100) / 100;
  root.classList.toggle('has-app-background', state.store.homeBackgroundScope === 'app');
  root.classList.toggle('has-home-background', state.store.homeBackgroundScope === 'home');
  root.style.setProperty('--app-user-image', `url("${source}")`);
  root.style.setProperty('--app-user-image-opacity', String(opacity));
  root.style.setProperty('--app-user-image-blur', `${clampNumber(state.store.homeBackgroundBlur, 0, 24)}px`);
  root.style.setProperty('--app-user-image-size', fit === 'stretch' ? '100% 100%' : fit === 'tile' ? 'auto' : fit);
  root.style.setProperty('--app-user-image-repeat', fit === 'tile' ? 'repeat' : 'no-repeat');
  root.style.setProperty('--app-user-image-position', positions[state.store.homeBackgroundPosition]);
  // A full-opacity image deliberately removes the former fixed pale/dark veils
  // and surface blur, so the selected image remains genuinely visible.
  root.style.setProperty('--app-background-veil', String((1 - opacity) * 0.72));
  root.style.setProperty('--app-surface-veil', String((1 - opacity) * 0.68));
  root.style.setProperty('--app-surface-blur', `${Math.round((1 - opacity) * 10)}px`);
}

export type HomeBackgroundPatch = Partial<Pick<typeof state.store, 'homeBackgroundImage' | 'homeBackgroundOpacity' | 'homeBackgroundBlur' | 'homeBackgroundScope' | 'homeBackgroundFit' | 'homeBackgroundPosition'>>;

export function updateHomeBackground(patch: HomeBackgroundPatch, persist: boolean): void {
  const next: typeof state.store = { ...state.store };
  if (patch.homeBackgroundImage !== undefined) next.homeBackgroundImage = patch.homeBackgroundImage;
  if (patch.homeBackgroundOpacity !== undefined) next.homeBackgroundOpacity = clampNumber(patch.homeBackgroundOpacity, 0, 100);
  if (patch.homeBackgroundBlur !== undefined) next.homeBackgroundBlur = clampNumber(patch.homeBackgroundBlur, 0, 24);
  if (patch.homeBackgroundScope !== undefined) next.homeBackgroundScope = patch.homeBackgroundScope;
  if (patch.homeBackgroundFit !== undefined) next.homeBackgroundFit = patch.homeBackgroundFit;
  if (patch.homeBackgroundPosition !== undefined) next.homeBackgroundPosition = patch.homeBackgroundPosition;
  state.store = next;
  if (persist) {
    // Only the privileged native chooser may authorize a non-empty image path.
    // Clearance (empty string) and non-image settings may still be persisted
    // through the generic store patch.
    const storePatch = { ...patch };
    if (typeof storePatch.homeBackgroundImage === 'string' && storePatch.homeBackgroundImage.trim() !== '') {
      delete storePatch.homeBackgroundImage;
    }
    if (Object.keys(storePatch).length > 0) void desktop.storeSet(storePatch);
  }
  emit();
}

export { clampNumber };
