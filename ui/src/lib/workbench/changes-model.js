// Git Changes panel model, mirroring the shared logic of thinkrail's
// changesModel: porcelain letters -> canonical status names, file grouping
// into Staged / Unstaged / Untracked sections, and unified-diff line
// classification for the diff surface. Pure functions only so the panel stays
// a thin projection of the /api/git/* endpoints.

export const STATUS_NAMES = {
  A: 'added',
  M: 'modified',
  D: 'deleted',
  R: 'renamed',
  C: 'copied',
  U: 'updated',
  '?': 'untracked',
  '!': 'untracked'
};

export function statusName(letter) {
  return STATUS_NAMES[letter] || 'modified';
}

export function changeSections(payload) {
  const files = Array.isArray(payload?.files) ? payload.files : [];
  const sections = {
    staged: { label: 'staged', files: [] },
    unstaged: { label: 'unstaged', files: [] },
    untracked: { label: 'untracked', files: [] }
  };
  for (const file of files) {
    if (file.status === '?') {
      sections.untracked.files.push(file);
    } else if (file.staged) {
      sections.staged.files.push(file);
    } else {
      sections.unstaged.files.push(file);
    }
  }
  return sections;
}

export function changeCount(payload) {
  if (!payload?.repo) return 0;
  return Array.isArray(payload.files) ? payload.files.length : 0;
}

// splitPath mirrors thinkrail: render the directory dimmed before the file.
export function splitPath(path) {
  const idx = path.lastIndexOf('/');
  if (idx < 0) return { dir: '', name: path };
  return { dir: path.slice(0, idx + 1), name: path.slice(idx + 1) };
}

const DIFF_KINDS = [
  ['hunk', (line) => line.startsWith('@@')],
  ['file', (line) => line.startsWith('--- ') || line.startsWith('+++ ')],
  ['meta', (line) => line.startsWith('diff ') || line.startsWith('index ') || line.startsWith('new file') || line.startsWith('deleted file') || line.startsWith('similarity ') || line.startsWith('rename ') || line.startsWith('Binary files ') || line.startsWith('GIT ')],
  ['added', (line) => line.startsWith('+')],
  ['removed', (line) => line.startsWith('-')],
  ['context', () => true]
];

export function classifyDiffLine(line) {
  for (const [kind, test] of DIFF_KINDS) {
    if (test(line)) return kind;
  }
  return 'context';
}

export function renderDiffLines(text) {
  if (!text) return [];
  const source = text.endsWith('\n') ? text.slice(0, -1) : text;
  return source.split('\n').map((line) => ({ line, kind: classifyDiffLine(line) }));
}
