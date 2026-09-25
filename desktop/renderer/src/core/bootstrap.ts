// renderer 启动与事件桥接:ACP 事件 → core 投影 → emit → React 重渲染。

import { acp, desktop, type RendererEvent } from './api';
import { notifyCronCompleted } from './automation';
import { refreshDraftConfigOptions } from './composer';
import { setLocale } from './i18n';
import { refreshProjects, refreshSessions } from './sessions';
import { emit, state } from './state';
import { applyHomeBackground, applyTheme } from './theme';
import { applyReverseRequest, applySessionEvent, applySessionUpdate } from './transcript';
import { applyWorktreeStatus } from './worktrees';

export function handleEvent(event: RendererEvent): void {
  switch (event.type) {
    case 'state':
      state.connection = event.snapshot;
      if (event.snapshot.state === 'ready') {
        desktop.log('renderer conn ready');
        void refreshSessions();
        void refreshProjects();
        void refreshDraftConfigOptions();
      }
      emit();
      return;
    case 'session-update':
      applySessionUpdate(event.sessionId, event.update as Record<string, unknown>);
      emit();
      return;
    case 'session-event':
      applySessionEvent(event.event as Record<string, unknown>);
      if (String((event.event as Record<string, unknown>)?.event || '') === 'cron_completed') {
        notifyCronCompleted(event.event as Record<string, unknown>);
      }
      emit();
      return;
    case 'reverse-request':
      applyReverseRequest(event.id, event.method, (event.params || {}) as Record<string, unknown>);
      emit();
      return;
    case 'worktree-status':
      applyWorktreeStatus(event.worktree);
      emit();
      return;
    default:
      return;
  }
}

// bootstrapStore 加载 Desktop 本地偏好(store/appInfo/locale/theme)并返回
// 根壳层背景应用函数需要的初始数据。React 根组件挂载前调用。
export async function bootstrapStore(): Promise<void> {
  const store = await desktop.storeGet().catch(() => null);
  if (store) state.store = store;
  state.appInfo = await desktop.appInfo().catch(() => state.appInfo);
  setLocale(state.store.locale);
  applyTheme(state.store.theme);
}

// bootstrapConnection 订阅 ACP 事件并同步一次连接状态;ready 时加载任务库。
export async function bootstrapConnection(): Promise<void> {
  acp.onEvent(handleEvent);
  state.connection = await acp.getState().catch(() => state.connection);
  if (state.connection.state === 'ready') {
    desktop.log('renderer conn ready');
    state.newSessionCwd = state.store.lastWorkspace || await desktop.defaultNewSessionDirectory();
    state.dirConfirmed = state.newSessionCwd !== '';
    await refreshSessions();
    await refreshDraftConfigOptions();
    void refreshProjects();
  }
  emit();
}

export function applyPlatformClass(): void {
  document.body.classList.add(`platform-${desktop.platform()}`);
}

export { applyHomeBackground };
