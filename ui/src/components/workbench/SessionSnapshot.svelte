<script>
  import { sessionStateStore } from '../../lib/session-store.js';
  import { t } from '../../lib/preferences.js';

  export let sessionId = '';
  export let onFocus = () => {};

  const state = sessionStateStore(sessionId);
  $: messages = $state?.messages || [];
  $: runtime = $state?.runtime || null;
  $: pending = (runtime?.pendingApprovals || []).length + (runtime?.pendingQuestions || []).length;
</script>

<article class="wb-snapshot" aria-label={sessionId}>
  <header>
    <span class="wb-snapshot-title">{sessionId || 'Session'}</span>
    {#if pending > 0}<span class="wb-snapshot-pending">{pending}</span>{/if}
    <button type="button" onclick={() => onFocus(sessionId)}>{$t('workbench.snapshot.focus') || 'Focus'}</button>
  </header>
  <div class="wb-snapshot-body">
    {#each messages.slice(-80) as message, index (message.id || index)}
      <div class="wb-snapshot-message" class:wb-snapshot-user={message.role === 'user'}>
        <span class="wb-snapshot-role">{message.role || 'message'}</span>
        <span class="wb-snapshot-content">{message.content || ''}</span>
      </div>
    {/each}
  </div>
</article>

<style>
  .wb-snapshot { display: flex; flex-direction: column; height: 100%; min-height: 0; background: rgba(120,120,120,.04); }
  .wb-snapshot header { display: flex; align-items: center; gap: 8px; padding: 8px 10px; border-bottom: 1px solid rgba(120,120,120,.2); font-size: 12px; }
  .wb-snapshot-title { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .wb-snapshot-pending { border-radius: 999px; padding: 0 6px; background: #b45309; color: white; }
  .wb-snapshot header button { margin-left: auto; border: 1px solid rgba(120,120,120,.35); background: transparent; color: inherit; border-radius: 5px; padding: 2px 8px; cursor: pointer; }
  .wb-snapshot-body { flex: 1; min-height: 0; overflow: auto; padding: 8px; display: flex; flex-direction: column; gap: 8px; }
  .wb-snapshot-message { display: flex; flex-direction: column; gap: 3px; padding: 6px 8px; border-radius: 6px; background: rgba(120,120,120,.08); white-space: pre-wrap; }
  .wb-snapshot-user { background: rgba(80,140,255,.12); }
  .wb-snapshot-role { font-size: 10px; text-transform: uppercase; opacity: .6; }
  .wb-snapshot-content { font-size: 13px; overflow-wrap: anywhere; }
</style>
