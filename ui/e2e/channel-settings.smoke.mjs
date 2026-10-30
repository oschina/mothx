import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { createServer as createNetServer } from 'node:net';
import { existsSync, readFileSync, rmSync } from 'node:fs';
import { extname, join } from 'node:path';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));
const dist = join(root, 'dist');
const chromium = process.env.CHROMIUM || '/usr/bin/chromium';
assert.ok(existsSync(join(dist, 'index.html')), 'build ui before running the browser smoke test');

const state = {
  patchCount: 0,
  toolPutCount: 0,
  lastToolBody: null,
  serveConfigPutCount: 0,
  lastServeConfigBody: null,
  loginCount: 0,
  unbindCount: 0,
  deleteCount: 0,
  sessionExists: true,
  deleteFailure: false,
};
const config = {
  listen: '127.0.0.1:7872',
  mode: 'agent',
  auth: { enabled: false, tokens: [] },
  features: { webUI: true, openaiAPI: true, wechat: false, feishu: false, cron: false, memory: false },
  channels: {
    wechat: { enabled: false, credPath: 'old.json', autoTyping: true, allowedUsers: [] },
    feishu: { enabled: false, appId: '', appSecret: '', allowedUsers: [] },
  },
  webUI: { enabled: true, dir: 'dist' },
};
const toolCatalog = [
  { name: 'read', available: true, default: true },
  { name: 'browser', available: false, default: false, unavailableReason: 'browser runtime is disabled' },
];
const toolState = toolCatalog.map((tool) => ({
  name: tool.name,
  requestedEnabled: tool.default,
  available: tool.available,
  effectiveEnabled: tool.available && tool.default,
  registered: false,
  willRegister: tool.available && tool.default,
}));

function json(res, status, value) {
  const body = JSON.stringify(value);
  res.writeHead(status, { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) });
  res.end(body);
}

function body(req) {
  return new Promise((resolve) => {
    const chunks = [];
    req.on('data', (chunk) => chunks.push(chunk));
    req.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
  });
}

const server = createServer(async (req, res) => {
  const url = new URL(req.url, 'http://127.0.0.1');
  if (url.pathname === '/__e2e/reset-delete-failure') {
    state.sessionExists = true;
    state.deleteFailure = true;
    return json(res, 200, { ok: true });
  }
  if (url.pathname === '/health') return json(res, 200, { status: 'ok' });
  if (url.pathname === '/api/auth/status') return json(res, 200, { authenticated: true, authEnabled: config.auth.enabled });
  if (url.pathname === '/api/auth/login' && req.method === 'POST') {
    state.loginCount += 1;
    return json(res, 200, { authenticated: true, authEnabled: config.auth.enabled });
  }
  if (url.pathname === '/api/status') return json(res, 200, {
    status: 'ok', features: { webUI: true, openaiAPI: true, cron: false, memory: false, multiAgent: false, browser: false, a2aMaster: false },
    channels: [{ name: 'wechat', enabled: false, connected: false }, { name: 'feishu', enabled: false, connected: false }],
  });
  if (url.pathname === '/api/channels') return json(res, 200, [{ name: 'wechat', enabled: false, connected: false }, { name: 'feishu', enabled: false, connected: false }]);
  if (url.pathname === '/api/sessions') {
    const sessions = state.sessionExists
      ? [{ id: 'session-e2e', title: 'E2E Session', channelType: 'wechat', channelId: 'user-e2e', bound: true, running: false }]
      : [];
    return json(res, 200, { sessions, total: sessions.length });
  }
  if (url.pathname === '/api/session-bindings') {
    return json(res, 200, state.sessionExists ? [{ sessionId: 'session-e2e', channelType: 'wechat', channelId: 'user-e2e' }] : []);
  }
  if (url.pathname === '/api/cron') return json(res, 200, { enabled: false, running: false, jobs: [] });
  if (url.pathname === '/api/serve/config') {
    if (req.method === 'PUT') {
      state.serveConfigPutCount += 1;
      state.lastServeConfigBody = JSON.parse(await body(req));
      Object.assign(config, state.lastServeConfigBody);
    }
    return json(res, 200, config);
  }
  if (url.pathname === '/api/settings') return json(res, 200, {});
  if (url.pathname === '/api/memory') return json(res, 200, { enabled: false, content: '' });
  if (url.pathname === '/api/stats/summary') return json(res, 200, {});
  if (url.pathname === '/v1/models') return json(res, 200, { data: [{ id: 'e2e-model', owned_by: 'test' }] });
  if (url.pathname === '/api/models/catalog') return json(res, 200, {
    object: 'list', providers: ['e2e'], defaultProvider: 'e2e', defaultModel: 'e2e-model',
    data: [{ id: 'e2e-model', name: 'E2E Model', object: 'model', owned_by: 'test', provider: 'e2e', input: ['text'] }],
  });
  if (url.pathname === '/api/session-tools/catalog') return json(res, 200, { platform: url.searchParams.get('platform'), tools: toolCatalog });
  if (url.pathname === '/api/sessions/session-e2e/channel-tools') {
    if (req.method === 'PUT') {
      state.toolPutCount += 1;
      state.lastToolBody = JSON.parse(await body(req));
      return json(res, 200, { sessionId: 'session-e2e', platform: 'wechat', generation: state.toolPutCount, appliesTo: 'next_run', tools: toolState });
    }
    return json(res, 200, { sessionId: 'session-e2e', platform: 'wechat', generation: 0, appliesTo: 'next_run', tools: toolState });
  }
  if (url.pathname === '/api/sessions/session-e2e/bindings' && req.method === 'DELETE') {
    state.unbindCount += 1;
    return json(res, 200, { sessionId: 'session-e2e', channelType: 'wechat', channelId: 'user-e2e' });
  }
  if (url.pathname === '/api/sessions/session-e2e' && req.method === 'DELETE') {
    state.deleteCount += 1;
    if (state.deleteFailure) return json(res, 500, { error: 'injected delete failure' });
    state.sessionExists = false;
    return json(res, 200, { deleted: true, sessionId: 'session-e2e' });
  }
  if (url.pathname === '/api/serve/config/channels/wechat' && req.method === 'PATCH') {
    state.patchCount += 1;
    const patch = JSON.parse(await body(req));
    Object.assign(config.channels.wechat, patch);
    return json(res, 200, { platform: 'wechat', configured: config.channels.wechat, effective: config.channels.wechat, restart: { platform: 'wechat', required: false } });
  }
  if (url.pathname.startsWith('/api/')) return json(res, 200, {});
  if (url.pathname === '/' || !extname(url.pathname)) {
    res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
    return res.end(readFileSync(join(dist, 'index.html')));
  }
  try {
    const file = join(dist, url.pathname);
    const contentTypes = {
      '.css': 'text/css; charset=utf-8',
      '.ico': 'image/x-icon',
      '.js': 'text/javascript; charset=utf-8',
      '.json': 'application/json; charset=utf-8',
    };
    res.writeHead(200, { 'Content-Type': contentTypes[extname(file)] || 'application/octet-stream' });
    return res.end(readFileSync(file));
  } catch {
    res.writeHead(404);
    return res.end();
  }
});

server.on('upgrade', (_req, socket) => socket.destroy());

function listen(server) {
  return new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
}

async function freePort() {
  const probe = createNetServer();
  await new Promise((resolve) => probe.listen(0, '127.0.0.1', resolve));
  const port = probe.address().port;
  await new Promise((resolve) => probe.close(resolve));
  return port;
}

async function cdpSession(url) {
  const debugPort = await freePort();
  const browser = spawn(chromium, [
    '--headless=new', '--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage',
    `--remote-debugging-port=${debugPort}`, '--user-data-dir=/tmp/mothx-ui-e2e', url,
  ], { stdio: 'ignore' });
  let target;
  for (let i = 0; i < 100; i += 1) {
    try {
      const response = await fetch(`http://127.0.0.1:${debugPort}/json/list`);
      const targets = await response.json();
      target = targets.find((entry) => entry.type === 'page' && entry.webSocketDebuggerUrl);
      if (target) break;
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
  }
  if (!target) {
    browser.kill('SIGTERM');
    throw new Error('Chromium page DevTools endpoint did not become ready');
  }
  const socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  let id = 0;
  const pending = new Map();
  const events = [];
  socket.onmessage = (event) => {
    const message = JSON.parse(event.data);
    const waiter = pending.get(message.id);
    if (!waiter) {
      events.push(message);
      return;
    }
    pending.delete(message.id);
    if (message.error) waiter.reject(new Error(message.error.message));
    else waiter.resolve(message.result);
  };
  const call = (method, params = {}) => new Promise((resolve, reject) => {
    const requestID = ++id;
    pending.set(requestID, { resolve, reject });
    socket.send(JSON.stringify({ id: requestID, method, params }));
  });
  return { browser, socket, call, events };
}

async function evaluate(cdp, expression) {
  const result = await cdp.call('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.text || 'browser evaluation failed');
  return result.result?.value;
}

async function waitFor(cdp, expression) {
  for (let i = 0; i < 100; i += 1) {
    if (await evaluate(cdp, expression)) return;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  const diagnostic = await evaluate(cdp, '({ href: location.href, title: document.title, text: document.body.innerText.slice(0, 1000) })');
  const errors = cdp.events.filter((event) => event.method === 'Runtime.exceptionThrown' || event.method === 'Log.entryAdded');
  throw new Error(`timed out waiting for ${expression}: ${JSON.stringify({ diagnostic, errors })}`);
}

await listen(server);
const address = server.address();
let cdp;
try {
  cdp = await cdpSession(`http://127.0.0.1:${address.port}/#/settings/channels`);
  await cdp.call('Page.enable');
  await cdp.call('Runtime.enable');
  await cdp.call('Log.enable');
  await waitFor(cdp, `document.querySelector('.channel-settings-grid') !== null`);
  await waitFor(cdp, `document.body.innerText.includes('下一次运行生效')`);
  await waitFor(cdp, `[...document.querySelectorAll('button')].some((button) => button.textContent.includes('保存工具') && !button.disabled)`);
  await evaluate(cdp, `(() => [...document.querySelectorAll('button')].find((button) => button.textContent.includes('保存工具') && !button.disabled).click())()`);
  for (let i = 0; i < 100 && state.toolPutCount === 0; i += 1) await new Promise((resolve) => setTimeout(resolve, 20));
  assert.equal(state.toolPutCount, 1, 'tool save did not reach the API');
  await evaluate(cdp, `(() => [...document.querySelectorAll('.channel-card-actions button')].find((button) => button.textContent.trim() === '保存').click())()`);
  for (let i = 0; i < 100 && state.patchCount === 0; i += 1) await new Promise((resolve) => setTimeout(resolve, 20));
  assert.equal(state.patchCount, 1, 'channel patch did not reach the API');

  await evaluate(cdp, `window.location.hash = '#/settings/serve'`);
  await waitFor(cdp, `document.querySelector('.settings-save-card button') !== null`);
  // Wait for the fetched serve config to be applied to the form. Clicking the
  // switch before that resolves can be overwritten by the late syncFromStore.
  await waitFor(cdp, `[...document.querySelectorAll('.settings-field')].find((field) => field.textContent.includes('Web UI 目录'))?.querySelector('input')?.value === 'dist'`);
  await new Promise((resolve) => setTimeout(resolve, 100));
  await waitFor(cdp, `[...document.querySelectorAll('.settings-switch-row')].some((row) => row.textContent.includes('Bearer Token'))`);
  await evaluate(cdp, `(() => [...document.querySelectorAll('.settings-switch-row')]
    .find((row) => row.textContent.includes('Bearer Token')).querySelector('[role="switch"]').click())()`);
  await waitFor(cdp, `[...document.querySelectorAll('.settings-switch-row')]
    .find((row) => row.textContent.includes('Bearer Token')).querySelector('[role="switch"]').getAttribute('aria-checked') === 'true'`);
  await evaluate(cdp, `document.querySelector('.list-editor button').click()`);
  await waitFor(cdp, `document.querySelector('.list-editor input[type="password"]') !== null`);
  await evaluate(cdp, `(() => {
    const token = document.querySelector('.list-editor input[type="password"]');
    token.value = 'e2e-auth-token';
    token.dispatchEvent(new Event('input', { bubbles: true }));
  })()`);
  await evaluate(cdp, `document.querySelector('.settings-save-card button').click()`);
  for (let i = 0; i < 100 && (state.serveConfigPutCount === 0 || state.lastServeConfigBody === null); i += 1) await new Promise((resolve) => setTimeout(resolve, 20));
  assert.equal(state.serveConfigPutCount, 1, 'serve auth save did not reach the API');
  assert.deepEqual(state.lastServeConfigBody.auth, { enabled: true, tokens: ['e2e-auth-token'] }, 'auth was not written using the serve config schema');
  assert.equal(state.lastServeConfigBody.api?.auth, undefined, 'legacy nested auth should not be saved');
  for (let i = 0; i < 100 && state.loginCount === 0; i += 1) await new Promise((resolve) => setTimeout(resolve, 20));
  assert.equal(state.loginCount, 1, 'saving enabled auth did not log the browser in');

  await evaluate(cdp, `window.location.hash = '#/sessions'`);
  await waitFor(cdp, `document.querySelector('.session-action-delete') !== null`);
  await evaluate(cdp, `window.confirm = () => true`);
  await evaluate(cdp, `document.querySelector('.session-action-delete').click()`);
  for (let i = 0; i < 100 && state.deleteCount === 0; i += 1) await new Promise((resolve) => setTimeout(resolve, 20));
  assert.equal(state.unbindCount, 1, 'bound session was not unbound before deletion');
  assert.equal(state.deleteCount, 1, 'session delete did not reach the API');
  await waitFor(cdp, `document.querySelector('.sessions-table .empty-cell, .session-cards .empty') !== null`);

  // Exercise the partial-success branch: the binding is removed, but the
  // session delete fails and the refreshed list must still show the session.
  await evaluate(cdp, `fetch('/__e2e/reset-delete-failure')`);
  await evaluate(cdp, `window.location.reload()`);
  await waitFor(cdp, `document.querySelector('.session-action-delete') !== null`);
  await evaluate(cdp, `window.confirm = () => true`);
  await evaluate(cdp, `document.querySelector('.session-action-delete').click()`);
  for (let i = 0; i < 100 && state.deleteCount < 2; i += 1) await new Promise((resolve) => setTimeout(resolve, 20));
  assert.equal(state.unbindCount, 2, 'partial-failure branch did not unbind first');
  assert.equal(state.deleteCount, 2, 'partial-failure branch did not attempt delete');
  await waitFor(cdp, `document.body.innerText.includes('已解绑，但删除失败')`);
  await waitFor(cdp, `document.querySelector('.session-action-delete') !== null`);
} finally {
  if (cdp) {
    cdp.socket.close();
    if (cdp.browser.exitCode === null && cdp.browser.signalCode === null) {
      cdp.browser.kill('SIGTERM');
      await new Promise((resolve) => cdp.browser.once('close', resolve));
    }
  }
  for (let attempt = 0; attempt < 10; attempt += 1) {
    try {
      rmSync('/tmp/mothx-ui-e2e', { recursive: true, force: true, maxRetries: 3, retryDelay: 50 });
      break;
    } catch (error) {
      if (attempt === 9) throw error;
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
  }
  await new Promise((resolve) => server.close(resolve));
}
