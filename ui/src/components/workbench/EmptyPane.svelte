<script>
  import { t } from '../../lib/preferences.js';

  export let onNewTask = () => {};
</script>

<div class="wb-empty">
  <p>{$t('workbench.empty.title') || 'No session open'}</p>
  <button type="button" onclick={onNewTask}>{$t('workbench.empty.newTask') || 'New task'}</button>
</div>

<style>
  .wb-empty { display: grid; place-content: center; gap: 10px; height: 100%; color: rgba(160,160,160,.9); text-align: center; }
  .wb-empty button { justify-self: center; padding: 6px 14px; border-radius: 6px; border: 1px solid rgba(160,160,160,.35); background: transparent; color: inherit; cursor: pointer; }
</style>
