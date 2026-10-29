import assert from "node:assert/strict";
import test from "node:test";
import { createSessionRuntimeManager, displayModeFromSnapshot } from "./chat-runtime-state.js";

function harness(overrides = {}) {
	let runtime = null;
	let snapshots = [];
	const calls = { patched: 0, persisted: 0, errors: [] };
	const deps = {
		store: {
			get: () => runtime,
			set: (next) => { runtime = next; }
		},
		currentSession: () => "session-a",
		runLifecycle: () => 1,
		isBusy: () => false,
		getSessionRuntime: async () => ({ sessionId: "session-a", mode: "yolo", displayMode: "work" }),
		patchSessionRuntime: async (id, patch) => {
			calls.patched += 1;
			return { sessionId: id, mode: patch.mode || "yolo", displayMode: patch.displayMode || "work" };
		},
		getSessionTools: () => ({}),
		setSessionTools: () => {},
		upsertSession: () => {},
		refreshSessions: async () => {},
		onError: (err) => calls.errors.push(err),
		onSnapshot: (next) => snapshots.push(next),
		onDisplayMode: () => {},
		onNewSessionMode: () => {},
		onUpdatingChange: () => {},
		...overrides
	};
	return { deps, calls, snapshots, get runtime() { return runtime; } };
}

test("load publishes the canonical snapshot into the injected store", async () => {
	const h = harness();
	const manager = createSessionRuntimeManager(h.deps);
	await manager.load("session-a");
	assert.equal(h.runtime?.sessionId, "session-a");
	assert.equal(h.snapshots.length, 1);
});

test("update applies the optimistic snapshot and keeps the authoritative response", async () => {
	const h = harness();
	const manager = createSessionRuntimeManager(h.deps);
	await manager.update({ mode: "plan" });
	assert.equal(h.runtime?.mode, "plan");
	assert.equal(h.calls.patched, 1);
});

test("update rolls back to the previous snapshot when the patch fails", async () => {
	const h = harness({
		patchSessionRuntime: async () => { throw new Error("patch failed"); }
	});
	const manager = createSessionRuntimeManager(h.deps);
	h.deps.store.set({ sessionId: "session-a", mode: "agent", displayMode: "work" });
	await manager.update({ mode: "plan" });
	assert.equal(h.runtime?.mode, "agent");
	assert.equal(h.calls.errors.length, 1);
});

test("a stale load cannot overwrite a newer mutation", async () => {
	let resolveLoad;
	const h = harness({
		getSessionRuntime: () => new Promise((resolve) => { resolveLoad = resolve; })
	});
	const manager = createSessionRuntimeManager(h.deps);
	h.deps.store.set({ sessionId: "session-a", mode: "agent", displayMode: "work" });
	const pending = manager.load("session-a");
	await manager.update({ mode: "plan" });
	resolveLoad({ sessionId: "session-a", mode: "agent", displayMode: "work" });
	await pending;
	assert.equal(h.runtime?.mode, "plan");
});

test("display mode normalization collapses unknown modes to work", () => {
	assert.equal(displayModeFromSnapshot({ displayMode: "code" }), "code");
	assert.equal(displayModeFromSnapshot({ displayMode: "work" }), "work");
	assert.equal(displayModeFromSnapshot(null), "work");
});
