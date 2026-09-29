// Regression test for task grouping refresh: assigning a session to a
// project must refresh every cached projection immediately, including a
// collapsed project branch whose page was loaded earlier. Before the fix the
// session vanished from "ungrouped" but never appeared under its group until
// an unrelated later refresh (e.g. ungroup + regroup) reloaded the page.
import assert from 'node:assert/strict';
import test from 'node:test';

import { state } from './state.ts';
import {
  deleteSession,
  evictSessionFromPages,
  recentSessions,
  refreshSessions,
  sessionPage,
  setSessionProject,
  taskProjectPageKey,
  taskUngroupedPageKey,
  toggleProjectExpanded,
  ungroupedSessions,
} from './sessions.ts';
import { registerUiHost } from './ui-host.ts';

interface FakeSession {
  sessionId: string;
  cwd: string;
  title: string;
  updatedAt: string;
  projectId: string | null;
}

const fakeSessions: FakeSession[] = [
  { sessionId: 's1', cwd: '/w/s1', title: 'S1', updatedAt: '2026-01-02T00:00:00Z', projectId: null },
  { sessionId: 's2', cwd: '/w/s2', title: 'S2', updatedAt: '2026-01-01T00:00:00Z', projectId: null },
];
const fakeProjects = [{ id: 'p1', name: 'Group One' }];
let listAllCalls: Record<string, unknown>[] = [];

function projectOf(sessionId: string): string | null {
  return fakeSessions.find((entry) => entry.sessionId === sessionId)?.projectId ?? null;
}

function listedShape(session: FakeSession) {
  return {
    sessionId: session.sessionId,
    cwd: session.cwd,
    title: session.title,
    provider: 'p',
    model: 'm',
    updatedAt: session.updatedAt,
    _meta: { pinned: false, projectId: session.projectId },
  };
}

async function fakeInvoke(method: string, params?: unknown): Promise<{ ok: true; result: unknown }> {
  switch (method) {
    case 'mothx/session/listAll': {
      const query = (params || {}) as Record<string, unknown>;
      listAllCalls.push(query);
      const scope = String(query.scope || 'all');
      const projectId = String(query.projectId || '');
      const sessions = fakeSessions.filter((session) => {
        if (scope === 'ungrouped') return !session.projectId;
        if (scope === 'project') return session.projectId === projectId;
        return true;
      });
      return { ok: true, result: { sessions: sessions.map(listedShape), nextCursor: '' } };
    }
    case 'mothx/session/setMeta': {
      const request = (params || {}) as Record<string, unknown>;
      const session = fakeSessions.find((entry) => entry.sessionId === String(request.sessionId));
      assert.ok(session, 'setMeta must target a known session');
      session.projectId = request.projectId == null ? null : String(request.projectId);
      return { ok: true, result: { pinned: false, projectId: session.projectId, updatedAt: session.updatedAt } };
    }
    case 'mothx/projects/list': {
      return {
        ok: true,
        result: {
          projects: fakeProjects.map((project) => ({
            ...project,
            sessionCount: fakeSessions.filter((session) => session.projectId === project.id).length,
          })),
        },
      };
    }
    case 'session/delete': {
      const request = (params || {}) as Record<string, unknown>;
      const index = fakeSessions.findIndex((entry) => entry.sessionId === String(request.sessionId));
      if (index >= 0) fakeSessions.splice(index, 1);
      return { ok: true, result: {} };
    }
    default:
      return { ok: true, result: {} };
  }
}

const fakeWindow = {
  mothx: {
    isDesktop: true,
    platform: 'linux',
    acp: {
      invoke: fakeInvoke,
      notify: () => undefined,
      respond: () => undefined,
      cancelReverse: () => undefined,
      getState: async () => ({ state: 'ready', workspace: '' }),
      restart: async () => ({ ok: true as const, result: {} }),
      onEvent: () => () => undefined,
    },
    desktop: {
      logDiagnostic: () => undefined,
      storeGet: async () => state.store,
      storeSet: async (patch: Record<string, unknown>) => Object.assign(state.store, patch),
    },
  },
};

const previousWindow = (globalThis as { window?: unknown }).window;
const previousState = {
  connection: state.connection,
  projects: state.projects,
  sessions: state.sessions,
  sessionPages: state.sessionPages,
  expandedProjectIds: state.expandedProjectIds,
  view: state.view,
};
(globalThis as { window?: unknown }).window = fakeWindow;
state.connection = {
  state: 'ready',
  workspace: '',
  agentCapabilities: { _meta: { 'mothx.dev': { features: ['sessionListAll', 'projects', 'sessionMeta'] } } },
};
state.view = 'home';

try {
  await test('grouping a session refreshes a collapsed cached project page immediately', async () => {
    await refreshSessions();
    // Load the p1 branch once, then collapse it: its page cache stays behind.
    await toggleProjectExpanded('p1');
    await toggleProjectExpanded('p1');
    assert.equal(state.expandedProjectIds.includes('p1'), false);
    assert.equal(sessionPage(taskProjectPageKey('p1')).loaded, true);

    listAllCalls = [];
    await setSessionProject('s1', 'p1');

    // The collapsed cached branch must be revalidated even though it is not
    // expanded, so the projection never goes stale after a grouping change.
    const projectKeys = listAllCalls
      .filter((call) => call.scope === 'project')
      .map((call) => String(call.projectId || ''));
    assert.ok(projectKeys.includes('p1'), 'refresh must reload the cached project page while collapsed');

    const page = sessionPage(taskProjectPageKey('p1'));
    assert.ok(page.sessions.some((session) => session.sessionId === 's1'), 'grouped session must appear in its project page');
    assert.ok(!ungroupedSessions().some((session) => session.sessionId === 's1'), 'grouped session must leave the ungrouped list');
    assert.equal(state.projects.find((project) => project.id === 'p1')?.sessionCount, 1, 'project count badge must refresh');
  });

  await test('expanding a cached branch revalidates it instead of trusting the stale cache', async () => {
    // Simulate an out-of-band backend change while the branch stays collapsed.
    const s1 = fakeSessions.find((entry) => entry.sessionId === 's1');
    assert.ok(s1);
    s1.projectId = null;

    listAllCalls = [];
    await toggleProjectExpanded('p1');

    const reloaded = listAllCalls.some((call) => call.scope === 'project' && String(call.projectId || '') === 'p1');
    assert.ok(reloaded, 'expand must revalidate a previously loaded page');
    const page = sessionPage(taskProjectPageKey('p1'));
    assert.ok(!page.sessions.some((session) => session.sessionId === 's1'), 'stale membership must not survive the expand');

    // Restore the grouping for any later assertions and collapse again.
    s1.projectId = 'p1';
    await toggleProjectExpanded('p1');
  });

  await test('ungrouping a session refreshes both the ungrouped list and the cached project page', async () => {
    await refreshSessions();
    assert.ok(sessionPage(taskProjectPageKey('p1')).sessions.some((session) => session.sessionId === 's1'));

    await setSessionProject('s1', null);

    assert.ok(ungroupedSessions().some((session) => session.sessionId === 's1'), 'session must return to ungrouped');
    const page = sessionPage(taskProjectPageKey('p1'));
    assert.ok(!page.sessions.some((session) => session.sessionId === 's1'), 'project page must drop the removed session');
    assert.ok(sessionPage(taskUngroupedPageKey()).loaded, 'ungrouped page must stay loaded');
    assert.equal(projectOf('s1'), null);
  });

  // 删除回归:会话从所有已缓存投影中消失,项目展开的分支也不能再留着它。
  await test('deleting a session removes it from every cached projection', async () => {
    const unregister = registerUiHost({
      toast: () => undefined,
      prompt: async () => null,
      confirm: async () => true,
      previewImage: () => undefined,
    });
    try {
      await setSessionProject('s1', 'p1');
      await toggleProjectExpanded('p1');
      await refreshSessions();
      assert.ok(sessionPage(taskProjectPageKey('p1')).sessions.some((session) => session.sessionId === 's1'));

      await deleteSession('s1');

      const visible = [
        ...sessionPage(taskProjectPageKey('p1')).sessions,
        ...sessionPage(taskUngroupedPageKey()).sessions,
        ...recentSessions(),
        ...ungroupedSessions(),
        ...state.sessions,
      ];
      assert.ok(
        !visible.some((session) => session.sessionId === 's1'),
        'a deleted session must disappear from the project branch and every task list',
      );
      assert.equal(projectOf('s1'), null, 'the durable session must be gone');
    } finally {
      unregister();
    }
  });

  // 即使某个已缓存页面仍然持有旧行(例如在删除前就加载过),主动驱逐也必须
  // 清掉它:删除是 Runtime 的持久事实,不能依赖某一次刷新恰好成功。
  await test('evictSessionFromPages drops the row from a page cached before the delete', async () => {
    await setSessionProject('s2', 'p1');
    await toggleProjectExpanded('p1');
    assert.ok(sessionPage(taskProjectPageKey('p1')).sessions.some((session) => session.sessionId === 's2'));

    evictSessionFromPages('s2');

    assert.ok(!sessionPage(taskProjectPageKey('p1')).sessions.some((session) => session.sessionId === 's2'));
    assert.ok(!state.sessions.some((session) => session.sessionId === 's2'));
    assert.ok(!recentSessions().some((session) => session.sessionId === 's2'));
  });
} finally {
  (globalThis as { window?: unknown }).window = previousWindow;
  state.connection = previousState.connection;
  state.projects = previousState.projects;
  state.sessions = previousState.sessions;
  state.sessionPages = previousState.sessionPages;
  state.expandedProjectIds = previousState.expandedProjectIds;
  state.view = previousState.view;
}
