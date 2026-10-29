import assert from "node:assert/strict";
import test from "node:test";
import {
	MAX_CENTER_GROUPS,
	MAX_TABS_PER_PANE,
	MAX_TABS_PER_SURFACE,
	closeTab,
	collectLeaves,
	collectTabs,
	defaultState,
	findGroup,
	findTab,
	moveTab,
	openDraft,
	openSession,
	removeGroup,
	resizeSplit,
	setPreset,
	setTabMode,
	splitGroup
} from "./model.js";
import { BUILTIN_PRESETS } from "./presets.js";
import { decodeWorkbench, serializeWorkbench } from "./normalize.js";

// Deterministic xorshift32 so a failing sequence can be replayed exactly.
function rng(seed) {
	let value = seed >>> 0;
	return () => {
		value ^= value << 13;
		value >>>= 0;
		value ^= value >>> 17;
		value ^= value << 5;
		value >>>= 0;
		return value / 0xffffffff;
	};
}

function pick(next, list) {
	return list.length ? list[Math.floor(next() * list.length) % list.length] : null;
}

function splitIDs(node, out = []) {
	if (!node) return out;
	if (node.kind === "split") {
		out.push(node.id);
		splitIDs(node.children?.[0], out);
		splitIDs(node.children?.[1], out);
	}
	return out;
}

function assertInvariants(state, label) {
	const leaves = collectLeaves(state.frame?.center);
	assert.ok(leaves.length >= 1 && leaves.length <= MAX_CENTER_GROUPS, `${label}: leaf count`);
	const leafIDs = new Set(leaves.map((leaf) => leaf.id));
	const frameIDs = new Set([...leafIDs, ...splitIDs(state.frame?.center)]);
	assert.equal(frameIDs.size, leafIDs.size + splitIDs(state.frame?.center).length, `${label}: duplicate node ids`);

	for (const leaf of leaves) assert.ok(findGroup(state.frame.center, leaf.id), `${label}: leaf missing`);
	for (const groupID of Object.keys(state.view.groups || {})) {
		assert.ok(leafIDs.has(groupID), `${label}: view group ${groupID} is not in the frame`);
	}
	for (const splitID of splitIDs(state.frame?.center)) {
		const findSplit = (node) => {
			if (!node) return null;
			if (node.kind === "group") return null;
			if (node.id === splitID) return node;
			return findSplit(node.children?.[0]) || findSplit(node.children?.[1]);
		};
		const node = findSplit(state.frame.center);
		assert.ok(node, `${label}: split ${splitID} missing`);
		assert.equal(node.children.length, 2, `${label}: split child count`);
		assert.ok(node.weights[0] >= 0.1 && node.weights[0] <= 0.9, `${label}: split lower weight`);
		assert.ok(node.weights[1] >= 0.1 && node.weights[1] <= 0.9, `${label}: split upper weight`);
		assert.ok(Math.abs(node.weights[0] + node.weights[1] - 1) < 1e-9, `${label}: split weight sum`);
	}

	const tabs = collectTabs(state);
	assert.ok(tabs.length <= MAX_TABS_PER_SURFACE, `${label}: surface tab limit`);
	assert.equal(new Set(tabs.map((tab) => tab.id)).size, tabs.length, `${label}: duplicate tab ids`);
	const sessionIDs = tabs.filter((tab) => tab.kind === "session").map((tab) => tab.sessionId);
	assert.equal(new Set(sessionIDs).size, sessionIDs.length, `${label}: duplicate session tabs`);
	const draftIDs = tabs.filter((tab) => tab.kind === "draft").map((tab) => tab.draftId);
	assert.equal(new Set(draftIDs).size, draftIDs.length, `${label}: duplicate draft tabs`);
	for (const leaf of leaves) {
		const view = state.view.groups[leaf.id];
		assert.ok(view, `${label}: missing view for ${leaf.id}`);
		assert.ok((view.tabs || []).length <= MAX_TABS_PER_PANE, `${label}: pane tab limit`);
		if ((view.tabs || []).length === 0) {
			assert.ok(!view.selectedTabId, `${label}: empty pane has a selected tab`);
		} else {
			assert.ok(view.tabs.some((tab) => tab.id === view.selectedTabId), `${label}: selected tab is absent`);
		}
		for (const tab of view.tabs || []) {
			assert.ok(tab.mode === "chat" || tab.mode === "trajectory", `${label}: invalid tab mode`);
		}
	}
	assert.ok(findGroup(state.frame.center, state.attention?.focusedGroupId), `${label}: focused group`);
	assert.ok(findGroup(state.frame.center, state.attention?.lastFocusedGroupId), `${label}: last focused group`);

	// The persisted document must round-trip without losing or duplicating tabs.
	const text = serializeWorkbench(state);
	assert.ok(text, `${label}: serialization produced an empty document`);
	const restored = decodeWorkbench(JSON.parse(text));
	assert.equal(collectLeaves(restored.frame.center).length, leaves.length, `${label}: restored leaf count`);
	assert.deepEqual(
		collectTabs(restored).map((tab) => [tab.id, tab.sessionId || "", tab.draftId || "", tab.mode]),
		tabs.map((tab) => [tab.id, tab.sessionId || "", tab.draftId || "", tab.mode]),
		`${label}: restored tabs`
	);
	assert.ok(findGroup(restored.frame.center, restored.attention.focusedGroupId), `${label}: restored focus`);
}

test("fixed-seed operation sequences preserve every workbench invariant", () => {
	const next = rng(0x5eed1234);
	let state = defaultState();
	let applied = 0;
	const counts = { open: 0, draft: 0, split: 0, close: 0, move: 0, remove: 0, resize: 0, mode: 0, preset: 0 };

	for (let step = 0; step < 1000; step += 1) {
		const leaves = collectLeaves(state.frame.center);
		const groups = leaves.map((leaf) => leaf.id);
		const splits = splitIDs(state.frame.center);
		const tabs = collectTabs(state);
		const tab = pick(next, tabs);
		const groupID = pick(next, groups);
		let result = null;
		let op = "";

		switch (Math.floor(next() * 10)) {
			case 0:
			case 1:
				op = "open";
				result = openSession(state, `s${Math.floor(next() * 6)}`, groupID);
				break;
			case 2:
				op = "draft";
				result = openDraft(state, { draftId: `d${Math.floor(next() * 3)}`, workDir: "/tmp" }, groupID);
				break;
			case 3:
				op = "split";
				result = splitGroup(state, groupID, {
					direction: next() < 0.5 ? "horizontal" : "vertical",
					side: next() < 0.5 ? "before" : "after"
				}, tab && next() < 0.5 ? tab.id : "");
				break;
			case 4:
				op = "close";
				if (tab) result = closeTab(state, tab.id);
				break;
			case 5:
				op = "move";
				if (tab) result = moveTab(state, tab.id, groupID, Math.floor(next() * 4));
				break;
			case 6:
				op = "remove";
				result = removeGroup(state, groupID);
				break;
			case 7:
				op = "resize";
				if (splits.length) result = resizeSplit(state, pick(next, splits), [next(), next()]);
				break;
			case 8:
				op = "mode";
				if (tab) result = setTabMode(state, tab.id, next() < 0.5 ? "chat" : "trajectory");
				break;
			default:
				op = "preset";
				result = setPreset(state, pick(next, BUILTIN_PRESETS).frame);
				break;
		}
		if (result?.ok) {
			state = result.state;
			applied += 1;
			counts[op] += 1;
			assertInvariants(state, `step ${step} (${op})`);
		}
	}

	assert.ok(applied > 300, `only ${applied} operations applied`);
	for (const [op, count] of Object.entries(counts)) assert.ok(count > 0, `${op} never applied: ${JSON.stringify(counts)}`);
	// The generated sequence must still exercise a split layout, not just one pane.
	assert.ok(splitIDs(state.frame.center).length > 0, "sequence ended without a split layout");
	assert.ok(findTab(state, collectTabs(state)[0]?.id), "final state lost its tabs");
});
