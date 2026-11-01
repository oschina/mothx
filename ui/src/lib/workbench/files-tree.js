// Pure helpers for the workbench Files panel tree: lazy per-directory node
// cache, expansion state, visible-row flattening, and persisted expansion
// (the lightweight analogue of thinkrail's filesTreeState: a per-root path
// list with a hard cap; scroll restoration is intentionally out of scope).

export const MAX_EXPANDED_PATHS = 2000;

export function childRel(parentRel, name) {
  return parentRel ? `${parentRel}/${name}` : name;
}

export function parentRel(rel) {
  const idx = rel.lastIndexOf('/');
  return idx < 0 ? '' : rel.slice(0, idx);
}

export function baseName(rel) {
  const idx = rel.lastIndexOf('/');
  return idx < 0 ? rel : rel.slice(idx + 1);
}

// rowsFor renders the flattened visible tree. `nodes` maps a directory rel
// path ('' is the root) to its loaded entries; only expanded directories
// contribute their children.
export function flattenRows(nodes, expanded) {
  const rows = [];
  const expandedSet = new Set(expanded || []);
  const walk = (dirRel, depth) => {
    const node = nodes[dirRel];
    if (!node) return;
    for (const entry of node.entries || []) {
      rows.push({
        key: entry.path,
        rel: entry.path,
        name: entry.name,
        dir: Boolean(entry.dir),
        size: entry.size || 0,
        depth,
        expanded: entry.dir && expandedSet.has(entry.path),
        loading: Boolean(entry.dir) && expandedSet.has(entry.path) && !nodes[entry.path]
      });
      if (entry.dir && expandedSet.has(entry.path)) {
        walk(entry.path, depth + 1);
      }
    }
  };
  walk('', 0);
  return rows;
}

export function togglePath(expanded, path) {
  const set = new Set(expanded || []);
  if (set.has(path)) set.delete(path);
  else set.add(path);
  return [...set].slice(-MAX_EXPANDED_PATHS);
}

const STORAGE_PREFIX = 'mothx.workbench.filesTree:';

export function readExpanded(storage, rootKey) {
  if (!storage || !rootKey) return [];
  try {
    const raw = storage.getItem(STORAGE_PREFIX + rootKey);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.filter((value) => typeof value === 'string').slice(0, MAX_EXPANDED_PATHS);
  } catch {
    return [];
  }
}

export function writeExpanded(storage, rootKey, paths) {
  if (!storage || !rootKey) return;
  try {
    storage.setItem(STORAGE_PREFIX + rootKey, JSON.stringify((paths || []).slice(0, MAX_EXPANDED_PATHS)));
  } catch {
    // Tree expansion is presentation state; a blocked storage must not fail
    // the interaction.
  }
}

// formatSize renders byte counts the way thinkrail's tree meta shows them.
export function formatSize(bytes) {
  const value = Number(bytes) || 0;
  if (value < 1024) return `${value} B`;
  const units = ['KB', 'MB', 'GB'];
  let size = value / 1024;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${size >= 10 ? Math.round(size) : size.toFixed(1)} ${units[unit]}`;
}
