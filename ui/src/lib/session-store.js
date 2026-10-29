import { derived } from "svelte/store";
import { sessionRunStates } from "./session-runs.js";

const cache = new Map();

// sessionStateStore returns a derived store that only notifies subscribers when
// the selected session's state reference changes. This keeps one pane from
// re-rendering for every other session's stream events.
export function sessionStateStore(sessionId) {
	if (!sessionId) return derived(sessionRunStates, () => null);
	if (!cache.has(sessionId)) {
		cache.set(sessionId, derived(sessionRunStates, ($states) => $states[sessionId] || null));
	}
	return cache.get(sessionId);
}

export function forgetSessionStateStore(sessionId) {
	cache.delete(sessionId);
}

export function clearSessionStateStores() {
	cache.clear();
}
