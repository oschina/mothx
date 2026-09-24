import test from 'node:test';
import assert from 'node:assert/strict';

const originalFetch = globalThis.fetch;

function installFetch(t, implementation) {
  globalThis.fetch = implementation;
  t.after(() => { globalThis.fetch = originalFetch; });
}

const {
  defaultKnowledgeBase,
  emptyKnowledgeBaseView,
  knowledgeBasePayload,
  validateKnowledgeBase,
  normalizeKnowledgeBaseView,
  knowledgeBaseIndexing,
  knowledgeBaseIsIndexing,
  listKnowledgeBases,
  getKnowledgeBase,
  createKnowledgeBase,
  updateKnowledgeBase,
  deleteKnowledgeBase,
  scanKnowledgeBase,
  queryKnowledgeBase,
  listKnowledgeBaseRuns,
  listKnowledgeBaseSources,
  clearKnowledgeBase,
  normalizeKnowledgeIndexRun,
  normalizeKnowledgeSource
} = await import('./knowledge-base.js');

test('defaultKnowledgeBase provides yolo defaults', () => {
  const draft = defaultKnowledgeBase();
  assert.equal(draft.mode, 'yolo');
  assert.equal(draft.thinkingLevel, 'off');
  assert.equal(draft.preprocessProfile, 'documents');
  assert.equal(draft.schedule, 'manual');
  assert.equal(draft.enabled, true);
  assert.equal(draft.name, '');
  assert.deepEqual(draft.ignoreGlobs, []);
});

test('emptyKnowledgeBaseView mirrors the backend view shape', () => {
  const view = emptyKnowledgeBaseView();
  assert.equal(view.knowledgeBase.mode, 'yolo');
  assert.equal(view.knowledgeBase.preprocessProfile, 'documents');
  assert.equal(view.status, 'unindexed');
  assert.equal(view.snapshot, null);
});

test('knowledgeBasePayload trims strings and preserves booleans', () => {
  const payload = knowledgeBasePayload({
    name: '  Docs  ',
    rootDir: ' /tmp/docs ',
    provider: ' openai ',
    model: 'gpt-4o',
    mode: 'agent',
    thinkingLevel: 'medium',
    schedule: ' @daily ',
    enabled: false
  });
  assert.equal(payload.name, 'Docs');
  assert.equal(payload.rootDir, '/tmp/docs');
  assert.equal(payload.provider, 'openai');
  assert.equal(payload.model, 'gpt-4o');
  assert.equal(payload.preprocessProfile, 'documents');
  assert.equal(payload.mode, 'agent');
  assert.equal(payload.schedule, '@daily');
  assert.equal(payload.enabled, false);
});

test('knowledgeBasePayload falls back to document defaults', () => {
  const payload = knowledgeBasePayload({});
  assert.equal(payload.preprocessProfile, 'documents');
  assert.equal(payload.schedule, 'manual');
  assert.equal(payload.mode, 'yolo');
  assert.equal(payload.thinkingLevel, 'off');
  assert.equal(payload.enabled, true);
});

test('knowledgeBasePayload trims ignore globs and drops empties', () => {
  const payload = knowledgeBasePayload({ name: 'Docs', rootDir: '/docs', ignoreGlobs: [' *.log ', '', 'tmp/**'] });
  assert.deepEqual(payload.ignoreGlobs, ['*.log', 'tmp/**']);
});

test('validateKnowledgeBase requires name and rootDir', () => {
  assert.deepEqual(validateKnowledgeBase(knowledgeBasePayload({})), [
    'name is required',
    'rootDir is required'
  ]);
});

test('validateKnowledgeBase accepts empty or paired provider/model', () => {
  assert.deepEqual(
    validateKnowledgeBase(knowledgeBasePayload({ name: 'x', rootDir: 'y', provider: '', model: '' })),
    []
  );
  assert.deepEqual(
    validateKnowledgeBase(knowledgeBasePayload({ name: 'x', rootDir: 'y', provider: 'p', model: 'm' })),
    []
  );
});

test('validateKnowledgeBase rejects a lone provider or model', () => {
  assert.deepEqual(
    validateKnowledgeBase(knowledgeBasePayload({ name: 'x', rootDir: 'y', provider: 'p', model: '' })),
    ['provider and model must both be set or both be empty']
  );
  assert.deepEqual(
    validateKnowledgeBase(knowledgeBasePayload({ name: 'x', rootDir: 'y', provider: '', model: 'm' })),
    ['provider and model must both be set or both be empty']
  );
});

test('normalizeKnowledgeBaseView fills missing fields with defaults', () => {
  const normalized = normalizeKnowledgeBaseView({ knowledgeBase: { id: 'kb-1', name: 'Project' } });
  assert.equal(normalized.knowledgeBase.id, 'kb-1');
  assert.equal(normalized.knowledgeBase.name, 'Project');
  assert.equal(normalized.knowledgeBase.mode, 'yolo');
  assert.equal(normalized.knowledgeBase.enabled, true);
  assert.equal(normalized.knowledgeBase.preprocessProfile, 'documents');
  assert.equal(normalized.knowledgeBase.schedule, 'manual');
  assert.deepEqual(normalized.knowledgeBase.ignoreGlobs, []);
  assert.equal(normalized.status, 'unindexed');
});

test('normalizeKnowledgeBaseView preserves a persisted Desktop schedule', () => {
  const normalized = normalizeKnowledgeBaseView({ knowledgeBase: { id: 'kb-1', name: 'A', schedule: 'daily' } });
  assert.equal(normalized.knowledgeBase.schedule, 'daily');
});

test('normalizeKnowledgeBaseView keeps a live indexing projection', () => {
  const normalized = normalizeKnowledgeBaseView({
    knowledgeBase: { id: 'kb-1', name: 'A' },
    status: 'indexing',
    indexing: { running: true, phase: 'scanning', filesDone: 3, filesTotal: 10 }
  });
  assert.equal(normalized.indexing.running, true);
  assert.equal(normalized.indexing.phase, 'scanning');
  assert.equal(normalized.indexing.filesTotal, 10);
  // A finished job projection must not masquerade as a running scan.
  assert.equal(normalizeKnowledgeBaseView({ indexing: { running: false } }).indexing, null);
  assert.equal(normalizeKnowledgeBaseView({}).indexing, null);
});

test('knowledgeBaseIndexing exposes only running scans', () => {
  assert.equal(knowledgeBaseIndexing(null), null);
  assert.equal(knowledgeBaseIndexing({}), null);
  assert.equal(knowledgeBaseIndexing({ indexing: { running: false } }), null);
  assert.deepEqual(
    knowledgeBaseIndexing({ indexing: { running: true, phase: 'indexing' } }),
    { running: true, phase: 'indexing' }
  );
});

test('knowledgeBaseIsIndexing drives polling for the whole list', () => {
  assert.equal(knowledgeBaseIsIndexing([]), false);
  assert.equal(knowledgeBaseIsIndexing(null), false);
  assert.equal(knowledgeBaseIsIndexing([{ indexing: { running: false } }]), false);
  assert.equal(knowledgeBaseIsIndexing([{ indexing: { running: true } }, {}]), true);
});

test('normalizeKnowledgeBaseView is safe for null and non-objects', () => {
  assert.equal(normalizeKnowledgeBaseView(null).knowledgeBase.mode, 'yolo');
  assert.equal(normalizeKnowledgeBaseView(undefined).knowledgeBase.mode, 'yolo');
  assert.equal(normalizeKnowledgeBaseView('string').knowledgeBase.mode, 'yolo');
});

test('listKnowledgeBases returns knowledgeBases array', async (t) => {
  installFetch(t, async () => new Response(JSON.stringify({
    knowledgeBases: [{ knowledgeBase: { id: 'kb-1', name: 'A' } }, { knowledgeBase: { id: 'kb-2', name: 'B' } }]
  }), { status: 200 }));

  const list = await listKnowledgeBases();
  assert.equal(list.length, 2);
  assert.equal(list[0].knowledgeBase.id, 'kb-1');
});

test('listKnowledgeBases falls back to empty array when field is missing', async (t) => {
  installFetch(t, async () => new Response(JSON.stringify({}), { status: 200 }));
  assert.deepEqual(await listKnowledgeBases(), []);
});

test('getKnowledgeBase encodes the id and returns the payload', async (t) => {
  installFetch(t, async (path) => {
    assert.equal(path, '/api/knowledge-bases/kb%2F1');
    return new Response(JSON.stringify({ knowledgeBase: { id: 'kb/1', name: 'Docs' } }), { status: 200 });
  });
  const result = await getKnowledgeBase('kb/1');
  assert.equal(result.knowledgeBase.name, 'Docs');
});

test('createKnowledgeBase wraps draft under knowledgeBase key', async (t) => {
  let body;
  installFetch(t, async (_path, options) => {
    body = JSON.parse(options.body);
    assert.equal(options.method, 'POST');
    assert.equal(new Headers(options.headers).get('content-type'), 'application/json');
    return new Response(JSON.stringify({ knowledgeBase: { id: 'kb-3', name: 'Docs' } }), { status: 201 });
  });
  const result = await createKnowledgeBase({ name: 'Docs', rootDir: '/docs', provider: 'x', model: 'y' });
  assert.deepEqual(body, { knowledgeBase: { name: 'Docs', rootDir: '/docs', provider: 'x', model: 'y' } });
  assert.equal(result.knowledgeBase.id, 'kb-3');
});

test('updateKnowledgeBase uses PATCH and encodes id', async (t) => {
  let path;
  installFetch(t, async (p, options) => {
    path = p;
    assert.equal(options.method, 'PATCH');
    return new Response(JSON.stringify({ knowledgeBase: { id: 'kb-4' } }), { status: 200 });
  });
  await updateKnowledgeBase('kb 4', { name: 'Updated' });
  assert.equal(path, '/api/knowledge-bases/kb%204');
});

test('deleteKnowledgeBase uses DELETE and encodes id', async (t) => {
  let path;
  installFetch(t, async (p, options) => {
    path = p;
    assert.equal(options.method, 'DELETE');
    return new Response(null, { status: 204 });
  });
  await deleteKnowledgeBase('kb&1');
  assert.equal(path, '/api/knowledge-bases/kb%261');
});

test('scanKnowledgeBase posts to the scan endpoint', async (t) => {
  let path;
  installFetch(t, async (p, options) => {
    path = p;
    assert.equal(options.method, 'POST');
    return new Response(JSON.stringify({ ok: true }), { status: 202 });
  });
  await scanKnowledgeBase('kb-1');
  assert.equal(path, '/api/knowledge-bases/kb-1/scan');
});

test('queryKnowledgeBase posts query and bounded limit', async (t) => {
  let body;
  installFetch(t, async (_path, options) => {
    body = JSON.parse(options.body);
    return new Response(JSON.stringify({ query: { chunks: [{ content: 'hello' }] } }), { status: 200 });
  });
  const result = await queryKnowledgeBase('kb-1', 'how do I', 3);
  assert.equal(body.query, 'how do I');
  assert.equal(body.limit, 3);
  assert.equal(result.query.chunks.length, 1);
});

test('listKnowledgeBaseRuns projects the run history', async (t) => {
  let path;
  installFetch(t, async (p) => {
    path = p;
    return new Response(JSON.stringify({ runs: [
      { runId: 'knowledge_index_1', status: 'completed', fileCount: 3, nodeCount: 9, active: true },
      { runId: 'knowledge_index_0', status: 'failed', errorSummary: 'boom' }
    ] }), { status: 200 });
  });
  const runs = await listKnowledgeBaseRuns('kb/1', 5);
  assert.equal(path, '/api/knowledge-bases/kb%2F1/runs?limit=5');
  assert.equal(runs.length, 2);
  assert.equal(runs[0].runId, 'knowledge_index_1');
  assert.equal(runs[0].fileCount, 3);
  assert.equal(runs[0].active, true);
  assert.equal(runs[1].status, 'failed');
  assert.equal(runs[1].errorSummary, 'boom');
});

test('listKnowledgeBaseSources projects file provenance without contents', async (t) => {
  let path;
  installFetch(t, async (p) => {
    path = p;
    return new Response(JSON.stringify({ sources: [
      { relativePath: 'docs/a.md', status: 'indexed', byteSize: 2048, chunkCount: 4, title: 'A' }
    ] }), { status: 200 });
  });
  const sources = await listKnowledgeBaseSources('kb-1');
  assert.equal(path, '/api/knowledge-bases/kb-1/sources');
  assert.equal(sources.length, 1);
  assert.equal(sources[0].path, 'docs/a.md');
  assert.equal(sources[0].chunkCount, 4);
  assert.equal(sources[0].byteSize, 2048);
});

test('clearKnowledgeBase posts to the clear endpoint', async (t) => {
  let path;
  installFetch(t, async (p, options) => {
    path = p;
    assert.equal(options.method, 'POST');
    return new Response(JSON.stringify({ cleared: true }), { status: 200 });
  });
  await clearKnowledgeBase('kb-1');
  assert.equal(path, '/api/knowledge-bases/kb-1/clear');
});

test('normalizeKnowledgeIndexRun and normalizeKnowledgeSource are null-safe', () => {
  assert.equal(normalizeKnowledgeIndexRun(null), null);
  assert.equal(normalizeKnowledgeSource('nope'), null);
  assert.equal(normalizeKnowledgeIndexRun({ runId: 'r' }).status, 'unknown');
  assert.equal(normalizeKnowledgeSource({}).chunkCount, 0);
});
