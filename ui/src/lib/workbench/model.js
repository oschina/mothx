// Pure workbench layout operations. This module deliberately has no Svelte,
// store, DOM, or API imports so it can be exercised by node:test and reused by
// both the desktop and mobile projections.

export const MAX_CENTER_GROUPS = 4;
export const MAX_TABS_PER_PANE = 16;
export const MAX_TABS_PER_SURFACE = 32;
export const MIN_WEIGHT = 0.1;
export const MAX_WEIGHT = 0.9;
export const MAX_ID_LENGTH = 256;

const MODES = new Set(["chat", "trajectory"]);

export function defaultState() {
	return {
		frame: { version: 1, center: { kind: "group", id: "g1" } },
		view: { groups: { g1: { tabs: [] } } },
		attention: { focusedGroupId: "g1", lastFocusedGroupId: "g1" }
	};
}

export function group(id) {
	return { kind: "group", id };
}

export function split(id, direction, left, right, weights = [0.5, 0.5]) {
	return { kind: "split", id, direction, weights: [...weights], children: [left, right] };
}

export function findGroup(node, groupId) {
	if (!node) return null;
	if (node.kind === "group") return node.id === groupId ? node : null;
	for (const child of node.children || []) {
		const found = findGroup(child, groupId);
		if (found) return found;
	}
	return null;
}

export function collectLeaves(node, out = []) {
	if (!node) return out;
	if (node.kind === "group") {
		out.push(node);
		return out;
	}
	for (const child of node.children || []) collectLeaves(child, out);
	return out;
}

export function countLeaves(node) {
	return collectLeaves(node).length;
}

export function collectTabs(state, groupId = "") {
	const leaves = groupId ? [findGroup(state.frame.center, groupId)].filter(Boolean) : collectLeaves(state.frame.center);
	const out = [];
	for (const leaf of leaves) out.push(...(state.view.groups[leaf.id]?.tabs || []));
	return out;
}

export function countTabs(state) {
	return collectTabs(state).length;
}

export function findTab(state, tabId) {
	for (const [groupId, groupView] of Object.entries(state.view.groups || {})) {
		const tab = (groupView.tabs || []).find((item) => item.id === tabId);
		if (tab) return { groupId, tab };
	}
	return null;
}

export function findSessionTab(state, sessionId) {
	for (const [groupId, groupView] of Object.entries(state.view.groups || {})) {
		const tab = (groupView.tabs || []).find((item) => item.kind === "session" && item.sessionId === sessionId);
		if (tab) return { groupId, tab };
	}
	return null;
}

export function selectedTab(state, groupId) {
	const view = state.view.groups?.[groupId];
	if (!view?.selectedTabId) return null;
	return (view.tabs || []).find((tab) => tab.id === view.selectedTabId) || null;
}

function focus(attention, groupId) {
	const current = attention?.focusedGroupId;
	return {
		focusedGroupId: groupId,
		lastFocusedGroupId: current && current !== groupId ? current : attention?.lastFocusedGroupId || groupId
	};
}

function updateGroup(state, groupId, updater) {
	const groupView = state.view.groups?.[groupId] || { tabs: [] };
	const next = updater({ ...groupView, tabs: [...(groupView.tabs || [])] });
	return {
		...state,
		view: { ...state.view, groups: { ...state.view.groups, [groupId]: next } }
	};
}

function withSelection(state, groupId, tabId) {
	const next = updateGroup(state, groupId, (groupView) => {
		const exists = groupView.tabs.some((tab) => tab.id === tabId);
		return exists ? { ...groupView, selectedTabId: tabId } : groupView;
	});
	return { ...next, attention: focus(next.attention, groupId) };
}

function makeSessionTab(sessionId, mode = "chat") {
	return { kind: "session", id: `session:${sessionId}`, sessionId, mode: normalizeMode(mode) };
}

function makeDraftTab(draft) {
	const draftId = String(draft?.draftId || draft?.id || "").trim();
	return {
		kind: "draft",
		id: `draft:${draftId}`,
		draftId,
		workDir: draft?.workDir || "",
		model: draft?.model || "",
		provider: draft?.provider || "",
		expertId: draft?.expertId || "",
		mode: "chat"
	};
}

function normalizeMode(mode) {
	return mode === "trajectory" ? "trajectory" : "chat";
}

function nextID(state, prefix) {
	let max = 0;
	const visit = (node) => {
		if (!node) return;
		if (node.kind === "split") {
			const n = Number(String(node.id || "").replace(prefix, ""));
			if (Number.isFinite(n)) max = Math.max(max, n);
			visit(node.children?.[0]);
			visit(node.children?.[1]);
		} else {
			const n = Number(String(node.id || "").replace(prefix, ""));
			if (Number.isFinite(n)) max = Math.max(max, n);
		}
	};
	visit(state.frame.center);
	return `${prefix}${max + 1}`;
}

function replaceNode(node, targetId, build) {
	if (!node) return node;
	if (node.kind === "group") return node.id === targetId ? build(node) : node;
	const children = [replaceNode(node.children[0], targetId, build), replaceNode(node.children[1], targetId, build)];
	return { ...node, children };
}

export function openSession(state, sessionId, targetGroupId = state.attention.focusedGroupId) {
	if (!sessionId) return fail("group-missing", { groupId: targetGroupId });
	const existing = findSessionTab(state, sessionId);
	if (existing) {
		return ok(withSelection(state, existing.groupId, existing.tab.id), {
			focus: { groupId: existing.groupId, tabId: existing.tab.id }
		});
	}
	if (!findGroup(state.frame.center, targetGroupId)) return fail("group-missing", { groupId: targetGroupId });
	if (countTabs(state) >= MAX_TABS_PER_SURFACE) return fail("tab-limit", { limit: MAX_TABS_PER_SURFACE });
	const groupView = state.view.groups?.[targetGroupId] || { tabs: [] };
	const tabs = groupView.tabs || [];
	if (tabs.length >= MAX_TABS_PER_PANE) return fail("tab-limit", { limit: MAX_TABS_PER_PANE });
	const tab = makeSessionTab(sessionId);
	const at = groupView.selectedTabId
		? Math.max(0, tabs.findIndex((item) => item.id === groupView.selectedTabId) + 1)
		: tabs.length;
	const next = updateGroup(state, targetGroupId, (current) => ({
		...current,
		tabs: [...tabs.slice(0, at), tab, ...tabs.slice(at)],
		selectedTabId: tab.id
	}));
	return ok({ ...next, attention: focus(next.attention, targetGroupId) }, { focus: { groupId: targetGroupId, tabId: tab.id } });
}

export function openDraft(state, draft, targetGroupId = state.attention.focusedGroupId) {
	const tab = makeDraftTab(draft);
	if (!tab.draftId) return fail("draft-missing", { draftId: "" });
	const existing = findTab(state, tab.id);
	if (existing) {
		return ok(withSelection(state, existing.groupId, existing.tab.id), {
			focus: { groupId: existing.groupId, tabId: existing.tab.id }
		});
	}
	if (!findGroup(state.frame.center, targetGroupId)) return fail("group-missing", { groupId: targetGroupId });
	if (countTabs(state) >= MAX_TABS_PER_SURFACE) return fail("tab-limit", { limit: MAX_TABS_PER_SURFACE });
	const groupView = state.view.groups?.[targetGroupId] || { tabs: [] };
	const tabs = groupView.tabs || [];
	if (tabs.length >= MAX_TABS_PER_PANE) return fail("tab-limit", { limit: MAX_TABS_PER_PANE });
	const at = groupView.selectedTabId
		? Math.max(0, tabs.findIndex((item) => item.id === groupView.selectedTabId) + 1)
		: tabs.length;
	const next = updateGroup(state, targetGroupId, (current) => ({
		...current,
		tabs: [...tabs.slice(0, at), tab, ...tabs.slice(at)],
		selectedTabId: tab.id
	}));
	return ok({ ...next, attention: focus(next.attention, targetGroupId) }, { focus: { groupId: targetGroupId, tabId: tab.id } });
}

export function replaceDraft(state, draftId, sessionId) {
	const found = findTab(state, `draft:${draftId}`);
	if (!found) return fail("draft-missing", { draftId });
	const existing = findSessionTab(state, sessionId);
	if (existing && existing.tab.id !== found.tab.id) {
		const without = closeTab(state, found.tab.id);
		if (!without.ok) return without;
		return openSession(without.state, sessionId, existing.groupId);
	}
	const sessionTab = { ...makeSessionTab(sessionId, found.tab.mode), id: `session:${sessionId}` };
	const next = updateGroup(state, found.groupId, (groupView) => ({
		...groupView,
		tabs: (groupView.tabs || []).map((tab) => (tab.id === found.tab.id ? sessionTab : tab)),
		selectedTabId: sessionTab.id
	}));
	return ok({ ...next, attention: focus(next.attention, found.groupId) }, { focus: { groupId: found.groupId, tabId: sessionTab.id } });
}

export function selectTab(state, groupId, tabId) {
	if (!findGroup(state.frame.center, groupId)) return fail("group-missing", { groupId });
	if (!(state.view.groups?.[groupId]?.tabs || []).some((tab) => tab.id === tabId)) return fail("tab-missing", { tabId });
	return ok(withSelection(state, groupId, tabId));
}

export function focusGroup(state, groupId) {
	if (!findGroup(state.frame.center, groupId)) return fail("group-missing", { groupId });
	return ok({ ...state, attention: focus(state.attention, groupId) });
}

export function closeTab(state, tabId) {
	const found = findTab(state, tabId);
	if (!found) return fail("tab-missing", { tabId });
	const groupView = state.view.groups[found.groupId];
	const index = groupView.tabs.findIndex((tab) => tab.id === tabId);
	const tabs = groupView.tabs.filter((tab) => tab.id !== tabId);
	let selectedTabId = groupView.selectedTabId;
	if (selectedTabId === tabId) {
		selectedTabId = tabs[Math.max(0, index - 1)]?.id || tabs[0]?.id;
	}
	const next = updateGroup(state, found.groupId, (current) => ({ ...current, tabs, selectedTabId }));
	return ok(next);
}

export function closeOtherTabs(state, groupId, keepTabId) {
	const groupView = state.view.groups?.[groupId];
	if (!groupView) return fail("group-missing", { groupId });
	if (!groupView.tabs.some((tab) => tab.id === keepTabId)) return fail("tab-missing", { tabId: keepTabId });
	const next = updateGroup(state, groupId, (current) => ({
		...current,
		tabs: current.tabs.filter((tab) => tab.id === keepTabId),
		selectedTabId: keepTabId
	}));
	return ok(next);
}

export function moveTab(state, tabId, destGroupId, index = Number.MAX_SAFE_INTEGER) {
	const found = findTab(state, tabId);
	if (!found) return fail("tab-missing", { tabId });
	if (!findGroup(state.frame.center, destGroupId)) return fail("group-missing", { groupId: destGroupId });
	const source = state.view.groups[found.groupId];
	const sourceIndex = source.tabs.findIndex((tab) => tab.id === tabId);
	const tab = found.tab;
	if (found.groupId === destGroupId) {
		const tabs = source.tabs.filter((item) => item.id !== tabId);
		const clamped = Math.max(0, Math.min(Number.isFinite(index) ? index : tabs.length, tabs.length));
		if (sourceIndex === clamped) return fail("same-position");
		tabs.splice(clamped, 0, tab);
		const next = updateGroup(state, destGroupId, (current) => ({
			...current,
			tabs,
			selectedTabId: tabId
		}));
		return ok({ ...next, attention: focus(next.attention, destGroupId) });
	}
	const sourceTabs = source.tabs.filter((item) => item.id !== tabId);
	const sourceSelected = source.selectedTabId === tabId
		? (sourceTabs[Math.max(0, sourceIndex - 1)]?.id || sourceTabs[0]?.id || "")
		: source.selectedTabId;
	let next = updateGroup(state, found.groupId, (current) => ({
		...current,
		tabs: sourceTabs,
		selectedTabId: sourceSelected
	}));
	const destTabs = [...(state.view.groups[destGroupId]?.tabs || [])];
	const clamped = Math.max(0, Math.min(Number.isFinite(index) ? index : destTabs.length, destTabs.length));
	destTabs.splice(clamped, 0, tab);
	next = updateGroup(next, destGroupId, (current) => ({
		...current,
		tabs: destTabs,
		selectedTabId: tabId
	}));
	return ok({ ...next, attention: focus(next.attention, destGroupId) });
}

export function splitGroup(state, groupId, spec = {}, tabId = "") {
	if (countLeaves(state.frame.center) >= MAX_CENTER_GROUPS) return fail("center-limit", { limit: MAX_CENTER_GROUPS });
	const source = findGroup(state.frame.center, groupId);
	if (!source) return fail("group-missing", { groupId });
	const direction = spec.direction === "vertical" ? "vertical" : "horizontal";
	const newGroupId = nextID(state, "g");
	const newSplitId = nextID(state, "s");
	const newGroup = group(newGroupId);
	const splitNode = split(newSplitId, direction, spec.side === "before" ? newGroup : source, spec.side === "before" ? source : newGroup);
	let next = {
		...state,
		frame: { ...state.frame, center: replaceNode(state.frame.center, groupId, () => splitNode) },
		view: { ...state.view, groups: { ...state.view.groups, [newGroupId]: { tabs: [] } } },
		attention: focus(state.attention, newGroupId)
	};
	if (tabId) {
		next = moveTab(next, tabId, newGroupId, 0);
		if (!next.ok) return next;
		next = next.state;
	}
	return ok(next, { focus: { groupId: newGroupId }, shapeChanged: true });
}

export function removeGroup(state, groupId) {
	if (countLeaves(state.frame.center) <= 1) return fail("last-group");
	const leaf = findGroup(state.frame.center, groupId);
	if (!leaf) return fail("group-missing", { groupId });
	const leaves = collectLeaves(state.frame.center);
	const index = leaves.findIndex((item) => item.id === groupId);
	const target = leaves[index - 1] || leaves[index + 1];
	const movedTabs = state.view.groups?.[groupId]?.tabs || [];
	let next = updateGroup(state, target.id, (current) => ({
		...current,
		tabs: [...(current.tabs || []), ...movedTabs],
		selectedTabId: current.selectedTabId || movedTabs[0]?.id
	}));
	const removeNode = (node) => {
		if (!node || node.kind === "group") return node;
		if (node.children?.[0]?.id === groupId) return node.children[1];
		if (node.children?.[1]?.id === groupId) return node.children[0];
		return { ...node, children: [removeNode(node.children[0]), removeNode(node.children[1])] };
	};
	const groups = { ...next.view.groups };
	delete groups[groupId];
	const center = removeNode(next.frame.center);
	let attention = next.attention;
	if (attention.focusedGroupId === groupId) {
		const fallback = attention.lastFocusedGroupId && attention.lastFocusedGroupId !== groupId
			&& findGroup(center, attention.lastFocusedGroupId) ? attention.lastFocusedGroupId : target.id;
		attention = { focusedGroupId: fallback, lastFocusedGroupId: fallback };
	} else if (attention.lastFocusedGroupId === groupId || !findGroup(center, attention.lastFocusedGroupId)) {
		attention = { ...attention, lastFocusedGroupId: target.id };
	}
	next = {
		...next,
		frame: { ...next.frame, center },
		view: { ...next.view, groups },
		attention
	};
	return ok(next, { focus: { groupId: target.id }, shapeChanged: true });
}

export function resizeSplit(state, splitId, weights) {
	let found = false;
	const apply = (node) => {
		if (!node || node.kind === "group") return node;
		if (node.id === splitId) {
			found = true;
			const [a, b] = normalizeWeights(weights);
			return { ...node, weights: [a, b] };
		}
		return { ...node, children: [apply(node.children[0]), apply(node.children[1])] };
	};
	const center = apply(state.frame.center);
	if (!found) return fail("group-missing", { groupId: splitId });
	return ok({ ...state, frame: { ...state.frame, center } });
}

export function setTabMode(state, tabId, mode) {
	const found = findTab(state, tabId);
	if (!found) return fail("tab-missing", { tabId });
	const next = updateGroup(state, found.groupId, (groupView) => ({
		...groupView,
		tabs: groupView.tabs.map((tab) => (tab.id === tabId ? { ...tab, mode: normalizeMode(mode) } : tab))
	}));
	return ok(next);
}

export function capturePreset(state) {
	return { version: 1, center: cloneNode(state.frame.center) };
}

export function setPreset(state, frame) {
	if (!frame?.center) return fail("unknown-preset", { presetId: "" });
	if (countLeaves(frame.center) > MAX_CENTER_GROUPS) return fail("center-limit", { limit: MAX_CENTER_GROUPS });
	const tabs = collectTabs(state);
	const leaves = collectLeaves(frame.center);
	if (!leaves.length) return fail("unknown-preset", { presetId: "" });
	const groups = {};
	leaves.forEach((leaf, index) => {
		groups[leaf.id] = { tabs: [], selectedTabId: undefined };
	});
	const tabGroups = new Map();
	tabs.forEach((tab, index) => {
		const leaf = leaves[Math.min(index, leaves.length - 1)];
		groups[leaf.id].tabs.push(tab);
		tabGroups.set(tab.id, leaf.id);
	});
	for (const leaf of leaves) {
		const groupView = groups[leaf.id];
		groupView.selectedTabId = groupView.tabs[0]?.id;
	}
	const focused = state.attention.focusedGroupId;
	const focusedTabId = state.view.groups?.[focused]?.selectedTabId || "";
	// Applying a preset must keep the focused session visible: follow the
	// selected tab into its new leaf before falling back to the old group id or
	// the first leaf (the old id may not exist in the new frame at all).
	const focusedLeaf = tabGroups.get(focusedTabId)
		|| (findGroup(frame.center, focused) ? focused : leaves[0].id);
	if (focusedTabId && tabGroups.has(focusedTabId)) {
		groups[focusedLeaf].selectedTabId = focusedTabId;
	}
	const lastFocused = findGroup(frame.center, state.attention.lastFocusedGroupId)
		? state.attention.lastFocusedGroupId : focusedLeaf;
	return ok({
		...state,
		frame: { version: 1, center: cloneNode(frame.center) },
		view: { groups },
		attention: { focusedGroupId: focusedLeaf, lastFocusedGroupId: lastFocused }
	}, { focus: { groupId: focusedLeaf }, shapeChanged: true });
}

export function reconcileSessions(state, deletedSessionIds = []) {
	const deleted = new Set(deletedSessionIds);
	let next = state;
	for (const tab of collectTabs(state)) {
		if (tab.kind === "session" && deleted.has(tab.sessionId)) {
			const result = closeTab(next, tab.id);
			if (result.ok) next = result.state;
		}
	}
	return ok(next);
}

export function countTabsInGroup(state, groupId) {
	return state.view.groups?.[groupId]?.tabs?.length || 0;
}

export function normalizeWeights(weights) {
	const a = Number(weights?.[0]);
	const b = Number(weights?.[1]);
	if (!Number.isFinite(a) || !Number.isFinite(b) || a <= 0 || b <= 0) return [0.5, 0.5];
	const sum = a + b;
	let left = Math.min(MAX_WEIGHT, Math.max(MIN_WEIGHT, a / sum));
	let right = 1 - left;
	// Floating point subtraction can leave the complement a hair below the
	// minimum (1 - 0.9 === 0.09999999999999998). Clamp the complement first and
	// derive the other side so both documented bounds hold exactly.
	if (right < MIN_WEIGHT) {
		right = MIN_WEIGHT;
		left = 1 - right;
	} else if (right > MAX_WEIGHT) {
		right = MAX_WEIGHT;
		left = 1 - right;
	}
	return [left, right];
}

function cloneNode(node) {
	if (!node) return node;
	if (node.kind === "group") return { kind: "group", id: node.id };
	return {
		kind: "split",
		id: node.id,
		direction: node.direction,
		weights: [...node.weights],
		children: [cloneNode(node.children[0]), cloneNode(node.children[1])]
	};
}

function ok(state, extra = {}) {
	return { ok: true, state, shapeChanged: false, ...extra };
}

function fail(code, extra = {}) {
	return { ok: false, reason: { code, ...extra } };
}
