// Terminal status projection shared by the xterm dock and its unit tests.
//
// The PTY is a direct shell on the serve host, not a sandboxed Agent tool. The
// UI must always disclose that it shares the session work directory, and it
// must make the warning stronger when the canonical Runtime snapshot says the
// session is executing locally or in another process.

const EXTERNAL_STATES = new Set(["external", "detached_remote", "reserved"]);
const ACTIVE_STATES = new Set(["local", "orphaned", "recovery_failed", "inconsistent", "unknown"]);

export function terminalAgentWarningKey(execution) {
	if (!execution || typeof execution !== "object") return "terminal.agentWarning.shared";
	const state = String(execution.state || "").trim();
	if (EXTERNAL_STATES.has(state)) return "terminal.agentWarning.external";
	if (execution.busy || execution.running || ACTIVE_STATES.has(state)) return "terminal.agentWarning.local";
	return "terminal.agentWarning.shared";
}

// A browser reload keeps sessionStorage but loses the in-memory terminal id.
// Reattach only when the server still reports a live terminal for this exact
// id; a stale id from a restarted serve process must be discarded.
export function restorableTerminal(terminals, terminalId) {
	if (!terminalId || !Array.isArray(terminals)) return null;
	const live = new Set(["starting", "running", "detached"]);
	return terminals.find((item) => item?.terminalId === terminalId && live.has(String(item?.state || ""))) || null;
}
