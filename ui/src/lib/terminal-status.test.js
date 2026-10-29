import assert from "node:assert/strict";
import test from "node:test";
import { restorableTerminal, terminalAgentWarningKey } from "./terminal-status.js";

test("terminal warning is always shared and stronger while the Agent is busy", () => {
	assert.equal(terminalAgentWarningKey(null), "terminal.agentWarning.shared");
	assert.equal(terminalAgentWarningKey({ state: "idle", busy: false, running: false }), "terminal.agentWarning.shared");
	assert.equal(terminalAgentWarningKey({ state: "local", busy: true, running: true }), "terminal.agentWarning.local");
	assert.equal(terminalAgentWarningKey({ state: "recovery_failed", busy: false, running: false }), "terminal.agentWarning.local");
	assert.equal(terminalAgentWarningKey({ state: "external", busy: true, running: true }), "terminal.agentWarning.external");
	assert.equal(terminalAgentWarningKey({ state: "detached_remote", busy: true }), "terminal.agentWarning.external");
	assert.equal(terminalAgentWarningKey({ state: "reserved", busy: true }), "terminal.agentWarning.external");
});

test("only a live terminal id survives a reload", () => {
	const terminals = [
		{ terminalId: "t-old", sessionId: "s1", state: "exited" },
		{ terminalId: "t-live", sessionId: "s1", state: "detached" }
	];
	assert.equal(restorableTerminal(terminals, "t-live")?.terminalId, "t-live");
	assert.equal(restorableTerminal(terminals, "t-old"), null);
	assert.equal(restorableTerminal(terminals, "missing"), null);
	assert.equal(restorableTerminal(null, "t-live"), null);
});
