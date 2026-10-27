import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';

const historyView = await readFile(new URL('./HistoryView.tsx', import.meta.url), 'utf8');

test('history rows show a live ACP run status badge with durable lastRun fallback', () => {
  assert.match(
    historyView,
    /historyStatus\(session, viewState\.sessionStatus\[session\.sessionId\]\)/,
    'the realtime ACP status projection must take precedence while history reloads catch up',
  );
  assert.match(
    historyView,
    /session\._meta\?\.lastRun\?\.active \? 'running' : session\._meta\?\.lastRun\?\.status/,
    'the durable listAll lastRun projection must remain the fallback',
  );
  assert.match(
    historyView,
    /<Badge variant=\{historyStatusVariant\(status\)\}>\{sessionStatusLabel\(status\)\}<\/Badge>/,
    'the status must be visibly rendered as a badge beside the session title',
  );
});

test('history badge variants distinguish active, successful, and unsuccessful runs', () => {
  assert.match(historyView, /case 'working':[\s\S]*?return 'info'/, 'running work must use the info badge');
  assert.match(historyView, /case 'completed':[\s\S]*?return 'success'/, 'completed runs must use the success badge');
  assert.match(historyView, /case 'incomplete':[\s\S]*?return 'danger'/, 'incomplete runs must use the danger badge');
  assert.match(historyView, /case 'cancelled':[\s\S]*?return 'cancelled'/, 'cancelled runs must retain their terminal status');
});
