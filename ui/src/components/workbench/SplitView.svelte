<script>
  import SplitHandle from './SplitHandle.svelte';

  export let node;
  export let onResize = () => {};
  export let pane;

  let dragging = false;
  let draft = node?.kind === 'split' ? [...node.weights] : [0.5, 0.5];
  let start = null;
  let host;

  $: if (!dragging && node?.kind === 'split') draft = [...node.weights];

  function clamp(value) {
    return Math.min(0.9, Math.max(0.1, value));
  }

  function pointerdown(event) {
    if (node?.kind !== 'split') return;
    dragging = true;
    start = { x: event.clientX, y: event.clientY, weights: [...node.weights], size: host.getBoundingClientRect() };
    event.currentTarget.setPointerCapture?.(event.pointerId);
  }

  function pointermove(event) {
    if (!dragging || !start) return;
    const horizontal = node.direction === 'horizontal';
    const total = horizontal ? start.size.width : start.size.height;
    if (!total) return;
    const delta = horizontal ? (event.clientX - start.x) / total : (event.clientY - start.y) / total;
    const first = clamp(start.weights[0] + delta);
    draft = [first, 1 - first];
  }

  function pointerup(event) {
    if (!dragging) return;
    dragging = false;
    onResize(node.id, draft);
    event.currentTarget.releasePointerCapture?.(event.pointerId);
  }

  function keydown(event) {
    if (node?.kind !== 'split') return;
    const horizontal = node.direction === 'horizontal';
    const delta = event.key === 'ArrowLeft' || event.key === 'ArrowUp' ? -0.05
      : event.key === 'ArrowRight' || event.key === 'ArrowDown' ? 0.05 : 0;
    if (!delta) return;
    if (horizontal && !['ArrowLeft', 'ArrowRight'].includes(event.key)) return;
    if (!horizontal && !['ArrowUp', 'ArrowDown'].includes(event.key)) return;
    event.preventDefault();
    const first = clamp(draft[0] + delta);
    draft = [first, 1 - first];
    onResize(node.id, draft);
  }

  function doubleclick() {
    if (node?.kind !== 'split') return;
    draft = [0.5, 0.5];
    onResize(node.id, draft);
  }
</script>

{#if node?.kind === 'group'}
  {@render pane(node)}
{:else if node?.kind === 'split'}
  <div class="wb-split wb-split-{node.direction}" bind:this={host}>
    <div class="wb-split-child" style={`flex: ${draft[0]} 1 0%`}>
      <svelte:self node={node.children[0]} {onResize} {pane} />
    </div>
    <SplitHandle
      direction={node.direction}
      value={draft[0] * 100}
      {dragging}
      onPointerDown={pointerdown}
      onKeyDown={keydown}
      onDoubleClick={doubleclick}
    />
    <div class="wb-split-child" style={`flex: ${draft[1]} 1 0%`}>
      <svelte:self node={node.children[1]} {onResize} {pane} />
    </div>
  </div>
{/if}

<svelte:window on:pointermove={pointermove} on:pointerup={pointerup} on:pointercancel={pointerup} />

<style>
  .wb-split { display: flex; min-width: 0; min-height: 0; width: 100%; height: 100%; }
  .wb-split-vertical { flex-direction: column; }
  .wb-split-child { display: flex; min-width: 0; min-height: 0; overflow: hidden; }
</style>
