import assert from "node:assert/strict";
import test from "node:test";
import { runEvents } from "./stores.js";
import { getSessionState, sessionRunStates } from "./session-runs.js";
import { resetSessionStreamCursor, startSessionStreamDispatcher, stopSessionStreamDispatcher } from "./session-stream.js";

function event(wsSeq, sessionId, eventName, data) {
	return { wsSeq, type: "session_event", sessionId, event: eventName, data: typeof data === "string" ? data : JSON.stringify(data) };
}

test("dispatches interleaved transcript events once per wsSeq", () => {
	stopSessionStreamDispatcher();
	sessionRunStates.set({});
	resetSessionStreamCursor(0);
	runEvents.set([
		event(1, "s1", "transcript", { type: "message", sessionId: "s1", message: { id: "m1", seq: 1, role: "assistant", content: "one" } }),
		event(2, "s2", "transcript", { type: "message", sessionId: "s2", message: { id: "m2", seq: 1, role: "assistant", content: "two" } })
	]);
	startSessionStreamDispatcher();
	assert.equal(getSessionState("s1").messages.length, 1);
	assert.equal(getSessionState("s2").messages.length, 1);
	assert.equal(getSessionState("s1").messages[0].content, "one");

	// Re-setting the same wsSeq must not append again.
	runEvents.update((items) => [...items]);
	assert.equal(getSessionState("s1").messages.length, 1);
	stopSessionStreamDispatcher();
});

test("done increments history reload and stats effects", () => {
	stopSessionStreamDispatcher();
	sessionRunStates.set({});
	runEvents.set([]);
	resetSessionStreamCursor(0);
	startSessionStreamDispatcher();
	runEvents.set([event(1, "s1", "done", { sessionId: "s1" })]);
	const state = getSessionState("s1");
	assert.equal(state.effects.historyReloadEpoch, 1);
	assert.equal(state.effects.statsRefreshEpoch, 1);
	stopSessionStreamDispatcher();
});

test("background responses runs are polled without a mounted pane", async () => {
	stopSessionStreamDispatcher();
	sessionRunStates.set({});
	runEvents.set([]);
	resetSessionStreamCursor(0);
	const calls = [];
	const originalFetch = globalThis.fetch;
	globalThis.fetch = async (url) => {
		calls.push(String(url));
		return new Response(JSON.stringify({ localRunId: "r1", responseId: "resp1", state: "running" }), {
			status: 200,
			headers: { "Content-Type": "application/json" }
		});
	};
	try {
		startSessionStreamDispatcher();
		sessionRunStates.set({
			s1: {
				sessionId: "s1",
				messages: [],
				toolEvents: [],
				runEvents: [],
				capabilityEvents: [],
				runtime: { responsesRun: { localRunId: "r1", state: "running" } },
				cursor: { entrySeq: 0, runSeq: 0, capabilitySeq: 0 }
			}
		});
		for (let i = 0; i < 40 && !calls.some((url) => url.includes("/api/responses/runs/r1")); i += 1) {
			await new Promise((resolve) => setTimeout(resolve, 10));
		}
		assert.equal(calls.some((url) => url.includes("/api/responses/runs/r1/reconnect")), true);
		assert.equal(calls.some((url) => url.includes("/api/responses/runs/r1?session_id=s1")), true);
	} finally {
		stopSessionStreamDispatcher();
		globalThis.fetch = originalFetch;
	}
});
