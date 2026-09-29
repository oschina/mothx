import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const composer = await readFile(new URL('./Composer.tsx', import.meta.url), 'utf8');
const homeView = await readFile(new URL('../views/HomeView.tsx', import.meta.url), 'utf8');
const chatView = await readFile(new URL('../views/ChatView.tsx', import.meta.url), 'utf8');

test('chat composer width stays identical to the home composer width', () => {
  const homeWidth = homeView.match(/w-\[min\(\d+px,\d+%\)\]/)?.[0];
  const chatWidth = chatView.match(/w-\[min\(\d+px,\d+%\)\]/)?.[0];
  assert.ok(homeWidth, 'home composer container must declare its width');
  assert.ok(chatWidth, 'chat composer container must declare its width');
  assert.equal(chatWidth, homeWidth, 'session composer must reuse the home composer width');
});

test('composer toolbar wraps instead of squeezing controls on narrow widths', () => {
  assert.match(composer, /flex min-w-0 flex-wrap items-center gap-1\.5/, 'composer bar must wrap gracefully instead of overflowing or squeezing');
});

test('toolbar controls keep a stable hit area and never shrink below it', () => {
  assert.match(composer, /min-h-7 max-w-60 min-w-0 shrink-0/, 'composer tool must keep the 28px hit area and its natural width');
  assert.match(composer, /size-8 shrink-0[\s\S]*?Send/, 'send button must keep its fixed square dimensions');
});

test('provider, model, workspace and expert labels truncate cleanly', () => {
  assert.match(composer, /\[&>span:not\(\.sr-only\)\]:truncate/, 'toolbar labels must truncate with ellipsis');
  assert.match(composer, /max-w-\[138px\]/, 'provider trigger must bound its width');
  assert.match(composer, /max-w-\[190px\]/, 'model and workspace triggers must bound their width');
});

test('both composer surfaces share one implementation with home/chat variants', () => {
  assert.match(composer, /export function Composer\(\{ source \}: \{ source: 'home' \| 'chat' \}\)/, 'one component must serve both toolbars');
  assert.match(composer, /isHome \? t\('home\.composerPlaceholder'\) : t\('chat\.inputPlaceholder'\)/, 'placeholders must stay scenario-specific');
  assert.match(composer, /const runningHere = isSessionRunning\(appState\.activeSessionId\);/, 'send gating must follow the active session run, not a global flag');
  assert.match(composer, /const sendDisabled = source === 'chat' && runningHere;/, 'a background task must never disable the new-task composer');
  assert.match(composer, /source === 'chat' && runningHere/, 'the stop control belongs to the active chat run only');
  assert.match(composer, /cancelRun\(\)/, 'stop must cancel through the canonical run action');
});

test('textarea keeps auto-grow, Enter-to-send, Shift+Enter newline, and image paste', () => {
  assert.match(composer, /Math\.min\(textarea\.scrollHeight, 200\)/, 'auto-grow must stay bounded at 200px');
  assert.match(composer, /event\.key === 'Enter' && !event\.shiftKey/, 'Enter sends, Shift+Enter keeps a newline');
  assert.match(composer, /attachPastedFiles\(files\)/, 'pasted images must attach through the core action');
});

test('attachments render as removable chips with embedded-state hints', () => {
  assert.match(composer, /removeAttachment\(attachment\.path\)/, 'chips must remove through the core action');
  assert.match(composer, /t\('attach\.outside'\)/, 'embedded out-of-workspace files must stay labeled');
  assert.match(composer, /formatBytes\(attachment\.size\)/, 'chip tooltip must show the file size');
});
