import {
	MAX_CENTER_GROUPS,
	MAX_ID_LENGTH,
	MAX_TABS_PER_PANE,
	MAX_TABS_PER_SURFACE,
	collectLeaves,
	defaultState,
	findGroup,
	normalizeWeights
} from "./model.js";

export const WORKBENCH_VERSION = 1;

export function decodeWorkbench(raw) {
	return normalizeWorkbench(raw).state;
}

export function normalizeWorkbench(raw) {
	const warnings = [];
	if (!raw || typeof raw !== "object" || raw.version !== WORKBENCH_VERSION) {
		return { state: defaultState(), warnings: ["version"] };
	}
	const center = normalizeNode(raw.frame?.center, new Set(), warnings, 0);
	if (!center || center.kind !== "group" && center.kind !== "split") {
		return { state: defaultState(), warnings: [...warnings, "frame"] };
	}
	const leaves = collectLeaves(center);
	if (leaves.length < 1 || leaves.length > MAX_CENTER_GROUPS) {
		return { state: defaultState(), warnings: [...warnings, "leaves"] };
	}
	const groups = {};
	const seenSessions = new Set();
	const seenDrafts = new Set();
	let totalTabs = 0;
	for (const leaf of leaves) {
		const rawGroup = raw.view?.groups?.[leaf.id] || {};
		const tabs = [];
		for (const rawTab of Array.isArray(rawGroup.tabs) ? rawGroup.tabs : []) {
			const tab = normalizeTab(rawTab, seenSessions, seenDrafts, warnings);
			if (!tab) continue;
			if (tabs.length >= MAX_TABS_PER_PANE || totalTabs >= MAX_TABS_PER_SURFACE) {
				warnings.push("tab-limit");
				break;
			}
			tabs.push(tab);
			totalTabs += 1;
		}
		groups[leaf.id] = {
			tabs,
			selectedTabId: tabs.some((tab) => tab.id === rawGroup.selectedTabId)
				? rawGroup.selectedTabId
				: tabs[0]?.id
		};
	}
	const focused = findGroup(center, raw.attention?.focusedGroupId) ? raw.attention.focusedGroupId : leaves[0].id;
	const last = findGroup(center, raw.attention?.lastFocusedGroupId) ? raw.attention.lastFocusedGroupId : focused;
	return {
		state: {
			frame: { version: WORKBENCH_VERSION, center },
			view: { groups },
			attention: { focusedGroupId: focused, lastFocusedGroupId: last }
		},
		warnings
	};
}

function normalizeNode(node, seen, warnings, depth) {
	if (!node || depth > 8 || typeof node !== "object") {
		warnings.push("frame-node");
		return null;
	}
	if (node.kind === "group") {
		const id = cleanID(node.id);
		if (!id || seen.has(id)) {
			warnings.push("group-id");
			return null;
		}
		seen.add(id);
		return { kind: "group", id };
	}
	if (node.kind !== "split") return null;
	const id = cleanID(node.id);
	if (!id || seen.has(id)) {
		warnings.push("split-id");
		return null;
	}
	seen.add(id);
	const left = normalizeNode(node.children?.[0], seen, warnings, depth + 1);
	const right = normalizeNode(node.children?.[1], seen, warnings, depth + 1);
	if (!left || !right) {
		warnings.push("split-empty");
		return null;
	}
	const [a, b] = normalizeWeights(node.weights);
	return {
		kind: "split",
		id,
		direction: node.direction === "vertical" ? "vertical" : "horizontal",
		weights: [a, b],
		children: [left, right]
	};
}

function normalizeTab(raw, seenSessions, seenDrafts, warnings) {
	if (!raw || typeof raw !== "object") return null;
	const mode = raw.mode === "trajectory" ? "trajectory" : "chat";
	if (raw.kind === "session") {
		const sessionId = cleanID(raw.sessionId);
		if (!sessionId || seenSessions.has(sessionId)) return null;
		seenSessions.add(sessionId);
		return { kind: "session", id: `session:${sessionId}`, sessionId, mode };
	}
	if (raw.kind === "draft") {
		const draftId = cleanID(raw.draftId);
		if (!draftId || seenDrafts.has(draftId)) return null;
		seenDrafts.add(draftId);
		return {
			kind: "draft",
			id: `draft:${draftId}`,
			draftId,
			workDir: cleanText(raw.workDir),
			model: cleanText(raw.model),
			provider: cleanText(raw.provider),
			expertId: cleanText(raw.expertId),
			mode
		};
	}
	warnings.push("tab-kind");
	return null;
}

function cleanID(value) {
	const text = typeof value === "string" ? value.trim() : "";
	return text && text.length <= MAX_ID_LENGTH ? text : "";
}

function cleanText(value) {
	const text = typeof value === "string" ? value.trim() : "";
	return text.length <= MAX_ID_LENGTH ? text : "";
}

export function encodeWorkbench(state) {
	const normalized = normalizeWorkbench({
		version: WORKBENCH_VERSION,
		frame: state?.frame,
		view: state?.view,
		attention: state?.attention
	});
	return normalized.state;
}

export function serializeWorkbench(state) {
	const doc = encodeWorkbench(state);
	// normalizeWorkbench()/decodeWorkbench() require a top-level version; the
	// normalized state itself deliberately does not carry it.
	const text = JSON.stringify({ version: WORKBENCH_VERSION, ...doc });
	return text.length <= 64 * 1024 ? text : "";
}
