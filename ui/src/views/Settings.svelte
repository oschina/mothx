<script>
  import { route, navigate } from '../lib/router.js';
  import { isMobile } from '../lib/stores.js';
  import { t } from '../lib/preferences.js';
  import { Button } from '$lib/components/ui/button/index.js';
  import SettingsOverview from './settings/Overview.svelte';
  import SettingsServe from './settings/ServeConfig.svelte';
  import SettingsApp from './settings/AppSettings.svelte';
  import SettingsProviders from './settings/ProviderSettings.svelte';
  import SettingsMemory from './settings/Memory.svelte';
  import SettingsWorkDir from './settings/WorkDir.svelte';
  import SettingsChannels from './settings/Channels.svelte';
  import SettingsLogs from './settings/Logs.svelte';
  import SettingsSkillHub from './settings/SkillHub.svelte';
  import SettingsEnv from './settings/Env.svelte';
  import SettingsMCP from './settings/MCP.svelte';
  import {
    LayoutGrid,
    Server,
    FolderOpen,
    Boxes,
    Settings2,
    Terminal,
    Puzzle,
    Brain,
    Radio,
    FileText,
    Sparkles
  } from '@lucide/svelte';

  const items = [
    { key: '', label: 'settings.tabs.overview', icon: LayoutGrid },
    { key: 'serve', label: 'settings.tabs.serve', icon: Server },
    { key: 'workdir', label: 'settings.tabs.workdir', icon: FolderOpen },
    { key: 'providers', label: 'settings.tabs.providers', icon: Boxes },
    { key: 'app', label: 'settings.tabs.app', icon: Settings2 },
    { key: 'env', label: 'settings.tabs.env', icon: Terminal },
    { key: 'mcp', label: 'settings.tabs.mcp', icon: Puzzle },
    { key: 'memory', label: 'settings.tabs.memory', icon: Brain },
    { key: 'channels', label: 'settings.tabs.channels', icon: Radio },
    { key: 'logs', label: 'settings.tabs.logs', icon: FileText },
    { key: 'skillhub', label: 'SkillHub', icon: Sparkles }
  ];

  $: activeKey = $route.sub || '';

  function open(key) {
    navigate(key ? `/settings/${key}` : '/settings');
  }
</script>

<section class="page settings-page">
  <div class="settings-layout">
    {#if $isMobile}
      <div class="settings-mobile-nav">
        <label class="settings-mobile-select">
          <span class="sr-only">{$t('nav.settings')}</span>
          <select value={activeKey} on:change={(event) => open(event.currentTarget.value)}>
            {#each items as item}
              <option value={item.key}>{$t(item.label)}</option>
            {/each}
          </select>
        </label>
      </div>
    {:else}
      <nav class="settings-nav" aria-label={$t('nav.settings')}>
        {#each items as item}
          <Button
            variant={activeKey === item.key ? 'secondary' : 'ghost'}
            size="sm"
            class={`settings-nav-item${activeKey === item.key ? ' active' : ''}`}
            onclick={() => open(item.key)}
          >
            <svelte:component this={item.icon} size={16} aria-hidden="true" />
            <span>{$t(item.label)}</span>
          </Button>
        {/each}
      </nav>
    {/if}

    <div class="settings-body">
      {#if activeKey === ''}
        <SettingsOverview />
      {:else if activeKey === 'serve'}
        <SettingsServe />
      {:else if activeKey === 'workdir'}
        <SettingsWorkDir />
      {:else if activeKey === 'providers'}
        <SettingsProviders />
      {:else if activeKey === 'app'}
        <SettingsApp />
      {:else if activeKey === 'env'}
        <SettingsEnv />
      {:else if activeKey === 'mcp'}
        <SettingsMCP />
      {:else if activeKey === 'memory'}
        <SettingsMemory />
      {:else if activeKey === 'channels'}
        <SettingsChannels />
      {:else if activeKey === 'logs'}
        <SettingsLogs />
      {:else if activeKey === 'skillhub'}
        <SettingsSkillHub />
      {:else}
        <p class="empty">{$t('settings.unknown')}</p>
      {/if}
    </div>
  </div>
</section>
