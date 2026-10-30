<script>
  import SplitView from './SplitView.svelte';
  import Pane from './Pane.svelte';
  import TabStrip from './TabStrip.svelte';
  import EmptyPane from './EmptyPane.svelte';
  import TerminalPane from '../terminal/TerminalPane.svelte';
  import PlanPanel from './PlanPanel.svelte';
  import { BUILTIN_PRESETS } from '../../lib/workbench/presets.js';
  import { collectLeaves, collectTabs, findTab } from '../../lib/workbench/model.js';
  import { getSessionState, isCompletionActive, isSessionRuntimeBusy } from '../../lib/session-runs.js';
  import { sessionRunStates } from '../../lib/session-runs.js';
  import { t } from '../../lib/preferences.js';

  export let state;
  export let mobile = false;
  export let width = 0;
  export let terminalOpen = false;
  export let terminalSessionId = '';
  export let terminalAvailable = false;
  export let onResize = () => {};
  export let onSelectTab = () => {};
  export let onCloseTab = () => {};
  export let onCloseOthers = () => {};
  export let onCloseRight = () => {};
  export let onFocusGroup = () => {};
  export let onNewTask = () => {};
  export let onSplit = () => {};
  export let onRemoveGroup = () => {};
  export let onDropTab = (tabId, groupId, side, index) => {};
  export let onDragStartTab = () => {};
  export let onPreset = () => {};
  export let onReset = () => {};
  export let onToggleTerminal = () => {};
  export let onAttention = () => {};
  export let pane;
  let planOpen = false;

  $: focusedGroupId = state?.attention?.focusedGroupId || '';
  $: leaves = state ? collectLeaves(state.frame?.center) : [];
  $: allTabs = state ? collectTabs(state) : [];
  $: focusedView = state?.view?.groups?.[focusedGroupId] || { tabs: [] };
  $: focusedTab = (focusedView.tabs || []).find((tab) => tab.id === focusedView.selectedTabId) || null;
  $: focusedSessionId = focusedTab?.kind === 'session' ? focusedTab.sessionId : '';
  $: if (!focusedSessionId) planOpen = false;
  $: splitPossible = !mobile && leaves.length > 1 && (width <= 0 || width / leaves.length >= 380);
  $: pendingCount = countPending($sessionRunStates, state);
  $: runningCount = countRunning($sessionRunStates, state);

  function countPending(states, value) {
    let total = 0;
    for (const tab of collectTabs(value || { frame: { center: { kind: 'group', id: '' } }, view: { groups: {} } })) {
      if (tab.kind !== 'session') continue;
      const runtime = states?.[tab.sessionId]?.runtime;
      total += (runtime?.pendingApprovals || []).length + (runtime?.pendingQuestions || []).length;
    }
    return total;
  }

  function countRunning(states, value) {
    let total = 0;
    for (const tab of collectTabs(value || { frame: { center: { kind: 'group', id: '' } }, view: { groups: {} } })) {
      if (tab.kind !== 'session') continue;
      const sessionState = states?.[tab.sessionId];
      if (isCompletionActive(sessionState) || isSessionRuntimeBusy(sessionState?.runtime)) total += 1;
    }
    return total;
  }

  function presetLabel(preset) {
    return $t(preset.name) || preset.id;
  }

  function presetActive(id) {
    const center = state?.frame?.center;
    if (!center) return false;
    if (id === 'single') return center.kind === 'group';
    if (center.kind !== 'split') return false;
    const children = center.children || [];
    if (id === 'two-column') return center.direction === 'horizontal' && children.length === 2 && children.every((child) => child.kind === 'group');
    if (id === 'two-row') return center.direction === 'vertical' && children.length === 2 && children.every((child) => child.kind === 'group');
    if (id === 'quad') {
      return center.direction === 'horizontal'
        && children.length === 2
        && children.every((child) => child.kind === 'split' && (child.children || []).every((leaf) => leaf.kind === 'group'));
    }
    return false;
  }

  function selectFromTabStrip(groupId, tabId) {
    onSelectTab(groupId, tabId);
  }
</script>

<div class="wb-root">
  <header class="wb-toolbar">
    <button type="button" class="wb-tool wb-tool-new" data-action="new-task" onclick={onNewTask} title={$t('workbench.newTask') || 'New task'} aria-label={$t('workbench.newTask') || 'New task'}>
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><path d="M12 5v14M5 12h14"/></svg>
    </button>
    <span class="wb-tool-divider"></span>
    <div class="wb-preset-group" role="group" aria-label={$t('workbench.layout') || 'Layout'}>
      {#each BUILTIN_PRESETS as preset (preset.id)}
        <button
          type="button"
          class="wb-tool wb-preset"
          class:active={presetActive(preset.id)}
          data-action={`preset-${preset.id}`}
          aria-pressed={presetActive(preset.id)}
          onclick={() => onPreset(preset.id)}
          title={presetLabel(preset)}
          aria-label={presetLabel(preset)}
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <rect x="3.5" y="4.5" width="17" height="15" rx="2"/>
            {#if preset.id === 'two-column' || preset.id === 'quad'}<path d="M12 4.5v15"/>{/if}
            {#if preset.id === 'two-row' || preset.id === 'quad'}<path d="M3.5 12h17"/>{/if}
          </svg>
        </button>
      {/each}
    </div>
    <span class="wb-tool-divider"></span>
    <button type="button" class="wb-tool" data-action="split-horizontal" disabled={leaves.length >= 4} onclick={() => onSplit('horizontal')} title={leaves.length >= 4 ? $t('workbench.limit.pane', { limit: 4 }) : ($t('workbench.splitColumn') || 'Split right')} aria-label={$t('workbench.splitColumn') || 'Split right'}>
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3.5" y="4.5" width="17" height="15" rx="2"/><path d="M15.5 4.5v15"/><path d="M6.5 12h5m-1.8-1.8L11.5 12l-1.8 1.8"/></svg>
    </button>
    <button type="button" class="wb-tool" data-action="split-vertical" disabled={leaves.length >= 4} onclick={() => onSplit('vertical')} title={leaves.length >= 4 ? $t('workbench.limit.pane', { limit: 4 }) : ($t('workbench.splitRow') || 'Split down')} aria-label={$t('workbench.splitRow') || 'Split down'}>
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3.5" y="4.5" width="17" height="15" rx="2"/><path d="M3.5 15.5h17"/><path d="M12 6.5v5m-1.8-1.8L12 11.5l1.8-1.8"/></svg>
    </button>
    <button type="button" class="wb-tool" data-action="close-pane" disabled={leaves.length <= 1} onclick={() => onRemoveGroup(focusedGroupId)} title={$t('workbench.closePane') || 'Close pane'} aria-label={$t('workbench.closePane') || 'Close pane'}>
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3.5" y="4.5" width="17" height="15" rx="2"/><path d="m9.5 9.5 5 5m0-5-5 5"/></svg>
    </button>
    <button type="button" class="wb-tool" data-action="reset" onclick={onReset} title={$t('workbench.reset') || 'Reset layout'} aria-label={$t('workbench.reset') || 'Reset layout'}>
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 4v5h5"/><path d="M5.3 9a8 8 0 1 1 .7 5"/></svg>
    </button>
    <span class="wb-tool-spacer"></span>
    {#if focusedSessionId}
      <button type="button" class="wb-tool" class:active={planOpen} data-action="plan" onclick={() => (planOpen = !planOpen)} title={$t('workbench.plan') || 'Plan'} aria-label={$t('workbench.plan') || 'Plan'}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 6h11M9 12h11M9 18h11"/><path d="m3 6 1.5 1.5L7 5M3 12l1.5 1.5L7 11M3 18l1.5 1.5L7 17"/></svg>
      </button>
    {/if}
    {#if terminalAvailable}
      <button type="button" class="wb-tool" class:active={terminalOpen} data-action="terminal" onclick={onToggleTerminal} disabled={!terminalSessionId} title={$t('workbench.terminal') || 'Terminal'} aria-label={$t('workbench.terminal') || 'Terminal'}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="4" width="18" height="16" rx="2"/><path d="m7 9 3 3-3 3"/><path d="M13 15h4"/></svg>
      </button>
    {/if}
    {#if pendingCount > 0}
      <button type="button" class="wb-tool wb-attention" data-action="attention" data-count={pendingCount} onclick={onAttention} title={`${$t('workbench.attention') || 'Pending'}: ${pendingCount}`} aria-label={`${$t('workbench.attention') || 'Pending'}: ${pendingCount}`}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M18 8a6 6 0 0 0-12 0c0 7-3 8-3 8h18s-3-1-3-8"/><path d="M10.3 21a2 2 0 0 0 3.4 0"/></svg>
        <span class="wb-attention-count">{pendingCount}</span>
      </button>
    {/if}
  </header>

  <div class="wb-main" class:wb-terminal-open={terminalOpen && terminalSessionId}>
    <div class="wb-body">
      <div class="wb-panes">
      {#if mobile || !splitPossible}
        <section class="wb-single">
          <TabStrip
            groupId={focusedGroupId}
            tabs={allTabs}
            selectedTabId={focusedTab?.id || ''}
            groupOfTab={(tabId) => findTab(state, tabId)?.groupId || focusedGroupId}
            onSelect={(groupId, tabId) => onSelectTab(findTab(state, tabId)?.groupId || groupId, tabId)}
            onClose={onCloseTab}
            {onCloseOthers}
            {onCloseRight}
            {onDragStartTab}
          />
          <div class="wb-single-body">
            {#if focusedTab}
              {@render pane(focusedGroupId, focusedTab)}
            {:else}
              <EmptyPane onNewTask={onNewTask} />
            {/if}
        </div>
      </section>
    {:else}
      {#snippet layoutPane(group)}
        <Pane
          {group}
          view={state?.view?.groups?.[group?.id] || { tabs: [] }}
          active={group?.id === focusedGroupId}
          {onSelectTab}
          {onCloseTab}
          {onCloseOthers}
          {onCloseRight}
          {onNewTask}
          {onFocusGroup}
          {onDragStartTab}
          {onDropTab}
          {pane}
        />
      {/snippet}
      <SplitView node={state?.frame?.center} {onResize} pane={layoutPane} />
    {/if}
      </div>
      {#if planOpen && focusedSessionId}
        <PlanPanel sessionId={focusedSessionId} {mobile} onClose={() => (planOpen = false)} />
      {/if}
    </div>

    {#if terminalOpen && terminalSessionId}
      <div class="wb-terminal-dock">
        <TerminalPane sessionId={terminalSessionId} active={true} />
      </div>
    {/if}
  </div>
</div>

<style>
  .wb-root { display: flex; flex-direction: column; width: 100%; height: 100%; min-width: 0; min-height: 0; }
  .wb-toolbar { display: flex; align-items: center; gap: 1px; padding: 2px 6px; min-height: 28px; border-bottom: 1px solid var(--moth-border-subtle); background: var(--moth-surface-page); overflow-x: auto; scrollbar-width: none; }
  .wb-toolbar::-webkit-scrollbar { display: none; }
  .wb-tool { display: inline-flex; align-items: center; justify-content: center; width: 24px; height: 24px; min-height: 0; padding: 0; border: 1px solid transparent; border-radius: 6px; background: transparent; color: var(--moth-text-secondary); cursor: pointer; flex: 0 0 auto; transition: background-color var(--moth-motion-fast) ease, color var(--moth-motion-fast) ease; }
  .wb-tool svg { width: 14px; height: 14px; }
  .wb-tool:hover:not(:disabled) { background: var(--moth-surface-hover); color: var(--moth-text-primary); }
  .wb-tool:disabled { opacity: .35; cursor: default; }
  .wb-tool.active { background: var(--moth-surface-active); color: var(--moth-text-primary); box-shadow: inset 0 0 0 1px var(--moth-border-subtle); }
  .wb-tool:focus-visible { outline: 2px solid var(--moth-selection-indicator); outline-offset: -2px; }
  .wb-tool-new { color: var(--moth-text-primary); }
  .wb-preset-group { display: inline-flex; align-items: center; gap: 1px; padding: 1px; border-radius: 7px; background: var(--moth-surface-secondary); flex: 0 0 auto; }
  .wb-preset { width: 22px; height: 22px; border-radius: 5px; }
  .wb-preset.active { background: var(--moth-surface-page); color: var(--moth-text-primary); box-shadow: var(--shadow-sm), inset 0 0 0 1px var(--moth-border-subtle); }
  .wb-tool-divider { width: 1px; height: 14px; background: var(--moth-border-subtle); margin: 0 3px; flex: 0 0 auto; }
  .wb-tool-spacer { flex: 1; }
  .wb-attention { position: relative; }
  .wb-attention-count { position: absolute; top: -3px; right: -3px; display: inline-flex; align-items: center; justify-content: center; min-width: 13px; height: 13px; padding: 0 3px; border-radius: 999px; background: var(--warning-bg); color: var(--warning); border: 1px solid var(--warning-border); font-size: 9px; font-weight: 600; line-height: 1; font-variant-numeric: tabular-nums; }
  .wb-main { position: relative; flex: 1; min-height: 0; min-width: 0; display: flex; flex-direction: column; }
  .wb-body { position: relative; display: flex; flex: 1; min-width: 0; min-height: 0; }
  .wb-panes { display: flex; flex-direction: column; flex: 1; min-width: 0; min-height: 0; }
  .wb-single { display: flex; flex-direction: column; flex: 1; min-height: 0; }
  .wb-single-body { flex: 1; min-height: 0; overflow: hidden; display: flex; flex-direction: column; }
  .wb-terminal-dock { height: 40%; min-height: 160px; max-height: 60%; border-top: 1px solid var(--moth-border-subtle); }

  @media (prefers-reduced-motion: reduce) {
    .wb-tool { transition: none; }
  }
</style>
