// 应用骨架:标题栏 / 侧边栏 / 视图路由 / 状态栏 + UI 宿主(toast、对话框、
// 图片预览)。视图按 state.view 条件挂载,挂载即触发各自的懒加载。

import { useEffect, useRef } from 'react';

import { ChatView } from '@/views/ChatView';
import { HomeView } from '@/views/HomeView';
import { SkillsView } from '@/views/SkillsView';
import { HistoryView } from '@/views/HistoryView';
import { AutomationView } from '@/views/AutomationView';
import { LibraryView } from '@/views/LibraryView';
import { SettingsView } from '@/views/settings/SettingsView';
import { Sidebar } from '@/components/Sidebar';
import { StatusBar } from '@/components/StatusBar';
import { TitleBar } from '@/components/TitleBar';
import { UiHost } from '@/components/UiHost';
import { Toaster } from '@/components/ui/sonner';
import { desktop } from '@/core/api';
import { applyHomeBackground } from '@/core/theme';
import { shallowEqual, useAppSelector } from '@/hooks/useAppState';
import { useAppBackground } from '@/hooks/useAppBackground';
import { cn } from '@/lib/utils';

export function App() {
  const view = useAppSelector((appState) => appState.view);
  const store = useAppSelector(
    (appState) => ({
      homeBackgroundImage: appState.store.homeBackgroundImage,
      homeBackgroundOpacity: appState.store.homeBackgroundOpacity,
      homeBackgroundBlur: appState.store.homeBackgroundBlur,
      homeBackgroundScope: appState.store.homeBackgroundScope,
      homeBackgroundFit: appState.store.homeBackgroundFit,
      homeBackgroundPosition: appState.store.homeBackgroundPosition,
    }),
    shallowEqual,
  );
  const background = useAppBackground();
  const shellRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    document.body.classList.add(`platform-${desktop.platform()}`);
  }, []);

  useEffect(() => {
    applyHomeBackground(shellRef.current);
  }, [
    store.homeBackgroundImage,
    store.homeBackgroundOpacity,
    store.homeBackgroundBlur,
    store.homeBackgroundScope,
    store.homeBackgroundFit,
    store.homeBackgroundPosition,
  ]);

  return (
    <div ref={shellRef} className="app-shell flex h-screen flex-col overflow-hidden">
      <TitleBar />
      <div className="app-layer flex min-h-0 flex-1">
        <Sidebar />
        <main
          className={cn(
            'relative min-w-0 flex-1 overflow-hidden',
            background.app ? 'app-surface-veil' : 'app-canvas'
          )}
        >
          {view === 'home' ? <HomeView /> : null}
          {view === 'chat' ? <ChatView /> : null}
          {view === 'skills' ? <SkillsView /> : null}
          {view === 'history' ? <HistoryView /> : null}
          {view === 'automation' ? <AutomationView /> : null}
          {view === 'library' ? <LibraryView /> : null}
          {view === 'settings' ? <SettingsView /> : null}
        </main>
      </div>
      <StatusBar />
      <UiHost />
      <Toaster />
    </div>
  );
}
