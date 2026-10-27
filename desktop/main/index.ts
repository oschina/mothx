import { app, BrowserWindow, dialog, Menu, shell } from 'electron';
import { createWriteStream, mkdirSync, renameSync, statSync } from 'node:fs';
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
import { createDiagnosticLogger, DiagnosticLogBuffer, shouldRotateDesktopLog } from './diagnostic-logs';
import { applyNativeWindowButtons, registerIpc, sendRendererEvent, setDiagnosticStreaming, type IpcDeps } from './ipc';
import { shouldDisableDevShm, shouldDisableGpu } from './linux-flags';
import {
  describeRuntimeBinary,
  resolveRuntimeBinary,
  type RuntimeBinaryLookup,
  type RuntimeBinaryStatus,
} from './runtime-binary';
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

function runtimeLookup(): RuntimeBinaryLookup {
  const settings = store.get();
  return {
    platform: process.platform,
    arch: process.arch,
    env: process.env,
    resourcesPath: process.resourcesPath,
    distDir: __dirname,
    settings: { source: settings.runtimeSource, path: settings.runtimeBinaryPath },
  };
}

// Which `mothx` executable becomes the ACP runtime: the bundled one by
// default, a user-configured custom binary (desktop settings), or the
// MOTHX_BINARY development override. See main/runtime-binary.ts.
function binaryPath(): string {
  return resolveRuntimeBinary(runtimeLookup()).path;
}

function runtimeStatus(): RuntimeBinaryStatus {
  return describeRuntimeBinary(runtimeLookup());
}

// This only selects the child process's initial cwd. It is also the one
// quick-start scratch directory available on the Home screen before a person
// explicitly starts a new task. It is never persisted as a default workspace:
// every later session supplies its own cwd through ACP.
function resolveRuntimeCwd(): string {
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
  runtimeStatus,
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
  // Only the hosts that cannot provide Chromium's shared-memory path need the
  // /tmp fallback; normal desktops keep the faster compositor path.
  if (shouldDisableDevShm()) {
    app.commandLine.appendSwitch('disable-dev-shm-usage');
  }
  // Unconditional GPU disable forced software compositing (scroll/animation
  // jank) on every packaged Linux desktop. Keep the fallback only for headless
  // hosts, or when MOTHX_DESKTOP_DISABLE_GPU explicitly asks for it.
  if (shouldDisableGpu({ platform: process.platform, packaged: app.isPackaged, env: process.env })) {
    app.commandLine.appendSwitch('disable-gpu');
  }
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
  // Apply the persisted titlebar projection immediately: on macOS the native
  // traffic lights are hidden when the user picked the Windows 11 chrome.
  applyNativeWindowButtons(win, store.get().windowStyle);
  win.webContents.setWindowOpenHandler(({ url: target }) => {
    if (target.startsWith('http://') || target.startsWith('https://')) void shell.openExternal(target);
    return { action: 'deny' };
  });
  // The preload bridge exposes the local ACP client. A page that somehow
  // navigates this existing WebContents must never inherit that bridge; all
  // intentional external links use window.open and the handler above.
  win.webContents.on('will-navigate', (event, target) => {
    event.preventDefault();
    logDesktopEvent(`blocked renderer navigation: ${target}`, 'renderer');
  });
  win.webContents.on('render-process-gone', (_event, details) => {
    logDesktopEvent(`renderer process gone: ${details.reason} ${details.exitCode}`, 'renderer');
  });
  win.webContents.on('did-start-loading', () => {
    // A reload drops the preload subscriber without running its cleanup; do not
    // keep projecting diagnostic lines to a renderer that is no longer viewing
    // them (the viewer re-subscribes on mount).
    setDiagnosticStreaming(false);
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
  try {
    if (shouldRotateDesktopLog(statSync(desktopLogPath).size)) {
      renameSync(desktopLogPath, `${desktopLogPath}.1`);
    }
  } catch {
    // First run or an unreadable/ineffective rotation target: keep appending.
  }
  logStream = createWriteStream(desktopLogPath, { flags: 'a' });

  const runtime = resolveRuntimeBinary(runtimeLookup());
  const runtimeCwd = resolveRuntimeCwd();
  logDesktopEvent(`starting ACP runtime ${runtime.path} (source: ${runtime.origin}, runtime cwd: ${runtimeCwd})`);
  const status = runtimeStatus();
  if (status.fallback === 'custom') {
    logDesktopEvent(`configured custom runtime binary is unavailable (${status.configuredPath}), using the bundled runtime`);
  }
  await client.start({
    binary: runtime.path,
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
