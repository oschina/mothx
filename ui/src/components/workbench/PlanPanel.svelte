<script>
  import { onDestroy, tick } from 'svelte';
  import {
    isSessionRuntimeBusy,
    sessionRunStates,
    updateSessionState
  } from '../../lib/session-runs.js';
  import { normalizeSessionMessage, upsertMessageInList } from '../../lib/session-view.js';
  import { updateSessionPlan } from '../../lib/stores.js';
  import { t } from '../../lib/preferences.js';

  export let sessionId = '';
  export let mobile = false;
  export let width = 320;
  export let onClose = () => {};

  let bodyEl;
  let titleEl;
  let editing = false;
  let editSessionId = '';
  let draft = null;
  let saving = false;
  let error = '';
  let saved = false;
  let savedTimer = null;
  let destroyed = false;

  $: state = sessionId ? $sessionRunStates?.[sessionId] : null;
  $: planMessages = (state?.messages || []).filter((message) => message?.role === 'plan' && message.plan);
  $: plan = planMessages.length ? planMessages[planMessages.length - 1].plan : null;
  $: running = isSessionRuntimeBusy(state?.runtime);
  $: doneCount = plan ? plan.steps.filter((step) => step.status === 'done').length : 0;
  $: progress = plan?.steps?.length ? Math.round((doneCount / plan.steps.length) * 100) : 0;
  $: if (editing && editSessionId !== sessionId) {
    editing = false;
    draft = null;
    error = '';
    saving = false;
  }

  onDestroy(() => {
    destroyed = true;
    if (savedTimer) clearTimeout(savedTimer);
  });

  function statusLabel(status) {
    switch (status) {
      case 'done': return $t('chat.plan.done');
      case 'running': return $t('chat.plan.running');
      case 'failed': return $t('chat.plan.failed');
      default: return $t('chat.plan.pending');
    }
  }

  const MAX_PLAN_STEPS = 50;

  function focusStep(index, selectText = false) {
    const input = bodyEl?.querySelector(`[data-plan-step="${index}"]`);
    input?.focus();
    if (selectText) input?.select?.();
  }

  function scrollToBottom() {
    if (bodyEl) bodyEl.scrollTop = bodyEl.scrollHeight;
  }

  function startEdit(create = false) {
    if (saving || (running && !create)) return;
    editSessionId = sessionId;
    const base = plan || { title: '', note: '', steps: [] };
    draft = {
      title: base.title || '',
      note: base.note || '',
      steps: (base.steps || []).map((step) => ({ title: step.title, status: step.status }))
    };
    if (draft.steps.length === 0) draft.steps = [{ title: '', status: 'pending' }];
    editing = true;
    error = '';
    saved = false;
    tick().then(() => {
      if (create) {
        focusStep(0, true);
        scrollToBottom();
      } else if (!draft?.title) {
        focusStep(0);
      } else {
        titleEl?.focus();
        titleEl?.select?.();
      }
    });
  }

  function cancelEdit() {
    if (saving) return;
    editing = false;
    draft = null;
    error = '';
  }

  function addStep() {
    if (!draft || saving || draft.steps.length >= MAX_PLAN_STEPS) return;
    draft = { ...draft, steps: [...draft.steps, { title: '', status: 'pending' }] };
    tick().then(() => {
      focusStep(draft.steps.length - 1, true);
      scrollToBottom();
    });
  }

  function addStepAfter(index) {
    if (!draft || saving || draft.steps.length >= MAX_PLAN_STEPS) return;
    const steps = [...draft.steps];
    steps.splice(index + 1, 0, { title: '', status: 'pending' });
    draft = { ...draft, steps };
    tick().then(() => {
      focusStep(index + 1, true);
      scrollToBottom();
    });
  }

  function removeStep(index) {
    if (!draft || saving || draft.steps.length <= 1) return;
    const steps = draft.steps.filter((_, stepIndex) => stepIndex !== index);
    draft = { ...draft, steps };
    tick().then(() => focusStep(Math.min(index, steps.length - 1)));
  }

  function handleEditorKeydown(event) {
    if (event.key === 'Escape') {
      event.preventDefault();
      cancelEdit();
      return;
    }
    if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') {
      event.preventDefault();
      save();
      return;
    }
    if (event.key === 'Enter' && !event.metaKey && !event.ctrlKey) {
      const target = event.target;
      if (target?.classList?.contains('plan-step-input')) {
        event.preventDefault();
        addStepAfter(Number(target.dataset?.planStep || 0));
        return;
      }
      if (target?.tagName === 'INPUT') {
        event.preventDefault();
        save();
      }
    }
  }

  function applySavedPlan(targetSessionId, result, submitted) {
    const fallbackPlan = result?.plan || submitted;
    if (result?.entry) {
      const normalized = normalizeSessionMessage(result.entry, (key) => $t(key));
      if (normalized?.plan) {
        updateSessionState(targetSessionId, (current) => ({
          ...current,
          messages: upsertMessageInList(current.messages, normalized)
        }));
        return;
      }
    }
    updateSessionState(targetSessionId, (current) => ({
      ...current,
      messages: upsertMessageInList(current.messages, {
        id: `ui-plan-${Date.now()}`,
        role: 'plan',
        toolName: 'plan',
        plan: fallbackPlan
      })
    }));
  }

  async function save() {
    if (!draft || saving) return;
    const steps = draft.steps.map((step) => ({
      title: String(step.title || '').trim(),
      status: step.status || 'pending'
    }));
    if (steps.length === 0) {
      error = $t('workbench.plan.error.emptyStep');
      return;
    }
    const emptyIndex = steps.findIndex((step) => !step.title);
    if (emptyIndex >= 0) {
      error = $t('workbench.plan.error.emptyStep');
      focusStep(emptyIndex);
      return;
    }
    saving = true;
    error = '';
    const targetSessionId = sessionId;
    const submitted = {
      title: (draft.title || '').trim(),
      note: (draft.note || '').trim(),
      steps
    };
    try {
      const result = await updateSessionPlan(targetSessionId, submitted);
      applySavedPlan(targetSessionId, result, submitted);
      if (destroyed || targetSessionId !== sessionId) return;
      editing = false;
      draft = null;
      saved = true;
      if (savedTimer) clearTimeout(savedTimer);
      savedTimer = setTimeout(() => { saved = false; }, 2200);
    } catch (err) {
      if (destroyed || targetSessionId !== sessionId) return;
      if (err?.code === 'session_busy') {
        error = $t('workbench.plan.error.busy');
      } else if (err?.code === 'invalid_plan') {
        error = $t('workbench.plan.error.invalid');
      } else {
        error = $t('workbench.plan.error.save');
      }
    } finally {
      saving = false;
    }
  }
</script>

<aside
  class="wb-plan-panel"
  class:wb-plan-mobile={mobile}
  style={mobile ? '' : `flex: 0 0 ${Math.round(width)}px`}
  aria-label={$t('workbench.plan')}
>
  <header>
    <strong>{editing ? $t('workbench.plan.editing') : $t('workbench.plan')}</strong>
    {#if !editing && plan}
      <span class="wb-plan-count" title={$t('workbench.plan.steps', { n: plan.steps.length })}>
        {doneCount}/{plan.steps.length}
      </span>
    {/if}
    {#if saved && !editing}
      <span class="wb-plan-saved" aria-live="polite" title={$t('workbench.plan.saved')}>
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 6 9 17l-5-5"/></svg>
      </span>
    {/if}
    {#if !editing && plan}
      <button
        type="button"
        class="wb-plan-icon wb-plan-edit"
        on:click={() => startEdit(false)}
        disabled={running}
        title={running ? $t('workbench.plan.error.busy') : $t('workbench.plan.edit')}
        aria-label={$t('workbench.plan.edit')}
      >
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z"/></svg>
      </button>
    {/if}
    <button type="button" class="wb-plan-icon wb-plan-close" on:click={onClose} title={$t('common.close')} aria-label={$t('common.close')}>
      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18"/></svg>
    </button>
  </header>

  <div class="wb-plan-body" bind:this={bodyEl}>
    {#if editing}
      <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
      <form class="wb-plan-editor" on:submit|preventDefault={save} on:keydown={handleEditorKeydown}>
        <input
          class="plan-input plan-title-input"
          bind:this={titleEl}
          bind:value={draft.title}
          maxlength="200"
          placeholder={$t('workbench.plan.titlePlaceholder')}
          aria-label={$t('workbench.plan.titlePlaceholder')}
          on:input={() => (error = '')}
        />
        <ol class="plan-edit-steps">
          {#each draft.steps as step, index}
            <li class="plan-edit-step">
              <span class="plan-edit-index" aria-hidden="true">{index + 1}</span>
              <div class="plan-edit-step-main">
                <input
                  class="plan-input plan-step-input"
                  bind:value={step.title}
                  data-plan-step={index}
                  maxlength="500"
                  placeholder={$t('workbench.plan.stepPlaceholder')}
                  aria-label={$t('workbench.plan.stepAria', { n: index + 1 })}
                  on:input={() => (error = '')}
                />
                <div class="plan-edit-step-meta">
                  <label class="plan-edit-status">
                    <span class="plan-status-dot" class:running={step.status === 'running'} class:done={step.status === 'done'} class:failed={step.status === 'failed'} aria-hidden="true"></span>
                    <select
                      bind:value={step.status}
                      aria-label={$t('workbench.plan.statusAria', { n: index + 1 })}
                    >
                      <option value="pending">{statusLabel('pending')}</option>
                      <option value="running">{statusLabel('running')}</option>
                      <option value="done">{statusLabel('done')}</option>
                      <option value="failed">{statusLabel('failed')}</option>
                    </select>
                  </label>
                  <button
                    type="button"
                    class="plan-edit-remove"
                    on:click={() => removeStep(index)}
                    disabled={saving || draft.steps.length <= 1}
                    title={$t('workbench.plan.removeStep')}
                    aria-label={$t('workbench.plan.removeStepAria', { n: index + 1 })}
                  >
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18"/></svg>
                  </button>
                </div>
              </div>
            </li>
          {/each}
        </ol>
        <button type="button" class="plan-edit-add" on:click={addStep} disabled={saving || draft.steps.length >= MAX_PLAN_STEPS}>
          <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" aria-hidden="true"><path d="M12 5v14M5 12h14"/></svg>
          {$t('workbench.plan.addStep')}
        </button>
        <textarea
          class="plan-input plan-note-input"
          bind:value={draft.note}
          rows="2"
          maxlength="4000"
          placeholder={$t('workbench.plan.notePlaceholder')}
          aria-label={$t('workbench.plan.notePlaceholder')}
          on:input={() => (error = '')}
        ></textarea>
        {#if error}
          <p class="wb-plan-error" role="alert">{error}</p>
        {/if}
        <div class="plan-edit-actions">
          <button type="button" class="plan-btn" on:click={cancelEdit} disabled={saving}>{$t('workbench.plan.cancel')}</button>
          <button type="submit" class="plan-btn primary" disabled={saving}>
            {#if saving}<span class="plan-spinner" aria-hidden="true"></span>{/if}
            {saving ? $t('workbench.plan.saving') : $t('workbench.plan.save')}
          </button>
        </div>
      </form>
    {:else if plan}
      <section class="todo-plan">
        {#if plan.title}
          <h3>{plan.title}</h3>
        {/if}
        <div
          class="wb-plan-progress"
          role="progressbar"
          aria-valuemin="0"
          aria-valuemax={plan.steps.length}
          aria-valuenow={doneCount}
          aria-label={$t('workbench.plan.progress')}
        >
          <span style={`width:${progress}%`}></span>
        </div>
        {#if running}
          <p class="wb-plan-running"><span class="plan-live-dot" aria-hidden="true"></span>{$t('workbench.plan.runningHint')}</p>
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
      <div class="wb-plan-empty">
        <svg class="wb-plan-empty-icon" width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <rect x="5" y="4" width="14" height="17" rx="2.5"/><path d="M9 4.5h6M9 10h6M9 14h6M9 18h3"/>
        </svg>
        <p>{$t('workbench.plan.empty')}</p>
        <button type="button" class="plan-btn" on:click={() => startEdit(true)} disabled={running}>
          {$t('workbench.plan.create')}
        </button>
        {#if running}
          <p class="wb-plan-empty-hint">{$t('workbench.plan.error.busy')}</p>
        {/if}
      </div>
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
    background: var(--moth-surface-page);
  }
  .wb-plan-panel header {
    display: flex;
    align-items: center;
    gap: 5px;
    min-height: 38px;
    padding: 0 6px 0 12px;
    border-bottom: 1px solid var(--moth-border-subtle);
  }
  .wb-plan-panel header strong { flex: 1; min-width: 0; font-size: 12px; font-weight: 600; }
  .wb-plan-count {
    flex: 0 0 auto;
    padding: 1px 6px;
    border-radius: 999px;
    background: var(--moth-surface-hover);
    color: var(--moth-text-muted);
    font-size: 10.5px;
    font-variant-numeric: tabular-nums;
    line-height: 16px;
  }
  .wb-plan-saved {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 20px;
    height: 20px;
    border-radius: 50%;
    background: color-mix(in srgb, var(--success) 12%, transparent);
    color: var(--success);
  }
  .wb-plan-icon {
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
  .wb-plan-icon:hover:not(:disabled) { background: var(--moth-surface-hover); color: var(--moth-text-primary); }
  .wb-plan-edit { color: var(--moth-text-secondary); }
  .wb-plan-edit:hover:not(:disabled) { color: var(--moth-brand-primary); }
  .wb-plan-icon:disabled { opacity: .4; cursor: not-allowed; }
  .wb-plan-icon:focus-visible { outline: 2px solid var(--moth-selection-indicator); outline-offset: -2px; }
  .wb-plan-body { flex: 1; min-height: 0; overflow-y: auto; padding: 10px; }

  .wb-plan-panel .todo-plan {
    border: 0;
    background: transparent;
  }
  .wb-plan-panel .todo-plan h3 {
    padding: 0 2px 8px;
    border-bottom: 0;
    font-size: 13px;
    font-weight: 650;
  }
  .wb-plan-panel .todo-plan ol { padding: 0; }
  .wb-plan-panel .todo-plan li {
    grid-template-columns: 14px minmax(0, 1fr) auto;
    gap: 8px;
    min-height: 30px;
    padding: 7px 2px;
    font-size: 12.5px;
  }
  .wb-plan-panel .todo-plan li + li { border-top-color: color-mix(in srgb, var(--moth-border-subtle) 70%, transparent); }
  .wb-plan-panel .todo-plan em { font-size: 11px; }
  .wb-plan-panel .todo-plan > p {
    padding: 9px 2px 0;
    font-size: 12px;
  }
  .wb-plan-progress {
    height: 3px;
    margin: 0 2px 10px;
    border-radius: 999px;
    background: var(--moth-surface-hover);
    overflow: hidden;
  }
  .wb-plan-progress span {
    display: block;
    height: 100%;
    border-radius: inherit;
    background: var(--moth-brand-primary);
    transition: width .2s ease;
  }
  .wb-plan-running {
    display: flex;
    align-items: center;
    gap: 6px;
    margin: 0 2px 8px;
    color: var(--moth-text-muted);
    font-size: 11.5px;
    line-height: 1.4;
  }
  .plan-live-dot {
    width: 5px;
    height: 5px;
    flex: 0 0 5px;
    border-radius: 50%;
    background: var(--running);
    animation: plan-pulse 1.6s ease-in-out infinite;
  }
  @keyframes plan-pulse {
    0%, 100% { opacity: .4; }
    50% { opacity: 1; }
  }

  .wb-plan-empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 8px;
    padding: 28px 8px;
    text-align: center;
  }
  .wb-plan-empty-icon { color: var(--moth-text-muted); opacity: .7; }
  .wb-plan-empty p {
    margin: 0;
    color: var(--moth-text-muted);
    font-size: 12px;
    line-height: 1.5;
  }
  .wb-plan-empty-hint { font-size: 11px !important; color: var(--moth-text-secondary) !important; }

  .wb-plan-editor { display: flex; flex-direction: column; gap: 8px; }
  .plan-input {
    width: 100%;
    box-sizing: border-box;
    padding: 6px 8px;
    border: 1px solid var(--moth-border-subtle);
    border-radius: 6px;
    background: var(--moth-surface-page);
    color: var(--moth-text-primary);
    font: inherit;
    font-size: 12.5px;
    line-height: 1.4;
  }
  .plan-input::placeholder { color: var(--moth-text-muted); }
  .plan-input:focus {
    outline: none;
    border-color: color-mix(in srgb, var(--moth-brand-primary) 55%, var(--moth-border-subtle));
    box-shadow: 0 0 0 2px color-mix(in srgb, var(--moth-brand-primary) 12%, transparent);
  }
  .plan-title-input { font-weight: 600; }
  .plan-note-input { min-height: 52px; resize: vertical; }
  .plan-edit-steps {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .plan-edit-step {
    display: grid;
    grid-template-columns: 16px minmax(0, 1fr);
    gap: 6px;
    padding: 7px;
    border: 1px solid var(--moth-border-subtle);
    border-radius: 7px;
    background: var(--moth-surface-secondary);
  }
  .plan-edit-index {
    padding-top: 7px;
    color: var(--moth-text-muted);
    font-size: 10px;
    font-variant-numeric: tabular-nums;
    text-align: center;
  }
  .plan-edit-step-main { display: flex; min-width: 0; flex-direction: column; gap: 6px; }
  .plan-edit-step-meta { display: flex; align-items: center; justify-content: space-between; gap: 6px; }
  .plan-edit-status { display: inline-flex; align-items: center; gap: 5px; min-width: 0; }
  .plan-edit-status select {
    max-width: 100%;
    padding: 2px 4px;
    border: 1px solid transparent;
    border-radius: 5px;
    background: transparent;
    color: var(--moth-text-secondary);
    font: inherit;
    font-size: 11.5px;
    cursor: pointer;
  }
  .plan-edit-status select:hover { background: var(--moth-surface-hover); }
  .plan-edit-status select:focus-visible { outline: 2px solid var(--moth-selection-indicator); outline-offset: 0; }
  .plan-status-dot {
    width: 6px;
    height: 6px;
    flex: 0 0 6px;
    border-radius: 50%;
    background: var(--moth-text-muted);
  }
  .plan-status-dot.running { background: var(--running); }
  .plan-status-dot.done { background: var(--success); }
  .plan-status-dot.failed { background: var(--danger); }
  .plan-edit-remove {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 22px;
    height: 22px;
    flex: 0 0 22px;
    padding: 0;
    border: 0;
    border-radius: 5px;
    background: transparent;
    color: var(--moth-text-secondary);
    cursor: pointer;
  }
  .plan-edit-remove:hover:not(:disabled) {
    background: color-mix(in srgb, var(--danger) 10%, transparent);
    color: var(--danger);
  }
  .plan-edit-remove:disabled { opacity: .3; cursor: not-allowed; }
  .plan-edit-remove:focus-visible { outline: 2px solid var(--moth-selection-indicator); outline-offset: -2px; }
  .plan-edit-add {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 5px;
    width: 100%;
    min-height: 30px;
    padding: 0 8px;
    border: 1px dashed var(--moth-border-strong);
    border-radius: 6px;
    background: transparent;
    color: var(--moth-text-secondary);
    font: inherit;
    font-size: 11.5px;
    cursor: pointer;
  }
  .plan-edit-add:hover:not(:disabled) {
    border-color: var(--moth-brand-primary);
    background: var(--moth-surface-hover);
    color: var(--moth-brand-primary);
  }
  .plan-edit-add:disabled { opacity: .5; cursor: not-allowed; }
  .plan-edit-add:focus-visible { outline: 2px solid var(--moth-selection-indicator); outline-offset: -2px; }
  .wb-plan-error {
    margin: 0;
    padding: 6px 8px;
    border-radius: 6px;
    background: color-mix(in srgb, var(--danger) 8%, transparent);
    color: var(--danger);
    font-size: 11.5px;
    line-height: 1.45;
  }
  .plan-edit-actions {
    position: sticky;
    bottom: 0;
    z-index: 1;
    display: flex;
    justify-content: flex-end;
    gap: 6px;
    margin: 0 -10px -10px;
    padding: 10px;
    border-top: 1px solid var(--moth-border-subtle);
    background: var(--moth-surface-page);
  }
  .plan-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 5px;
    min-height: 30px;
    padding: 0 11px;
    border: 1px solid var(--moth-border-strong);
    border-radius: 6px;
    background: var(--moth-surface-page);
    color: var(--moth-text-primary);
    font: inherit;
    font-size: 12px;
    font-weight: 550;
    cursor: pointer;
  }
  .plan-btn:hover:not(:disabled) { background: var(--moth-surface-hover); }
  .plan-btn:disabled { opacity: .5; cursor: not-allowed; }
  .plan-btn:focus-visible { outline: 2px solid var(--moth-selection-indicator); outline-offset: 1px; }
  .plan-btn.primary {
    border-color: transparent;
    background: var(--moth-brand-primary);
    color: var(--moth-brand-contrast);
  }
  .plan-btn.primary:hover:not(:disabled) { filter: brightness(1.05); }
  .plan-spinner {
    width: 10px;
    height: 10px;
    border: 1.5px solid color-mix(in srgb, currentColor 30%, transparent);
    border-top-color: currentColor;
    border-radius: 50%;
    animation: plan-spin .7s linear infinite;
  }
  @keyframes plan-spin { to { transform: rotate(360deg); } }

  .wb-plan-mobile {
    position: absolute;
    top: 0;
    right: 0;
    bottom: 0;
    z-index: 20;
    width: min(86vw, 340px);
    border-left: 1px solid var(--moth-border-subtle);
    box-shadow: -8px 0 24px rgba(0, 0, 0, .18);
  }

  @media (max-width: 900px) {
    .wb-plan-icon { width: 32px; height: 32px; }
    .plan-btn { min-height: 36px; padding: 0 13px; }
    .plan-edit-add { min-height: 34px; }
    .plan-edit-remove { width: 28px; height: 28px; flex-basis: 28px; }
    .plan-edit-status select { min-height: 26px; }
  }
</style>
