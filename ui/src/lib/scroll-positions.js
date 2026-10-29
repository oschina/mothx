// Runtime-only scroll positions for chat panes. The workbench unmounts a
// ChatPane when the user switches tabs, so a position has to live outside the
// component to survive the switch. This is presentation state (like an
// expanded tree row), not a durable fact, and is never persisted to the
// server or localStorage.

const LIMIT = 64;
const positions = new Map();

/**
 * @param {string} sessionId
 * @param {{ top: number, following: boolean }} position
 */
export function saveScrollPosition(sessionId, position) {
	if (!sessionId || !position) return;
	const top = Number(position.top);
	if (!Number.isFinite(top) || top < 0) return;
	positions.delete(sessionId);
	positions.set(sessionId, { top, following: Boolean(position.following) });
	while (positions.size > LIMIT) {
		const oldest = positions.keys().next().value;
		if (oldest === undefined) break;
		positions.delete(oldest);
	}
}

/** @param {string} sessionId */
export function getScrollPosition(sessionId) {
	if (!sessionId) return null;
	return positions.get(sessionId) || null;
}

/** @param {string} sessionId */
export function forgetScrollPosition(sessionId) {
	if (sessionId) positions.delete(sessionId);
}

export function clearScrollPositions() {
	positions.clear();
}

export function scrollPositionCount() {
	return positions.size;
}
