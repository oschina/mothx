import { get } from "svelte/store";
import { t } from "./preferences.js";
import { runEvents, runsConnected, refreshSessions, refreshStatsSummary, getResponsesRun, reconnectResponsesRun, getSessionRuntime } from "./stores.js";
import { readSSE } from "./api.js";
import {
	sessionRunStates,
	getSessionState,
	updateSessionState,
	eventBelongsToActiveRun,
	isCompletionActive,
	isSessionRuntimeRunning,
	isActiveRunStatus,
	registerObserver,
	clearObserver
} from "./session-runs.js";
import {
	sessionStateWithView,
	viewFromSessionState,
	reduceApprovalRequest,
	reduceApprovalResolved,
	reduceCapabilityEvent,
	reduceQuestionRequest,
	reduceQuestionResolved,
	reduceRunEvent,
	reduceRuntimeSnapshot,
	reduceStreamDone,
	reduceStreamError,
	reduceToolStatusEvent,
	reduceTranscriptEvent
} from "./session-view.js";
import { normalizeErrorInfo } from "./run-error.js";

let cursor = 0;
let unsubscribe = null;
let stateUnsubscribe = null;
let connectionUnsubscribe = null;
let connected = false;
const visibleSessionIds = new Set();
const observers = new Map();
const responsePollers = new Map();

export function setVisibleSessions(ids = []) {
	visibleSessionIds.clear();
	for (const id of ids) if (id) visibleSessionIds.add(id);
	syncObservers();
}

export function startSessionStreamDispatcher() {
	if (!unsubscribe) {
		unsubscribe = runEvents.subscribe((items) => {
			const pending = items.filter((item) => Number(item?.wsSeq || 0) > cursor);
			if (!pending.length) return;
			cursor = Number(pending[pending.length - 1].wsSeq) || cursor;
			for (const item of pending) {
				if (item?.type !== "session_event" || !item.sessionId) continue;
				applySessionStreamEvent(item);
			}
		});
	}
	if (!connectionUnsubscribe) {
		connectionUnsubscribe = runsConnected.subscribe((value) => {
			connected = value === true;
			syncObservers();
		});
	}
	if (!stateUnsubscribe) {
		stateUnsubscribe = sessionRunStates.subscribe(() => {
			syncObservers();
			syncResponsePollers();
		});
	}
	return stopSessionStreamDispatcher;
}

function shouldObserve(state) {
	if (!state?.sessionId || connected || state.streamCompleted) return false;
	return isCompletionActive(state) || isSessionRuntimeRunning(state.runtime);
}

function syncObservers() {
	const states = get(sessionRunStates) || {};
	for (const [sessionId, controller] of observers) {
		if (!shouldObserve(states[sessionId])) {
			controller.abort();
			clearObserver(sessionId, controller);
			observers.delete(sessionId);
		}
	}
	for (const [sessionId, state] of Object.entries(states)) {
		if (shouldObserve(state) && !observers.has(sessionId)) void startObserver(sessionId, state);
	}
}

async function startObserver(sessionId, state) {
	const controller = new AbortController();
	observers.set(sessionId, controller);
	registerObserver(sessionId, controller);
	const cursorValue = state?.cursor || {};
	const params = new URLSearchParams();
	if (cursorValue.entrySeq > 0) params.set("after_entry_seq", String(cursorValue.entrySeq));
	if (cursorValue.runSeq > 0) params.set("after_run_seq", String(cursorValue.runSeq));
	if (cursorValue.capabilitySeq > 0) params.set("after_capability_seq", String(cursorValue.capabilitySeq));
	const query = params.toString();
	try {
		const res = await fetch(`/api/sessions/${encodeURIComponent(sessionId)}/stream${query ? `?${query}` : ""}`, { signal: controller.signal });
		if (!res.ok || !res.body) {
			const text = await res.text();
			throw new Error(text || `${res.status} ${res.statusText}`);
		}
		await readSSE(res.body, (event) => {
			if (controller.signal.aborted) return;
			const name = event?.event || "";
			if (!name || name === "heartbeat") return;
			const normalized = ["started", "finished", "failed", "canceled", "cancelled", "retrying", "run_retrying", "timed_out", "incomplete"].includes(name)
				? "run_event"
				: name;
			applySessionStreamEvent({ type: "session_event", sessionId, event: normalized, data: event.data });
		});
	} catch (err) {
		if (err?.name !== "AbortError") console.warn(`[session stream] ${sessionId}: ${err?.message || err}`);
	} finally {
		clearObserver(sessionId, controller);
		if (observers.get(sessionId) === controller) observers.delete(sessionId);
	}
}

export function stopSessionStreamDispatcher() {
	unsubscribe?.();
	unsubscribe = null;
	stateUnsubscribe?.();
	stateUnsubscribe = null;
	connectionUnsubscribe?.();
	connectionUnsubscribe = null;
	for (const [sessionId, controller] of observers) {
		controller.abort();
		clearObserver(sessionId, controller);
	}
	observers.clear();
	for (const [sessionId, poller] of responsePollers) {
		clearInterval(poller.timer);
		responsePollers.delete(sessionId);
	}
}

// Responses background runs are durable and may belong to a session whose tab
// is not mounted. The dispatcher owns one poller per session so closing a pane
// cannot stop reconnect/polling for a background run.
function syncResponsePollers() {
	const states = get(sessionRunStates) || {};
	for (const [sessionId, poller] of responsePollers) {
		const run = states[sessionId]?.runtime?.responsesRun;
		if (!run || run.localRunId !== poller.localRunId || !isActiveRunStatus(run.state)) {
			clearInterval(poller.timer);
			responsePollers.delete(sessionId);
		}
	}
	for (const [sessionId, state] of Object.entries(states)) {
		const run = state?.runtime?.responsesRun;
		if (!run?.localRunId || !isActiveRunStatus(run.state) || responsePollers.has(sessionId)) continue;
		startResponsePoller(sessionId, run.localRunId);
	}
}

function startResponsePoller(sessionId, localRunId) {
	const entry = { localRunId, timer: 0, reconnecting: false, stopped: false };
	responsePollers.set(sessionId, entry);
	reconnectResponsesRun(sessionId, localRunId)
		.then((result) => {
			const remote = result?.run;
			if (entry.stopped || !remote || remote.localRunId !== localRunId) return;
			patchResponsesRun(sessionId, { localRunId, responseId: remote.responseId, state: remote.state, cancelRequested: remote.cancelRequested });
		})
		.catch(() => { /* the poll path still reports remote state */ });
	const poll = async () => {
		if (entry.stopped) return;
		const current = get(sessionRunStates)?.[sessionId]?.runtime?.responsesRun;
		if (!current || current.localRunId !== localRunId) {
			stopResponsePoller(sessionId, entry);
			return;
		}
		try {
			const remote = await getResponsesRun(sessionId, localRunId);
			if (entry.stopped) return;
			if (remote && remote.localRunId === localRunId) {
				patchResponsesRun(sessionId, { localRunId, responseId: remote.responseId, state: remote.state, cancelRequested: remote.cancelRequested });
			}
			if (!remote || !isActiveRunStatus(remote.state)) {
				stopResponsePoller(sessionId, entry);
				try {
					const snapshot = await getSessionRuntime(sessionId);
					if (!entry.stopped) updateSessionState(sessionId, (state) => ({ ...state, runtime: snapshot }));
				} catch { /* keep the last durable state */ }
			}
		} catch { /* retry on the next interval */ }
	};
	entry.timer = setInterval(poll, 1000);
	void poll();
}

function stopResponsePoller(sessionId, entry) {
	entry.stopped = true;
	clearInterval(entry.timer);
	if (responsePollers.get(sessionId) === entry) responsePollers.delete(sessionId);
}

function patchResponsesRun(sessionId, run) {
	updateSessionState(sessionId, (state) => ({
		...state,
		runtime: {
			...(state.runtime || {}),
			responsesRun: {
				...(state.runtime?.responsesRun || {}),
				...run
			}
		}
	}));
}

export function resetSessionStreamCursor(value = 0) {
	cursor = Number(value) || 0;
}

function applySessionStreamEvent(item) {
	const sessionId = item.sessionId;
	const event = item.event;
	if (!sessionId || !event || item.data === "[DONE]") return;
	const state = getSessionState(sessionId);
	if (event === "transcript") {
		const payload = parsePayload(item.data);
		if (!payload || !belongsToSession(sessionId, payload)) return;
		const seq = Number(payload?.message?.seq || 0);
		if (seq > 0 && isOlderThanLoadedHistory(state, seq)) return;
		reduce(sessionId, (view) => reduceTranscriptEvent(view, payload, get(t)), { scroll: true, subAgent: true });
		return;
	}
	if (event === "tool_event") {
		const payload = parsePayload(item.data);
		if (!payload || !belongsToSession(sessionId, payload)) return;
		reduce(sessionId, (view) => reduceToolStatusEvent(view, payload, get(t)), { scroll: true, subAgent: true });
		return;
	}
	if (event === "status") {
		const payload = parsePayload(item.data);
		if (!payload || !belongs(state, payload)) return;
		const retry = payload?.retry || payload?.data?.retry;
		if (!payload?.message && !retry) return;
		const entry = {
			id: payload?.id || `stream-status-${Date.now()}`,
			sessionId,
			runId: payload?.runId || retry?.runId || "",
			intentId: payload?.intentId || retry?.intentId || "",
			eventType: retry ? "run_retrying" : "status",
			status: "running",
			timestamp: payload?.timestamp || new Date().toISOString(),
			data: { ...(payload?.data || {}), ...(payload?.message ? { message: payload.message } : {}), ...(retry ? { retry } : {}) }
		};
		reduce(sessionId, (view) => reduceRunEvent(view, entry), { subAgent: false });
		return;
	}
	if (event === "done") {
		const payload = parsePayload(item.data);
		if (payload && (!belongsToSession(sessionId, payload) || !belongs(state, payload))) return;
		reduce(sessionId, (view) => reduceStreamDone(view), { historyReload: true, stats: true });
		refreshSessions().catch(() => {});
		refreshStatsSummary().catch(() => {});
		return;
	}
	if (event === "error") {
		const payload = parsePayload(item.data);
		const error = normalizeErrorInfo(payload?.errorInfo || payload?.error || payload);
		if (payload && !belongs(state, payload)) return;
		reduce(sessionId, (view) => reduceStreamError(view, error || payload, get(t)), { errorNotice: error });
		return;
	}
	if (event === "run_event" || ["started", "finished", "failed", "canceled", "cancelled", "retrying", "run_retrying", "timed_out", "incomplete"].includes(event)) {
		const payload = parsePayload(item.data);
		if (!payload) return;
		const runEvent = payload?.eventType ? payload : { ...payload, eventType: event, sessionId: payload?.sessionId || sessionId };
		if (!belongsToSession(sessionId, runEvent) || !belongs(state, runEvent)) return;
		const error = normalizeErrorInfo(runEvent?.data?.errorInfo || runEvent?.data?.error || runEvent?.errorInfo || runEvent?.error);
		reduce(sessionId, (view) => {
			const next = reduceRunEvent(view, runEvent);
			if (!isTerminalFailure(runEvent) || !error) return next;
			return reduceStreamError(next, error, get(t));
		}, { errorNotice: error });
		return;
	}
	if (event === "runtime_event") {
		const payload = parsePayload(item.data);
		if (!payload || !belongsToSession(sessionId, payload) || !belongs(state, payload?.activeRun || payload?.responsesRun || payload)) return;
		reduce(sessionId, (view) => ({ view: reduceRuntimeSnapshot(view, payload) }));
		return;
	}
	if (event === "capability_event") {
		const payload = parsePayload(item.data);
		if (!payload || !belongsToSession(sessionId, payload)) return;
		reduce(sessionId, (view) => ({ view: reduceCapabilityEvent(view, payload) }));
		return;
	}
	if (event === "approval_request") {
		const payload = parsePayload(item.data);
		if (!payload?.approvalId || !belongsToSession(sessionId, payload)) return;
		const result = reduce(sessionId, (view) => reduceApprovalRequest(view, payload, sessionId));
		if (result.effects?.applies) {
			patchEffects(sessionId, (effects) => {
				effects.approvalEpoch += 1;
				effects.approvalId = payload.approvalId;
			});
		}
		return;
	}
	if (event === "approval_resolved") {
		const payload = parsePayload(item.data);
		if (!payload?.approvalId) return;
		reduce(sessionId, (view) => ({ view: reduceApprovalResolved(view, payload) }));
		return;
	}
	if (event === "question_request") {
		const payload = parsePayload(item.data);
		if (!payload?.questionId || !belongsToSession(sessionId, payload)) return;
		reduce(sessionId, (view) => reduceQuestionRequest(view, payload));
		patchEffects(sessionId, (effects) => {
			effects.questionEpoch += 1;
			effects.questionId = payload.questionId;
		});
		return;
	}
	if (event === "question_resolved") {
		const payload = parsePayload(item.data);
		if (!payload?.questionId) return;
		reduce(sessionId, (view) => ({ view: reduceQuestionResolved(view, payload) }));
	}
}

function reduce(sessionId, reducer, marks = {}) {
	let reducerEffects = {};
	updateSessionState(sessionId, (state) => {
		const before = viewFromSessionState(state);
		const result = reducer(before);
		const view = result?.view || result;
		reducerEffects = result?.effects || {};
		let next = sessionStateWithView(state, view);
		next = applyEffects(next, marks, reducerEffects);
		return next;
	});
	return { effects: { ...marks, ...reducerEffects } };
}

function applyEffects(state, marks = {}, reducerEffects = {}) {
	const effects = {
		scrollEpoch: 0,
		historyReloadEpoch: 0,
		statsRefreshEpoch: 0,
		refreshEpoch: 0,
		subAgentEpoch: 0,
		approvalEpoch: 0,
		questionEpoch: 0,
		...(state.effects || {})
	};
	if (marks.scroll || reducerEffects.scroll || reducerEffects.forceScroll) effects.scrollEpoch += 1;
	if (marks.historyReload || reducerEffects.historyReload) effects.historyReloadEpoch += 1;
	if (marks.stats || reducerEffects.statsRefresh) effects.statsRefreshEpoch += 1;
	if (marks.subAgent && (reducerEffects.subAgentRefresh || reducerEffects.subAgentTranscriptAgent)) effects.subAgentEpoch += 1;
	if (marks.errorNotice) effects.errorNotice = marks.errorNotice.message || String(marks.errorNotice);
	return { ...state, effects };
}

function patchEffects(sessionId, updater) {
	updateSessionState(sessionId, (state) => {
		const effects = { scrollEpoch: 0, historyReloadEpoch: 0, statsRefreshEpoch: 0, refreshEpoch: 0, subAgentEpoch: 0, approvalEpoch: 0, questionEpoch: 0, ...(state.effects || {}) };
		updater(effects);
		return { ...state, effects };
	});
}

function parsePayload(data) {
	if (data == null) return null;
	if (typeof data === "string") {
		try {
			return JSON.parse(data);
		} catch {
			return null;
		}
	}
	return data;
}

function belongsToSession(sessionId, payload) {
	const bound = String(payload?.sessionId || payload?.sessionID || "").trim();
	return !bound || bound === sessionId;
}

function belongs(state, payload) {
	const runID = String(payload?.runId || payload?.runID || payload?.data?.runId || payload?.data?.runID || "").trim();
	return !runID || eventBelongsToActiveRun(state, runID);
}

function isTerminalFailure(event) {
	const eventType = String(event?.eventType || event?.event || "").toLowerCase();
	const status = String(event?.status || "").toLowerCase();
	return ["failed", "canceled", "cancelled", "timed_out", "incomplete"].includes(eventType)
		|| ["failed", "canceled", "cancelled", "timed_out", "incomplete"].includes(status);
}

function isOlderThanLoadedHistory(state, seq) {
	return Boolean(state?.historyLoaded) && state?.earliestSeq != null && seq > 0 && seq < state.earliestSeq;
}

export function sessionIsVisible(sessionId) {
	return visibleSessionIds.has(sessionId);
}
