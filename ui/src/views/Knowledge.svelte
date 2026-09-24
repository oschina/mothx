<script>
  import { onMount } from 'svelte';
  import {
    listKnowledgeBases,
    createKnowledgeBase,
    updateKnowledgeBase,
    deleteKnowledgeBase,
    scanKnowledgeBase,
    queryKnowledgeBase,
    listKnowledgeBaseRuns,
    listKnowledgeBaseSources,
    clearKnowledgeBase,
    defaultKnowledgeBase,
    knowledgeBasePayload,
    knowledgeBaseIndexing,
    knowledgeBaseIsIndexing,
    validateKnowledgeBase
  } from '../lib/knowledge-base.js';
  import { postJSON } from '../lib/api.js';
  import { setError, setNotice } from '../lib/stores.js';
  import { t } from '../lib/preferences.js';
  import DirBrowser from '../components/DirBrowser.svelte';
  import { Button } from '$lib/components/ui/button';
  import * as Card from '$lib/components/ui/card';

  let bases = [];
  let loading = true;
  let editing = null;
  let query = '';
  let queryResult = null;
  let busy = '';
  let dirBrowserOpen = false;
  let pollTimer = null;
  let panel = 'runs';
  let runs = [];
  let sources = [];
  let panelBusy = false;

  const PHASE_KEYS = {
    scanning: 'knowledge.phase.scanning',
    indexing: 'knowledge.phase.indexing',
    enriching: 'knowledge.phase.enriching',
    committing: 'knowledge.phase.committing'
  };

  function phaseLabel(phase) {
    const key = PHASE_KEYS[phase];
    return key ? $t(key) : phase || $t('knowledge.phase.indexing');
  }

  function status(view) {
    const indexing = knowledgeBaseIndexing(view);
    if (indexing) {
      const phase = phaseLabel(indexing.phase);
      return indexing.filesTotal > 0
        ? $t('knowledge.indexing', { phase, done: indexing.filesDone, total: indexing.filesTotal })
        : $t('knowledge.indexingIndeterminate', { phase });
    }
    if (view?.knowledgeBase?.enabled === false) return $t('knowledge.disabled');
    return view?.status === 'completed' || view?.snapshot?.status === 'completed'
      ? $t('knowledge.indexed')
      : $t('knowledge.notIndexed');
  }

  // Poll while any base is scanning so a running (or just-triggered) scan keeps
  // its progress visible across page reloads instead of being lost.
  $: if (typeof window !== 'undefined') {
    if (knowledgeBaseIsIndexing(bases)) startPolling();
    else stopPolling();
  }

  function startPolling() {
    if (pollTimer) return;
    pollTimer = setInterval(() => { load(true); }, 3000);
  }

  function stopPolling() {
    if (!pollTimer) return;
    clearInterval(pollTimer);
    pollTimer = null;
  }

  onMount(() => {
    load();
    return stopPolling;
  });

  function openEditor(view) {
    editing = view ? { ...view.knowledgeBase, ignoreGlobs: [...(view.knowledgeBase.ignoreGlobs || [])] } : defaultKnowledgeBase();
    query = '';
    queryResult = null;
    runs = [];
    sources = [];
    panel = 'runs';
    if (editing.id) loadPanels(editing.id);
  }

  // loadPanels refreshes the run-history and source-provenance projections for
  // one knowledge base. Both are bounded server projections and never carry
  // file contents.
  async function loadPanels(id) {
    if (!id) return;
    panelBusy = true;
    try {
      const [nextRuns, nextSources] = await Promise.all([
        listKnowledgeBaseRuns(id, 20),
        listKnowledgeBaseSources(id)
      ]);
      if (editing?.id !== id) return;
      runs = nextRuns.filter(Boolean);
      sources = nextSources.filter(Boolean);
    } catch (err) {
      setError(err);
    } finally {
      panelBusy = false;
    }
  }

  function formatDate(value) {
    if (!value) return '';
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? String(value) : date.toLocaleString();
  }

  function formatSize(bytes) {
    const size = Number(bytes) || 0;
    if (size < 1024) return `${size} B`;
    if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
    return `${(size / (1024 * 1024)).toFixed(1)} MB`;
  }

  async function clearIndex() {
    if (!editing?.id || busy) return;
    if (!confirm($t('knowledge.clearConfirm', { name: editing.name }))) return;
    const id = editing.id;
    busy = `clear:${id}`;
    try {
      await clearKnowledgeBase(id);
      setNotice($t('knowledge.cleared', { name: editing.name }));
      await loadPanels(id);
      await load(true);
    } catch (err) {
      setError(err);
    } finally {
      busy = '';
    }
  }

  async function load(silent = false) {
    if (!silent) loading = true;
    try {
      bases = await listKnowledgeBases();
    } catch (err) {
      if (!silent) setError(err);
    } finally {
      loading = false;
    }
  }

  async function chooseRootDir() {
    if (!editing) return;
    const desktop = globalThis.__MOTHX_DESKTOP__;
    if (desktop?.chooseDirectory) {
      try {
        const selected = await desktop.chooseDirectory(editing.rootDir.trim());
        if (selected) editing.rootDir = selected;
      } catch (err) {
        setError(err);
      }
      return;
    }
    try {
      const selected = await postJSON('/api/select-directory', { defaultPath: editing.rootDir.trim() }, { timeoutMs: 0 });
      if (selected?.canceled === false && selected?.path) {
        editing.rootDir = selected.path;
        return;
      }
    } catch (err) {
      if (err?.status !== 500 && err?.status !== 501) {
        setError(err);
        return;
      }
    }
    dirBrowserOpen = true;
  }

  async function save() {
    if (!editing || busy) return;
    const payload = knowledgeBasePayload(editing);
    const errors = validateKnowledgeBase(payload);
    if (errors.length > 0) {
      setError(errors.join('; '));
      return;
    }
    busy = 'save';
    const existing = editing.id;
    try {
      if (existing) {
        await updateKnowledgeBase(existing, payload);
        setNotice($t('knowledge.updated', { name: payload.name }));
      } else {
        await createKnowledgeBase(payload);
        setNotice($t('knowledge.created', { name: payload.name }));
      }
      editing = null;
      await load();
    } catch (err) {
      setError(err);
    } finally {
      busy = '';
    }
  }

  async function scan(view) {
    if (!view) return;
    const id = view.knowledgeBase?.id;
    if (!id) return;
    busy = `scan:${id}`;
    try {
      await scanKnowledgeBase(id);
      setNotice($t('knowledge.scanStarted', { name: view.knowledgeBase.name }));
      await load(true);
    } catch (err) {
      setError(err);
    } finally {
      busy = '';
    }
  }

  async function remove(view) {
    if (!view) return;
    const base = view.knowledgeBase;
    if (!confirm($t('knowledge.deleteConfirm', { name: base.name }))) return;
    busy = `delete:${base.id}`;
    try {
      await deleteKnowledgeBase(base.id);
      if (editing?.id === base.id) editing = null;
      setNotice($t('knowledge.deleted', { name: base.name }));
      await load();
    } catch (err) {
      setError(err);
    } finally {
      busy = '';
    }
  }

  async function runQuery() {
    if (!editing?.id || !query.trim() || busy) return;
    busy = `query:${editing.id}`;
    try {
      queryResult = (await queryKnowledgeBase(editing.id, query.trim(), 8))?.query || null;
    } catch (err) {
      setError(err);
    } finally {
      busy = '';
    }
  }
</script>

<section class="page knowledge-page">
  <div class="page-toolbar">
    <div>
      <h1>{$t('knowledge.title')}</h1>
      <p class="muted">{$t('knowledge.subtitle')}</p>
    </div>
    <Button onclick={() => openEditor(null)}>{$t('knowledge.new')}</Button>
  </div>

  {#if loading}
    <p class="empty">{$t('common.loading')}</p>
  {:else if !editing && bases.length === 0}
    <div class="empty-state">
      <p class="empty">{$t('knowledge.empty')}</p>
      <Button onclick={() => openEditor(null)}>{$t('knowledge.new')}</Button>
    </div>
  {:else}
    <div class="knowledge-grid">
      <div class="knowledge-list" role="list">
        {#each bases as view (view.knowledgeBase.id)}
          <Card.Root class="knowledge-card">
            <Card.Content class="p-4">
              <div class="knowledge-card-content">
                <button
                  type="button"
                  class="knowledge-open"
                  aria-label={`${$t('knowledge.edit')}: ${view.knowledgeBase.name}`}
                  onclick={() => openEditor(view)}
                >
                  <strong>{view.knowledgeBase.name}</strong>
                  <span>{view.knowledgeBase.rootDir}</span>
                  <small>{status(view)}</small>
                </button>
                <div class="knowledge-actions">
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={busy === `scan:${view.knowledgeBase.id}` || view.knowledgeBase.enabled === false || knowledgeBaseIndexing(view) !== null}
                    onclick={() => scan(view)}
                  >
                    {knowledgeBaseIndexing(view) ? $t('knowledge.scanning') : $t('knowledge.scan')}
                  </Button>
                  <Button
                    size="sm"
                    variant="destructive"
                    disabled={busy === `delete:${view.knowledgeBase.id}`}
                    onclick={() => remove(view)}
                  >
                    {$t('common.delete')}
                  </Button>
                </div>
              </div>
            </Card.Content>
          </Card.Root>
        {/each}
      </div>

      {#if editing}
        <Card.Root class="knowledge-editor">
          <Card.Content class="p-4">
            <h2 class="editor-title">{editing.id ? $t('knowledge.edit') : $t('knowledge.new')}</h2>

            <div class="knowledge-fields">
              <label>
                <span>{$t('knowledge.name')}</span>
                <input bind:value={editing.name} placeholder={$t('knowledge.namePlaceholder')} />
              </label>
              <label>
                <span>{$t('knowledge.rootDir')}</span>
                <span class="directory-field">
                  <input bind:value={editing.rootDir} placeholder={$t('knowledge.rootDirHint')} />
                  <Button type="button" size="sm" variant="outline" onclick={chooseRootDir}>{$t('knowledge.chooseDirectory')}</Button>
                </span>
              </label>
              <label>
                <span>{$t('knowledge.preprocessProfile')}</span>
                <select bind:value={editing.preprocessProfile}>
                  {#each ['documents', 'code', 'notes', 'mixed'] as profile}
                    <option value={profile}>{profile}</option>
                  {/each}
                </select>
              </label>
              <label>
                <span>{$t('knowledge.provider')}</span>
                <input bind:value={editing.provider} placeholder={$t('knowledge.provider')} />
              </label>
              <label>
                <span>{$t('knowledge.model')}</span>
                <input bind:value={editing.model} placeholder={$t('knowledge.model')} />
              </label>
              <label>
                <span>{$t('knowledge.mode')}</span>
                <select bind:value={editing.mode}>
                  {#each ['yolo', 'agent', 'plan'] as mode}
                    <option value={mode}>{mode}</option>
                  {/each}
                </select>
              </label>
              <label>
                <span>{$t('knowledge.thinkingLevel')}</span>
                <input bind:value={editing.thinkingLevel} placeholder={$t('knowledge.thinkingLevel')} />
              </label>
              <label>
                <span>{$t('knowledge.schedule')}</span>
                <input bind:value={editing.schedule} placeholder={$t('knowledge.schedulePlaceholder')} />
              </label>
              <label class="wide">
                <span>{$t('knowledge.ignoreGlobs')}</span>
                <input
                  value={(editing.ignoreGlobs || []).join(', ')}
                  placeholder={$t('knowledge.ignoreGlobsHint')}
                  oninput={(event) => { editing.ignoreGlobs = event.currentTarget.value.split(',').map((glob) => glob.trim()).filter(Boolean); }}
                />
              </label>
              <label class="checkbox">
                <input type="checkbox" bind:checked={editing.enabled} />
                <span>{$t('knowledge.enabled')}</span>
              </label>
            </div>

            <div class="knowledge-actions">
              <Button disabled={busy === 'save'} onclick={save}>
                {$t('knowledge.save')}
              </Button>
              <Button variant="outline" onclick={() => (editing = null)}>
                {$t('knowledge.cancel')}
              </Button>
            </div>

            {#if editing.id}
              <div class="knowledge-panels">
                <div class="knowledge-tabs" role="tablist">
                  <button type="button" class:active={panel === 'runs'} onclick={() => (panel = 'runs')}>{$t('knowledge.panelRuns')}</button>
                  <button type="button" class:active={panel === 'sources'} onclick={() => (panel = 'sources')}>{$t('knowledge.panelSources')}</button>
                  <button type="button" class:active={panel === 'query'} onclick={() => (panel = 'query')}>{$t('knowledge.panelQuery')}</button>
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={panelBusy || busy === `clear:${editing.id}`}
                    onclick={clearIndex}
                  >
                    {$t('knowledge.clearIndex')}
                  </Button>
                </div>

                {#if panel === 'runs'}
                  <div class="knowledge-results">
                    {#if runs.length}
                      {#each runs as run (run.runId)}
                        <article>
                          <strong>
                            {run.status}
                            {#if run.active}<em class="badge">{$t('knowledge.runActive')}</em>{/if}
                          </strong>
                          <small>{formatDate(run.startedAt)}{run.finishedAt ? ` → ${formatDate(run.finishedAt)}` : ''}</small>
                          <small>{$t('knowledge.runStats', { files: run.fileCount, chunks: run.chunkCount, nodes: run.nodeCount, edges: run.edgeCount })}</small>
                          {#if run.errorSummary}
                            <small class="run-error">{$t('knowledge.runError')}: {run.errorSummary}</small>
                          {/if}
                        </article>
                      {/each}
                    {:else}
                      <p class="empty">{$t('knowledge.runsEmpty')}</p>
                    {/if}
                  </div>
                {:else if panel === 'sources'}
                  <div class="knowledge-results">
                    {#if sources.length}
                      {#each sources as source (source.path)}
                        <article>
                          <strong>{source.title || source.path}</strong>
                          <small>{source.path}</small>
                          <small>{source.status} · {formatSize(source.byteSize)} · {$t('knowledge.sourceChunks', { count: source.chunkCount })}</small>
                        </article>
                      {/each}
                    {:else}
                      <p class="empty">{$t('knowledge.sourcesEmpty')}</p>
                    {/if}
                  </div>
                {:else}
                  <form class="knowledge-query" onsubmit={(event) => { event.preventDefault(); runQuery(); }}>
                    <label class="query-field">
                      <span>{$t('knowledge.query')}</span>
                      <input bind:value={query} placeholder={$t('knowledge.queryPlaceholder')} />
                    </label>
                    <Button type="submit" variant="outline" disabled={!query.trim() || busy === `query:${editing.id}`}>
                      {$t('knowledge.query')}
                    </Button>
                  </form>

                  {#if queryResult}
                    <div class="knowledge-results">
                      {#if queryResult.chunks?.length}
                        <h3>{$t('knowledge.queryResults')}</h3>
                        {#each queryResult.chunks as chunk (chunk.id)}
                          <article>
                            <strong>{chunk.relativePath || chunk.fileId}</strong>
                            <small>L{chunk.startLine}–{chunk.endLine}</small>
                            <pre>{chunk.text}</pre>
                          </article>
                        {/each}
                      {:else}
                        <p class="empty">{$t('knowledge.queryEmpty')}</p>
                      {/if}
                    </div>
                  {/if}
                {/if}
              </div>
            {/if}
          </Card.Content>
        </Card.Root>
      {/if}
    </div>
  {/if}
</section>

<DirBrowser
  bind:open={dirBrowserOpen}
  initialPath={editing?.rootDir?.trim() || ''}
  on:select={(event) => { if (event.detail?.path && editing) editing.rootDir = event.detail.path; }}
/>

<style>
  .knowledge-grid {
    display: grid;
    grid-template-columns: minmax(260px, 0.8fr) minmax(0, 1.2fr);
    gap: 16px;
    align-items: start;
  }
  .knowledge-list {
    display: grid;
    gap: 10px;
  }
  :global(.knowledge-card-content) {
    display: flex;
    gap: 10px;
    justify-content: space-between;
    align-items: center;
  }
  .knowledge-open {
    text-align: left;
    display: grid;
    gap: 4px;
    min-width: 0;
    background: none;
    border: 0;
    color: inherit;
    cursor: pointer;
  }
  .knowledge-open span,
  .knowledge-open small {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--muted-foreground);
  }
  .knowledge-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }
  .editor-title {
    margin: 0 0 12px;
    font-size: 1.125rem;
    font-weight: 600;
  }
  .knowledge-fields {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px;
  }
  .knowledge-fields label {
    display: grid;
    gap: 6px;
    font-size: 0.875rem;
  }
  .knowledge-fields label.checkbox {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .knowledge-fields label.wide {
    grid-column: 1 / -1;
  }
  .knowledge-fields input,
  .knowledge-fields select,
  .knowledge-query input {
    min-width: 0;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--background);
    color: inherit;
    padding: 8px;
  }
  .directory-field { display:flex; gap:8px; }
  .directory-field input { flex:1; }
  .knowledge-panels {
    margin-top: 18px;
    display: grid;
    gap: 12px;
  }
  .knowledge-tabs {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
    border-bottom: 1px solid var(--border);
    padding-bottom: 8px;
  }
  .knowledge-tabs button {
    background: none;
    border: 0;
    color: var(--muted-foreground);
    cursor: pointer;
    padding: 4px 2px;
    font: inherit;
  }
  .knowledge-tabs button.active {
    color: inherit;
    font-weight: 600;
    border-bottom: 2px solid var(--primary, var(--border));
  }
  .knowledge-tabs :global(button:last-child) {
    margin-left: auto;
  }
  .badge {
    margin-left: 6px;
    font-style: normal;
    font-size: 0.75rem;
    font-weight: 500;
    color: var(--muted-foreground);
    border: 1px solid var(--border);
    border-radius: 999px;
    padding: 0 6px;
  }
  .run-error {
    color: var(--destructive, #b91c1c);
  }
  .knowledge-query {
    display: flex;
    gap: 8px;
    margin-top: 18px;
    align-items: flex-end;
  }
  .knowledge-query .query-field {
    flex: 1;
    display: grid;
    gap: 6px;
    font-size: 0.875rem;
  }
  .knowledge-query input {
    width: 100%;
  }
  .knowledge-results {
    display: grid;
    gap: 10px;
    margin-top: 14px;
  }
  .knowledge-results h3 {
    margin: 0;
    font-size: 0.875rem;
    font-weight: 600;
  }
  .knowledge-results article {
    border-top: 1px solid var(--border);
    padding-top: 10px;
    display: grid;
    gap: 5px;
  }
  .knowledge-results small {
    color: var(--muted-foreground);
  }
  .knowledge-results pre {
    margin: 0;
    white-space: pre-wrap;
    font: inherit;
    color: var(--muted-foreground);
  }
  .empty-state {
    display: grid;
    gap: 12px;
    justify-items: start;
  }
  @media (max-width: 760px) {
    .knowledge-grid {
      grid-template-columns: minmax(0, 1fr);
    }
    .knowledge-fields {
      grid-template-columns: minmax(0, 1fr);
    }
  }
</style>
