// Headless Desktop/ACP round-trip e2e: drives the *vendored* MothX runtime
// binary over ACP stdio against a local mock OpenAI-compatible provider and
// asserts the real session round trip Desktop performs — session/new →
// session/prompt → session/close → session/load — replays the transcript.
//
// It deliberately does not launch Electron (that is scripts/e2e-smoke.mjs, which
// needs a display): this checks the shipped runtime artifact's ACP behavior and
// can run in headless CI. Run `npm run build:runtime` first.
//
// Usage: node scripts/acp-roundtrip.mjs

import { spawn } from 'node:child_process';
import { statSync, mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createInterface } from 'node:readline';
import { fileURLToPath } from 'node:url';

const desktopRoot = resolve(fileURLToPath(new URL('..', import.meta.url)));
const repoRoot = resolve(desktopRoot, '..');
const assistantText = 'ACP roundtrip response';

function goos() {
  return process.platform;
}
function goarch() {
  return process.arch === 'x64' ? 'amd64' : process.arch;
}
function runtimeBinary() {
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
  throw new Error(`MothX runtime not found; run "npm run build:runtime" (tried ${candidates.join(', ')})`);
}

function startMockProvider() {
  const server = createServer((req, res) => {
    if (req.method !== 'POST' || req.url !== '/v1/chat/completions') {
      res.writeHead(404).end();
      return;
    }
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    res.write(`data: ${JSON.stringify({ id: 'chatcmpl-e2e', object: 'chat.completion.chunk', choices: [{ index: 0, delta: { role: 'assistant', content: assistantText }, finish_reason: null }] })}\n`);
    res.write(`data: ${JSON.stringify({ id: 'chatcmpl-e2e', object: 'chat.completion.chunk', choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] })}\n`);
    res.write('data: [DONE]\n\n');
    res.end();
  });
  return new Promise((resolveStarted) => server.listen(0, '127.0.0.1', () => resolveStarted({ server, port: server.address().port })));
}

function containsAgentChunk(notifications, text) {
  return notifications.some((message) => {
    const params = message.params ?? {};
    const update = params.update ?? {};
    return message.method === 'session/update' && update.sessionUpdate === 'agent_message_chunk' && update.content?.text === text;
  });
}

const results = [];
const record = (name, ok, detail = '') => {
  results.push({ name, ok });
  console.log(`${ok ? 'PASS' : 'FAIL'} ${name}${ok || !detail ? '' : ` — ${detail}`}`);
};

const configDir = mkdtempSync(join(tmpdir(), 'mothx-acp-roundtrip-'));
const workDir = mkdtempSync(join(tmpdir(), 'mothx-acp-roundtrip-work-'));
let provider;
let child;

const cleanup = () => {
  try {
    child?.kill('SIGKILL');
  } catch {
    // already gone
  }
  try {
    provider?.server.close();
  } catch {
    // already closed
  }
  for (const dir of [configDir, workDir]) {
    try {
      rmSync(dir, { recursive: true, force: true });
    } catch {
      // leave the unique temp dir behind rather than touch anything else
    }
  }
};

try {
  const binary = runtimeBinary();
  provider = await startMockProvider();
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
  }, null, 2));

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
  record('session/new returns a session id', typeof sessionId === 'string' && sessionId.length > 0, JSON.stringify(created.message.result ?? created.message.error ?? {}));

  if (sessionId) {
    const prompted = await request('session/prompt', { sessionId, prompt: [{ type: 'text', text: 'round trip' }] });
    record('session/prompt stops at end_turn', prompted.message.result?.stopReason === 'end_turn', JSON.stringify(prompted.message.result ?? prompted.message.error ?? {}));
    record('session/prompt streams the assistant reply', containsAgentChunk(prompted.notifications, assistantText));

    const closed = await request('session/close', { sessionId });
    record('session/close succeeds', closed.message.error === undefined, JSON.stringify(closed.message.error ?? {}));

    const reloaded = await request('session/load', { sessionId, cwd: workDir });
    record('session/load replays the assistant reply', containsAgentChunk(reloaded.notifications, assistantText));
  }

  child.stdin.end();
  await new Promise((resolveExit) => child.on('exit', resolveExit));
} catch (error) {
  record('acp roundtrip completed', false, error?.stack ?? String(error));
} finally {
  cleanup();
}

const failed = results.filter((result) => !result.ok);
if (failed.length > 0) {
  console.error(`ACP ROUNDTRIP FAILED (${failed.length}/${results.length})`);
  process.exit(1);
}
console.log('ACP ROUNDTRIP DONE');
