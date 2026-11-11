import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const manageApi = await readFile(new URL('../../core/manage-api.ts', import.meta.url), 'utf8');
const translations = await readFile(new URL('../../core/i18n.ts', import.meta.url), 'utf8');
const composerTsx = await readFile(new URL('../../components/Composer.tsx', import.meta.url), 'utf8');

function assertBilingual(keys: string[]): void {
  for (const key of keys) {
    const occurrences = translations.split(`'${key}'`).length - 1;
    assert.equal(occurrences, 2, `${key} must be present in both translation maps`);
  }
}

test('the Router selection goes through the ACP management projection', () => {
  for (const method of [
    'mothx/manage/acp/profiles/list',
    'mothx/manage/acp/profiles/patch',
    'mothx/worker/messages',
    'mothx/manage/router/state',
    'mothx/manage/router/activate',
    'mothx/manage/router/deactivate',
  ]) {
    assert.match(manageApi, new RegExp(method.replaceAll('/', '\\/')), `${method} must be used through ACP`);
  }
  // Desktop is a pure ACP projection: it must not persist a Router selection
  // in Electron-owned storage or read the settings file itself.
  assert.doesNotMatch(manageApi, /desktop\.storeSet\([^)]*router/i, 'the Router selection must not enter the Desktop store');
  assert.doesNotMatch(composerTsx, /desktop\.storeSet|localStorage/i, 'the composer must not persist the Router selection locally');
});

test('the Router control is gated on the manageRouter feature key', () => {
  assert.match(manageApi, /hasFeature\('manageRouter'\)/, 'the Router projection must be feature-gated');
  assert.match(composerTsx, /hasFeature\('manageRouter'\)/, 'the Router control must be feature-gated');
  // Every guarded call must return a safe fallback instead of calling a method
  // the host may not implement.
  assert.match(manageApi, /if \(!hasFeature\('manageRouter'\)[^)]*\) return \[\];/, 'the catalog must fall back to an empty list');
  assert.match(manageApi, /if \(!hasFeature\('manageRouter'\) \|\| !sessionId\) return undefined;/, 'the Router calls must fall back to undefined');
});

test('the Router labels are bilingual', () => {
  assertBilingual([
    'composer.routerWorker',
    'composer.routerNone',
    'composer.routerSelectWorker',
    'composer.routerMode',
    'composer.routerActivate',
    'composer.routerLeave',
    'composer.routerParallel',
    'composer.routerWorkerMessages',
    'composer.routerPendingQuestions',
    'composer.routerLeadOnly',
    'settings.tab.acpWorkers',
    'settings.acpWorkers.hint',
    'settings.acpWorkers.model',
    'settings.acpWorkers.modelPlaceholder',
    'settings.acpWorkers.driver',
    'settings.acpWorkers.bridge',
    'settings.acpWorkers.overrides',
    'settings.acpWorkers.save',
    'settings.acpWorkers.saved',
    'settings.acpWorkers.saveFailed',
    'settings.acpWorkers.empty',
    'settings.acpWorkers.loading',
    'settings.acpWorkers.unavailable',
  ]);
});

test('the Router control is labeled as a beta mode', () => {
  // Router is still an evaluation feature: the composer must say so instead of
  // presenting it as a production mode.
  for (const label of ['Router (beta)', 'Router（Beta）']) {
    assert.ok(
      translations.includes(label),
      `the Router mode label must be marked as a beta mode: ${label}`,
    );
  }
  assert.match(composerTsx, /t\('composer\.routerMode'\)/, 'the Router button must use the labeled mode text');
});

test('worker activity is a read-only ACP projection', () => {
  assert.match(manageApi, /hasFeature\('workerMessages'\)/, 'worker activity must be feature-gated');
  assert.match(manageApi, /hasFeature\('manageACPProfiles'\)/, 'the profile editor must be feature-gated');
  assert.match(manageApi, /capabilityOverrides/, 'the profile patch must expose capability tightening');
  assert.match(manageApi, /model\?: string/, 'the profile patch must expose the worker model');
  assert.doesNotMatch(manageApi, /command:.*acpAgents|acpAgents.*command/, 'the renderer must not manage argv');
});

test('the composer offers only enabled and eligible workers', () => {
  assert.match(
    composerTsx,
    /routerWorkers\.filter\(\(worker\) => worker\.enabled && worker\.routerEligible\)/,
    'the worker list must exclude disabled and ineligible workers',
  );
});
