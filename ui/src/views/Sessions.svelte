<script>
  import { onMount, onDestroy } from 'svelte';
  import { currentSession, sessions, setError, setNotice, clearBanners, isMobile } from '../lib/stores.js';
  import { del, postJSON, request } from '../lib/api.js';
  import { navigate } from '../lib/router.js';
  import { formatDateTime, shortID } from '../lib/format.js';
  import { t } from '../lib/preferences.js';
  import { Badge } from '$lib/components/ui/badge/index.js';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Input } from '$lib/components/ui/input/index.js';
  import {
    ChevronLeft,
    ChevronRight,
    ChevronsLeft,
    ChevronsRight,
    ExternalLink,
    GitFork,
    RefreshCw,
    Search,
    Trash2
  } from '@lucide/svelte';

  const pageSize = 25;

  // pageItems holds the server-paginated snapshot; items is derived from it
  // merged with the shared sessions store so run-state updates stay live.
  let pageItems = [];
  let items = [];
  let total = 0;
  let loading = false;
  let filter = '';
  let page = 1;
  let previousFilter = '';

  $: items = mergePageWithSessions(pageItems, $sessions);

  $: totalPages = Math.max(1, Math.ceil(total / pageSize));
  $: if (filter !== previousFilter) {
    page = 1;
    previousFilter = filter;
  }
  $: if (page > totalPages) page = totalPages;
  $: if (page < 1) page = 1;
  $: pageStart = total === 0 ? 0 : (page - 1) * pageSize + 1;
  $: pageEnd = Math.min(total, page * pageSize);
  $: pageNumbers = buildPageNumbers(page, totalPages);

  $: fetchPage(page, filter);

  function isBusy(item) {
    if (item?.execution) return Boolean(item.execution.busy);
    return Boolean(item?.running);
  }

  function isRunning(item) {
    if (item?.execution) return Boolean(item.execution.running);
    return Boolean(item?.running);
  }

  function statusLabel(item) {
    const state = item?.execution?.state;
    if (state && state !== 'idle') return $t(`chat.execution.${state}`);
    return item?.active ? $t('sessions.active') : $t('sessions.history');
  }

  async function fetchPage(p, term) {
    loading = true;
    try {
      const offset = (p - 1) * pageSize;
      const params = new URLSearchParams({ limit: String(pageSize), offset: String(offset) });
      if (term.trim()) params.set('search', term.trim());
      const data = await request(`/api/sessions?${params}`);
      const list = data?.sessions || [];
      total = Number(data?.total) || list.length;
      pageItems = list;
    } catch (err) {
      setError(err);
      pageItems = [];
      total = 0;
    } finally {
      loading = false;
    }
  }


  function open(id) {
    currentSession.set(id);
    navigate(id ? `/chat?session=${encodeURIComponent(id)}` : '/chat');
  }

  function mergePageWithSessions(page, all) {
    if (!page.length || !all.length) return page;
    const byId = new Map(all.map((s) => [s.id, s]));
    return page.map((item) => {
      const shared = byId.get(item.id);
      if (!shared) return item;
      // Only apply the authoritative execution snapshot; never fall back to a
      // stale local `running` boolean and never remove an existing snapshot when
      // the server response lacks one (older server compatibility).
      if (!shared.execution) return item;
      return { ...item, execution: shared.execution };
    });
  }

  function buildPageNumbers(current, total) {
    if (total <= 7) return Array.from({ length: total }, (_, i) => i + 1);
    const pages = [1];
    const start = Math.max(2, current - 1);
    const end = Math.min(total - 1, current + 1);
    if (start > 2) pages.push('gap-start');
    for (let n = start; n <= end; n += 1) pages.push(n);
    if (end < total - 1) pages.push('gap-end');
    pages.push(total);
    return pages;
  }

  function goToPage(next) {
    page = Math.min(totalPages, Math.max(1, Number(next) || 1));
  }

  async function remove(item) {
    const id = item?.id || '';
    if (!id || isBusy(item)) return;
    const prompt = item?.bound
      ? $t('sessions.confirmUnbindDelete', { id: shortID(id) })
      : $t('sessions.confirmDelete', { id: shortID(id) });
    if (!window.confirm(prompt)) return;
    clearBanners();
    let unbound = false;
    try {
      if (item?.bound) {
        await del(`/api/sessions/${encodeURIComponent(id)}/bindings`);
        unbound = true;
      }
      await del(`/api/sessions/${encodeURIComponent(id)}`);
      if ($currentSession === id) currentSession.set('');
      setNotice($t('sessions.deleted', { id: shortID(id) }));
      await fetchPage(page, filter);
    } catch (err) {
      if (unbound) setNotice($t('sessions.unboundDeleteFailed', { id: shortID(id) }));
      setError(err);
      await fetchPage(page, filter);
    }
  }

  function idempotencyKey() {
    if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID();
    return `webui-fork-${Date.now()}-${Math.random().toString(16).slice(2)}`;
  }

  async function fork(item) {
    const id = item?.id || '';
    if (!id || isBusy(item)) return;
    clearBanners();
    try {
      const result = await postJSON(`/api/sessions/${encodeURIComponent(id)}/fork`, {}, {
        headers: { 'Idempotency-Key': idempotencyKey() }
      });
      await fetchPage(page, filter);
      if (result?.sessionId) open(result.sessionId);
    } catch (err) {
      setError(err);
    }
  }
</script>

<section class="page sessions-page">
  <div class="sessions-toolbar">
    <label class="sessions-search">
      <Search class="sessions-search-icon" size={15} aria-hidden="true" />
      <Input
        class="filter sessions-filter"
        bind:value={filter}
        aria-label={$t('sessions.filter')}
        placeholder={$t('sessions.filter')}
      />
    </label>
    <div class="sessions-toolbar-actions">
      <span class="sessions-total">{$t('common.items', { count: total })}</span>
      <Button variant="ghost" size="sm" class="sessions-refresh" disabled={loading} onclick={() => fetchPage(page, filter)}>
        <RefreshCw size={15} class={loading ? 'animate-spin' : ''} aria-hidden="true" />
        {$t('common.refresh')}
      </Button>
    </div>
  </div>

  <div class="page-body sessions-content">
    {#if loading}
      <div class="session-state"><span class="spinner sm"></span><span>{$t('common.loading')}</span></div>
    {:else if $isMobile}
      <div class="session-cards">
        {#each items as s (s.id)}
          <div class="session-card" class:active={$currentSession === s.id}>
            <button
              type="button"
              class="link-btn session-card-title"
              title={s.title || s.preview || shortID(s.id)}
              on:click={() => open(s.id)}
            >
              {s.title || s.preview || shortID(s.id)}
            </button>
            <div class="session-card-meta">
              <Badge variant="secondary" class={`session-card-status${isRunning(s) ? ' running' : ''}`}><span class:running={isRunning(s)} class="session-status-dot"></span>{s.channelLabel || $t('sessions.local')} · {statusLabel(s)}</Badge>
              <span class="session-card-time" title={formatDateTime(s.lastUsed)}><span class="session-meta-label">{$t('sessions.lastReply')}</span>{formatDateTime(s.lastUsed) || '—'}</span>
              <span class="session-card-count"><span class="session-meta-label">{$t('sessions.messageCount')}</span>{s.messageCount || 0}</span>
            </div>
            <div class="session-card-id" title={s.id}>{s.id}</div>
            {#if s.workDir}
              <div class="session-card-wd" title={s.workDir}>{s.workDir}</div>
            {/if}
            {#if s.preview && s.title}
              <div class="session-card-preview" title={s.preview}>{s.preview}</div>
            {/if}
            <div class="session-card-actions">
              <div class="session-action-group">
                <Button variant="ghost" size="icon-sm" class="session-action session-action-open" title={$t('common.open')} aria-label={$t('common.open')} onclick={() => open(s.id)}><ExternalLink size={15} aria-hidden="true" /></Button>
                <Button variant="ghost" size="icon-sm" class="session-action session-action-fork" title={$t('sessions.fork')} aria-label={$t('sessions.fork')} disabled={isBusy(s)} onclick={() => fork(s)}><GitFork size={15} aria-hidden="true" /></Button>
                <Button variant="ghost" size="icon-sm" class="session-action session-action-delete" title={$t('common.delete')} aria-label={$t('common.delete')} disabled={isBusy(s)} onclick={() => remove(s)}><Trash2 size={15} aria-hidden="true" /></Button>
              </div>
            </div>
          </div>
        {/each}
        {#if items.length === 0}
          <p class="empty">{$t('sessions.empty')}</p>
        {/if}
      </div>
    {:else}
      <div class="sessions-table-wrap">
        <table class="table sessions-table">
          <colgroup>
            <col class="session-col" />
            <col class="workdir-col" />
            <col class="status-col" />
            <col class="last-reply-col" />
            <col class="count-col" />
            <col class="actions-col" />
          </colgroup>
          <thead>
            <tr>
              <th>{$t('sessions.session')}</th>
              <th>{$t('sessions.workDir')}</th>
              <th>{$t('sessions.status')}</th>
              <th>{$t('sessions.lastReply')}</th>
              <th class="num">{$t('sessions.messageCount')}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {#each items as s (s.id)}
              <tr class:active={$currentSession === s.id}>
                <td class="session-cell">
                  <button
                    type="button"
                    class="link-btn session-title"
                    title={s.title || s.preview || shortID(s.id)}
                    on:click={() => open(s.id)}
                  >
                    {s.title || s.preview || shortID(s.id)}
                  </button>
                  <div class="session-subline">
                    <span class="sub session-id-line" title={s.id}>{s.id}</span>
                    {#if s.preview && s.title}
                      <span class="sub session-preview" title={s.preview}>{s.preview}</span>
                    {/if}
                  </div>
                </td>
                <td class="wd" title={s.workDir || ''}>{s.workDir || '—'}</td>
                <td><Badge variant="secondary" class={`session-status-badge${isRunning(s) ? ' running' : ''}`}><span class:running={isRunning(s)} class="session-status-dot"></span>{s.channelLabel || $t('sessions.local')} · {statusLabel(s)}</Badge></td>
                <td class="last-reply" title={formatDateTime(s.lastUsed)}>{formatDateTime(s.lastUsed) || '—'}</td>
                <td class="num">{s.messageCount || 0}</td>
                <td class="actions">
                  <div class="session-action-group">
                    <Button variant="ghost" size="icon-sm" class="session-action session-action-open" title={$t('common.open')} aria-label={$t('common.open')} onclick={() => open(s.id)}><ExternalLink size={15} aria-hidden="true" /></Button>
                    <Button variant="ghost" size="icon-sm" class="session-action session-action-fork" title={$t('sessions.fork')} aria-label={$t('sessions.fork')} disabled={isBusy(s)} onclick={() => fork(s)}><GitFork size={15} aria-hidden="true" /></Button>
                    <Button variant="ghost" size="icon-sm" class="session-action session-action-delete" title={$t('common.delete')} aria-label={$t('common.delete')} disabled={isBusy(s)} onclick={() => remove(s)}><Trash2 size={15} aria-hidden="true" /></Button>
                  </div>
                </td>
              </tr>
            {/each}
            {#if items.length === 0}
              <tr>
                <td colspan="6" class="empty-cell">{$t('sessions.empty')}</td>
              </tr>
            {/if}
          </tbody>
        </table>
      </div>
    {/if}
    {#if total > pageSize}
      <div class="stats-pagination sessions-pagination">
        <Button
          variant="outline"
          size="icon-sm"
          class="session-page-control"
          disabled={page <= 1}
          title={$t('common.first')}
          aria-label={$t('common.first')}
          onclick={() => goToPage(1)}
        ><ChevronsLeft size={15} aria-hidden="true" /></Button>
        <Button
          variant="outline"
          size="icon-sm"
          class="session-page-control"
          disabled={page <= 1}
          title={$t('common.previous')}
          aria-label={$t('common.previous')}
          onclick={() => goToPage(page - 1)}
        ><ChevronLeft size={15} aria-hidden="true" /></Button>
        {#each pageNumbers as item}
          {#if typeof item === 'number'}
            <Button
              variant={item === page ? 'secondary' : 'ghost'}
              size="icon-sm"
              class="session-page-control session-page-number"
              aria-current={item === page ? 'page' : undefined}
              onclick={() => goToPage(item)}
            >
              {item}
            </Button>
          {:else}
            <span class="page-gap" aria-hidden="true">...</span>
          {/if}
        {/each}
        <Button
          variant="outline"
          size="icon-sm"
          class="session-page-control"
          disabled={page >= totalPages}
          title={$t('common.nextPage')}
          aria-label={$t('common.nextPage')}
          onclick={() => goToPage(page + 1)}
        ><ChevronRight size={15} aria-hidden="true" /></Button>
        <Button
          variant="outline"
          size="icon-sm"
          class="session-page-control"
          disabled={page >= totalPages}
          title={$t('common.last')}
          aria-label={$t('common.last')}
          onclick={() => goToPage(totalPages)}
        ><ChevronsRight size={15} aria-hidden="true" /></Button>
        <span class="page-info">{$t('sessions.pageRange', { start: pageStart, end: pageEnd, total: total })}</span>
      </div>
    {/if}
  </div>
</section>
