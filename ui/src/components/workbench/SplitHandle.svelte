<script>
  export let direction = 'horizontal';
  export let value = 50;
  export let dragging = false;
  export let onPointerDown = () => {};
  export let onKeyDown = () => {};
  export let onDoubleClick = () => {};
</script>

<div
  class="wb-split-handle wb-split-handle-{direction}"
  class:dragging
  role="separator"
  aria-orientation={direction === 'horizontal' ? 'vertical' : 'horizontal'}
  aria-valuemin="10"
  aria-valuemax="90"
  aria-valuenow={Math.round(value)}
  tabindex="0"
  on:pointerdown={onPointerDown}
  on:keydown={onKeyDown}
  on:dblclick={onDoubleClick}
></div>

<style>
  .wb-split-handle { flex: 0 0 9px; background: rgba(120, 120, 120, .2); touch-action: none; cursor: col-resize; }
  .wb-split-handle-vertical { cursor: row-resize; }
  .wb-split-handle.dragging { background: rgba(80, 140, 255, .55); }
  .wb-split-handle:focus-visible { outline: 2px solid #6ea8ff; outline-offset: -2px; }
</style>
