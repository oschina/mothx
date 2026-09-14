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
import { useAppState } from '@/hooks/useAppState';
import { useAppBackground } from '@/hooks/useAppBackground';
import { cn } from '@/lib/utils';

export function App() {
  const appState = useAppState();
  const background = useAppBackground();
  const shellRef = useRef<HTMLDivElement>(null);
  const store = appState.store;

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
            background.app ? 'app-surface-veil' : 'bg-background'
          )}
        >
          {appState.view === 'home' ? <HomeView /> : null}
          {appState.view === 'chat' ? <ChatView /> : null}
          {appState.view === 'skills' ? <SkillsView /> : null}
          {appState.view === 'history' ? <HistoryView /> : null}
          {appState.view === 'automation' ? <AutomationView /> : null}
          {appState.view === 'library' ? <LibraryView /> : null}
          {appState.view === 'settings' ? <SettingsView /> : null}
        </main>
      </div>
      <StatusBar />
      <UiHost />
      <Toaster />
    </div>
  );
}
