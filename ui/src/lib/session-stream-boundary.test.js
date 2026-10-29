import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const root = new URL("../", import.meta.url).pathname;

test("session-stream is the only /ws/runs consumer", () => {
	const pane = readFileSync(`${root}components/chat/ChatPane.svelte`, "utf8");
	assert.equal(/\$runEvents/.test(pane), false, "ChatPane must not subscribe to $runEvents");
	assert.equal(/handleSessionStreamEvent/.test(pane), false, "ChatPane must not own stream reduction");
	assert.equal(/readSSE/.test(pane), false, "ChatPane must not own SSE fallback");
	const dispatcher = readFileSync(`${root}lib/session-stream.js`, "utf8");
	assert.match(dispatcher, /runEvents\.subscribe/);
	assert.match(dispatcher, /startObserver/);
});

test("pane components never abort another session's observer on destroy", () => {
	const pane = readFileSync(`${root}components/chat/ChatPane.svelte`, "utf8");
	assert.equal(/observer\?\.controller\?\.abort/.test(pane), false);
	assert.equal(/stopObserver\(prevSession\)/.test(pane), false);
});

test("ChatPane renders canonical session state instead of persisting local projections", () => {
	const pane = readFileSync(`${root}components/chat/ChatPane.svelte`, "utf8");
	assert.equal(/persistLocalSessionState/.test(pane), false, "local view must not be copied back into the store");
	assert.equal(/restoreLocalSessionState/.test(pane), false, "local view must not be restored from the store");
	assert.equal(/let messages = \[\]/.test(pane), false, "transcript must derive from sessionRunStates");
	assert.match(pane, /sessionStateStore\(sessionID\)/);
	assert.match(pane, /\$: messages = view\.messages;/);
});
