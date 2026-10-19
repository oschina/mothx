// Desktop e2e smoke：真实启动 Electron + ACP 子进程，断言关键日志行。
// 需要图形环境（Linux 下 DISPLAY）；无显示环境时明确失败，不能把未启动
// Desktop 当作 smoke 通过。
// 用法：npm run build && npm run e2e

import { spawn, spawnSync } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createInterface } from 'node:readline';
import { fileURLToPath } from 'node:url';

const isLinux = process.platform === 'linux';
if (isLinux && !process.env.DISPLAY && !process.env.WAYLAND_DISPLAY) {
  console.error('e2e smoke requires DISPLAY or WAYLAND_DISPLAY; refusing to report a skipped desktop launch as success');
  process.exit(1);
}

const electronBin = process.platform === 'win32' ? 'npx.cmd' : 'npx';
const userDataDir = mkdtempSync(join(tmpdir(), 'mothx-desktop-e2e-'));
const child = spawn(electronBin, ['electron', '.', '--no-sandbox', '--disable-gpu', `--user-data-dir=${userDataDir}`], {
  cwd: new URL('..', import.meta.url).pathname,
  env: { ...process.env },
  stdio: ['ignore', 'pipe', 'pipe'],
  detached: process.platform !== 'win32',
});

const checks = [
  { name: 'acp runtime ready', pattern: /acp state: ready/ },
  { name: 'renderer bootstrap home', pattern: /bootstrap done view=home/ },
  { name: 'renderer connection ready', pattern: /renderer conn ready/ },
];
const seen = new Set();
let finished = false;

const finish = (code, message) => {
  if (finished) return;
  finished = true;
  if (message) console.log(message);
  try {
    if (child.pid && process.platform !== 'win32') process.kill(-child.pid, 'SIGTERM');
    else child.kill('SIGTERM');
  } catch {
    // ignore
  }
  setTimeout(() => {
    try {
      if (child.pid && process.platform !== 'win32') process.kill(-child.pid, 'SIGKILL');
    } catch {
      // ignore
    }
    try {
      rmSync(userDataDir, { recursive: true, force: true });
    } catch {
      // The Electron process may still be closing on platforms that retain a
      // handle briefly. Leaving only this unique temporary directory is safer
      // than touching unrelated application instances.
    }
    process.exit(code);
  }, 800);
};

const observe = (stream) => {
  const rl = createInterface({ input: stream });
  rl.on('line', (line) => {
    for (const check of checks) {
      if (!seen.has(check.name) && check.pattern.test(line)) {
        seen.add(check.name);
        console.log(`PASS ${check.name}`);
      }
    }
    if (seen.size === checks.length && !finished) {
      console.log('E2E SMOKE DONE');
      // Startup alone is a weak gate: also drive the packaged runtime through a
      // real session round trip (new -> prompt -> close -> load) before passing.
      const roundtrip = spawnSync(process.execPath, [fileURLToPath(new URL('./acp-roundtrip.mjs', import.meta.url))], { stdio: 'inherit' });
      finish(roundtrip.status === 0 ? 0 : 1, roundtrip.status === 0 ? undefined : 'ACP roundtrip failed');
    }
  });
};
observe(child.stdout);
observe(child.stderr);

child.on('exit', (code) => {
  if (!finished) finish(seen.size === checks.length ? 0 : 1, `electron exited early: ${code}`);
});

setTimeout(() => {
  const missing = checks.filter((check) => !seen.has(check.name)).map((check) => check.name);
  finish(missing.length === 0 ? 0 : 1, missing.length ? `E2E TIMEOUT missing: ${missing.join(', ')}` : undefined);
}, 60000);
