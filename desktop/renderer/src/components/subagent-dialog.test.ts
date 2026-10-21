import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';

const dialog = await readFile(new URL('./SubagentDialog.tsx', import.meta.url), 'utf8');
const chatView = await readFile(new URL('../views/ChatView.tsx', import.meta.url), 'utf8');
const i18n = await readFile(new URL('../core/i18n.ts', import.meta.url), 'utf8');

test('sub-agent panel reuses the chat transcript and has no composer', () => {
  assert.ok(dialog.includes("import { TranscriptEntry } from '@/components/TranscriptItems'"), 'must render child messages with the ordinary transcript entries');
  assert.ok(dialog.includes('<TranscriptEntry key={item.key} item={item} />'), 'child transcript items must be rendered by TranscriptEntry');
  assert.doesNotMatch(dialog, /<Composer|sendPrompt|acp\.respond|session\/prompt/, 'the sub-agent panel is read-only');
});

test('chat header exposes the sub-agent count only when ACP advertises the capability', () => {
  assert.match(chatView, /hasFeature\('subagentPanel'\)/, 'the entry point must be capability gated');
  assert.match(chatView, /<SubagentSummaryButton sessionId=\{appState\.activeSessionId\}/, 'the header must project the active session sub-agent list');
  assert.match(chatView, /<SubagentDialog open=\{subagentsOpen\}/, 'clicking the summary opens the read-only dialog');
});

test('sub-agent panel strings exist in both locales', () => {
  for (const key of ['chat.subagents.title', 'chat.subagents.running', 'chat.subagents.readonly', 'chat.subagents.status.completed']) {
    const occurrences = i18n.split(`'${key}'`).length - 1;
    assert.equal(occurrences, 2, `${key} must be translated in zh and en`);
  }
});
