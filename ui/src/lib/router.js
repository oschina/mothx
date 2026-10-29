// Minimal hash-based router. Views register once; the shell derives the
// active route from window.location.hash.

import { writable } from 'svelte/store';

const route = writable(parseHash());

if (typeof window !== 'undefined') {
  window.addEventListener('hashchange', () => route.set(parseHash()));
}

export { route };

export function navigate(path) {
  const target = normalise(path);
  if (window.location.hash === target) {
    route.set(parseHash());
    return;
  }
  window.location.hash = target;
}

// replaceNavigate updates the hash without growing browser history. Tab focus
// changes should not turn every pane click into a Back-button step.
export function replaceNavigate(path) {
  const target = normalise(path);
  if (window.location.hash === target) {
    route.set(parseHash());
    return;
  }
  if (typeof window === 'undefined') return;
  window.history.replaceState(null, '', target);
  route.set(parseHash());
}

function parseHash() {
  const raw = (typeof window === 'undefined' ? '' : window.location.hash) || '#/chat';
  const clean = raw.startsWith('#') ? raw.slice(1) : raw;
  const [pathRaw, queryRaw = ''] = clean.split('?');
  const path = pathRaw || '/chat';
  const segments = path.split('/').filter(Boolean);
  const query = new URLSearchParams(queryRaw);
  return {
    path,
    segments,
    section: segments[0] || 'chat',
    sub: segments[1] || '',
    query: Object.fromEntries(query.entries())
  };
}

function normalise(path) {
  if (!path) return '#/chat';
  if (path.startsWith('#')) return path;
  if (path.startsWith('/')) return `#${path}`;
  return `#/${path}`;
}
