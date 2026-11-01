<script>
  // Shared rail-panel chrome for the workbench side panels (plan, changes,
  // files). One owner of the header/body layout and icon buttons so a new
  // projection panel cannot fork the pattern. Panels stay presentational:
  // state, fetching, and refresh policy remain in the caller.
  import { t } from '../../lib/preferences.js';

  export let titleKey = '';
  export let width = 320;
  export let mobile = false;
  export let onClose = () => {};
  export let bodyClass = '';
  export let ariaLabelKey = '';
</script>

<aside
  class="wb-rail"
  style={mobile ? '' : `flex: 0 0 ${Math.round(width)}px`}
  aria-label={$t(ariaLabelKey) || titleKey}
>
  <header>
    <strong>{$t(titleKey) || titleKey}</strong>
    <slot name="head" />
    <button type="button" class="wb-rail-icon wb-rail-close" onclick={onClose} title={$t('common.close')} aria-label={$t('common.close')}>
      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18"/></svg>
    </button>
  </header>
  <div class="wb-rail-body {bodyClass}">
    <slot />
  </div>
</aside>

<style>
  .wb-rail {
    display: flex;
    flex-direction: column;
    flex: 0 0 320px;
    min-width: 0;
    min-height: 0;
    background: var(--moth-surface-page);
  }
  .wb-rail header {
    display: flex;
    align-items: center;
    gap: 5px;
    min-height: 38px;
    padding: 0 6px 0 12px;
    border-bottom: 1px solid var(--moth-border-subtle);
  }
  .wb-rail header strong { flex: 0 0 auto; font-size: 12px; font-weight: 600; }
  .wb-rail-icon {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 26px;
    height: 26px;
    min-height: 0;
    padding: 0;
    border: 0;
    border-radius: 6px;
    background: transparent;
    color: var(--moth-text-muted);
    cursor: pointer;
  }
  .wb-rail-icon:hover:not(:disabled) { background: var(--moth-surface-hover); color: var(--moth-text-primary); }
  .wb-rail-icon:disabled { opacity: .4; cursor: not-allowed; }
  .wb-rail-icon:focus-visible { outline: 2px solid var(--moth-selection-indicator); outline-offset: -2px; }
  .wb-rail-icon :global(.spinning) { animation: wb-rail-spin 1s linear infinite; }
  @keyframes wb-rail-spin { to { transform: rotate(360deg); } }
  .wb-rail-body { flex: 1; min-height: 0; overflow-y: auto; }
</style>
