import assert from "node:assert/strict";
import test from "node:test";
import {
	moveTab,
	MAX_TABS_PER_SURFACE,
	closeTab,
	defaultState,
	findSessionTab,
	openDraft,
	openSession,
	reconcileSessions,
	removeGroup,
	replaceDraft,
	resizeSplit,
	setPreset,
	setTabMode,
	selectTab,
	splitGroup,
	normalizeWeights
} from "./model.js";
import { BUILTIN_PRESETS } from "./presets.js";
import { normalizeWorkbench, decodeWorkbench, serializeWorkbench } from "./normalize.js";

function ok(result) {
	assert.equal(result.ok, true, JSON.stringify(result.reason));
	return result.state;
}

test("openSession focuses an existing tab instead of duplicating", () => {
	let state = ok(openSession(defaultState(), "s1"));
	state = ok(openSession(state, "s2"));
	state = ok(openSession(state, "s1"));
	const found = findSessionTab(state, "s1");
	assert.ok(found);
	assert.equal(state.view.groups.g1.tabs.length, 2);
	assert.equal(state.headlessFinal, undefined);
});

test("closeTab keeps the group and selects a neighbor", () => {
	let state = ok(openSession(defaultState(), "s1"));
	state = ok(openSession(state, "s2"));
	const tabId = findSessionTab(state, "s2").tab.id;
	state = ok(closeTab(state, tabId));
	assert.equal(state.view.groups.g1.tabs.length, 1);
	assert.equal(state.view.groups.g1.selectedTabId, "session:s1");
});

test("closing and reopening a session tab restores focus without a draft", () => {
	let state = ok(openSession(defaultState(), "s1"));
	state = ok(openSession(state, "s2"));
	state = ok(closeTab(state, findSessionTab(state, "s2").tab.id));
	state = ok(openSession(state, "s2"));
	const found = findSessionTab(state, "s2");
	assert.ok(found);
	assert.equal(found.groupId, "g1");
	assert.equal(state.view.groups.g1.selectedTabId, "session:s2");
	assert.equal(state.view.groups.g1.tabs.length, 2);
	assert.equal(
		state.view.groups.g1.tabs.some((tab) => tab.kind === "draft"),
		false,
		"a closed/reopened session must not leave a draft tab behind"
	);
});

test("openDraft is idempotent and replaceDraft preserves mode", () => {
	let state = ok(openDraft(defaultState(), { draftId: "d1", workDir: "/tmp" }));
	state = ok(openDraft(state, { draftId: "d1" }));
	assert.equal(state.view.groups.g1.tabs.length, 1);
	const draftTab = state.view.groups.g1.tabs[0];
	state = ok(setTabMode(state, draftTab.id, "trajectory"));
	state = ok(replaceDraft(state, "d1", "s1"));
	const session = findSessionTab(state, "s1");
	assert.equal(session.tab.mode, "trajectory");
});

test("splitGroup moves a tab and removeGroup folds the tree", () => {
	let state = ok(openSession(defaultState(), "s1"));
	state = ok(openSession(state, "s2"));
	const tabId = findSessionTab(state, "s2").tab.id;
	state = ok(splitGroup(state, "g1", { direction: "horizontal", side: "after" }, tabId));
	assert.equal(state.frame.center.kind, "split");
	const newGroup = state.frame.center.children[1].id;
	assert.equal(state.view.groups[newGroup].tabs.length, 1);
	state = ok(removeGroup(state, newGroup));
	assert.equal(state.frame.center.kind, "group");
	assert.equal(state.view.groups[state.frame.center.id].tabs.length, 2);
});

test("resizeSplit clamps and unknown split fails", () => {
	let state = ok(openSession(defaultState(), "s1"));
	const tabId = findSessionTab(state, "s1").tab.id;
	state = ok(splitGroup(state, "g1", { direction: "vertical", side: "after" }, tabId));
	const splitId = state.frame.center.id;
	const resized = ok(resizeSplit(state, splitId, [10, -2]));
	assert.ok(resized.frame.center.weights[0] >= 0.1);
	assert.ok(resized.frame.center.weights[0] <= 0.9);
	assert.equal(resizeSplit(state, "missing", [0.5, 0.5]).ok, false);
});

test("setPreset preserves tab modes and all tabs", () => {
	let state = ok(openSession(defaultState(), "s1"));
	state = ok(openSession(state, "s2"));
	const s1 = findSessionTab(state, "s1").tab.id;
	state = ok(setTabMode(state, s1, "trajectory"));
	state = ok(setPreset(state, BUILTIN_PRESETS.find((p) => p.id === "quad").frame));
	assert.equal(Object.values(state.view.groups).reduce((n, g) => n + g.tabs.length, 0), 2);
	assert.ok(Object.values(state.view.groups).some((group) => group.tabs.some((tab) => tab.mode === "trajectory")));
});

test("surface tab limit is enforced", () => {
	let state = ok(splitGroup(defaultState(), "g1", { direction: "horizontal", side: "after" }));
	const groups = Object.keys(state.view.groups);
	for (let i = 0; i < MAX_TABS_PER_SURFACE; i += 1) {
		const groupId = groups[Math.floor(i / 16)];
		const result = openSession(state, `s${i}`, groupId);
		assert.equal(result.ok, true, `tab ${i}`);
		state = result.state;
	}
	assert.equal(openSession(state, "overflow", groups[0]).ok, false);
});

test("reconcileSessions removes deleted sessions", () => {
	let state = ok(openSession(defaultState(), "s1"));
	state = ok(openSession(state, "s2"));
	state = ok(reconcileSessions(state, ["s1"]));
	assert.equal(findSessionTab(state, "s1"), null);
	assert.ok(state.view.groups.g1.tabs.length === 1);
});

test("normalize repairs duplicate sessions and invalid mode", () => {
	const result = normalizeWorkbench({
		version: 1,
		frame: { version: 1, center: { kind: "group", id: "g1" } },
		view: { groups: { g1: { tabs: [
			{ kind: "session", id: "session:a", sessionId: "a", mode: "bogus" },
			{ kind: "session", id: "session:a", sessionId: "a" },
			{ kind: "draft", id: "draft:d", draftId: "d" }
		], selectedTabId: "session:a" } } },
		attention: { focusedGroupId: "missing", lastFocusedGroupId: "g1" }
	});
	assert.equal(result.state.view.groups.g1.tabs.length, 2);
	assert.equal(result.state.view.groups.g1.tabs[0].mode, "chat");
	assert.equal(result.state.attention.focusedGroupId, "g1");
});

test("serialized workbench restores tabs and focus with a top-level version", () => {
	let state = ok(openSession(defaultState(), "s1"));
	state = ok(openSession(state, "s2"));
	const text = serializeWorkbench(state);
	const doc = JSON.parse(text);
	assert.equal(doc.version, 1);
	const restored = decodeWorkbench(doc);
	assert.equal(restored.view.groups.g1.tabs.length, 2);
	assert.equal(restored.view.groups.g1.selectedTabId, "session:s2");
});

test("splitGroup moves the selected tab into the new group and fixes source selection", () => {
	let state = ok(openSession(defaultState(), "s1"));
	state = ok(openSession(state, "s2"));
	assert.equal(state.view.groups.g1.selectedTabId, "session:s2");
	const result = splitGroup(state, "g1", { direction: "horizontal", side: "after" }, "session:s2");
	assert.equal(result.ok, true);
	state = result.state;
	assert.equal(state.view.groups.g1.tabs.length, 1);
	assert.equal(state.view.groups.g1.tabs[0].sessionId, "s1");
	assert.equal(state.view.groups.g1.selectedTabId, "session:s1");
	assert.equal(state.view.groups.g2.tabs.length, 1);
	assert.equal(state.view.groups.g2.tabs[0].sessionId, "s2");
	assert.equal(state.view.groups.g2.selectedTabId, "session:s2");
});

test("moveTab across groups leaves the destination tab selected", () => {
	let state = ok(openSession(defaultState(), "s1"));
	state = ok(openSession(state, "s2"));
	state = ok(splitGroup(state, "g1", { direction: "horizontal" }));
	state = ok(moveTab(state, "session:s1", "g2", 0));
	assert.deepEqual(state.view.groups.g2.tabs.map((tab) => tab.sessionId), ["s1"]);
	assert.equal(state.view.groups.g2.selectedTabId, "session:s1");
	assert.equal(state.view.groups.g1.tabs.length, 1);
});

test("draft trajectory mode survives serialization", () => {
	let state = ok(openDraft(defaultState(), { draftId: "d1", workDir: "/tmp" }));
	const draft = state.view.groups.g1.tabs[0];
	state = ok(setTabMode(state, draft.id, "trajectory"));
	const restored = decodeWorkbench(JSON.parse(serializeWorkbench(state)));
	assert.equal(restored.view.groups.g1.tabs[0].mode, "trajectory");
});

test("removing the focused group repairs both attention ids", () => {
	let state = ok(splitGroup(defaultState(), "g1", { direction: "horizontal" }));
	const focused = state.attention.focusedGroupId;
	const remaining = Object.keys(state.view.groups).find((id) => id !== focused);
	state = ok(removeGroup(state, focused));
	assert.equal(state.attention.focusedGroupId, remaining);
	assert.equal(state.attention.lastFocusedGroupId, remaining);
});

test("setPreset keeps the focused session's tab focused", () => {
	let state = defaultState();
	for (const id of ["s1", "s2", "s3", "s4"]) state = ok(openSession(state, id));
	state = ok(setPreset(state, BUILTIN_PRESETS.find((preset) => preset.id === "quad").frame));
	state = ok(selectTab(state, "g4", "session:s4"));
	state = ok(setPreset(state, BUILTIN_PRESETS.find((preset) => preset.id === "two-column").frame));
	const focused = state.attention.focusedGroupId;
	assert.equal(state.view.groups[focused].selectedTabId, "session:s4");
});

test("setPreset never leaves focus on a group outside the new frame", () => {
	let state = ok(splitGroup(defaultState(), "g1", { direction: "horizontal" }));
	state = ok(openSession(state, "s1", "g2"));
	assert.equal(state.attention.focusedGroupId, "g2");
	state = ok(setPreset(state, BUILTIN_PRESETS.find((preset) => preset.id === "single").frame));
	assert.equal(state.attention.focusedGroupId, "g1");
	assert.equal(state.attention.lastFocusedGroupId, "g1");
});

test("normalizeWeights clamps both sides to the documented bounds", () => {
	const [left, right] = normalizeWeights([0.99, 0.01]);
	assert.equal(left, 0.9);
	assert.ok(right >= 0.1 && right <= 0.9);
	assert.equal(left + right, 1);
});
