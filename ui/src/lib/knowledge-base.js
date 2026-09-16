// Knowledge base REST API helpers. Mirrors the backend contract added by the
// runtime team and keeps URL construction and payload shaping in one place so
// the view stays declarative.

import { request, postJSON, patchJSON, del } from './api.js';

export const DEFAULT_MODE = 'yolo';
export const DEFAULT_THINKING_LEVEL = 'none';
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
    enabled: true
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
      schedule: DEFAULT_SCHEDULE
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
    schedule: DEFAULT_SCHEDULE,
    enabled: draft.enabled !== false
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
