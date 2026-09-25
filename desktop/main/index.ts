import { app, BrowserWindow, dialog, Menu, shell } from 'electron';
import { createWriteStream, existsSync, mkdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

import { AcpClient } from './acp-client';
import {
  configureDevModeSwitches,
  devRemoteDebuggingPort,
  enableDevModeWindow,
  isDesktopDevMode,
  rendererDistPath,
} from './dev-mode';
import { initialNewSessionDirectory } from './default-new-session-directory';
import { createDiagnosticLogger, DiagnosticLogBuffer } from './diagnostic-logs';
import { registerIpc, sendRendererEvent, type IpcDeps } from './ipc';
import { DesktopStore } from './store';

// MothX Desktop is a pure ACP client: the Electron main process owns one
// `mothx acp` child (JSON-RPC over stdio) and the renderer is a standalone
// frontend under desktop/renderer. There is no serve process, no HTTP
// channel, and no dependency on ui/.

let windowRef: BrowserWindow | undefined;
let desktopLogPath = '';
let logStream: ReturnType<typeof createWriteStream> | undefined;
const diagnosticLogs = new DiagnosticLogBuffer({ maxEntries: 400 });

const logger = createDiagnosticLogger({
  buffer: diagnosticLogs,
  getLogStream: () => logStream,
});
function logDesktopEvent(message: string, source: 'desktop' | 'acp' | 'renderer' = 'desktop'): void {
  logger.log(message, source);
}

const store = new DesktopStore(app.getPath('userData'));

function binaryPath(): string {
  const name = process.platform === 'win32' ? 'mothx.exe' : 'mothx';
  const roots = [
    // Explicit development override wins so `npm start` can use a fresh build.
    process.env.MOTHX_BINARY || '',
    // electron-builder places the explicit `vendor` file pattern beside
    // `resources/app` when asar is disabled.
    join(process.resourcesPath, '..', 'vendor', 'mothx', 'bin', name),
    join(process.resourcesPath, '..', 'vendor', 'mothx', name),
    join(process.resourcesPath, 'app', 'vendor', 'mothx', 'bin', name),
    join(process.resourcesPath, 'app', 'vendor', 'mothx', name),
    join(__dirname, '..', '..', 'vendor', 'mothx', 'bin', name),
    join(__dirname, '..', '..', 'vendor', 'mothx', name),
    join(__dirname, '..', 'vendor', 'mothx', 'bin', name),
    join(__dirname, '..', 'vendor', 'mothx', name),
    // Development fallback: the repository build output.
    join(__dirname, '..', '..', '..', 'bin', name),
  ].filter((candidate) => candidate !== '');
  const found = roots.find((candidate) => existsSync(candidate));
  if (!found) throw new Error(`MothX runtime not found. Checked:\n${roots.join('\n')}`);
  return found;
}

// This only selects the child process's initial cwd. Session working
// directories are supplied independently in ACP requests and are never
// constrained by this process startup path.
function resolveRuntimeCwd(): string {
  const last = store.get().lastWorkspace;
  if (last) {
    try {
      if (statSync(last).isDirectory()) return last;
    } catch {
      logDesktopEvent(`stored default work directory is unavailable, creating a fresh session directory: ${last}`);
    }
  }
  // This is only the ACP child startup cwd. Session cwd remains independent
  // and is sent in the canonical session/new and session/setWorkDir flows.
  return initialNewSessionDirectory();
}

const client = new AcpClient({
  onState: (snapshot) => {
    logDesktopEvent(`acp state: ${snapshot.state}${snapshot.error ? ` error: ${snapshot.error.message}` : ''}`);
    sendRendererEvent(deps, { type: 'state', snapshot });
  },
  onSessionUpdate: (notification) => {
    sendRendererEvent(deps, { type: 'session-update', sessionId: notification.sessionId, update: notification.update });
  },
  onSessionEvent: (event) => {
    sendRendererEvent(deps, { type: 'session-event', event });
  },
  onReverseRequest: (request) => {
    sendRendererEvent(deps, { type: 'reverse-request', id: request.id, method: request.method, params: request.params });
  },
  onWorktreeStatus: (notification) => {
    sendRendererEvent(deps, { type: 'worktree-status', worktree: notification.worktree });
  },
  onLog: (line) => logDesktopEvent(line, 'acp'),
});

const deps: IpcDeps = {
  client,
  store,
  getWindow: () => windowRef,
  appVersion: app.getVersion(),
  runtimeBinary: binaryPath,
  logFile: () => desktopLogPath,
  log: logDesktopEvent,
  diagnosticLogs,
};

// Dev mode switches (remote debugging port, etc.) must be configured before
// the application is ready.
configureDevModeSwitches(app.commandLine);

// AppImage mounts are commonly `nosuid`, so Electron's SUID sandbox helper
// cannot be used even though the application itself is otherwise valid. The
// desktop app only spawns its bundled ACP runtime, so use Chromium's fallback
// for packaged Linux builds.
if (process.platform === 'linux' && app.isPackaged) {
  app.commandLine.appendSwitch('no-sandbox');
  // Some Linux hosts and CI-produced AppImage environments do not provide a
  // writable /dev/shm, which crashes Chromium's renderer before the UI can
  // paint. Keep Chromium's shared-memory data under Electron's userData dir.
  app.commandLine.appendSwitch('disable-dev-shm-usage');
  // Avoid GPU-process startup failures on headless/remote Linux desktops.
  app.commandLine.appendSwitch('disable-gpu');
}

function rendererIndex(): string {
  return join(__dirname, 'renderer', 'index.html');
}

function createWindow(): void {
  const isMac = process.platform === 'darwin';
  windowRef = new BrowserWindow({
    title: 'MothX Desktop',
    width: 1440,
    height: 900,
    minWidth: 900,
    minHeight: 640,
    show: false,
    backgroundColor: store.get().theme === 'dark' ? '#1e1e1e' : '#ffffff',
    // macOS keeps native traffic lights; Windows/Linux use the renderer's
    // custom titlebar controls (see workbuddy prototype layout).
    ...(isMac ? { titleBarStyle: 'hidden' as const, trafficLightPosition: { x: 12, y: 12 } } : { frame: false }),
    webPreferences: {
      preload: join(__dirname, 'preload.cjs'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: !(process.platform === 'linux' && app.isPackaged),
      devTools: !app.isPackaged || isDesktopDevMode(),
    },
  });
  const win = windowRef;
  win.webContents.setWindowOpenHandler(({ url: target }) => {
    if (target.startsWith('http://') || target.startsWith('https://')) void shell.openExternal(target);
    return { action: 'deny' };
  });
  win.webContents.on('render-process-gone', (_event, details) => {
    logDesktopEvent(`renderer process gone: ${details.reason} ${details.exitCode}`, 'renderer');
  });
  win.webContents.on('did-fail-load', (_event, errorCode, errorDescription, validatedURL, isMainFrame) => {
    if (isMainFrame) logDesktopEvent(`failed to load ${validatedURL}: ${errorCode} ${errorDescription}`, 'renderer');
  });
  win.webContents.on('console-message', (_event, level, message, line, sourceId) => {
    if (level >= 2) logDesktopEvent(`console[${level}] ${sourceId}:${line} ${message}`, 'renderer');
  });
  win.once('ready-to-show', () => win.show());
  void win.loadFile(rendererIndex()).catch((error: unknown) => {
    showStartupError(error);
  });

  if (isDesktopDevMode()) {
    const port = devRemoteDebuggingPort();
    logDesktopEvent(`dev mode active: DevTools enabled, remote debugging on localhost:${port}`);
    const cleanupDevMode = enableDevModeWindow(win, rendererDistPath(__dirname));
    win.once('closed', () => {
      cleanupDevMode();
    });
  }
}

function showStartupError(error: unknown): void {
  const message = error instanceof Error ? error.message : String(error);
  void dialog.showErrorBox('Unable to start MothX', `${message}\n\nCheck the desktop log under your MothX user data directory.`);
}

async function startRuntime(): Promise<void> {
  const logDir = join(app.getPath('userData'), 'logs');
  mkdirSync(logDir, { recursive: true });
  desktopLogPath = join(logDir, 'desktop.log');
  logStream = createWriteStream(desktopLogPath, { flags: 'a' });

  const binary = binaryPath();
  const runtimeCwd = resolveRuntimeCwd();
  logDesktopEvent(`starting ACP runtime ${binary} (runtime cwd: ${runtimeCwd})`);
  await client.start({
    binary,
    args: ['acp'],
    cwd: runtimeCwd,
    clientInfo: { name: 'mothx-desktop', title: 'MothX Desktop', version: app.getVersion() },
    permissionTimeout: '30m',
    questionTimeout: '30m',
  });
}

const gotLock = app.requestSingleInstanceLock();
if (!gotLock) {
  app.quit();
} else {
  app.on('second-instance', () => {
    if (windowRef && !windowRef.isDestroyed()) {
      if (windowRef.isMinimized()) windowRef.restore();
      windowRef.show();
      windowRef.focus();
    }
  });
  app.whenReady().then(async () => {
    if (!app.isPackaged) {
      Menu.setApplicationMenu(Menu.buildFromTemplate([
        { role: 'editMenu' },
        {
          label: 'View',
          submenu: [{ role: 'reload' }, { role: 'toggleDevTools' }, { type: 'separator' }, { role: 'togglefullscreen' }],
        },
        { role: 'windowMenu' },
      ]));
    } else {
      Menu.setApplicationMenu(null);
    }
    registerIpc(deps);
    createWindow();
    try {
      await startRuntime();
    } catch (error) {
      // The window is already visible; the renderer surfaces the error state
      // and offers retry/settings, so only log fatal startup problems here.
      logDesktopEvent(`ACP runtime startup failed: ${error instanceof Error ? error.message : String(error)}`);
    }
  });
  app.on('before-quit', () => {
    void client.stop();
  });
  app.on('window-all-closed', () => {
    if (process.platform !== 'darwin') app.quit();
  });
  app.on('activate', () => {
    if (!windowRef || windowRef.isDestroyed()) createWindow();
  });
}
