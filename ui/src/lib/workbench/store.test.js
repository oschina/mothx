import assert from "node:assert/strict";
import test from "node:test";
import { get } from "svelte/store";

import { WORKBENCH_STORAGE_KEY } from "./persistence.js";

function installStorage(initialDocument) {
	const data = new Map();
	if (initialDocument) data.set(WORKBENCH_STORAGE_KEY, JSON.stringify(initialDocument));
	globalThis.localStorage = {
		getItem(key) {
			return data.has(key) ? data.get(key) : null;
		},
		setItem(key, value) {
			data.set(key, String(value));
		},
		removeItem(key) {
			data.delete(key);
		},
		clear() {
			data.clear();
		}
	};
	return data;
}

function documentWithSession(sessionId) {
	return {
		version: 1,
		frame: { version: 1, center: { kind: "group", id: "g1" } },
		view: {
			groups: {
				g1: {
					tabs: [{ kind: "session", id: `session:${sessionId}`, sessionId, mode: "chat" }],
					selectedTabId: `session:${sessionId}`
				}
			}
		},
		attention: { focusedGroupId: "g1", lastFocusedGroupId: "g1" }
	};
}

test("hydration does not discard a draft created before the persistence debounce", async () => {
	installStorage(documentWithSession("session-a"));
	const store = await import("./store.js?case=draft-before-hydrate");
	assert.equal(get(store.workbench).view.groups.g1.tabs[0].sessionId, "session-a");

	store.openDraftInWorkbench({ draftId: "draft-race", workDir: "/tmp/race" });
	// The 300ms persistence debounce has not fired, so storage still holds the
	// startup document while memory already holds the new Draft tab.
	assert.equal(globalThis.localStorage.getItem(WORKBENCH_STORAGE_KEY).includes("draft-race"), false);

	store.hydrateWorkbench();
	const tabs = get(store.workbench).view.groups.g1.tabs;
	assert.deepEqual(tabs.map((tab) => tab.id), ["session:session-a", "draft:draft-race"]);
	store.persistWorkbenchNow();
	assert.equal(globalThis.localStorage.getItem(WORKBENCH_STORAGE_KEY).includes("draft-race"), true);
});

test("the first startup hydration still applies a newer shared layout document", async () => {
	installStorage(documentWithSession("session-a"));
	const store = await import("./store.js?case=first-hydrate-newer");

	// Simulate another page committing a layout between module load and the
	// first app-start hydration. Last-write-wins remains observable once.
	globalThis.localStorage.setItem(WORKBENCH_STORAGE_KEY, JSON.stringify(documentWithSession("session-b")));
	store.hydrateWorkbench();
	assert.equal(get(store.workbench).view.groups.g1.tabs[0].sessionId, "session-b");
});

test("later hydration is a no-op once the workbench has started", async () => {
	installStorage(documentWithSession("session-a"));
	const store = await import("./store.js?case=one-shot");
	store.openSessionInWorkbench("session-b");
	store.persistWorkbenchNow();

	// Another page writes while /chat is unmounted. A remount must not silently
	// replace this page's authoritative in-memory layout.
	globalThis.localStorage.setItem(WORKBENCH_STORAGE_KEY, JSON.stringify(documentWithSession("session-c")));
	store.hydrateWorkbench();
	const tabs = get(store.workbench).view.groups.g1.tabs;
	assert.deepEqual(tabs.map((tab) => tab.sessionId), ["session-a", "session-b"]);
});
