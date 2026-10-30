import test from 'node:test';
import assert from 'node:assert/strict';
import { get } from 'svelte/store';

// Fake browser globals before importing stores.js (module side effects read them).
class FakeWebSocket {
  static OPEN = 1;
  static CLOSED = 3;
  static instances = [];
  constructor(url) {
    this.url = url;
    this.readyState = FakeWebSocket.OPEN;
    this.sent = [];
    FakeWebSocket.instances.push(this);
  }
  send(data) {
    this.sent.push(JSON.parse(data));
  }
  close() {
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.();
  }
  subscribeMessages() {
    return this.sent.filter((msg) => msg?.type === 'subscribe');
  }
}

globalThis.window = {
  location: { protocol: 'http:', host: 'unit.test' },
  setTimeout: (fn) => {
    fn();
    return 0;
  },
  clearTimeout: () => {}
};
globalThis.WebSocket = FakeWebSocket;

const {
  connectRuns,
  runCursors,
  runEvents,
  sessions,
  syncRunSubscriptions,
  updateSessionPlan
} = await import('./stores.js');

function subscribedIDs(socket) {
  return socket.subscribeMessages().flatMap((msg) => (msg.subscriptions || []).map((sub) => sub.sessionId));
}

test('runs socket subscribes to sessions that load after the socket opens', () => {
  FakeWebSocket.instances = [];
  sessions.set([]);

  connectRuns();
  const socket = FakeWebSocket.instances[0];
  assert.ok(socket, 'socket should be created');
  socket.onopen();
  assert.deepEqual(subscribedIDs(socket), [], 'no sessions loaded yet, nothing to subscribe');

  // Sessions arriving after the socket opens must still be subscribed —
  // otherwise run events for existing sessions never reach the UI.
  sessions.set([{ id: 's1' }, { id: 's2' }]);
  assert.deepEqual(subscribedIDs(socket), ['s1', 's2']);
});

test('runs socket does not re-subscribe already subscribed sessions', () => {
  const socket = FakeWebSocket.instances[FakeWebSocket.instances.length - 1];
  socket.sent = socket.sent.filter((msg) => msg?.type !== 'subscribe');

  sessions.set([{ id: 's1' }, { id: 's2' }, { id: 's3' }]);
  assert.deepEqual(subscribedIDs(socket), ['s3'], 'only the new session should be subscribed');
});

test('runs socket re-subscribes all sessions after reconnect', () => {
  const first = FakeWebSocket.instances[FakeWebSocket.instances.length - 1];
  first.onclose();

  // window.setTimeout fires the reconnect immediately in this test harness.
  const second = FakeWebSocket.instances[FakeWebSocket.instances.length - 1];
  assert.notEqual(second, first, 'reconnect should create a new socket');
  second.onopen();
  assert.deepEqual(subscribedIDs(second).sort(), ['s1', 's2', 's3']);
});

test('runs socket advances only the persisted cursor for each event stream', () => {
  const socket = FakeWebSocket.instances[FakeWebSocket.instances.length - 1];
  runCursors.set({});
  runEvents.set([]);

  socket.onmessage({ data: JSON.stringify({
    type: 'session_event', sessionId: 's1', stream: 'transcript', seq: 900, data: { seq: 4 }
  }) });
  socket.onmessage({ data: JSON.stringify({
    type: 'session_event', sessionId: 's1', stream: 'capability', seq: 901, data: { seq: 2 }
  }) });
  socket.onmessage({ data: JSON.stringify({
    type: 'run_state', sessionId: 's1', stream: 'run', seq: 902, data: { seq: 7 }
  }) });
  socket.onmessage({ data: JSON.stringify({
    type: 'session_event', sessionId: 's1', stream: 'transcript', seq: 903, data: { seq: 3 }
  }) });
  socket.onmessage({ data: JSON.stringify({
    type: 'session_event', sessionId: 's2', stream: 'transcript', seq: 904, data: {}
  }) });

  assert.deepEqual(get(runCursors), {
    s1: { entrySeq: 4, runSeq: 7, capabilitySeq: 2 }
  });
  assert.equal(get(runEvents).length, 5, 'live broker events should still reach consumers');
  assert.deepEqual(get(runEvents).map((event) => event.wsSeq), [1, 2, 3, 4, 5]);
});

test('a rejected run subscription can be retried without reconnecting', () => {
  const socket = FakeWebSocket.instances[FakeWebSocket.instances.length - 1];
  socket.sent = [];

  socket.onmessage({ data: JSON.stringify({
    type: 'error', sessionId: 's2', data: { message: 'temporary rejection' }
  }) });
  syncRunSubscriptions();

  assert.deepEqual(subscribedIDs(socket), ['s2']);
});

function lastSocket() {
  if (FakeWebSocket.instances.length === 0) connectRuns();
  return FakeWebSocket.instances[FakeWebSocket.instances.length - 1];
}

test('runtime event updates a non-current historical session in the shared store', () => {
  sessions.set([
    { id: 'current', lastUsed: '2026-08-27T00:00:00.000Z', messageCount: 5 },
    { id: 'hist', lastUsed: '2026-08-26T00:00:00.000Z', messageCount: 3 }
  ]);
  runEvents.set([]);
  runCursors.set({});

  const socket = lastSocket();
  socket.onmessage({ data: JSON.stringify({
    type: 'session_event', sessionId: 'hist', stream: 'runtime', event: 'runtime_event',
    data: { execution: { running: true, busy: true, state: 'external' } }
  }) });

  const all = get(sessions);
  const hist = all.find((s) => s.id === 'hist');
  assert.ok(hist, 'historical session should remain in store');
  assert.equal(hist.execution?.running, true, 'historical session should now be running');
  assert.equal(hist.execution?.busy, true, 'historical session should be busy');
  assert.equal(hist.execution?.state, 'external', 'Runtime ownership state should be preserved');
  assert.equal(all.find((s) => s.id === 'current')?.execution, undefined, 'current session should not gain execution');
});

test('runtime event bursts update history without issuing HTTP refreshes', (t) => {
  sessions.set([{ id: 's1', lastUsed: '2026-08-27T00:00:00.000Z', messageCount: 1 }]);
  runEvents.set([]);
  runCursors.set({});

  const originalFetch = globalThis.fetch;
  t.after(() => { globalThis.fetch = originalFetch; });
  let fetchCalls = 0;
  globalThis.fetch = () => {
    fetchCalls += 1;
    throw new Error('runtime events must not trigger an HTTP refresh');
  };

  const socket = lastSocket();
  for (let i = 0; i < 5; i += 1) {
    socket.onmessage({ data: JSON.stringify({
      type: 'session_event', sessionId: 's1', stream: 'runtime', event: 'runtime_event',
      data: { execution: { running: i % 2 === 0, busy: true, state: i % 2 === 0 ? 'external' : 'reserved' } }
    }) });
  }

  const all = get(sessions);
  assert.equal(fetchCalls, 0, 'runtime snapshots must not cause HTTP refreshes');
  assert.equal(all.find((s) => s.id === 's1')?.execution?.state, 'external', 'the latest Runtime snapshot should win');
});

test('updateSessionPlan PUTs the trimmed canonical plan payload', async (t) => {
  const originalFetch = globalThis.fetch;
  t.after(() => { globalThis.fetch = originalFetch; });
  const calls = [];
  globalThis.fetch = async (path, options) => {
    calls.push({ path, options });
    return new Response(JSON.stringify({ plan: { title: 'Plan', steps: [{ title: 'One', status: 'done' }] } }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' }
    });
  };

  const result = await updateSessionPlan('s 1', {
    title: '  Plan  ',
    note: '  note  ',
    steps: [{ title: 'One', status: 'done' }]
  });
  assert.equal(calls.length, 1);
  assert.equal(calls[0].path, '/api/sessions/s%201/plan');
  assert.equal(calls[0].options.method, 'PUT');
  assert.deepEqual(JSON.parse(calls[0].options.body), {
    title: 'Plan',
    note: 'note',
    steps: [{ title: 'One', status: 'done' }]
  });
  assert.equal(result?.plan?.title, 'Plan');
});
