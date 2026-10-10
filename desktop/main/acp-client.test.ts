import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { PassThrough } from 'node:stream';
import test from 'node:test';

import { AcpClient, classifyMessage, desktopInitializeParams, parseStartupErrorLine, shouldRetryStartupError } from './acp-client.ts';
import { DesktopStore } from './store.ts';

test('classifyMessage separates responses, notifications, and reverse requests', () => {
  assert.equal(classifyMessage({ jsonrpc: '2.0', id: 1, result: {} }), 'response');
  assert.equal(classifyMessage({ jsonrpc: '2.0', id: 2, error: { code: -1, message: 'x' } }), 'response');
  assert.equal(classifyMessage({ jsonrpc: '2.0', method: 'session/update', params: {} }), 'notification');
  assert.equal(classifyMessage({ jsonrpc: '2.0', id: 3, method: 'session/request_permission', params: {} }), 'reverse-request');
  assert.equal(classifyMessage({ jsonrpc: '2.0' }), 'invalid');
  assert.equal(classifyMessage({ jsonrpc: '1.0', id: 4, result: {} } as never), 'invalid');
});

test('parseStartupErrorLine extracts the MOTHX_ACP_ERROR payload', () => {
  const parsed = parseStartupErrorLine('MOTHX_ACP_ERROR {"code":"provider_unusable","message":"default provider x has no API key","fix":"configure a key"}');
  assert.deepEqual(parsed, { code: 'provider_unusable', message: 'default provider x has no API key', fix: 'configure a key' });
  assert.equal(parseStartupErrorLine('plain stderr line'), null);
  assert.equal(parseStartupErrorLine('MOTHX_ACP_ERROR not-json'), null);
});

test('deterministic configuration startup errors do not enter the restart loop', () => {
  assert.equal(shouldRetryStartupError({ code: 'config_invalid', message: 'bad settings' }), false);
  assert.equal(shouldRetryStartupError({ code: 'provider_unusable', message: 'missing key' }), false);
  assert.equal(shouldRetryStartupError({ code: 'exited', message: 'process crashed' }), true);
  assert.equal(shouldRetryStartupError(null), true);
});

test('desktop ACP initialization leaves session work directories unrestricted by default', () => {
  const params = desktopInitializeParams({ clientInfo: { name: 'mothx-desktop', version: 'test' } });
  assert.deepEqual(params._meta, { mothx: { surface: 'desktop' } });
  assert.equal('workspace' in (params._meta?.mothx || {}), false);
});

test('desktop store persists UI state and clamps workspace history', () => {
  const dir = mkdtempSync(join(tmpdir(), 'mothx-desktop-store-'));
  try {
    const store = new DesktopStore(dir);
    assert.equal(store.get().theme, 'light');
    store.set({ theme: 'dark', locale: 'en', homeBackgroundImage: '/tmp/wallpaper.webp', homeBackgroundOpacity: 68, homeBackgroundBlur: 7, homeBackgroundScope: 'home', homeBackgroundFit: 'tile', homeBackgroundPosition: 'right', homeLogoVisible: false, homeLogoImage: '/tmp/logo.png', lastWorkspace: '/tmp/project-a' });
    store.set({ lastWorkspace: '/tmp/project-b' });
    store.set({ sessionStatus: { s1: 'completed' } });
    const reloaded = new DesktopStore(dir);
    const data = reloaded.get();
    assert.equal(data.theme, 'dark');
    assert.equal(data.locale, 'en');
    assert.equal(data.homeBackgroundImage, '/tmp/wallpaper.webp');
    assert.equal(data.homeBackgroundOpacity, 68);
    assert.equal(data.homeBackgroundBlur, 7);
    assert.equal(data.homeBackgroundScope, 'home');
    assert.equal(data.homeBackgroundFit, 'tile');
    assert.equal(data.homeBackgroundPosition, 'right');
    assert.equal(data.homeLogoVisible, false);
    assert.equal(data.homeLogoImage, '/tmp/logo.png');
    assert.equal(data.lastWorkspace, '/tmp/project-b');
    assert.deepEqual(data.recentWorkspaces, ['/tmp/project-b', '/tmp/project-a']);
    assert.deepEqual(data.sessionStatus, { s1: 'completed' });
    store.set({ homeBackgroundOpacity: 999, homeBackgroundBlur: -5, homeBackgroundScope: 'invalid' as never, homeBackgroundFit: 'invalid' as never, homeBackgroundPosition: 'invalid' as never });
    assert.equal(store.get().homeBackgroundOpacity, 100);
    assert.equal(store.get().homeBackgroundBlur, 0);
    assert.equal(store.get().homeBackgroundScope, 'app');
    assert.equal(store.get().homeBackgroundFit, 'cover');
    assert.equal(store.get().homeBackgroundPosition, 'center');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('desktop store ignores malformed persisted files', () => {
  const dir = mkdtempSync(join(tmpdir(), 'mothx-desktop-store-'));
  try {
    const store = new DesktopStore(dir);
    store.set({ theme: 'dark' });
    const file = join(dir, 'desktop-store.json');
    writeFileSync(file, '{ broken json', 'utf8');
    const reloaded = new DesktopStore(dir);
    assert.equal(reloaded.get().theme, 'light');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

interface MockChild extends EventEmitter {
  pid: number;
  exitCode: number | null;
  signalCode: NodeJS.Signals | null;
  stdin: PassThrough;
  stdout: PassThrough;
  stderr: PassThrough;
  kill: (signal?: NodeJS.Signals | number) => boolean;
}

interface MockChildBehavior {
  initializeResult?: object;
  failAfterSpawn?: boolean;
}

function createMockChild(pid: number, behavior: MockChildBehavior = {}): MockChild {
  const child = new EventEmitter() as MockChild;
  child.pid = pid;
  child.exitCode = null;
  child.signalCode = null;
  child.stdin = new PassThrough();
  child.stdout = new PassThrough();
  child.stderr = new PassThrough();
  child.kill = () => true;

  if (behavior.failAfterSpawn) {
    process.nextTick(() => child.emit('error', new Error('spawn failed')));
  }

  if (behavior.initializeResult) {
    child.stdin.on('data', (data: Buffer) => {
      const lines = data.toString('utf8').split('\n').filter((line) => line.trim() !== '');
      for (const line of lines) {
        const msg = JSON.parse(line) as { id?: number | string; method?: string };
        if (msg.method === 'initialize') {
          process.nextTick(() => {
            child.stdout.push(`${JSON.stringify({ jsonrpc: '2.0', id: msg.id, result: behavior.initializeResult })}\n`);
          });
        }
      }
    });
  }
  return child;
}

class TestableAcpClient extends AcpClient {
  private mockChild?: MockChild;

  useMock(child: MockChild): void {
    this.mockChild = child;
  }

  protected override spawnChild(): ReturnType<typeof import('node:child_process').spawn> {
    if (!this.mockChild) throw new Error('no mock child configured');
    return this.mockChild as unknown as ReturnType<typeof import('node:child_process').spawn>;
  }

  pendingRestart(): boolean {
    return this.getRestartTimer() !== undefined;
  }
}

const baseStartOptions = {
  binary: '/fake/mothx',
  args: ['acp'],
  cwd: '/tmp',
  clientInfo: { name: 'mothx-desktop', version: 'test' },
};

test('stale error/exit callbacks from a replaced child do not affect the current child', async () => {
  const client = new TestableAcpClient({});
  const first = createMockChild(1000, {
    initializeResult: { protocolVersion: 1, agentInfo: { name: 'test' } },
  });
  client.useMock(first);

  await client.start({ ...baseStartOptions });
  assert.equal(client.getState().state, 'ready');
  assert.equal(client.getState().pid, 1000);

  const second = createMockChild(2000, {
    initializeResult: { protocolVersion: 1, agentInfo: { name: 'test' } },
  });
  client.useMock(second);
  const restartPromise = client.restart('/tmp/other');
  // While the explicit restart is in progress, the old child may still fire
  // its exit/error handlers. Those callbacks must not reject the new child
  // initialization or schedule a spurious restart.
  first.emit('error', new Error('stale error'));
  first.emit('exit', 1, null);
  await restartPromise;

  assert.equal(client.getState().state, 'ready');
  assert.equal(client.getState().pid, 2000);
  assert.equal(client.getState().error, undefined);
  assert.equal(client.pendingRestart(), false);
});

test('explicit start clears a pending automatic restart timer', async () => {
  const client = new TestableAcpClient({});
  const failing = createMockChild(1000, { failAfterSpawn: true });
  client.useMock(failing);

  try {
    await client.start({ ...baseStartOptions });
    assert.fail('expected start to reject for a failing child');
  } catch {
    // Expected.
  }
  // scheduleRestart transitions the state to 'restarting' while it waits.
  assert.equal(client.getState().state, 'restarting');
  // The failing child should have scheduled an automatic restart.
  assert.equal(client.pendingRestart(), true);

  const succeeding = createMockChild(2000, {
    initializeResult: { protocolVersion: 1, agentInfo: { name: 'test' } },
  });
  client.useMock(succeeding);
  await client.start({ ...baseStartOptions });
  assert.equal(client.getState().state, 'ready');
  assert.equal(client.getState().pid, 2000);
  // The manual recovery must have cancelled the automatic timer.
  assert.equal(client.pendingRestart(), false);
});
