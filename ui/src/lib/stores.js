// Shared reactive stores. Views subscribe; a small refresh helper reloads
// everything after significant server-state changes.

import { writable, derived, readable, get } from 'svelte/store';
import { request, jsonBody } from './api.js';
import { emptyTrajectoryState } from './trajectory/reducer.js';

// Reactive media-query store: true when viewport is mobile-width.
export const isMobile = readable(false, (set) => {
  if (typeof window === 'undefined') return;
  const mql = window.matchMedia('(max-width: 900px)');
  set(mql.matches);
  const handler = (e) => set(e.matches);
  mql.addEventListener('change', handler);
  return () => mql.removeEventListener('change', handler);
});

export const health = writable(null);
export const status = writable(null);
export const capabilities = writable(null);
export const channels = writable([]);
export const sessions = writable([]);
export const sessionBindings = writable([]);
// Server-resolved model catalog (GET /api/models/catalog). The server builds
// it with the same provider-factory logic the TUI uses, so the WebUI picker
// must not re-derive model lists from raw settings JSON.
export function emptyModelCatalog() {
  return { providers: [], models: [], modelDefaults: {}, defaultProvider: '', defaultModel: '' };
}
export const modelCatalog = writable(emptyModelCatalog());
export const cronInfo = writable(null);
export const serveConfig = writable('');
export const settings = writable('');
export const memoryInfo = writable(null);
export const memory = writable('');
export const logs = writable([]);
export const logsConnected = writable(false);
export const statsSummary = writable(null);
export const notice = writable('');
export const error = writable('');
export const currentSession = writable('');
export const sidebarOpen = writable(false);
const sidebarCollapsedStorageKey = 'mothx.sidebar.collapsed';
function readSidebarCollapsed() {
  if (typeof window === 'undefined') return false;
  try { return window.localStorage.getItem(sidebarCollapsedStorageKey) === '1'; } catch { return false; }
}
export const sidebarCollapsed = writable(readSidebarCollapsed());
if (typeof window !== 'undefined') {
  sidebarCollapsed.subscribe((value) => {
    try { window.localStorage.setItem(sidebarCollapsedStorageKey, value ? '1' : '0'); } catch { /* storage is optional */ }
  });
}
export const selectedModel = writable('default');
export const sessionRuntime = writable(null);
export const pendingApprovals = derived(sessionRuntime, ($runtime) => $runtime?.pendingApprovals || []);
export const activeApproval = writable(null);
export const approvalHistory = writable([]);
export const toolEvents = writable([]);
export const trajectoryState = writable({});
export const trajectoryViewState = writable({});
export const sessionExportState = writable({});

const emptyTrajectoryViewState = {
  query: '',
  kindFilter: 'all',
  statusFilter: 'all',
  selectedID: '',
  collapsed: [],
  detailWidth: 380
};

export function getTrajectoryState(id) {
  if (!id) return emptyTrajectoryState();
  return get(trajectoryState)[id] || emptyTrajectoryState();
}

export function setTrajectoryState(id, state) {
  if (!id) return;
  trajectoryState.update((all) => ({ ...all, [id]: state }));
}

export function getTrajectoryViewState(id) {
  if (!id) return { ...emptyTrajectoryViewState };
  return { ...emptyTrajectoryViewState, ...(get(trajectoryViewState)[id] || {}) };
}

export function setTrajectoryViewState(id, state) {
  if (!id) return;
  trajectoryViewState.update((all) => ({ ...all, [id]: { ...emptyTrajectoryViewState, ...(state || {}) } }));
}

export function clearTrajectoryState(id) {
  if (!id) return;
  trajectoryState.update((all) => {
    const next = { ...all };
    delete next[id];
    return next;
  });
}

export function getSessionExportState(id) {
  if (!id) return null;
  return get(sessionExportState)[id] || null;
}

export function setSessionExportState(id, state) {
  if (!id) return;
  sessionExportState.update((all) => ({ ...all, [id]: state }));
}

const sessionToolStorageKey = 'mothx.webui.sessionTools';
const defaultSessionTools = {
  webSearch: false,
  browser: false,
  a2aMaster: false,
  delegate: false,
  multiAgent: false,
  workflows: false
};
export const sessionToolOptions = writable(loadSessionToolOptions());

export const features = derived(status, ($s) => ({
  api: $s?.features?.openaiAPI !== false,
  webUI: $s?.features?.webUI !== false,
  cron: $s?.features?.cron !== false,
  memory: $s?.features?.memory !== false,
  multiAgent: $s?.features?.multiAgent === true,
  delegate: $s?.features?.delegate === true,
  webSearch: $s?.features?.webSearch === true,
  browser: $s?.features?.browser === true,
  a2aMaster: $s?.features?.a2aMaster === true,
  workflows: $s?.features?.workflows === true
}));

export const connectedChannels = derived(channels, ($c) =>
  $c.filter((ch) => ch.connected).length
);

export function setError(err) {
  error.set(err instanceof Error ? err.message : String(err || ''));
}

export function setNotice(msg) {
  notice.set(msg || '');
}

export function clearBanners() {
  error.set('');
  notice.set('');
}

let logsSocket = null;
let runsSocket = null;
let wsEventSeq = 0;
let logsReconnectTimer = 0;
let logsReconnectAttempt = 0;
let logsClosing = false;
let runsReconnectTimer = 0;
let runsReconnectAttempt = 0;
let runsClosing = false;
export const runsConnected = writable(false);
export const runEvents = writable([]);
export const runCursors = writable({});

function runSubscriptions() {
  const cursors = get(runCursors);
  return get(sessions).filter((item) => item?.id).map((item) => ({
    sessionId: item.id,
    cursor: cursors[item.id] || { entrySeq: 0, runSeq: 0, capabilitySeq: 0 }
  }));
}

// Tracks session IDs the open socket has already subscribed to, so sessions
// loaded after the socket opens (or added later) still get subscribed without
// re-subscribing — and replaying — every session on each list change.
const subscribedRunSessionIDs = new Set();

function sendRunSubscriptionBatch(list) {
  if (runsSocket?.readyState !== WebSocket.OPEN || !list.length) return;
  list.forEach((item) => subscribedRunSessionIDs.add(item.sessionId));
  runsSocket.send(JSON.stringify({ type: 'subscribe', subscriptions: list }));
}

// syncRunSubscriptions subscribes the open socket to sessions it has not
// subscribed to yet. Existing subscriptions keep their server-side cursor.
export function syncRunSubscriptions() {
  sendRunSubscriptionBatch(runSubscriptions().filter((item) => !subscribedRunSessionIDs.has(item.sessionId)));
}

function scheduleRunsReconnect() {
  if (runsClosing || runsReconnectTimer || typeof window === 'undefined') return;
  const delay = Math.min(1000 * (2 ** runsReconnectAttempt), 15000);
  runsReconnectAttempt += 1;
  runsReconnectTimer = window.setTimeout(() => {
    runsReconnectTimer = 0;
    connectRuns();
  }, delay);
}

function reportRunsSocketDiagnostic(message) {
  const text = `[runs websocket] ${message}`;
  console.warn(text);
  try {
    globalThis.__MOTHX_DESKTOP__?.logDiagnostic?.(text);
  } catch {
    // Diagnostics are best-effort and must not affect reconnection.
  }
}

export function connectRuns() {
  if (runsSocket || typeof window === 'undefined') return;
  runsClosing = false;
  const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws';
  runsSocket = new WebSocket(`${scheme}://${window.location.host}/ws/runs`);
  runsSocket.onopen = () => {
    runsConnected.set(true);
    reportRunsSocketDiagnostic(`connected to ${runsSocket.url}`);
    runsReconnectAttempt = 0;
    subscribedRunSessionIDs.clear();
    runsSocket.send(JSON.stringify({ type: 'hello', protocol: 1, clientId: `webui-${Date.now()}` }));
    sendRunSubscriptionBatch(runSubscriptions());
  };
  runsSocket.onmessage = (event) => {
    try {
      const item = JSON.parse(event.data);
      if (item.type === 'error' && item.sessionId) {
        // The server rejected this subscription — clear the dedupe mark so
        // the next sync retries instead of skipping the session forever.
        subscribedRunSessionIDs.delete(item.sessionId);
        console.warn('runs subscription rejected:', item.sessionId, item.data);
        return;
      }
      if (item.type === 'subscribed' || item.type === 'ready') return;
      if (item.type === 'session_event' && item.event === 'title_updated') {
        const title = String(item.data?.title || '').trim();
        if (item.sessionId && title) upsertSession({ id: item.sessionId, title });
        return;
      }
      if (item.type === 'session_event' || item.type === 'run_state') {
        wsEventSeq += 1;
        runEvents.update((prev) => [...prev.slice(-999), { ...item, wsSeq: wsEventSeq }]);
        if (item.type === 'session_event' && item.event === 'runtime_event') {
          applyRuntimeSnapshotToSession(item.sessionId, item.data);
        }
        // Reconnect cursors address SQLite tables, not the EventBroker. Only
        // persisted/replayed frames carry the relevant table cursor in data.seq;
        // using item.seq here would mix in-memory broker sequence IDs with
        // SQLite sequence IDs and could skip durable history after reconnect.
        const persistedSeq = Number(item.data?.seq || 0);
        if (item.sessionId && persistedSeq > 0) {
          runCursors.update((all) => {
            const current = all[item.sessionId] || { entrySeq: 0, runSeq: 0, capabilitySeq: 0 };
            const key = item.stream === 'transcript' ? 'entrySeq' : item.stream === 'capability' ? 'capabilitySeq' : 'runSeq';
            return { ...all, [item.sessionId]: { ...current, [key]: Math.max(current[key] || 0, persistedSeq) } };
          });
        }
      }
    } catch {}
  };
  runsSocket.onclose = (event) => {
    reportRunsSocketDiagnostic(`closed (code=${event?.code ?? 'unknown'}, reason=${event?.reason || 'none'}); reconnecting`);
    runsConnected.set(false);
    runsSocket = null;
    subscribedRunSessionIDs.clear();
    scheduleRunsReconnect();
  };
  runsSocket.onerror = () => {
    reportRunsSocketDiagnostic(`connection error for ${runsSocket?.url || 'unknown URL'}`);
    runsConnected.set(false);
  };
}

export function disconnectRuns() {
  runsClosing = true;
  if (runsReconnectTimer) {
    clearTimeout(runsReconnectTimer);
    runsReconnectTimer = 0;
  }
  if (runsSocket) runsSocket.close();
  runsSocket = null;
  runsConnected.set(false);
}


function scheduleLogsReconnect() {
  if (logsClosing || logsReconnectTimer || typeof window === 'undefined') return;
  const delay = Math.min(1000 * (2 ** logsReconnectAttempt), 15000);
  logsReconnectAttempt += 1;
  logsReconnectTimer = window.setTimeout(() => {
    logsReconnectTimer = 0;
    connectLogs();
  }, delay);
}

function reportLogsSocketDiagnostic(message) {
  const text = `[logs websocket] ${message}`;
  console.warn(text);
  try {
    globalThis.__MOTHX_DESKTOP__?.logDiagnostic?.(text);
  } catch {
    // Diagnostics are best-effort and must not affect reconnection.
  }
}

export function connectLogs() {
  if (logsSocket || typeof window === 'undefined') return;
  logsClosing = false;
  const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws';
  logsSocket = new WebSocket(`${scheme}://${window.location.host}/ws/logs`);
  logsSocket.onopen = () => {
    logsConnected.set(true);
    logsReconnectAttempt = 0;
    reportLogsSocketDiagnostic(`connected to ${logsSocket.url}`);
  };
  logsSocket.onmessage = (event) => {
    try {
      const item = JSON.parse(event.data);
      if (item.type === 'heartbeat') return;
      logs.update((prev) => [...prev.slice(-199), item]);
      if (item.type === 'connected' || item.type === 'config_changed' || item.type === 'channel_status_changed') {
        if (item.status) {
          status.set(item.status);
          channels.set(item.status.channels || []);
        }
      }
      if (['config_changed', 'channel_config_changed', 'channel_status_changed', 'binding_changed', 'session_deleted', 'channel_tools_changed'].includes(item.type)) {
        refreshAll();
      }
    } catch {
      logs.update((prev) => [...prev.slice(-199), { type: 'log', message: event.data }]);
    }
  };
  logsSocket.onclose = (event) => {
    reportLogsSocketDiagnostic(`closed (code=${event?.code ?? 'unknown'}, reason=${event?.reason || 'none'}); reconnecting`);
    logsConnected.set(false);
    logsSocket = null;
    scheduleLogsReconnect();
  };
  logsSocket.onerror = () => {
    reportLogsSocketDiagnostic(`connection error for ${logsSocket?.url || 'unknown URL'}`);
    logsConnected.set(false);
  };
}

export function disconnectLogs() {
  logsClosing = true;
  if (logsReconnectTimer) {
    clearTimeout(logsReconnectTimer);
    logsReconnectTimer = 0;
  }
  if (logsSocket) logsSocket.close();
  logsSocket = null;
  logsConnected.set(false);
}

export async function refreshAll() {
  error.set('');
  const endpoints = [
    ['health', '/health'],
    ['status', '/api/status'],
    ['capabilities', '/api/capabilities'],
    ['channels', '/api/channels'],
    ['sessions', '/api/sessions?limit=100'],
    ['cron', '/api/cron'],
    ['serveConfig', '/api/serve/config'],
    ['settings', '/api/settings'],
    ['memory', '/api/memory'],
    ['bindings', '/api/session-bindings']
  ];
  const results = await Promise.all(endpoints.map(async ([key, path]) => {
    try {
      return [key, { ok: true, value: await request(path) }];
    } catch (err) {
      return [key, { ok: false, error: err }];
    }
  }));
  const loaded = Object.fromEntries(results);
  const failures = [];

  for (const [key, result] of Object.entries(loaded)) {
    if (!result.ok) failures.push(`${key}: ${result.error?.message || result.error}`);
  }
  if (loaded.health?.ok) health.set(loaded.health.value);
  if (loaded.status?.ok) status.set(loaded.status.value);
  if (loaded.capabilities?.ok) capabilities.set(loaded.capabilities.value);
  if (loaded.channels?.ok) channels.set(loaded.channels.value || []);
  if (loaded.sessions?.ok) {
    const list = loaded.sessions.value?.sessions || [];
    sessions.set(sortSessions(list.map(normalizeSessionListEntry)));
  }
  if (loaded.bindings?.ok) sessionBindings.set(loaded.bindings.value?.bindings || []);
  if (loaded.cron?.ok) cronInfo.set(loaded.cron.value);
  if (loaded.serveConfig?.ok) serveConfig.set(JSON.stringify(loaded.serveConfig.value, null, 2));
  if (loaded.settings?.ok) settings.set(JSON.stringify(loaded.settings.value, null, 2));
  if (loaded.memory?.ok) {
    memoryInfo.set(loaded.memory.value);
    memory.set(loaded.memory.value?.content || '');
  }

  await Promise.all([refreshModelCatalog(), refreshStatsSummary()]);
  if (failures.length > 0) setError(new Error(`Some data could not be refreshed: ${failures.join('; ')}`));
}

// Runtime snapshots arrive on the runs WebSocket for every subscribed session,
// not only the chat currently open in the UI. Project their execution state
// directly into the shared list so historical rows do not retain a stale
// running bit. Older servers may omit execution, in which case the existing
// compatible list entry remains untouched.
function applyRuntimeSnapshotToSession(sessionID, snapshot) {
  const execution = snapshot?.execution;
  if (!sessionID || !execution) return;
  sessions.update((items) => {
    const index = (items || []).findIndex((item) => item?.id === sessionID);
    if (index < 0) return items;
    const next = items.slice();
    next[index] = {
      ...next[index],
      running: Boolean(execution.running),
      execution
    };
    return next;
  });
}

export async function refreshSessions() {
  // Use the paginated endpoint so sidebar refreshes receive the same
  // persisted project/pinned metadata as the Sessions view.
  const data = await request('/api/sessions?limit=100&offset=0');
  sessions.set(sortSessions((data?.sessions || []).map(normalizeSessionListEntry)));
  // Only subscribe sessions the socket has not subscribed yet — re-sending the
  // full list would make the server cancel and replay every subscription.
  syncRunSubscriptions();
  const bindingData = await request('/api/session-bindings');
  sessionBindings.set(bindingData?.bindings || []);
}

// Keep the runs socket subscribed as the sessions list loads and changes.
// This covers the page-load race where the socket opens before /api/sessions
// returns — without it, sending a message in an existing session never
// receives run events and the UI waits forever.
sessions.subscribe(() => syncRunSubscriptions());

export function upsertSession(session) {
  if (!session?.id) return;
  sessions.update((items) => {
    const incoming = normalizeSessionListEntry(session);
    const idx = (items || []).findIndex((item) => item?.id === incoming.id);
    let next;
    if (idx >= 0) {
      next = items.slice();
      next[idx] = { ...next[idx], ...incoming };
    } else {
      next = [incoming, ...(items || [])];
    }
    return sortSessions(next);
  });
}

function normalizeSessionListEntry(session) {
  return {
    ...session,
    lastUsed: session.lastUsed || new Date().toISOString(),
    messageCount: Number(session.messageCount || 0)
  };
}

function sortSessions(items = []) {
  return [...items].sort((a, b) => {
    // Pinned sessions must remain visible after refresh, even when they are
    // older than the recent-session window shown in the sidebar.
    const pinnedDelta = Number(Boolean(b?.pinned)) - Number(Boolean(a?.pinned));
    if (pinnedDelta !== 0) return pinnedDelta;
    const left = Date.parse(a?.lastUsed || '') || 0;
    const right = Date.parse(b?.lastUsed || '') || 0;
    if (left === right) return String(a?.id || '').localeCompare(String(b?.id || ''));
    return right - left;
  });
}

export async function refreshStatsSummary() {
  try {
    statsSummary.set(await request('/api/stats/summary'));
  } catch {
    statsSummary.set(null);
  }
}

function statsQuery(params = {}) {
  const q = new URLSearchParams();
  for (const [key, value] of Object.entries(params || {})) {
    if (value !== undefined && value !== null && value !== '') q.set(key, value);
  }
  const query = q.toString();
  return query ? `?${query}` : '';
}

export async function getStatsSummary(params = {}) {
  return request(`/api/stats/summary${statsQuery(params)}`);
}

export async function getStatsTimeSeries(params = {}) {
  return request(`/api/stats/timeseries${statsQuery(params)}`);
}

export async function getStatsByProvider(params = {}) {
  return request(`/api/stats/by-provider${statsQuery(params)}`);
}

export async function getStatsByModel(params = {}) {
  return request(`/api/stats/by-model${statsQuery(params)}`);
}

export async function getStatsRecent(params = {}) {
  return request(`/api/stats/recent${statsQuery(params)}`);
}

export async function getSessionMessages(id) {
  if (!id) return []; // default session with no messages endpoint
  const data = await request(`/api/sessions/${encodeURIComponent(id)}/messages`);
  return data?.messages || [];
}

export async function getSessionMessagesLatest(id, limit = 50) {
  if (!id) return { messages: [], hasMore: false };
  const data = await request(`/api/sessions/${encodeURIComponent(id)}/messages?limit=${limit}`);
  return { messages: data?.messages || [], hasMore: data?.hasMore === true };
}

export async function getSessionMessagesBefore(id, beforeSeq, limit = 50) {
  if (!id) return { messages: [], hasMore: false };
  const data = await request(`/api/sessions/${encodeURIComponent(id)}/messages?before=${beforeSeq}&limit=${limit}`);
  return { messages: data?.messages || [], hasMore: data?.hasMore === true };
}

export async function getSessionToolResult(id, toolCallID) {
  if (!id || !toolCallID) return null;
  return request(
    `/api/sessions/${encodeURIComponent(id)}/tool-results/${encodeURIComponent(toolCallID)}`
  );
}

export async function getSessionSubAgents(id) {
  if (!id) return [];
  const data = await request(`/api/sessions/${encodeURIComponent(id)}/subagents`);
  return data?.subagents || [];
}

export async function getSessionSubAgentMessages(id, agentID) {
  if (!id || !agentID) return [];
  const data = await request(
    `/api/sessions/${encodeURIComponent(id)}/subagents/${encodeURIComponent(agentID)}/messages`
  );
  return data?.messages || [];
}

export async function getSessionRunEvents(id) {
  if (!id) return [];
  const data = await request(`/api/sessions/${encodeURIComponent(id)}/run-events`);
  return data?.events || [];
}

export async function getSessionCapabilityEvents(id) {
  if (!id) return [];
  const data = await request(`/api/sessions/${encodeURIComponent(id)}/capability-events`);
  return data?.events || [];
}

export async function getSessionTrajectory(id, before = '', limit = 200) {
  if (!id) return { records: [], highWater: {}, hasMore: false };
  const query = new URLSearchParams({ limit: String(limit) });
  if (before) query.set('before', before);
  return request(`/api/sessions/${encodeURIComponent(id)}/trajectory?${query.toString()}`);
}

export async function getSessionRuntime(id) {
  if (!id) return null;
  return request(`/api/sessions/${encodeURIComponent(id)}/runtime`);
}

export async function patchSessionRuntime(id, patch) {
  if (!id) throw new Error('session ID is required');
  return request(`/api/sessions/${encodeURIComponent(id)}/runtime`, {
    method: 'PATCH',
    ...jsonBody(patch)
  });
}

export async function cancelResponsesRun(sessionID, localRunID) {
  if (!sessionID || !localRunID) throw new Error('session ID and response run ID are required');
  return request(`/api/responses/runs/${encodeURIComponent(localRunID)}/cancel?session_id=${encodeURIComponent(sessionID)}`, {
    method: 'POST'
  });
}

export async function getResponsesRun(sessionID, localRunID) {
	if (!sessionID || !localRunID) throw new Error('session ID and response run ID are required');
	return request(`/api/responses/runs/${encodeURIComponent(localRunID)}?session_id=${encodeURIComponent(sessionID)}`);
}

export async function reconnectResponsesRun(sessionID, localRunID) {
	if (!sessionID || !localRunID) throw new Error('session ID and response run ID are required');
	return request(`/api/responses/runs/${encodeURIComponent(localRunID)}/reconnect?session_id=${encodeURIComponent(sessionID)}`, {
		method: 'POST'
	});
}

export async function refreshCron(sessionId = '') {
  const query = sessionId ? `?sessionId=${encodeURIComponent(sessionId)}` : '';
  cronInfo.set(await request(`/api/cron${query}`));
}

export async function refreshModelCatalog() {
  const st = get(status);
  if (st?.features?.openaiAPI === false) {
    modelCatalog.set(emptyModelCatalog());
    selectedModel.set('default');
    return;
  }
  try {
    const data = await request('/api/models/catalog');
    const catalog = {
      providers: (Array.isArray(data?.providers) ? data.providers : []).map(String).filter(Boolean),
      models: Array.isArray(data?.data) ? data.data : [],
      modelDefaults: data?.modelDefaults || {},
      defaultProvider: stringValue(data?.defaultProvider),
      defaultModel: stringValue(data?.defaultModel)
    };
    modelCatalog.set(catalog);
    const current = get(selectedModel);
    const validIds = new Set(catalog.models.map((m) => m?.id).filter(Boolean));
    if (catalog.defaultModel) validIds.add(catalog.defaultModel);
    if (catalog.models.length > 0 && (!current || current === 'default' || !validIds.has(current))) {
      selectedModel.set(defaultModelForCatalog(catalog));
    }
  } catch {
    modelCatalog.set(emptyModelCatalog());
    selectedModel.set('default');
  }
}

export function resetSelectedModelToDefault() {
  selectedModel.set(defaultModelForCatalog(get(modelCatalog)));
}

function defaultModelForCatalog(catalog) {
  const list = Array.isArray(catalog?.models) ? catalog.models : [];
  if (list.length === 0) return 'default';
  const ids = new Set(list.map((m) => m?.id).filter(Boolean));
  const serve = parseJSONStore(serveConfig);
  const serveModel = stringValue(serve?.api?.model);
  if (serveModel && ids.has(serveModel)) return serveModel;
  // The server-resolved effective model already reflects serve config and
  // settings defaults; it may be a synthetic ID outside the catalog list.
  if (catalog?.defaultModel) return catalog.defaultModel;
  return list[0]?.id || 'default';
}

function parseJSONStore(store) {
  try {
    const raw = get(store);
    return raw ? JSON.parse(raw) : {};
  } catch {
    return {};
  }
}

function stringValue(value) {
  return typeof value === 'string' ? value.trim() : '';
}

function loadSessionToolOptions() {
  if (typeof window === 'undefined') return {};
  try {
    const parsed = JSON.parse(window.localStorage.getItem(sessionToolStorageKey) || '{}');
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : {};
  } catch {
    return {};
  }
}

function saveSessionToolOptions(value) {
  if (typeof window === 'undefined') return;
  window.localStorage.setItem(sessionToolStorageKey, JSON.stringify(value || {}));
}

function normalizeSessionTools(value = {}) {
  return {
    webSearch: Boolean(value.webSearch),
    browser: Boolean(value.browser),
    a2aMaster: Boolean(value.a2aMaster),
    delegate: Boolean(value.delegate ?? value.delegateMode),
    multiAgent: Boolean(value.multiAgent),
    workflows: Boolean(value.workflows)
  };
}

export function sessionToolsFor(map, id, fallback = null) {
  const key = id || '__new__';
  const base = fallback ? normalizeSessionTools(fallback) : { ...defaultSessionTools };
  return normalizeSessionTools({ ...base, ...(map?.[key] || {}) });
}

export function setSessionTools(id, value) {
  const key = id || '__new__';
  const normalized = normalizeSessionTools(value);
  sessionToolOptions.update((prev) => {
    const next = { ...(prev || {}), [key]: normalized };
    saveSessionToolOptions(next);
    return next;
  });
}

export function moveSessionTools(fromID, toID) {
  const from = fromID || '__new__';
  const to = toID || '__new__';
  if (!from || !to || from === to) return;
  sessionToolOptions.update((prev) => {
    if (!prev?.[from]) return prev || {};
    const next = { ...(prev || {}), [to]: prev[from] };
    delete next[from];
    saveSessionToolOptions(next);
    return next;
  });
}
