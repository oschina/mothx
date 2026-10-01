import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const root = fileURLToPath(new URL('..', import.meta.url));
const pkg = JSON.parse(await readFile(join(root, 'package.json'), 'utf8'));
const devScript = await readFile(join(root, 'scripts', 'dev.ts'), 'utf8');

test('npm run dev runs the bounded dev runner', () => {
  assert.ok(pkg.scripts.dev, 'dev script should exist');
  assert.match(pkg.scripts.dev, /tsx scripts\/dev\.ts/, 'dev script must run scripts/dev.ts');
  assert.doesNotMatch(pkg.scripts.dev, /build:runtime/, 'frontend restarts must not rebuild the ACP runtime');
});

test('dev runner enables explicit dev mode when spawning Electron', () => {
  assert.match(
    devScript,
    /MOTHX_DESKTOP_DEV[\s]*:[\s]*['"]1['"]/,
    'dev runner must spawn Electron with MOTHX_DESKTOP_DEV=1',
  );
  assert.match(devScript, /node_modules', '\.bin'/, 'dev runner must keep the project-local Electron process attached');
  assert.match(devScript, /--user-data-dir=\$\{devUserData\}/, 'dev runner must isolate itself from an installed Desktop instance');
  assert.match(devScript, /\.mothx-renderer-ready/, 'dev runner must signal only after a completed renderer update');
});

test('dev runner refuses to start where Desktop cannot show a window', () => {
  assert.match(
    devScript,
    /from '\.\/dev-environment\.ts'/,
    'dev runner must use the tested display preconditions',
  );
  assert.match(
    devScript,
    /displayProblem\(\{ platform: process\.platform, env: process\.env \}\)/,
    'dev runner must check for a display server',
  );
  assert.match(
    devScript,
    /const problem = displayProblem[\s\S]{0,120}process\.exit\(1\)/,
    'dev runner must fail before building instead of showing a white window and crashing',
  );
  assert.match(
    devScript,
    /stdio: \['inherit', 'inherit', 'pipe'\]/,
    "dev runner must read Electron's stderr to recognise a display that is set but unusable",
  );
  assert.match(
    devScript,
    /explainDisplayFailure\(child\.stderr\)/,
    'dev runner must explain a display failure from Chromium output',
  );
  assert.match(
    devScript,
    /process\.stderr\.write\(text\)/,
    "piping Electron's stderr must not swallow it",
  );
});

test('dev runner owns shutdown so Ctrl+C terminates everything', () => {
  assert.match(
    devScript,
    /from '\.\/dev-shutdown\.ts'/,
    'dev runner must delegate shutdown to the tested dev-shutdown module',
  );
  assert.match(
    devScript,
    /detached:\s*process\.platform !== 'win32'/,
    'dev runner must lead a dedicated process group so the whole Electron tree can be stopped',
  );
  assert.match(devScript, /for \(const signal of DEV_SHUTDOWN_SIGNALS\)/, 'dev runner must handle every shutdown signal');
  assert.match(
    devScript,
    /child\.on\('exit',[\s\S]{0,40}shutdown\.childExited/,
    'dev runner must report an Electron exit to the shutdown controller',
  );
  assert.match(
    devScript,
    /child\.on\('error',[\s\S]{0,40}shutdown\.childFailed/,
    'dev runner must report a spawn failure to the shutdown controller',
  );
  assert.doesNotMatch(
    devScript,
    /if \(!exited\)/,
    'the old guard that skipped the watcher close after an interrupt must not come back',
  );
  assert.doesNotMatch(
    devScript,
    /child\.kill\(/,
    'shutting down only the Electron main process would orphan its renderer children',
  );
});

test('npm run dev keeps the pure ACP architecture: no serve/HTTP proxy', () => {
  assert.doesNotMatch(pkg.scripts.dev, /serve/, 'dev script must not start mothx serve');
  assert.doesNotMatch(
    pkg.scripts.dev,
    /http:\/\/localhost/,
    'dev script must not start a renderer HTTP server',
  );
});
