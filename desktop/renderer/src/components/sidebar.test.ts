import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const sidebar = await readFile(new URL('./Sidebar.tsx', import.meta.url), 'utf8');
const scrollbarHook = await readFile(new URL('../hooks/useAutoHideScrollbar.ts', import.meta.url), 'utf8');
const flipHook = await readFile(new URL('../hooks/useFlipList.ts', import.meta.url), 'utf8');
const sessionsCore = await readFile(new URL('../core/sessions.ts', import.meta.url), 'utf8');
const styles = await readFile(new URL('../index.css', import.meta.url), 'utf8');

test('sidebar scrollbar is hidden by default and revealed only during interaction', () => {
  assert.match(styles, /\.autohide-scrollbar \{[\s\S]*?scrollbar-gutter: stable/, 'scrollbar gutter must be reserved so toggling visibility never relayouts the list');
  assert.match(styles, /\.autohide-scrollbar::-webkit-scrollbar \{[\s\S]*?width: 8px/, 'WebKit scrollbar width must stay constant');
  assert.match(styles, /\.autohide-scrollbar::-webkit-scrollbar-thumb \{[\s\S]*?background: transparent/, 'WebKit default thumb must stay hidden');
  assert.match(styles, /\.autohide-scrollbar\.scrolling::-webkit-scrollbar-thumb \{[\s\S]*?background: var\(--fg-faint\)/, 'interaction class must reveal the thumb');
  for (const event of ['pointerenter', 'pointerleave', 'wheel', 'scroll']) {
    assert.match(scrollbarHook, new RegExp(`addEventListener\\('${event}'`), `${event} must control scrollbar visibility`);
  }
  assert.match(scrollbarHook, /AUTO_HIDE_DELAY_MS = 600/, 'scrollbar must hide quickly after the last interaction');
  assert.match(scrollbarHook, /LEAVE_HIDE_DELAY_MS = 200/, 'leaving the sidebar must clear the scrollbar almost immediately');
  assert.doesNotMatch(scrollbarHook, /2000/, 'the old 2s sticky timeout must not return');
  assert.match(scrollbarHook, /container\.classList\.remove\('scrolling'\)/, 'idle scrollbar must hide again');
  assert.match(scrollbarHook, /pointerdown/, 'dragging the scrollbar must keep it visible');
  assert.match(scrollbarHook, /return \(\) => \{[\s\S]*?removeEventListener/, 'binding must be cleaned up with the component lifecycle');
  assert.match(sidebar, /useAutoHideScrollbar\(scrollRef\)/, 'sidebar must attach the auto-hiding scrollbar behavior');
  assert.match(sidebar, /autohide-scrollbar/, 'sidebar scroll container must carry the scrollbar class');
  assert.doesNotMatch(sidebar, /animate-in[^"']*duration-\d+/, 'animate-in rows must not set transition-duration: every hover would then animate all properties');
});

test('session list refresh swaps pages atomically instead of clearing and refilling', () => {
  assert.doesNotMatch(sessionsCore, /state\.sessionPages = \{\};/, 'refresh must never drop existing pages mid-load');
  assert.doesNotMatch(sessionsCore, /state\.sessions = \[\];/, 'refresh must never drop the session cache mid-load');
  assert.match(sessionsCore, /sessions: base\?\.sessions \|\| \[\]/, 'loading pages must keep previous entries visible until the swap');
  assert.match(sessionsCore, /state\.sessionPages = \{ \.\.\.state\.sessionPages, \[key\]: page \};/, 'finished pages must swap in atomically');
});

test('session rows animate reorders and entrances smoothly', () => {
  assert.match(sidebar, /useFlipList\(flipSignature\)/, 'sidebar must drive reorder animation from the visible-list signature');
  assert.match(flipHook, /el\.animate\(/, 'moved rows must animate through WAAPI transforms');
  assert.match(flipHook, /if \(signature === previousSignature\.current\) return;/, 'flip must skip renders where the list did not change');
  assert.match(sidebar, /animate-in fade-in slide-in-from-bottom-1/, 'new rows must enter with a subtle animation');
  assert.match(sidebar, /ref=\{registerFlip\(session\.sessionId\)\}/, 'rows must register stable refs keyed by session id');
});

test('new task opens the native directory picker and binds the chosen cwd', () => {
  assert.match(sessionsCore, /export async function startNewTask\(pickDirectory = true\)/, 'new task must be an async flow with an opt-out for internal resets');
  assert.match(sessionsCore, /const picked = await chooseWorkingDirectory\(\);/, 'new task must await the native directory picker');
  assert.match(sessionsCore, /replaceDraftWorkspace\('selected', picked\)/, 'a picked directory must be explicitly bound to this new-task draft');
  assert.match(sidebar, /onClick=\{\(\) => void startNewTask\(\)\}/, 'the new-task buttons must run the picker flow');
  assert.match(sessionsCore, /void startNewTask\(false\)/, 'session delete must not pop the picker again');
});

test('sidebar keeps the canonical navigation order and feature gates', () => {
  const home = sidebar.indexOf("view: 'home'");
  const skills = sidebar.indexOf("view: 'skills'");
  const history = sidebar.indexOf("view: 'history'");
  const automation = sidebar.indexOf("view: 'automation'");
  const experts = sidebar.indexOf("view: 'experts'");
  const library = sidebar.indexOf("view: 'library'");
  assert.ok(home >= 0 && home < skills && skills < history && history < automation && automation < experts && experts < library, 'nav order must stay home → skills → history → automation → experts → library');
  assert.match(sidebar, /feature: 'manageExperts'/, 'experts entry must be gated on the ACP feature key');
  assert.match(sidebar, /hasFeature\(entry\.feature\)/, 'gated entries must hide when the runtime lacks the capability');
});

test('sidebar task tree projects ACP session pages without local persistence', () => {
  assert.match(sidebar, /taskProjectPageKey\(project\.id\)/, 'project branches must read their own session page');
  assert.match(sidebar, /taskUngroupedPageKey\(\)/, 'ungrouped list must read the canonical page key');
  assert.match(sidebar, /recentSessions\(\)/, 'recent list must use the shared projection');
  assert.match(sidebar, /loadMoreTaskSessions/, 'branches must paginate through the shared loader');
  assert.match(sidebar, /matchesTaskSearch/, 'search must reuse the shared pure matcher');
  assert.doesNotMatch(sidebar, /desktop\.storeSet\([^)]*(session|project|task)/i, 'sidebar must not persist task grouping locally');
  assert.doesNotMatch(sidebar, /localStorage/i, 'sidebar must not use localStorage');
});

test('session status dots follow the canonical run projection', () => {
  assert.match(sidebar, /hasFeature\('runStatus'\) && lastRun/, 'status must prefer the ACP lastRun projection when available');
  assert.match(sidebar, /lastRun\.active \|\| lastRun\.status === 'running'/, 'active runs must map to working');
  for (const status of ['planning', 'working', 'pending', 'completed', 'failed', 'cancelled']) {
    assert.ok(sidebar.includes(`${status}:`), `${status} dot mapping must exist`);
  }
});

test('session context menu preserves pin/rename/project/workspace/fork/delete actions', () => {
  assert.match(sidebar, /togglePin\(session\.sessionId\)/);
  assert.match(sidebar, /renameSession\(session\.sessionId\)/);
  assert.match(sidebar, /setSessionProject\(session\.sessionId, project\.id\)/);
  assert.match(sidebar, /createProjectAndAssign\(session\.sessionId\)/);
  assert.match(sidebar, /setSessionProject\(session\.sessionId, null\)/);
  assert.match(sidebar, /changeSessionWorkingDirectory\(session\.sessionId\)/);
  assert.match(sidebar, /forkSession\(session\.sessionId\)/);
  assert.match(sidebar, /deleteSession\(session\.sessionId\)/);
  assert.match(sidebar, /variant="destructive"/, 'delete must render as a destructive action');
});
