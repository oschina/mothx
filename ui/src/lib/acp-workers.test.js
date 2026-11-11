import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const stores = await readFile(new URL('./stores.js', import.meta.url), 'utf8');
const page = await readFile(new URL('../views/settings/ACPWorkers.svelte', import.meta.url), 'utf8');
const preferences = await readFile(new URL('./preferences.js', import.meta.url), 'utf8');

test('ACP worker settings project the shared Runtime catalog and patch endpoint', () => {
  assert.match(stores, /\/api\/acp\/profiles/, 'the catalog must come from the Runtime projection');
  assert.match(stores, /method: 'PATCH'/, 'the profile edit must use the PATCH endpoint');
  assert.match(stores, /capabilityNames/, 'the page needs the Runtime-provided capability vocabulary');
  assert.match(page, /patchACPWorkerProfile/, 'the page must send the narrow patch');
  assert.doesNotMatch(page, /command|environment|url/i, 'the page must not manage argv, environment, or URLs');
});

test('ACP worker settings are bilingual', () => {
  for (const key of [
    'settings.tabs.acpWorkers',
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
  ]) {
    const occurrences = preferences.split(`'${key}'`).length - 1;
    assert.equal(occurrences, 2, `${key} must be present in both language maps`);
  }
});
