import { contextBridge, ipcRenderer } from 'electron';

import type { ReadHomeImageResult } from '../main/home-image';

// The renderer only sees this bridge. All ACP traffic flows through the main
// process client; reverse requests (permission/question) arrive as events and
// are answered with acp.respond.

export interface DiagnosticLogEntry {
  id: number;
  timestamp: string;
  source: 'desktop' | 'acp' | 'renderer';
  message: string;
}

export interface MothxDesktopBridge {
  isDesktop: true;
  platform: NodeJS.Platform;
  acp: {
    invoke: (method: string, params?: unknown) => Promise<{ ok: true; result: unknown } | { ok: false; error: { message: string; code?: number; data?: unknown } }>;
    notify: (method: string, params?: unknown) => void;
    respond: (id: number | string, result: unknown) => void;
    cancelReverse: (requestId: number | string) => void;
    getState: () => Promise<unknown>;
    restart: (cwd?: string) => Promise<{ ok: true; result: unknown } | { ok: false; error: { message: string; code?: number; data?: unknown } }>;
    onEvent: (callback: (event: unknown) => void) => () => void;
  };
  desktop: {
    version: string;
    appInfo: () => Promise<{ version: string; platform: string; arch: string; runtimeBinary: string }>;
    chooseDirectory: (defaultPath?: string) => Promise<string | null>;
    defaultNewSessionDirectory: () => Promise<string>;
    chooseHomeBackground: (defaultPath?: string) => Promise<string | null>;
    homeBackgroundDataURL: (path: string) => Promise<ReadHomeImageResult>;
    chooseHomeLogo: (defaultPath?: string) => Promise<string | null>;
    homeLogoDataURL: (path: string) => Promise<ReadHomeImageResult>;
    chooseFiles: () => Promise<{ path: string; grant: string }[]>;
    readFileBase64: (grant: string) => Promise<{ ok: true; data: string; size: number } | { ok: false; error: string }>;
    storeGet: () => Promise<unknown>;
    storeSet: (patch: unknown) => Promise<unknown>;
    windowControl: (action: 'minimize' | 'maximize' | 'close') => void;
    logDiagnostic: (message: string) => void;
    getDiagnosticLogs: () => Promise<DiagnosticLogEntry[]>;
    onDiagnosticLog: (callback: (entry: DiagnosticLogEntry) => void) => () => void;
  };
}

const bridge: MothxDesktopBridge = {
  isDesktop: true,
  platform: process.platform,
  acp: {
    invoke: (method, params) => ipcRenderer.invoke('acp:invoke', method, params),
    notify: (method, params) => ipcRenderer.send('acp:notify', method, params),
    respond: (id, result) => ipcRenderer.send('acp:respond', id, result),
    cancelReverse: (requestId) => ipcRenderer.send('acp:cancel-reverse', requestId),
    getState: () => ipcRenderer.invoke('acp:state'),
    restart: (cwd) => ipcRenderer.invoke('acp:restart', cwd),
    onEvent: (callback) => {
      const listener = (_event: Electron.IpcRendererEvent, payload: unknown) => callback(payload);
      ipcRenderer.on('acp:event', listener);
      return () => ipcRenderer.removeListener('acp:event', listener);
    },
  },
  desktop: {
    version: process.env.npm_package_version || 'dev',
    appInfo: () => ipcRenderer.invoke('desktop:app-info'),
    chooseDirectory: (defaultPath = '') => ipcRenderer.invoke('desktop:choose-directory', defaultPath),
    defaultNewSessionDirectory: () => ipcRenderer.invoke('desktop:default-new-session-directory'),
    chooseHomeBackground: (defaultPath = '') => ipcRenderer.invoke('desktop:choose-home-background', defaultPath),
    homeBackgroundDataURL: (path) => ipcRenderer.invoke('desktop:home-background-data-url', path),
    chooseHomeLogo: (defaultPath = '') => ipcRenderer.invoke('desktop:choose-home-logo', defaultPath),
    homeLogoDataURL: (path) => ipcRenderer.invoke('desktop:home-logo-data-url', path),
    chooseFiles: () => ipcRenderer.invoke('desktop:choose-files'),
    readFileBase64: (grant) => ipcRenderer.invoke('desktop:read-file-base64', grant),
    storeGet: () => ipcRenderer.invoke('desktop:store-get'),
    storeSet: (patch) => ipcRenderer.invoke('desktop:store-set', patch),
    windowControl: (action) => ipcRenderer.send('desktop:window-control', action),
    logDiagnostic: (message) => ipcRenderer.send('desktop:log-diagnostic', String(message).slice(0, 2000)),
    getDiagnosticLogs: () => ipcRenderer.invoke('desktop:diagnostic-logs'),
    onDiagnosticLog: (callback) => {
      const listener = (_event: Electron.IpcRendererEvent, payload: DiagnosticLogEntry) => callback(payload);
      ipcRenderer.on('desktop:diagnostic-log', listener);
      return () => ipcRenderer.removeListener('desktop:diagnostic-log', listener);
    },
  },
};

contextBridge.exposeInMainWorld('mothx', bridge);

// Legacy probe kept for diagnostics pages that check for the desktop shell.
contextBridge.exposeInMainWorld('__MOTHX_DESKTOP__', {
  isDesktop: true,
  version: bridge.desktop.version,
  chooseDirectory: bridge.desktop.chooseDirectory,
  logDiagnostic: bridge.desktop.logDiagnostic,
});
