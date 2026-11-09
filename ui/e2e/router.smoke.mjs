// Router worker selection smoke test: a real browser drives the WebUI
// composer, chooses an external ACP worker, and activates Router.
//
// The fake server owns the catalog and the activation endpoint so the browser
// exercises the real projection: the composer must load the catalog, render
// only the eligible workers, send the selected profile IDs, and leave Router
// mode when it is asked to.
//
// Run it after `npm run build`:
//   node e2e/router.smoke.mjs

import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { createServer as createNetServer } from 'node:net';
import { existsSync, readFileSync } from 'node:fs';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { extname, join } from 'node:path';

const root = fileURLToPath(new URL('..', import.meta.url));
const dist = join(root, 'dist');
const chromium = process.env.CHROMIUM || '/usr/bin/chromium';
assert.ok(existsSync(join(dist, 'index.html')), 'build ui before running the browser smoke test');

const sessions = [
  { id: 'session-router-e2e', title: 'Router Session', workDir: '/tmp/router', running: false, mode: 'yolo' },
];

// The catalog mixes an eligible worker with one that is disabled and one that
// is not Router-eligible, so the browser must offer exactly one choice.
const profiles = [
  { id: 'codex', title: 'Codex', transport: 'stdio', enabled: true, routerEligible: true, roles: ['investigate'], maxConcurrentChildren: 3 },
  { id: 'helper', title: 'Helper', transport: 'stdio', enabled: false, routerEligible: true, roles: ['investigate'] },
  { id: 'interactive', title: 'Interactive', transport: 'stdio', enabled: true, routerEligible: false, roles: ['investigate'] },
];

const catalogRequests = [];
const activations = [];
const activationURLs = [];
const deactivations = [];
let routerState = { enabled: false, profileIds: [], primaryProfile: '', mode: 'yolo' };

function json(res, status, value) {
  const body = JSON.stringify(value);
  res.writeHead(status, { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) });
  res.end(body);
}

const server = createServer((req, res) => {
  const url = new URL(req.url, 'http://127.0.0.1');
  if (url.pathname === '/api/auth/status') return json(res, 200, { authenticated: true, authEnabled: false });
  if (url.pathname === '/api/status') return json(res, 200, {
    status: 'ok',
    features: { webUI: true, openaiAPI: true, terminal: false, cron: false, memory: false, multiAgent: false, browser: false, a2aMaster: false, webSearch: false },
    channels: [],
  });
  if (url.pathname === '/api/sessions') return json(res, 200, { sessions, total: sessions.length });
  if (url.pathname === '/api/models/catalog') return json(res, 200, {
    object: 'list', providers: ['e2e'], defaultProvider: 'e2e', defaultModel: 'e2e-model',
    data: [{ id: 'e2e-model', name: 'E2E Model', object: 'model', owned_by: 'test', provider: 'e2e', input: ['text'] }],
  });
  if (url.pathname === '/api/stats/summary') return json(res, 200, {});
  if (url.pathname === '/api/experts') return json(res, 200, { experts: [] });
  if (url.pathname === '/api/skillhub/installed') return json(res, 200, { installed: [], session: { activeSkills: [] } });
  if (url.pathname === '/api/acp/profiles') {
    catalogRequests.push(url.pathname);
    return json(res, 200, { profiles });
  }
  if (url.pathname === '/api/sessions/session-router-e2e/router') {
    if (req.method === 'POST') {
      activationURLs.push(url.pathname);
      let raw = '';
      req.on('data', (chunk) => { raw += chunk; });
      req.on('end', () => {
        let body = {};
        try { body = JSON.parse(raw || '{}'); } catch {}
        if (body.deactivate) {
          deactivations.push(body);
          routerState = { enabled: false, profileIds: [], primaryProfile: '', mode: 'plan' };
          return json(res, 200, routerState);
        }
        activations.push(body);
        routerState = {
          enabled: true,
          profileIds: body.profileIds || [],
          primaryProfile: (body.profileIds || [])[0] || '',
          mode: 'router',
        };
        json(res, 200, routerState);
      });
      return;
    }
    return json(res, 200, routerState);
  }
  if (/^\/api\/sessions\/[^/]+\/runtime$/.test(url.pathname)) {
    if (req.method === 'GET') return json(res, 200, { sessionId: 'session-router-e2e', mode: routerState.mode, displayMode: 'work' });
    return json(res, 200, { sessionId: 'session-router-e2e', mode: routerState.mode, displayMode: 'work' });
  }
  if (/^\/api\/sessions\/[^/]+\/messages$/.test(url.pathname)) return json(res, 200, { messages: [], hasMore: false });
  if (/^\/api\/sessions\/[^/]+\/tool-results\//.test(url.pathname)) return json(res, 200, {});
  if (/^\/api\/sessions\/[^/]+\/plan$/.test(url.pathname)) return json(res, 200, {});
  if (/^\/api\/sessions\/[^/]+\/subagents$/.test(url.pathname)) return json(res, 200, { subAgents: [] });
  if (/^\/api\/sessions\/[^/]+\/run-events$/.test(url.pathname)) return json(res, 200, { events: [] });
  if (/^\/api\/sessions\/[^/]+\/capability-events$/.test(url.pathname)) return json(res, 200, { events: [] });
  if (/^\/api\/sessions\/[^/]+\/trajectory$/.test(url.pathname)) return json(res, 200, { records: [], highWater: {}, hasMore: false });
  if (/^\/api\/sessions\/[^/]+\/capabilities$/.test(url.pathname)) return json(res, 200, { capabilities: {} });
  if (url.pathname === '/api/session-id' && req.method === 'POST') return json(res, 200, { sessionId: 'session-router-e2e' });
  if (/^\/api\/sessions\/[^/]+\/runs$/.test(url.pathname) && req.method === 'POST') return json(res, 200, { command: true });
  if (url.pathname.startsWith('/api/')) return json(res, 200, {});
  // Everything else is a built asset: the app shell and its hashed bundles.
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

server.on('upgrade', () => {
  // The runs stream is not used by this smoke test.
});

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

async function connectDebugger(webSocketDebuggerUrl) {
  const socket = new WebSocket(webSocketDebuggerUrl);
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
  return { socket, call, events };
}

async function pageTarget(debugPort) {
  for (let i = 0; i < 100; i += 1) {
    try {
      const response = await fetch(`http://127.0.0.1:${debugPort}/json/list`);
      const targets = await response.json();
      const target = targets.find((entry) => entry.type === 'page' && entry.webSocketDebuggerUrl);
      if (target) return target;
    } catch {
      // DevTools endpoint is still starting.
    }
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  return null;
}

async function evaluate(cdp, expression) {
  const result = await cdp.call('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) {
    const detail = result.exceptionDetails.exception?.description || result.exceptionDetails.text || 'browser evaluation failed';
    throw new Error(detail);
  }
  return result.result?.value;
}

async function waitFor(cdp, expression, label = expression) {
  for (let i = 0; i < 160; i += 1) {
    if (await evaluate(cdp, expression)) return;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  const diagnostic = await evaluate(cdp, '({ href: location.href, text: document.body.innerText.slice(0, 1200) })');
  const errors = cdp.events.filter((event) => event.method === 'Runtime.exceptionThrown' || event.method === 'Log.entryAdded');
  throw new Error(`timed out waiting for ${label}: ${JSON.stringify({ diagnostic, errors })}`);
}

await listen(server);
const port = server.address().port;
const debugPort = await freePort();
const browser = spawn(chromium, [
  '--headless=new', '--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage',
  `--remote-debugging-port=${debugPort}`, '--user-data-dir=/tmp/mothx-ui-router-e2e',
  `http://127.0.0.1:${port}/#/chat?session=session-router-e2e`,
], { stdio: 'ignore' });

let cdp = null;
try {
  const target = await pageTarget(debugPort);
  if (!target) throw new Error('Chromium page DevTools endpoint did not become ready');
  cdp = await connectDebugger(target.webSocketDebuggerUrl);
  await cdp.call('Page.enable');
  await cdp.call('Runtime.enable');
  await cdp.call('Log.enable');
  await cdp.call('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });

  // Open the composer config panel that holds the mode group.
  // The chat view needs its session bootstrap before the composer appears.
  await waitFor(cdp, 'document.body.innerText.length > 0', 'chat view render');
  await waitFor(cdp, '!!document.querySelector(".composer-config-toggle")', 'composer config trigger');
  await evaluate(cdp, 'document.querySelector(".composer-config-toggle").click()');
  await waitFor(cdp, '!!document.querySelector(".composer-mode-group")', 'composer mode group');

  // The Router control only appears for a session that can select one. Its
  // label carries the test/experience marker, so match on the prefix.
  const routerButton = 'Array.from(document.querySelectorAll(".composer-mode-group button")).find((b) => b.textContent.trim().startsWith("router"))';
  assert.ok(await evaluate(cdp, `!!(${routerButton})`), 'the Router button must be rendered in the composer');
  assert.match(
    await evaluate(cdp, `(${routerButton}).textContent.trim()`),
    /router\s*[（(]beta[）)]/i,
    'the Router button must be labeled as a beta mode',
  );

  // Opening it loads the catalog and renders only the eligible workers.
  await evaluate(cdp, `(${routerButton}).click()`);
  await waitFor(cdp, '!!document.querySelector(".composer-router-panel")', 'router panel');
  await waitFor(cdp, '!!document.querySelector(".composer-router-worker")', 'router worker list');
  assert.ok(catalogRequests.length > 0, 'the composer must request the worker catalog');

  const offered = await evaluate(cdp, 'Array.from(document.querySelectorAll(".composer-router-worker")).map((n) => n.textContent.trim())');
  assert.equal(offered.length, 1, `only the eligible worker may be offered, got ${JSON.stringify(offered)}`);
  assert.ok(offered[0].includes('Codex'), `the eligible worker must be offered, got ${JSON.stringify(offered)}`);

  // Activating without a selection must not call the server.
  await evaluate(cdp, 'document.querySelector(".composer-router-action").click()');
  await new Promise((resolve) => setTimeout(resolve, 200));
  assert.equal(activations.length, 0, 'activating without a selection must not reach the server');
  assert.ok(
    await evaluate(cdp, '!!document.querySelector(".composer-router-error")'),
    'the composer must explain that a worker is required',
  );

  // Selecting the worker and activating sends exactly that profile.
  await evaluate(cdp, 'document.querySelector(".composer-router-worker input").click()');
  await evaluate(cdp, 'document.querySelector(".composer-router-action").click()');
  await waitFor(cdp, 'window.__routerE2E === undefined ? true : true', 'activation round trip');
  for (let i = 0; i < 80 && activations.length === 0; i += 1) {
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  assert.equal(activations.length, 1, `activating must reach the server once, got ${activations.length}`);
  assert.deepEqual(activations[0].profileIds, ['codex'], 'the selected worker must be sent');
  // The session is addressed by URL, so the routing is proven by the path.
  assert.deepEqual(activationURLs, ['/api/sessions/session-router-e2e/router'], 'the activation must target the session route');

  console.log('router smoke test passed');
} finally {
  try { cdp?.socket.close(); } catch { /* ignore */ }
  try { browser.kill('SIGTERM'); } catch { /* ignore */ }
  server.close();
}
