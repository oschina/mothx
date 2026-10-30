<script>
  import { logs, logsConnected, connectLogs, disconnectLogs, refreshAll } from '../../lib/stores.js';
  import { formatTime, formatLogMessage } from '../../lib/format.js';
  import { t } from '../../lib/preferences.js';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Input } from '$lib/components/ui/input/index.js';
  import { Badge } from '$lib/components/ui/badge/index.js';
  import { RefreshCw, X, Trash2, Radio } from '@lucide/svelte';

  let filter = '';
  $: filtered = filterLogs($logs, filter).slice(-500).reverse();

  function isUdpLog(item) {
    const text = formatLogMessage(item);
    return text.toLowerCase().startsWith('[udp]');
  }

  function logTypeLabel(item) {
    return isUdpLog(item) ? 'UDP' : (item.type || '');
  }

  function filterLogs(list, term) {
    const t = term.trim().toLowerCase();
    if (!t) return list;
    return list.filter((item) =>
      `${logTypeLabel(item)} ${formatLogMessage(item)}`.toLowerCase().includes(t)
    );
  }

  function clearLogs() {
    logs.set([]);
  }
</script>

<div class="logs-page">
  <div class="logs-toolbar">
    <Input class="logs-filter" bind:value={filter} placeholder={$t('logs.filter')} />
    <Badge variant={$logsConnected ? 'success' : 'secondary'}>
      {$logsConnected ? $t('common.connected') : $t('common.disconnected')}
    </Badge>
    {#if $logsConnected}
      <Button type="button" variant="outline" size="sm" onclick={disconnectLogs}>
        <X size={14} aria-hidden="true" />
        <span>{$t('topbar.closeLogs')}</span>
      </Button>
    {:else}
      <Button type="button" variant="outline" size="sm" onclick={connectLogs}>
        <Radio size={14} aria-hidden="true" />
        <span>{$t('topbar.openLogs')}</span>
      </Button>
    {/if}
    <Button type="button" variant="outline" size="sm" onclick={refreshAll}>
      <RefreshCw size={14} aria-hidden="true" />
      <span>{$t('common.refresh')}</span>
    </Button>
    <Button type="button" variant="outline" size="sm" onclick={clearLogs}>
      <Trash2 size={14} aria-hidden="true" />
      <span>{$t('common.clear')}</span>
    </Button>
  </div>

  <div class="logs-frame">
    <div class="log-list">
      <div class="log-line log-head">
        <span class="ts">{$t('logs.time')}</span>
        <span class="type">{$t('logs.type')}</span>
        <span>{$t('logs.message')}</span>
      </div>
      {#each filtered as item, idx (idx)}
        <div class="log-line">
          <span class="ts">{formatTime(item.timestamp)}</span>
          {#if isUdpLog(item)}
            <Badge class="type udp-badge" variant="secondary" title={$t('logs.udpTraffic')} aria-label={$t('logs.udpTraffic')}>UDP</Badge>
          {:else}
            <strong class="type">{item.type}</strong>
          {/if}
          <code>{formatLogMessage(item)}</code>
        </div>
      {/each}
      {#if filtered.length === 0}
        <p class="logs-empty">{$t('logs.empty')}</p>
      {/if}
    </div>
  </div>
</div>

<style>
  .logs-page { display: grid; gap: 12px; }
  .logs-toolbar {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }
  :global(.logs-filter) {
    flex: 1 1 240px;
    min-width: 200px;
    max-width: 360px;
  }
  .logs-frame {
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--bg);
    overflow: hidden;
  }
  .log-list {
    height: 60vh;
    min-height: 320px;
    overflow: auto;
    padding: 12px;
    font-family: var(--font-mono);
    font-size: 12px;
    line-height: 1.5;
  }
  .log-line { display: grid; grid-template-columns: 80px 110px 1fr; gap: 12px; padding: 2px 0; }
  .log-line .ts { color: var(--text-muted); white-space: nowrap; }
  .log-line .type { color: var(--accent-text); white-space: nowrap; }
  .log-line :global(.udp-badge) { color: var(--info-text); }
  .log-line code { color: var(--text); min-width: 0; overflow-wrap: anywhere; }
  .logs-empty {
    display: flex;
    align-items: center;
    justify-content: center;
    height: 100%;
    color: var(--text-muted);
    font-size: 13px;
    font-family: var(--font-base);
  }
  @media (max-width: 640px) {
    .log-line { grid-template-columns: 70px 80px 1fr; gap: 8px; }
  }
</style>
