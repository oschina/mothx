<script>
  import { cronInfo, currentSession, refreshCron, setError, setNotice, clearBanners } from '../lib/stores.js';
  import { postJSON, patchJSON, del } from '../lib/api.js';
  import { shortID, scheduleLabel, formatDateTime } from '../lib/format.js';
  import { t } from '../lib/preferences.js';
  import Modal from '../components/Modal.svelte';
  import { RefreshCw } from '@lucide/svelte';

  let form = { name: '', prompt: '', schedule: '', oneshot: false, mode: 'yolo' };
  let lastLoadedSession = '';
  let selectedJobID = '';

  $: info = $cronInfo;
  $: disabled = info?.enabled === false;
  $: sessionID = $currentSession || '';
  $: jobs = info?.jobs || [];
  $: selectedJob = selectedJobID ? jobs.find((job) => job.id === selectedJobID) || null : null;
  $: if (sessionID && sessionID !== lastLoadedSession) {
    lastLoadedSession = sessionID;
    refreshCron(sessionID);
  }

  async function create() {
    if (!form.name.trim() || !form.prompt.trim()) return;
    if (!sessionID) {
      setError('sessionId is required');
      return;
    }
    clearBanners();
    try {
      await postJSON('/api/cron', {
        sessionId: sessionID,
        name: form.name.trim(),
        prompt: form.prompt,
        schedule: form.schedule.trim(),
        oneshot: form.oneshot,
        mode: form.mode
      });
      form = { name: '', prompt: '', schedule: '', oneshot: false, mode: 'yolo' };
      setNotice($t('cron.created'));
      await refreshCron(sessionID);
    } catch (err) {
      setError(err);
    }
  }

  async function refreshCurrentCron() {
    await refreshCron(sessionID);
  }

  function openDetails(job) {
    selectedJobID = job.id;
  }

  function closeDetails() {
    selectedJobID = '';
  }

  function handleWindowKeydown(event) {
    if (event.key === 'Escape' && selectedJob) closeDetails();
  }

  function detailDate(value) {
    if (!value) return '-';
    const date = new Date(value);
    if (Number.isNaN(date.getTime()) || date.getUTCFullYear() <= 1) return '-';
    return formatDateTime(value) || '-';
  }

  function sessionQuery() {
    return sessionID ? `?sessionId=${encodeURIComponent(sessionID)}` : '';
  }

  async function toggle(id, enabled) {
    clearBanners();
    try {
      await patchJSON(`/api/cron/${encodeURIComponent(id)}${sessionQuery()}`, { enabled });
      await refreshCron(sessionID);
    } catch (err) {
      setError(err);
    }
  }

  async function remove(id) {
    clearBanners();
    try {
      await del(`/api/cron/${encodeURIComponent(id)}${sessionQuery()}`);
      if (selectedJobID === id) closeDetails();
      setNotice($t('cron.deleted', { id: shortID(id) }));
      await refreshCron(sessionID);
    } catch (err) {
      setError(err);
    }
  }
</script>

<svelte:window on:keydown={handleWindowKeydown} />

<section class="page">
  <div class="page-toolbar">
    <div class="cron-status">
      <span class={`status-tag${disabled ? ' muted' : ''}`}>
        {disabled ? $t('common.disabledState') : info?.running ? $t('common.running') : $t('common.idle')}
      </span>
      {#if info?.path}<span class="cron-status-path" title={info.path}>{info.path}</span>{/if}
    </div>
    <button type="button" class="cron-refresh" on:click={refreshCurrentCron}>
      <RefreshCw size={14} aria-hidden="true" />
      {$t('common.refresh')}
    </button>
  </div>

  <div class="page-body">
    <div class="card">
      <div class="card-head"><h3>{$t('cron.newTask')}</h3></div>
      <form class="form-grid" on:submit|preventDefault={create}>
        <label>
          <span>{$t('cron.name')}</span>
          <input bind:value={form.name} disabled={disabled || !sessionID} placeholder={$t('cron.namePlaceholder')} />
        </label>
        <label>
          <span>{$t('cron.schedule')}</span>
          <input
            bind:value={form.schedule}
            disabled={disabled || !sessionID || form.oneshot}
            placeholder={$t('cron.schedulePlaceholder')}
          />
        </label>
        <label>
          <span>{$t('cron.mode')}</span>
          <select bind:value={form.mode} disabled={disabled || !sessionID}>
            <option value="yolo">yolo</option>
            <option value="agent">agent</option>
          </select>
        </label>
        <label class="checkbox">
          <input type="checkbox" bind:checked={form.oneshot} disabled={disabled || !sessionID} />
          <span>{$t('cron.oneshot')}</span>
        </label>
        <label class="full">
          <span>Prompt</span>
          <textarea bind:value={form.prompt} disabled={disabled || !sessionID} rows="4" placeholder={$t('cron.promptPlaceholder')}></textarea>
        </label>
        <div class="form-actions">
          <button
            type="submit"
            class="primary"
            disabled={disabled || !sessionID || !form.name.trim() || !form.prompt.trim()}
          >
            {$t('common.create')}
          </button>
        </div>
      </form>
    </div>

    <div class="card">
      <div class="card-head"><h3>{$t('cron.list')}</h3><span class="hint">{$t('common.items', { count: jobs.length })}</span></div>
      <div class="cron-list">
        {#each jobs as job (job.id)}
          <div class="cron-row">
            <button type="button" class="cron-main cron-open" title={$t('cron.openDetails')} on:click={() => openDetails(job)}>
              <strong>{job.name}</strong>
              <span class="mono">{shortID(job.id)}</span>
            </button>
            <div class="cron-meta">
              <span class="tag">{scheduleLabel(job)}</span>
              <span class="tag">{job.mode || 'yolo'}</span>
              <span>{$t('common.times', { count: job.run_count || 0 })}</span>
              {#if job.next_run}<span>{$t('common.next', { time: formatDateTime(job.next_run) })}</span>{/if}
              {#if job.last_status}<span class="tag">{job.last_status}</span>{/if}
            </div>
            <div class="cron-actions">
              {#if job.enabled}
                <button type="button" class="ghost" on:click={() => toggle(job.id, false)}>{$t('common.disable')}</button>
              {:else}
                <button type="button" class="ghost" on:click={() => toggle(job.id, true)}>{$t('common.enable')}</button>
              {/if}
              <button type="button" class="danger" on:click={() => remove(job.id)}>{$t('common.delete')}</button>
            </div>
            {#if job.last_error}
              <code class="cron-error">{job.last_error}</code>
            {/if}
          </div>
        {/each}
        {#if jobs.length === 0}
          <p class="empty">{$t('cron.empty')}</p>
        {/if}
      </div>
    </div>
  </div>
</section>

{#if selectedJob}
  <Modal open={Boolean(selectedJob)} title={selectedJob.name} labelledBy="cron-detail-title" className="cron-detail-overlay" on:close={closeDetails}>
    <div class="cron-detail-modal">
      <header class="cron-detail-header">
        <div>
          <h3 id="cron-detail-title">{selectedJob.name}</h3>
          <span class="mono">{selectedJob.id}</span>
        </div>
        <button type="button" class="ghost" aria-label={$t('common.close')} title={$t('common.close')} on:click={closeDetails}>✕</button>
      </header>

      <div class="cron-detail-body">
        <div class="cron-detail-grid">
          <div><span>{$t('cron.status')}</span><strong>{selectedJob.last_status || (selectedJob.enabled ? $t('common.enabled') : $t('common.disabledState'))}</strong></div>
          <div><span>{$t('cron.mode')}</span><strong>{selectedJob.mode || 'yolo'}</strong></div>
          <div><span>{$t('cron.schedule')}</span><strong>{scheduleLabel(selectedJob)}</strong></div>
          <div><span>{$t('cron.runs')}</span><strong>{selectedJob.run_count || 0}</strong></div>
          <div><span>{$t('cron.createdAt')}</span><strong>{detailDate(selectedJob.created_at)}</strong></div>
          <div><span>{$t('cron.lastRun')}</span><strong>{detailDate(selectedJob.last_run)}</strong></div>
          <div><span>{$t('cron.nextRun')}</span><strong>{detailDate(selectedJob.next_run)}</strong></div>
          {#if selectedJob.work_dir}
            <div class="wide"><span>{$t('cron.workDir')}</span><strong class="mono">{selectedJob.work_dir}</strong></div>
          {/if}
        </div>

        <div class="cron-detail-prompt">
          <span>{$t('cron.prompt')}</span>
          <pre>{selectedJob.prompt}</pre>
        </div>

        {#if selectedJob.last_error}
          <div class="cron-detail-error">
            <span>{$t('cron.error')}</span>
            <code>{selectedJob.last_error}</code>
          </div>
        {/if}
      </div>

      <footer class="cron-detail-footer">
        <button type="button" class="danger" on:click={() => remove(selectedJob.id)}>{$t('common.delete')}</button>
        <button type="button" class="ghost" on:click={() => toggle(selectedJob.id, !selectedJob.enabled)}>
          {selectedJob.enabled ? $t('common.disable') : $t('common.enable')}
        </button>
        <button type="button" class="primary" on:click={closeDetails}>{$t('common.close')}</button>
      </footer>
    </div>
  </Modal>
{/if}
