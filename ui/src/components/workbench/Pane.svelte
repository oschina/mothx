<script>
  import TabStrip from './TabStrip.svelte';
  import EmptyPane from './EmptyPane.svelte';

  export let group;
  export let view = { tabs: [] };
  export let active = false;
  export let onSelectTab = () => {};
  export let onCloseTab = () => {};
  export let onCloseOthers = () => {};
  export let onCloseRight = () => {};
  export let onNewTask = () => {};
  export let onFocusGroup = () => {};
  export let onDragStartTab = () => {};
  export let onDropTab = () => {};
  export let pane;

  let dragover = false;
  let dropSide = '';

  $: selectedTab = (view?.tabs || []).find((tab) => tab.id === view?.selectedTabId);

  function sideFor(event, element) {
    const rect = element?.getBoundingClientRect?.();
    if (!rect || !rect.width || !rect.height) return 'middle';
    const x = (event.clientX - rect.left) / rect.width;
    const y = (event.clientY - rect.top) / rect.height;
    const edge = 0.25;
    if (x < edge) return 'left';
    if (x > 1 - edge) return 'right';
    if (y < edge) return 'top';
    if (y > 1 - edge) return 'bottom';
    return 'middle';
  }

  function overTabStrip(event, element) {
    const strip = element?.querySelector?.('.wb-tabstrip');
    const rect = strip?.getBoundingClientRect?.();
    if (!rect) return false;
    return event.clientY >= rect.top && event.clientY <= rect.bottom;
  }

  function dropSideFor(event, element) {
    // The tab strip occupies the pane's top-left corner: without this guard a
    // leftward tab reorder would always be read as a left/top edge split.
    return overTabStrip(event, element) ? 'middle' : sideFor(event, element);
  }

  function dragOver(event) {
    if (!event.dataTransfer) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = 'move';
    dragover = true;
    dropSide = dropSideFor(event, event.currentTarget);
  }

  function dragLeave() {
    dragover = false;
    dropSide = '';
  }

  function drop(event) {
    event.preventDefault();
    const tabId = event.dataTransfer?.getData('text/plain') || '';
    const side = dropSide || dropSideFor(event, event.currentTarget);
    dragover = false;
    if (tabId) onDropTab(tabId, group?.id, side, dropIndex(event));
    dropSide = '';
  }

  function dropIndex(event) {
    const elements = [...event.currentTarget.querySelectorAll('.wb-tab')];
    let index = 0;
    for (const element of elements) {
      const rect = element.getBoundingClientRect();
      if (event.clientX <= rect.left + rect.width / 2) break;
      index += 1;
    }
    return index;
  }
</script>

<svelte:window on:dragend={() => { dragover = false; dropSide = ''; }} />

<section
  class="wb-pane"
  class:wb-pane-active={active}
  class:wb-pane-dragover={dragover}
  class:wb-drop-left={dropSide === 'left'}
  class:wb-drop-right={dropSide === 'right'}
  class:wb-drop-top={dropSide === 'top'}
  class:wb-drop-bottom={dropSide === 'bottom'}
  data-group-id={group?.id}
  data-selected-tab={view?.selectedTabId || ""}
  data-tab-count={(view?.tabs || []).length}
  on:pointerdown={() => onFocusGroup(group?.id)}
  on:dragover={dragOver}
  on:dragleave={dragLeave}
  on:dragend={() => { dragover = false; dropSide = ''; }}
  on:drop={drop}
>
  <TabStrip groupId={group?.id} tabs={view?.tabs || []} selectedTabId={view?.selectedTabId || ''} onSelect={onSelectTab} onClose={onCloseTab} {onCloseOthers} {onCloseRight} {onDragStartTab} />
  <div class="wb-pane-body">
    {#if selectedTab}
      {@render pane(group?.id, selectedTab)}
    {:else}
      <EmptyPane onNewTask={onNewTask} />
    {/if}
  </div>
</section>

<style>
  .wb-pane { position: relative; display: flex; flex-direction: column; min-width: 0; min-height: 0; width: 100%; height: 100%; border: 1px solid transparent; }
  .wb-pane-active { border-color: rgba(80,140,255,.35); }
  .wb-pane-body { flex: 1; min-height: 0; overflow: hidden; display: flex; flex-direction: column; }
  .wb-pane-dragover::after {
    content: '';
    position: absolute;
    inset: 0;
    pointer-events: none;
    background: rgba(80,140,255,.12);
    border: 1px dashed rgba(80,140,255,.6);
  }
  .wb-drop-left::after { background: linear-gradient(90deg, rgba(80,140,255,.28) 0 30%, rgba(80,140,255,.08) 30% 100%); }
  .wb-drop-right::after { background: linear-gradient(270deg, rgba(80,140,255,.28) 0 30%, rgba(80,140,255,.08) 30% 100%); }
  .wb-drop-top::after { background: linear-gradient(180deg, rgba(80,140,255,.28) 0 30%, rgba(80,140,255,.08) 30% 100%); }
  .wb-drop-bottom::after { background: linear-gradient(0deg, rgba(80,140,255,.28) 0 30%, rgba(80,140,255,.08) 30% 100%); }
</style>
