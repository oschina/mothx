import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const editorPath = new URL('../components/MCPConfigEditor.svelte', import.meta.url);
const stylePath = new URL('../style.css', import.meta.url);

const editorSource = await readFile(editorPath, 'utf8');
const globalStyle = await readFile(stylePath, 'utf8');

test('MCP editor uses theme tokens for its surfaces and controls', () => {
  // The empty state is an inline placeholder, not a modal: it deliberately
  // uses flat surface tokens instead of --modal-shadow/--overlay.
  for (const token of ['--bg-secondary', '--border', '--border-subtle', '--text', '--text-secondary', '--text-muted']) {
    assert.match(editorSource, new RegExp(`var\\(${token.replaceAll('-', '\\-')}\\)`), `editor should use ${token}`);
  }
  assert.doesNotMatch(editorSource, /background:\s*#[0-9a-f]{3,8}/i, 'editor should not hardcode background colors');
  assert.doesNotMatch(editorSource, /color:\s*#[0-9a-f]{3,8}/i, 'editor should not hardcode text colors');
});

test('global theme defines MCP editor tokens in both light and dark modes', () => {
  assert.match(globalStyle, /:root\s*\{/);
  assert.match(globalStyle, /:root\[data-theme="dark"\]\s*\{/);
  for (const token of ['--bg', '--bg-secondary', '--border', '--border-subtle', '--text-secondary', '--text-muted', '--modal-shadow', '--overlay']) {
    const occurrences = globalStyle.match(new RegExp(`${token.replaceAll('-', '\\-')}\\s*:`, 'g')) || [];
    assert.ok(occurrences.length >= 2, `${token} should be defined for light and dark themes`);
  }
});

import { resolveEffectiveTheme } from './preferences.js';

test('theme resolution switches explicit modes and follows system preference in auto mode', () => {
  assert.equal(resolveEffectiveTheme('light', true), 'light');
  assert.equal(resolveEffectiveTheme('dark', false), 'dark');
  assert.equal(resolveEffectiveTheme('auto', true), 'dark');
  assert.equal(resolveEffectiveTheme('auto', false), 'light');
});
