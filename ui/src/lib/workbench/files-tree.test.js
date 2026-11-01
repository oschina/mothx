import test from 'node:test';
import assert from 'node:assert/strict';
import {
  baseName,
  childRel,
  flattenRows,
  formatSize,
  parentRel,
  readExpanded,
  togglePath,
  writeExpanded,
  MAX_EXPANDED_PATHS
} from './files-tree.js';

function fakeStorage() {
  const map = new Map();
  return {
    getItem: (key) => (map.has(key) ? map.get(key) : null),
    setItem: (key, value) => map.set(key, String(value)),
    removeItem: (key) => map.delete(key),
    dump: map
  };
}

test('rel helpers round-trip child and parent paths', () => {
  assert.equal(childRel('', 'a'), 'a');
  assert.equal(childRel('a/b', 'c.txt'), 'a/b/c.txt');
  assert.equal(parentRel('a/b/c.txt'), 'a/b');
  assert.equal(parentRel('a'), '');
  assert.equal(baseName('a/b/c.txt'), 'c.txt');
  assert.equal(baseName('c.txt'), 'c.txt');
});

test('flattenRows renders loaded directories with depth and expansion', () => {
  const nodes = {
    '': { entries: [
      { name: 'src', path: 'src', dir: true },
      { name: 'a.txt', path: 'a.txt', dir: false, size: 12 }
    ] },
    src: { entries: [{ name: 'b.go', path: 'src/b.go', dir: false, size: 1 }] }
  };
  const rows = flattenRows(nodes, ['src']);
  assert.deepEqual(rows.map((row) => [row.rel, row.depth, row.expanded]), [
    ['src', 0, true],
    ['src/b.go', 1, false],
    ['a.txt', 0, false]
  ]);
});

test('flattenRows marks expanded-but-unloaded directories as loading', () => {
  const nodes = { '': { entries: [{ name: 'x', path: 'x', dir: true }] } };
  const rows = flattenRows(nodes, ['x']);
  assert.equal(rows[0].loading, true);
});

test('togglePath adds removes and caps expansion persistence', () => {
  let expanded = togglePath([], 'a');
  assert.deepEqual(expanded, ['a']);
  expanded = togglePath(expanded, 'a');
  assert.deepEqual(expanded, []);
  const many = Array.from({ length: MAX_EXPANDED_PATHS + 10 }, (_, i) => `p${i}`);
  const capped = togglePath(many, 'extra');
  assert.equal(capped.length, MAX_EXPANDED_PATHS);
});

test('expanded persistence is scoped per root and crash-safe', () => {
  const storage = fakeStorage();
  writeExpanded(storage, '/repo/a', ['src', 'src/deep']);
  assert.deepEqual(readExpanded(storage, '/repo/a'), ['src', 'src/deep']);
  assert.deepEqual(readExpanded(storage, '/repo/b'), []);
  storage.dump.set('mothx.workbench.filesTree:/bad', 'not json');
  assert.deepEqual(readExpanded(storage, '/bad'), []);
  storage.dump.set('mothx.workbench.filesTree:/mixed', JSON.stringify(['ok', 42, null]));
  assert.deepEqual(readExpanded(storage, '/mixed'), ['ok']);
});

test('formatSize keeps single-digit decimals', () => {
  assert.equal(formatSize(0), '0 B');
  assert.equal(formatSize(512), '512 B');
  assert.equal(formatSize(1536), '1.5 KB');
  assert.equal(formatSize(20 * 1024), '20 KB');
  assert.equal(formatSize(3 * 1024 * 1024), '3.0 MB');
});
