import test from 'node:test';
import assert from 'node:assert/strict';

import {
  createRouterSelectionManager,
  normalizeWorkerCatalog,
  selectableWorkers,
  validateWorkerSelection
} from './router-selection.js';

const CATALOG = [
  { id: 'codex', title: 'Codex', enabled: true, routerEligible: true, maxConcurrentChildren: 3 },
  { id: 'reviewer', enabled: true, routerEligible: true },
  { id: 'helper', enabled: false, routerEligible: true },
  { id: 'interactive', enabled: true, routerEligible: false },
  { id: 'broken', enabled: true, routerEligible: false, invalidReason: 'empty command[0]' }
];

test('normalizeWorkerCatalog keeps the invalid reason and drops unusable rows', () => {
  const normalized = normalizeWorkerCatalog(CATALOG);
  assert.equal(normalized.length, 5);
  const broken = normalized.find((worker) => worker.id === 'broken');
  assert.equal(broken.invalidReason, 'empty command[0]');
  assert.equal('roles' in normalized[0], false);
  assert.equal(normalizeWorkerCatalog(null).length, 0);
  assert.equal(normalizeWorkerCatalog([{ title: 'no id' }]).length, 0);
});

test('selectableWorkers offers only enabled and eligible workers', () => {
  const ids = selectableWorkers(CATALOG).map((worker) => worker.id);
  assert.deepEqual(ids, ['codex', 'reviewer']);
});

test('validateWorkerSelection refuses an empty or unknown selection', () => {
  assert.equal(validateWorkerSelection(CATALOG, []).ok, false);
  assert.equal(validateWorkerSelection(CATALOG, ['codex', 'helper']).ok, false);
  assert.equal(validateWorkerSelection(CATALOG, ['nope']).ok, false);
  const valid = validateWorkerSelection(CATALOG, ['codex']);
  assert.equal(valid.ok, true);
  assert.deepEqual(valid.profileIds, ['codex']);
});

test('the manager loads the catalog and publishes selectable workers', async () => {
  const states = [];
  const manager = createRouterSelectionManager({
    listWorkers: async () => CATALOG,
    activate: async () => ({ enabled: true }),
    deactivate: async () => ({ enabled: false }),
    onState: (state) => states.push(state)
  });

  await manager.load();
  const last = states[states.length - 1];
  assert.equal(last.loading, false);
  assert.deepEqual(last.selectable.map((worker) => worker.id), ['codex', 'reviewer']);
});

test('the manager drops a selection that a reload no longer offers', async () => {
  let catalog = CATALOG;
  const manager = createRouterSelectionManager({
    listWorkers: async () => catalog,
    activate: async () => ({ enabled: true }),
    deactivate: async () => ({ enabled: false }),
    onState: () => {}
  });
  await manager.load();
  manager.toggle('codex');
  catalog = CATALOG.map((worker) => (worker.id === 'codex' ? { ...worker, enabled: false } : worker));
  await manager.load();
  let state = null;
  const observer = createRouterSelectionManager({
    listWorkers: async () => catalog,
    activate: async () => ({ enabled: true }),
    deactivate: async () => ({ enabled: false }),
    onState: (next) => { state = next; }
  });
  await observer.load();
  assert.deepEqual(observer ? state.selection : null, []);
});

test('activating sends the validated worker list to the server', async () => {
  const activated = [];
  const manager = createRouterSelectionManager({
    listWorkers: async () => CATALOG,
    activate: async (profileIds) => {
      activated.push(profileIds);
      return { enabled: true, profileIds };
    },
    deactivate: async () => ({ enabled: false }),
    onState: () => {}
  });
  await manager.load();
  manager.toggle('codex');
  manager.toggle('reviewer');
  const result = await manager.activate();
  assert.equal(result.ok, true);
  assert.deepEqual(activated[0], ['codex', 'reviewer']);
});

test('activating without a selection reports an error instead of calling the server', async () => {
  let called = false;
  const manager = createRouterSelectionManager({
    listWorkers: async () => CATALOG,
    activate: async () => { called = true; return {}; },
    deactivate: async () => ({}),
    onState: () => {}
  });
  await manager.load();
  const result = await manager.activate();
  assert.equal(result.ok, false);
  assert.equal(called, false);
});

test('a failed activation surfaces the server error', async () => {
  const states = [];
  const manager = createRouterSelectionManager({
    listWorkers: async () => CATALOG,
    activate: async () => { throw new Error('session is busy'); },
    deactivate: async () => ({}),
    onState: (state) => states.push(state)
  });
  await manager.load();
  manager.toggle('codex');
  const result = await manager.activate();
  assert.equal(result.ok, false);
  assert.match(result.error, /busy/);
  assert.match(states[states.length - 1].error, /busy/);
});
