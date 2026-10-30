<script>
  import { MessageSquareText, Activity } from '@lucide/svelte';
  import { t } from '../../lib/preferences.js';
  import { Button } from '$lib/components/ui/button';
  import SessionLogDownload from './SessionLogDownload.svelte';

  export let title = '';
  export let channelLabel = '';
  export let sessionID = '';
  export let workDir = '';
  export let view = 'chat';
  export let busy = false;
  export let onViewChange = () => {};

  function compactPath(path) {
    if (!path) return '';
    const normalized = String(path).replace(/[\\/]+$/, '');
    const parts = normalized.split(/[\\/]/).filter(Boolean);
    if (parts.length <= 2) return normalized || '/';
    return `.../${parts.slice(-2).join('/')}`;
  }
</script>

<header class="chat-session-header">
  <div class="chat-session-headline">
    <div class="chat-session-heading">
      <strong>{title || $t('chat.newSession')}</strong>
      {#if channelLabel}<span>{channelLabel}</span>{/if}
      {#if busy}<span class="chat-session-run-status"><span class="dot running"></span>{$t('common.running')}</span>{/if}
    </div>
  </div>
  {#if workDir}
    <div class="composer-session-info chat-session-workdir" title={workDir}>
      <span class="session-dir"><span class="session-dir-icon" aria-hidden="true">📁</span>{compactPath(workDir)}</span>
    </div>
  {/if}
  <div class="chat-session-utilities">
    <SessionLogDownload {sessionID} compact />
  </div>
  <nav class="chat-view-tabs" aria-label={$t('chat.session')}>
    <Button
      type="button"
      variant={view === 'chat' ? 'secondary' : 'ghost'}
      size="sm"
      class={view === 'chat' ? 'chat-view-tab active' : 'chat-view-tab'}
      onclick={() => onViewChange('chat')}
    >
      <MessageSquareText size={16} />
      <span>{$t('chat.view.chat')}</span>
    </Button>
    <Button
      type="button"
      variant={view === 'trajectory' ? 'secondary' : 'ghost'}
      size="sm"
      class={view === 'trajectory' ? 'chat-view-tab active' : 'chat-view-tab'}
      onclick={() => onViewChange('trajectory')}
    >
      <Activity size={16} />
      <span>{$t('chat.view.trajectory')}</span>
    </Button>
  </nav>
</header>
