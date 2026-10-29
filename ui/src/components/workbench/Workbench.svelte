<script>
  import SplitView from './SplitView.svelte';
  import Pane from './Pane.svelte';
  import TabStrip from './TabStrip.svelte';
  import EmptyPane from './EmptyPane.svelte';
  import TerminalPane from '../terminal/TerminalPane.svelte';
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

  $: focusedGroupId = state?.attention?.focusedGroupId || '';
  $: leaves = state ? collectLeaves(state.frame?.center) : [];
  $: allTabs = state ? collectTabs(state) : [];
  $: focusedView = state?.view?.groups?.[focusedGroupId] || { tabs: [] };
  $: focusedTab = (focusedView.tabs || []).find((tab) => tab.id === focusedView.selectedTabId) || null;
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

  function selectFromTabStrip(groupId, tabId) {
    onSelectTab(groupId, tabId);
  }
</script>

<div class="wb-root">
  <header class="wb-toolbar">
    <button type="button" class="wb-tool" onclick={onNewTask}>{$t('workbench.newTask') || 'New task'}</button>
    <span class="wb-tool-divider"></span>
    {#each BUILTIN_PRESETS as preset (preset.id)}
      <button type="button" class="wb-tool wb-preset" onclick={() => onPreset(preset.id)}>{presetLabel(preset)}</button>
    {/each}
    <span class="wb-tool-divider"></span>
    <button type="button" class="wb-tool" disabled={leaves.length >= 4} onclick={() => onSplit('horizontal')} title={leaves.length >= 4 ? $t('workbench.limit.pane', { limit: 4 }) : ($t('workbench.splitColumn') || 'Split right')}>
      {$t('workbench.splitColumn') || 'Split right'}
    </button>
    <button type="button" class="wb-tool" disabled={leaves.length >= 4} onclick={() => onSplit('vertical')} title={leaves.length >= 4 ? $t('workbench.limit.pane', { limit: 4 }) : ($t('workbench.splitRow') || 'Split down')}>
      {$t('workbench.splitRow') || 'Split down'}
    </button>
    <button type="button" class="wb-tool" disabled={leaves.length <= 1} onclick={() => onRemoveGroup(focusedGroupId)}>{$t('workbench.closePane') || 'Close pane'}</button>
    <button type="button" class="wb-tool" onclick={onReset}>{$t('workbench.reset') || 'Reset layout'}</button>
    <span class="wb-tool-spacer"></span>
    {#if terminalAvailable}
      <button type="button" class="wb-tool" class:active={terminalOpen} onclick={onToggleTerminal} disabled={!terminalSessionId}>
        {$t('workbench.terminal') || 'Terminal'}
      </button>
    {/if}
    {#if pendingCount > 0}
      <button type="button" class="wb-tool wb-attention" onclick={onAttention}>{$t('workbench.attention') || 'Pending'}: {pendingCount}</button>
    {/if}
  </header>

  <div class="wb-main" class:wb-terminal-open={terminalOpen && terminalSessionId}>
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

    {#if terminalOpen && terminalSessionId}
      <div class="wb-terminal-dock">
        <TerminalPane sessionId={terminalSessionId} active={true} />
      </div>
    {/if}
  </div>
</div>

<style>
  .wb-root { display: flex; flex-direction: column; width: 100%; height: 100%; min-width: 0; min-height: 0; }
  .wb-toolbar { display: flex; align-items: center; gap: 4px; padding: 4px 8px; border-bottom: 1px solid rgba(120,120,120,.2); min-height: 34px; overflow-x: auto; }
  .wb-tool { border: 1px solid transparent; background: transparent; color: inherit; border-radius: 5px; padding: 4px 8px; cursor: pointer; font-size: 12px; white-space: nowrap; }
  .wb-tool:hover:not(:disabled) { background: rgba(120,120,120,.14); }
  .wb-tool:disabled { opacity: .4; cursor: default; }
  .wb-tool.active { background: rgba(80,140,255,.2); border-color: rgba(80,140,255,.4); }
  .wb-tool-divider { width: 1px; height: 18px; background: rgba(120,120,120,.25); margin: 0 4px; }
  .wb-tool-spacer { flex: 1; }
  .wb-attention { background: rgba(180,83,9,.2); border-color: rgba(180,83,9,.5); }
  .wb-main { position: relative; flex: 1; min-height: 0; min-width: 0; display: flex; flex-direction: column; }
  .wb-single { display: flex; flex-direction: column; flex: 1; min-height: 0; }
  .wb-single-body { flex: 1; min-height: 0; overflow: hidden; }
  .wb-terminal-dock { height: 40%; min-height: 160px; max-height: 60%; border-top: 1px solid rgba(120,120,120,.3); }
</style>
