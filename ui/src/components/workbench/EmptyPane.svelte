<script>
  import { t } from '../../lib/preferences.js';

  export let onNewTask = () => {};
</script>

<div class="wb-empty">
  <div class="wb-empty-inner">
    <span class="wb-empty-mark" aria-hidden="true">
      <img src="/mothx-small.ico" alt="" />
    </span>
    <h2>{$t('workbench.empty.title') || 'No session open'}</h2>
    <p>{$t('workbench.empty.hint') || 'Pick a session from the sidebar, or start a new task.'}</p>
    <button type="button" class="wb-empty-cta" onclick={onNewTask}>
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>
      <span>{$t('workbench.empty.newTask') || 'New task'}</span>
    </button>
  </div>
</div>

<style>
  .wb-empty {
    display: grid;
    place-items: center;
    height: 100%;
    min-height: 0;
    padding: 32px 20px;
    background:
      radial-gradient(420px 300px at 50% 38%, color-mix(in srgb, var(--accent) 6%, transparent), transparent 72%);
  }
  .wb-empty-inner {
    display: grid;
    justify-items: center;
    gap: 8px;
    width: min(100%, 380px);
    text-align: center;
  }
  .wb-empty-mark {
    display: grid;
    place-items: center;
    width: 52px;
    height: 52px;
    margin-bottom: 6px;
    border: 1px solid var(--border);
    border-radius: 14px;
    background: var(--bg);
    box-shadow: var(--shadow-sm);
  }
  .wb-empty-mark img {
    display: block;
    width: 28px;
    height: 28px;
    object-fit: contain;
  }
  .wb-empty h2 {
    margin: 0;
    color: var(--text);
    font-size: 15px;
    font-weight: 600;
  }
  .wb-empty p {
    margin: 0;
    color: var(--text-secondary);
    font-size: 13px;
    line-height: 1.6;
  }
  .wb-empty-cta {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    min-height: 34px;
    margin-top: 8px;
    padding: 0 14px;
    border: 1px solid var(--primary);
    border-radius: 8px;
    background: var(--primary);
    color: var(--primary-contrast);
    font-size: 13px;
    font-weight: 600;
    cursor: pointer;
    transition: background-color .15s ease, border-color .15s ease;
  }
  .wb-empty-cta:hover {
    border-color: var(--primary-hover);
    background: var(--primary-hover);
  }
  .wb-empty-cta:focus-visible {
    outline: 2px solid var(--primary);
    outline-offset: 2px;
  }
  .wb-empty-cta svg {
    width: 15px;
    height: 15px;
  }
</style>
