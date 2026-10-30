<script>
  import { sessionRunStates } from '../../lib/session-runs.js';
  import { t } from '../../lib/preferences.js';

  export let sessionId = '';
  export let mobile = false;
  export let onClose = () => {};

  $: state = sessionId ? $sessionRunStates?.[sessionId] : null;
  $: planMessages = (state?.messages || []).filter((message) => message?.role === 'plan' && message.plan);
  $: plan = planMessages.length ? planMessages[planMessages.length - 1].plan : null;

  function statusLabel(status) {
    switch (status) {
      case 'done': return $t('chat.plan.done');
      case 'running': return $t('chat.plan.running');
      case 'failed': return $t('chat.plan.failed');
      default: return $t('chat.plan.pending');
    }
  }
</script>

<aside class="wb-plan-panel" class:wb-plan-mobile={mobile} aria-label={$t('workbench.plan')}>
  <header>
    <strong>{$t('workbench.plan')}</strong>
    <button type="button" class="wb-plan-close" on:click={onClose} title={$t('common.close')} aria-label={$t('common.close')}>
      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18"/></svg>
    </button>
  </header>
  <div class="wb-plan-body">
    {#if plan}
      <section class="todo-plan">
        {#if plan.title}
          <h3>{plan.title}</h3>
        {/if}
        <ol>
          {#each plan.steps as step}
            <li class:done={step.status === 'done'} class:running={step.status === 'running'} class:failed={step.status === 'failed'}>
              <span class="todo-mark" aria-hidden="true"></span>
              <span class="todo-title">{step.title}</span>
              <em>{statusLabel(step.status)}</em>
            </li>
          {/each}
        </ol>
        {#if plan.note}
          <p>{plan.note}</p>
        {/if}
      </section>
    {:else}
      <p class="wb-plan-empty">{$t('workbench.plan.empty')}</p>
    {/if}
  </div>
</aside>

<style>
  .wb-plan-panel {
    display: flex;
    flex-direction: column;
    flex: 0 0 320px;
    min-width: 0;
    min-height: 0;
    border-left: 1px solid var(--moth-border-subtle);
    background: var(--moth-surface-page);
  }
  .wb-plan-panel header {
    display: flex;
    align-items: center;
    gap: 8px;
    min-height: 38px;
    padding: 0 6px 0 12px;
    border-bottom: 1px solid var(--moth-border-subtle);
  }
  .wb-plan-panel header strong { flex: 1; min-width: 0; font-size: 12px; font-weight: 600; }
  .wb-plan-close {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 24px;
    height: 24px;
    min-height: 0;
    padding: 0;
    border: 0;
    border-radius: 6px;
    background: transparent;
    color: var(--moth-text-muted);
    cursor: pointer;
  }
  .wb-plan-close:hover { background: var(--moth-surface-hover); color: var(--moth-text-primary); }
  .wb-plan-close:focus-visible { outline: 2px solid var(--moth-selection-indicator); outline-offset: -2px; }
  .wb-plan-body { flex: 1; min-height: 0; overflow-y: auto; padding: 10px; }
  .wb-plan-empty { margin: 0; padding: 24px 8px; text-align: center; color: var(--moth-text-muted); font-size: 12px; line-height: 1.5; }
  .wb-plan-mobile {
    position: absolute;
    top: 0;
    right: 0;
    bottom: 0;
    z-index: 20;
    width: min(86vw, 340px);
    box-shadow: -8px 0 24px rgba(0, 0, 0, .18);
  }
</style>
