import assert from 'node:assert/strict';
import { existsSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, join, win32 } from 'node:path';
import test from 'node:test';

import { defaultNewSessionDirectory, newSessionDirectoryPath, newSessionDirectoryTimestamp } from './default-new-session-directory.ts';

test('new-session directories use a cross-platform safe timestamp under MothX', () => {
  const now = new Date(2026, 8, 8, 7, 6, 5);
  assert.equal(newSessionDirectoryTimestamp(now), '20260908_070605');

  const home = mkdtempSync(join(tmpdir(), 'mothx-default-workdir-'));
  try {
    const directory = defaultNewSessionDirectory(home, now);
    assert.equal(directory, join(home, 'MothX', 'mothx_projects_20260908_070605'));
    assert.equal(basename(directory), 'mothx_projects_20260908_070605');
    assert.equal(existsSync(directory), true);
  } finally {
    rmSync(home, { recursive: true, force: true });
  }
});

test('new-session path uses the platform path joiner and a Windows-safe leaf name', () => {
  const now = new Date(2026, 8, 8, 7, 6, 5);
  const directory = newSessionDirectoryPath('C:\\Users\\Ada', now, win32.join);
  assert.equal(directory, 'C:\\Users\\Ada\\MothX\\mothx_projects_20260908_070605');
  assert.doesNotMatch(win32.basename(directory), /[:<>"/\\|?*]/, 'directory leaf must be usable on Windows');
});
