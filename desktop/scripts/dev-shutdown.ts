import { execFileSync } from 'node:child_process';

/**
 * Bounded shutdown for the Desktop development runner.
 *
 * The dev runner owns two long-lived resources: the Vite renderer watcher (a
 * Node process that stays alive as long as its file watchers are open) and the
 * Electron process tree (main process plus renderer/GPU/utility children). Both
 * have to be torn down explicitly, because a terminal interrupt is delivered to
 * the foreground process group and nothing else. Leaving either one alive keeps
 * the dev runner hanging after Ctrl+C, and leaving the Electron children behind
 * keeps a headless Desktop running with no window.
 *
 * The helpers here are pure and dependency-injected so the ordering guarantees
 * can be tested without spawning Electron.
 */

/** Grace period between the terminate signal and the forced kill. */
export const DEFAULT_SHUTDOWN_GRACE_MS = 4_000;

/** Interrupt signals the dev runner reacts to. SIGHUP covers a closed terminal. */
export const DEV_SHUTDOWN_SIGNALS = ['SIGINT', 'SIGTERM', 'SIGHUP'] as const;
export type DevShutdownSignal = (typeof DEV_SHUTDOWN_SIGNALS)[number];

/** Conventional 128 + signal exit codes so `make` sees the interrupt. */
export function exitCodeForSignal(signal: DevShutdownSignal): number {
  switch (signal) {
    case 'SIGINT':
      return 130;
    case 'SIGTERM':
      return 143;
    case 'SIGHUP':
      return 129;
    default:
      return 1;
  }
}

export type ShutdownState = 'running' | 'stopping' | 'stopped';
export type ShutdownEscalation = 'terminate' | 'kill';

export interface ShutdownControllerOptions {
  /** Closes the Vite renderer watcher. Must be idempotent from the caller's side. */
  closeWatcher: () => Promise<void>;
  /** Sends a termination escalation to the Electron process tree. */
  sendSignal: (escalation: ShutdownEscalation) => void;
  /** Terminates the dev runner process itself. */
  exit: (code: number) => void;
  graceMs?: number;
  setTimer?: (fn: () => void, ms: number) => unknown;
  clearTimer?: (handle: unknown) => void;
  log?: (message: string) => void;
}

export interface ShutdownController {
  readonly state: ShutdownState;
  /** Handles an interrupt; a second one escalates immediately. */
  request: (signal: DevShutdownSignal) => void;
  /** Reports that the Electron main process is gone. */
  childExited: (code: number | null) => void;
  /** Reports that Electron could not be spawned at all. */
  childFailed: (error: unknown) => void;
}

/**
 * Creates the shutdown state machine used by scripts/dev.ts.
 *
 * Guarantees, in order:
 *  1. The watcher is closed on every exit path, including the interrupt path
 *     (the previous implementation only closed it when Electron exited first,
 *     which is why the runner survived Ctrl+C).
 *  2. The watcher close is started at most once and awaited before exiting, so
 *     `process.exit` cannot truncate a pending close.
 *  3. Electron is asked to terminate immediately, then killed after the grace
 *     period if it has not exited, so a stuck process cannot hang the runner.
 *  4. A second interrupt skips the wait and exits at once.
 */
export function createShutdownController(options: ShutdownControllerOptions): ShutdownController {
  const graceMs = options.graceMs ?? DEFAULT_SHUTDOWN_GRACE_MS;
  const setTimer = options.setTimer ?? ((fn: () => void, ms: number) => setTimeout(fn, ms));
  const clearTimer = options.clearTimer ?? ((handle: unknown) => clearTimeout(handle as NodeJS.Timeout));
  const log = options.log ?? ((message: string) => console.log(`[desktop-dev] ${message}`));

  let state: ShutdownState = 'running';
  let timer: unknown = null;
  let closing: Promise<void> | null = null;
  let interrupt: DevShutdownSignal | null = null;

  function closeWatcherOnce(): Promise<void> {
    if (!closing) {
      closing = options.closeWatcher().catch((error: unknown) => {
        log(`renderer watcher close failed: ${error instanceof Error ? error.message : String(error)}`);
      });
    }
    return closing;
  }

  function stopTimer(): void {
    if (timer === null) return;
    clearTimer(timer);
    timer = null;
  }

  function finish(code: number): void {
    stopTimer();
    state = 'stopped';
    options.exit(code);
  }

  function request(signal: DevShutdownSignal): void {
    if (state === 'stopped') return;
    if (state === 'stopping') {
      log(`${signal} received again, killing Electron and exiting now`);
      stopTimer();
      options.sendSignal('kill');
      finish(exitCodeForSignal(signal));
      return;
    }

    state = 'stopping';
    interrupt = signal;
    log(`${signal} received, closing renderer watcher and stopping Electron`);
    void closeWatcherOnce();
    options.sendSignal('terminate');

    const handle = setTimer(() => {
      timer = null;
      log(`Electron did not exit within ${graceMs}ms, killing it`);
      options.sendSignal('kill');
      finish(exitCodeForSignal(signal));
    }, graceMs);
    timer = handle;
    // The grace timer must never be the reason the runner stays alive.
    (handle as { unref?: () => void } | null)?.unref?.();
  }

  return {
    get state(): ShutdownState {
      return state;
    },
    request,
    childExited(code: number | null): void {
      if (state === 'stopped') return;
      const interrupted = state === 'stopping';
      state = 'stopped';
      stopTimer();
      const exitCode = interrupted && interrupt ? exitCodeForSignal(interrupt) : (code ?? 0);
      log(`Electron exited with code ${code ?? 0}`);
      void closeWatcherOnce().then(() => finish(exitCode));
    },
    childFailed(error: unknown): void {
      if (state === 'stopped') return;
      const interrupted = state === 'stopping';
      state = 'stopped';
      stopTimer();
      log(`Electron failed to start: ${error instanceof Error ? error.message : String(error)}`);
      const exitCode = interrupted && interrupt ? exitCodeForSignal(interrupt) : 1;
      void closeWatcherOnce().then(() => finish(exitCode));
    },
  };
}

export type KillFn = (target: number, signal: NodeJS.Signals) => void;
export type RunCommand = (command: string, args: string[]) => void;

export interface KillProcessTreeOptions {
  pid: number;
  platform: NodeJS.Platform;
  escalation?: ShutdownEscalation;
  kill?: KillFn;
  run?: RunCommand;
  log?: (message: string) => void;
}

/**
 * Terminates the whole Electron process tree, not just the main process.
 *
 * The dev runner spawns Electron detached so it leads its own process group;
 * that makes the group the unit of termination on POSIX (renderer, GPU and
 * utility processes included) and `taskkill /T` the unit on Windows, where the
 * spawn goes through a shell wrapper.
 */
export function killProcessTree(options: KillProcessTreeOptions): void {
  const escalation = options.escalation ?? 'terminate';
  const log = options.log ?? ((message: string) => console.log(`[desktop-dev] ${message}`));

  if (options.pid <= 0) return;

  if (options.platform === 'win32') {
    const run = options.run ?? ((command: string, args: string[]) => void execFileSync(command, args, { stdio: 'ignore' }));
    const args = ['/pid', String(options.pid), '/T'];
    if (escalation === 'kill') args.push('/F');
    try {
      run('taskkill', args);
    } catch (error: unknown) {
      log(`taskkill failed: ${error instanceof Error ? error.message : String(error)}`);
    }
    return;
  }

  const kill = options.kill ?? ((target: number, signal: NodeJS.Signals) => void process.kill(target, signal));
  const signal: NodeJS.Signals = escalation === 'kill' ? 'SIGKILL' : 'SIGTERM';
  try {
    kill(-options.pid, signal);
  } catch (error: unknown) {
    const code = (error as { code?: string } | null)?.code;
    if (code !== 'ESRCH') {
      log(`process group kill failed: ${error instanceof Error ? error.message : String(error)}`);
    }
    // The group is gone (or was never created because a non-detached spawn was
    // used); fall back to the main process itself.
    try {
      kill(options.pid, signal);
    } catch (fallbackError: unknown) {
      const fallbackCode = (fallbackError as { code?: string } | null)?.code;
      if (fallbackCode !== 'ESRCH') {
        log(`kill failed: ${fallbackError instanceof Error ? fallbackError.message : String(fallbackError)}`);
      }
    }
  }
}
