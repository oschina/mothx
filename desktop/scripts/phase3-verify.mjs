// Phase 3 验收 harness：对重建后的 vendor 运行时验证工作单 #3 的 mothx/manage/* 管理面。
// 用法：node scripts/phase3-verify.mjs [binaryPath]
// 安全：mcp/set 仅做同列表往返；memory/put 先备份原内容并恢复；cron 测试任务用完即删。

import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { createInterface } from 'node:readline';
import { fileURLToPath } from 'node:url';
import { join } from 'node:path';

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

const child = spawn(binary, ['acp'], { cwd: root, stdio: ['pipe', 'pipe', 'pipe'] });
const rl = createInterface({ input: child.stdout });
let nextId = 1;
const pending = new Map();
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

try {
  const init = await send('initialize', { protocolVersion: 1, clientInfo: { name: 'mothx-desktop-verify3', version: '0' } });
  const features = (init.agentCapabilities?._meta?.['mothx.dev'] || init.agentCapabilities?._meta?.mothx || {}).features || [];
  const required = ['manageSettings', 'manageProviders', 'manageSkills', 'manageMcp', 'manageCron', 'manageStats', 'manageMemory'];
  check('manage features 齐备', required.every((k) => features.includes(k)), features.filter((f) => f.startsWith('manage')).join(','));

  // settings get/patch
  const settings = await send('mothx/manage/settings/get', {});
  check('settings/get providers 视图', Array.isArray(settings?.providers) && settings.providers.length > 0, `${settings?.providers?.length ?? 0} providers`);
  const masked = (settings?.providers || []).map((p) => p.maskedKey).filter(Boolean);
  check('密钥掩码格式', masked.every((m) => m.includes('***')), masked.join(',') || 'no keys configured');
  let patchErr = null;
  try {
    await send('mothx/manage/settings/patch', { patch: { evilField: 1 } });
  } catch (e) {
    patchErr = e.data?.code || e.message;
  }
  check('patch 白名单越权拒绝', patchErr === 'settings_field_not_allowed', String(patchErr));

  // providers list/test
  const providers = await send('mothx/manage/providers/list', {});
  const providerList = providers?.providers || providers?.catalog || [];
  check('providers/list 含 models', providerList.length > 0, `${providerList.length} entries`);
  const testName = settings?.defaultProvider || providerList[0]?.name;
  if (testName) {
    const testResult = await send('mothx/manage/providers/test', { provider: testName });
    check('providers/test 形状', typeof testResult?.ok === 'boolean', JSON.stringify(testResult).slice(0, 120));
  }

  // skills
  const skills = await send('mothx/manage/skills/list', {});
  check('skills/list 形状', Array.isArray(skills?.skills), `${skills?.skills?.length ?? 0} skills`);
  let skillErr = null;
  try {
    await send('mothx/manage/skills/set', { name: 'no-such-skill-xyz', enabled: true });
  } catch (e) {
    skillErr = e.data?.code || e.message;
  }
  check('skills/set 未知技能错误', skillErr === 'skill_not_found', String(skillErr));

  // mcp 往返（同列表，不改动用户配置）
  const mcp = await send('mothx/manage/mcp/list', {});
  const servers = mcp?.servers || [];
  const mcpSet = await send('mothx/manage/mcp/set', { servers });
  check('mcp/set 同列表往返', Array.isArray(mcpSet?.servers) && mcpSet.servers.length === servers.length, `${servers.length} servers`);
  check('mcp env 掩码', servers.every((s) => !s.env || Object.values(s.env || {}).every((v) => typeof v !== 'string' || v === '')), 'env values not echoed');

  // cron create → run → completed → remove
  const created = await send('mothx/manage/cron/create', {
    name: 'verify-cron',
    schedule: '0 0 1 1 *',
    prompt: 'Reply with the single word: ok',
    mode: 'yolo',
    enabled: true,
  });
  const jobId = created?.id || created?.job?.id;
  check('cron/create 返回 id', !!jobId, JSON.stringify(created).slice(0, 120));
  if (jobId) {
    const listBefore = await send('mothx/manage/cron/list', {});
    check('cron/list 含新任务', (listBefore?.jobs || []).some((j) => j.id === jobId));
    await send('mothx/manage/cron/run', { id: jobId });
    const deadline = Date.now() + 90000;
    let completed = null;
    while (Date.now() < deadline && !completed) {
      completed = events.find((e) => e.event === 'cron_completed' && String(e.jobId || '') === jobId) || null;
      if (!completed) await new Promise((r) => setTimeout(r, 1500));
    }
    check('cron_completed 事件', !!completed, JSON.stringify(completed || null).slice(0, 120));
    const listAfter = await send('mothx/manage/cron/list', {});
    const job = (listAfter?.jobs || []).find((j) => j.id === jobId);
    check('cron lastRun 写回', !!job?.lastRun || !!job?.lastStatus, JSON.stringify({ at: job?.lastRun, status: job?.lastStatus }));
    await send('mothx/manage/cron/remove', { id: jobId });
    const listEnd = await send('mothx/manage/cron/list', {});
    check('cron/remove 生效', !(listEnd?.jobs || []).some((j) => j.id === jobId));
  }

  // stats
  const summary = await send('mothx/manage/stats/summary', {});
  check('stats/summary 形状', typeof summary === 'object' && summary !== null && ('runs' in summary || 'sessions' in summary), JSON.stringify(summary).slice(0, 120));
  const series = await send('mothx/manage/stats/timeseries', { group: 'day', days: 14 });
  check('stats/timeseries 形状', Array.isArray(series?.points), `${series?.points?.length ?? 0} points`);

  // memory 往返恢复
  const original = await send('mothx/manage/memory/get', {});
  const put = await send('mothx/manage/memory/put', { content: 'phase3 verify memory' });
  check('memory/put 返回 size', typeof put?.size === 'number' && put.size === 'phase3 verify memory'.length, JSON.stringify(put));
  const readBack = await send('mothx/manage/memory/get', {});
  check('memory 往返', readBack?.content === 'phase3 verify memory');
  await send('mothx/manage/memory/put', { content: original?.content || '' });
  const restored = await send('mothx/manage/memory/get', {});
  check('memory 恢复原状', (restored?.content || '') === (original?.content || ''));

  console.log(failures === 0 ? 'PHASE3 VERIFY DONE (all pass)' : `PHASE3 VERIFY FAILURES: ${failures}`);
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
