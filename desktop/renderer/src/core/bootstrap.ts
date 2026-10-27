// renderer 启动与事件桥接:ACP 事件 → core 投影 → emit → React 重渲染。

import { acp, desktop, type RendererEvent } from './api';
import { notifyCronCompleted } from './automation';
import { refreshDraftConfigOptions } from './composer';
import { setLocale } from './i18n';
import { refreshProjects, refreshSessions } from './sessions';
import { emit, isCurrentDraftWorkspace, state } from './state';
import { applyHomeBackground, applyTheme, applyWindowStyleClass } from './theme';
import { applyReverseRequest, applySessionEvent, applySessionUpdate } from './transcript';
import { applyWorktreeStatus } from './worktrees';

let quickStartInitialization: Promise<boolean> | null = null;

async function initializeQuickStartWorkspace(): Promise<boolean> {
  if (state.activeSessionId || state.draftWorkspaceSource !== 'quick-start' || state.newSessionCwd) return false;
  if (quickStartInitialization) return quickStartInitialization;
  const generation = state.draftGeneration;
  quickStartInitialization = desktop.defaultNewSessionDirectory().then((cwd) => {
    if (!isCurrentDraftWorkspace('quick-start', '', generation)) return false;
    state.newSessionCwd = cwd;
    return true;
  }).finally(() => {
    quickStartInitialization = null;
  });
  return quickStartInitialization;
}

export function handleEvent(event: RendererEvent): void {
  switch (event.type) {
    case 'state':
      state.connection = event.snapshot;
      if (event.snapshot.state === 'ready') {
        desktop.log('renderer conn ready');
        // Do not paint a ready connection before its quick-start scratch cwd
        // exists. Otherwise a person can submit in the tiny recovery window
        // between the ready event and the privileged directory request.
        void initializeQuickStartWorkspace().then(
          () => {
            void Promise.allSettled([refreshSessions(), refreshProjects(), refreshDraftConfigOptions()]);
            emit();
          },
          (error: unknown) => {
            desktop.log(`quick-start scratch directory failed: ${error instanceof Error ? error.message : String(error)}`);
            emit();
          },
        );
        return;
      }
      emit();
      return;
    case 'session-update':
      // applySessionUpdate owns the single render notification for this event
      // (including meta-only updates); emitting here doubled every token.
      applySessionUpdate(event.sessionId, event.update as Record<string, unknown>);
      return;
    case 'session-event':
      applySessionEvent(event.event as Record<string, unknown>);
      if (String((event.event as Record<string, unknown>)?.event || '') === 'cron_completed') {
        notifyCronCompleted(event.event as Record<string, unknown>);
      }
      return;
    case 'reverse-request':
      applyReverseRequest(event.id, event.method, (event.params || {}) as Record<string, unknown>);
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
  // Both reads are independent IPC round-trips; start them together so the
  // first paint does not wait for two sequential process hops.
  const [store, appInfo] = await Promise.all([
    desktop.storeGet().catch(() => null),
    desktop.appInfo().catch(() => null),
  ]);
  if (store) state.store = store;
  if (appInfo) state.appInfo = appInfo;
  setLocale(state.store.locale);
  applyTheme(state.store.theme);
  applyWindowStyleClass(state.store.windowStyle);
}

// bootstrapConnection 订阅 ACP 事件并同步一次连接状态;ready 时加载任务库。
export async function bootstrapConnection(): Promise<void> {
  acp.onEvent(handleEvent);
  state.connection = await acp.getState().catch(() => state.connection);
  if (state.connection.state === 'ready') {
    desktop.log('renderer conn ready');
    await Promise.all([
      initializeQuickStartWorkspace(),
      refreshSessions(),
      refreshDraftConfigOptions(),
    ]);
    void refreshProjects();
  }
  emit();
}

export function applyPlatformClass(): void {
  document.body.classList.add(`platform-${desktop.platform()}`);
}

export { applyHomeBackground };
