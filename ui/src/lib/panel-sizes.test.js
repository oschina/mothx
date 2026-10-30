import test from 'node:test';
import assert from 'node:assert/strict';
import {
	PLAN_MAX,
	PLAN_MIN,
	PLAN_SIZE_KEY,
	TERMINAL_MIN,
	TERMINAL_SIZE_KEY,
	clampPlanWidth,
	clampTerminalHeight,
	readPanelSize,
	writePanelSize
} from './panel-sizes.js';

test('terminal height stays above its minimum and below 60% of the container', () => {
	assert.equal(clampTerminalHeight(40, 800), TERMINAL_MIN);
	assert.equal(clampTerminalHeight(900, 800), 480);
	assert.equal(clampTerminalHeight(320, 800), 320);
	assert.equal(clampTerminalHeight(320, 0), 320);
});

test('plan width respects the minimum, the configured maximum, and container share', () => {
	assert.equal(clampPlanWidth(100, 1200), PLAN_MIN);
	assert.equal(clampPlanWidth(900, 1200), PLAN_MAX);
	assert.equal(clampPlanWidth(420, 1200), 420);
	assert.equal(clampPlanWidth(600, 700), 490);
	assert.equal(clampPlanWidth(420, 300), PLAN_MIN);
});

test('panel sizes round-trip through localStorage and tolerate unavailable storage', () => {
	const previous = globalThis.localStorage;
	const values = new Map();
	globalThis.localStorage = {
		getItem: (key) => (values.has(key) ? values.get(key) : null),
		setItem: (key, value) => values.set(key, String(value))
	};
	try {
		writePanelSize(PLAN_SIZE_KEY, 421.6);
		assert.equal(values.get(PLAN_SIZE_KEY), '422');
		assert.equal(readPanelSize(PLAN_SIZE_KEY, 320), 422);
		assert.equal(readPanelSize('missing', 320), 320);
	} finally {
		globalThis.localStorage = previous;
	}

	const previousMissing = globalThis.localStorage;
	delete globalThis.localStorage;
	try {
		assert.equal(readPanelSize(TERMINAL_SIZE_KEY, 300), 300);
		assert.doesNotThrow(() => writePanelSize(TERMINAL_SIZE_KEY, 300));
	} finally {
		globalThis.localStorage = previousMissing;
	}
});
