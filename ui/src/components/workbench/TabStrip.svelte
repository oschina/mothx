<script>
  import { onMount, tick } from 'svelte';
  import { t } from '../../lib/preferences.js';
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
    return tab.sessionId || $t('workbench.session') || 'Session';
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
        <button type="button" class="wb-tab-close" aria-label={$t('workbench.tab.close') || 'Close tab'} onclick={() => onClose(tab.id)}>×</button>
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
  .wb-tabbar { display: flex; align-items: stretch; min-width: 0; border-bottom: 1px solid rgba(120,120,120,.2); }
  .wb-tabstrip { display: flex; gap: 2px; min-height: 32px; min-width: 0; flex: 1; overflow-x: auto; scrollbar-width: thin; }
  .wb-tab { display: flex; align-items: center; min-width: 0; max-width: 240px; border-radius: 5px 5px 0 0; background: rgba(120,120,120,.08); flex: 0 0 auto; }
  .wb-tab-active { background: rgba(120,120,120,.22); }
  .wb-tab-label { display: flex; align-items: center; gap: 5px; min-width: 0; overflow: hidden; border: 0; background: transparent; color: inherit; padding: 6px 8px; cursor: pointer; }
  .wb-tab-text { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .wb-tab-running { width: 7px; height: 7px; border-radius: 999px; background: #4ade80; flex: 0 0 auto; }
  .wb-tab-pending { border-radius: 999px; padding: 0 5px; background: #b45309; color: #fff; font-size: 10px; }
  .wb-tab-close { border: 0; background: transparent; color: inherit; opacity: .55; padding: 2px 6px; cursor: pointer; }
  .wb-tab-close:hover { opacity: 1; }
  .wb-tab-overflow-wrap { position: relative; display: flex; align-items: stretch; flex: 0 0 auto; }
  .wb-tab-overflow { display: flex; align-items: center; gap: 3px; border: 0; border-left: 1px solid rgba(120,120,120,.25); background: transparent; color: inherit; padding: 0 8px; cursor: pointer; font-size: 13px; }
  .wb-tab-overflow:hover, .wb-tab-overflow.active { background: rgba(120,120,120,.16); }
  .wb-tab-overflow-count { font-size: 10px; opacity: .7; }
  .wb-tab-menu { position: fixed; z-index: 50; display: flex; flex-direction: column; min-width: 150px; padding: 4px; border-radius: 7px; border: 1px solid rgba(120,120,120,.35); background: #1b1d23; color: #e6e8ee; box-shadow: 0 8px 24px rgba(0,0,0,.35); }
  .wb-tab-menu button { display: flex; align-items: center; gap: 6px; text-align: left; border: 0; border-radius: 4px; background: transparent; color: inherit; padding: 6px 8px; cursor: pointer; font-size: 12px; }
  .wb-tab-menu button:hover { background: rgba(120,120,140,.25); }
  .wb-tab-overflow-menu { position: absolute; top: 100%; right: 0; left: auto; max-height: 60vh; overflow-y: auto; }
  .wb-tab-overflow-menu .wb-tab-overflow-label { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .wb-tab-overflow-menu .wb-tab-overflow-active { background: rgba(120,120,140,.2); }
</style>
