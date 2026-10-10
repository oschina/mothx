// 回归测试:多任务并行(bug 6/7)与删除后残留(bug 4)。
//
// Desktop 之前用一个全局 promptInFlight 标志描述“正在运行”,并把
// transcriptSessionId 为空当作“匹配任何会话”。两个后果:
//   1. 任务 A 运行中无法切换/新建任务 B,输入区也被禁用;
//   2. B 的流式内容落进 A 的转录(草稿态 transcriptSessionId 为空时,
//      后台会话的 agent_message_chunk 会被当成当前会话渲染)。
// 现在 Run 状态按会话投影,转录路由规则只有一条:内容属于拥有它的会话。
import assert from 'node:assert/strict';
import test from 'node:test';

import { isSessionRunning, sessionOwnsTranscript, sessionRunStatus, setSessionRunStatus, state } from './state.ts';
import { openSession } from './sessions.ts';
import { cancelRun } from './composer.ts';
import { applyReverseRequest, applySessionEvent, applySessionUpdate, clearTranscript, resolvePermission } from './transcript.ts';

// setSessionRunStatus 会把状态点写回 Desktop 本地 store,因此测试需要一个
// 最小 preload 桥(只实现 storeSet/log),不引入任何真实 ACP 通道。
const handlers = new Map<string, (params: unknown) => unknown>();
const previousWindow = (globalThis as { window?: unknown }).window;
(globalThis as { window?: unknown }).window = {
  mothx: {
    isDesktop: true,
    platform: 'linux',
    acp: {
      invoke: async (method: string, params?: unknown) => ({ ok: true, result: handlers.get(method)?.(params) ?? {} }),
      notify: () => undefined,
      respond: () => undefined,
      cancelReverse: () => undefined,
      getState: async () => ({ state: 'ready', workspace: '' }),
      restart: async () => ({ ok: true, result: {} }),
      onEvent: () => () => undefined,
    },
    desktop: { logDiagnostic: () => undefined, storeGet: async () => state.store, storeSet: async (patch: Record<string, unknown>) => Object.assign(state.store, patch) },
  },
};

function reset(): void {
  state.transcript = [];
  state.transcriptSessionId = null;
  state.activeSessionId = null;
  state.runningSessions = {};
  state.pendingDecisions = {};
  state.pendingUserKey = null;
  state.currentPlanKey = null;
}

test.after(() => {
  (globalThis as { window?: unknown }).window = previousWindow;
});

// 任务切换:另一个任务正在运行时,openSession 必须照常切换过去(并把该会话
// 自己已记住的状态恢复出来),不能弹“当前任务还在运行”。
test('switching tasks works while another task keeps running', async () => {
  reset();
  const previousSessions = state.sessions;
  const previousPages = state.sessionPages;
  state.sessions = [
    { sessionId: 'task-a', cwd: '/w/a', provider: 'p', model: 'm' },
    { sessionId: 'task-b', cwd: '/w/b', provider: 'p', model: 'm' },
  ];
  state.sessionPages = {};
  state.store.sessionStatus = { 'task-a': 'working' };
  state.activeSessionId = 'task-a';
  state.transcriptSessionId = 'task-a';
  setSessionRunStatus('task-a', 'working');
  handlers.set('session/load', (params) => ({ sessionId: (params as { sessionId: string }).sessionId }));
  try {
    await openSession('task-b');
    assert.equal(state.activeSessionId, 'task-b', 'a running task must not block the task switch');
    assert.equal(sessionRunStatus('task-a'), 'working', 'the background task keeps running');
    assert.equal(sessionRunStatus('task-b'), 'completed');
    assert.equal(state.transcriptSessionId, 'task-b', 'the transcript belongs to the task that was opened');
  } finally {
    handlers.delete('session/load');
    state.sessions = previousSessions;
    state.sessionPages = previousPages;
    reset();
  }
});

// 切回一个仍在运行的任务:必须保持“执行中”,不能被 session/load 的加载态
// 覆盖成“加载中”后永远停在忙碌。
test('reattaching to a running task keeps its live run status', async () => {
  reset();
  const previousSessions = state.sessions;
  state.sessions = [{ sessionId: 'task-a', cwd: '/w/a', provider: 'p', model: 'm' }];
  state.activeSessionId = null;
  state.transcriptSessionId = null;
  setSessionRunStatus('task-a', 'working');
  handlers.set('session/load', () => ({ sessionId: 'task-a' }));
  try {
    await openSession('task-a');
    assert.equal(sessionRunStatus('task-a'), 'working', 'a reattached run must stay visible as running');
    assert.equal(isSessionRunning('task-a'), true);
  } finally {
    handlers.delete('session/load');
    state.sessions = previousSessions;
    reset();
  }
});

test('a late session load cannot overwrite the session opened afterwards', async () => {
  reset();
  const previousSessions = state.sessions;
  const previousConfig = state.configOptions;
  const previousMode = state.currentMode;
  state.sessions = [
    { sessionId: 'task-a', cwd: '/w/a', provider: 'p', model: 'm' },
    { sessionId: 'task-b', cwd: '/w/b', provider: 'p', model: 'm' },
  ];
  let resolveA: ((value: unknown) => void) | undefined;
  handlers.set('session/load', (params) => {
    const sessionId = (params as { sessionId: string }).sessionId;
    if (sessionId === 'task-a') return new Promise((resolve) => { resolveA = resolve; });
    return {
      sessionId,
      configOptions: [{ id: 'mode', type: 'select', name: 'Mode', currentValue: 'yolo' }],
      modes: { currentModeId: 'yolo' },
      history: { sessionId, updates: [] },
    };
  });
  try {
    const openingA = openSession('task-a');
    await Promise.resolve();
    await openSession('task-b');
    resolveA?.({
      sessionId: 'task-a',
      configOptions: [{ id: 'mode', type: 'select', name: 'Mode', currentValue: 'plan' }],
      modes: { currentModeId: 'plan' },
      history: { sessionId: 'task-a', updates: [{ sessionUpdate: 'agent_message_chunk', messageId: 'late', content: { type: 'text', text: 'late A' } }] },
    });
    await openingA;
    assert.equal(state.activeSessionId, 'task-b');
    assert.equal(state.currentMode, 'yolo', 'late A config must not replace B mode');
    assert.equal(state.configOptions.find((option) => option.id === 'mode')?.currentValue, 'yolo');
    assert.equal(state.transcriptSessionId, 'task-b');
    assert.equal(state.transcript.length, 0, 'late A history must not enter B transcript');
  } finally {
    handlers.delete('session/load');
    state.sessions = previousSessions;
    state.configOptions = previousConfig;
    state.currentMode = previousMode;
    reset();
  }
});

test('a background run never makes the active task look busy', () => {
  reset();
  try {
    setSessionRunStatus('task-a', 'working');
    assert.equal(isSessionRunning('task-a'), true);
    assert.equal(isSessionRunning('task-b'), false, 'an unrelated task must stay idle');
    state.activeSessionId = 'task-b';
    assert.equal(sessionRunStatus('task-b'), 'idle');
  } finally {
    reset();
  }
});

test('cancelling keeps only that session submission-blocked until Runtime confirms terminal state', () => {
  reset();
  try {
    state.activeSessionId = 'task-a';
    state.transcriptSessionId = 'task-a';
    setSessionRunStatus('task-a', 'working');
    setSessionRunStatus('task-b', 'working');
    cancelRun();
    assert.equal(sessionRunStatus('task-a'), 'cancelling');
    assert.equal(isSessionRunning('task-a'), true, 'cancelling must still block another prompt for A');
    assert.equal(sessionRunStatus('task-b'), 'working', 'cancelling A must not alter B');
    applySessionEvent({ sessionId: 'task-a', event: 'run_status', status: 'running' });
    assert.equal(sessionRunStatus('task-a'), 'cancelling', 'a late running pulse must not reopen a cancelling session');
    applySessionEvent({ sessionId: 'task-a', event: 'terminal', status: 'cancelled' });
    assert.equal(sessionRunStatus('task-a'), 'cancelled');
    assert.equal(isSessionRunning('task-a'), false);
  } finally {
    reset();
  }
});

test('resolving a decision while cancelling does not reopen that session', () => {
  reset();
  try {
    state.activeSessionId = 'task-a';
    state.transcriptSessionId = 'task-a';
    setSessionRunStatus('task-a', 'working');
    applyReverseRequest('decision-a', 'session/request_permission', {
      sessionId: 'task-a',
      toolCall: { toolCallId: 'tool-a', title: 'write', kind: 'edit' },
      options: [{ optionId: 'allow', name: 'Allow', kind: 'allow_once' }],
    });
    assert.equal(sessionRunStatus('task-a'), 'pending');
    cancelRun();
    assert.equal(sessionRunStatus('task-a'), 'cancelling');
    const permission = state.transcript.find((entry) => entry.kind === 'permission');
    assert.ok(permission && permission.kind === 'permission');
    // Resolving the last decision must not reopen a session that is still
    // cancelling; only a Runtime terminal confirmation may change that.
    resolvePermission(permission, 'allow');
    assert.equal(sessionRunStatus('task-a'), 'cancelling');
    applySessionEvent({ sessionId: 'task-a', event: 'terminal', status: 'cancelled' });
    assert.equal(sessionRunStatus('task-a'), 'cancelled');
  } finally {
    reset();
  }
});

test('starting a second task does not block the composer or the session switch', () => {
  reset();
  try {
    state.activeSessionId = 'task-a';
    state.transcriptSessionId = 'task-a';
    setSessionRunStatus('task-a', 'working');

    // 新任务草稿:没有绑定会话,输入区不得被视为忙碌。
    state.activeSessionId = null;
    state.transcriptSessionId = null;
    assert.equal(isSessionRunning(state.activeSessionId), false, 'a draft must not inherit another task run');

    // 回到运行中的任务:它仍然是运行中,可以取消,但不影响别人。
    state.activeSessionId = 'task-a';
    assert.equal(isSessionRunning('task-a'), true);
  } finally {
    reset();
  }
});

test('streamed output of a background session never enters the active transcript', () => {
  reset();
  try {
    state.activeSessionId = 'task-b';
    state.transcriptSessionId = 'task-b';
    applySessionUpdate('task-a', { sessionUpdate: 'agent_message_chunk', messageId: 'm1', content: { type: 'text', text: 'A 的输出' } });
    assert.equal(state.transcript.length, 0, 'a background session must not write into the visible transcript');

    applySessionUpdate('task-b', { sessionUpdate: 'agent_message_chunk', messageId: 'm2', content: { type: 'text', text: 'B 的输出' } });
    assert.equal(state.transcript.length, 1);
    assert.equal(state.transcript[0].kind === 'user' || state.transcript[0].kind === 'agent' ? state.transcript[0].text : '', 'B 的输出');
  } finally {
    reset();
  }
});

test('a draft owns no transcript, so a running task cannot stream into it', () => {
  reset();
  try {
    state.activeSessionId = null;
    state.transcriptSessionId = null;
    applySessionUpdate('task-a', { sessionUpdate: 'agent_message_chunk', messageId: 'm1', content: { type: 'text', text: 'A 的输出' } });
    assert.equal(state.transcript.length, 0, 'draft state must not absorb a background run');
    assert.equal(sessionOwnsTranscript('task-a'), false);
  } finally {
    reset();
  }
});

test('a background decision is retained for its session without entering the active transcript', () => {
  reset();
  try {
    state.activeSessionId = 'task-b';
    state.transcriptSessionId = 'task-b';
    applyReverseRequest('decision-a', 'session/request_permission', {
      sessionId: 'task-a',
      toolCall: { toolCallId: 'tool-a', title: 'write', kind: 'edit' },
      options: [{ optionId: 'allow', name: 'Allow', kind: 'allow_once' }],
    });
    assert.equal(sessionRunStatus('task-a'), 'pending');
    assert.equal(state.transcript.length, 0, 'background request must not bleed into B');
    assert.equal(state.pendingDecisions['task-a']?.['decision-a']?.kind, 'permission');

    clearTranscript('task-a');
    assert.equal(state.transcript.filter((entry) => entry.kind === 'permission').length, 1, 'opening A materializes its retained request once');
    applyReverseRequest('decision-a', 'session/request_permission', {
      sessionId: 'task-a',
      toolCall: { toolCallId: 'tool-a', title: 'write', kind: 'edit' },
      options: [{ optionId: 'allow', name: 'Allow', kind: 'allow_once' }],
    });
    assert.equal(state.transcript.filter((entry) => entry.kind === 'permission').length, 1, 'ACP replay must dedupe by request ID');
    applySessionEvent({ sessionId: 'task-a', event: 'terminal', status: 'cancelled' });
    assert.equal(state.pendingDecisions['task-a'], undefined, 'terminal state clears transient decision projection');
  } finally {
    reset();
  }
});

test('background available command updates cannot replace active session commands', () => {
  reset();
  const previousCommands = state.availableCommands;
  try {
    state.activeSessionId = 'task-b';
    state.transcriptSessionId = 'task-b';
    state.availableCommands = [{ name: 'b-command' }];
    applySessionUpdate('task-a', { sessionUpdate: 'available_commands_update', availableCommands: [{ name: 'a-command' }] });
    assert.deepEqual(state.availableCommands, [{ name: 'b-command' }]);
  } finally {
    state.availableCommands = previousCommands;
    reset();
  }
});

test('a terminal event only finishes its own session run', () => {
  reset();
  try {
    state.activeSessionId = 'task-b';
    state.transcriptSessionId = 'task-b';
    setSessionRunStatus('task-a', 'working');
    setSessionRunStatus('task-b', 'working');

    applySessionEvent({ sessionId: 'task-a', event: 'terminal', status: 'completed' });

    assert.equal(sessionRunStatus('task-a'), 'completed');
    assert.equal(sessionRunStatus('task-b'), 'working', 'the active task must keep running');
    assert.equal(state.transcript.length, 0, 'another session terminal must not add transcript items');
  } finally {
    reset();
  }
});

test('the active session terminal failure is always visible in its transcript', () => {
  reset();
  try {
    state.activeSessionId = 'task-b';
    state.transcriptSessionId = 'task-b';
    applySessionEvent({ sessionId: 'task-b', event: 'terminal', status: 'failed', error: 'provider rejected the request' });
    const errors = state.transcript.filter((entry) => entry.kind === 'error');
    assert.equal(errors.length, 1, 'a failed run must explain itself instead of only moving the status dot');
    assert.equal(errors[0].kind === 'error' ? errors[0].message : '', 'provider rejected the request');
    assert.equal(sessionRunStatus('task-b'), 'failed');
  } finally {
    reset();
  }
});

test('run_status projections stay per session', () => {
  reset();
  try {
    state.activeSessionId = 'task-b';
    state.transcriptSessionId = 'task-b';
    applySessionEvent({ sessionId: 'task-a', event: 'run_status', status: 'running' });
    assert.equal(isSessionRunning('task-a'), true);
    assert.equal(sessionRunStatus('task-b'), 'idle');
    applySessionEvent({ sessionId: 'task-b', event: 'run_status', status: 'running' });
    assert.equal(isSessionRunning('task-b'), true);
  } finally {
    reset();
  }
});
