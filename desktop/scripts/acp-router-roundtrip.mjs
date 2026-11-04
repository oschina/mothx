// Headless Desktop Router round-trip e2e: drives the real MothX runtime over
// ACP stdio and asserts the Router selection projection Desktop performs.
//
// It proves the whole chain a Desktop user depends on: the worker catalog is
// projected through mothx/manage/acp/profiles/list, a session binds a worker
// through mothx/manage/router/activate, the Runtime persists the binding, and
// the session reports Router mode.
//
// The external ACP worker is a fake Agent that speaks ACP v1 NDJSON on stdio,
// so the test needs no real coding agent and no display.
//
// Usage: node scripts/acp-router-roundtrip.mjs

import { spawn } from 'node:child_process';
import { statSync, mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createInterface } from 'node:readline';
import { fileURLToPath } from 'node:url';

const desktopRoot = resolve(fileURLToPath(new URL('..', import.meta.url)));
const repoRoot = resolve(desktopRoot, '..');

function goos() {
  return process.platform;
}
function goarch() {
  return process.arch === 'x64' ? 'amd64' : process.arch;
}
function runtimeBinary() {
  // MOTHX_ROUTER_E2E_BINARY lets the harness point at a freshly built runtime
  // that carries the Router projection; otherwise the vendored one is used.
  if (process.env.MOTHX_ROUTER_E2E_BINARY) return process.env.MOTHX_ROUTER_E2E_BINARY;
  const name = process.platform === 'win32' ? 'mothx.exe' : 'mothx';
  const candidates = [
    join(desktopRoot, 'vendor', 'mothx', 'bin', `${goos()}-${goarch()}`, name),
    join(desktopRoot, 'vendor', 'mothx', 'bin', goarch(), name),
    join(repoRoot, 'bin', name),
  ];
  for (const candidate of candidates) {
    try {
      if (statSync(candidate).isFile()) return candidate;
    } catch {
      // try the next candidate
    }
  }
  throw new Error(
    `MothX runtime not found; run "npm run build:runtime" (tried ${candidates.join(', ')}). ` +
    'The Router projection must be present in the runtime, so a runtime built before it cannot run this check.',
  );
}

// The fake ACP Agent. It answers initialize/session-new/session-prompt with
// one text update so a Router worker is a real, runnable external Agent.
function fakeACPWorker() {
  const script = `
    const readline = require('node:readline');
    const write = (value) => process.stdout.write(JSON.stringify(value) + '\n');
    const sessions = new Map();
    let seq = 0;
    const send = (id, result) => write({ jsonrpc: '2.0', id, result });
    readline.createInterface({ input: process.stdin }).on('line', (line) => {
      let request;
      try { request = JSON.parse(line); } catch { return; }
      if (request.method === 'initialize') {
        send(request.id, {
          protocolVersion: 1,
          agentInfo: { name: 'router-e2e-worker', version: '1' },
          agentCapabilities: { sessionCapabilities: { close: {} } },
        });
        return;
      }
      if (request.method === 'session/new') {
        seq += 1;
        const sessionId = 'router-e2e-session-' + seq;
        sessions.set(sessionId, true);
        send(request.id, { sessionId });
        return;
      }
      if (request.method === 'session/prompt') {
        const sessionId = request.params?.sessionId || '';
        write({
          jsonrpc: '2.0',
          method: 'session/update',
          params: {
            sessionId,
            update: {
              sessionUpdate: 'agent_message_chunk',
              content: { type: 'text', text: 'router worker inspected the task' },
            },
          },
        });
        send(request.id, { stopReason: 'end_turn' });
        return;
      }
      if (request.method === 'session/close') {
        send(request.id, {});
        return;
      }
      if (request.id !== undefined) send(request.id, {});
    });
  `;
  return spawn(process.execPath, ['-e', script], { stdio: ['pipe', 'pipe', 'pipe'] });
}

const results = [];
const record = (name, ok, detail = '') => {
  results.push({ name, ok });
  console.log(`${ok ? 'PASS' : 'FAIL'} ${name}${ok || !detail ? '' : ` — ${detail}`}`);
};

const configDir = mkdtempSync(join(tmpdir(), 'mothx-acp-router-e2e-'));
const workDir = mkdtempSync(join(tmpdir(), 'mothx-acp-router-e2e-work-'));
let provider;
let child;
let worker;

const cleanup = () => {
  for (const proc of [child, worker]) {
    try { proc?.kill('SIGKILL'); } catch { /* already gone */ }
  }
  try { provider?.server.close(); } catch { /* already closed */ }
  for (const dir of [configDir, workDir]) {
    try { rmSync(dir, { recursive: true, force: true }); } catch { /* leave the unique temp dir behind */ }
  }
};

try {
  const binary = runtimeBinary();

  // A mock OpenAI-compatible provider keeps the lead model happy; the Router
  // projection never needs it, but the session must be constructible.
  const server = createServer((req, res) => {
    if (req.method !== 'POST' || req.url !== '/v1/chat/completions') {
      res.writeHead(404).end();
      return;
    }
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    res.write(`data: ${JSON.stringify({ id: 'chatcmpl-router', object: 'chat.completion.chunk', choices: [{ index: 0, delta: { role: 'assistant', content: 'ok' }, finish_reason: null }] })}\n`);
    res.write(`data: ${JSON.stringify({ id: 'chatcmpl-router', object: 'chat.completion.chunk', choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] })}\n`);
    res.write('data: [DONE]\n\n');
    res.end();
  });
  provider = await new Promise((resolveStarted) => server.listen(0, '127.0.0.1', () => resolveStarted({ server, port: server.address().port })));

  worker = fakeACPWorker();

  writeFileSync(join(configDir, 'settings.json'), JSON.stringify({
    providers: {
      e2e: {
        apiKey: 'test-key',
        baseUrl: `http://127.0.0.1:${provider.port}/v1`,
        api: 'openai-chat',
        models: [{ id: 'e2e-model', name: 'E2E Model', contextWindow: 32768, maxTokens: 1024 }],
      },
    },
    defaultProvider: 'e2e',
    defaultModel: 'e2e-model',
    defaultMode: 'yolo',
    sessionDir: join(configDir, 'sessions'),
    acpAgents: [
      {
        id: 'router-e2e-worker',
        // The fake worker speaks ACP on stdio, exactly like a local worker.
        command: [process.execPath, join(configDir, 'fake-worker.cjs')],
        enabled: true,
        permissionPolicy: 'auto_allow_once',
        roles: ['investigate'],
        writeRoots: ['src'],
      },
      {
        id: 'disabled-worker',
        command: [process.execPath, join(configDir, 'fake-worker.cjs')],
        enabled: false,
        permissionPolicy: 'auto_allow_once',
      },
    ],
  }, null, 2));

  // The fake worker script is written next to the settings so the profile can
  // launch it by absolute path.
  writeFileSync(join(configDir, 'fake-worker.cjs'), `
    const readline = require('node:readline');
    const write = (value) => process.stdout.write(JSON.stringify(value) + '\n');
    const send = (id, result) => write({ jsonrpc: '2.0', id, result });
    let seq = 0;
    readline.createInterface({ input: process.stdin }).on('line', (line) => {
      let request;
      try { request = JSON.parse(line); } catch { return; }
      if (request.method === 'initialize') {
        send(request.id, { protocolVersion: 1, agentInfo: { name: 'router-e2e-worker', version: '1' }, agentCapabilities: { sessionCapabilities: { close: {} } } });
        return;
      }
      if (request.method === 'session/new') {
        seq += 1;
        send(request.id, { sessionId: 'router-e2e-session-' + seq });
        return;
      }
      if (request.method === 'session/prompt') {
        write({ jsonrpc: '2.0', method: 'session/update', params: { sessionId: request.params?.sessionId || '', update: { sessionUpdate: 'agent_message_chunk', content: { type: 'text', text: 'router worker inspected the task' } } } });
        send(request.id, { stopReason: 'end_turn' });
        return;
      }
      if (request.id !== undefined) send(request.id, {});
    });
  `);

  child = spawn(binary, ['acp'], { env: { ...process.env, MOTHX_DIR: configDir }, stdio: ['pipe', 'pipe', 'pipe'] });

  let nextId = 1;
  const pending = new Map();
  createInterface({ input: child.stdout }).on('line', (line) => {
    let message;
    try {
      message = JSON.parse(line);
    } catch {
      return;
    }
    if (message.id === undefined) {
      for (const entry of pending.values()) entry.notifications.push(message);
      return;
    }
    const entry = pending.get(message.id);
    if (entry) {
      pending.delete(message.id);
      entry.resolve(message);
    }
  });
  child.stderr.on('data', (chunk) => process.stderr.write(chunk));

  const request = (method, params) => {
    const id = nextId++;
    const entry = { notifications: [] };
    const settled = new Promise((resolveRequest) => {
      entry.resolve = resolveRequest;
      pending.set(id, entry);
    });
    child.stdin.write(`${JSON.stringify({ jsonrpc: '2.0', id, method, params })}\n`);
    return settled.then((message) => ({ message, notifications: entry.notifications }));
  };

  const initialize = await request('initialize', { protocolVersion: 1 });
  record('initialize', initialize.message.result !== undefined, JSON.stringify(initialize.message.error ?? {}));

  const created = await request('session/new', { cwd: workDir });
  const sessionId = created.message.result?.sessionId;
  record('session/new returns a session id', typeof sessionId === 'string' && sessionId.length > 0, JSON.stringify(created.message.error ?? {}));

  if (!sessionId) throw new Error('no session to drive');

  // The catalog must project the Runtime-owned eligibility: the enabled
  // non-interactive worker is Router-eligible, the disabled one is not.
  const catalog = await request('mothx/manage/acp/profiles/list', {});
  const profiles = catalog.message.result?.profiles || [];
  const eligible = profiles.find((profile) => profile.id === 'router-e2e-worker');
  const disabled = profiles.find((profile) => profile.id === 'disabled-worker');
  record('catalog projects the eligible worker', eligible?.routerEligible === true, JSON.stringify(profiles));
  record('catalog keeps a disabled worker ineligible', disabled?.routerEligible === false, JSON.stringify(profiles));
  record('catalog hides the worker argv', !JSON.stringify(profiles).includes('fake-worker.cjs'), JSON.stringify(profiles));

  // An unbound session reports no Router selection.
  const before = await request('mothx/manage/router/state', { sessionId });
  record('router state starts unbound', before.message.result?.enabled === false, JSON.stringify(before.message.result ?? before.message.error ?? {}));

  // Activating binds the worker through the shared Runtime path.
  const activated = await request('mothx/manage/router/activate', { sessionId, profileIds: ['router-e2e-worker'] });
  record('router activation binds the worker', activated.message.result?.enabled === true, JSON.stringify(activated.message.result ?? activated.message.error ?? {}));
  record('router activation selects the worker', activated.message.result?.primaryProfile === 'router-e2e-worker', JSON.stringify(activated.message.result ?? {}));
  record('router activation enters router mode', activated.message.result?.mode === 'router', JSON.stringify(activated.message.result ?? {}));

  // The binding must survive a fresh read, because the next session attach
  // replays it.
  const after = await request('mothx/manage/router/state', { sessionId });
  record('router state replays the binding', after.message.result?.enabled === true, JSON.stringify(after.message.result ?? {}));

  // An unknown worker must be refused without leaving a binding behind.
  const refused = await request('mothx/manage/router/activate', { sessionId, profileIds: ['not-a-worker'] });
  record('unknown worker is refused', refused.message.error !== undefined, JSON.stringify(refused.message.result ?? {}));
  const stillBound = await request('mothx/manage/router/state', { sessionId });
  record('a refused activation keeps the existing binding', stillBound.message.result?.enabled === true, JSON.stringify(stillBound.message.result ?? {}));

  // Leaving Router returns the session to an ordinary mode.
  const left = await request('mothx/manage/router/deactivate', { sessionId });
  record('router deactivation leaves router mode', left.message.result?.mode !== 'router', JSON.stringify(left.message.result ?? left.message.error ?? {}));

  child.stdin.end();
  await new Promise((resolveExit) => child.on('exit', resolveExit));
} catch (error) {
  record('acp router roundtrip completed', false, error?.stack ?? String(error));
} finally {
  cleanup();
}

const failed = results.filter((entry) => !entry.ok);
console.log(failed.length === 0 ? 'ACP ROUTER ROUNDTRIP DONE' : `ACP ROUTER ROUNDTRIP FAILED (${failed.length})`);
process.exit(failed.length === 0 ? 0 : 1);
