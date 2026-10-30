<script>
  import { onMount, tick } from 'svelte';
  import { t } from '../../lib/preferences.js';
  import { sessions } from '../../lib/stores.js';
  import { shortID } from '../../lib/format.js';
  import { sessionRunStates, isCompletionActive, isSessionRuntimeBusy } from '../../lib/session-runs.js';

  export let groupId = '';
  export let tabs = [];
  export let selectedTabId = '';
  export let onSelect = () => {};
  export let onClose = () => {};
  export let onCloseOthers = () => {};
  export let onCloseRight = () => {};
  export let onDragStartTab = () => {};
  export let groupOfTab = null;

  let menu = null;
  let menuEl;
  let stripEl;
  let overflowBtnEl;
  let overflow = false;
  let overflowOpen = false;

  // A horizontal scroller hides tabs with no discoverable affordance on
  // trackpads. When the strip overflows, expose the same tab list through an
  // explicit menu so every open session stays reachable.
  function measure() {
    if (!stripEl) return;
    const next = stripEl.scrollWidth > stripEl.clientWidth + 1;
    if (next !== overflow) overflow = next;
    if (!overflow) overflowOpen = false;
  }

  onMount(() => {
    const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(measure);
    if (observer && stripEl) observer.observe(stripEl);
    measure();
    return () => observer?.disconnect();
  });

  $: tabs, tick().then(measure);

  function openMenu(event, tab) {
    event.preventDefault();
    const tabGroupId = typeof groupOfTab === 'function' ? (groupOfTab(tab.id) || groupId) : groupId;
    menu = { tabId: tab.id, groupId: tabGroupId, x: event.clientX, y: event.clientY };
  }

  function closeMenu() {
    menu = null;
  }

  function toggleOverflow() {
    overflowOpen = !overflowOpen;
  }

  function selectFromOverflow(tabId) {
    onSelect(groupId, tabId);
    overflowOpen = false;
  }

  function windowPointer(event) {
    if (menu && menuEl && !menuEl.contains(event.target)) closeMenu();
    if (overflowOpen && !event.target.closest?.('.wb-tab-overflow-wrap')) overflowOpen = false;
  }

  function escape(event) {
    if (event.key === 'Escape') {
      closeMenu();
      overflowOpen = false;
    }
  }

  function label(tab) {
    if (tab.kind === 'draft') return tab.workDir || $t('workbench.draft') || 'New task';
    const session = ($sessions || []).find((item) => item?.id === tab.sessionId);
    // Untitled sessions often mirror their ID; prefer the user's first message
    // so tabs and the header read as a conversation instead of a hex handle.
    const name = session?.title && session.title !== tab.sessionId ? session.title : session?.preview;
    return name || shortID(tab.sessionId) || $t('workbench.session') || 'Session';
  }

  // Read the store directly in markup so each tab's badge reacts to that
  // session's canonical state instead of only re-rendering when props change.
  function pendingOf(state) {
    return (state?.runtime?.pendingApprovals || []).length + (state?.runtime?.pendingQuestions || []).length;
  }

  function runningOf(state) {
    return isCompletionActive(state) || isSessionRuntimeBusy(state?.runtime);
  }

  function dragStart(event, tab) {
    try {
      event.dataTransfer.setData('text/plain', tab.id);
      event.dataTransfer.effectAllowed = 'move';
    } catch {
      // Drag data is best effort; the callback still carries the tab id.
    }
    onDragStartTab(tab.id);
  }
</script>

<div class="wb-tabbar">
  <div class="wb-tabstrip" bind:this={stripEl} role="tablist" aria-label={$t('workbench.tabs.label') || 'Workspace tabs'}>
    {#each tabs as tab (tab.id)}
      {@const tabState = tab.kind === 'session' ? ($sessionRunStates?.[tab.sessionId] || null) : null}
      {@const pendingCount = pendingOf(tabState)}
      <div
        class="wb-tab"
        class:wb-tab-active={tab.id === selectedTabId}
        role="tab"
        aria-selected={tab.id === selectedTabId}
        tabindex={tab.id === selectedTabId ? 0 : -1}
        draggable="true"
        ondragstart={(event) => dragStart(event, tab)}
        onkeydown={(event) => {
          if (event.key === 'Enter' || event.key === ' ') {
            event.preventDefault();
            onSelect(groupId, tab.id);
          }
        }}
        oncontextmenu={(event) => openMenu(event, tab)}
      >
        <button type="button" class="wb-tab-label" onclick={() => onSelect(groupId, tab.id)} title={label(tab)}>
          {#if runningOf(tabState)}<span class="wb-tab-running" aria-hidden="true"></span>{/if}
          <span class="wb-tab-text">{label(tab)}</span>
          {#if pendingCount > 0}<span class="wb-tab-pending">{pendingCount}</span>{/if}
        </button>
        <button type="button" class="wb-tab-close" aria-label={$t('workbench.tab.close') || 'Close tab'} onclick={() => onClose(tab.id)}>
          <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18"/></svg>
        </button>
      </div>
    {/each}
  </div>
  {#if overflow}
    <div class="wb-tab-overflow-wrap">
      <button
        type="button"
        class="wb-tab-overflow"
        class:active={overflowOpen}
        bind:this={overflowBtnEl}
        aria-haspopup="menu"
        aria-expanded={overflowOpen}
        title={$t('workbench.tab.overflow') || 'All tabs'}
        onclick={toggleOverflow}
      >
        <span aria-hidden="true">…</span>
        <span class="wb-tab-overflow-count">{tabs.length}</span>
      </button>
      {#if overflowOpen}
        <div class="wb-tab-menu wb-tab-overflow-menu" role="menu">
          {#each tabs as tab (tab.id)}
            {@const tabState = tab.kind === 'session' ? ($sessionRunStates?.[tab.sessionId] || null) : null}
            {@const pendingCount = pendingOf(tabState)}
            <button
              type="button"
              role="menuitem"
              class:wb-tab-overflow-active={tab.id === selectedTabId}
              onclick={() => selectFromOverflow(tab.id)}
            >
              {#if runningOf(tabState)}<span class="wb-tab-running" aria-hidden="true"></span>{/if}
              <span class="wb-tab-overflow-label">{label(tab)}</span>
              {#if pendingCount > 0}<span class="wb-tab-pending">{pendingCount}</span>{/if}
            </button>
          {/each}
        </div>
      {/if}
    </div>
  {/if}
</div>

{#if menu}
  <div class="wb-tab-menu" bind:this={menuEl} style={`left:${menu.x}px; top:${menu.y}px`} role="menu">
    <button type="button" role="menuitem" onclick={() => { onClose(menu.tabId); closeMenu(); }}>{$t('workbench.tab.close') || 'Close'}</button>
    <button type="button" role="menuitem" onclick={() => { onCloseOthers(menu.groupId, menu.tabId); closeMenu(); }}>{$t('workbench.tab.closeOthers') || 'Close others'}</button>
    <button type="button" role="menuitem" onclick={() => { onCloseRight(menu.groupId, menu.tabId); closeMenu(); }}>{$t('workbench.tab.closeRight') || 'Close to the right'}</button>
  </div>
{/if}

<svelte:window onpointerdown={windowPointer} onkeydown={escape} />

<style>
  .wb-tabbar {
    display: flex;
    align-items: center;
    gap: 2px;
    min-width: 0;
    padding: 0 6px;
    background: var(--moth-surface-secondary);
    border-bottom: 1px solid var(--moth-border-subtle);
  }
  .wb-tabstrip {
    display: flex;
    align-items: center;
    gap: 2px;
    min-height: 30px;
    min-width: 0;
    flex: 1;
    padding: 0 2px;
    overflow-x: auto;
    overflow-y: hidden;
    scrollbar-width: none;
    overscroll-behavior-x: contain;
  }
  .wb-tabstrip::-webkit-scrollbar { display: none; }
  .wb-tab {
    position: relative;
    display: flex;
    align-items: center;
    min-width: 0;
    max-width: 220px;
    height: 26px;
    border-radius: 6px;
    background: transparent;
    color: var(--moth-text-secondary);
    flex: 0 0 auto;
    transition: background-color var(--moth-motion-fast) ease, color var(--moth-motion-fast) ease, box-shadow var(--moth-motion-fast) ease;
  }
  .wb-tab:hover { background: var(--moth-surface-hover); color: var(--moth-text-primary); }
  .wb-tab-active, .wb-tab-active:hover {
    background: var(--moth-surface-page);
    color: var(--moth-text-primary);
    box-shadow: inset 0 0 0 1px var(--moth-border-subtle), var(--shadow-sm);
  }
  .wb-tab:focus-visible { outline: 2px solid var(--moth-selection-indicator); outline-offset: -2px; }
  .wb-tab-label {
    display: flex;
    align-items: center;
    gap: 6px;
    flex: 1;
    min-width: 0;
    height: 100%;
    min-height: 0;
    overflow: hidden;
    border: 0;
    background: transparent;
    color: inherit;
    padding: 0 2px 0 9px;
    cursor: pointer;
    font: inherit;
  }
  .wb-tab-text {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 12px;
    font-weight: 500;
    letter-spacing: .01em;
  }
  .wb-tab-active .wb-tab-text { font-weight: 600; }
  .wb-tab-running {
    width: 6px;
    height: 6px;
    border-radius: 999px;
    background: var(--moth-state-running);
    box-shadow: 0 0 0 3px color-mix(in srgb, var(--moth-state-running) 16%, transparent);
    flex: 0 0 auto;
    animation: wb-tab-pulse 2.4s ease-in-out infinite;
  }
  @keyframes wb-tab-pulse { 0%, 100% { opacity: 1; } 50% { opacity: .45; } }
  .wb-tab-pending {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 16px;
    height: 16px;
    border-radius: 999px;
    padding: 0 5px;
    background: var(--warning-bg);
    color: var(--warning);
    border: 1px solid var(--warning-border);
    font-size: 10px;
    font-weight: 600;
    line-height: 1;
    font-variant-numeric: tabular-nums;
    flex: 0 0 auto;
  }
  .wb-tab-close {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex: 0 0 auto;
    width: 18px;
    height: 18px;
    min-height: 0;
    padding: 0;
    margin-right: 2px;
    border: 0;
    border-radius: 5px;
    background: transparent;
    color: var(--moth-text-muted);
    opacity: .45;
    cursor: pointer;
    transition: opacity var(--moth-motion-fast) ease, background-color var(--moth-motion-fast) ease, color var(--moth-motion-fast) ease;
  }
  .wb-tab:hover .wb-tab-close, .wb-tab-active .wb-tab-close, .wb-tab:focus-within .wb-tab-close { opacity: .8; }
  .wb-tab-close:hover { opacity: 1; background: var(--moth-surface-active); color: var(--moth-text-primary); }
  .wb-tab-close:focus-visible { outline: 2px solid var(--moth-selection-indicator); outline-offset: -2px; opacity: 1; }
  .wb-tab-overflow-wrap { position: relative; display: flex; align-items: center; flex: 0 0 auto; margin-left: 2px; padding-right: 2px; }
  .wb-tab-overflow {
    display: flex;
    align-items: center;
    gap: 5px;
    height: 26px;
    min-height: 0;
    border: 0;
    border-radius: 7px;
    background: transparent;
    color: var(--moth-text-secondary);
    padding: 0 8px;
    cursor: pointer;
    font: inherit;
    font-size: 12px;
    transition: background-color var(--moth-motion-fast) ease, color var(--moth-motion-fast) ease;
  }
  .wb-tab-overflow:hover { background: var(--moth-surface-hover); color: var(--moth-text-primary); }
  .wb-tab-overflow.active { background: var(--moth-surface-active); color: var(--moth-text-primary); }
  .wb-tab-overflow-count {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 16px;
    height: 16px;
    border-radius: 999px;
    padding: 0 5px;
    background: var(--moth-surface-active);
    color: var(--moth-text-secondary);
    font-size: 10px;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
  }
  .wb-tab-menu {
    position: fixed;
    z-index: 50;
    display: flex;
    flex-direction: column;
    min-width: 168px;
    padding: 5px;
    border-radius: 10px;
    border: 1px solid var(--moth-border-strong);
    background: var(--moth-surface-page);
    color: var(--moth-text-primary);
    box-shadow: var(--shadow-md);
  }
  .wb-tab-menu button {
    display: flex;
    align-items: center;
    gap: 8px;
    min-height: 0;
    text-align: left;
    border: 0;
    border-radius: 6px;
    background: transparent;
    color: inherit;
    padding: 7px 9px;
    cursor: pointer;
    font: inherit;
    font-size: 12px;
  }
  .wb-tab-menu button:hover { background: var(--moth-surface-hover); }
  .wb-tab-overflow-menu { position: absolute; top: calc(100% + 6px); right: 0; left: auto; max-height: 60vh; overflow-y: auto; }
  .wb-tab-overflow-menu button { min-height: 30px; }
  .wb-tab-overflow-label { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .wb-tab-overflow-menu .wb-tab-overflow-active { background: var(--moth-surface-active); color: var(--moth-text-primary); }

  @media (prefers-reduced-motion: reduce) {
    .wb-tab, .wb-tab-close, .wb-tab-overflow { transition: none; }
    .wb-tab-running { animation: none; }
  }
</style>
