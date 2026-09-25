import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

import { applyWorktreeStatus, waitForWorktree, worktreeReadiness } from './worktrees.ts';

const here = dirname(fileURLToPath(import.meta.url));
const worktreesSource = readFileSync(join(here, 'worktrees.ts'), 'utf8');
const composerSource = readFileSync(join(here, '..', 'components', 'Composer.tsx'), 'utf8');
const clientSource = readFileSync(join(here, '..', '..', '..', 'main', 'acp-client.ts'), 'utf8');
const translations = readFileSync(join(here, 'i18n.ts'), 'utf8');

const dir = (name: string) => `/tmp/mothx-worktree-${name}-${Math.random().toString(16).slice(2)}`;

test('waitForWorktree resolves a pending worktree on ready', async () => {
  const key = dir('ready');
  applyWorktreeStatus({ directory: key, status: 'pending' });
  const waiting = waitForWorktree(`${key}/`);
  assert.deepEqual(worktreeReadiness(key), { status: 'pending' });

  applyWorktreeStatus({ directory: key, status: 'ready' });
  assert.deepEqual(await waiting, { status: 'ready' });
  assert.deepEqual(await waitForWorktree(key), { status: 'ready' });
});

test('waitForWorktree surfaces a failure message', async () => {
  const key = dir('failed');
  applyWorktreeStatus({ directory: key, status: 'pending' });
  const waiting = waitForWorktree(key);
  applyWorktreeStatus({ directory: key, status: 'failed', error: 'checkout failed' });
  assert.deepEqual(await waiting, { status: 'failed', message: 'checkout failed' });
});

test('terminal readiness is not overwritten by a late pending notification', () => {
  const key = dir('sticky');
  applyWorktreeStatus({ directory: key, status: 'ready' });
  applyWorktreeStatus({ directory: key, status: 'pending' });
  assert.deepEqual(worktreeReadiness(key), { status: 'ready' });
});

test('removed clears the readiness projection', () => {
  const key = dir('removed');
  applyWorktreeStatus({ directory: key, status: 'ready' });
  applyWorktreeStatus({ directory: key, status: 'removed' });
  assert.equal(worktreeReadiness(key), undefined);
});

test('worktree actions stay behind the ACP capability gate', () => {
  assert.match(worktreesSource, /hasFeature\('worktrees'\)/, 'worktrees must be capability-gated');
  for (const method of ['mothx/worktree/list', 'mothx/worktree/create', 'mothx/worktree/remove', 'mothx/worktree/reset']) {
    assert.ok(worktreesSource.includes(method), `missing ${method}`);
  }
  assert.doesNotMatch(worktreesSource, /child_process|require\('node:fs'\)|localStorage/i, 'renderer must not run git or persist locally');
});

test('main forwards the worktree status notification to the renderer', () => {
  assert.match(clientSource, /method === 'mothx\/worktree\/status'/, 'main must handle the worktree status notification');
  assert.match(clientSource, /onWorktreeStatus/, 'main must expose a worktree status handler');
});

test('composer exposes the worktree action only when advertised', () => {
  assert.match(composerSource, /worktreeSupported\(\)/, 'composer must gate the worktree control on the capability key');
  assert.match(composerSource, /createIsolatedWorktree\(\)/, 'composer must call the shared worktree action');
});

test('composer worktree menu lists, resets and removes through shared actions', () => {
  assert.match(composerSource, /function WorktreeMenu/, 'composer must render a worktree menu');
  assert.match(composerSource, /listWorktrees\(workspace\)/, 'menu must list worktrees for the workspace');
  assert.match(composerSource, /resetWorktree\(/, 'menu must expose reset');
  assert.match(composerSource, /removeWorktree\(/, 'menu must expose remove');
  assert.match(composerSource, /composer\.worktreeConfirmReset/, 'reset must confirm');
  assert.match(composerSource, /composer\.worktreeConfirmRemove/, 'remove must confirm');
});

test('worktree composer copy remains bilingual', () => {
  for (const key of ['composer.worktree', 'composer.worktreeCreate', 'composer.worktreeReady', 'composer.worktreeFailed']) {
    assert.equal(translations.split(`'${key}'`).length - 1, 2, `${key} must exist in both locales`);
  }
});
