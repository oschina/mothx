// Presentation-only sizes for the workbench dock and plan panel. These are
// kept outside the workbench layout document so resizing a local panel never
// rewrites the shared multi-page layout state.
export const TERMINAL_SIZE_KEY = 'mothx.workbench.terminalHeight';
export const PLAN_SIZE_KEY = 'mothx.workbench.planWidth';

export const TERMINAL_MIN = 160;
export const PLAN_MIN = 260;
export const PLAN_MAX = 720;

export function clampTerminalHeight(value, containerHeight) {
	const height = Number(containerHeight) || 0;
	const next = Math.max(TERMINAL_MIN, Number(value) || 0);
	if (!(height > 0)) return Math.round(next);
	const max = Math.max(TERMINAL_MIN, Math.floor(height * 0.6 + 1e-6));
	return Math.round(Math.min(max, next));
}

export function clampPlanWidth(value, containerWidth) {
	const width = Number(containerWidth) || 0;
	const next = Math.max(PLAN_MIN, Number(value) || 0);
	if (!(width > 0)) return Math.round(next);
	const max = Math.max(PLAN_MIN, Math.min(PLAN_MAX, Math.floor(width * 0.7 + 1e-6)));
	return Math.round(Math.min(max, next));
}

export function readPanelSize(key, fallback) {
	try {
		const stored = Number(globalThis.localStorage?.getItem(key));
		return Number.isFinite(stored) && stored > 0 ? stored : fallback;
	} catch {
		return fallback;
	}
}

export function writePanelSize(key, value) {
	try {
		const size = Number(value);
		if (!Number.isFinite(size) || size <= 0) return;
		globalThis.localStorage?.setItem(key, String(Math.round(size)));
	} catch {
		// Resizing is presentation state; a blocked localStorage must not fail
		// the interaction.
	}
}
