import { BrowserWindow, dialog, ipcMain } from 'electron';
import { realpathSync, readFileSync, statSync } from 'node:fs';

import type { AcpClient, AcpClientSnapshot } from './acp-client';
import { initialNewSessionDirectory } from './default-new-session-directory';
import type { DiagnosticLogBuffer, DiagnosticLogEntry } from './diagnostic-logs';
import { SelectedFileGrants } from './file-grants';
import { readHomeImageDataURL } from './home-image';
import type { ReadHomeImageResult } from './home-image';
import type { DesktopStore, DesktopStoreData } from './store';

export type RendererEvent =
  | { type: 'state'; snapshot: AcpClientSnapshot }
  | { type: 'session-update'; sessionId: string; update: unknown }
  | { type: 'session-event'; event: unknown }
  | { type: 'reverse-request'; id: number | string; method: string; params: unknown };

export interface IpcDeps {
  client: AcpClient;
  store: DesktopStore;
  getWindow: () => BrowserWindow | undefined;
  appVersion: string;
  runtimeBinary: () => string;
  logFile: () => string;
  log: (message: string, source?: 'desktop' | 'acp' | 'renderer') => void;
  diagnosticLogs: DiagnosticLogBuffer;
}

export function sendRendererEvent(deps: IpcDeps, event: RendererEvent): void {
  const win = deps.getWindow();
  if (!win || win.isDestroyed()) return;
  win.webContents.send('acp:event', event);
}

export function sendDiagnosticLog(deps: IpcDeps, entry: DiagnosticLogEntry): void {
  const win = deps.getWindow();
  if (!win || win.isDestroyed()) return;
  win.webContents.send('desktop:diagnostic-log', entry);
}

function serializeError(error: unknown): { message: string; code?: number; data?: unknown } {
  if (error && typeof error === 'object') {
    const candidate = error as { message?: string; code?: number; data?: unknown };
    return {
      message: typeof candidate.message === 'string' ? candidate.message : String(error),
      code: typeof candidate.code === 'number' ? candidate.code : undefined,
      data: candidate.data,
    };
  }
  return { message: String(error) };
}

// registerIpc installs every renderer-facing channel exactly once. The ACP
// client lives only in the main process; the renderer never talks to the
// child process directly.
export function registerIpc(deps: IpcDeps): void {
  const selectedFileGrants = new SelectedFileGrants();
  ipcMain.handle('acp:invoke', async (_event, method: unknown, params: unknown) => {
    if (typeof method !== 'string' || method.trim() === '') {
      throw new Error('method is required');
    }
    try {
      return { ok: true, result: await deps.client.request(method, params) };
    } catch (error) {
      return { ok: false, error: serializeError(error) };
    }
  });

  ipcMain.on('acp:notify', (_event, method: unknown, params: unknown) => {
    if (typeof method === 'string' && method.trim() !== '') deps.client.notify(method, params);
  });

  ipcMain.on('acp:respond', (_event, id: unknown, result: unknown) => {
    if (typeof id === 'number' || typeof id === 'string') deps.client.respondTo(id, result);
  });

  ipcMain.on('acp:cancel-reverse', (_event, requestId: unknown) => {
    if (typeof requestId === 'number' || typeof requestId === 'string') {
      deps.client.cancelReverseRequest(requestId);
    }
  });

  ipcMain.handle('acp:state', () => deps.client.getState());

  ipcMain.handle('acp:restart', async (_event, cwd: unknown) => {
    const target = typeof cwd === 'string' && cwd.trim() !== '' ? cwd.trim() : undefined;
    try {
      const result = await deps.client.restart(target);
      const workspace = deps.client.getState().workspace;
      if (workspace) deps.store.set({ lastWorkspace: workspace });
      return { ok: true, result };
    } catch (error) {
      return { ok: false, error: serializeError(error) };
    }
  });

  ipcMain.handle('desktop:choose-directory', async (event, defaultPath?: string) => {
    if (!event.sender || event.sender.isDestroyed()) return null;
    // 传父窗口：Linux 下无父的 GTK 对话框缺少 transient-for 关联，
    // 会被窗口管理器摆到应用窗口后面；带父窗口后模态置顶。
    const parent = BrowserWindow.fromWebContents(event.sender) || undefined;
    const options = {
      defaultPath: defaultPath || undefined,
      properties: ['openDirectory', 'createDirectory'] as ('openDirectory' | 'createDirectory')[],
      title: 'Select working directory',
    };
    const result = parent ? await dialog.showOpenDialog(parent, options) : await dialog.showOpenDialog(options);
    const selected = result.canceled ? '' : result.filePaths[0] || '';
    if (!selected) return null;
    // workspace/extend resolves directory symlinks before adding its access
    // root. Return the same physical path so session/new and session/prompt
    // never disagree merely because the user chose a symlinked directory.
    try {
      return realpathSync.native(selected);
    } catch {
      return selected;
    }
  });

  // The renderer has no Node access. Keep generation and creation of the
  // first new-session directory in the privileged process, while leaving its
  // use as a next-session preference to the renderer/ACP session flow.
  ipcMain.handle('desktop:default-new-session-directory', () => initialNewSessionDirectory());

  ipcMain.handle('desktop:choose-home-background', async (event, defaultPath?: string) => {
    if (!event.sender || event.sender.isDestroyed()) return null;
    const parent = BrowserWindow.fromWebContents(event.sender) || undefined;
    const options = {
      defaultPath: typeof defaultPath === 'string' && defaultPath.trim() ? defaultPath : undefined,
      properties: ['openFile'] as ('openFile')[],
      title: 'Select home background image',
      filters: [{ name: 'Images', extensions: ['png', 'jpg', 'jpeg', 'webp', 'gif', 'bmp', 'avif'] }],
    };
    const result = parent ? await dialog.showOpenDialog(parent, options) : await dialog.showOpenDialog(options);
    const selected = result.canceled ? '' : result.filePaths[0] || '';
    if (!selected) return null;
    // Persist the explicit user selection before the renderer requests its
    // display data. The renderer still mirrors this value into its UI state.
    deps.store.set({ homeBackgroundImage: selected });
    return selected;
  });

  ipcMain.handle('desktop:home-background-data-url', (_event, path: unknown): ReadHomeImageResult =>
    readHomeImageDataURL(path, deps.store.get().homeBackgroundImage),
  );

  ipcMain.handle('desktop:choose-home-logo', async (event, defaultPath?: string) => {
    if (!event.sender || event.sender.isDestroyed()) return null;
    const parent = BrowserWindow.fromWebContents(event.sender) || undefined;
    const options = {
      defaultPath: typeof defaultPath === 'string' && defaultPath.trim() ? defaultPath : undefined,
      properties: ['openFile'] as ('openFile')[],
      title: 'Select home logo image',
      filters: [{ name: 'Images', extensions: ['png', 'jpg', 'jpeg', 'webp', 'gif', 'bmp', 'avif'] }],
    };
    const result = parent ? await dialog.showOpenDialog(parent, options) : await dialog.showOpenDialog(options);
    const selected = result.canceled ? '' : result.filePaths[0] || '';
    if (!selected) return null;
    deps.store.set({ homeLogoImage: selected });
    return selected;
  });

  ipcMain.handle('desktop:home-logo-data-url', (_event, path: unknown): ReadHomeImageResult =>
    readHomeImageDataURL(path, deps.store.get().homeLogoImage),
  );

  ipcMain.handle('desktop:choose-files', async (event) => {
    if (!event.sender || event.sender.isDestroyed()) return [];
    const parent = BrowserWindow.fromWebContents(event.sender) || undefined;
    const options = {
      properties: ['openFile', 'multiSelections'] as ('openFile' | 'multiSelections')[],
      title: 'Attach files',
    };
    const result = parent ? await dialog.showOpenDialog(parent, options) : await dialog.showOpenDialog(options);
    if (result.canceled) return [];
    return result.filePaths.map((path) => ({ path, grant: selectedFileGrants.issue(path) }));
  });

  ipcMain.handle('desktop:store-get', () => deps.store.get());

  ipcMain.handle('desktop:store-set', (_event, patch: unknown) => {
    if (!patch || typeof patch !== 'object' || Array.isArray(patch)) return deps.store.get();
    return deps.store.set(patch as Partial<DesktopStoreData>);
  });

  ipcMain.on('desktop:window-control', (event, action: unknown) => {
    const win = BrowserWindow.fromWebContents(event.sender);
    if (!win || win.isDestroyed()) return;
    switch (action) {
      case 'minimize':
        win.minimize();
        break;
      case 'maximize':
        if (win.isMaximized()) win.unmaximize();
        else win.maximize();
        break;
      case 'close':
        win.close();
        break;
      default:
        break;
    }
  });

  ipcMain.handle('desktop:read-file-base64', async (_event, grant: unknown) => {
    // Attachment bridge: only a just-selected file can be embedded. The
    // renderer gets no generic local-file read primitive, even though it still
    // receives the selected path for canonical ACP resource_link input.
    const MAX_BYTES = 8 * 1024 * 1024;
    if (typeof grant !== 'string' || grant.trim() === '') return { ok: false, error: 'invalid file selection' };
    const target = selectedFileGrants.consume(grant);
    if (!target) return { ok: false, error: 'file selection expired or was already used' };
    try {
      const info = statSync(target);
      if (!info.isFile()) return { ok: false, error: 'not a regular file' };
      if (info.size > MAX_BYTES) return { ok: false, error: `file exceeds ${MAX_BYTES} bytes` };
      return { ok: true, data: readFileSync(target).toString('base64'), size: info.size };
    } catch (error) {
      return { ok: false, error: error instanceof Error ? error.message : String(error) };
    }
  });

  ipcMain.handle('desktop:diagnostic-logs', () => deps.diagnosticLogs.snapshot());

  ipcMain.handle('desktop:app-info', () => ({
    version: deps.appVersion,
    platform: process.platform,
    arch: process.arch,
    runtimeBinary: (() => {
      try {
        return deps.runtimeBinary();
      } catch {
        return '';
      }
    })(),
  }));

  ipcMain.on('desktop:log-diagnostic', (_event, message: unknown) => {
    const text = String(message || '').replace(/[\r\n]+/g, ' ').slice(0, 2000);
    if (!text) return;
    deps.log(text, 'renderer');
  });

  deps.diagnosticLogs.subscribe((entry) => {
    sendDiagnosticLog(deps, entry);
  });
}
