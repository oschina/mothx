import assert from 'node:assert/strict';
import test from 'node:test';

import {
  createShutdownController,
  DEFAULT_SHUTDOWN_GRACE_MS,
  DEV_SHUTDOWN_SIGNALS,
  exitCodeForSignal,
  killProcessTree,
  type ShutdownController,
} from './dev-shutdown.ts';

interface Harness {
  controller: ShutdownController;
  events: string[];
  exits: number[];
  runTimer: () => void;
  readonly pendingTimers: number;
  readonly watcherClosed: number;
}

function harness(options: { graceMs?: number } = {}): Harness {
  const events: string[] = [];
  const exits: number[] = [];
  const timers = new Map<number, () => void>();
  let nextTimerId = 1;
  let watcherClosed = 0;

  const controller = createShutdownController({
    closeWatcher: async () => {
      watcherClosed += 1;
      events.push('close-watcher');
      await new Promise((resolve) => setImmediate(resolve));
      events.push('close-watcher-done');
    },
    sendSignal: (escalation) => events.push(`signal:${escalation}`),
    exit: (code) => {
      exits.push(code);
      events.push(`exit:${code}`);
    },
    graceMs: options.graceMs ?? DEFAULT_SHUTDOWN_GRACE_MS,
    setTimer: (fn) => {
      const id = nextTimerId++;
      timers.set(id, fn);
      return id;
    },
    clearTimer: (handle) => {
      timers.delete(handle as number);
    },
    log: () => {},
  });

  return {
    controller,
    events,
    exits,
    get pendingTimers() {
      return timers.size;
    },
    get watcherClosed() {
      return watcherClosed;
    },
    runTimer: () => {
      const pending = [...timers.values()];
      timers.clear();
      for (const fn of pending) fn();
    },
  };
}

test('an interrupt closes the renderer watcher and terminates Electron', async () => {
  const h = harness();

  h.controller.request('SIGINT');
  assert.equal(h.controller.state, 'stopping');
  assert.deepEqual(h.events, ['close-watcher', 'signal:terminate']);

  // The runner must not exit before the watcher close settles, otherwise
  // process.exit truncates the close and the watcher keeps the process alive.
  assert.deepEqual(h.exits, []);
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(h.events, ['close-watcher', 'signal:terminate', 'close-watcher-done']);

  // The interrupt alone cannot end the run: a bounded escalation is armed so a
  // process that ignores SIGTERM still cannot hang the runner.
  assert.equal(h.pendingTimers, 1);
  assert.equal(h.watcherClosed, 1);
});

test('an interrupt alone does not leave the process running when Electron exits', async () => {
  const h = harness();

  h.controller.request('SIGINT');
  h.controller.childExited(0);

  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));
  // The interrupt wins the exit code so `make` reports 130 rather than 0.
  assert.deepEqual(h.exits, [130]);
  // The grace timer is cancelled because Electron already exited.
  assert.equal(h.pendingTimers, 0);
});

test('a stuck Electron process is killed after the grace period', async () => {
  const h = harness({ graceMs: 10 });

  h.controller.request('SIGINT');
  h.runTimer();
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));

  assert.deepEqual(h.events, [
    'close-watcher',
    'signal:terminate',
    'signal:kill',
    'exit:130',
    'close-watcher-done',
  ]);
  assert.equal(h.controller.state, 'stopped');
});

test('a second interrupt escalates immediately without waiting for the grace period', async () => {
  const h = harness();

  h.controller.request('SIGINT');
  h.controller.request('SIGINT');

  assert.deepEqual(h.events.slice(0, 3), ['close-watcher', 'signal:terminate', 'signal:kill']);
  assert.deepEqual(h.exits, [130]);
  assert.equal(h.pendingTimers, 0);
});

test('signals after shutdown are ignored', () => {
  const h = harness();

  h.controller.request('SIGINT');
  h.controller.request('SIGINT');
  h.controller.request('SIGHUP');
  h.controller.childExited(0);
  h.controller.childFailed(new Error('late failure'));

  assert.equal(h.events.filter((event) => event.startsWith('signal:kill')).length, 1);
  assert.equal(h.events.filter((event) => event.startsWith('exit:')).length, 1);
  assert.equal(h.watcherClosed, 1);
});

test('Electron exiting on its own closes the watcher and reports its own code', async () => {
  const h = harness();

  h.controller.childExited(7);
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));

  assert.deepEqual(h.events, ['close-watcher', 'close-watcher-done', 'exit:7']);
  assert.equal(h.pendingTimers, 0);
});

test('a spawn failure is reported and does not hang the runner', async () => {
  const h = harness();

  h.controller.childFailed(new Error('ENOENT'));
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));

  assert.deepEqual(h.exits, [1]);
  assert.equal(h.watcherClosed, 1);
});

test('a watcher that fails to close still terminates Electron', async () => {
  const events: string[] = [];
  const exits: number[] = [];
  const controller = createShutdownController({
    closeWatcher: async () => {
      throw new Error('watcher is already closed');
    },
    sendSignal: (escalation) => events.push(`signal:${escalation}`),
    exit: (code) => exits.push(code),
    setTimer: () => null,
    clearTimer: () => {},
    log: () => {},
  });

  controller.request('SIGINT');
  controller.childExited(0);
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));

  assert.deepEqual(events, ['signal:terminate']);
  assert.deepEqual(exits, [130]);
});

test('exit codes follow the 128 + signal convention', () => {
  assert.deepEqual(DEV_SHUTDOWN_SIGNALS.map(exitCodeForSignal), [130, 143, 129]);
});

test('POSIX shutdown targets the whole process group', () => {
  const killed: Array<[number, string]> = [];
  killProcessTree({
    pid: 4242,
    platform: 'linux',
    escalation: 'terminate',
    kill: (target, signal) => killed.push([target, signal]),
    log: () => {},
  });

  assert.deepEqual(killed, [[-4242, 'SIGTERM']]);
});

test('POSIX forced shutdown uses SIGKILL and falls back to the process itself', () => {
  const killed: Array<[number, string]> = [];
  const esrch = Object.assign(new Error('no such process'), { code: 'ESRCH' });
  killProcessTree({
    pid: 7,
    platform: 'darwin',
    escalation: 'kill',
    kill: (target, signal) => {
      killed.push([target, signal]);
      if (target < 0) throw esrch;
    },
    log: () => {},
  });

  assert.deepEqual(killed, [
    [-7, 'SIGKILL'],
    [7, 'SIGKILL'],
  ]);
});

test('a missing process group is not reported as a failure', () => {
  const logged: string[] = [];
  const esrch = Object.assign(new Error('no such process'), { code: 'ESRCH' });
  killProcessTree({
    pid: 9,
    platform: 'linux',
    kill: () => {
      throw esrch;
    },
    log: (message) => logged.push(message),
  });

  assert.deepEqual(logged, []);
});

test('Windows shutdown uses taskkill for the whole tree and forces on escalation', () => {
  const commands: Array<[string, string[]]> = [];
  const run = (command: string, args: string[]) => {
    commands.push([command, args]);
  };

  killProcessTree({ pid: 100, platform: 'win32', escalation: 'terminate', run });
  killProcessTree({ pid: 100, platform: 'win32', escalation: 'kill', run });

  assert.deepEqual(commands, [
    ['taskkill', ['/pid', '100', '/T']],
    ['taskkill', ['/pid', '100', '/T', '/F']],
  ]);
});

test('a failed taskkill is reported instead of thrown', () => {
  const logged: string[] = [];
  killProcessTree({
    pid: 5,
    platform: 'win32',
    run: () => {
      throw new Error('taskkill is unavailable');
    },
    log: (message) => logged.push(message),
  });

  assert.equal(logged.length, 1);
  assert.match(logged[0], /taskkill failed/);
});

test('an unknown process id is a no-op', () => {
  const commands: string[] = [];
  killProcessTree({
    pid: 0,
    platform: 'win32',
    run: (command) => commands.push(command),
  });
  killProcessTree({
    pid: -1,
    platform: 'linux',
    kill: () => commands.push('kill'),
  });

  assert.deepEqual(commands, []);
});
