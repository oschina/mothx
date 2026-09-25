import { ChildProcess, spawn } from 'node:child_process';
import { createInterface } from 'node:readline';

import type {
  AcpStartupError,
  InitializeParams,
  InitializeResult,
  RpcError,
  RpcMessage,
  SessionEvent,
  SessionUpdateNotification,
  WorktreeStatusNotification,
} from './acp-types';

export const ACP_PROTOCOL_VERSION = 1;

export type AcpConnectionState = 'idle' | 'starting' | 'ready' | 'restarting' | 'stopped' | 'error';

export interface AcpClientSnapshot {
  state: AcpConnectionState;
  workspace: string;
  agentInfo?: { name?: string; title?: string; version?: string };
  agentCapabilities?: InitializeResult['agentCapabilities'];
  error?: AcpStartupError | { code: string; message: string };
  pid?: number;
}

export interface ReverseRequest {
  id: number | string;
  method: string;
  params: unknown;
}

export interface AcpClientHandlers {
  onState?: (snapshot: AcpClientSnapshot) => void;
  onSessionUpdate?: (notification: SessionUpdateNotification) => void;
  onSessionEvent?: (event: SessionEvent) => void;
  onWorktreeStatus?: (notification: WorktreeStatusNotification) => void;
  onReverseRequest?: (request: ReverseRequest) => void;
  onLog?: (line: string) => void;
}

export class AcpRequestError extends Error {
  code: number;
  data?: unknown;

  constructor(err: RpcError) {
    super(err.message || 'ACP request failed');
    this.name = 'AcpRequestError';
    this.code = err.code;
    this.data = err.data;
  }
}

// classifyMessage is pure so wire dispatch can be unit-tested without a child
// process. JSON-RPC: id+method is a reverse request, method-only is a
// notification, id+result/error is a response.
export function classifyMessage(msg: RpcMessage): 'reverse-request' | 'notification' | 'response' | 'invalid' {
  if (msg.jsonrpc !== '2.0') return 'invalid';
  const hasId = msg.id !== undefined && msg.id !== null;
  if (typeof msg.method === 'string' && msg.method !== '') {
    return hasId ? 'reverse-request' : 'notification';
  }
  if (hasId && (msg.result !== undefined || msg.error !== undefined)) return 'response';
  return 'invalid';
}

export function parseStartupErrorLine(line: string): AcpStartupError | null {
  const marker = 'MOTHX_ACP_ERROR ';
  const index = line.indexOf(marker);
  if (index < 0) return null;
  try {
    const parsed = JSON.parse(line.slice(index + marker.length).trim()) as AcpStartupError;
    if (parsed && typeof parsed.message === 'string') {
      return { code: String(parsed.code || 'acp_startup'), message: parsed.message, fix: parsed.fix };
    }
  } catch {
    // Fall through: malformed marker lines are surfaced as raw logs.
  }
  return null;
}

// These errors are deterministic configuration failures. Restarting the same
// binary with the same configuration cannot repair them and only creates a
// noisy crash loop. A person must correct the MothX configuration first, then
// explicitly retry the runtime from Desktop.
const NON_RETRYABLE_STARTUP_ERROR_CODES = new Set([
  'config_invalid',
  'provider_unusable',
]);

export function shouldRetryStartupError(error: AcpStartupError | null | undefined): boolean {
  return !error || !NON_RETRYABLE_STARTUP_ERROR_CODES.has(error.code);
}

interface PendingRequest {
  resolve: (value: unknown) => void;
  reject: (error: Error) => void;
  method: string;
}

interface StartOptions {
  binary: string;
  args: string[];
  cwd: string;
  env?: NodeJS.ProcessEnv;
  clientInfo: { name: string; title?: string; version?: string };
  permissionTimeout?: string;
  questionTimeout?: string;
}

// Desktop is a local, session-first client: it must not turn the ACP process
// start directory into a workspace allowlist. Every session sends its own cwd,
// and configured Runtime security policy remains the only directory boundary.
// Do not add `_meta.mothx.workspace` here: it creates an immutable,
// process-wide ACP workspace window.
export function desktopInitializeParams(options: Pick<StartOptions, 'clientInfo'>): InitializeParams {
  return {
    protocolVersion: ACP_PROTOCOL_VERSION,
    clientCapabilities: {
      fs: { readTextFile: false, writeTextFile: false },
      terminal: false,
      session: { configOptions: { boolean: {} } },
    },
    clientInfo: options.clientInfo,
    _meta: { mothx: { surface: 'desktop' } },
  };
}

// AcpClient owns the `mothx acp` child process and the JSON-RPC channel.
// It is the only ACP transport in the desktop app; renderer access goes
// through the IPC bridge, never through a second client.
export class AcpClient {
  private child?: ChildProcess;
  private pending = new Map<string, PendingRequest>();
  private nextID = 1;
  private handlers: AcpClientHandlers;
  private snapshot: AcpClientSnapshot = { state: 'idle', workspace: '' };
  private startOptions?: StartOptions;
  private stopping = false;
  private restartTimer?: NodeJS.Timeout;
  private restartAttempts = 0;
  private stderrTail: string[] = [];

  constructor(handlers: AcpClientHandlers = {}) {
    this.handlers = handlers;
  }

  getState(): AcpClientSnapshot {
    return { ...this.snapshot };
  }

  private setState(patch: Partial<AcpClientSnapshot>): void {
    this.snapshot = { ...this.snapshot, ...patch };
    this.handlers.onState?.(this.getState());
  }

  async start(options: StartOptions): Promise<InitializeResult> {
    this.stopChild('restart');
    this.stopping = false;
    this.startOptions = options;
    this.stderrTail = [];
    this.setState({ state: 'starting', workspace: options.cwd, error: undefined, pid: undefined });

    const env: NodeJS.ProcessEnv = { ...(options.env || process.env) };
    // Desktop approvals are human-paced; keep decisions alive far longer than
    // the ACP default (see desktop-acp-frontend-gap-proposal.md P0-3).
    if (options.permissionTimeout) env.MOTHX_ACP_PERMISSION_TIMEOUT = options.permissionTimeout;
    if (options.questionTimeout) env.MOTHX_ACP_QUESTION_TIMEOUT = options.questionTimeout;

    const child = spawn(options.binary, options.args, {
      cwd: options.cwd,
      env,
      stdio: ['pipe', 'pipe', 'pipe'],
      windowsHide: true,
    });
    this.child = child;
    this.setState({ pid: child.pid });

    child.on('error', (error) => {
      this.setState({
        state: 'error',
        error: { code: 'spawn_failed', message: `Unable to start the MothX ACP runtime: ${error.message}` },
      });
      this.failAllPending(error);
      this.scheduleRestart();
    });

    child.once('exit', (code, signal) => {
      const detail = `code=${code ?? 'none'} signal=${signal ?? 'none'}`;
      this.handlers.onLog?.(`acp process exited: ${detail}`);
      this.failAllPending(new Error(`ACP process exited (${detail})`));
      if (this.child === child) this.child = undefined;
      if (this.stopping) {
        this.setState({ state: 'stopped', pid: undefined });
        return;
      }
      const startupError = this.lastStartupError();
      this.setState({
        state: 'error',
        pid: undefined,
        error: startupError || { code: 'exited', message: `MothX ACP runtime stopped unexpectedly (${detail})` },
      });
      this.scheduleRestart(startupError);
    });

    if (child.stderr) {
      const stderrRl = createInterface({ input: child.stderr });
      stderrRl.on('line', (line) => {
        this.stderrTail.push(line);
        if (this.stderrTail.length > 40) this.stderrTail.shift();
        const startup = parseStartupErrorLine(line);
        if (startup) {
          this.setState({ state: 'error', error: startup });
        }
        this.handlers.onLog?.(`acp stderr: ${line}`);
      });
    }

    if (child.stdout) {
      const stdoutRl = createInterface({ input: child.stdout });
      stdoutRl.on('line', (line) => this.handleLine(line));
    }

    const init = (await this.request('initialize', desktopInitializeParams(options))) as InitializeResult;

    this.restartAttempts = 0;
    this.setState({
      state: 'ready',
      error: undefined,
      agentInfo: init.agentInfo,
      agentCapabilities: init.agentCapabilities,
    });
    return init;
  }

  // Restart only recreates the local ACP transport. Session work directories
  // stay per-session and are never negotiated as a process-wide workspace.
  async restart(cwd?: string): Promise<InitializeResult> {
    const options = this.startOptions;
    if (!options) throw new Error('ACP client has not been started');
    const nextCwd = cwd && cwd.trim() ? cwd : options.cwd;
    this.setState({ state: 'restarting' });
    return this.start({ ...options, cwd: nextCwd });
  }

  request(method: string, params?: unknown): Promise<unknown> {
    const child = this.child;
    if (!child || !child.stdin || child.exitCode !== null || this.snapshot.state === 'stopped') {
      return Promise.reject(new Error('ACP runtime is not connected'));
    }
    const id = this.nextID++;
    const message: RpcMessage = { jsonrpc: '2.0', id, method, params };
    return new Promise<unknown>((resolve, reject) => {
      this.pending.set(String(id), { resolve, reject, method });
      child.stdin!.write(`${JSON.stringify(message)}\n`, (error) => {
        if (error) {
          this.pending.delete(String(id));
          reject(error);
        }
      });
    });
  }

  notify(method: string, params?: unknown): void {
    const child = this.child;
    if (!child || !child.stdin || child.exitCode !== null) return;
    const message: RpcMessage = { jsonrpc: '2.0', method, params };
    child.stdin.write(`${JSON.stringify(message)}\n`);
  }

  // respondTo answers an agent->client reverse request (permission/question).
  respondTo(id: number | string, result: unknown): void {
    const child = this.child;
    if (!child || !child.stdin || child.exitCode !== null) return;
    const message: RpcMessage = { jsonrpc: '2.0', id, result };
    child.stdin.write(`${JSON.stringify(message)}\n`);
  }

  // cancelReverseRequest asks the agent to drop a pending reverse request.
  cancelReverseRequest(requestId: number | string): void {
    this.notify('$/cancel_request', { requestId });
  }

  async stop(): Promise<void> {
    this.stopping = true;
    if (this.restartTimer) {
      clearTimeout(this.restartTimer);
      this.restartTimer = undefined;
    }
    this.stopChild('stop');
    this.setState({ state: 'stopped', pid: undefined });
  }

  private stopChild(reason: string): void {
    const child = this.child;
    this.child = undefined;
    if (!child || child.exitCode !== null || child.signalCode !== null) return;
    this.handlers.onLog?.(`stopping acp process (${reason})`);
    try {
      child.stdin?.end();
      child.kill('SIGTERM');
    } catch {
      // Ignore: the exit handler reports the final state.
    }
    const killer = setTimeout(() => {
      try {
        if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL');
      } catch {
        // Ignore.
      }
    }, 3000);
    killer.unref?.();
  }

  private scheduleRestart(startupError?: AcpStartupError | null): void {
    if (this.stopping || this.restartTimer || !this.startOptions) return;
    if (!shouldRetryStartupError(startupError)) {
      this.handlers.onLog?.(`acp restart disabled for non-retryable startup error: ${startupError?.code}`);
      return;
    }
    this.restartAttempts += 1;
    if (this.restartAttempts > 5) {
      this.handlers.onLog?.('acp restart attempts exhausted');
      return;
    }
    const delay = Math.min(1000 * 2 ** (this.restartAttempts - 1), 15000);
    this.setState({ state: 'restarting' });
    this.restartTimer = setTimeout(() => {
      this.restartTimer = undefined;
      const options = this.startOptions;
      if (!options || this.stopping) return;
      this.start(options).catch((error: unknown) => {
        this.handlers.onLog?.(`acp restart failed: ${error instanceof Error ? error.message : String(error)}`);
      });
    }, delay);
    this.restartTimer.unref?.();
  }

  private lastStartupError(): AcpStartupError | null {
    for (let i = this.stderrTail.length - 1; i >= 0; i -= 1) {
      const parsed = parseStartupErrorLine(this.stderrTail[i]);
      if (parsed) return parsed;
    }
    return null;
  }

  private failAllPending(error: Error): void {
    for (const [, entry] of this.pending) entry.reject(error);
    this.pending.clear();
  }

  private handleLine(line: string): void {
    const trimmed = line.trim();
    if (!trimmed) return;
    let msg: RpcMessage;
    try {
      msg = JSON.parse(trimmed) as RpcMessage;
    } catch {
      this.handlers.onLog?.(`acp stdout (non-JSON): ${trimmed.slice(0, 400)}`);
      return;
    }
    switch (classifyMessage(msg)) {
      case 'response': {
        const key = String(msg.id);
        const entry = this.pending.get(key);
        if (!entry) return;
        this.pending.delete(key);
        if (msg.error) entry.reject(new AcpRequestError(msg.error));
        else entry.resolve(msg.result);
        return;
      }
      case 'reverse-request': {
        if (msg.id === undefined || msg.id === null) return;
        this.handlers.onReverseRequest?.({ id: msg.id, method: msg.method!, params: msg.params });
        return;
      }
      case 'notification': {
        this.dispatchNotification(msg.method!, msg.params);
        return;
      }
      default:
        this.handlers.onLog?.(`acp stdout (unclassified): ${trimmed.slice(0, 400)}`);
    }
  }

  private dispatchNotification(method: string, params: unknown): void {
    if (method === 'session/update') {
      const notification = params as SessionUpdateNotification;
      if (notification && typeof notification.sessionId === 'string' && notification.update) {
        this.handlers.onSessionUpdate?.(notification);
      }
      return;
    }
    if (method === '_mothx/session_event') {
      const event = params as SessionEvent;
      if (event && typeof event.sessionId === 'string') this.handlers.onSessionEvent?.(event);
      return;
    }
    if (method === 'mothx/worktree/status') {
      const notification = params as WorktreeStatusNotification;
      if (notification && notification.worktree && typeof notification.worktree.directory === 'string') {
        this.handlers.onWorktreeStatus?.(notification);
      }
      return;
    }
    this.handlers.onLog?.(`acp notification ignored: ${method}`);
  }
}
