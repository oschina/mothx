// Knowledge base REST API helpers. Mirrors the backend contract added by the
// runtime team and keeps URL construction and payload shaping in one place so
// the view stays declarative.

import { request, postJSON, patchJSON, del } from './api.js';

export const DEFAULT_MODE = 'yolo';
export const DEFAULT_THINKING_LEVEL = 'off';
export const DEFAULT_PREPROCESS_PROFILE = 'documents';
export const DEFAULT_SCHEDULE = 'manual';

export function defaultKnowledgeBase() {
  return {
    name: '',
    rootDir: '',
    preprocessProfile: DEFAULT_PREPROCESS_PROFILE,
    provider: '',
    model: '',
    mode: DEFAULT_MODE,
    thinkingLevel: DEFAULT_THINKING_LEVEL,
    schedule: DEFAULT_SCHEDULE,
    enabled: true,
    ignoreGlobs: []
  };
}

export function emptyKnowledgeBaseView() {
  return {
    knowledgeBase: { id: '', ...defaultKnowledgeBase() },
    snapshot: null,
    status: 'unindexed',
    indexing: null
  };
}

// knowledgeBaseIndexing returns the live background scan projection when a
// scan is running, and null otherwise. The view polls while this is non-null.
export function knowledgeBaseIndexing(view) {
  const indexing = view?.indexing;
  return indexing && indexing.running ? indexing : null;
}

// knowledgeBaseIsIndexing reports whether any base currently has a scan in
// flight, which drives periodic progress polling in the knowledge view.
export function knowledgeBaseIsIndexing(views) {
  return Array.isArray(views) && views.some((view) => knowledgeBaseIndexing(view) !== null);
}

export async function listKnowledgeBases() {
  const data = await request('/api/knowledge-bases');
  return Array.isArray(data?.knowledgeBases)
    ? data.knowledgeBases.map(normalizeKnowledgeBaseView)
    : [];
}

export async function getKnowledgeBase(id) {
  return normalizeKnowledgeBaseView(await request(`/api/knowledge-bases/${encodeURIComponent(id)}`));
}

export async function createKnowledgeBase(knowledgeBase) {
  return normalizeKnowledgeBaseView(await postJSON('/api/knowledge-bases', { knowledgeBase }));
}

export async function updateKnowledgeBase(id, knowledgeBase) {
  return normalizeKnowledgeBaseView(await patchJSON(`/api/knowledge-bases/${encodeURIComponent(id)}`, { knowledgeBase }));
}

export async function deleteKnowledgeBase(id) {
  return del(`/api/knowledge-bases/${encodeURIComponent(id)}`);
}

export async function scanKnowledgeBase(id) {
  return normalizeKnowledgeBaseView(await postJSON(`/api/knowledge-bases/${encodeURIComponent(id)}/scan`, {}));
}

export async function queryKnowledgeBase(id, query, limit = 10) {
  return postJSON(`/api/knowledge-bases/${encodeURIComponent(id)}/query`, { query, limit });
}

export async function listKnowledgeBaseRuns(id, limit = 20) {
  const data = await request(`/api/knowledge-bases/${encodeURIComponent(id)}/runs?limit=${encodeURIComponent(limit)}`);
  return Array.isArray(data?.runs) ? data.runs.map(normalizeKnowledgeIndexRun) : [];
}

export async function getKnowledgeBaseRun(id, runId) {
  return normalizeKnowledgeIndexRun(await request(`/api/knowledge-bases/${encodeURIComponent(id)}/runs/${encodeURIComponent(runId)}`));
}

export async function listKnowledgeBaseSources(id) {
  const data = await request(`/api/knowledge-bases/${encodeURIComponent(id)}/sources`);
  return Array.isArray(data?.sources) ? data.sources.map(normalizeKnowledgeSource) : [];
}

export async function clearKnowledgeBase(id) {
  return postJSON(`/api/knowledge-bases/${encodeURIComponent(id)}/clear`, {});
}

// normalizeKnowledgeIndexRun keeps the run-history projection renderable even if
// a future field is added: the view only reads the bounded, known keys.
export function normalizeKnowledgeIndexRun(value) {
  if (!value || typeof value !== 'object') return null;
  return {
    runId: String(value.runId || ''),
    snapshotId: String(value.snapshotId || ''),
    status: String(value.status || 'unknown'),
    startedAt: value.startedAt || '',
    finishedAt: value.finishedAt || '',
    errorSummary: String(value.errorSummary || ''),
    fileCount: Number(value.fileCount || 0),
    chunkCount: Number(value.chunkCount || 0),
    nodeCount: Number(value.nodeCount || 0),
    edgeCount: Number(value.edgeCount || 0),
    active: Boolean(value.active)
  };
}

// normalizeKnowledgeSource mirrors the file-level provenance projection. It
// never carries file contents, only path/size/status/hit metadata.
export function normalizeKnowledgeSource(value) {
  if (!value || typeof value !== 'object') return null;
  return {
    path: String(value.path || value.relativePath || ''),
    mediaType: String(value.mediaType || ''),
    status: String(value.status || ''),
    byteSize: Number(value.byteSize || 0),
    chunkCount: Number(value.chunkCount || 0),
    title: String(value.title || '')
  };
}

export function normalizeKnowledgeBaseView(value) {
  if (!value || typeof value !== 'object') return emptyKnowledgeBaseView();
  const base = value.knowledgeBase && typeof value.knowledgeBase === 'object'
    ? value.knowledgeBase
    : value;
  return {
    ...emptyKnowledgeBaseView(),
    ...value,
    knowledgeBase: {
      ...defaultKnowledgeBase(),
      ...base,
      // The WebUI does not own a scheduler: reflect the real persisted cadence
      // instead of silently downgrading a Desktop schedule to manual.
      schedule: String(base.schedule || DEFAULT_SCHEDULE),
      ignoreGlobs: Array.isArray(base.ignoreGlobs) ? base.ignoreGlobs : []
    },
    snapshot: value.snapshot && typeof value.snapshot === 'object' ? value.snapshot : null,
    indexing: value.indexing && value.indexing.running ? { ...value.indexing } : null,
    status: String(value.status || value.snapshot?.status || 'unindexed')
  };
}

export function knowledgeBasePayload(draft) {
  return {
    name: String(draft.name || '').trim(),
    rootDir: String(draft.rootDir || '').trim(),
    preprocessProfile: String(draft.preprocessProfile || DEFAULT_PREPROCESS_PROFILE).trim(),
    provider: String(draft.provider || '').trim(),
    model: String(draft.model || '').trim(),
    mode: draft.mode || DEFAULT_MODE,
    thinkingLevel: draft.thinkingLevel || DEFAULT_THINKING_LEVEL,
    // Send the real draft cadence; the backend preserves an existing schedule
    // when this is empty so a WebUI save cannot downgrade a Desktop plan.
    schedule: String(draft.schedule || DEFAULT_SCHEDULE).trim(),
    enabled: draft.enabled !== false,
    ignoreGlobs: Array.isArray(draft.ignoreGlobs)
      ? draft.ignoreGlobs.map((glob) => String(glob).trim()).filter(Boolean)
      : []
  };
}

export function validateKnowledgeBase(payload) {
  const errors = [];
  if (!payload.name) errors.push('name is required');
  if (!payload.rootDir) errors.push('rootDir is required');
  const hasProvider = Boolean(payload.provider);
  const hasModel = Boolean(payload.model);
  if (hasProvider !== hasModel) {
    errors.push('provider and model must both be set or both be empty');
  }
  return errors;
}
