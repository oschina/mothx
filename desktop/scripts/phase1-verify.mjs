// Phase 1 验收 harness：对重建后的 vendor 运行时验证工作单 #2 的 desktop 相关协议面。
// 用法：node scripts/phase1-verify.mjs [binaryPath]（默认 desktop/vendor/mothx/bin/<arch>/mothx）
// 覆盖：features 发现键、lastRun(active/终态)、setMeta/projects、workspace/extend、
//       attachment/list + fetch 往返、resume 制品重放。
// 审批 deadline 与 subagent 由工作单 #2 的进程级 wire 测试覆盖（需 multi-agent/审批场景）。

import { spawn } from 'node:child_process';
import { createInterface } from 'node:readline';
import { existsSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { join, dirname } from 'node:path';

const root = fileURLToPath(new URL('..', import.meta.url));
const binaryName = process.platform === 'win32' ? 'mothx.exe' : 'mothx';
const goarch = process.arch === 'x64' ? 'amd64' : process.arch;
const goos = process.platform === 'win32' ? 'windows' : process.platform === 'darwin' ? 'darwin' : process.platform;
const binary =
  process.argv[2] ||
  [
    join(root, 'vendor', 'mothx', 'bin', `${goos}-${goarch}`, binaryName),
    join(root, 'vendor', 'mothx', 'bin', goarch, binaryName),
    join(root, 'vendor', 'mothx', 'bin', binaryName),
  ].find(existsSync) ||
  join(root, 'vendor', 'mothx', 'bin', `${goos}-${goarch}`, binaryName);
const WS = join(root, '.phase1-verify-ws');
rmSync(WS, { recursive: true, force: true });
mkdirSync(WS, { recursive: true });
const EXTRA = join(WS, 'extra-dir');
mkdirSync(EXTRA, { recursive: true });
writeFileSync(join(EXTRA, 'note.txt'), 'extended workspace file');

const child = spawn(binary, ['acp'], {
  cwd: WS,
  env: { ...process.env, MOTHX_ACP_PERMISSION_TIMEOUT: '30m', MOTHX_ACP_QUESTION_TIMEOUT: '30m' },
  stdio: ['pipe', 'pipe', 'pipe'],
});
const rl = createInterface({ input: child.stdout });
let nextId = 1;
const pending = new Map();
const updates = [];
const events = [];
function send(method, params) {
  const id = nextId++;
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    child.stdin.write(JSON.stringify({ jsonrpc: '2.0', id, method, params }) + '\n');
  });
}
rl.on('line', (line) => {
  let msg;
  try {
    msg = JSON.parse(line);
  } catch {
    return;
  }
  if (msg.id !== undefined && pending.has(msg.id)) {
    const p = pending.get(msg.id);
    pending.delete(msg.id);
    if (msg.error) p.reject(Object.assign(new Error(`${msg.error.code}: ${msg.error.message}`), { data: msg.error.data }));
    else p.resolve(msg.result);
    return;
  }
  if (msg.method === 'session/update') updates.push(msg.params?.update || {});
  if (msg.method === '_mothx/session_event') events.push(msg.params || {});
});
child.stderr.on('data', (d) => {
  const s = d.toString().trim();
  if (s) console.log('  [stderr]', s.slice(0, 200));
});

let failures = 0;
const check = (name, cond, extra = '') => {
  console.log(`${cond ? 'PASS' : 'FAIL'} ${name}${extra ? ' — ' + extra : ''}`);
  if (!cond) failures += 1;
};
const meta = (result) => result?._meta?.['mothx.dev'] || result?._meta?.mothx || {};

try {
  const init = await send('initialize', {
    protocolVersion: 1,
    clientCapabilities: { fs: {}, terminal: false, session: { configOptions: { boolean: {} } } },
    clientInfo: { name: 'mothx-desktop-verify', version: '0' },
    _meta: { mothx: { workspace: { cwd: WS }, surface: 'desktop' } },
  });
  const features = meta(init.agentCapabilities)?.features || [];
  const required = ['runStatus', 'sessionMeta', 'projects', 'workspaceExtend', 'decisionDeadline', 'subagentEvents', 'toolResultImages', 'attachmentList'];
  check('features 发现键齐备', required.every((key) => features.includes(key)), features.join(','));

  const created = await send('session/new', { cwd: WS, _meta: { mothx: { workspace: { cwd: WS } } } });
  const sid = created.sessionId;

  // lastRun：运行中 active=true，终态后 completed
  const promptPromise = send('session/prompt', {
    sessionId: sid,
    prompt: [{ type: 'text', text: 'Write a file verify.txt containing "phase1" in the working directory, then publish_artifact it. Reply with one word.' }],
    _meta: { mothx: { workspace: { cwd: WS } } },
  });
  await new Promise((resolve) => setTimeout(resolve, 2500));
  const listRunning = await send('session/list', { cwd: WS, _meta: { mothx: { workspace: { cwd: WS } } } });
  const runningEntry = (listRunning.sessions || []).find((s) => s.sessionId === sid);
  check('lastRun active=true（运行中）', runningEntry?._meta?.lastRun?.active === true || runningEntry?._meta?.lastRun?.status === 'running', JSON.stringify(runningEntry?._meta?.lastRun || null));
  const promptResult = await promptPromise;
  check('prompt 完成', ['end_turn', 'stop'].includes(promptResult?.stopReason), promptResult?.stopReason);
  const listDone = await send('session/list', { cwd: WS, _meta: { mothx: { workspace: { cwd: WS } } } });
  const doneEntry = (listDone.sessions || []).find((s) => s.sessionId === sid);
  check('lastRun 终态 completed', doneEntry?._meta?.lastRun?.status === 'completed' && doneEntry?._meta?.lastRun?.active !== true, JSON.stringify(doneEntry?._meta?.lastRun || null));
  check('run_status 事件投影', events.some((e) => e.event === 'run_status'), `${events.filter((e) => e.event === 'run_status').length} events`);

  // setMeta + projects
  await send('mothx/session/setMeta', { sessionId: sid, pinned: true });
  const project = await send('mothx/projects/create', { name: 'verify-project' });
  const projectId = project?.id || project?.project?.id;
  check('projects/create 返回 id', !!projectId, JSON.stringify(project).slice(0, 120));
  if (projectId) {
    await send('mothx/session/setMeta', { sessionId: sid, projectId });
  }
  const listMeta = await send('session/list', { cwd: WS, _meta: { mothx: { workspace: { cwd: WS } } } });
  const metaEntry = (listMeta.sessions || []).find((s) => s.sessionId === sid);
  check('list _meta.pinned 透传', metaEntry?._meta?.pinned === true, JSON.stringify(metaEntry?._meta?.pinned));
  check('list _meta.projectId 透传', !projectId || metaEntry?._meta?.projectId === projectId, JSON.stringify(metaEntry?._meta?.projectId));
  const projects = await send('mothx/projects/list', {});
  check('projects/list 含新建项目', (projects?.projects || []).some((p) => p.id === projectId));

  // workspace/extend
  const extended = await send('mothx/workspace/extend', { additionalDirectories: [EXTRA] });
  check('workspace/extend 生效', (extended?.additionalDirectories || []).includes(EXTRA), JSON.stringify(extended?.additionalDirectories || []));
  check('workspace 事件投影', events.some((e) => e.event === 'workspace'));
  let extendBad = null;
  try {
    await send('mothx/workspace/extend', { additionalDirectories: ['relative/path'] });
  } catch (e) {
    extendBad = e.message;
  }
  check('workspace/extend 拒绝非绝对路径', !!extendBad, extendBad || 'no error');

  // attachment/list + fetch + resume 重放
  const artifacts = updates.filter((u) => u.sessionUpdate === 'artifact');
  check('artifact 更新收到', artifacts.length > 0, `${artifacts.length}`);
  const listAttach = await send('mothx/attachment/list', { sessionId: sid, status: 'generated' });
  const attachments = listAttach?.attachments || [];
  check('attachment/list 与 artifact 事件一致', attachments.length === artifacts.length && attachments.every((a) => artifacts.some((ev) => ev.artifactId === a.attachmentId)), `${attachments.length} vs ${artifacts.length}`);
  if (attachments.length > 0) {
    const fetched = await send('mothx/attachment/fetch', { sessionId: sid, attachmentId: attachments[0].attachmentId });
    check('fetch 内容往返', Buffer.from(fetched.contentBase64, 'base64').toString('utf8') === 'phase1', fetched.filename);
  }
  await send('session/close', { sessionId: sid });
  updates.length = 0;
  await send('session/resume', { sessionId: sid, cwd: WS, _meta: { mothx: { workspace: { cwd: WS } } } });
  check('resume 重放制品', updates.some((u) => u.sessionUpdate === 'artifact'), `${updates.filter((u) => u.sessionUpdate === 'artifact').length} replayed`);

  // 清理
  await send('session/close', { sessionId: sid }).catch(() => {});
  await send('session/delete', { sessionId: sid });
  if (projectId) await send('mothx/projects/delete', { id: projectId }).catch(() => {});

  console.log(failures === 0 ? 'PHASE1 VERIFY DONE (all pass)' : `PHASE1 VERIFY FAILURES: ${failures}`);
  process.exitCode = failures === 0 ? 0 : 1;
} catch (error) {
  console.log('VERIFY ERROR:', error.message, JSON.stringify(error.data || null));
  process.exitCode = 1;
} finally {
  child.stdin.end();
  child.kill('SIGTERM');
  setTimeout(() => process.exit(process.exitCode || 0), 400);
}
setTimeout(() => {
  console.log('VERIFY TIMEOUT');
  process.exit(1);
}, 300000);
