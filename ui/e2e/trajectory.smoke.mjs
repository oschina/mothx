import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { createServer as createNetServer } from 'node:net';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { extname, join } from 'node:path';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));
const dist = join(root, 'dist');
const chromium = process.env.CHROMIUM || '/usr/bin/chromium';
assert.ok(existsSync(join(dist, 'index.html')), 'build ui before running the trajectory smoke test');

const records = [
  { id: 'transcript:session-e2e:user-1', sessionId: 'session-e2e', seq: 1, source: 'transcript', kind: 'user', status: 'completed', summary: 'User', preview: 'Inspect repository', content: 'Inspect repository', timestamp: '2026-08-21T10:00:00Z' },
  { id: 'run:session-e2e:run-1', sessionId: 'session-e2e', runId: 'run-1', source: 'run', kind: 'run', status: 'completed', summary: 'Run completed', preview: 'completed', timestamp: '2026-08-21T10:00:01Z', startedAt: '2026-08-21T10:00:01Z', completedAt: '2026-08-21T10:00:03Z' },
  { id: 'tool:session-e2e:tool-1', sessionId: 'session-e2e', runId: 'run-1', source: 'transcript', kind: 'tool', status: 'completed', summary: 'read', preview: 'Read package.json', toolCallId: 'tool-1', timestamp: '2026-08-21T10:00:02Z', input: { path: 'package.json' }, output: 'ok' },
  { id: 'capability:session-e2e:cap-1', sessionId: 'session-e2e', source: 'capability', kind: 'capability', status: 'completed', summary: 'Capability changed', preview: 'browser -> enabled', timestamp: '2026-08-21T10:00:04Z' },
];

const session = { id: 'session-e2e', title: 'Trajectory E2E', channelType: 'local', channelLabel: 'Local', bound: false, running: false, workDir: '/tmp/project' };

function json(res, status, value, headers = {}) {
  const body = JSON.stringify(value);
  res.writeHead(status, { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body), ...headers });
  res.end(body);
}

const server = createServer((req, res) => {
  const url = new URL(req.url, 'http://127.0.0.1');
  if (url.pathname === '/health') return json(res, 200, { status: 'ok' });
  if (url.pathname === '/api/auth/status') return json(res, 200, { authenticated: true, authEnabled: false });
  if (url.pathname === '/api/status') return json(res, 200, { status: 'ok', features: { webUI: true, openaiAPI: true }, channels: [] });
  if (url.pathname === '/api/channels') return json(res, 200, []);
  if (url.pathname === '/api/sessions') return json(res, 200, { sessions: [session], total: 1 });
  if (url.pathname === '/api/session-bindings') return json(res, 200, []);
  if (url.pathname === '/api/settings') return json(res, 200, {});
  if (url.pathname === '/api/cron') return json(res, 200, { enabled: false, running: false, jobs: [] });
  if (url.pathname === '/api/memory') return json(res, 200, { enabled: false, content: '' });
  if (url.pathname === '/api/stats/summary') return json(res, 200, {});
  if (url.pathname === '/api/serve/config') return json(res, 200, { mode: 'agent', auth: { enabled: false, tokens: [] }, features: { webUI: true } });
  if (url.pathname === '/v1/models') return json(res, 200, { data: [{ id: 'trajectory-model', owned_by: 'e2e' }] });
  if (url.pathname === '/api/models/catalog') return json(res, 200, {
    object: 'list', providers: ['e2e'], defaultProvider: 'e2e', defaultModel: 'trajectory-model',
    data: [{ id: 'trajectory-model', name: 'Trajectory Model', object: 'model', owned_by: 'e2e', provider: 'e2e', input: ['text'] }],
  });
  if (url.pathname === '/api/session-tools/catalog') return json(res, 200, { tools: [] });
  if (url.pathname === '/api/sessions/session-e2e/messages') return json(res, 200, { messages: [{ id: 'user-1', seq: 1, role: 'user', content: 'Inspect repository' }], hasMore: false });
  if (url.pathname === '/api/sessions/session-e2e/run-events') return json(res, 200, { events: [] });
  if (url.pathname === '/api/sessions/session-e2e/capability-events') return json(res, 200, { events: [] });
  if (url.pathname === '/api/sessions/session-e2e/runtime') return json(res, 200, { mode: 'agent', activeRun: null, pendingApprovals: [] });
  if (url.pathname === '/api/sessions/session-e2e/subagents') return json(res, 200, { subagents: [] });
  if (url.pathname === '/api/sessions/session-e2e/trajectory') return json(res, 200, { sessionId: 'session-e2e', records, highWater: { entrySeq: 1, runSeq: 1, capabilitySeq: 1, decisionSeq: 0 }, hasMore: false });
  if (url.pathname === '/api/sessions/session-e2e/export' && req.method === 'HEAD') {
    return res.writeHead(200, { 'Content-Type': 'application/x-ndjson; charset=utf-8', 'Content-Disposition': 'attachment; filename="mothx-session-session-e2e.log"' }).end();
  }
  if (url.pathname === '/api/sessions/session-e2e/export') return json(res, 200, { ok: true });
  if (url.pathname.startsWith('/api/')) return json(res, 200, {});
  if (url.pathname === '/' || !extname(url.pathname)) {
    res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
    return res.end(readFileSync(join(dist, 'index.html')));
  }
  try {
    const file = join(dist, url.pathname);
    const contentTypes = { '.css': 'text/css; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.json': 'application/json; charset=utf-8' };
    res.writeHead(200, { 'Content-Type': contentTypes[extname(file)] || 'application/octet-stream' });
    return res.end(readFileSync(file));
  } catch {
    res.writeHead(404).end();
  }
});
server.on('upgrade', (_req, socket) => socket.destroy());

function listen(target) { return new Promise((resolve) => target.listen(0, '127.0.0.1', resolve)); }
function freePort() {
  const probe = createNetServer();
  return new Promise((resolve) => probe.listen(0, '127.0.0.1', () => {
    const port = probe.address().port;
    probe.close(() => resolve(port));
  }));
}

async function openBrowser(url) {
  const debugPort = await freePort();
  const browser = spawn(chromium, ['--headless=new', '--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage', `--remote-debugging-port=${debugPort}`, `--user-data-dir=/tmp/mothx-trajectory-e2e-${debugPort}`, url], { stdio: 'ignore' });
  let target;
  for (let i = 0; i < 100; i += 1) {
    try {
      const response = await fetch(`http://127.0.0.1:${debugPort}/json/list`);
      target = (await response.json()).find((entry) => entry.type === 'page' && entry.webSocketDebuggerUrl);
      if (target) break;
    } catch { await new Promise((resolve) => setTimeout(resolve, 50)); }
  }
  assert.ok(target, 'Chromium DevTools endpoint did not become ready');
  const socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  let id = 0;
  const pending = new Map();
  socket.onmessage = (event) => {
    const message = JSON.parse(event.data);
    const waiter = pending.get(message.id);
    if (!waiter) return;
    pending.delete(message.id);
    if (message.error) waiter.reject(new Error(message.error.message)); else waiter.resolve(message.result);
  };
  const call = (method, params = {}) => new Promise((resolve, reject) => {
    const requestID = ++id;
    pending.set(requestID, { resolve, reject });
    socket.send(JSON.stringify({ id: requestID, method, params }));
  });
  return { browser, socket, call, userDataDir: `/tmp/mothx-trajectory-e2e-${debugPort}` };
}

async function evaluate(cdp, expression) {
  const result = await cdp.call('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.text || 'browser evaluation failed');
  return result.result?.value;
}

async function waitFor(cdp, expression) {
  for (let i = 0; i < 120; i += 1) {
    if (await evaluate(cdp, expression)) return;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error(`timed out waiting for ${expression}`);
}

await listen(server);
const address = server.address();
let cdp;
try {
  cdp = await openBrowser(`http://127.0.0.1:${address.port}/#/chat?session=session-e2e&view=trajectory`);
  await cdp.call('Page.enable');
  await cdp.call('Runtime.enable');
  await waitFor(cdp, `document.querySelector('.trajectory-view') !== null`);
  await waitFor(cdp, `document.querySelectorAll('.trajectory-row').length >= 3`);
  assert.equal(await evaluate(cdp, `document.querySelector('.topbar') === null`), true);
  assert.equal(await evaluate(cdp, `document.querySelector('.chat-session-header') !== null`), true);
  assert.equal(await evaluate(cdp, `document.querySelector('.chat-view-tabs button.active')?.textContent.trim()`), '轨迹');
  await evaluate(cdp, `document.querySelector('.trajectory-row')?.click()`);
  await waitFor(cdp, `document.querySelector('.trajectory-details') !== null`);
  assert.equal(await evaluate(cdp, `document.querySelector('.trajectory-details')?.textContent.includes('预览')`), true);
  assert.equal(await evaluate(cdp, `document.querySelector('.trajectory-timeline') === null`), true);
  assert.equal(await evaluate(cdp, `document.querySelector('.trajectory-time') !== null`), true);
  assert.equal(await evaluate(cdp, `document.querySelector('.session-log-download')?.getAttribute('aria-label')`), '下载 Session 日志');
  await cdp.call('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
  await waitFor(cdp, `!matchMedia('(max-width: 900px)').matches && document.querySelector('.sidebar:not(.mobile-drawer)') !== null`);
  await waitFor(cdp, `getComputedStyle(document.querySelector('.app-shell')).gridTemplateColumns.startsWith('280px')`);
  assert.equal(await evaluate(cdp, `getComputedStyle(document.querySelector('.trajectory-layout')).display`), 'grid');
  assert.equal(await evaluate(cdp, `getComputedStyle(document.querySelector('.sidebar')).paddingTop`), '16px');
  assert.equal(await evaluate(cdp, `getComputedStyle(document.querySelector('.sidebar')).width`), '280px');
  assert.equal(await evaluate(cdp, `document.querySelector('.side-search') === null`), true);
  assert.equal(await evaluate(cdp, `document.querySelector('.sidebar-browser-head') !== null`), true);
  assert.equal(await evaluate(cdp, `document.querySelector('.sidebar-browser-head .sidebar-section-label') === null`), true);
  assert.equal(await evaluate(cdp, `getComputedStyle(document.querySelector('.new-chat')).height`), '38px');
  assert.equal(await evaluate(cdp, `getComputedStyle(document.querySelector('.side-nav')).borderRadius`), '0px');
  assert.equal(await evaluate(cdp, `document.querySelector('.session-tree-row.active .session-status-dot') !== null`), true);
  const desktop = await cdp.call('Page.captureScreenshot', { format: 'png' });
  writeFileSync('/tmp/mothx-trajectory-desktop.png', Buffer.from(desktop.data, 'base64'));
  await evaluate(cdp, `document.querySelector('.sidebar-collapse-toggle')?.click()`);
  await waitFor(cdp, `document.querySelector('.sidebar-rail-content') !== null`);
  await waitFor(cdp, `parseFloat(getComputedStyle(document.querySelector('.app-shell')).gridTemplateColumns.split(' ')[0]) <= 56.1`);
  assert.equal(await evaluate(cdp, `Math.round(parseFloat(getComputedStyle(document.querySelector('.app-shell')).gridTemplateColumns.split(' ')[0]))`), 56);
  assert.equal(await evaluate(cdp, `Math.round(parseFloat(getComputedStyle(document.querySelector('.sidebar')).width))`), 56);
  assert.equal(await evaluate(cdp, `document.querySelector('.sidebar-rail-content .label') === null`), true);
  const rail = await cdp.call('Page.captureScreenshot', { format: 'png' });
  writeFileSync('/tmp/mothx-trajectory-rail.png', Buffer.from(rail.data, 'base64'));
  await evaluate(cdp, `document.querySelector('.rail-action[aria-label="搜索会话..."]')?.click()`);
  await waitFor(cdp, `document.querySelector('.side-search-inline') !== null`);
  await waitFor(cdp, `document.querySelector('.side-search-inline input') === document.activeElement`);
  await cdp.call('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: true });
  await waitFor(cdp, `document.querySelector('.wb-toolbar [data-action="menu"]') !== null`);
  await evaluate(cdp, `document.querySelector('.wb-toolbar [data-action="menu"]')?.click()`);
  await waitFor(cdp, `document.querySelector('.mobile-drawer') !== null`);
  assert.equal(await evaluate(cdp, `getComputedStyle(document.querySelector('.mobile-drawer')).width`), '304px');
  await evaluate(cdp, `document.querySelector('.sidebar-overlay')?.click()`);
  await waitFor(cdp, `document.querySelector('.mobile-drawer') === null`);
  const mobile = await cdp.call('Page.captureScreenshot', { format: 'png' });
  writeFileSync('/tmp/mothx-trajectory-mobile.png', Buffer.from(mobile.data, 'base64'));
} finally {
  if (cdp) {
    cdp.socket.close();
    if (cdp.browser.exitCode === null && cdp.browser.signalCode === null) {
      cdp.browser.kill('SIGTERM');
      await new Promise((resolve) => cdp.browser.once('close', resolve));
    }
    try { await import('node:fs/promises').then(({ rm }) => rm(cdp.userDataDir, { recursive: true, force: true })); } catch { /* best effort */ }
  }
  await new Promise((resolve) => server.close(resolve));
}
