import { spawn } from 'node:child_process';
import { cpSync, mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { build } from 'esbuild';
import { build as viteBuild, type Plugin } from 'vite';

import { displayFailureHelp, displayProblem, looksLikeDisplayFailure } from './dev-environment.ts';
import {
  createShutdownController,
  DEV_SHUTDOWN_SIGNALS,
  killProcessTree,
  type DevShutdownSignal,
} from './dev-shutdown.ts';

/**
 * Bounded development runner for MothX Desktop.
 *
 * - Builds main and preload once at startup. Changes to `main/` or `preload/`
 *   require a manual restart (documented in desktop/README.md).
 * - Builds the React renderer with Vite in watch mode, writing classic-script
 *   bundles into `dist/renderer` and recopying the app icon.
 * - Spawns Electron in explicit dev mode (`MOTHX_DESKTOP_DEV=1`), which enables
 *   DevTools, a localhost-only Chrome remote debugging port, and auto-reload
 *   when the renderer-ready signal appears.
 * - Owns shutdown: Ctrl+C closes the renderer watcher and terminates the whole
 *   Electron process tree, escalating to a forced kill if Electron does not
 *   exit, so nothing survives the runner.
 *
 * The renderer is still served as static `file://` assets; this script does not
 * start an HTTP server and the renderer does not call local APIs.
 */

const root = fileURLToPath(new URL('..', import.meta.url));
const out = join(root, 'dist');
const rendererOut = join(out, 'renderer');
const devUserData = process.env.MOTHX_DESKTOP_USER_DATA || join(root, '.dev-user-data');
const rendererReadySignal = join(rendererOut, '.mothx-renderer-ready');

function notifyRendererReady(): void {
  writeFileSync(rendererReadySignal, String(Date.now()));
}

function copyRendererStatic(): void {
  mkdirSync(rendererOut, { recursive: true });
  cpSync(join(root, 'resources', 'mothx.png'), join(rendererOut, 'mothx.png'));
  notifyRendererReady();
}

async function buildMainAndPreload(): Promise<void> {
  await build({
    entryPoints: [join(root, 'main', 'index.ts')],
    outfile: join(out, 'main.cjs'),
    bundle: true,
    platform: 'node',
    format: 'cjs',
    target: 'node22',
    external: ['electron'],
    sourcemap: true,
  });

  await build({
    entryPoints: [join(root, 'preload', 'index.ts')],
    outfile: join(out, 'preload.cjs'),
    bundle: true,
    platform: 'node',
    format: 'cjs',
    target: 'node22',
    external: ['electron'],
    sourcemap: true,
  });
}

async function buildRenderer(): Promise<{ close: () => Promise<void> }> {
  copyRendererStatic();

  // Signal only after a complete bundle update so the main-process watcher
  // never reloads file:// while index.html is being replaced.
  const signalRendererReady: Plugin = {
    name: 'desktop-dev-renderer-ready',
    writeBundle() {
      notifyRendererReady();
    },
  };

  const watcher = await viteBuild({
    configFile: join(root, 'renderer', 'vite.config.ts'),
    logLevel: 'warn',
    plugins: [signalRendererReady],
    build: {
      sourcemap: true,
      watch: {},
    },
  });

  return {
    close: async () => {
      if (watcher && 'close' in watcher) await watcher.close();
    },
  };
}

function electronCommand(): { command: string; args: string[] } {
  // Spawn the project-local binary directly. Unlike a package-manager wrapper,
  // it stays attached for the entire Desktop session so the CDP endpoint stays
  // available for screenshots and review.
  const executable = process.platform === 'win32' ? 'electron.cmd' : 'electron';
  return {
    command: join(root, 'node_modules', '.bin', executable),
    args: ['.', '--no-sandbox', `--user-data-dir=${devUserData}`],
  };
}

/**
 * Desktop is a graphical client, so it needs a display server. On Linux without
 * a usable display, Chromium aborts during platform initialization and dies on
 * SIGSEGV, which the user only ever sees as a white, frameless window followed by
 * a crash line. See scripts/dev-environment.ts.
 */

async function run(): Promise<void> {
  const problem = displayProblem({ platform: process.platform, env: process.env });
  if (problem) {
    console.error(`[desktop-dev] ${problem}`);
    process.exit(1);
  }

  await buildMainAndPreload();
  const rendererWatcher = await buildRenderer();

  const { command: electronBin, args: electronArgs } = electronCommand();
  const child = spawn(electronBin, electronArgs, {
    cwd: root,
    env: { ...process.env, MOTHX_DESKTOP_DEV: '1' },
    // stdout stays on the terminal (DevTools and renderer output); stderr is
    // piped so a display that exists but cannot be connected to is recognised
    // from Chromium's own diagnostic and explained instead of only crashing.
    stdio: ['inherit', 'inherit', 'pipe'],
    shell: process.platform === 'win32',
    // Lead a dedicated process group so shutdown can terminate Electron's
    // renderer/GPU/utility children as a unit instead of orphaning them, and so
    // this runner — not the terminal — decides when Electron receives a signal.
    detached: process.platform !== 'win32',
  });

  explainDisplayFailure(child.stderr);

  const shutdown = createShutdownController({
    closeWatcher: rendererWatcher.close,
    sendSignal: (escalation) => {
      if (child.pid === undefined) return;
      killProcessTree({ pid: child.pid, platform: process.platform, escalation });
    },
    exit: (code) => process.exit(code),
  });

  for (const signal of DEV_SHUTDOWN_SIGNALS) {
    process.on(signal, () => shutdown.request(signal as DevShutdownSignal));
  }

  child.on('exit', (code) => shutdown.childExited(code));
  child.on('error', (error) => shutdown.childFailed(error));
}

/**
 * Forward Electron's stderr to the terminal unchanged, and once a display
 * failure is recognised, print what to do about it. A display that is set but
 * unusable (stale `:0`, dead X server, X11 forwarding that never connected)
 * cannot be detected from the environment, so Chromium's message is the
 * diagnosis.
 */
function explainDisplayFailure(stderr: NodeJS.ReadableStream | null): void {
  if (!stderr) return;
  let reported = false;
  stderr.on('data', (chunk: Buffer | string) => {
    const text = chunk.toString();
    process.stderr.write(text);
    if (reported || !looksLikeDisplayFailure(text)) return;
    reported = true;
    console.error(`[desktop-dev] ${displayFailureHelp()}`);
  });
}

run().catch((error: unknown) => {
  console.error('desktop dev runner failed:', error instanceof Error ? error.message : String(error));
  process.exit(1);
});
