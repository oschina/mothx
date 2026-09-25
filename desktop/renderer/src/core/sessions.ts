// Canonical session/project actions and in-memory history projections.
// Desktop never persists task grouping, pins, work directories, or history
// pages: ACP owns those facts and this module only renders its projections.

import { desktop, invoke } from './api';
import { requestChatScroll, requestComposerFocus } from './bus';
import { t } from './i18n';
import {
  emit,
  hasFeature,
  newSessionWorkspace,
  setSessionStatus,
  state,
  type ListedSessionShape,
  type NewSessionResultShape,
  type ProjectShape,
  type SessionListScope,
  type SessionPageState,
  type TranscriptPageShape,
} from './state';
import { sortTaskSessions } from './task-tree';
import { applyTranscriptPage, clearTranscript } from './transcript';
import { confirmDanger, promptModal, toast } from './ui-host';
import { switchView } from './views';
import { createWorktree, waitForWorktree, worktreeSupported } from './worktrees';

const RECENT_SESSION_LIMIT = 8;
const TRANSCRIPT_PAGE_SIZE = 40;

interface SessionListResult {
  sessions?: ListedSessionShape[];
  nextCursor?: string;
}

interface SessionListOptions {
  scope: SessionListScope;
  projectId?: string;
  query?: string;
}

function pageKey(prefix: string, options: SessionListOptions): string {
  return `${prefix}:${options.scope}:${options.projectId || ''}:${options.query || ''}`;
}

export function taskProjectPageKey(projectId: string): string {
  return pageKey('task', { scope: 'project', projectId });
}

export function taskUngroupedPageKey(): string {
  return pageKey('task', { scope: 'ungrouped' });
}

export function taskRecentPageKey(): string {
  return pageKey('task', { scope: 'all' });
}

function historyPageKey(): string {
  return pageKey('history', {
    scope: state.historyScope,
    projectId: state.historyProjectId,
    query: state.historyQuery,
  });
}

export function sessionListParams(options: SessionListOptions, cursor = ''): Record<string, unknown> {
  const params: Record<string, unknown> = {};
  if (cursor) params.cursor = cursor;
  if (options.scope !== 'all') params.scope = options.scope;
  if (options.projectId) params.projectId = options.projectId;
  if (options.query) params.query = options.query;
  return params;
}

function newPage(): SessionPageState {
  return { sessions: [], nextCursor: '', loading: false, loaded: false };
}

export function sessionPage(key: string): SessionPageState {
  return state.sessionPages[key] || newPage();
}

function rebuildSessionCache(): void {
  const byID = new Map<string, ListedSessionShape>();
  for (const page of Object.values(state.sessionPages)) {
    for (const session of page.sessions) {
      const existing = byID.get(session.sessionId);
      // Different list scopes can arrive with different summary completeness.
      // Keep the canonical Runtime title rather than letting a sparse recent
      // row replace it with an ID-only projection.
      byID.set(
        session.sessionId,
        existing?.title && !session.title ? { ...session, title: existing.title } : session,
      );
    }
  }
  state.sessions = [...byID.values()];
}

// 分页加载不再清空旧数据:加载期间保留现有条目(仅标记 loading),成功后
// 一次性原子替换 page 并 emit,避免列表清空重填造成的闪烁。
const pageRequests = new Map<string, Promise<void>>();

async function runPageLoad(key: string, options: SessionListOptions, reset: boolean): Promise<void> {
  const existingRequest = pageRequests.get(key);
  if (existingRequest) return existingRequest;
  const request = (async () => {
    const base = state.sessionPages[key];
    if (!reset && base) {
      if (base.loading || (base.loaded && !base.nextCursor)) return;
    }
    const cursor = reset ? '' : base?.nextCursor || '';
    const loadingPage: SessionPageState = reset
      ? { sessions: base?.sessions || [], nextCursor: base?.nextCursor || '', loading: true, loaded: base?.loaded ?? false }
      : { ...(base as SessionPageState), loading: true };
    state.sessionPages = { ...state.sessionPages, [key]: loadingPage };
    emit();
    try {
      const result = await invoke<SessionListResult>('mothx/session/listAll', sessionListParams(options, cursor));
      const byID = new Map<string, ListedSessionShape>();
      if (!reset) {
        for (const existing of loadingPage.sessions) byID.set(existing.sessionId, existing);
      }
      for (const session of result.sessions || []) byID.set(session.sessionId, session);
      const page: SessionPageState = {
        sessions: [...byID.values()],
        nextCursor: result.nextCursor || '',
        loading: false,
        loaded: true,
      };
      state.sessionPages = { ...state.sessionPages, [key]: page };
      rebuildSessionCache();
    } catch (error) {
      desktop.log(`mothx/session/listAll failed: ${error instanceof Error ? error.message : String(error)}`);
      state.sessionPages = { ...state.sessionPages, [key]: { ...loadingPage, loading: false } };
    }
    emit();
  })();
  pageRequests.set(key, request);
  void request.finally(() => {
    if (pageRequests.get(key) === request) pageRequests.delete(key);
  });
  return request;
}

// Refresh only the projections needed by the visible task tree. Project
// branches are intentionally lazy, so a large task library is never pulled
// through a 20-page client-side loop.
export async function refreshSessions(): Promise<void> {
  if (!hasFeature('sessionListAll')) return;
  const expanded = [...state.expandedProjectIds];
  state.sessionsLoading = true;
  emit();
  try {
    await Promise.all([
      runPageLoad(taskRecentPageKey(), { scope: 'all' }, true),
      runPageLoad(taskUngroupedPageKey(), { scope: 'ungrouped' }, true),
      ...expanded.map((projectId) => runPageLoad(taskProjectPageKey(projectId), { scope: 'project', projectId }, true)),
    ]);
  } finally {
    state.sessionsLoading = false;
    emit();
  }
}

export async function loadMoreTaskSessions(key: string, options: SessionListOptions): Promise<void> {
  await runPageLoad(key, options, false);
}

export async function toggleProjectExpanded(projectId: string): Promise<void> {
  const expanded = new Set(state.expandedProjectIds);
  if (expanded.has(projectId)) {
    expanded.delete(projectId);
    state.expandedProjectIds = [...expanded];
    emit();
    return;
  }
  expanded.add(projectId);
  state.expandedProjectIds = [...expanded];
  emit();
  const key = taskProjectPageKey(projectId);
  if (sessionPage(key).loaded) return;
  await runPageLoad(key, { scope: 'project', projectId }, true);
}

export function recentSessions(): ListedSessionShape[] {
  return sortTaskSessions(sessionPage(taskRecentPageKey()).sessions).slice(0, RECENT_SESSION_LIMIT);
}

export function ungroupedSessions(): ListedSessionShape[] {
  return sortTaskSessions(sessionPage(taskUngroupedPageKey()).sessions);
}

export async function refreshHistory(): Promise<void> {
  if (!hasFeature('sessionListAll')) return;
  if (state.historyScope === 'project' && !state.historyProjectId) {
    state.historyScope = 'all';
  }
  await runPageLoad(historyPageKey(), {
    scope: state.historyScope,
    projectId: state.historyProjectId,
    query: state.historyQuery,
  }, true);
}

export async function loadMoreHistory(): Promise<void> {
  await runPageLoad(historyPageKey(), {
    scope: state.historyScope,
    projectId: state.historyProjectId,
    query: state.historyQuery,
  }, false);
}

export function historySessions(): SessionPageState {
  return sessionPage(historyPageKey());
}

export async function setHistoryQuery(query: string): Promise<void> {
  state.historyQuery = query.trim();
  await refreshHistory();
}

export async function setHistoryScope(scope: SessionListScope, projectId = ''): Promise<void> {
  state.historyScope = scope;
  state.historyProjectId = scope === 'project' ? projectId : '';
  await refreshHistory();
}

export function sessionById(sessionId: string): ListedSessionShape | undefined {
  return state.sessions.find((entry) => entry.sessionId === sessionId);
}

export function sessionWorkingDirectory(sessionId: string): string {
  const listedCwd = sessionById(sessionId)?.cwd;
  if (listedCwd) return listedCwd;
  if (sessionId === state.activeSessionId) return state.activeSessionCwd;
  return '';
}

export async function openSession(sessionId: string): Promise<void> {
  const cwd = sessionWorkingDirectory(sessionId);
  if (!cwd || !sessionId) return;
  if (state.promptInFlight && state.activeSessionId && state.activeSessionId !== sessionId) {
    toast(t('prompt.busy'));
    return;
  }
  clearTranscript(sessionId);
  state.activeSessionId = sessionId;
  state.activeSessionCwd = cwd;
  state.activeTitle = sessionById(sessionId)?.title || sessionId.slice(0, 8);
  state.runStatus = 'loading';
  switchView('chat');
  emit();
  try {
    const params: Record<string, unknown> = {
      sessionId,
      cwd,
      _meta: { mothx: { workspace: { cwd } } },
    };
    if (hasFeature('sessionHistoryPaging')) params.historyLimit = TRANSCRIPT_PAGE_SIZE;
    const result = await invoke<NewSessionResultShape>('session/load', params);
    applySessionResult(result);
    const remembered = state.store.sessionStatus[sessionId];
    state.runStatus = remembered === 'failed' ? 'failed' : remembered === 'pending' ? 'pending' : 'completed';
  } catch (error) {
    state.runStatus = 'failed';
    toast(t('session.loadFailed', { e: error instanceof Error ? error.message : String(error) }));
  }
  requestChatScroll(true);
  emit();
}

export function applySessionResult(result: NewSessionResultShape | undefined | null): void {
  if (!result) return;
  if (Array.isArray(result.configOptions) && result.configOptions.length > 0) {
    state.configOptions = result.configOptions;
    const mode = result.configOptions.find((option) => option.id === 'mode');
    if (mode) state.currentMode = mode.currentValue;
  }
  if (result.modes?.currentModeId) state.currentMode = result.modes.currentModeId;
  if (result.history) applyTranscriptResult(result.history, false);
}

function applyTranscriptResult(page: TranscriptPageShape, prepend: boolean): void {
  if (!state.activeSessionId || page.sessionId !== state.activeSessionId) return;
  applyTranscriptPage(page.sessionId, page.updates || [], prepend);
  state.transcriptNextCursor = page.nextCursor || '';
  state.transcriptLoading = false;
}

export async function loadOlderTranscript(): Promise<void> {
  const sessionId = state.activeSessionId;
  if (!sessionId || !state.transcriptNextCursor || state.transcriptLoading || !hasFeature('sessionHistoryPaging')) return;
  state.transcriptLoading = true;
  emit();
  try {
    const page = await invoke<TranscriptPageShape>('mothx/session/history', {
      sessionId,
      cursor: state.transcriptNextCursor,
      limit: TRANSCRIPT_PAGE_SIZE,
    });
    applyTranscriptResult(page, true);
  } catch (error) {
    state.transcriptLoading = false;
    toast(error instanceof Error ? error.message : String(error));
  }
  emit();
}

export async function createSession(): Promise<{ sessionId: string; cwd: string }> {
  const cwd = newSessionWorkspace();
  if (!cwd) throw new Error('workspace is not ready');
  const result = await invoke<NewSessionResultShape>('session/new', {
    cwd,
    _meta: { mothx: { workspace: { cwd }, surface: 'desktop' } },
  });
  applySessionResult(result);
  return { sessionId: result.sessionId, cwd };
}

function resetTaskState(): void {
  state.activeSessionId = null;
  state.activeSessionCwd = '';
  state.activeTitle = '';
  state.runStatus = 'idle';
  state.promptInFlight = false;
  state.attachments = [];
  state.dirConfirmed = false;
  clearTranscript(null);
}

function enterNewTaskDraft(): void {
  resetTaskState();
  state.dirConfirmed = hasWorkingDirectory(newSessionWorkspace());
  switchView('home');
  emit();
}

// 新建任务:先落 home 草稿,随即弹出原生工作目录选择框,选中的目录成为这个
// 新会话的工作目录;取消选择时回退到已记住的默认目录(发送时仍会再次询问)。
export async function startNewTask(pickDirectory = true): Promise<void> {
  enterNewTaskDraft();
  if (pickDirectory) {
    const picked = await chooseWorkingDirectory();
    if (picked) {
      state.dirConfirmed = true;
      emit();
    }
  }
  requestComposerFocus('home');
}

export function hasWorkingDirectory(cwd: string): boolean {
  return cwd.trim() !== '';
}

export async function chooseWorkingDirectory(): Promise<boolean> {
  const picked = await desktop.chooseDirectory(newSessionWorkspace());
  if (!picked) return false;
  state.newSessionCwd = picked;
  void desktop.storeSet({ lastWorkspace: picked });
  state.dirConfirmed = true;
  emit();
  return true;
}

// createIsolatedWorktree materializes an isolated git worktree on demand and
// makes it the workspace of the active session (when idle) or of the next new
// session. The Runtime authorizes the directory; the renderer waits for the
// canonical ready/failed notification before switching to it.
export async function createIsolatedWorktree(): Promise<void> {
  if (!worktreeSupported()) return;
  const baseCwd = state.activeSessionCwd || newSessionWorkspace();
  if (!hasWorkingDirectory(baseCwd)) {
    toast(t('composer.worktreeFailed', { e: 'workspace is not ready' }));
    return;
  }
  const targetSession = state.activeSessionId;
  if (targetSession && state.promptInFlight) {
    toast(t('prompt.busy'));
    return;
  }
  try {
    const worktree = await createWorktree({ baseCwd });
    const readiness = await waitForWorktree(worktree.directory);
    if (readiness.status === 'failed') {
      toast(t('composer.worktreeFailed', { e: readiness.message }));
      return;
    }
    if (targetSession) {
      const result = await invoke<{ cwd?: string }>('mothx/session/setWorkDir', {
        sessionId: targetSession,
        cwd: worktree.directory,
        _meta: { mothx: { workspace: { cwd: worktree.directory } } },
      });
      if (state.activeSessionId === targetSession) {
        state.activeSessionCwd = result.cwd || worktree.directory;
        state.configOptions = [];
      }
      await refreshTaskLibrary();
      if (state.activeSessionId === targetSession) await openSession(targetSession);
    } else {
      state.newSessionCwd = worktree.directory;
      void desktop.storeSet({ lastWorkspace: worktree.directory });
      state.dirConfirmed = true;
    }
    toast(t('composer.worktreeReady'));
    emit();
  } catch (error) {
    toast(t('composer.worktreeFailed', { e: error instanceof Error ? error.message : String(error) }));
  }
}

async function refreshTaskLibrary(): Promise<void> {
  await refreshProjects();
  await refreshSessions();
  if (state.view === 'history') await refreshHistory();
}

export async function changeSessionWorkingDirectory(sessionId: string): Promise<void> {
  const current = sessionWorkingDirectory(sessionId);
  if (!current || !sessionId) return;
  if (state.promptInFlight && state.activeSessionId === sessionId) {
    toast(t('prompt.busy'));
    return;
  }
  const picked = await desktop.chooseDirectory(current);
  if (!picked || picked === current) return;
  try {
    const result = await invoke<{ cwd?: string }>('mothx/session/setWorkDir', {
      sessionId,
      cwd: picked,
      _meta: { mothx: { workspace: { cwd: picked } } },
    });
    if (state.activeSessionId === sessionId) {
      state.activeSessionCwd = result.cwd || picked;
      state.configOptions = [];
    }
    await refreshTaskLibrary();
    toast(t('session.workspaceChanged'));
    if (state.activeSessionId === sessionId) await openSession(sessionId);
  } catch (error) {
    toast(t('session.workspaceChangeFailed', { e: error instanceof Error ? error.message : String(error) }));
  }
}

export async function deleteSession(sessionId: string): Promise<void> {
  if (!await confirmDanger(t('chat.confirmDelete'))) return;
  const cwd = sessionWorkingDirectory(sessionId);
  if (!hasWorkingDirectory(cwd)) return;
  await invoke('session/close', { sessionId }).catch(() => undefined);
  try {
    await invoke('session/delete', { sessionId });
  } catch {
    try {
      await invoke('mothx/session/delete', { sessionId });
    } catch (error) {
      toast(t('session.deleteFailed', { e: error instanceof Error ? error.message : String(error) }));
      return;
    }
  }
  if (state.activeSessionId === sessionId) void startNewTask(false);
  toast(t('session.deleted'));
  await refreshTaskLibrary();
}

export async function renameSession(sessionId: string): Promise<void> {
  const current = sessionById(sessionId)?.title || '';
  const next = await promptModal({ title: t('modal.renameTitle'), initialValue: current, okLabel: t('modal.ok'), cancelLabel: t('modal.cancel') });
  if (!next || !hasWorkingDirectory(sessionWorkingDirectory(sessionId))) return;
  try {
    await invoke('mothx/session/setTitle', { sessionId, title: next });
    if (state.activeSessionId === sessionId) state.activeTitle = next;
    toast(t('session.renamed'));
    await refreshTaskLibrary();
  } catch (error) {
    toast(t('session.renameFailed', { e: error instanceof Error ? error.message : String(error) }));
  }
}

export async function forkSession(sessionId: string, expertId?: string): Promise<void> {
  const cwd = sessionWorkingDirectory(sessionId);
  if (!hasWorkingDirectory(cwd)) return;
  try {
    const result = await invoke<NewSessionResultShape>('session/fork', {
      sessionId,
      cwd,
      ...(expertId !== undefined ? { expertId } : {}),
      _meta: { mothx: { workspace: { cwd }, parentSessionId: sessionId } },
    });
    toast(t('session.forked'));
    await refreshTaskLibrary();
    if (result.sessionId) await openSession(result.sessionId);
  } catch (error) {
    toast(t('session.forkFailed', { e: error instanceof Error ? error.message : String(error) }));
  }
}

export function isPinned(sessionId: string): boolean {
  return hasFeature('sessionMeta') && !!sessionById(sessionId)?._meta?.pinned;
}

export async function togglePin(sessionId: string): Promise<void> {
  if (!hasFeature('sessionMeta')) {
    toast(t('projects.unsupported'));
    return;
  }
  const cwd = sessionWorkingDirectory(sessionId);
  if (!hasWorkingDirectory(cwd)) return;
  try {
    await invoke('mothx/session/setMeta', {
      sessionId,
      pinned: !isPinned(sessionId),
      _meta: { mothx: { workspace: { cwd } } },
    });
    await refreshTaskLibrary();
  } catch (error) {
    toast(error instanceof Error ? error.message : String(error));
  }
}

export async function setSessionProject(sessionId: string, projectId: string | null): Promise<void> {
  if (!hasFeature('projects') || !hasFeature('sessionMeta')) {
    toast(t('projects.unsupported'));
    return;
  }
  const cwd = sessionWorkingDirectory(sessionId);
  if (!hasWorkingDirectory(cwd)) return;
  try {
    await invoke('mothx/session/setMeta', {
      sessionId,
      projectId,
      _meta: { mothx: { workspace: { cwd } } },
    });
    await refreshTaskLibrary();
  } catch (error) {
    toast(error instanceof Error ? error.message : String(error));
  }
}

export async function refreshProjects(): Promise<void> {
  if (!hasFeature('projects')) {
    state.projects = [];
    emit();
    return;
  }
  try {
    const result = await invoke<{ projects?: ProjectShape[] }>('mothx/projects/list', {});
    state.projects = result.projects || [];
  } catch (error) {
    desktop.log(`projects/list failed: ${error instanceof Error ? error.message : String(error)}`);
  }
  emit();
}

export async function createProject(): Promise<ProjectShape | null> {
  if (!hasFeature('projects')) {
    toast(t('projects.unsupported'));
    return null;
  }
  const name = await promptModal({ title: t('projects.new'), initialValue: '', okLabel: t('modal.ok'), cancelLabel: t('modal.cancel') });
  if (!name) return null;
  try {
    const project = await invoke<ProjectShape>('mothx/projects/create', { name });
    await refreshProjects();
    return project;
  } catch (error) {
    toast(error instanceof Error ? error.message : String(error));
    return null;
  }
}

export async function createProjectAndAssign(sessionId: string): Promise<void> {
  const project = await createProject();
  if (project) await setSessionProject(sessionId, project.id);
}

export async function renameProject(id: string, current: string): Promise<void> {
  const name = await promptModal({ title: t('projects.rename'), initialValue: current, okLabel: t('modal.ok'), cancelLabel: t('modal.cancel') });
  if (!name) return;
  try {
    await invoke('mothx/projects/rename', { id, name });
    await refreshTaskLibrary();
  } catch (error) {
    toast(error instanceof Error ? error.message : String(error));
  }
}

export async function deleteProject(id: string): Promise<void> {
  if (!await confirmDanger(t('projects.confirmDelete'))) return;
  try {
    await invoke('mothx/projects/delete', { id });
    state.expandedProjectIds = state.expandedProjectIds.filter((entry) => entry !== id);
    await refreshTaskLibrary();
  } catch (error) {
    toast(error instanceof Error ? error.message : String(error));
  }
}

export function rememberActiveStatus(status: string): void {
  if (state.activeSessionId) setSessionStatus(state.activeSessionId, status);
}
