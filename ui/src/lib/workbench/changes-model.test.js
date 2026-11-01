import test from 'node:test';
import assert from 'node:assert/strict';
import {
  changeCount,
  changeSections,
  classifyDiffLine,
  renderDiffLines,
  splitPath,
  statusName
} from './changes-model.js';

test('statusName maps porcelain letters to canonical names', () => {
  assert.equal(statusName('A'), 'added');
  assert.equal(statusName('M'), 'modified');
  assert.equal(statusName('D'), 'deleted');
  assert.equal(statusName('R'), 'renamed');
  assert.equal(statusName('?'), 'untracked');
  assert.equal(statusName('Z'), 'modified');
});

test('changeSections groups staged, unstaged, and untracked files', () => {
  const payload = {
    repo: true,
    files: [
      { path: 'a.go', status: 'M', staged: true },
      { path: 'a.go', status: 'M', staged: false },
      { path: 'new.txt', status: '?' },
      { path: 'gone.go', status: 'D', staged: true }
    ]
  };
  const sections = changeSections(payload);
  assert.deepEqual(sections.staged.files.map((f) => f.path), ['a.go', 'gone.go']);
  assert.deepEqual(sections.unstaged.files.map((f) => f.path), ['a.go']);
  assert.deepEqual(sections.untracked.files.map((f) => f.path), ['new.txt']);
});

test('changeCount is zero for non-repositories and missing payloads', () => {
  assert.equal(changeCount({ repo: false, files: [{ path: 'x' }] }), 0);
  assert.equal(changeCount({ repo: true, files: [{ path: 'x' }] }), 1);
  assert.equal(changeCount(undefined), 0);
});

test('splitPath keeps the directory separator on the dimmed part', () => {
  assert.deepEqual(splitPath('internal/serve/run.go'), { dir: 'internal/serve/', name: 'run.go' });
  assert.deepEqual(splitPath('README.md'), { dir: '', name: 'README.md' });
});

test('diff line classification separates headers, hunks, and +/- lines', () => {
  assert.equal(classifyDiffLine('diff --git a/x b/x'), 'meta');
  assert.equal(classifyDiffLine('--- a/x'), 'file');
  assert.equal(classifyDiffLine('+++ b/x'), 'file');
  assert.equal(classifyDiffLine('@@ -1,3 +1,4 @@'), 'hunk');
  assert.equal(classifyDiffLine('+added'), 'added');
  assert.equal(classifyDiffLine('-removed'), 'removed');
  assert.equal(classifyDiffLine(' context'), 'context');
  assert.equal(classifyDiffLine('Binary files a/x and b/x differ'), 'meta');
});

test('renderDiffLines drops only the trailing newline', () => {
  const lines = renderDiffLines('@@ h\n+one\n-two\n');
  assert.equal(lines.length, 3);
  assert.deepEqual(lines.map((entry) => entry.kind), ['hunk', 'added', 'removed']);
  assert.deepEqual(renderDiffLines(''), []);
});
