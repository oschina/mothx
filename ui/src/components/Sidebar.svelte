<script>
  import { onDestroy, onMount, tick } from 'svelte';
  import { get } from 'svelte/store';
  import { fly, fade } from 'svelte/transition';
  import { sessions, currentSession, features, statsSummary, refreshStatsSummary, refreshSessions, sidebarOpen, sidebarCollapsed, isMobile, setError } from '../lib/stores.js';
  import { shortID } from '../lib/format.js';
  import { route, navigate } from '../lib/router.js';
  import { t } from '../lib/preferences.js';
  import { request, jsonBody, del } from '../lib/api.js';
  import { openDraftInWorkbench } from '../lib/workbench/store.js';
  import PreferenceControls from './PreferenceControls.svelte';
  import Modal from './Modal.svelte';
  import {
    Brain,
    ChartNoAxesColumn,
    Check,
    ChevronRight,
    Clock3,
    Ellipsis,
    Folder,
    FolderPlus,
    ListFilter,
    PanelLeftClose,
    PanelLeftOpen,
    PenLine,
    Search,
    Settings2,
    Sparkles,
    Timer,
    UsersRound,
    X
  } from '@lucide/svelte';

  let searchTerm = '';
  let searchInput;
  let searchExpanded = false;
  let filterMode = 'all';
  let filterMenuOpen = false;
  let filterButton;
  let isMac = false;
  let newChatShortcut = 'Ctrl⇧K';
  let removeShortcutListener = null;
  let removeMenuOutsideListener = null;
  let historyScrollbarVisible = false;
  let historyPointerInside = false;
  let hideHistoryScrollbarTimer = null;
  let previousBodyOverflow = '';
  let previousRoutePath = '';
  let projects = [];
  let openMenu = '';
  let railSettled = false;
  let railSettleTimer = null;
  let collapsed = { projects: false, recent: false, unprojected: true };
  let collapsedProjects = {};
  let namingDialog = null;
  let nameDraft = '';
  let namingInput;

  $: if (namingDialog) {
    tick().then(() => {
      setTimeout(() => {
        namingInput?.focus();
        namingInput?.select();
      }, 0);
    });
  }

  const primaryNav = [
    { key: 'chat', path: '/chat', label: 'nav.newChat', icon: 'edit', accent: true },
    { key: 'sessions', path: '/sessions', label: 'nav.sessions', icon: 'clock' },
    { key: 'experts', path: '/experts', label: 'nav.experts', icon: 'experts' },
    { key: 'skills', path: '/skills', label: 'nav.skills', icon: 'skills' },
    { key: 'knowledge', path: '/knowledge', label: 'nav.knowledge', icon: 'knowledge' },
    { key: 'stats', path: '/stats', label: 'nav.stats', icon: 'chart' },
    { key: 'cron', path: '/cron', label: 'nav.cron', icon: 'timer' }
  ];

  const secondaryNav = [
    { key: 'settings', path: '/settings', label: 'nav.settings', icon: 'settings' }
  ];

  $: filteredSessions = filterSessions($sessions, searchTerm, filterMode);
  $: recentSessions = filteredSessions.slice(0, 8);
  $: unprojectedSessions = filteredSessions.filter((item) => !item.projectId);
  $: projectSessions = (projectID) => filteredSessions.filter((item) => item.projectId === projectID);
  $: summaryStats = $statsSummary || {};
  $: newChatAriaShortcut = isMac ? 'Shift+Meta+K' : 'Shift+Control+K';
  // Keep the expanded tree mounted during the short collapse transition so
  // the shell can clip and fade it before replacing it with the rail.
  $: if ($isMobile || !$sidebarCollapsed) {
    railSettled = false;
    if (railSettleTimer) {
      clearTimeout(railSettleTimer);
      railSettleTimer = null;
    }
  } else if (!railSettled && !railSettleTimer) {
    railSettleTimer = setTimeout(() => {
      railSettled = true;
      railSettleTimer = null;
    }, 150);
  }
  $: compact = !$isMobile && $sidebarCollapsed && railSettled;
  $: sidebarCollapsing = !$isMobile && $sidebarCollapsed && !railSettled;

  onMount(() => {
    isMac = /Mac|iPhone|iPad|iPod/.test(navigator.platform || '');
    newChatShortcut = isMac ? '⇧⌘K' : 'Ctrl⇧K';
    const onKeydown = (event) => {
      if (event.key === 'Escape' && filterMenuOpen) {
        event.preventDefault();
        closeFilterMenu(true);
        return;
      }
      if (event.key === 'Escape' && get(sidebarOpen)) {
        closeSidebar();
        return;
      }
      handleGlobalShortcut(event);
    };
    window.addEventListener('keydown', onKeydown);
    removeShortcutListener = () => window.removeEventListener('keydown', onKeydown);
    const onPointerDown = (event) => {
      if (openMenu && !event.target.closest('.side-menu, .more-btn')) openMenu = '';
      if (searchExpanded && !event.target.closest('.side-search, .sidebar-head-icon')) {
        if (!searchTerm) searchExpanded = false;
      }
      if (filterMenuOpen && !event.target.closest('.workspace-filter')) closeFilterMenu();
    };
    document.addEventListener('pointerdown', onPointerDown, true);
    removeMenuOutsideListener = () => document.removeEventListener('pointerdown', onPointerDown, true);
    refreshStatsSummary();
    loadProjects();
  });

  $: if ($isMobile && $sidebarOpen) lockBodyScroll();
  $: if ((!$isMobile || !$sidebarOpen) && previousBodyOverflow !== '') unlockBodyScroll();
  $: if ($route.path !== previousRoutePath) {
    previousRoutePath = $route.path;
    if (previousRoutePath && $sidebarOpen) closeSidebar();
  }

  function lockBodyScroll() {
    if (previousBodyOverflow !== '') return;
    previousBodyOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
  }

  function unlockBodyScroll() {
    document.body.style.overflow = previousBodyOverflow;
    previousBodyOverflow = '';
  }

  onDestroy(() => {
    removeShortcutListener?.();
    removeMenuOutsideListener?.();
    if (hideHistoryScrollbarTimer) clearTimeout(hideHistoryScrollbarTimer);
    if (railSettleTimer) clearTimeout(railSettleTimer);
    if (previousBodyOverflow !== '') unlockBodyScroll();
  });

  function filterSessions(list, term, mode = 'all') {
    const t = term.trim().toLowerCase();
    const scoped = mode === 'projects'
      ? list.filter((session) => session.projectId)
      : mode === 'unprojected'
        ? list.filter((session) => !session.projectId)
        : list;
    if (!t) return scoped;
    return scoped.filter((s) => {
      const hay = `${s.id || ''} ${s.workDir || ''} ${(s.title || '')}`.toLowerCase();
      return hay.includes(t);
    });
  }

  function toggleFilterMenu() {
    filterMenuOpen = !filterMenuOpen;
    if (filterMenuOpen) openMenu = '';
  }

  function closeFilterMenu(restoreFocus = false) {
    if (!filterMenuOpen) return;
    filterMenuOpen = false;
    if (restoreFocus) tick().then(() => filterButton?.focus());
  }

  function setFilterMode(mode) {
    filterMode = mode;
    closeFilterMenu(true);
  }

  async function loadProjects() {
    try { projects = (await request('/api/projects'))?.projects || []; } catch (err) { setError(err); }
  }

  function toggle(section) { collapsed = { ...collapsed, [section]: !collapsed[section] }; }
  function toggleProject(id) { collapsedProjects = { ...collapsedProjects, [id]: !collapsedProjects[id] }; }
  function toggleMenu(event, key) { event.stopPropagation(); openMenu = openMenu === key ? '' : key; }
  function updateSessionLocal(id, patch) {
    sessions.update((list) => list.map((item) => item.id === id ? { ...item, ...patch } : item));
  }
  async function patchSession(id, body) {
    updateSessionLocal(id, body);
    try {
      const updated = await request(`/api/sessions/${encodeURIComponent(id)}/metadata`, { method: 'PATCH', ...jsonBody(body) });
      if (updated) updateSessionLocal(id, updated);
      await refreshSessions();
    } catch (err) { await refreshSessions(); setError(err); }
    openMenu = '';
  }
  function openNamingDialog(kind, target = null, current = '') {
    openMenu = '';
    namingDialog = { kind, target };
    nameDraft = current || '';
  }
  function closeNamingDialog() {
    namingDialog = null;
    nameDraft = '';
  }
  async function submitNamingDialog() {
    const name = nameDraft.trim();
    if (!name || !namingDialog) return;
    const dialog = namingDialog;
    closeNamingDialog();
    try {
      if (dialog.kind === 'session') {
        updateSessionLocal(dialog.target, { title: name });
        try {
          const updated = await request(`/api/sessions/${encodeURIComponent(dialog.target)}/title`, { method: 'POST', ...jsonBody({ title: name }) });
          if (updated) updateSessionLocal(dialog.target, updated);
          await refreshSessions();
        } catch (err) { await refreshSessions(); throw err; }
      } else if (dialog.kind === 'new-project') {
        await request('/api/projects', { method: 'POST', ...jsonBody({ name }) });
        await loadProjects();
      } else {
        await request(`/api/projects/${encodeURIComponent(dialog.target.id)}`, { method: 'PATCH', ...jsonBody({ name }) });
        await loadProjects();
      }
    } catch (err) { setError(err); }
    openMenu = '';
  }
  async function renameSession(id, current) {
    openNamingDialog('session', id, current);
  }
  async function deleteSession(session) {
    const id = session?.id || '';
    if (!id || (session?.execution ? session.execution.busy : session?.running)) return;
    if (!window.confirm($t('sessions.deleteConfirm'))) return;
    try { await del(`/api/sessions/${encodeURIComponent(id)}`); if ($currentSession === id) openSession(''); await refreshSessions(); } catch (err) { setError(err); }
    openMenu = '';
  }
  function createProject() {
    openNamingDialog('new-project');
  }
  function renameProject(project) {
    openNamingDialog('project', project, project.name);
  }
  async function deleteProject(project) {
    if (!window.confirm($t('projects.deleteConfirm', { name: project.name }))) return;
    try { await del(`/api/projects/${encodeURIComponent(project.id)}`); await Promise.all([loadProjects(), refreshSessions()]); } catch (err) { setError(err); }
    openMenu = '';
  }

  function openSession(id) {
    currentSession.set(id);
    navigate(id ? `/chat?session=${encodeURIComponent(id)}` : '/chat');
    closeSidebar();
  }

  function openNewChat() {
    currentSession.set('');
    const draftId = globalThis.crypto?.randomUUID
      ? globalThis.crypto.randomUUID()
      : `draft-${Date.now()}-${Math.random().toString(16).slice(2)}`;
    openDraftInWorkbench({ draftId, workDir: '' });
    navigate('/chat');
    closeSidebar();
  }

  function closeSidebar() {
    sidebarOpen.set(false);
  }

  function toggleSidebarLayout() {
    if ($isMobile) {
      closeSidebar();
      return;
    }
    if ($sidebarCollapsed) sidebarCollapsed.set(false);
    else {
      searchExpanded = false;
      closeFilterMenu();
      sidebarCollapsed.set(true);
    }
  }

  function onNavClick(item) {
    navigate(item.path);
    closeSidebar();
  }

  async function focusSearch() {
    const wasCollapsed = $sidebarCollapsed;
    if (wasCollapsed) sidebarCollapsed.set(false);
    searchExpanded = true;
    await tick();
    if (wasCollapsed) await new Promise((resolve) => setTimeout(resolve, 300));
    searchInput?.focus();
    searchInput?.select();
  }

  function handleGlobalShortcut(event) {
    const key = (event.key || '').toLowerCase();
    const mod = isMac ? event.metaKey : event.ctrlKey;
    if (!mod || key !== 'k' || event.altKey || !event.shiftKey) return;
    event.preventDefault();
    event.stopPropagation();
    openNewChat();
  }

  function handleSearchKeydown(event) {
    if (event.key !== 'Escape') return;
    event.preventDefault();
    if (searchTerm) searchTerm = '';
    else searchExpanded = false;
  }

  function showHistoryScrollbar() {
    historyScrollbarVisible = true;
    if (hideHistoryScrollbarTimer) clearTimeout(hideHistoryScrollbarTimer);
    if (historyPointerInside) return;
    hideHistoryScrollbarTimer = setTimeout(() => {
      historyScrollbarVisible = false;
      hideHistoryScrollbarTimer = null;
    }, 2000);
  }

  function enterHistoryList() {
    historyPointerInside = true;
    historyScrollbarVisible = true;
    if (hideHistoryScrollbarTimer) {
      clearTimeout(hideHistoryScrollbarTimer);
      hideHistoryScrollbarTimer = null;
    }
  }

  function leaveHistoryList() {
    historyPointerInside = false;
    showHistoryScrollbar();
  }

  function isActive(item) {
    return $route.section === item.key;
  }

  function isFeatureEnabled(item) {
    if (!item.feature) return true;
    return $features[item.feature] !== false;
  }

  function formatStat(value) {
    const n = Number(value || 0);
    if (!Number.isFinite(n)) return '0';
    if (Math.abs(n) < 10000) return new Intl.NumberFormat().format(n);
    return new Intl.NumberFormat(undefined, {
      notation: 'compact',
      maximumFractionDigits: 1
    }).format(n);
  }
</script>

{#snippet sessionRow(session, rowKey)}
  <div class="session-tree-row" class:active={$currentSession === session.id && $route.section === 'chat'}>
    <button type="button" class="session-tree-open" title={session.title || session.preview || shortID(session.id)} on:click={() => openSession(session.id)}>
      {#if session.pinned}<span class="pin" aria-label={$t('sessions.pinned')}>⌖</span>{/if}
      <span class="session-status-dot" class:running={session.execution ? session.execution.running : session.running} aria-hidden="true"></span>
      <span>{session.title || session.preview || shortID(session.id)}</span>
    </button>
    <button type="button" class="more-btn" aria-label={$t('sessions.manage')} on:click={(event) => toggleMenu(event, rowKey)}><Ellipsis size={15} aria-hidden="true" /></button>
    {#if openMenu === rowKey}
      <div class="side-menu">
        <button type="button" on:click={() => patchSession(session.id, { pinned: !session.pinned })}><span class="side-menu-label">{$t(session.pinned ? 'sessions.unpin' : 'sessions.pin')}</span></button>
        <button type="button" on:click={() => renameSession(session.id, session.title || session.preview || shortID(session.id))}><span class="side-menu-label">{$t('sessions.rename')}</span></button>
        <div class="menu-label">{$t('sessions.moveToProject')}</div>
        {#each projects as project (project.id)}
          {#if project.id !== session.projectId}<button type="button" on:click={() => patchSession(session.id, { projectId: project.id })}><span class="side-menu-label">{project.name}</span></button>{/if}
        {/each}
        <button type="button" on:click={createProject}><span class="side-menu-label">{$t('projects.new')}</span></button>
        {#if session.projectId}<button type="button" on:click={() => patchSession(session.id, { projectId: '' })}><span class="side-menu-label">{$t('sessions.removeFromProject')}</span></button>{/if}
        <button type="button" class="danger-menu" disabled={session.execution ? session.execution.busy : session.running} on:click={() => deleteSession(session)}><span class="side-menu-label">{$t('common.delete')}</span></button>
      </div>
    {/if}
  </div>
{/snippet}

{#snippet content()}
  <div class="sidebar-shell-head">
    <div class="sidebar-brand" class:hidden={compact} aria-label="MothX">
      <img class="sidebar-brand-mark" src="/mothx-small.ico" alt="" aria-hidden="true" />
      <span>MothX</span>
    </div>
    <button
      type="button"
      class="sidebar-collapse-toggle"
      aria-label={$isMobile ? $t('sidebar.menu') : (compact ? $t('sidebar.expand') : $t('sidebar.collapse'))}
      title={$isMobile ? $t('sidebar.menu') : (compact ? $t('sidebar.expand') : $t('sidebar.collapse'))}
      on:click={toggleSidebarLayout}
    >
      {#if $isMobile}<span aria-hidden="true">×</span>{:else if compact}<PanelLeftOpen size={16} aria-hidden="true" />{:else}<PanelLeftClose size={16} aria-hidden="true" />{/if}
    </button>
  </div>

  {#if !compact}
    <div class="sidebar-wide-content">
    <div class="sidebar-browser-head">
      <span class="sidebar-workspaces-title">{$t('projects.title')}</span>
      <div class="sidebar-head-actions">
        {#if searchExpanded}
          <div class="side-search side-search-inline">
            <Search class="ico" size={14} aria-hidden="true" />
            <input
              bind:this={searchInput}
              bind:value={searchTerm}
              placeholder={$t('sidebar.search')}
              aria-label={$t('sidebar.search')}
              on:keydown={handleSearchKeydown}
            />
            <button type="button" class="side-search-clear" aria-label={$t('common.cancel')} on:click={() => { searchTerm = ''; searchExpanded = false }}>×</button>
          </div>
        {:else}
          <button type="button" class="sidebar-head-icon sidebar-head-icon-search" aria-label={$t('sidebar.search')} title={$t('sidebar.search')} on:click={focusSearch}><Search size={15} aria-hidden="true" /></button>
        {/if}
        <div class="workspace-filter" class:open={filterMenuOpen}>
          <button
            bind:this={filterButton}
            type="button"
            class="sidebar-head-icon"
            class:active={filterMode !== 'all'}
            aria-label={$t('sidebar.filter')}
            aria-controls={filterMenuOpen ? 'workspace-filter-menu' : undefined}
            aria-expanded={filterMenuOpen}
            aria-haspopup="menu"
            title={$t('sidebar.filter')}
            on:click={toggleFilterMenu}
          ><ListFilter size={15} aria-hidden="true" /></button>
          {#if filterMenuOpen}
            <div id="workspace-filter-menu" class="workspace-filter-menu" role="menu" aria-label={$t('sidebar.filter')}>
              <button type="button" role="menuitemradio" aria-checked={filterMode === 'all'} class:active={filterMode === 'all'} on:click={() => setFilterMode('all')}><span>{$t('sidebar.filterAll')}</span>{#if filterMode === 'all'}<Check size={13} aria-hidden="true" />{/if}</button>
              <button type="button" role="menuitemradio" aria-checked={filterMode === 'projects'} class:active={filterMode === 'projects'} on:click={() => setFilterMode('projects')}><span>{$t('sidebar.filterProjects')}</span>{#if filterMode === 'projects'}<Check size={13} aria-hidden="true" />{/if}</button>
              <button type="button" role="menuitemradio" aria-checked={filterMode === 'unprojected'} class:active={filterMode === 'unprojected'} on:click={() => setFilterMode('unprojected')}><span>{$t('sidebar.filterUnprojected')}</span>{#if filterMode === 'unprojected'}<Check size={13} aria-hidden="true" />{/if}</button>
            </div>
          {/if}
        </div>
        <button type="button" class="sidebar-head-icon" aria-label={$t('projects.new')} title={$t('projects.new')} on:click={createProject}><FolderPlus size={15} aria-hidden="true" /></button>
      </div>
    </div>

    <button
      type="button"
      class="new-chat"
      on:click={openNewChat}
      aria-keyshortcuts={newChatAriaShortcut}
      title={`${$t('nav.newChat')} (${newChatShortcut})`}
    >
      <PenLine class="ico" size={14} aria-hidden="true" />
      <span class="label">{$t('nav.newChat')}</span>
      <kbd>{newChatShortcut}</kbd>
    </button>

    <nav class="side-nav" aria-label={$t('nav.sessions')}>
      {#each primaryNav.slice(1) as item}
        <button
          type="button"
          class="nav-item"
          class:active={isActive(item)}
          disabled={!isFeatureEnabled(item)}
          title={$t(item.label)}
          on:click={() => onNavClick(item)}
        >
          {#if item.icon === 'clock'}<Clock3 class="ico" size={15} aria-hidden="true" />{:else if item.icon === 'skills'}<Sparkles class="ico" size={15} aria-hidden="true" />{:else if item.icon === 'experts'}<UsersRound class="ico" size={15} aria-hidden="true" />{:else if item.icon === 'knowledge'}<Brain class="ico" size={15} aria-hidden="true" />{:else if item.icon === 'chart'}<ChartNoAxesColumn class="ico" size={15} aria-hidden="true" />{:else}<Timer class="ico" size={15} aria-hidden="true" />{/if}
          <span class="label">{$t(item.label)}</span>
        </button>
      {/each}

      <div class="nav-divider"></div>

      {#each secondaryNav as item}
        <button
          type="button"
          class="nav-item"
          class:active={isActive(item)}
          title={$t(item.label)}
          on:click={() => onNavClick(item)}
        >
          <Settings2 class="ico" size={15} aria-hidden="true" />
          <span class="label">{$t(item.label)}</span>
        </button>
      {/each}
    </nav>

    <section class="side-history" aria-label={$t('sidebar.history')}>
      <div class="side-history-list" role="region" aria-label={$t('sidebar.history')} class:scrolling={historyScrollbarVisible} on:pointerenter={enterHistoryList} on:pointerleave={leaveHistoryList} on:wheel={showHistoryScrollbar} on:scroll={showHistoryScrollbar}>
        <div class="session-section">
          <div class="side-history-head">
            <button type="button" class="section-toggle" aria-expanded={!collapsed.projects} on:click={() => toggle('projects')}><span class="section-chevron" class:expanded={!collapsed.projects} aria-hidden="true"><ChevronRight size={13} /></span> <span>{$t('projects.title')}</span></button>
            <button type="button" class="section-action" title={$t('projects.new')} aria-label={$t('projects.new')} on:click={createProject}><FolderPlus size={14} aria-hidden="true" /></button>
          </div>
          {#if !collapsed.projects}
            {#each projects as project (project.id)}
              <div class="project-group">
                <div class="project-row">
                  <button type="button" class="section-toggle" aria-expanded={!collapsedProjects[project.id]} on:click={() => toggleProject(project.id)}><span class="section-chevron" class:expanded={!collapsedProjects[project.id]} aria-hidden="true"><ChevronRight size={13} /></span> <Folder size={14} aria-hidden="true" /> <span>{project.name}</span></button>
                  <button type="button" class="more-btn" aria-label={$t('common.open')} on:click={(event) => toggleMenu(event, `project-${project.id}`)}><Ellipsis size={15} aria-hidden="true" /></button>
                  {#if openMenu === `project-${project.id}`}
                    <div class="side-menu project-menu">
                      <button type="button" on:click={() => renameProject(project)}><span class="side-menu-label">{$t('projects.rename')}</span></button>
                      <button type="button" class="danger-menu" on:click={() => deleteProject(project)}><span class="side-menu-label">{$t('common.delete')}</span></button>
                    </div>
                  {/if}
                </div>
                {#if !collapsedProjects[project.id]}
                  {#each projectSessions(project.id) as session (session.id)}
                    {@render sessionRow(session, `project-${project.id}-${session.id}`)}
                  {:else}
                    <p class="project-empty">{$t('projects.empty')}</p>
                  {/each}
                {/if}
              </div>
            {/each}
          {/if}
        </div>

        <div class="session-section">
          <div class="side-history-head"><button type="button" class="section-toggle" aria-expanded={!collapsed.recent} on:click={() => toggle('recent')}><span class="section-chevron" class:expanded={!collapsed.recent} aria-hidden="true"><ChevronRight size={13} /></span> <span>{$t('projects.recent')}</span></button></div>
          {#if !collapsed.recent}
            {#each recentSessions as session (session.id)}
              {@render sessionRow(session, `recent-${session.id}`)}
            {/each}
          {/if}
        </div>

        <div class="session-section">
          <div class="side-history-head"><button type="button" class="section-toggle" aria-expanded={!collapsed.unprojected} on:click={() => toggle('unprojected')}><span class="section-chevron" class:expanded={!collapsed.unprojected} aria-hidden="true"><ChevronRight size={13} /></span> <span>{$t('projects.unprojected')}</span></button></div>
          {#if !collapsed.unprojected}
            {#each unprojectedSessions as session (session.id)}
              {@render sessionRow(session, `unprojected-${session.id}`)}
            {/each}
          {/if}
        </div>
      </div>
    </section>
    </div>
  {:else}
    <div class="sidebar-rail-content" aria-label={$t('sidebar.history')}>
      <button type="button" class="rail-action rail-action-primary" aria-label={$t('nav.newChat')} title={$t('nav.newChat')} on:click={openNewChat}>✎</button>
      <button type="button" class="rail-action" aria-label={$t('sidebar.search')} title={$t('sidebar.search')} on:click={focusSearch}>⌕</button>
      <button type="button" class="rail-action" aria-label={$t('projects.new')} title={$t('projects.new')} on:click={createProject}>+</button>
      <div class="rail-divider"></div>
      {#each primaryNav.slice(1) as item}
        <button type="button" class="rail-action" class:active={isActive(item)} disabled={!isFeatureEnabled(item)} aria-label={$t(item.label)} title={$t(item.label)} on:click={() => onNavClick(item)}>{item.icon === 'clock' ? '◷' : item.icon === 'skills' ? '✦' : item.icon === 'experts' ? '◎' : item.icon === 'knowledge' ? '⧉' : item.icon === 'chart' ? '▥' : item.icon === 'timer' ? '◴' : '⚙'}</button>
      {/each}
      <div class="rail-spacer"></div>
      {#each secondaryNav as item}
        <button type="button" class="rail-action" class:active={isActive(item)} aria-label={$t(item.label)} title={$t(item.label)} on:click={() => onNavClick(item)}>{'⚙'}</button>
      {/each}
    </div>
  {/if}

  <div class="side-utility" class:side-utility-compact={compact}>
    <button type="button" class="side-stats" aria-label={$t('sidebar.stats')} title={$t('sidebar.stats')} on:click={() => onNavClick({ path: '/stats' })}>
      <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
        <path d="M4 19V5M4 19h16M7.5 16v-4M12 16V8M16.5 16v-7"></path>
      </svg>
      <span class="stat-pair">
        <span class="stat-label">{$t('sidebar.stats.requests')}</span>
        <span class="stat-value">{formatStat(summaryStats.totalRequests)}</span>
      </span>
      <span class="stat-divider" aria-hidden="true"></span>
      <span class="stat-pair">
        <span class="stat-label">{$t('sidebar.stats.tokens')}</span>
        <span class="stat-value">{formatStat(summaryStats.totalTokens)}</span>
      </span>
    </button>
    <PreferenceControls />
  </div>
{/snippet}

{#if namingDialog}
  <Modal open={true} title={namingDialog.kind === 'session' ? $t('sessions.rename') : (namingDialog.kind === 'new-project' ? $t('projects.new') : $t('projects.rename'))} className="sidebar-naming-overlay" on:close={closeNamingDialog}>
    <div class="sidebar-naming-dialog">
      <div class="sidebar-naming-header">
        <h3>{namingDialog.kind === 'session' ? $t('sessions.rename') : (namingDialog.kind === 'new-project' ? $t('projects.new') : $t('projects.rename'))}</h3>
        <button type="button" class="sidebar-naming-close" aria-label={$t('common.cancel')} on:click={closeNamingDialog}><X size={18} aria-hidden="true" /></button>
      </div>
      <label for="sidebar-naming-input">{namingDialog.kind === 'session' ? $t('sessions.nameLabel') : $t('projects.nameLabel')}</label>
      <input id="sidebar-naming-input" bind:this={namingInput} bind:value={nameDraft} maxlength="80" autocomplete="off" placeholder={namingDialog.kind === 'session' ? $t('sessions.namePlaceholder') : $t('projects.namePlaceholder')} on:keydown={(event) => event.key === 'Enter' && submitNamingDialog()} />
      <div class="sidebar-naming-actions">
        <button type="button" class="sidebar-naming-cancel" on:click={closeNamingDialog}>{$t('common.cancel')}</button>
        <button type="button" class="sidebar-naming-submit" disabled={!nameDraft.trim()} on:click={submitNamingDialog}>{$t('common.confirm')}</button>
      </div>
    </div>
  </Modal>
{/if}

{#if $isMobile}
  {#if $sidebarOpen}
    <button
      type="button"
      class="sidebar-overlay"
      aria-label={$t('common.close')}
      transition:fade={{ duration: 200 }}
      on:click={closeSidebar}
    ></button>
    <aside class="sidebar mobile-drawer" transition:fly={{ x: -300, duration: 250 }}>
      {@render content()}
    </aside>
  {/if}
{:else}
  <aside class="sidebar" class:sidebar-collapsed={compact} class:sidebar-collapsing={sidebarCollapsing}>
    {@render content()}
  </aside>
{/if}
