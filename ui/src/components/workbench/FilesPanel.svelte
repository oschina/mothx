<script>
  // Workbench Files panel: a lazy directory tree of the focused tab's working
  // directory with an in-panel text preview (same rail mode as the plan and
  // changes panels) and per-row "open externally" actions. Tree listing and
  // content are read-only /api/files projections; the open actions only accept
  // allowlisted targets inside the same root. Git badges reuse /api/git/changes.
  import { onDestroy } from 'svelte';
  import { request, postJSON } from '../../lib/api.js';
  import RailPanel from './RailPanel.svelte';
  import { isSessionRuntimeBusy, sessionRunStates } from '../../lib/session-runs.js';
  import { t } from '../../lib/preferences.js';
  import { buildChangeMarks } from '../../lib/workbench/changes-model.js';
  import { statusName } from '../../lib/workbench/changes-model.js';
  import {
    baseName,
    flattenRows,
    formatSize,
    parentRel,
    readExpanded,
    togglePath,
    writeExpanded
  } from '../../lib/workbench/files-tree.js';

  export let sessionId = '';
  export let workDir = '';
  export let mobile = false;
  export let width = 320;
  export let onClose = () => {};
  export let terminalAvailable = false;
  export let onOpenWebTerminal = null;

  let nodes = {};
  let expanded = [];
  let rootName = '';
  let treeError = '';
  let loadingRoot = false;
  let marks = { byFile: new Map(), dirs: new Set() };
  let openers = { reveal: false, systemTerminal: false, editorCandidates: [] };
  // /api/files/open* share the terminal's transport gate. When the gate is
  // closed (non-loopback without auth, or --unsafe), the menu must not offer
  // local launches; only the web-terminal action stays available because it
  // reuses the already-gated dock.
  let openersGated = false;
  let menu = null;
  let preview = null;
  let refreshTimer = 0;

  $: state = sessionId ? $sessionRunStates?.[sessionId] : null;
  $: running = isSessionRuntimeBusy(state?.runtime);
  $: queryKey = sessionId
    ? `sessionId=${encodeURIComponent(sessionId)}`
    : `path=${encodeURIComponent(workDir || '')}`;
  $: targetRoot = sessionId || workDir;
  $: rows = flattenRows(nodes, expanded);

  function storage() {
    try {
      return globalThis.localStorage;
    } catch {
      return null;
    }
  }

  async function ensureLoaded(dirRel) {
    if (nodes[dirRel]?.loading || nodes[dirRel]?.entries) return nodes[dirRel];
    nodes = { ...nodes, [dirRel]: { ...(nodes[dirRel] || {}), loading: true, error: '' } };
    try {
      const params = dirRel ? `${queryKey}&dir=${encodeURIComponent(dirRel)}` : queryKey;
      const data = await request(`/api/files/list?${params}`);
      nodes = {
        ...nodes,
        [dirRel]: { loading: false, entries: data?.entries || [], error: '', truncated: Boolean(data?.truncated) }
      };
      if (dirRel === '') rootName = baseName((data?.root || '').replace(/\/+$/, '')) || data?.root || '';
    } catch (err) {
      nodes = { ...nodes, [dirRel]: { loading: false, entries: [], error: err?.message || String(err) } };
    }
    return nodes[dirRel];
  }

  async function refresh() {
    if (!sessionId && !workDir) {
      nodes = {};
      marks = { byFile: new Map(), dirs: new Set() };
      preview = null;
      return;
    }
    loadingRoot = true;
    treeError = '';
    try {
      const reload = new Set(['', ...(expanded || [])]);
      const next = {};
      for (const key of Object.keys(nodes)) {
        if (!reload.has(key)) next[key] = nodes[key];
      }
      nodes = next;
      await ensureLoaded('');
      if (nodes['']?.error) treeError = nodes[''].error;
      const pending = [...reload].filter((key) => key !== '' && !(nodes[key]?.entries?.length));
      for (const key of pending) {
        if (expanded.includes(key)) await ensureLoaded(key);
      }
      const changes = await request(`/api/git/changes?${queryKey}`).catch(() => null);
      marks = buildChangeMarks(changes?.files);
    } finally {
      loadingRoot = false;
    }
  }

  function toggleDir(rel) {
    const willExpand = !expanded.includes(rel);
    expanded = togglePath(expanded, rel);
    writeExpanded(storage(), targetRoot, expanded);
    if (willExpand) ensureLoaded(rel);
  }

  async function openPreview(file) {
    preview = { rel: file.path, loading: true };
    try {
      const params = `${queryKey}&file=${encodeURIComponent(file.path)}`;
      const data = await request(`/api/files/content?${params}`);
      preview = { rel: file.path, loading: false, ...data };
    } catch (err) {
      preview = { rel: file.path, loading: false, error: err?.message || String(err) };
    }
  }

  function closePreview() {
    preview = null;
  }

  async function loadOpeners() {
    try {
      const data = await request('/api/files/openers');
      if (data) openers = data;
      openersGated = false;
    } catch (err) {
      openers = { reveal: false, systemTerminal: false, editorCandidates: [] };
      // A gated host reports the same codes as the terminal dock; remember it
      // so the menu explains why local launches are missing.
      openersGated = ['terminal_unsafe', 'terminal_disabled', 'terminal_unsupported'].includes(String(err?.code || ''));
    }
  }

  function menuItems(target) {
    const items = [];
    if (!openersGated && openers.reveal) items.push({ action: 'reveal', label: $t('workbench.files.open.reveal') });
    if (!openersGated && openers.systemTerminal) items.push({ action: 'terminal', label: $t('workbench.files.open.systemTerminal') });
    if (!openersGated) {
      for (const editor of openers.editorCandidates || []) {
        items.push({ action: 'editor', editor, label: $t(`workbench.files.open.editor.${editor}`) });
      }
    }
    if (terminalAvailable && sessionId && onOpenWebTerminal) {
      items.push({ action: 'webterminal', label: $t('workbench.files.open.webTerminal') });
    }
    return items.filter((item) => item.label);
  }

  function openMenu(event, target, isDir) {
    event.preventDefault();
    event.stopPropagation();
    menu = { rel: target, dir: Boolean(isDir), x: Math.min(event.clientX, (globalThis.innerWidth || 800) - 220), y: Math.min(event.clientY, (globalThis.innerHeight || 600) - 200) };
  }

  async function runOpen(item) {
    const target = menu?.rel || '';
    const isDir = Boolean(menu?.dir);
    menu = null;
    if (item.action === 'webterminal') {
      // The dock shell starts in a directory; a file row targets its parent.
      onOpenWebTerminal?.(isDir ? target : parentRel(target));
      return;
    }
    try {
      const body = { target: target || '', action: item.action };
      if (sessionId) body.sessionId = sessionId;
      else body.path = workDir;
      if (item.editor) body.editor = item.editor;
      await postJSON('/api/files/open', body);
    } catch (err) {
      treeError = err?.message || String(err);
    }
  }

  function handleWindowClick() {
    if (menu) menu = null;
  }
  function handleWindowKeydown(event) {
    if (event.key === 'Escape' && menu) menu = null;
  }
  if (typeof window !== 'undefined') {
    window.addEventListener('click', handleWindowClick);
    window.addEventListener('keydown', handleWindowKeydown);
  }
  onDestroy(() => {
    if (typeof window !== 'undefined') {
      window.removeEventListener('click', handleWindowClick);
      window.removeEventListener('keydown', handleWindowKeydown);
    }
    if (refreshTimer) clearTimeout(refreshTimer);
  });

  // Re-read on target changes and when a run reaches its terminal state,
  // debounced so a burst of run events costs one listing.
  let lastRefreshKey = '';
  let wasRunning = false;
  $: if (lastRefreshKey !== `${queryKey}` || (wasRunning && !running)) {
    const targetChanged = lastRefreshKey !== `${queryKey}`;
    lastRefreshKey = `${queryKey}`;
    if (targetChanged) {
      nodes = {};
      preview = null;
      treeError = '';
      expanded = readExpanded(storage(), targetRoot);
    }
    if (refreshTimer) clearTimeout(refreshTimer);
    refreshTimer = setTimeout(() => {
      refreshTimer = 0;
      refresh();
    }, 150);
  }
  $: wasRunning = running;

  loadOpeners();
  $: previewLines = preview?.content ? preview.content.split('\n') : [];
</script>

<RailPanel titleKey="workbench.files" ariaLabelKey="workbench.files" {width} {mobile} onClose={onClose}>
  <svelte:fragment slot="head">
    {#if rootName}
      <span class="wb-files-root" title={targetRoot}>{rootName}</span>
    {/if}
    <button type="button" class="wb-rail-icon" onclick={refresh} disabled={loadingRoot} title={$t('workbench.files.refresh')} aria-label={$t('workbench.files.refresh')}>
      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" class={loadingRoot ? 'spinning' : ''} aria-hidden="true"><path d="M4 4v5h5"/><path d="M5.3 9a8 8 0 1 1 .7 5"/></svg>
    </button>
  </svelte:fragment>

  {#if preview}
    <div class="wb-files-preview-head">
      <button type="button" class="wb-files-back" onclick={closePreview} title={$t('workbench.files.back')} aria-label={$t('workbench.files.back')}>
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M19 12H5"/><path d="m11 6-6 6 6 6"/></svg>
      </button>
      <code class="wb-files-preview-path" title={preview.rel}>{preview.rel}</code>
      {#if marks.byFile.get(preview.rel)}
        <span class={`wb-files-status status-${statusName(marks.byFile.get(preview.rel))}`}>{marks.byFile.get(preview.rel)}</span>
      {/if}
      <button type="button" class="wb-files-icon wb-files-openin" onclick={(event) => openMenu(event, preview.rel, false)} title={$t('workbench.files.openIn')} aria-label={$t('workbench.files.openIn')}>
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/></svg>
      </button>
    </div>
    <div class="wb-files-body wb-files-preview-body">
      {#if preview.loading}
        <p class="wb-files-note">{$t('common.loading')}</p>
      {:else if preview.error}
        <p class="wb-files-note wb-files-error" role="alert">{preview.error}</p>
      {:else if preview.binary}
        <p class="wb-files-note">{$t('workbench.files.binary')}</p>
      {:else if preview.tooLarge}
        <p class="wb-files-note">{$t('workbench.files.tooLarge', { size: formatSize(preview.size) })}</p>
      {:else if previewLines.length && !(previewLines.length === 1 && previewLines[0] === '')}
        <ol class="wb-files-lines">
          {#each previewLines as line, index (index)}
            <li><span class="wb-files-ln">{index + 1}</span><span class="wb-files-line">{line || ' '}</span></li>
          {/each}
        </ol>
      {:else}
        <p class="wb-files-note">{$t('workbench.files.emptyFile')}</p>
      {/if}
    </div>
  {:else}
    <div class="wb-files-body" role="tree" aria-label={$t('workbench.files')}>
      {#if treeError}
        <p class="wb-files-note wb-files-error" role="alert">{treeError}</p>
      {:else if loadingRoot && !nodes['']}
        <p class="wb-files-note">{$t('common.loading')}</p>
      {:else if nodes[''] && rows.length === 0}
        <p class="wb-files-note">{$t('workbench.files.empty')}</p>
      {/if}
      {#each rows as row (row.key)}
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <div
          class="wb-files-row"
          class:wb-files-dir={row.dir}
          role="treeitem"
          aria-expanded={row.dir ? row.expanded : undefined}
          tabindex="0"
          style={`padding-left:${6 + row.depth * 13}px`}
          onclick={() => (row.dir ? toggleDir(row.rel) : openPreview({ path: row.rel }))}
          onkeydown={(event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); row.dir ? toggleDir(row.rel) : openPreview({ path: row.rel }); } }}
          oncontextmenu={(event) => openMenu(event, row.rel)}
        >
          {#if row.dir}
            <span class="wb-files-caret" class:open={row.expanded} aria-hidden="true">›</span>
          {:else}
            <span class="wb-files-caret placeholder" aria-hidden="true"></span>
          {/if}
          <span class="wb-files-name">{row.name}</span>
          {#if row.dir && row.loading}
            <span class="wb-files-meta loading">…</span>
          {:else if !row.dir}
            <span class="wb-files-meta">{formatSize(row.size)}</span>
          {/if}
          {#if row.dir && marks.dirs.has(row.rel)}
            <span class="wb-files-dir-mark" aria-hidden="true" title={$t('workbench.files.hasChanges')}></span>
          {:else if marks.byFile.get(row.rel)}
            <span class={`wb-files-status status-${statusName(marks.byFile.get(row.rel))}`}>{marks.byFile.get(row.rel)}</span>
          {/if}
          <button type="button" class="wb-files-openin" onclick={(event) => openMenu(event, row.rel, row.dir)} title={$t('workbench.files.openIn')} aria-label={$t('workbench.files.openIn')}>
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/></svg>
          </button>
        </div>
      {/each}
      {#if nodes['']?.truncated}
        <p class="wb-files-note">{$t('workbench.files.truncated')}</p>
      {/if}
    </div>
  {/if}
</RailPanel>

{#if menu}
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div
    class="wb-files-menu"
    role="menu"
    style={`left:${menu.x}px;top:${menu.y}px`}
    onclick={(event) => event.stopPropagation()}
  >
    {#each menuItems(menu.rel) as item (item.action + (item.editor || ''))}
      <button type="button" role="menuitem" onclick={() => runOpen(item)}>{item.label}</button>
    {:else}
      <p class="wb-files-note">{openersGated ? $t('workbench.files.openersGated') : $t('workbench.files.noOpeners')}</p>
    {/each}
  </div>
{/if}

<style>
  .wb-files-panel header strong { flex: 0 0 auto; font-size: 12px; font-weight: 600; }
  .wb-files-root {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    margin-left: 6px;
    color: var(--moth-text-secondary);
    font-family: var(--font-mono);
    font-size: 11px;
  }
  .wb-files-body { flex: 1; min-height: 0; overflow-y: auto; padding: 4px 4px 10px; }
  .wb-files-note {
    margin: 10px 6px;
    color: var(--moth-text-muted);
    font-size: 11.5px;
    line-height: 1.5;
  }
  .wb-files-error { color: var(--moth-state-danger); }
  .wb-files-row {
    display: flex;
    align-items: center;
    gap: 5px;
    min-height: 26px;
    padding-right: 4px;
    border-radius: 6px;
    color: var(--moth-text-primary);
    font-size: 11.5px;
    cursor: pointer;
    user-select: none;
  }
  .wb-files-row:hover { background: var(--moth-surface-hover); }
  .wb-files-row:focus-visible { outline: 2px solid var(--moth-selection-indicator); outline-offset: -2px; }
  .wb-files-caret {
    flex: 0 0 auto;
    width: 12px;
    color: var(--moth-text-muted);
    font-size: 12px;
    line-height: 1;
    text-align: center;
    transition: transform var(--moth-motion-fast) ease;
  }
  .wb-files-caret.open { transform: rotate(90deg); }
  .wb-files-name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .wb-files-dir .wb-files-name { font-weight: 500; }
  .wb-files-meta { margin-left: auto; flex: 0 0 auto; color: var(--moth-text-muted); font-size: 10px; font-variant-numeric: tabular-nums; }
  .wb-files-meta.loading { opacity: .6; }
  .wb-files-dir-mark {
    flex: 0 0 auto;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: color-mix(in srgb, #d97706 60%, transparent);
  }
  .wb-files-status {
    flex: 0 0 auto;
    width: 15px;
    height: 15px;
    border-radius: 4px;
    background: var(--moth-surface-hover);
    color: var(--moth-text-secondary);
    font-family: var(--font-mono);
    font-size: 9.5px;
    font-weight: 700;
    line-height: 15px;
    text-align: center;
  }
  .status-added, .status-untracked { background: color-mix(in srgb, var(--moth-state-success) 14%, transparent); color: var(--moth-state-success); }
  .status-deleted { background: color-mix(in srgb, var(--moth-state-danger) 12%, transparent); color: var(--moth-state-danger); }
  .status-renamed, .status-copied { background: color-mix(in srgb, var(--moth-state-running) 12%, transparent); color: var(--moth-state-running); }
  .status-modified, .status-updated { background: color-mix(in srgb, #d97706 14%, transparent); color: #b45309; }
  .wb-files-openin {
    display: none;
    flex: 0 0 auto;
    width: 20px;
    height: 20px;
    padding: 0;
    border: 0;
    border-radius: 5px;
    background: transparent;
    color: var(--moth-text-muted);
    cursor: pointer;
  }
  .wb-files-row:hover .wb-files-openin, .wb-files-preview-head .wb-files-openin { display: inline-flex; }
  .wb-files-openin:hover { background: var(--moth-surface-active); color: var(--moth-text-primary); }
  .wb-files-preview-head {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 7px 8px;
    border-bottom: 1px solid var(--moth-border-subtle);
  }
  .wb-files-back {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 24px;
    height: 24px;
    padding: 0;
    border: 0;
    border-radius: 6px;
    background: transparent;
    color: var(--moth-text-secondary);
    cursor: pointer;
  }
  .wb-files-back:hover { background: var(--moth-surface-hover); color: var(--moth-text-primary); }
  .wb-files-preview-path {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--moth-text-secondary);
    font-family: var(--font-mono);
    font-size: 11px;
  }
  .wb-files-preview-head .wb-files-status,
  .wb-files-preview-head .wb-files-openin { margin-left: auto; }
  .wb-files-preview-head .wb-files-status { margin-left: 0; }
  .wb-files-preview-body { padding: 0; }
  .wb-files-lines {
    margin: 0;
    padding: 6px 0 10px;
    list-style: none;
    font-family: var(--font-mono);
    font-size: 11px;
    line-height: 1.5;
  }
  .wb-files-lines li { display: flex; align-items: flex-start; }
  .wb-files-lines li:hover { background: var(--moth-surface-hover); }
  .wb-files-ln {
    flex: 0 0 40px;
    padding-right: 9px;
    color: var(--moth-text-muted);
    text-align: right;
    user-select: none;
  }
  .wb-files-line {
    min-width: 0;
    flex: 1;
    padding-right: 10px;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    color: var(--moth-text-primary);
  }
  .wb-files-menu {
    position: fixed;
    z-index: 60;
    min-width: 200px;
    padding: 4px;
    border: 1px solid var(--moth-border-strong);
    border-radius: 8px;
    background: var(--popover, var(--moth-surface-page));
    box-shadow: var(--shadow-md, 0 8px 24px rgba(0, 0, 0, 0.16));
  }
  .wb-files-menu button {
    display: block;
    width: 100%;
    padding: 6px 9px;
    border: none;
    border-radius: 6px;
    background: transparent;
    color: var(--moth-text-primary);
    font-size: 12px;
    text-align: left;
    cursor: pointer;
  }
  .wb-files-menu button:hover { background: var(--moth-surface-hover); }
</style>
