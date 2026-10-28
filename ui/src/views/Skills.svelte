<script>
  import { onMount } from 'svelte';
  import { currentSession, sessions, setError, setNotice, clearBanners } from '../lib/stores.js';
  import { request, postJSON } from '../lib/api.js';
  import { t } from '../lib/preferences.js';
  import { Download, Package, Search, Star, X } from '@lucide/svelte';
  import { Badge } from '$lib/components/ui/badge';
  import { Button } from '$lib/components/ui/button';

  const pageSize = 20;
  let markets = [];
  let defaultMarket = 'skillhub.cn';
  let market = 'skillhub.cn';
  let view = 'official';
  let query = '';
  let category = '';
  let sort = 'downloads';
  let scope = 'project';
  let selectedSession = '';
  let targetDir = '';
  let targets = [];
  let targetLoading = false;
  let page = 1;
  let cursor = '';
  let cursorHistory = [];
  let nextCursor = '';
  let total = 0;
  let items = [];
  let selected = null;
  let detail = null;
  let categories = [];
  let activeSkills = [];
  let loading = false;
  let detailLoading = false;
  let actionLoading = false;
  let selectedBatch = new Set();
  let resultsRequest = 0;
  let detailRequest = 0;

  $: sessionID = selectedSession || '';
  $: selectedSessionInfo = $sessions.find((item) => item.id === sessionID) || null;
  $: isCursorMarket = market === 'clawhub.ai' || market === 'yuandun';
  $: supportsFilters = market === 'skillhub.cn' || market === 'yuandun';
  $: canPrevious = isCursorMarket ? cursorHistory.length > 0 : page > 1;
  $: canNext = isCursorMarket ? Boolean(nextCursor) : page * pageSize < total;
  $: detailDrawerOpen = detailLoading || Boolean(detail);

  onMount(async () => {
    if (!selectedSession && $currentSession) selectedSession = $currentSession;
    await loadMarkets();
    await Promise.all([loadCategories(), loadInstalled(), loadTargets()]);
    await loadResults();
  });

  async function loadMarkets() {
    try {
      const data = await request(`/api/skillhub/markets?${sessionParams()}`);
      markets = data?.markets || [];
      const preferred = data?.defaultMarket || defaultMarket;
      if (markets.some((entry) => entry.id === preferred)) {
        defaultMarket = preferred;
        market = preferred;
        view = market === 'skillhub.cn' ? 'official' : 'browse';
      }
    } catch {
      markets = [
        { id: 'yuandun', name: '源盾' },
        { id: 'skillhub.cn', name: 'SkillHub.cn' },
        { id: 'clawhub.ai', name: 'ClawHub.ai' }
      ];
    }
  }

  function sessionParams(extra = {}) {
    const params = new URLSearchParams(extra);
    if (sessionID) params.set('sessionId', sessionID);
    return params;
  }

  async function loadInstalled() {
    try {
      const data = await request(`/api/skillhub/installed?${sessionParams()}`);
      activeSkills = data?.session?.activeSkills || [];
    } catch (err) {
      setError(err);
    }
  }

  async function loadCategories() {
    const supports = market === 'skillhub.cn' || market === 'yuandun';
    if (!supports) {
      categories = [];
      return;
    }
    try {
      const data = await request(`/api/skillhub/categories?${sessionParams({ market })}`);
      categories = data?.categories || [];
    } catch {
      categories = [];
    }
  }

  async function loadTargets() {
    targets = [];
    targetDir = '';
    if (!sessionID) return;
    targetLoading = true;
    try {
      const data = await request(`/api/skillhub/targets?${sessionParams()}`);
      targets = data?.targets || [];
    } catch (err) {
      setError(err);
    } finally {
      targetLoading = false;
    }
  }

  function selectSession(id) {
    selectedSession = id;
    loadTargets();
    loadInstalled();
  }

  async function loadResults() {
	const requestID = ++resultsRequest;
	const cursorMarket = market === 'clawhub.ai' || market === 'yuandun';
	detailRequest += 1;
	detail = null;
    loading = true;
    clearBanners();
    try {
      const params = sessionParams({ market, limit: String(pageSize) });
      let endpoint = '/api/skillhub/search';
      if (view === 'official') {
        endpoint = '/api/skillhub/official';
        params.set('page', String(page));
        if (query.trim()) params.set('q', query.trim());
      } else {
        if (view === 'search' && query.trim()) params.set('q', query.trim());
        if (cursorMarket) {
          if (cursor) params.set('cursor', cursor);
        } else {
          params.set('page', String(page));
          params.set('sort', sort);
          params.set('order', 'desc');
          if (category) params.set('category', category);
        }
        if (market === 'yuandun') {
          const sortValue = sort === 'updated_at' ? 'latest' : sort === 'installs' ? 'downloads' : sort === 'score' ? 'comprehensive' : sort;
          params.set('sort', sortValue);
          if (category) params.set('category', category);
        }
      }
      const data = await request(`${endpoint}?${params}`);
	  if (requestID !== resultsRequest) return;
      items = data?.items || [];
      total = Number(data?.total || items.length);
      nextCursor = data?.nextCursor || '';
      selected = null;
      detail = null;
    } catch (err) {
	  if (requestID !== resultsRequest) return;
      items = [];
      selected = null;
      detail = null;
      setError(err);
    } finally {
	  if (requestID === resultsRequest) loading = false;
    }
  }

  async function loadDetail(item) {
    if (!item) return;
	const requestID = ++detailRequest;
    selected = item;
    detailLoading = true;
    try {
      const params = sessionParams();
	  const next = await request(`/api/skillhub/skills/${encodeURIComponent(item.market)}/${encodeURIComponent(item.id)}?${params}`);
	  if (requestID !== detailRequest) return;
	  detail = next;
    } catch (err) {
	  if (requestID !== detailRequest) return;
      detail = null;
      setError(err);
    } finally {
	  if (requestID === detailRequest) detailLoading = false;
    }
  }

  function closeDetail() {
    detailRequest += 1;
    detail = null;
    detailLoading = false;
    selected = null;
  }

  function switchMarket(next) {
    if (market === next) return;
    market = next;
    category = '';
    view = market === 'skillhub.cn' ? 'official' : 'browse';
    resetPaging();
    loadCategories();
    loadResults();
  }

  function switchView(next) {
    if (next === 'official' && market !== 'skillhub.cn') return;
    view = next;
    resetPaging();
    loadResults();
  }

  function search() {
    view = query.trim() ? 'search' : market === 'skillhub.cn' ? 'official' : 'browse';
    resetPaging();
    loadResults();
  }

  function resetPaging() {
    page = 1;
    cursor = '';
    cursorHistory = [];
    nextCursor = '';
  }

  function previousPage() {
    const cursorMarket = market === 'clawhub.ai' || market === 'yuandun';
    if (!canPrevious) return;
    if (cursorMarket) {
      cursor = cursorHistory[cursorHistory.length - 1] || '';
      cursorHistory = cursorHistory.slice(0, -1);
      page = Math.max(1, page - 1);
    } else {
      page -= 1;
    }
    loadResults();
  }

  function nextPage() {
    const cursorMarket = market === 'clawhub.ai' || market === 'yuandun';
    if (!canNext) return;
    if (cursorMarket) {
      cursorHistory = [...cursorHistory, cursor];
      cursor = nextCursor;
    }
    page += 1;
    loadResults();
  }

  function marketLabel(entry) {
    return entry?.name || entry?.id || '';
  }

  async function loadShowcase(kind = 'recommended') {
    if (market !== 'skillhub.cn') return;
    loading = true; clearBanners();
    try { const data = await request(`/api/skillhub/showcase/${encodeURIComponent(kind)}?${sessionParams({ market })}`); items = data?.items || []; total = Number(data?.total || items.length); selected = null; detail = null; } catch (err) { setError(err); } finally { loading = false; }
  }
  function toggleBatch(item) { const next = new Set(selectedBatch); const key = `${item.market}:${item.id}`; if (next.has(key)) next.delete(key); else next.add(key); selectedBatch = next; }
  async function installBatch() { const chosen = items.filter((item) => selectedBatch.has(`${item.market}:${item.id}`)); if (!chosen.length || !sessionID || !targetDir) return; actionLoading = true; try { await postJSON('/api/skillhub/skillset', { skills: chosen.map((item) => ({ market: item.market, id: item.id, version: item.version || '', scope, targetDir })), sessionId: sessionID, targetDir, scope }); selectedBatch = new Set(); setNotice('Selected skills installed.'); await loadResults(); } catch (err) { setError(err); } finally { actionLoading = false; } }
  async function loadFileContent(file) { if (!detail || !file?.path) return; try { const data = await request(`/api/skillhub/content/${encodeURIComponent(detail.market)}/${encodeURIComponent(detail.id)}?${sessionParams({ path: file.path, version: detail.version || '' })}`); file.content = data?.content || ''; detail = { ...detail }; } catch (err) { setError(err); } }
  async function install(activate = false, overwrite = false) {
    if (!detail) return;
    actionLoading = true;
    clearBanners();
    try {
      const data = await postJSON('/api/skillhub/install', {
        market: detail.market,
        id: detail.id,
        version: detail.version || '',
        scope,
        sessionId: sessionID,
        targetDir,
        overwrite,
        activate
      });
      activeSkills = data?.session?.activeSkills || activeSkills;
      setNotice(overwrite ? $t('skills.updated', { name: data?.install?.name || detail.name }) : $t('skills.installed', { name: data?.install?.name || detail.name }));
      await loadResults();
    } catch (err) {
      setError(err);
    } finally {
      actionLoading = false;
    }
  }

  async function activate() {
    const name = installedName(detail);
    if (!name) return;
    actionLoading = true;
    clearBanners();
    try {
      const data = await postJSON('/api/skillhub/activate', { name, sessionId: sessionID });
      activeSkills = data?.session?.activeSkills || activeSkills;
      setNotice($t('skills.activated', { name }));
    } catch (err) {
      setError(err);
    } finally {
      actionLoading = false;
    }
  }

  async function uninstall() {
    if (!detail) return;
    actionLoading = true;
    clearBanners();
    try {
      await postJSON('/api/skillhub/uninstall', { market: detail.market, id: detail.id, scope, sessionId: sessionID });
      activeSkills = activeSkills.filter((name) => name !== installedName(detail));
      setNotice(`Uninstalled ${detail.name || detail.slug}`);
      await loadResults();
    } catch (err) {
      setError(err);
    } finally {
      actionLoading = false;
    }
  }

  function installedName(item) {
    const dir = item?.installed?.dir || '';
    if (dir) return dir.split(/[\\/]/).filter(Boolean).pop() || '';
    return item?.slug || '';
  }

  function isActive(item) {
    const name = installedName(item);
    return name && activeSkills.includes(name);
  }

  function statusBadges(item) {
    const values = [];
    if (item?.suspicious) values.push({ key: 'risk', variant: 'destructive' });
    if (isActive(item)) {
      values.push({ key: 'active', variant: 'default' });
    } else if (item?.installed?.updateAvailable) {
      values.push({ key: 'update', variant: 'secondary' });
    } else if (item?.installed?.installed) {
      values.push({ key: 'installed', variant: 'secondary' });
    }
    return values;
  }

  function badges(item) {
    const values = [];
    if (view === 'official' && item?.market === 'skillhub.cn') values.push('official');
    if (item?.publisherVerified) values.push('certified');
    if (item?.verified) values.push('verified');
    if (item?.suspicious) values.push('risk');
    if (item?.installed?.installed) values.push('installed');
    if (item?.installed?.updateAvailable) values.push('update');
    if (isActive(item)) values.push('active');
    return values;
  }

  function formatCount(value) {
    return new Intl.NumberFormat().format(Number(value || 0));
  }

  function formatCompact(value) {
    const count = Number(value || 0);
    return count >= 1000 ? `${(count / 1000).toFixed(1)}k` : String(count);
  }

  function formatReport(value) {
    if (!value || typeof value !== 'object') return $t('skills.notAvailable');
    return JSON.stringify(value, null, 2);
  }

  function securityLabel(item) {
    const status = item?.securityStatus || item?.securityReports?.yuandun?.status;
    switch (status) {
      case 'SAFE': return { key: 'securitySafe', tone: 'positive' };
      case 'RISKY': return { key: 'securityRisky', tone: 'warning' };
      case 'SUSPECTED': return { key: 'securitySuspected', tone: 'warning' };
      case 'AUDITING': return { key: 'securityAuditing', tone: '' };
      case 'NOT_AUDITED': return { key: 'securityNotAudited', tone: '' };
    }
    if (item?.suspicious) return { key: 'securityRisky', tone: 'warning' };
    return null;
  }
</script>

<svelte:window on:keydown={(event) => { if (event.key === 'Escape' && detailDrawerOpen) closeDetail(); }} />

<section class="page skills-page">
  <div class="skills-hero">
    <div class="skills-hero-copy">
      <span class="skills-hero-icon" aria-hidden="true"><Package size={22} /></span>
      <div>
        <h1>{$t('skills.heroTitle')}</h1>
        <p>{$t('skills.heroSubtitle')}</p>
      </div>
    </div>
    <form class="skills-hero-search" on:submit|preventDefault={search}>
      <Search size={16} aria-hidden="true" />
      <input bind:value={query} placeholder={$t('skills.searchPlaceholder')} aria-label={$t('skills.search')} />
      <button type="submit" class="primary">{$t('skills.search')}</button>
    </form>
    <div class="skills-market-tabs" aria-label={$t('skills.market')}>
      {#each markets as entry}
        <button type="button" class:active={market === entry.id} on:click={() => switchMarket(entry.id)}>{marketLabel(entry)}</button>
      {/each}
    </div>
    <div class="skills-filter-row">
      <div class="segmented skills-view-tabs" aria-label={$t('skills.view')}>
        <button type="button" class:active={view === 'browse'} on:click={() => switchView('browse')}>{$t('skills.browse')}</button>
        <button type="button" class:active={view === 'search'} on:click={() => switchView('search')}>{$t('skills.search')}</button>
        {#if market === 'skillhub.cn'}
          <button type="button" class:active={view === 'official'} on:click={() => switchView('official')}>{$t('skills.official')}</button>
          <button type="button" on:click={() => loadShowcase('recommended')}>{$t('skills.showcase')}</button>
        {/if}
      </div>
      {#if supportsFilters && view === 'browse'}
        <label class="skills-filter-select">
          <span>{$t('skills.category')}</span>
          <select bind:value={category} on:change={() => { resetPaging(); loadResults(); }}>
            <option value="">{$t('skills.allCategories')}</option>
            {#each categories as item}
              <option value={item.key}>{item.nameEn || item.name || item.key}</option>
            {/each}
          </select>
        </label>
        <label class="skills-filter-select">
          <span>{$t('skills.sort')}</span>
          <select bind:value={sort} on:change={() => { resetPaging(); loadResults(); }}>
            <option value="downloads">{$t('skills.downloads')}</option>
            <option value="stars">{$t('skills.stars')}</option>
            <option value="installs">{$t('skills.installs')}</option>
            <option value="score">Score</option>
            <option value="updated_at">{$t('skills.updatedAt')}</option>
          </select>
        </label>
      {/if}
    </div>
  </div>

  <div class="skills-workbench">
    <section class="skills-list" aria-label={$t('skills.results')}>
      <div class="skills-section-head">
        <strong>{$t('skills.results')}</strong>
        <span class="loading-row">{#if loading}<span class="spinner sm"></span>{$t('common.loading')}{:else}{$t('common.items', { count: total })}{/if}</span>
        <Button type="button" size="sm" disabled={selectedBatch.size === 0 || actionLoading || !sessionID || !targetDir} onclick={installBatch}>
          {#if actionLoading}<span class="spinner sm"></span> {/if}
          {$t('skills.installSelected', { count: selectedBatch.size })}
        </Button>
      </div>
      <div class="skills-rows">
        {#if loading && items.length === 0}
          <div class="spinner-center skills-rows-empty"><span class="spinner lg"></span><span>{$t('common.loading')}</span></div>
        {:else}
        {#each items as item (item.market + ':' + item.id)}
          {@const itemBadges = statusBadges(item)}
          {@const security = securityLabel(item)}
          <div class="skill-market-card-wrap">
            <button
              type="button"
              data-slot="skill-market-card"
              class="skill-market-card"
              class:selected={selected?.id === item.id}
              on:click={() => loadDetail(item)}
              aria-pressed={selected?.id === item.id}
            >
              <span class="skill-market-card-head">
                <span class="skill-market-card-icon" aria-hidden="true">
                  {#if item.iconUrl}<img src={item.iconUrl} alt="" />{:else}<Package size={18} />{/if}
                </span>
                <span class="skill-market-card-title">
                  <span class="skill-market-card-name">{item.displayName || item.name || item.slug || item.id}</span>
                  <span class="skill-market-card-meta">
                    {item.author || item.publisherName || item.market}
                    {#if item.version}<span class="skill-market-card-version">· v{item.version}</span>{/if}
                  </span>
                </span>
              </span>
              <span class="skill-market-card-desc">{item.description || $t('skills.noDescription')}</span>
              <span class="skill-market-card-foot">
                <span><Star size={12} aria-hidden="true" /><span class="tabular-nums">{formatCompact(item.stars)}</span></span>
                <span><Download size={12} aria-hidden="true" /><span class="tabular-nums">{formatCompact(item.downloads)}</span></span>
                {#each itemBadges.filter((badge) => badge.key !== 'risk') as badge}
                  <Badge variant={badge.variant}>{$t(`skills.badge.${badge.key}`)}</Badge>
                {/each}
                {#if security}<span class="skill-market-card-security" class:positive={security.tone === 'positive'} class:warning={security.tone === 'warning'}>{$t(`skills.${security.key}`)}</span>{/if}
              </span>
            </button>
            <label class="skill-market-check" title={$t('skills.selectForBatch')}>
              <input
                type="checkbox"
                checked={selectedBatch.has(`${item.market}:${item.id}`)}
                on:change={() => toggleBatch(item)}
                aria-label={`${$t('skills.selectForBatch')}: ${item.displayName || item.name || item.slug || item.id}`}
              />
            </label>
          </div>
        {/each}
        {#if !loading && items.length === 0}<p class="empty skills-rows-empty">{$t('skills.empty')}</p>{/if}
        {/if}
      </div>
      <div class="skills-pager">
        <button type="button" class="sm" disabled={!canPrevious || loading} on:click={previousPage} aria-label={$t('common.previous')}>‹</button>
        <span>{$t('skills.page', { page })}</span>
        <button type="button" class="sm" disabled={!canNext || loading} on:click={nextPage} aria-label={$t('common.nextPage')}>›</button>
      </div>
    </section>

    <div class="skills-detail-backdrop" class:open={detailDrawerOpen} on:click={closeDetail}></div>
    <section class="skills-detail" class:open={detailDrawerOpen} role="dialog" aria-modal={detailDrawerOpen ? 'true' : undefined} aria-label={$t('skills.detail')}>
      {#if detailLoading}
        <div class="spinner-center"><span class="spinner lg"></span><span>{$t('common.loading')}</span></div>
      {:else if detail}
        {@const security = securityLabel(detail)}
        <div class="skills-detail-layout">
          <div class="skills-detail-main">
            <div class="skills-detail-head">
              <div class="skills-detail-title">
                <span class="skills-detail-icon" aria-hidden="true">
                  {#if detail.iconUrl}<img src={detail.iconUrl} alt="" />{:else}<Package size={20} />{/if}
                </span>
                <div>
                  <h2>{detail.displayName || detail.name || detail.slug}</h2>
                  <p>{detail.author || detail.publisherName || detail.market} · {detail.version || $t('skills.notAvailable')}</p>
                </div>
              </div>
              <div class="skills-detail-head-actions">
                <span class="download-count"><Download size={13} aria-hidden="true" /> {formatCompact(detail.downloads)}</span>
                <button type="button" data-slot="skill-detail-close" class="skills-detail-close" on:click={closeDetail} aria-label={$t('common.close')}>
                  <X size={16} />
                </button>
              </div>
            </div>
            <p class="skills-summary">{detail.description || $t('skills.noDescription')}</p>
            <div class="skill-badges detail-badges">
              {#if security}<span class:warning={security.tone === 'warning'} class:positive={security.tone === 'positive'}>{$t(`skills.${security.key}`)}</span>{/if}
              {#each badges(detail).filter((badge) => !security || badge !== 'risk') as badge}<span class:warning={badge === 'risk'} class:positive={badge === 'active' || badge === 'certified' || badge === 'verified'}>{$t(`skills.badge.${badge}`)}</span>{/each}
            </div>
            <aside class="skills-detail-side">
              <div class="skills-install-target">
                <div class="skills-install-target-head">
                  <div>
                    <span class="skills-install-target-kicker">INSTALL TARGET</span>
                    <h3>{$t('skills.installTarget')}</h3>
                  </div>
                  <span class:ready={sessionID && targetDir} class="skills-install-target-state">{sessionID && targetDir ? $t('skills.ready') : $t('skills.notSelected')}</span>
                </div>
                <p class="skills-install-target-hint">{$t('skills.installTargetHint')}</p>
                <div class="skills-install-target-fields">
                  <label>
                    <span>{$t('skills.session')}</span>
                    <select bind:value={selectedSession} on:change={(event) => selectSession(event.currentTarget.value)}>
                      <option value="">{$t('skills.selectSession')}</option>
                      {#each $sessions as item}<option value={item.id}>{item.title || item.id} · {item.workDir || $t('skills.unknownDir')}</option>{/each}
                    </select>
                    {#if selectedSessionInfo}<small>{selectedSessionInfo.workDir || $t('skills.workDirUnknown')}</small>{/if}
                  </label>
                  <label>
                    <span>{$t('skills.installDir')}</span>
                    <select bind:value={targetDir} on:change={() => { scope = targets.find((item) => item.path === targetDir)?.scope || 'project'; }} disabled={!sessionID || targetLoading}>
                      <option value="">{$t('skills.selectInstallDir')}</option>
                      {#each targets as target}<option value={target.path}>{target.label} · {target.path}</option>{/each}
                    </select>
                    {#if targetDir}<small class="skills-target-path">{targetDir}</small>{/if}
                  </label>
                </div>
                <div class="skills-install-target-confirmation">
                  <span class="skills-target-dot"></span>
                  {#if sessionID && targetDir}
                    <span>{$t('skills.installTargetConfirm', { target: targetDir })}</span>
                  {:else}
                    <span>{$t('skills.installTargetHint')}</span>
                  {/if}
                </div>
              </div>

              <div class="skills-actions">
                {#if detail.installed?.updateAvailable}
                <button type="button" class="primary" disabled={actionLoading || !sessionID || !targetDir} on:click={() => install(false, true)}>{#if actionLoading}<span class="spinner sm"></span> {/if}{$t('skills.update')}</button>
                {:else if !detail.installed?.installed}
                <button type="button" class="primary" disabled={actionLoading || !sessionID || !targetDir} on:click={() => install(false, false)}>{#if actionLoading}<span class="spinner sm"></span> {/if}{$t('skills.install')}</button>
                <button type="button" disabled={actionLoading || !sessionID || !targetDir} on:click={() => install(true, false)}>{#if actionLoading}<span class="spinner sm"></span> {/if}{$t('skills.installActivate')}</button>
                {:else if !isActive(detail)}
                  <button type="button" class="primary" disabled={actionLoading} on:click={activate}>{#if actionLoading}<span class="spinner sm"></span> {/if}{$t('skills.activate')}</button>
                  <button type="button" disabled={actionLoading} on:click={uninstall}>{#if actionLoading}<span class="spinner sm"></span> {/if}{$t('skills.uninstall')}</button>
                {:else}
                  <span class="status-tag">{$t('skills.active')}</span>
                  <button type="button" disabled={actionLoading} on:click={uninstall}>{#if actionLoading}<span class="spinner sm"></span> {/if}{$t('skills.uninstall')}</button>
                {/if}
              </div>
            </aside>

            <dl class="skills-metadata">
              <div><dt>{$t('skills.market')}</dt><dd>{detail.market}</dd></div>
              <div><dt>{$t('skills.version')}</dt><dd>{detail.version || $t('skills.notAvailable')}</dd></div>
              <div><dt>{$t('skills.category')}</dt><dd>{detail.category || $t('skills.notAvailable')}</dd></div>
              <div><dt>{$t('skills.source')}</dt><dd>{detail.source || $t('skills.notAvailable')}</dd></div>
              <div><dt>{$t('skills.downloads')}</dt><dd>{formatCompact(detail.downloads)}</dd></div>
              <div><dt>{$t('skills.stars')}</dt><dd>{formatCompact(detail.stars)}</dd></div>
            </dl>
            {#if detail.tags?.length}
              <div class="skills-block"><h3>Tags</h3><p>{detail.tags.join(', ')}</p></div>
            {/if}
            <div class="skills-block">
              <h3>{$t('skills.files')} ({detail.files?.length || 0})</h3>
              <ul class="skill-files">
                {#each detail.files || [] as file}<li><code>{file.path}</code><span>{formatCount(file.size)} B <button type="button" data-slot="skill-file-view" on:click={() => loadFileContent(file)}>View</button></span>{#if file.content}<pre>{file.content}</pre>{/if}</li>{/each}
              </ul>
            </div>
            <div class="skills-block">
              <h3>{$t('skills.downloadSources')}</h3>
              {#if detail.downloadSources?.length}
                <ul class="download-sources">
                  {#each detail.downloadSources as source}
                    <li><span>{source.kind}{source.fallback ? ` · ${$t('skills.fallback')}` : ''}</span><code>{source.url}</code></li>
                  {/each}
                </ul>
              {:else}<p>{$t('skills.notAvailable')}</p>{/if}
            </div>
            <details class="skills-report"><summary>{$t('skills.security')}</summary><pre>{formatReport(detail.securityReports)}</pre></details>
            <details class="skills-report"><summary>{$t('skills.evaluation')}</summary><pre>{formatReport(detail.evaluation)}</pre></details>
          </div>


        </div>
      {:else}
        <p class="empty">{$t('skills.select')}</p>
      {/if}
    </section>
  </div>
</section>
