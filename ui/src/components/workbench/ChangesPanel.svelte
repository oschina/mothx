<script>
  // Workbench Git Changes panel. Same rail mode as the plan/files panels:
  // chrome comes from the shared RailPanel; this component owns only the
  // /api/git/* projection state. Read-only: it never stages, commits, or
  // mutates the repository, and re-reads when a run reaches its terminal state.
  import { request } from '../../lib/api.js';
  import { isSessionRuntimeBusy, sessionRunStates } from '../../lib/session-runs.js';
  import { t } from '../../lib/preferences.js';
  import RailPanel from './RailPanel.svelte';
  import { changeCount, changeSections, renderDiffLines, splitPath, statusName } from '../../lib/workbench/changes-model.js';

  export let sessionId = '';
  export let workDir = '';
  export let mobile = false;
  export let width = 320;
  export let onClose = () => {};

  let payload = null;
  let loading = false;
  let error = '';
  let scope = 'all';
  let selected = null;
  let diff = null;
  let diffLoading = false;
  let diffError = '';

  $: state = sessionId ? $sessionRunStates?.[sessionId] : null;
  $: running = isSessionRuntimeBusy(state?.runtime);
  $: queryKey = sessionId
    ? `sessionId=${encodeURIComponent(sessionId)}`
    : `path=${encodeURIComponent(workDir || '')}`;
  $: sections = changeSections(payload);
  $: visibleSections = computeSections(sections, scope);
  $: totalChanges = changeCount(payload);

  function computeSections(sectionsValue, scopeValue) {
    const order = scopeValue === 'staged'
      ? [sectionsValue.staged]
      : scopeValue === 'unstaged'
        ? [sectionsValue.unstaged, sectionsValue.untracked]
        : [sectionsValue.staged, sectionsValue.unstaged, sectionsValue.untracked];
    return order.filter((section) => section.files.length > 0);
  }

  async function refresh() {
    if (!sessionId && !workDir) {
      payload = null;
      error = '';
      return;
    }
    loading = true;
    error = '';
    try {
      payload = await request(`/api/git/changes?${queryKey}`);
      if (selected && payload?.repo) {
        const stillThere = (payload.files || []).some(
          (file) => file.path === selected.path && file.staged === selected.staged
        );
        if (!stillThere) closeDiff();
      }
    } catch (err) {
      payload = null;
      error = err?.message || String(err);
    } finally {
      loading = false;
    }
  }

  async function openDiff(file) {
    selected = file;
    diff = null;
    diffError = '';
    diffLoading = true;
    try {
      const params = new URLSearchParams(sessionId ? [['sessionId', sessionId]] : [['path', workDir]]);
      params.set('file', file.path);
      if (file.status === '?') params.set('untracked', '1');
      else if (file.staged) params.set('staged', '1');
      diff = await request(`/api/git/diff?${params.toString()}`);
    } catch (err) {
      diff = null;
      diffError = err?.message || String(err);
    } finally {
      diffLoading = false;
    }
  }

  function closeDiff() {
    selected = null;
    diff = null;
    diffError = '';
  }

  function scopeCount(scopeValue) {
    if (scopeValue === 'all') return totalChanges;
    if (scopeValue === 'staged') return sections.staged.files.length;
    return sections.unstaged.files.length + sections.untracked.files.length;
  }

  // Re-read when the resolved target changes and when a run reaches its
  // terminal state, mirroring how the plan panel follows the session runtime.
  let lastRefreshKey = '';
  let wasRunning = false;
  $: if (lastRefreshKey !== queryKey || (wasRunning && !running)) {
    lastRefreshKey = queryKey;
    wasRunning = running;
    refresh();
  }
  $: wasRunning = running;
</script>

<RailPanel titleKey="workbench.changes" ariaLabelKey="workbench.changes" {width} {mobile} onClose={onClose}>
  <svelte:fragment slot="head">
    {#if payload?.branch}
      <span class="wb-changes-branch" title={payload.root || ''}>
        <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="6" cy="6" r="2.5"/><circle cx="6" cy="18" r="2.5"/><circle cx="18" cy="6" r="2.5"/><path d="M6 8.5v7"/><path d="M15.5 6H9a3 3 0 0 0-3 3v0"/></svg>
        <span>{payload.branch}</span>
      </span>
    {/if}
    <span class="wb-changes-count" title={$t('workbench.changes.total', { n: totalChanges })}>{totalChanges}</span>
    <button type="button" class="wb-rail-icon" onclick={refresh} disabled={loading} title={$t('workbench.changes.refresh')} aria-label={$t('workbench.changes.refresh')}>
      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" class={loading ? 'spinning' : ''} aria-hidden="true"><path d="M4 4v5h5"/><path d="M5.3 9a8 8 0 1 1 .7 5"/></svg>
    </button>
  </svelte:fragment>

  {#if error}
    <p class="wb-changes-note wb-changes-error" role="alert">{error}</p>
  {:else if payload && !payload.repo}
    <p class="wb-changes-note">{$t('workbench.changes.notRepo')}</p>
  {:else if selected}
    <div class="wb-changes-diff-head">
      <button type="button" class="wb-changes-back" onclick={closeDiff} title={$t('workbench.changes.back')} aria-label={$t('workbench.changes.back')}>
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M19 12H5"/><path d="m11 6-6 6 6 6"/></svg>
      </button>
      <code class="wb-changes-diff-path" title={selected.path}>{selected.path}</code>
      <span class={`wb-changes-status status-${statusName(selected.status)}`}>{selected.status}</span>
    </div>
    {#if diffLoading}
        <p class="wb-changes-note">{$t('common.loading')}</p>
      {:else if diffError}
        <p class="wb-changes-note wb-changes-error" role="alert">{diffError}</p>
      {:else if diff?.binary}
        <p class="wb-changes-note">{$t('workbench.changes.binary')}</p>
      {:else if diff}
        <pre class="wb-changes-diff">{#each renderDiffLines(diff.diff) as entry, index (index)}<span class={`diff-line diff-${entry.kind}`}>{entry.line || ' '}</span>{/each}</pre>
        {#if diff.truncated}<p class="wb-changes-note">{$t('workbench.changes.truncated')}</p>{/if}
      {:else}
        <p class="wb-changes-note">{$t('workbench.changes.noDiff')}</p>
      {/if}
  {:else}
    <div class="wb-changes-scope" role="group" aria-label={$t('workbench.changes.scope')}>
      {#each ['all', 'staged', 'working'] as mode (mode)}
        <button type="button" class:active={scope === mode} aria-pressed={scope === mode} onclick={() => (scope = mode)}>
          {mode === 'all' ? $t('workbench.changes.scopeAll') : mode === 'staged' ? $t('workbench.changes.staged') : $t('workbench.changes.scopeWorking')}
          <em>{scopeCount(mode)}</em>
        </button>
      {/each}
    </div>
    <div class="wb-changes-list">
      {#if loading && !payload}
        <p class="wb-changes-note">{$t('common.loading')}</p>
      {:else if visibleSections.length === 0}
        <p class="wb-changes-note">{$t('workbench.changes.clean')}</p>
      {:else}
        {#each visibleSections as section (section.label)}
          <details class="wb-changes-section" open>
            <summary>
              <span>{$t(`workbench.changes.${section.label === 'unstaged' ? 'unstaged' : section.label}`)}</span>
              <span class="wb-changes-section-count">{section.files.length}</span>
            </summary>
            <ul class="wb-changes-files">
              {#each section.files as file (`${section.label}:${file.path}:${file.staged}`)}
                <li>
                  <button type="button" class="wb-changes-file" onclick={() => openDiff(file)}>
                    <span class={`wb-changes-status status-${statusName(file.status)}`}>{file.status}</span>
                    <span class="wb-changes-file-path">
                      {#if splitPath(file.path).dir}<em>{splitPath(file.path).dir}</em>{/if}
                      <strong>{splitPath(file.path).name}</strong>
                      {#if file.oldPath}<em> ← {file.oldPath}</em>{/if}
                    </span>
                    {#if file.added || file.removed}
                      <span class="wb-changes-file-stats">
                        {#if file.added}<i class="plus">+{file.added}</i>{/if}
                        {#if file.removed}<i class="minus">-{file.removed}</i>{/if}
                      </span>
                    {/if}
                  </button>
                </li>
              {/each}
            </ul>
          </details>
        {/each}
      {/if}
    </div>
  {/if}
</RailPanel>

<style>
  .wb-changes-branch {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    min-width: 0;
    margin-left: 6px;
    color: var(--moth-text-secondary);
    font-size: 11px;
  }
  .wb-changes-branch span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .wb-changes-count {
    flex: 0 0 auto;
    margin-left: auto;
    padding: 1px 6px;
    border-radius: 999px;
    background: var(--moth-surface-hover);
    color: var(--moth-text-muted);
    font-size: 10.5px;
    font-variant-numeric: tabular-nums;
    line-height: 16px;
  }
  .wb-changes-scope {
    display: flex;
    gap: 3px;
    padding: 8px 10px 2px;
  }
  .wb-changes-scope button {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    min-height: 26px;
    padding: 0 9px;
    border: 1px solid transparent;
    border-radius: 6px;
    background: transparent;
    color: var(--moth-text-secondary);
    font-size: 11.5px;
    font-weight: 600;
    cursor: pointer;
  }
  .wb-changes-scope button:hover { background: var(--moth-surface-hover); color: var(--moth-text-primary); }
  .wb-changes-scope button.active {
    border-color: var(--moth-border-subtle);
    background: var(--moth-surface-secondary);
    color: var(--moth-text-primary);
    box-shadow: var(--shadow-sm);
  }
  .wb-changes-scope em {
    padding: 0 5px;
    border-radius: 999px;
    background: var(--moth-surface-hover);
    color: var(--moth-text-muted);
    font-size: 10px;
    font-style: normal;
    line-height: 14px;
  }
  .wb-changes-note {
    margin: 10px 6px;
    color: var(--moth-text-muted);
    font-size: 11.5px;
    line-height: 1.5;
  }
  .wb-changes-error { color: var(--moth-state-danger); }
  .wb-changes-section { border-bottom: 1px solid var(--moth-border-subtle); }
  .wb-changes-section:last-child { border-bottom: none; }
  .wb-changes-section summary {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 7px 8px;
    color: var(--moth-text-secondary);
    font-size: 10.5px;
    font-weight: 700;
    letter-spacing: .4px;
    text-transform: uppercase;
    cursor: pointer;
    list-style: none;
  }
  .wb-changes-section summary::-webkit-details-marker { display: none; }
  .wb-changes-section summary::before { content: '⌄'; color: var(--moth-text-muted); font-weight: 400; }
  .wb-changes-section:not([open]) summary::before { content: '›'; }
  .wb-changes-section-count { margin-left: auto; color: var(--moth-text-muted); font-weight: 600; font-variant-numeric: tabular-nums; }
  .wb-changes-files { margin: 0; padding: 0 2px 6px; list-style: none; }
  .wb-changes-file {
    display: flex;
    align-items: center;
    gap: 7px;
    width: 100%;
    min-height: 28px;
    padding: 2px 6px;
    border: none;
    border-radius: 6px;
    background: transparent;
    color: var(--moth-text-primary);
    font-size: 11.5px;
    text-align: left;
    cursor: pointer;
  }
  .wb-changes-file:hover { background: var(--moth-surface-hover); }
  .wb-changes-file:focus-visible { outline: 2px solid var(--moth-selection-indicator); outline-offset: -2px; }
  .wb-changes-status {
    flex: 0 0 auto;
    width: 17px;
    height: 17px;
    border-radius: 4px;
    background: var(--moth-surface-hover);
    color: var(--moth-text-secondary);
    font-family: var(--font-mono);
    font-size: 10.5px;
    font-weight: 700;
    line-height: 17px;
    text-align: center;
  }
  .status-added, .status-untracked { background: color-mix(in srgb, var(--moth-state-success) 14%, transparent); color: var(--moth-state-success); }
  .status-deleted { background: color-mix(in srgb, var(--moth-state-danger) 12%, transparent); color: var(--moth-state-danger); }
  .status-renamed, .status-copied { background: color-mix(in srgb, var(--moth-state-running) 12%, transparent); color: var(--moth-state-running); }
  .status-modified, .status-updated { background: color-mix(in srgb, #d97706 14%, transparent); color: #b45309; }
  .wb-changes-file-path { display: inline-flex; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .wb-changes-file-path em { color: var(--moth-text-muted); font-style: normal; }
  .wb-changes-file-path strong { color: var(--moth-text-primary); font-weight: 500; }
  .wb-changes-file-stats { display: inline-flex; gap: 5px; margin-left: auto; flex: 0 0 auto; font-family: var(--font-mono); font-size: 10.5px; font-style: normal; }
  .wb-changes-file-stats .plus { color: var(--moth-state-success); }
  .wb-changes-file-stats .minus { color: var(--moth-state-danger); }
  .wb-changes-diff-head {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 7px 8px;
    border-bottom: 1px solid var(--moth-border-subtle);
  }
  .wb-changes-back {
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
  .wb-changes-back:hover { background: var(--moth-surface-hover); color: var(--moth-text-primary); }
  .wb-changes-diff-path {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--moth-text-secondary);
    font-family: var(--font-mono);
    font-size: 11px;
  }
  .wb-changes-diff-head .wb-changes-status { margin-left: auto; }
  .wb-changes-list { padding: 4px 6px 10px; }
  .wb-changes-diff {
    margin: 0;
    padding: 6px 0 10px;
    font-family: var(--font-mono);
    font-size: 11px;
    line-height: 1.5;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  .wb-changes-diff :global(.diff-line) { display: block; padding: 0 10px; color: var(--moth-text-secondary); }
  .wb-changes-diff :global(.diff-added) { background: color-mix(in srgb, var(--moth-state-success) 9%, transparent); color: var(--moth-text-primary); }
  .wb-changes-diff :global(.diff-removed) { background: color-mix(in srgb, var(--moth-state-danger) 8%, transparent); color: var(--moth-text-primary); }
  .wb-changes-diff :global(.diff-hunk) { background: color-mix(in srgb, var(--moth-state-running) 8%, transparent); color: var(--moth-state-running); font-weight: 600; }
  .wb-changes-diff :global(.diff-file) { color: var(--moth-text-primary); font-weight: 600; }
  .wb-changes-diff :global(.diff-meta) { color: var(--moth-text-muted); }
</style>
