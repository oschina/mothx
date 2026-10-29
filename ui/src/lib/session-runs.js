import { get, writable } from 'svelte/store';
import { normalizeErrorInfo } from './run-error.js';

export const sessionRunStates = writable({});

export function emptySessionState(sessionId = '') {
  return {
    sessionId,
    completion: null,
    observer: null,
    messages: [],
    toolEvents: [],
    runEvents: [],
    capabilityEvents: [],
    runtime: null,
    pendingApprovals: [],
    cursor: { entrySeq: 0, runSeq: 0, capabilitySeq: 0 },
    historyLoaded: false,
    loading: false,
    status: "idle",
    loadedAt: 0,
    earliestSeq: null,
    hasMore: false,
    effects: {
      scrollEpoch: 0,
      historyReloadEpoch: 0,
      statsRefreshEpoch: 0,
      refreshEpoch: 0,
      subAgentEpoch: 0,
      approvalEpoch: 0,
      questionEpoch: 0
    },
    modelOverride: "",
    providerOverride: "",
    streamCompleted: false,
    streamUsesTranscript: false,
    optimisticRunEventID: '',
    subAgents: [],
    subAgentTranscripts: {},
    currentRunId: '',
    intentId: '',
    attempt: 0,
    retry: null,
    lastError: null
  };
}

export function ensureSessionState(sessionId) {
  if (!sessionId) return emptySessionState('');
  let state;
  sessionRunStates.update((states) => {
    state = states[sessionId];
    if (state) return states;
    state = emptySessionState(sessionId);
    return { ...states, [sessionId]: state };
  });
  return state;
}

export function getSessionState(sessionId) {
  if (!sessionId) return emptySessionState('');
  return get(sessionRunStates)[sessionId] || ensureSessionState(sessionId);
}

export function updateSessionState(sessionId, updater) {
  if (!sessionId || typeof updater !== 'function') return getSessionState(sessionId);
  let nextState;
  sessionRunStates.update((states) => {
    const current = states[sessionId] || emptySessionState(sessionId);
    nextState = updater(current) || current;
    if (nextState === current) return states;
    return { ...states, [sessionId]: { ...nextState, sessionId } };
  });
  return nextState;
}

export function setSessionState(sessionId, patch) {
  return updateSessionState(sessionId, (current) => ({ ...current, ...patch }));
}

export function isCompletionActive(state) {
  const status = state?.completion?.status;
  return status === 'starting' || status === 'running' || status === 'cancel_requested';
}

// isActiveRunStatus reports whether a server-side run status means the run is
// still executing (queued/running/cancelling/terminalizing). Unlike
// isCompletionActive — which only tracks runs started by this page — this also
// matches runs observed after a page refresh via the runtime snapshot.
export function isActiveRunStatus(status) {
  return ['queued', 'running', 'retrying', 'waiting_for_approval', 'waiting_for_question', 'cancelling', 'terminalizing'].includes(status);
}

// Runtime execution is the canonical cross-process view. Fall back to the
// legacy run projections only for servers from before the execution snapshot
// contract was added.
export function isSessionRuntimeBusy(runtime) {
  if (runtime?.execution) return Boolean(runtime.execution.busy);
  return isActiveRunStatus(runtime?.activeRun?.status)
    || isActiveRunStatus(runtime?.responsesRun?.state);
}

export function isSessionRuntimeRunning(runtime) {
  if (runtime?.execution) return Boolean(runtime.execution.running);
  return isSessionRuntimeBusy(runtime);
}

export function canStopSessionRuntime(runtime) {
  const execution = runtime?.execution;
  if (!execution) return isSessionRuntimeBusy(runtime);
  return Boolean(
    execution.canCancelLocal
      || execution.canCancelRemote
      || execution.state === 'orphaned'
      || execution.state === 'recovery_failed'
  );
}

// A completion run ID is authoritative while this page owns an active
// request. After a refresh there is no local completion, so fall back to the
// runtime snapshot/current run identity for filtering delayed events.
export function activeRunID(state) {
  const completionRunID = String(state?.completion?.runId || '').trim();
  if (state?.completion) return completionRunID;
  return String(
    state?.runtime?.activeRun?.runId
      || state?.runtime?.responsesRun?.localRunId
      || state?.currentRunId
      || ''
  ).trim();
}

export function eventBelongsToActiveRun(state, runID) {
  const incoming = String(runID || '').trim();
  if (!incoming) return true;
  const current = activeRunID(state);
  if (current) return current === incoming;
  const superseded = String(state?.completion?.supersededRunId || '').trim();
  return !superseded || superseded !== incoming;
}

export function completionOwnedBy(state, controller) {
  return Boolean(controller) && state?.completion?.controller === controller;
}

export function registerCompletion(sessionId, controller, { idempotencyKey = '' } = {}) {
  return updateSessionState(sessionId, (current) => {
    if (isCompletionActive(current)) {
      throw new Error('This session already has an active run.');
    }
    const supersededRunId = String(
      current.currentRunId
        || current.runtime?.activeRun?.runId
        || current.runtime?.responsesRun?.localRunId
        || ''
    ).trim();
    return {
      ...current,
      completion: {
        controller,
        status: 'starting',
        startedAt: new Date().toISOString(),
        runId: '',
        intentId: '',
        idempotencyKey,
        supersededRunId
      },
      streamCompleted: false,
      streamUsesTranscript: false,
      retry: null,
      lastError: null
    };
  });
}

export function setCompletionRun(sessionId, controller, run = {}) {
  const runId = String(run.runId || '').trim();
  const intentId = String(run.intentId || '').trim();
  const attempt = Number(run.attempt || 0) || 0;
  return updateSessionState(sessionId, (current) => {
    if (!current.completion || (controller && current.completion.controller !== controller)) return current;
    return {
      ...current,
      completion: {
        ...current.completion,
        ...(runId ? { runId } : {}),
        ...(intentId ? { intentId } : {}),
        ...(attempt ? { attempt } : {}),
        ...(run.idempotencyKey ? { idempotencyKey: run.idempotencyKey } : {})
      },
      ...(runId ? { currentRunId: runId } : {}),
      ...(intentId ? { intentId } : {}),
      ...(attempt ? { attempt } : {})
    };
  });
}

export function markCompletion(sessionId, status, error = null) {
  return updateSessionState(sessionId, (current) => ({
    ...current,
    completion: current.completion ? { ...current.completion, status } : null,
    lastError: error
      ? normalizeErrorInfo(error)
      : status === 'completed'
        ? null
        : current.lastError
  }));
}

export function clearCompletion(sessionId, controller) {
  return updateSessionState(sessionId, (current) => {
    if (!current.completion || current.completion.controller !== controller) return current;
    return { ...current, completion: null };
  });
}

export function abortCompletion(sessionId) {
  const state = getSessionState(sessionId);
  const controller = state.completion?.controller;
  if (!controller) return false;
  markCompletion(sessionId, 'cancel_requested');
  controller.abort();
  return true;
}

export function registerObserver(sessionId, controller) {
  return updateSessionState(sessionId, (current) => ({
    ...current,
    observer: { controller, source: 'session_stream' }
  }));
}

export function clearObserver(sessionId, controller) {
  return updateSessionState(sessionId, (current) => {
    if (!current.observer || current.observer.controller !== controller) return current;
    return { ...current, observer: null };
  });
}

export function stopObserver(sessionId) {
  const state = getSessionState(sessionId);
  const controller = state.observer?.controller;
  if (!controller) return false;
  controller.abort();
  clearObserver(sessionId, controller);
  return true;
}

export function removeSessionState(sessionId) {
  if (!sessionId) return;
  sessionRunStates.update((states) => {
    if (!states[sessionId]) return states;
    const next = { ...states };
    delete next[sessionId];
    return next;
  });
}

export function eventSessionID(payload, fallback = '') {
  return String(payload?.sessionId || payload?.x_session_id || fallback || '').trim();
}

export function eventBelongsToSession(boundSessionId, payload) {
  const actual = eventSessionID(payload, boundSessionId);
  return !actual || actual === boundSessionId;
}
