import { Minus, Square, X } from 'lucide-react';

import { MothxLogo } from '@/components/MothxLogo';
import { desktop } from '@/core/api';
import { t } from '@/core/i18n';
import { applyWindowStyle } from '@/core/theme';
import { toast } from '@/core/ui-host';
import { useAppBackground } from '@/hooks/useAppBackground';
import { shallowEqual, useAppSelector } from '@/hooks/useAppState';
import { cn } from '@/lib/utils';

type WindowStyle = 'mac' | 'win';

// macOS 红绿灯只在“macOS 窗体 + 非 macOS 真机”时自绘;真机使用原生红绿灯。
function MacTrafficLights() {
  return (
    <div className="mac-traffic-lights titlebar-no-drag">
      <button
        type="button"
        className="mac-btn close"
        title={t('win.close')}
        aria-label={t('win.close')}
        onClick={() => desktop.windowControl('close')}
      >
        <svg width="6" height="6" viewBox="0 0 6 6" fill="none" aria-hidden="true">
          <path d="M0.5 0.5L5.5 5.5M5.5 0.5L0.5 5.5" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
        </svg>
      </button>
      <button
        type="button"
        className="mac-btn minimize"
        title={t('win.minimize')}
        aria-label={t('win.minimize')}
        onClick={() => desktop.windowControl('minimize')}
      >
        <svg width="6" height="6" viewBox="0 0 6 6" fill="none" aria-hidden="true">
          <path d="M0.5 3H5.5" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
        </svg>
      </button>
      <button
        type="button"
        className="mac-btn maximize"
        title={t('win.maximize')}
        aria-label={t('win.maximize')}
        onClick={() => desktop.windowControl('maximize')}
      >
        <svg width="6" height="6" viewBox="0 0 6 6" fill="none" aria-hidden="true">
          <path d="M1 5L5 1M2 1H5V4M4 5H1V2" stroke="currentColor" strokeWidth="0.9" strokeLinecap="round" />
        </svg>
      </button>
    </div>
  );
}

function WindowsWindowControls({ backgroundApp }: { backgroundApp: boolean }) {
  return (
    <div className={cn('win-window-controls titlebar-no-drag', backgroundApp && 'app-titlebar-surface rounded-lg p-0.5')}>
      <button
        type="button"
        className={cn(
          'win-ctrl-btn hover:bg-hoverbg hover:text-strong',
          backgroundApp && 'rounded-[5px] border border-transparent hover:border-[var(--app-control-border-strong)] hover:bg-[var(--app-titlebar-control-hover)] hover:text-strong'
        )}
        title={t('win.minimize')}
        aria-label={t('win.minimize')}
        onClick={() => desktop.windowControl('minimize')}
      >
        <Minus className="size-4" />
      </button>
      <button
        type="button"
        className={cn(
          'win-ctrl-btn hover:bg-hoverbg hover:text-strong',
          backgroundApp && 'rounded-[5px] border border-transparent hover:border-[var(--app-control-border-strong)] hover:bg-[var(--app-titlebar-control-hover)] hover:text-strong'
        )}
        title={t('win.maximize')}
        aria-label={t('win.maximize')}
        onClick={() => desktop.windowControl('maximize')}
      >
        <Square className="size-3.5" />
      </button>
      <button
        type="button"
        className={cn(
          'win-ctrl-btn close hover:bg-danger hover:text-destructive-foreground',
          backgroundApp && 'rounded-[5px] border border-transparent hover:border-transparent hover:bg-danger hover:text-destructive-foreground'
        )}
        title={t('win.close')}
        aria-label={t('win.close')}
        onClick={() => desktop.windowControl('close')}
      >
        <X className="size-4" />
      </button>
    </div>
  );
}

function WindowStyleSwitch({ windowStyle }: { windowStyle: WindowStyle }) {
  return (
    <div className="os-switcher titlebar-no-drag" role="group" aria-label={t('windowStyle.label')}>
      <button
        type="button"
        className={cn('os-switch-btn', windowStyle === 'mac' && 'active')}
        aria-pressed={windowStyle === 'mac'}
        title={t('windowStyle.switchToMac')}
        onClick={() => switchWindowStyle('mac')}
      >
        <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <path d="M18.71 19.5c-.83 1.24-1.71 2.45-3.05 2.47-1.34.03-1.77-.79-3.29-.79-1.53 0-2 .77-3.27.82-1.31.05-2.3-1.32-3.14-2.53C4.25 17 2.94 12.45 4.7 9.39c.87-1.52 2.43-2.48 4.12-2.51 1.28-.02 2.5.87 3.29.87.78 0 2.26-1.07 3.81-.91.65.03 2.47.26 3.64 1.98-.09.06-2.17 1.28-2.15 3.81.03 3.02 2.65 4.03 2.68 4.04-.03.07-.42 1.44-1.38 2.83M15.97 6.37c.63-.77 1.06-1.85.94-2.93-.93.04-2.02.63-2.67 1.39-.58.67-1.09 1.76-.95 2.81 1.03.08 2.06-.52 2.68-1.27z" />
        </svg>
        <span>macOS</span>
      </button>
      <button
        type="button"
        className={cn('os-switch-btn', windowStyle === 'win' && 'active')}
        aria-pressed={windowStyle === 'win'}
        title={t('windowStyle.switchToWin')}
        onClick={() => switchWindowStyle('win')}
      >
        <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <path d="M0 3.449L9.75 2.1v9.451H0m10.949-9.602L24 0v11.4H10.949M0 12.6h9.75v9.451L0 20.699M10.949 12.6H24V24l-12.9-1.801" />
        </svg>
        <span>Windows 11</span>
      </button>
    </div>
  );
}

function switchWindowStyle(style: WindowStyle): void {
  applyWindowStyle(style);
  toast(t(style === 'mac' ? 'windowStyle.macToast' : 'windowStyle.winToast'));
}

// The window-style switch is intentionally hidden (not removed): the platform
// default already picks the right projection, and the component/CSS are kept so
// the switch can be restored later without rebuilding the chrome.
const WINDOW_STYLE_SWITCH_VISIBLE = false;

// 标题栏:高度随窗体样式投影(macOS 42px / Windows 11 40px),可拖拽。
// macOS 真机使用原生红绿灯;Windows 投影在所有平台自绘右侧控制键。
export function TitleBar() {
  const { windowStyle, version } = useAppSelector(
    (state) => ({ windowStyle: state.store.windowStyle, version: state.appInfo.version }),
    shallowEqual,
  );
  const background = useAppBackground();
  const isDarwin = desktop.platform() === 'darwin';

  return (
    <header
      className={cn(
        'titlebar-drag app-layer relative flex h-[var(--titlebar-h)] shrink-0 items-center justify-between border-b border-border bg-titlebar pl-3',
        windowStyle === 'win' ? 'pr-0' : 'pr-2',
        background.app && 'app-surface-veil border-b-transparent'
      )}
    >
      <div
        className={cn(
          'flex min-w-0 items-center gap-2',
          background.app && 'app-titlebar-surface rounded-lg py-0.5 pr-2 pl-1'
        )}
      >
        {windowStyle === 'mac' && !isDarwin ? <MacTrafficLights /> : null}
        <span className="brand-logo-small block size-5 shrink-0 rounded-[5px]">
          <MothxLogo />
        </span>
        <span
          className={cn(
            'text-[12.5px] font-semibold tracking-[.2px] text-strong',
            background.app && "text-strong [text-shadow:0_1px_2px_rgb(var(--app-outline-rgb)/.12)]"
          )}
        >
          MothX
        </span>
        <span
          className={cn(
            'rounded-lg bg-hoverbg px-1.5 py-px text-[10.5px] text-faint',
            background.app && 'border border-[rgb(var(--app-outline-rgb)/.1)] bg-[rgb(var(--app-outline-rgb)/.07)] text-muted-foreground'
          )}
        >
          v{version}
        </span>
      </div>

      {windowStyle === 'mac' ? (
        <div className="titlebar-center pointer-events-none absolute left-1/2 hidden -translate-x-1/2 text-[12px] tracking-[.2px] text-faint md:block">
          {t('titlebar.tagline')}
        </div>
      ) : null}

      <div className="titlebar-right flex items-center gap-2">
        {WINDOW_STYLE_SWITCH_VISIBLE ? <WindowStyleSwitch windowStyle={windowStyle} /> : null}
        {windowStyle === 'win' ? <WindowsWindowControls backgroundApp={background.app} /> : null}
      </div>
    </header>
  );
}
