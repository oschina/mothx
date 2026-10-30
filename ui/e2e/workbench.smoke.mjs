import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { createServer as createNetServer } from 'node:net';
import { existsSync, readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { extname, join } from 'node:path';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));
const dist = join(root, 'dist');
const chromium = process.env.CHROMIUM || '/usr/bin/chromium';
assert.ok(existsSync(join(dist, 'index.html')), 'build ui before running the browser smoke test');

const sessions = [
  { id: 'session-e2e-a', title: 'Session A', workDir: '/tmp/a', running: false, mode: 'yolo' },
  { id: 'session-e2e-b', title: 'Session B', workDir: '/tmp/b', running: false, mode: 'yolo' },
];

// A long transcript must stay inside the pane's scroll area so the composer is
// always visible. Before the workbench body became a flex container, the chat
// view sized itself to content and pushed the composer below the viewport.
const longHistory = Array.from({ length: 30 }, (_, index) => [
  { id: `history-user-${index}`, seq: index * 2 + 1, role: 'user', content: `history question ${index}` },
  { id: `history-assistant-${index}`, seq: index * 2 + 2, role: 'assistant', content: `history answer ${index}\n\n${'long history paragraph '.repeat(12)}\n\n- one\n- two\n- three` },
]).flat();
longHistory.push({
  id: 'plan-1',
  seq: 61,
  role: 'toolCall',
  toolName: 'plan',
  toolCallId: 'plan-call-1',
  arguments: {
    title: 'Polish WebUI workbench',
    note: 'Read-only projection of the latest plan message.',
    steps: [
      { title: 'Align composer with Desktop', status: 'done' },
      { title: 'Add the read-only plan dock', status: 'running' },
      { title: 'Verify mobile projection', status: 'pending' },
    ],
  },
});

function json(res, status, value) {
  const body = JSON.stringify(value);
  res.writeHead(status, { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) });
  res.end(body);
}

const wsSockets = new Set();
let runsSocket = null;
let approvalResolved = false;
let terminalConnections = 0;
let terminalFrames = [];
let terminalInput = '';
let terminalDeletes = 0;
let terminalSeq = 0;
const terminalSessions = [];
const liveTerminals = new Map();

function emitQuestion() {
  if (!runsSocket) return;
  const payload = {
    type: 'session_event',
    sessionId: 'session-e2e-b',
    event: 'question_request',
    data: JSON.stringify({
      sessionId: 'session-e2e-b',
      questionId: 'question-e2e-b',
      question: 'Continue with the e2e question?',
      context: 'e2e question',
      options: ['Yes', 'No']
    })
  };
  runsSocket.write(websocketFrame(JSON.stringify(payload), 0x1));
}

const server = createServer((req, res) => {
  const url = new URL(req.url, 'http://127.0.0.1');
  if (url.pathname === '/api/auth/status') return json(res, 200, { authenticated: true, authEnabled: false });
  if (url.pathname === '/api/status') return json(res, 200, {
    status: 'ok',
    features: { webUI: true, openaiAPI: true, terminal: true, terminalWarning: 'placeholder_token', cron: false, memory: false, multiAgent: false, browser: false, a2aMaster: false, webSearch: false },
    channels: [],
  });
  if (url.pathname === '/api/sessions') return json(res, 200, { sessions, total: sessions.length });
  if (url.pathname === '/api/models/catalog') return json(res, 200, {
    object: 'list', providers: ['e2e'], defaultProvider: 'e2e', defaultModel: 'e2e-model',
    data: [
      { id: 'e2e-model', name: 'E2E Model', object: 'model', owned_by: 'test', provider: 'e2e', input: ['text'] },
      { id: 'e2e-model-2', name: 'E2E Model 2', object: 'model', owned_by: 'test', provider: 'e2e', input: ['text'] }
    ],
  });
  if (url.pathname === '/api/stats/summary') return json(res, 200, {});
  if (url.pathname === '/api/session-id' && req.method === 'POST') return json(res, 200, { sessionId: 'session-e2e-draft' });
  if (url.pathname === '/api/select-directory' && req.method === 'POST') return json(res, 200, { canceled: false, path: '/tmp/draft' });
  if (url.pathname === '/api/e2e/question' && req.method === 'POST') {
    emitQuestion();
    return json(res, 200, { emitted: true });
  }
  if (/^\/api\/sessions\/[^/]+\/runs$/.test(url.pathname) && req.method === 'POST') return json(res, 200, { command: true, message: 'send accepted' });
  if (url.pathname === '/api/terminals' && req.method === 'POST') {
    let raw = '';
    req.on('data', (chunk) => { raw += chunk; });
    req.on('end', () => {
      let body = {};
      try { body = JSON.parse(raw || '{}'); } catch {}
      terminalSeq += 1;
      terminalSessions.push(body.sessionId || '');
      const terminal = { terminalId: `term-e2e-${terminalSeq}`, sessionId: body.sessionId || '', cwd: `/tmp/${body.sessionId || 'unknown'}`, shell: '/bin/sh', cols: 80, rows: 24, state: 'running' };
      liveTerminals.set(terminal.terminalId, terminal);
      json(res, 200, terminal);
    });
    return;
  }
  if (url.pathname === '/api/terminals' && req.method === 'GET') {
    const sessionId = url.searchParams.get('sessionId') || '';
    return json(res, 200, { terminals: [...liveTerminals.values()].filter((terminal) => !sessionId || terminal.sessionId === sessionId) });
  }
  if (url.pathname.startsWith('/api/terminals/') && req.method === 'DELETE') {
    terminalDeletes += 1;
    const terminalId = decodeURIComponent(url.pathname.slice('/api/terminals/'.length));
    liveTerminals.delete(terminalId);
    return json(res, 200, { terminalId, closed: true });
  }
  if (url.pathname === '/api/skillhub/installed') return json(res, 200, { installed: [], session: { activeSkills: [] } });
  if (url.pathname === '/api/experts') return json(res, 200, { experts: [] });
  if (/^\/api\/sessions\/[^/]+\/messages$/.test(url.pathname)) {
    const sessionId = url.pathname.split('/')[3];
    return json(res, 200, { messages: sessionId === 'session-e2e-a' ? longHistory : [], hasMore: false });
  }
  if (/^\/api\/sessions\/[^/]+\/approvals\/[^/]+$/.test(url.pathname) && req.method === 'POST') {
    approvalResolved = true;
    const parts = url.pathname.split('/');
    return json(res, 200, { sessionId: parts[3], approvalId: decodeURIComponent(parts[5]), status: 'approved', timestamp: new Date().toISOString() });
  }
  if (/^\/api\/sessions\/[^/]+\/questions\/[^/]+$/.test(url.pathname) && req.method === 'POST') {
    const parts = url.pathname.split('/');
    return json(res, 200, { sessionId: parts[3], questionId: decodeURIComponent(parts[5]), answer: 'Yes', status: 'answered', timestamp: new Date().toISOString() });
  }
  if (/^\/api\/sessions\/[^/]+\/runtime$/.test(url.pathname)) {
    const sessionId = url.pathname.split('/')[3];
    return json(res, 200, {
      sessionId, mode: 'yolo', displayMode: 'work',
      pendingApprovals: sessionId === 'session-e2e-b' && !approvalResolved
        ? [{ sessionId, approvalId: 'approval-e2e-b', tool: { name: 'bash', args: { command: 'echo hi' } }, summary: 'e2e approval' }]
        : [],
      pendingQuestions: [], capabilities: {}
    });
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

function websocketFrame(payload, opcode = 0x2) {
  const data = Buffer.isBuffer(payload) ? payload : Buffer.from(payload);
  let header;
  if (data.length < 126) {
    header = Buffer.from([0x80 | opcode, data.length]);
  } else if (data.length < 65536) {
    header = Buffer.alloc(4);
    header[0] = 0x80 | opcode;
    header[1] = 126;
    header.writeUInt16BE(data.length, 2);
  } else {
    header = Buffer.alloc(10);
    header[0] = 0x80 | opcode;
    header[1] = 127;
    header.writeBigUInt64BE(BigInt(data.length), 2);
  }
  return Buffer.concat([header, data]);
}

function acceptWebSocket(req, socket) {
  const key = req.headers['sec-websocket-key'];
  const accept = createHash('sha1').update(`${key}258EAFA5-E914-47DA-95CA-C5AB0DC85B11`).digest('base64');
  socket.write([
    'HTTP/1.1 101 Switching Protocols',
    'Upgrade: websocket',
    'Connection: Upgrade',
    `Sec-WebSocket-Accept: ${accept}`,
    '', ''
  ].join('\r\n'));
}

// Minimal client-frame parser. Browsers mask frames; the terminal smoke test
// needs to observe the resize/input frames the pane actually sends.
function readClientFrames(buffer, onFrame) {
  let offset = 0;
  while (buffer.length - offset >= 2) {
    const first = buffer[offset];
    const second = buffer[offset + 1];
    const opcode = first & 0x0f;
    let length = second & 0x7f;
    let header = 2;
    if (length === 126) {
      if (buffer.length - offset < 4) break;
      length = buffer.readUInt16BE(offset + 2);
      header = 4;
    } else if (length === 127) {
      if (buffer.length - offset < 10) break;
      length = Number(buffer.readBigUInt64BE(offset + 2));
      header = 10;
    }
    const masked = (second & 0x80) !== 0;
    if (masked) header += 4;
    if (buffer.length - offset < header + length) break;
    let payload = buffer.subarray(offset + header, offset + header + length);
    if (masked) {
      const mask = buffer.subarray(offset + header - 4, offset + header);
      payload = Buffer.from(payload);
      for (let i = 0; i < payload.length; i += 1) payload[i] ^= mask[i % 4];
    }
    onFrame(opcode, payload);
    offset += header + length;
  }
  return buffer.subarray(offset);
}

async function waitForState(predicate, label) {
  for (let i = 0; i < 140; i += 1) {
    if (predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error(`timed out waiting for ${label}`);
}

server.on('upgrade', (req, socket) => {
  if (req.url?.startsWith('/ws/runs')) {
    runsSocket = socket;
    wsSockets.add(socket);
    socket.on('close', () => {
      if (runsSocket === socket) runsSocket = null;
      wsSockets.delete(socket);
    });
    socket.on('error', () => {});
    acceptWebSocket(req, socket);
    const send = (sessionId, id, content) => {
      const payload = {
        type: 'session_event',
        sessionId,
        event: 'transcript',
        data: JSON.stringify({ type: 'message', sessionId, message: { id, seq: 1, role: 'assistant', content } })
      };
      socket.write(websocketFrame(JSON.stringify(payload), 0x1));
    };
    setTimeout(() => {
      send('session-e2e-a', 'msg-a', 'alpha-only');
      send('session-e2e-b', 'msg-b', 'beta-only');
    }, 250);
    setTimeout(() => {
      if (approvalResolved) return;
      const payload = {
        type: 'session_event',
        sessionId: 'session-e2e-b',
        event: 'approval_request',
        data: JSON.stringify({
          sessionId: 'session-e2e-b',
          approvalId: 'approval-e2e-b',
          tool: { name: 'bash', args: { command: 'echo hi' } },
          summary: 'e2e approval'
        })
      };
      socket.write(websocketFrame(JSON.stringify(payload), 0x1));
    }, 900);
    return;
  }
  if (!req.url?.startsWith('/ws/terminals/')) {
    socket.destroy();
    return;
  }
  terminalConnections += 1;
  const connection = terminalConnections;
  let typed = '';
  let pending = Buffer.alloc(0);
  const sendOutput = (text) => socket.write(websocketFrame(Buffer.concat([Buffer.from([0x01]), Buffer.from(text)]), 0x2));
  wsSockets.add(socket);
  socket.on('close', () => wsSockets.delete(socket));
  acceptWebSocket(req, socket);
  const control = Buffer.from(JSON.stringify({ type: 'attached', cols: 80, rows: 24, throttled: connection === 1 }));
  socket.write(websocketFrame(Buffer.concat([Buffer.from([0x03]), control]), 0x2));
  sendOutput(connection === 1 ? 'hello from pty\r\n' : 'hello after reconnect\r\n');
  socket.on('data', (chunk) => {
    pending = readClientFrames(Buffer.concat([pending, chunk]), (opcode, payload) => {
      if (opcode !== 0x2 || payload.length === 0) return;
      const type = payload[0];
      const frame = { connection, type };
      if (type === 0x02 && payload.length >= 5) {
        frame.cols = payload.readUInt16BE(1);
        frame.rows = payload.readUInt16BE(3);
      } else if (type === 0x01) {
        frame.text = payload.subarray(1).toString('utf8');
        typed += frame.text;
        terminalInput += frame.text;
      }
      terminalFrames.push(frame);
      if (type === 0x01 && typed.includes('echo e2e') && frame.text.includes('\r')) {
        const release = Buffer.from(JSON.stringify({ type: 'backpressure', active: false }));
        socket.write(websocketFrame(Buffer.concat([Buffer.from([0x03]), release]), 0x2));
        sendOutput('echo e2e\r\ne2e-ok\r\n');
        setTimeout(() => { try { socket.end(); } catch {} }, 200);
      }
    });
  });
  socket.on('error', () => {});
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

async function pageTarget(debugPort, predicate = (entry) => entry.type === 'page' && entry.webSocketDebuggerUrl) {
  for (let i = 0; i < 100; i += 1) {
    try {
      const response = await fetch(`http://127.0.0.1:${debugPort}/json/list`);
      const targets = await response.json();
      const target = targets.find(predicate);
      if (target) return target;
    } catch {
      // DevTools endpoint is still starting.
    }
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  return null;
}

async function cdpSession(url) {
  const debugPort = await freePort();
  const browser = spawn(chromium, [
    '--headless=new', '--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage',
    `--remote-debugging-port=${debugPort}`, '--user-data-dir=/tmp/mothx-ui-workbench-e2e', url,
  ], { stdio: 'ignore' });
  const target = await pageTarget(debugPort);
  if (!target) {
    browser.kill('SIGTERM');
    throw new Error('Chromium page DevTools endpoint did not become ready');
  }
  const connection = await connectDebugger(target.webSocketDebuggerUrl);
  return { browser, debugPort, ...connection };
}

// Open a second page in the same browser profile. Both pages share the origin
// localStorage document, which is exactly the multi-tab last-layout contract.
async function openPage(debugPort, url) {
  const version = await (await fetch(`http://127.0.0.1:${debugPort}/json/version`)).json();
  const browser = await connectDebugger(version.webSocketDebuggerUrl);
  const { targetId } = await browser.call('Target.createTarget', { url });
  browser.socket.close();
  const target = await pageTarget(debugPort, (entry) => entry.id === targetId && entry.webSocketDebuggerUrl);
  if (!target) throw new Error('Chromium second page target did not become ready');
  return connectDebugger(target.webSocketDebuggerUrl);
}

async function evaluate(cdp, expression) {
  const result = await cdp.call('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.text || 'browser evaluation failed');
  return result.result?.value;
}

async function waitFor(cdp, expression) {
  for (let i = 0; i < 140; i += 1) {
    if (await evaluate(cdp, expression)) return;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  const diagnostic = await evaluate(cdp, '({ href: location.href, title: document.title, text: document.body.innerText.slice(0, 1200) })');
  const errors = cdp.events.filter((event) => event.method === 'Runtime.exceptionThrown' || event.method === 'Log.entryAdded');
  throw new Error(`timed out waiting for ${expression}: ${JSON.stringify({ diagnostic, errors })}`);
}

// Every chat pane must fill its workbench host and keep the composer inside the
// viewport. A long transcript may only grow the scroll area, never the pane.
async function assertChatPanesFill(cdp, label, { expectScroll = false } = {}) {
  const panes = await evaluate(cdp, `[...document.querySelectorAll('.wb-single-body, .wb-pane-body')].map((host) => {
    const view = [...host.children].find((child) => child.classList?.contains('chat-view')) || host.querySelector('.chat-view');
    const composer = host.querySelector('.composer');
    const scroll = host.querySelector('.chat-scroll');
    const viewRect = view?.getBoundingClientRect();
    const hostRect = host.getBoundingClientRect();
    const composerRect = composer?.getBoundingClientRect();
    return {
      hostHeight: Math.round(hostRect.height),
      viewHeight: viewRect ? Math.round(viewRect.height) : 0,
      composerTop: composerRect ? Math.round(composerRect.top) : null,
      composerBottom: composerRect ? Math.round(composerRect.bottom) : null,
      scrollClient: scroll?.clientHeight ?? 0,
      scrollHeight: scroll?.scrollHeight ?? 0,
      viewportHeight: innerHeight,
    };
  })`);
  assert.ok(panes.length >= 1, `${label}: no chat pane found`);
  for (const [index, pane] of panes.entries()) {
    assert.ok(pane.viewHeight > 0, `${label}: pane ${index} renders a chat view`);
    assert.ok(
      Math.abs(pane.viewHeight - pane.hostHeight) <= 1,
      `${label}: pane ${index} chat view must fill its host (${pane.viewHeight} vs ${pane.hostHeight})`
    );
    assert.ok(
      pane.composerTop !== null && pane.composerTop >= 0 && pane.composerBottom <= pane.viewportHeight + 1,
      `${label}: pane ${index} composer must stay inside the viewport (${pane.composerTop}..${pane.composerBottom} of ${pane.viewportHeight})`
    );
  }
  if (expectScroll) {
    assert.ok(
      panes.some((pane) => pane.scrollHeight > pane.scrollClient),
      `${label}: long history must scroll inside the transcript instead of growing the pane`
    );
  }
}

async function startTabDrag(cdp, tabId) {
  await evaluate(cdp, `(() => { window.__e2eDrag = new DataTransfer(); window.__e2eDrag.setData('text/plain', ${JSON.stringify(tabId)}); })()`);
}

async function dragOverPaneAt(cdp, paneSelector, xRatio, yRatio, index = 0) {
  await evaluate(cdp, `(() => {
    const pane = document.querySelectorAll(${JSON.stringify(paneSelector)})[${index}];
    const rect = pane.getBoundingClientRect();
    const x = rect.left + rect.width * ${xRatio};
    const y = rect.top + rect.height * ${yRatio};
    window.__e2eDrop = { x, y };
    pane.dispatchEvent(new DragEvent('dragover', { bubbles: true, cancelable: true, clientX: x, clientY: y, dataTransfer: window.__e2eDrag }));
  })()`);
}

async function dragOverTabEdge(cdp, paneSelector, edge) {
  await evaluate(cdp, `(() => {
    const pane = document.querySelector(${JSON.stringify(paneSelector)});
    const tabs = [...pane.querySelectorAll('.wb-tab')];
    const target = ${JSON.stringify(edge)} === 'after' ? tabs[tabs.length - 1] : tabs[0];
    const rect = target.getBoundingClientRect();
    const x = ${JSON.stringify(edge)} === 'after' ? rect.right - 2 : rect.left + 2;
    const y = rect.top + rect.height / 2;
    window.__e2eDrop = { x, y };
    pane.dispatchEvent(new DragEvent('dragover', { bubbles: true, cancelable: true, clientX: x, clientY: y, dataTransfer: window.__e2eDrag }));
  })()`);
}

async function dropOnPane(cdp, paneSelector, index = 0) {
  await evaluate(cdp, `(() => {
    const pane = document.querySelectorAll(${JSON.stringify(paneSelector)})[${index}];
    const point = window.__e2eDrop;
    pane.dispatchEvent(new DragEvent('drop', { bubbles: true, cancelable: true, clientX: point.x, clientY: point.y, dataTransfer: window.__e2eDrag }));
  })()`);
}

await listen(server);
const address = server.address();
let cdp;
try {
  cdp = await cdpSession(`http://127.0.0.1:${address.port}/#/chat?session=session-e2e-a`);
  await cdp.call('Page.enable');
  await cdp.call('Runtime.enable');
  await cdp.call('Log.enable');
  await cdp.call('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });

  await waitFor(cdp, `document.querySelector('.wb-toolbar') !== null`);
  await waitFor(cdp, `document.querySelectorAll('.chat-view').length === 1`);
  await waitFor(cdp, `document.querySelector('.transcript')?.textContent.includes('history answer 29') === true`);
  await assertChatPanesFill(cdp, 'initial long history', { expectScroll: true });
  const toolbarSize = await evaluate(cdp, `(() => {
    const bar = document.querySelector('.wb-toolbar');
    const widths = [...bar.querySelectorAll('.wb-tool')].map((button) => button.getBoundingClientRect().width);
    return { height: bar.getBoundingClientRect().height, scrollWidth: bar.scrollWidth, clientWidth: bar.clientWidth, maxButtonWidth: Math.max(...widths) };
  })()`);
  assert.ok(toolbarSize.height <= 31, `workbench toolbar must stay compact (${toolbarSize.height}px)`);
  assert.ok(toolbarSize.maxButtonWidth <= 28, `workbench toolbar buttons must stay icon-sized (${toolbarSize.maxButtonWidth}px)`);
  assert.ok(toolbarSize.scrollWidth <= toolbarSize.clientWidth + 1, 'workbench toolbar must not need horizontal scrolling at desktop width');
  const tabChrome = await evaluate(cdp, `(() => {
    const bar = document.querySelector('.wb-tabbar').getBoundingClientRect();
    const tab = document.querySelector('.wb-tab').getBoundingClientRect();
    return { bar: bar.height, tab: tab.height };
  })()`);
  assert.ok(tabChrome.bar <= 32, `tab strip must stay vertically compact (${tabChrome.bar}px)`);
  assert.ok(tabChrome.tab <= 27, `tabs must not reintroduce vertical padding (${tabChrome.tab}px)`);
  const composerSession = await evaluate(cdp, `(() => {
    const info = document.querySelector('.chat-session-header .composer-session-info');
    const header = info?.closest('.chat-session-header');
    return {
      insideHeader: Boolean(header),
      afterHeadline: info?.previousElementSibling?.classList?.contains('chat-session-headline') === true,
      inComposer: Boolean(document.querySelector('.composer .composer-session-info')),
      hasNewSessionButton: Boolean(info?.querySelector('.session-new')),
      headerHeight: header?.getBoundingClientRect().height ?? 0,
      configCollapsed: document.querySelector('.composer-config-panel') === null,
      hasConfigToggle: document.querySelector('.composer-config-toggle') !== null,
      modeBadge: document.querySelector('.composer-config-mode')?.textContent?.trim() || '',
      hasStandaloneRuntimeToggle: document.querySelector('.runtime-toggle') !== null,
    };
  })()`);
  assert.ok(composerSession.insideHeader, 'session workdir must live in the chat session header');
  assert.ok(composerSession.afterHeadline, 'session workdir must sit directly after the session headline');
  assert.equal(composerSession.inComposer, false, 'session workdir must not render inside the composer');
  assert.equal(composerSession.hasNewSessionButton, false, 'session workdir must not add a duplicate new-session button');
  assert.ok(composerSession.headerHeight <= 60, `session workdir must not grow the header (${composerSession.headerHeight}px)`);
  assert.ok(composerSession.hasConfigToggle, 'composer must expose a compact model/expert config toggle');
  assert.equal(composerSession.configCollapsed, true, 'provider/model/expert controls must start collapsed');
  assert.equal(composerSession.modeBadge, 'yolo', 'config toggle must show the current agent mode');
  assert.equal(composerSession.hasStandaloneRuntimeToggle, false, 'agent mode must be merged into the config toggle, not a separate control');
  const headerCenters = await evaluate(cdp, `(() => {
    const header = document.querySelector('.chat-session-header');
    const hr = header.getBoundingClientRect();
    const center = hr.top + hr.height / 2;
    return [...header.children]
      .map((child) => ({ child, rect: child.getBoundingClientRect() }))
      .filter((entry) => entry.rect.height > 0)
      .map((entry) => ({ name: entry.child.className || entry.child.tagName, delta: Math.round((entry.rect.top + entry.rect.height / 2 - center) * 10) / 10 }));
  })()`);
  assert.ok(headerCenters.length >= 3, `session header must expose its aligned elements (${headerCenters.length})`);
  for (const entry of headerCenters) {
    assert.ok(Math.abs(entry.delta) <= 1, `${entry.name} must be vertically centered in the session header (offset ${entry.delta}px)`);
  }
  await evaluate(cdp, `document.querySelector('.composer-config-toggle').click()`);
  await waitFor(cdp, `document.querySelector('.composer-config-panel') !== null`);
  await waitFor(cdp, `document.querySelector('.composer-config-panel .model-search-select') !== null`);
  await waitFor(cdp, `document.querySelectorAll('.composer-mode-group button').length === 4 && document.querySelector('.composer-mode-group button.active')?.textContent === 'yolo'`);
  assert.equal(await evaluate(cdp, `document.querySelectorAll('.composer-display-group button').length`), 2, 'display mode must live in the merged config panel');
  await evaluate(cdp, `document.querySelector('.composer-config-toggle').click()`);
  await waitFor(cdp, `document.querySelector('.composer-config-panel') === null`);
  assert.equal(await evaluate(cdp, `document.querySelector('.wb-tabstrip')?.textContent.includes('Session A') === true`), true);

  // Read-only Plan dock: the latest plan message is projected without new
  // storage, and the dock sits beside the pane area on desktop.
  await waitFor(cdp, `document.querySelector('.plan-card h3')?.textContent === 'Polish WebUI workbench'`);
  await evaluate(cdp, `document.querySelector('[data-action="plan"]').click()`);
  await waitFor(cdp, `document.querySelector('.wb-plan-panel') !== null`);
  await waitFor(cdp, `document.querySelector('.wb-plan-panel .todo-plan h3')?.textContent === 'Polish WebUI workbench'`);
  assert.equal(await evaluate(cdp, `document.querySelectorAll('.wb-plan-panel .todo-plan li').length`), 3, 'plan dock must render every step');
  assert.equal(await evaluate(cdp, `document.querySelectorAll('.wb-plan-panel .todo-plan li.done').length`), 1, 'plan dock must project completed steps');
  assert.equal(await evaluate(cdp, `document.querySelectorAll('.wb-plan-panel .todo-plan li.running').length`), 1, 'plan dock must project the running step');
  const planLayout = await evaluate(cdp, `(() => { const panel = document.querySelector('.wb-plan-panel').getBoundingClientRect(); const panes = document.querySelector('.wb-panes').getBoundingClientRect(); return { width: panel.width, left: panel.left, panesRight: panes.right }; })()`);
  assert.ok(planLayout.width >= 280, `plan dock must keep a readable width (${planLayout.width}px)`);
  assert.ok(planLayout.left >= planLayout.panesRight - 1, 'plan dock must sit beside the pane area, not overlay it on desktop');
  await evaluate(cdp, `document.querySelector('.wb-plan-panel .wb-plan-close').click()`);
  await waitFor(cdp, `document.querySelector('.wb-plan-panel') === null`);

  // Sending from an existing session exercises the canonical composer path.
  // This guards the ChatPane refactor against local session-id shadowing and
  // ensures the optimistic user turn plus the server result render once.
  await evaluate(cdp, `(() => { const textarea = document.querySelector('textarea'); textarea.value = 'ping e2e'; textarea.dispatchEvent(new Event('input', { bubbles: true })); })()`);
  await waitFor(cdp, `document.querySelector('.send-btn')?.disabled === false`);
  await evaluate(cdp, `document.querySelector('.send-btn').click()`);
  await waitFor(cdp, `document.body.textContent.includes('ping e2e') && document.body.textContent.includes('send accepted')`);
  assert.equal(await evaluate(cdp, `document.querySelectorAll('.wb-tab').length`), 1);

  // Terminal feature: open the PTY dock, verify the initial resize frame,
  // type into the shell, receive echoed output, survive a server-side socket
  // drop by reconnecting, resize the host, then close and delete the terminal.
  await evaluate(cdp, `document.querySelector('[data-action="terminal"]').click()`);
  await waitFor(cdp, `document.querySelector('.terminal-pane') !== null`);
  assert.ok(
    await evaluate(cdp, `(document.querySelector('.terminal-sandbox')?.textContent || '').trim().length > 0`),
    'terminal shows the sandbox warning'
  );
  assert.ok(
    await evaluate(cdp, `(document.querySelector('.terminal-warning')?.textContent || '').includes('占位')`),
    'terminal shows the placeholder-token warning'
  );
  assert.ok(
    await evaluate(cdp, `(document.querySelector('.terminal-agent-warning')?.textContent || '').length > 0`),
    'terminal shows the shared-workdir Agent warning'
  );
  await waitFor(cdp, `document.querySelector('.xterm-rows')?.textContent.includes('hello from pty') === true`);
  await waitFor(cdp, `(document.querySelector('.terminal-throttled')?.textContent || '').length > 0`);
  assert.ok(
    await evaluate(cdp, `(document.querySelector('.terminal-throttled')?.textContent || '').length > 0`),
    'terminal shows the backpressure notice'
  );
  await waitForState(() => terminalFrames.some((frame) => frame.connection === 1 && frame.type === 2), 'initial terminal resize frame');
  const initialResize = terminalFrames.find((frame) => frame.connection === 1 && frame.type === 2);
  assert.ok(initialResize.cols >= 20 && initialResize.rows >= 5, `initial terminal size ${initialResize.cols}x${initialResize.rows}`);

  await evaluate(cdp, `document.querySelector('.xterm-helper-textarea')?.focus()`);
  for (const char of 'echo e2e') {
    await cdp.call('Input.dispatchKeyEvent', { type: 'keyDown', text: char, unmodifiedText: char, key: char });
  }
  await cdp.call('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13 });
  await cdp.call('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13 });
  await waitFor(cdp, `document.querySelector('.xterm-rows')?.textContent.includes('e2e-ok') === true`);
  await waitForState(() => terminalInput.includes('echo e2e'), 'terminal input frame');
  await waitFor(cdp, `document.querySelector('.terminal-throttled') === null`);

  await waitForState(() => terminalConnections >= 2, 'terminal reconnect after socket drop');
  await waitFor(cdp, `document.querySelector('.xterm-rows')?.textContent.includes('hello after reconnect') === true`);
  await waitForState(() => terminalFrames.some((frame) => frame.connection >= 2 && frame.type === 2), 'resize frame after reconnect');

  await evaluate(cdp, `(() => { const host = document.querySelector('.terminal-host'); host.style.flex = 'none'; host.style.width = '420px'; host.style.height = '220px'; })()`);
  await waitForState(() => terminalFrames.some((frame) => frame.connection >= 2 && frame.type === 2 && frame.cols < initialResize.cols), 'resize frame after host shrink');

  // A page reload loses the in-memory id but sessionStorage keeps it. Reopening
  // the dock must reattach the detached shell instead of leaking it and POSTing
  // a brand-new terminal.
  const terminalPostsBeforeReload = terminalSeq;
  const connectionsBeforeReload = terminalConnections;
  await cdp.call('Page.reload');
  await new Promise((resolve) => setTimeout(resolve, 1000));
  await waitFor(cdp, `document.querySelector('.wb-toolbar') !== null`);
  await waitFor(cdp, `document.querySelector('[data-action="terminal"]') !== null`);
  await evaluate(cdp, `document.querySelector('[data-action="terminal"]').click()`);
  await waitFor(cdp, `document.querySelector('.terminal-pane') !== null`);
  await waitForState(() => terminalSeq === terminalPostsBeforeReload, 'page reload reattaches instead of creating a terminal');
  await waitForState(() => terminalConnections > connectionsBeforeReload, 'page reload reattaches the terminal websocket');

  // Switching focus while the dock is open must release the old session's
  // shell and create a terminal bound to the newly focused session.
  const deletesBeforeSwitch = terminalDeletes;
  await evaluate(cdp, `window.location.hash = '#/chat?session=session-e2e-b'`);
  await waitFor(cdp, `document.querySelectorAll('.wb-tab').length === 2`);
  await waitForState(() => terminalSessions.includes('session-e2e-b'), 'terminal rebinds to the focused session');
  await waitForState(() => terminalDeletes > deletesBeforeSwitch, 'old terminal deleted on focus switch');

  await evaluate(cdp, `document.querySelector('[data-action="terminal"]').click()`);
  await waitFor(cdp, `document.querySelector('.terminal-pane') === null`);
  await waitForState(() => terminalDeletes >= 2, 'terminal delete on close');

  // Opening a second session from the shared focus projection creates a tab in
  // the focused pane; the first session stays rendered.
  await evaluate(cdp, `window.location.hash = '#/chat?session=session-e2e-b'`);
  await waitFor(cdp, `document.querySelectorAll('.wb-tab').length === 2`);
  await evaluate(cdp, `document.querySelector('[data-action="plan"]').click()`);
  await waitFor(cdp, `document.querySelector('.wb-plan-panel .wb-plan-empty') !== null`);
  assert.equal(await evaluate(cdp, `document.querySelector('.wb-plan-panel .todo-plan') === null`), true, 'a session without a plan must show the empty state');
  await evaluate(cdp, `document.querySelector('.wb-plan-panel .wb-plan-close').click()`);
  await waitFor(cdp, `document.querySelector('.wb-plan-panel') === null`);

  // Composer drafts survive tab switches. ChatPane is unmounted when another
  // tab becomes selected, so this exercises the shared draft store instead of
  // local component state.
  await evaluate(cdp, `(() => [...document.querySelectorAll('.wb-tab-label')].find((tab) => tab.textContent.includes('Session A')).click())()`);
  await waitFor(cdp, `document.querySelector('.wb-tab-text')?.textContent.includes('Session A') === true`);
  await evaluate(cdp, `(() => { const textarea = document.querySelector('.wb-pane-body textarea, .wb-single-body textarea'); textarea.value = 'unsent draft'; textarea.dispatchEvent(new Event('input', { bubbles: true })); })()`);
  await waitFor(cdp, `document.querySelector('.wb-pane-body textarea, .wb-single-body textarea')?.value === 'unsent draft'`);
  await evaluate(cdp, `(() => [...document.querySelectorAll('.wb-tab-label')].find((tab) => tab.textContent.includes('Session B')).click())()`);
  await waitFor(cdp, `document.querySelector('.wb-pane-body textarea, .wb-single-body textarea')?.value === ''`);
  await evaluate(cdp, `(() => [...document.querySelectorAll('.wb-tab-label')].find((tab) => tab.textContent.includes('Session A')).click())()`);
  await waitFor(cdp, `document.querySelector('.wb-pane-body textarea, .wb-single-body textarea')?.value === 'unsent draft'`);
  await assertChatPanesFill(cdp, 'after switching back to the long tab', { expectScroll: true });
  await evaluate(cdp, `(() => { const textarea = document.querySelector('.wb-pane-body textarea, .wb-single-body textarea'); textarea.value = ''; textarea.dispatchEvent(new Event('input', { bubbles: true })); })()`);
  await waitFor(cdp, `document.querySelector('.wb-pane-body textarea, .wb-single-body textarea')?.value === ''`);

  // Layout is restored after a reload from the shared localStorage document.
  await waitFor(cdp, `(() => { try { return JSON.parse(localStorage.getItem('mothx.workbench.v1')).view.groups.g1.tabs.length === 2; } catch { return false; } })()`);
  await cdp.call('Page.reload');
  await new Promise((resolve) => setTimeout(resolve, 1000));
  await waitFor(cdp, `document.querySelectorAll('.wb-tab').length === 2`);

  await evaluate(cdp, `document.querySelector('[data-action="split-horizontal"]').click()`);
  await waitFor(cdp, `document.querySelectorAll('.wb-pane').length === 2`);
  await waitFor(cdp, `document.querySelectorAll('.chat-view').length === 2`);
  await assertChatPanesFill(cdp, 'split panes', { expectScroll: true });
  assert.equal(await evaluate(cdp, `document.querySelectorAll('.wb-split-handle').length >= 1`), true);
  assert.equal(await evaluate(cdp, `(() => { const ids = [...document.querySelectorAll('[id]')].map((el) => el.id); return ids.length === new Set(ids).size; })()`), true, 'duplicate DOM ids across panes');

  // Dragging over a pane shows an edge preview; cancelling the drag (Escape
  // fires dragend) must clear that preview instead of leaving it stuck on.
  await evaluate(cdp, `(() => {
    const pane = document.querySelector('.wb-pane');
    const rect = pane.getBoundingClientRect();
    const dataTransfer = new DataTransfer();
    dataTransfer.setData('text/plain', 'session:session-e2e-a');
    pane.dispatchEvent(new DragEvent('dragover', { bubbles: true, cancelable: true, clientX: rect.left + 1, clientY: rect.top + rect.height / 2, dataTransfer }));
  })()`);
  await waitFor(cdp, `document.querySelector('.wb-drop-left') !== null`);
  await evaluate(cdp, `window.dispatchEvent(new DragEvent('dragend', { bubbles: true }))`);
  await waitFor(cdp, `document.querySelector('.wb-drop-left') === null && document.querySelector('.wb-pane-dragover') === null`);

  // Per-pane model selection is independent and survives reload. Provider/model
  // controls are collapsed behind each pane's compact config toggle.
  await evaluate(cdp, `document.querySelector('.wb-pane[data-selected-tab="session:session-e2e-a"] .composer-config-toggle').click()`);
  await evaluate(cdp, `document.querySelector('.wb-pane[data-selected-tab="session:session-e2e-b"] .composer-config-toggle').click()`);
  await waitFor(cdp, `document.querySelectorAll('.composer-config-panel .model-search-select input').length === 2`);
  const modelABefore = await evaluate(cdp, `document.querySelector('.wb-pane[data-selected-tab="session:session-e2e-a"] .model-search-select input')?.value`);
  await evaluate(cdp, `document.querySelector('.wb-pane[data-selected-tab="session:session-e2e-b"] .model-search-select input').focus()`);
  await waitFor(cdp, `document.querySelectorAll('.wb-pane[data-selected-tab="session:session-e2e-b"] .model-search-select-option').length >= 2`);
  await evaluate(cdp, `(() => { const option = [...document.querySelectorAll('.wb-pane[data-selected-tab="session:session-e2e-b"] .model-search-select-option')].find((button) => button.textContent.includes('E2E Model 2')); option.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true })); })()`);
  await waitFor(cdp, `document.querySelector('.wb-pane[data-selected-tab="session:session-e2e-b"] .model-search-select input')?.value === 'E2E Model 2'`);
  assert.equal(await evaluate(cdp, `document.querySelector('.wb-pane[data-selected-tab="session:session-e2e-a"] .model-search-select input')?.value`), modelABefore);
  await evaluate(cdp, `document.querySelector('.wb-pane[data-selected-tab="session:session-e2e-a"] .composer-config-toggle').click()`);
  await evaluate(cdp, `document.querySelector('.wb-pane[data-selected-tab="session:session-e2e-b"] .composer-config-toggle').click()`);
  await waitFor(cdp, `document.querySelectorAll('.composer-config-panel').length === 0`);

  // Both panes stream independently: each transcript contains only its own
  // session's event, exactly once (the sole dispatcher must not double-write).
  await waitFor(cdp, `document.body.textContent.includes('alpha-only') && document.body.textContent.includes('beta-only')`);
  const streamed = await evaluate(cdp, `[...document.querySelectorAll('.wb-pane-body')].map((body) => body.textContent)`);
  assert.equal(streamed.some((text) => text.includes('alpha-only') && !text.includes('beta-only')), true);
  assert.equal(streamed.some((text) => text.includes('beta-only') && !text.includes('alpha-only')), true);
  assert.equal((await evaluate(cdp, `document.body.textContent`)).match(/alpha-only/g).length, 1);
  assert.equal((await evaluate(cdp, `document.body.textContent`)).match(/beta-only/g).length, 1);

  // A pending decision for a non-focused session surfaces as a tab badge and
  // in the global attention count without stealing the focused pane.
  await waitFor(cdp, `document.querySelector('.wb-attention') !== null`);
  assert.equal(await evaluate(cdp, `document.querySelectorAll('.wb-tab-pending').length >= 1`), true);
  assert.equal(await evaluate(cdp, `(document.querySelector('.wb-attention')?.textContent || '').includes('1')`), true);
  // The attention entry moves focus to the pane/tab that owns the decision.
  await evaluate(cdp, `document.querySelector('.wb-attention').click()`);
  await waitFor(cdp, `document.querySelector('.wb-pane-active')?.dataset.selectedTab === 'session:session-e2e-b'`);
  // Resolving the decision from the owning pane clears both the tab badge and
  // the global attention entry without touching the other pane.
  await waitFor(cdp, `document.querySelector('.approval-center .approval-actions .primary') !== null`);
  await evaluate(cdp, `document.querySelector('.approval-center .approval-actions .primary').click()`);
  await waitFor(cdp, `document.querySelector('.wb-attention') === null && document.querySelectorAll('.wb-tab-pending').length === 0`);

  // A background question must not steal the focused pane: it only raises the
  // tab/attention badges until the user focuses its owner.
  await evaluate(cdp, `(() => [...document.querySelectorAll('.wb-tab-label')].find((tab) => tab.textContent.includes('Session A')).click())()`);
  await waitFor(cdp, `document.querySelector('.wb-pane-active')?.dataset.selectedTab === 'session:session-e2e-a'`);
  await evaluate(cdp, `fetch('/api/e2e/question', { method: 'POST' }).then((response) => response.json())`);
  await waitFor(cdp, `document.querySelectorAll('.wb-tab-pending').length >= 1 && (document.querySelector('.wb-attention')?.textContent || '').includes('1')`);
  assert.equal(await evaluate(cdp, `document.querySelector('.question-overlay') === null`), true, 'background question must not overlay the focused pane');
  assert.equal(await evaluate(cdp, `document.querySelector('.wb-pane-active')?.dataset.selectedTab === 'session:session-e2e-a'`), true, 'background question must not steal focus');

  // Focusing the owner through the attention entry reveals the question in its
  // owning pane; answering it clears every projection.
  await evaluate(cdp, `document.querySelector('.wb-attention').click()`);
  await waitFor(cdp, `document.querySelector('.wb-pane-active')?.dataset.selectedTab === 'session:session-e2e-b' && document.querySelector('.question-overlay') !== null`);
  assert.equal(await evaluate(cdp, `document.querySelectorAll('.wb-pane-body').length === 2`), true);
  assert.equal(await evaluate(cdp, `document.querySelector('.question-overlay')?.textContent.includes('Continue with the e2e question?') === true`), true);
  await evaluate(cdp, `(() => { const overlay = document.querySelector('.question-overlay'); [...overlay.querySelectorAll('.approval-actions button')].find((button) => button.textContent.trim() === 'Yes').click(); })()`);
  await waitFor(cdp, `document.querySelector('.question-overlay') === null && document.querySelector('.wb-attention') === null && document.querySelectorAll('.wb-tab-pending').length === 0`);

  // Keyboard resize persists the split ratio, and per-tab chat/trajectory mode
  // survives a reload independently of the other pane.
  await evaluate(cdp, `document.querySelector('.wb-split-handle').focus()`);
  await evaluate(cdp, `document.querySelector('.wb-split-handle').dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }))`);
  await waitFor(cdp, `(() => { const doc = JSON.parse(localStorage.getItem('mothx.workbench.v1')); return doc?.frame?.center?.kind === 'split' && Math.abs(doc.frame.center.weights[0] - 0.55) < 0.001; })()`);
  await evaluate(cdp, `(() => { const pane = document.querySelector('.wb-pane-active'); [...pane.querySelectorAll('.chat-view-tab')].find((button) => button.textContent.includes('轨迹') || button.textContent.includes('Trajectory')).click(); })()`);
  await waitFor(cdp, `document.querySelector('.wb-pane-active .trajectory-view') !== null`);
  await waitFor(cdp, `(() => { const doc = JSON.parse(localStorage.getItem('mothx.workbench.v1')); return Object.values(doc?.view?.groups || {}).some((group) => group.tabs.some((tab) => tab.sessionId === 'session-e2e-b' && tab.mode === 'trajectory')); })()`);
  await cdp.call('Page.reload');
  await new Promise((resolve) => setTimeout(resolve, 1000));
  await waitFor(cdp, `document.querySelectorAll('.wb-tab').length === 2 && document.querySelector('.wb-pane-active .trajectory-view') !== null`);
  assert.equal(await evaluate(cdp, `(() => { const doc = JSON.parse(localStorage.getItem('mothx.workbench.v1')); return Math.abs(doc.frame.center.weights[0] - 0.55) < 0.001; })()`), true, 'split ratio must survive reload');
  // Double-clicking the handle restores the 50/50 default and persists it.
  await evaluate(cdp, `document.querySelector('.wb-split-handle').dispatchEvent(new MouseEvent('dblclick', { bubbles: true }))`);
  await waitFor(cdp, `(() => { const doc = JSON.parse(localStorage.getItem('mothx.workbench.v1')); return Math.abs(doc.frame.center.weights[0] - 0.5) < 0.001; })()`);
  await waitFor(cdp, `document.querySelector('.wb-pane[data-selected-tab="session:session-e2e-b"] .composer-config-model')?.textContent === 'E2E Model 2'`);
  await waitFor(cdp, `document.querySelector('.wb-pane[data-selected-tab="session:session-e2e-a"] .composer-config-model')?.textContent === ${JSON.stringify(modelABefore)}`);
  await evaluate(cdp, `(() => { const pane = document.querySelector('.wb-pane-active'); [...pane.querySelectorAll('.chat-view-tab')].find((button) => button.textContent.includes('对话') || button.textContent.includes('Chat')).click(); })()`);
  await waitFor(cdp, `document.querySelector('.wb-pane-active .trajectory-view') === null`);

  // Closing the focused pane folds its tabs back into the remaining pane.
  await evaluate(cdp, `document.querySelector('[data-action="close-pane"]').click()`);
  await waitFor(cdp, `document.querySelectorAll('.wb-pane').length <= 1 && document.querySelectorAll('.chat-view').length === 1`);

  // Desktop width matrix: two-column split stays usable and never introduces
  // horizontal page overflow.
  await cdp.call('Emulation.setDeviceMetricsOverride', { width: 1024, height: 900, deviceScaleFactor: 1, mobile: false });
  await evaluate(cdp, `document.querySelector('[data-action="preset-two-column"]').click()`);
  await waitFor(cdp, `document.querySelector('.wb-single') !== null`);
  assert.equal(await evaluate(cdp, `document.documentElement.scrollWidth <= window.innerWidth + 1`), true, 'horizontal overflow at 1024px');
  for (const width of [1280, 1440, 1920]) {
    await cdp.call('Emulation.setDeviceMetricsOverride', { width, height: 900, deviceScaleFactor: 1, mobile: false });
    await evaluate(cdp, `document.querySelector('[data-action="preset-two-column"]').click()`);
    await waitFor(cdp, `document.querySelectorAll('.wb-pane').length === 2`);
    assert.equal(await evaluate(cdp, `document.documentElement.scrollWidth <= window.innerWidth + 1`), true, `horizontal overflow at ${width}px`);
  }

  // Presets reshape the same tabs into four leaves at a wide viewport.
  await cdp.call('Emulation.setDeviceMetricsOverride', { width: 1920, height: 1080, deviceScaleFactor: 1, mobile: false });
  await evaluate(cdp, `document.querySelector('[data-action="preset-quad"]').click()`);
  await waitFor(cdp, `document.querySelectorAll('.wb-pane').length === 4`);
  // A fifth pane is rejected with an explicit reason instead of a silent no-op.
  await evaluate(cdp, `(() => {
    const pane = document.querySelector('.wb-pane');
    const rect = pane.getBoundingClientRect();
    const dataTransfer = new DataTransfer();
    dataTransfer.setData('text/plain', 'session:session-e2e-a');
    pane.dispatchEvent(new DragEvent('dragover', { bubbles: true, cancelable: true, clientX: rect.left + 1, clientY: rect.top + rect.height / 2, dataTransfer }));
    pane.dispatchEvent(new DragEvent('drop', { bubbles: true, cancelable: true, clientX: rect.left + 1, clientY: rect.top + rect.height / 2, dataTransfer }));
  })()`);
  await waitFor(cdp, `document.querySelector('.banner-info')?.textContent.includes('4') === true`);
  assert.equal(await evaluate(cdp, `document.querySelectorAll('.wb-pane').length`), 4);
  await evaluate(cdp, `document.querySelector('.banner-info .link-btn').click()`);
  await waitFor(cdp, `document.querySelector('.banner-info') === null`);
  await evaluate(cdp, `document.querySelector('[data-action="preset-single"]').click()`);
  await waitFor(cdp, `document.querySelectorAll('.wb-pane').length === 0 && document.querySelector('.wb-single') !== null`);

  // Mobile projection: one pane plus a merged tab strip, no split handles.
  await cdp.call('Emulation.setDeviceMetricsOverride', { width: 390, height: 820, deviceScaleFactor: 2, mobile: true });
  await waitFor(cdp, `document.querySelector('.wb-single') !== null`);
  const mobileToolbar = await evaluate(cdp, `(() => {
    const bar = document.querySelector('.wb-toolbar');
    return { height: bar.getBoundingClientRect().height, scrollWidth: bar.scrollWidth, clientWidth: bar.clientWidth };
  })()`);
  // Mobile keeps a touch-sized bar (but still compact) because layout presets
  // and split controls are hidden there; desktop stays icon-dense.
  assert.ok(mobileToolbar.height <= 44, `mobile workbench toolbar must stay compact (${mobileToolbar.height}px)`);
  assert.ok(mobileToolbar.scrollWidth <= mobileToolbar.clientWidth + 1, 'mobile workbench toolbar must fit without horizontal scrolling');
  assert.equal(await evaluate(cdp, `document.querySelectorAll('.wb-pane').length`), 0);
  assert.equal(await evaluate(cdp, `document.querySelectorAll('.wb-split-handle').length`), 0);
  await waitFor(cdp, `document.querySelectorAll('.wb-tab').length >= 1`);
  // Overflowing tabs stay reachable through the explicit overflow menu.
  // Tabs now show conversation titles instead of 16-char IDs, so more tabs are
  // needed before the strip overflows at phone width.
  for (const expected of [3, 4, 5, 6, 7, 8]) {
    await evaluate(cdp, `document.querySelector('[data-action="new-task"]').click()`);
    await waitFor(cdp, `document.querySelectorAll('.wb-tab').length === ${expected}`);
  }
  await waitFor(cdp, `document.querySelector('.wb-tab-overflow') !== null`);
  await evaluate(cdp, `document.querySelector('.wb-tab-overflow').click()`);
  await waitFor(cdp, `document.querySelectorAll('.wb-tab-overflow-menu button').length >= 5`);
  await evaluate(cdp, `document.querySelector('.wb-tab-overflow').click()`);
  await waitFor(cdp, `document.querySelector('.wb-tab-overflow-menu') === null`);
  await cdp.call('Emulation.setDeviceMetricsOverride', { width: 768, height: 900, deviceScaleFactor: 2, mobile: true });
  await waitFor(cdp, `document.querySelector('.wb-single') !== null`);
  assert.equal(await evaluate(cdp, `document.querySelectorAll('.wb-pane').length`), 0);
  assert.equal(await evaluate(cdp, `document.documentElement.scrollWidth <= window.innerWidth + 1`), true, 'horizontal overflow at 768px');

  // The legacy ?workbench=0 / single-session fallback is gone; the query is
  // ignored and the workbench remains the only /chat projection.
  await cdp.call('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
  await evaluate(cdp, `window.location.hash = '#/chat?session=session-e2e-a&workbench=0'`);
  await waitFor(cdp, `document.querySelector('.wb-toolbar') !== null && document.querySelector('.chat-view') !== null`);
  assert.equal(await evaluate(cdp, `document.querySelector('.chat-single-host') === null`), true, 'legacy single-session host must be removed');

  // Draft -> real session replacement, appended last so it cannot perturb the
  // layout assertions above. The first send must allocate a session, keep the
  // target tab focused despite a stale /api/sessions refresh, and render the
  // server result.
  await evaluate(cdp, `window.location.hash = '#/chat'`);
  await waitFor(cdp, `document.querySelector('.wb-toolbar') !== null`);
  await evaluate(cdp, `document.querySelector('[data-action="reset"]').click()`);
  await waitFor(cdp, `document.querySelectorAll('.wb-tab').length === 0`);
  await evaluate(cdp, `(() => [...document.querySelectorAll('.session-tree-open')].find((button) => button.textContent.includes('Session A')).click())()`);
  await waitFor(cdp, `document.querySelectorAll('.wb-tab').length === 1`);
  await evaluate(cdp, `document.querySelector('[data-action="new-task"]').click()`);
  await waitFor(cdp, `document.querySelectorAll('.wb-tab').length === 2 && document.querySelector('.workdir-pill') !== null`);
  await assertChatPanesFill(cdp, 'new draft tab');
  assert.equal(await evaluate(cdp, `document.querySelector('[data-action="plan"]') === null`), true, 'draft tabs have no plan source and must not expose the plan dock');
  await evaluate(cdp, `document.querySelector('.workdir-pill').click()`);
  await waitFor(cdp, `document.querySelector('.workdir-pill')?.title === '/tmp/draft'`);
  assert.equal(await evaluate(cdp, `document.querySelector('.chat-session-header .composer-session-info') === null`), true, 'draft must not duplicate the workdir selector in the header');
  assert.equal(await evaluate(cdp, `document.querySelector('.composer .workdir-pill') !== null`), true, 'draft keeps the workdir selector in the composer');
  await evaluate(cdp, `(() => { const textarea = document.querySelector('textarea'); textarea.value = 'draft hello'; textarea.dispatchEvent(new Event('input', { bubbles: true })); })()`);
  await waitFor(cdp, `document.querySelector('.send-btn')?.disabled === false`);
  await evaluate(cdp, `document.querySelector('.send-btn').click()`);
  // The draft tab is now labeled from the allocated session's title/preview
  // rather than its raw ID, so assert the replacement by tab composition.
  await waitFor(cdp, `document.querySelectorAll('.wb-tab').length === 2 && !(document.querySelector('.wb-tabstrip')?.textContent || '').match(/新任务|New task/)`);
  await waitFor(cdp, `document.body.textContent.includes('send accepted')`);
  assert.equal(await evaluate(cdp, `document.querySelectorAll('.wb-tab').length`), 2, 'draft tab must be atomically replaced');
  assert.equal(await evaluate(cdp, `(document.querySelector('.wb-tabstrip')?.textContent || '').includes('新任务') || (document.querySelector('.wb-tabstrip')?.textContent || '').includes('New task')`), false, 'draft tab must be gone');

  // Drag reorder and cross-pane drag must produce exactly the layout the drop
  // preview advertised. A two-column preset gives one tab per pane first.
  await evaluate(cdp, `document.querySelector('[data-action="preset-two-column"]').click()`);
  await waitFor(cdp, `document.querySelectorAll('.wb-pane').length === 2 && document.querySelectorAll('.wb-tab').length === 2`);
  assert.equal(await evaluate(cdp, `document.querySelectorAll('.wb-pane')[0]?.dataset.tabCount === '1' && document.querySelectorAll('.wb-pane')[1]?.dataset.tabCount === '1'`), true);
  assert.equal(await evaluate(cdp, `document.querySelectorAll('.wb-pane')[0]?.dataset.selectedTab.includes('session-e2e-a') === true`), true, 'two-column preset must keep the first tab in the left pane');

  // Move the draft tab from the right pane into the middle of the left pane.
  await startTabDrag(cdp, 'session:session-e2e-draft');
  await dragOverPaneAt(cdp, '.wb-pane', 0.5, 0.5);
  await waitFor(cdp, `document.querySelectorAll('.wb-pane')[0]?.classList.contains('wb-pane-dragover') === true`);
  assert.equal(await evaluate(cdp, `['left','right','top','bottom'].some((side) => document.querySelectorAll('.wb-pane')[0].classList.contains('wb-drop-' + side))`), false, 'middle drop preview must not show an edge split');
  await dropOnPane(cdp, '.wb-pane');
  await waitFor(cdp, `document.querySelectorAll('.wb-pane')[0]?.dataset.tabCount === '2' && document.querySelectorAll('.wb-pane')[1]?.dataset.tabCount === '0'`);

  // Same-pane reorder previews stay in the middle, and drops reorder the tabs.
  const tabOrder = () => evaluate(cdp, `[...document.querySelectorAll('.wb-tab')].map((tab) => tab.dataset.tabId || tab.querySelector('.wb-tab-text')?.textContent || '')`);
  await startTabDrag(cdp, 'session:session-e2e-draft');
  await dragOverTabEdge(cdp, '.wb-pane', 'before');
  await waitFor(cdp, `document.querySelector('.wb-pane-dragover') !== null`);
  assert.equal(await evaluate(cdp, `['left','right','top','bottom'].some((side) => document.querySelector('.wb-pane').classList.contains('wb-drop-' + side))`), false, 'middle reorder preview must not show an edge split');
  await dropOnPane(cdp, '.wb-pane');
  await waitFor(cdp, `(() => { const tabs = [...document.querySelectorAll('.wb-tab-text')]; return tabs[0]?.textContent.includes('session-e2e-draft') === true; })()`);
  assert.equal(await evaluate(cdp, `document.querySelector('.wb-pane-dragover') === null`), true, 'drop must clear the preview');

  await startTabDrag(cdp, 'session:session-e2e-draft');
  await dragOverTabEdge(cdp, '.wb-pane', 'after');
  await dropOnPane(cdp, '.wb-pane');
  await waitFor(cdp, `(() => { const tabs = [...document.querySelectorAll('.wb-tab-text')]; return tabs[tabs.length - 1]?.textContent.includes('session-e2e-draft') === true; })()`);
  assert.equal((await tabOrder()).length, 2);

  await startTabDrag(cdp, 'session:session-e2e-draft');
  await dragOverTabEdge(cdp, '.wb-pane', 'before');
  await dropOnPane(cdp, '.wb-pane');
  await waitFor(cdp, `(() => { const tabs = [...document.querySelectorAll('.wb-tab-text')]; return tabs[0]?.textContent.includes('session-e2e-draft') === true; })()`);

  // Closing the now-empty right pane returns to the single-pane projection.
  await evaluate(cdp, `document.querySelectorAll('.wb-pane')[1].dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }))`);
  await evaluate(cdp, `document.querySelector('[data-action="close-pane"]').click()`);
  await waitFor(cdp, `document.querySelector('.wb-single') !== null && document.querySelectorAll('.wb-tab').length === 2`);

  // Dropping on the right edge must split horizontally and place the dragged
  // tab in the new right-hand pane, matching the edge preview.
  await evaluate(cdp, `document.querySelector('[data-action="preset-two-column"]').click()`);
  await waitFor(cdp, `document.querySelectorAll('.wb-pane').length === 2`);
  await startTabDrag(cdp, 'session:session-e2e-draft');
  await dragOverPaneAt(cdp, '.wb-pane', 0.99, 0.5);
  await waitFor(cdp, `document.querySelectorAll('.wb-pane')[0]?.classList.contains('wb-drop-right') === true`);
  await dropOnPane(cdp, '.wb-pane');
  await waitFor(cdp, `document.querySelectorAll('.wb-pane').length === 3`);
  assert.equal(await evaluate(cdp, `document.querySelectorAll('.wb-pane')[1]?.dataset.selectedTab.includes('session-e2e-draft') === true`), true, 'right edge drop must place the tab in the new right-hand pane');
  assert.equal(await evaluate(cdp, `['left','right','top','bottom'].some((side) => document.querySelectorAll('.wb-pane')[0].classList.contains('wb-drop-' + side))`), false, 'drop must clear the edge preview');

  // Remove the empty pane left behind by the split, then move the draft tab
  // back into the left pane and collapse again.
  await evaluate(cdp, `(() => {
    const empty = [...document.querySelectorAll('.wb-pane')].find((pane) => pane.dataset.tabCount === '0');
    empty.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }));
  })()`);
  await evaluate(cdp, `document.querySelector('[data-action="close-pane"]').click()`);
  await waitFor(cdp, `document.querySelectorAll('.wb-pane').length === 2 && document.querySelectorAll('.wb-tab').length === 2`);
  await startTabDrag(cdp, 'session:session-e2e-a');
  await dragOverPaneAt(cdp, '.wb-pane', 0.5, 0.5);
  await waitFor(cdp, `document.querySelectorAll('.wb-pane')[0]?.classList.contains('wb-pane-dragover') === true`);
  await dropOnPane(cdp, '.wb-pane');
  await waitFor(cdp, `document.querySelectorAll('.wb-pane')[0]?.dataset.tabCount === '2' && document.querySelectorAll('.wb-pane')[1]?.dataset.tabCount === '0'`);
  await evaluate(cdp, `document.querySelectorAll('.wb-pane')[1].dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }))`);
  await evaluate(cdp, `document.querySelector('[data-action="close-pane"]').click()`);
  await waitFor(cdp, `document.querySelector('.wb-single') !== null && document.querySelectorAll('.wb-tab').length === 2`);

  // Multi-tab contract: a new browser page restores the shared last layout, and
  // concurrent writers settle last-write-wins without corrupting recovery.
  await evaluate(cdp, `document.querySelector('[data-action="preset-two-column"]').click()`);
  await waitFor(cdp, `document.querySelectorAll('.wb-pane').length === 2`);
  await waitFor(cdp, `(() => { try { return JSON.parse(localStorage.getItem('mothx.workbench.v1')).frame.center.kind === 'split'; } catch { return false; } })()`);
  const sharedBefore = await evaluate(cdp, `(() => {
    const doc = JSON.parse(localStorage.getItem('mothx.workbench.v1'));
    return { version: doc.version, split: doc.frame.center.kind === 'split', tabs: [...document.querySelectorAll('.wb-tab')].length };
  })()`);
  // localStorage is committed asynchronously across renderer processes; give
  // the new tab a moment to observe the shared document.
  await new Promise((resolve) => setTimeout(resolve, 500));
  const secondPage = await openPage(cdp.debugPort, `http://127.0.0.1:${address.port}/#/chat?session=session-e2e-a`);
  try {
    await secondPage.call('Page.enable');
    await secondPage.call('Runtime.enable');
    await secondPage.call('Log.enable');
    await secondPage.call('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
    await waitFor(secondPage, `document.querySelector('.wb-toolbar') !== null && document.querySelectorAll('.wb-pane').length === 2 && document.querySelectorAll('.wb-tab').length === ${sharedBefore.tabs}`);
    assert.equal(await evaluate(secondPage, `(() => { const doc = JSON.parse(localStorage.getItem('mothx.workbench.v1')); return doc.version === ${sharedBefore.version} && doc.frame.center.kind === 'split'; })()`), true, 'second page must inherit the shared layout');

    // Write from the second page. Reloading the first page must recover the
    // later document rather than its own stale in-memory layout.
    await evaluate(secondPage, `document.querySelector('[data-action="preset-single"]').click()`);
    await waitFor(secondPage, `document.querySelector('.wb-single') !== null`);
    await waitFor(cdp, `(() => { try { return JSON.parse(localStorage.getItem('mothx.workbench.v1')).frame.center.kind === 'group'; } catch { return false; } })()`);
    await cdp.call('Page.reload');
    await new Promise((resolve) => setTimeout(resolve, 1000));
    await waitFor(cdp, `document.querySelector('.wb-single') !== null && document.querySelectorAll('.wb-tab').length === ${sharedBefore.tabs}`);
    assert.equal(await evaluate(cdp, `document.querySelectorAll('.wb-pane').length`), 0, 'first page must recover the later single-pane document');

    // Now the first page writes the newer layout; the second page reloads into
    // it. This is the last-write-wins direction in the other order.
    await evaluate(cdp, `document.querySelector('[data-action="preset-two-column"]').click()`);
    await waitFor(cdp, `document.querySelectorAll('.wb-pane').length === 2`);
    await waitFor(cdp, `(() => { try { return JSON.parse(localStorage.getItem('mothx.workbench.v1')).frame.center.kind === 'split'; } catch { return false; } })()`);
    await secondPage.call('Page.reload');
    await new Promise((resolve) => setTimeout(resolve, 1000));
    await waitFor(secondPage, `document.querySelectorAll('.wb-pane').length === 2 && document.querySelectorAll('.wb-tab').length === ${sharedBefore.tabs}`);
    assert.equal(await evaluate(secondPage, `(() => { const doc = JSON.parse(localStorage.getItem('mothx.workbench.v1')); return doc.version === 1 && doc.frame.center.kind === 'split' && new Set((doc.view.groups.g1?.tabs || []).map((tab) => tab.id)).size === (doc.view.groups.g1?.tabs || []).length; })()`), true, 'shared document must remain valid and duplicate-free after last-write-wins');
  } finally {
    try { secondPage.socket.close(); } catch {}
  }

  // Sidebar can create a Draft while /chat is unmounted (for example from
  // Sessions/Settings). Chat is lazy-loaded and used to re-read localStorage on
  // every mount, which discarded that Draft before the 300ms persistence
  // debounce fired. The layout must survive the remount and then persist.
  const tabsBeforeDraft = await evaluate(cdp, `document.querySelectorAll('.wb-tab').length`);
  await evaluate(cdp, `(() => [...document.querySelectorAll('.nav-item')].find((button) => button.textContent.includes('Sessions') || button.textContent.includes('历史对话')).click())()`);
  await waitFor(cdp, `document.querySelector('.wb-toolbar') === null`);
  await evaluate(cdp, `document.querySelector('.new-chat').click()`);
  await waitFor(cdp, `document.querySelector('.wb-toolbar') !== null`);
  await waitFor(cdp, `document.querySelectorAll('.wb-tab').length === ${tabsBeforeDraft + 1}`);
  assert.equal(
    await evaluate(cdp, `(() => { const text = [...document.querySelectorAll('.wb-tabstrip')].map((strip) => strip.textContent).join(' '); return text.includes('New task') || text.includes('新任务'); })()`),
    true,
    'a Draft created outside /chat must survive Chat remounting'
  );
  await waitFor(cdp, `(() => { try { const groups = JSON.parse(localStorage.getItem('mothx.workbench.v1')).view.groups; return Object.values(groups).reduce((sum, group) => sum + (group.tabs || []).length, 0) >= ${tabsBeforeDraft + 1}; } catch { return false; } })()`);

} finally {
  try { await cdp?.call('Browser.close'); } catch {}
  try { cdp?.socket.close(); } catch {}
  try { cdp?.browser.kill('SIGTERM'); } catch {}
  for (const socket of wsSockets) socket.destroy();
  await new Promise((resolve) => server.close(resolve));
}
