import { get, writable } from "svelte/store";
import {
	defaultState,
	findSessionTab,
	findTab,
	focusGroup,
	openSession,
	openDraft,
	replaceDraft,
	selectTab,
	closeTab,
	closeOtherTabs,
	moveTab,
	splitGroup,
	removeGroup,
	resizeSplit,
	setTabMode,
	setPreset,
	reconcileSessions
} from "./model.js";
import { presetById } from "./presets.js";
import { normalizeWorkbench, serializeWorkbench } from "./normalize.js";
import { readWorkbenchDocument, writeWorkbenchDocument } from "./persistence.js";

let persistTimer = 0;
let hydrated = false;
let mutated = false;

function initialState() {
	const document = readWorkbenchDocument();
	return normalizeWorkbench(document).state || defaultState();
}

export const workbench = writable(initialState());

// Hydration is a one-shot startup step. Re-reading localStorage every time
// Chat.svelte remounts would discard layout changes that happened while /chat
// was unmounted but are still inside the 300ms persistence debounce (for
// example, Sidebar creating a Draft from another view). Multi-page last-write
// wins still works because a newly opened page hydrates once on startup.
export function hydrateWorkbench() {
	if (hydrated || mutated) {
		hydrated = true;
		return get(workbench);
	}
	hydrated = true;
	const next = normalizeWorkbench(readWorkbenchDocument()).state;
	workbench.set(next);
	return next;
}

export function persistWorkbenchNow() {
	if (persistTimer) {
		clearTimeout(persistTimer);
		persistTimer = 0;
	}
	const text = serializeWorkbench(get(workbench));
	if (text) writeWorkbenchDocument(text);
}

function schedulePersist() {
	if (persistTimer) return;
	persistTimer = setTimeout(() => {
		persistTimer = 0;
		const text = serializeWorkbench(get(workbench));
		if (text) writeWorkbenchDocument(text);
	}, 300);
}

export function commitWorkbench(nextState) {
	if (!nextState) return;
	mutated = true;
	workbench.set(nextState);
	schedulePersist();
}

export function openSessionInWorkbench(sessionId, targetGroupId) {
	const result = openSession(get(workbench), sessionId, targetGroupId);
	if (result.ok) commitWorkbench(result.state);
	return result;
}

export function openDraftInWorkbench(draft, targetGroupId) {
	const result = openDraft(get(workbench), draft, targetGroupId);
	if (result.ok) commitWorkbench(result.state);
	return result;
}

export function replaceDraftInWorkbench(draftId, sessionId) {
	const result = replaceDraft(get(workbench), draftId, sessionId);
	if (result.ok) commitWorkbench(result.state);
	return result;
}

export function selectTabInWorkbench(groupId, tabId) {
	const result = selectTab(get(workbench), groupId, tabId);
	if (result.ok) commitWorkbench(result.state);
	return result;
}

export function focusGroupInWorkbench(groupId) {
	const result = focusGroup(get(workbench), groupId);
	if (result.ok) commitWorkbench(result.state);
	return result;
}

export function closeOtherTabsInWorkbench(groupId, keepTabId) {
	const result = closeOtherTabs(get(workbench), groupId, keepTabId);
	if (result.ok) commitWorkbench(result.state);
	return result;
}

export function closeTabsRightInWorkbench(groupId, tabId) {
	let state = get(workbench);
	const groupView = state.view?.groups?.[groupId];
	if (!groupView) return { ok: false, reason: { code: "group-missing", groupId } };
	const index = (groupView.tabs || []).findIndex((tab) => tab.id === tabId);
	if (index < 0) return { ok: false, reason: { code: "tab-missing", tabId } };
	for (const tab of (groupView.tabs || []).slice(index + 1)) {
		const result = closeTab(state, tab.id);
		if (result.ok) state = result.state;
	}
	commitWorkbench(state);
	return { ok: true, state };
}

export function closeTabInWorkbench(tabId) {
	const result = closeTab(get(workbench), tabId);
	if (result.ok) commitWorkbench(result.state);
	return result;
}

export function moveTabInWorkbench(tabId, destGroupId, index) {
	const result = moveTab(get(workbench), tabId, destGroupId, index);
	if (result.ok) commitWorkbench(result.state);
	return result;
}

export function splitWorkbench(groupId, direction, tabId = "", side = "after") {
	const result = splitGroup(get(workbench), groupId, { direction, side }, tabId);
	if (result.ok) commitWorkbench(result.state);
	return result;
}

export function removeGroupFromWorkbench(groupId) {
	const result = removeGroup(get(workbench), groupId);
	if (result.ok) commitWorkbench(result.state);
	return result;
}

export function resizeWorkbenchSplit(splitId, weights) {
	const result = resizeSplit(get(workbench), splitId, weights);
	if (result.ok) commitWorkbench(result.state);
	return result;
}

export function setTabModeInWorkbench(tabId, mode) {
	const result = setTabMode(get(workbench), tabId, mode);
	if (result.ok) commitWorkbench(result.state);
	return result;
}

export function applyPresetInWorkbench(presetId) {
	const preset = presetById(presetId);
	if (!preset) return { ok: false, reason: { code: "unknown-preset", presetId } };
	const result = setPreset(get(workbench), preset.frame);
	if (result.ok) commitWorkbench(result.state);
	return result;
}

export function reconcileWorkbenchSessions(deletedSessionIds = []) {
	if (!deletedSessionIds.length) return { ok: true, changed: false };
	const result = reconcileSessions(get(workbench), deletedSessionIds);
	if (result.ok) commitWorkbench(result.state);
	return result;
}

export function focusedTabId(state = get(workbench)) {
	const groupId = state.attention?.focusedGroupId;
	const groupView = state.view?.groups?.[groupId];
	return groupView?.selectedTabId || "";
}

export function focusedSessionId(state = get(workbench)) {
	const tab = findTab(state, focusedTabId(state))?.tab;
	return tab?.kind === "session" ? tab.sessionId : "";
}

export function sessionTabFor(state, sessionId) {
	return findSessionTab(state, sessionId)?.tab || null;
}

export function resetWorkbench() {
	const next = defaultState();
	mutated = true;
	workbench.set(next);
	persistWorkbenchNow();
	return next;
}
