// window.mothx 桥接的类型化封装。renderer 只能通过这个模块访问主进程。

import type { ReadHomeImageResult } from '../../../main/home-image';

export interface AcpInvokeOk<T = unknown> {
  ok: true;
  result: T;
}
export interface AcpInvokeErr {
  ok: false;
  error: { message: string; code?: number; data?: unknown };
}
export type AcpInvokeResult<T = unknown> = AcpInvokeOk<T> | AcpInvokeErr;

export interface ConnectionState {
  state: 'idle' | 'starting' | 'ready' | 'restarting' | 'stopped' | 'error';
  workspace: string;
  agentInfo?: { name?: string; title?: string; version?: string };
  agentCapabilities?: Record<string, unknown>;
  error?: { code: string; message: string; fix?: string };
  pid?: number;
}

export type RendererEvent =
  | { type: 'state'; snapshot: ConnectionState }
  | { type: 'session-update'; sessionId: string; update: SessionUpdatePayload }
  | { type: 'session-event'; event: SessionEventPayload }
  | { type: 'reverse-request'; id: number | string; method: string; params: unknown };

export interface SessionUpdatePayload {
  sessionUpdate: string;
  [key: string]: unknown;
}

export interface SessionEventPayload {
  sessionId: string;
  event: string;
  [key: string]: unknown;
}

export interface StoreData {
  theme: 'light' | 'dark';
  locale: 'zh' | 'en';
  homeBackgroundImage: string;
  homeBackgroundOpacity: number;
  homeBackgroundBlur: number;
  homeBackgroundScope: 'app' | 'home';
  homeBackgroundFit: 'cover' | 'contain' | 'stretch' | 'tile';
  homeBackgroundPosition: 'center' | 'left' | 'right' | 'top' | 'bottom';
  homeLogoVisible: boolean;
  homeLogoImage: string;
  lastWorkspace: string;
  recentWorkspaces: string[];
  pinnedSessions: string[];
  sessionStatus: Record<string, string>;
}

export interface AppInfo {
  version: string;
  platform: string;
  arch: string;
  runtimeBinary: string;
}

export interface DiagnosticLogEntry {
  id: number;
  timestamp: string;
  source: 'desktop' | 'acp' | 'renderer';
  message: string;
}

interface MothxBridge {
  isDesktop: true;
  platform: string;
  acp: {
    invoke: (method: string, params?: unknown) => Promise<AcpInvokeResult>;
    notify: (method: string, params?: unknown) => void;
    respond: (id: number | string, result: unknown) => void;
    cancelReverse: (requestId: number | string) => void;
    getState: () => Promise<ConnectionState>;
    restart: (cwd?: string) => Promise<AcpInvokeResult>;
    onEvent: (callback: (event: RendererEvent) => void) => () => void;
  };
  desktop: {
    version: string;
    appInfo: () => Promise<AppInfo>;
    chooseDirectory: (defaultPath?: string) => Promise<string | null>;
    defaultNewSessionDirectory: () => Promise<string>;
    chooseHomeBackground: (defaultPath?: string) => Promise<string | null>;
    homeBackgroundDataURL: (path: string) => Promise<ReadHomeImageResult>;
    chooseHomeLogo: (defaultPath?: string) => Promise<string | null>;
    homeLogoDataURL: (path: string) => Promise<ReadHomeImageResult>;
    chooseFiles: () => Promise<{ path: string; grant: string }[]>;
    readFileBase64: (grant: string) => Promise<{ ok: true; data: string; size: number } | { ok: false; error: string }>;
    storeGet: () => Promise<StoreData>;
    storeSet: (patch: Partial<StoreData>) => Promise<StoreData>;
    windowControl: (action: 'minimize' | 'maximize' | 'close') => void;
    logDiagnostic: (message: string) => void;
    getDiagnosticLogs: () => Promise<DiagnosticLogEntry[]>;
    onDiagnosticLog: (callback: (entry: DiagnosticLogEntry) => void) => () => void;
  };
}

declare global {
  interface Window {
    mothx?: MothxBridge;
  }
}

export function bridge(): MothxBridge {
  if (!window.mothx) throw new Error('MothX desktop bridge is unavailable (open this UI inside MothX Desktop)');
  return window.mothx;
}

export async function invoke<T = unknown>(method: string, params?: unknown): Promise<T> {
  const outcome = await bridge().acp.invoke(method, params);
  if (!outcome.ok) {
    const error = new Error(outcome.error.message) as Error & { code?: number; data?: unknown };
    error.code = outcome.error.code;
    error.data = outcome.error.data;
    throw error;
  }
  return outcome.result as T;
}

export const acp = {
  notify: (method: string, params?: unknown) => bridge().acp.notify(method, params),
  respond: (id: number | string, result: unknown) => bridge().acp.respond(id, result),
  cancelReverse: (requestId: number | string) => bridge().acp.cancelReverse(requestId),
  getState: () => bridge().acp.getState(),
  restart: (cwd?: string) => bridge().acp.restart(cwd),
  onEvent: (callback: (event: RendererEvent) => void) => bridge().acp.onEvent(callback),
};

export const desktop = {
  platform: () => bridge().platform,
  appInfo: () => bridge().desktop.appInfo(),
  chooseDirectory: (defaultPath?: string) => bridge().desktop.chooseDirectory(defaultPath),
  defaultNewSessionDirectory: () => bridge().desktop.defaultNewSessionDirectory(),
  chooseHomeBackground: (defaultPath?: string) => bridge().desktop.chooseHomeBackground(defaultPath),
  homeBackgroundDataURL: (path: string) => bridge().desktop.homeBackgroundDataURL(path),
  chooseHomeLogo: (defaultPath?: string) => bridge().desktop.chooseHomeLogo(defaultPath),
  homeLogoDataURL: (path: string) => bridge().desktop.homeLogoDataURL(path),
  chooseFiles: () => bridge().desktop.chooseFiles(),
  readFileBase64: (grant: string) => bridge().desktop.readFileBase64(grant),
  storeGet: () => bridge().desktop.storeGet(),
  storeSet: (patch: Partial<StoreData>) => bridge().desktop.storeSet(patch),
  windowControl: (action: 'minimize' | 'maximize' | 'close') => bridge().desktop.windowControl(action),
  log: (message: string) => bridge().desktop.logDiagnostic(message),
  getDiagnosticLogs: () => bridge().desktop.getDiagnosticLogs(),
  onDiagnosticLog: (callback: (entry: DiagnosticLogEntry) => void) => bridge().desktop.onDiagnosticLog(callback),
};
