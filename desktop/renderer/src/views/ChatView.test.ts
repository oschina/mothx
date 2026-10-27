import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';

const chatView = await readFile(new URL('./ChatView.tsx', import.meta.url), 'utf8');
const transcript = await readFile(new URL('../core/transcript.ts', import.meta.url), 'utf8');
const state = await readFile(new URL('../core/state.ts', import.meta.url), 'utf8');

test('active plan panel is derived from the transcript, not from a separate store or localStorage', () => {
  assert.match(
    chatView,
    /let plan: PlanItem \| undefined;[\s\S]*?state\.transcript\.length - 1/,
    'ChatView must locate the active plan by scanning the current transcript',
  );
  assert.match(
    chatView,
    /item\.kind === 'plan'[\s\S]*?item\.entries\.length > 0/,
    'active plan must be the most recent plan item with at least one step',
  );
  assert.doesNotMatch(
    chatView,
    /localStorage|desktop\.storeGet|desktop\.storeSet/,
    'plan panel collapse and content must not be persisted in localStorage or the Desktop store',
  );
});

test('plan panel collapse state is ephemeral React state and resets on session or active plan change', () => {
  assert.ok(chatView.includes("useState(false)"), 'collapse state must be a local React useState, not persisted');
  assert.ok(chatView.includes("setPlanCollapsed(false)"), 'collapse state must reset when dependencies change');
  assert.ok(
    chatView.includes("[chat.activeSessionId, activePlan?.key]"),
    'collapse state must reset when the active session or active plan changes',
  );
});

test('plan panel is keyboard accessible and uses ARIA for collapse and progress', () => {
  assert.match(chatView, /aria-expanded=\{!collapsed\}/, 'collapse toggle must expose expanded state');
  assert.match(chatView, /role=\"progressbar\"/, 'progress bar must be announced as a progressbar');
  assert.match(chatView, /aria-valuenow=\{percent\}/, 'progress bar must communicate current value');
  assert.match(
    chatView,
    /aria-label=\{collapsed \? t\('chat\.planExpand'\) : t\('chat\.planCollapse'\)\}/,
    'collapse toggle must have a contextual accessibility label',
  );
  assert.match(
    chatView,
    /aria-label=\{t\('chat\.plan', \{ n: total \}\)\}/,
    'step list must use a localized, parameterized label',
  );
});

test('plan item type supports optional ACP _meta.mothx.dev title and note projection', () => {
  assert.ok(
    state.includes("title?: string") && state.includes("note?: string"),
    'PlanItem type must allow optional title and note from ACP _meta',
  );
  assert.match(
    transcript,
    /case 'plan':[\s\S]*?meta\?\.title[\s\S]*?meta\?\.note/,
    'plan projection must read _meta.mothx.dev title and note when present',
  );
});

test('active plan panel is responsive and non-obstructive on narrow viewports', () => {
  assert.match(
    chatView,
    /hidden[\s\S]*?xl:flex/,
    'panel must hide on smaller screens to avoid overlapping the chat stream',
  );
  assert.match(
    chatView,
    /absolute top-\[58px\] right-4/,
    'panel must be positioned below the chat header on the right side',
  );
  assert.match(
    chatView,
    /max-h-\[calc\(100%-170px\)\]/,
    'panel must be constrained above the composer/footer area',
  );
});
