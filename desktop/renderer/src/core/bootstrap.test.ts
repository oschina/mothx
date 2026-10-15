import assert from 'node:assert/strict';
import test from 'node:test';

import { handleEvent } from './bootstrap.ts';
import { state } from './state.ts';

const previousWindow = (globalThis as { window?: unknown }).window;
let resolveDirectory: ((directory: string) => void) | undefined;

(globalThis as { window?: unknown }).window = {
  mothx: {
    isDesktop: true,
    platform: 'linux',
    acp: {
      invoke: async () => ({ ok: true, result: {} }),
      notify: () => undefined,
      respond: () => undefined,
      cancelReverse: () => undefined,
      getState: async () => ({ state: 'idle', workspace: '' }),
      restart: async () => ({ ok: true, result: {} }),
      onEvent: () => () => undefined,
    },
    desktop: {
      defaultNewSessionDirectory: () => new Promise<string>((resolve) => { resolveDirectory = resolve; }),
      logDiagnostic: () => undefined,
      storeGet: async () => state.store,
      storeSet: async () => undefined,
    },
  },
};

function resetDraft(): void {
  state.activeSessionId = null;
  state.newSessionCwd = '';
  state.draftWorkspaceSource = 'quick-start';
  state.draftGeneration = 0;
  state.connection = { state: 'idle', workspace: '' };
}

test.after(() => {
  (globalThis as { window?: unknown }).window = previousWindow;
});

test('stale quick-start completion cannot repopulate a New Task draft', async () => {
  resetDraft();
  handleEvent({ type: 'state', snapshot: { state: 'ready', workspace: '' } });
  assert.ok(resolveDirectory, 'ready should request the scratch directory');

  state.draftGeneration += 1;
  state.draftWorkspaceSource = 'new-task';
  resolveDirectory?.('/scratch');
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(state.draftWorkspaceSource, 'new-task');
  assert.equal(state.newSessionCwd, '');
});

test('unchanged quick-start draft accepts its scratch directory', async () => {
  resetDraft();
  handleEvent({ type: 'state', snapshot: { state: 'ready', workspace: '' } });
  assert.ok(resolveDirectory, 'ready should request the scratch directory');
  resolveDirectory?.('/scratch');
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(state.draftWorkspaceSource, 'quick-start');
  assert.equal(state.newSessionCwd, '/scratch');
});
