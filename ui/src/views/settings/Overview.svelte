<script>
  import { navigate } from '../../lib/router.js';
  import { status, health, memoryInfo, cronInfo, features } from '../../lib/stores.js';
  import Features from './Features.svelte';
  import { t } from '../../lib/preferences.js';
  import { Card, CardHeader, CardTitle, CardDescription, CardContent } from '$lib/components/ui/card/index.js';
  import { Badge } from '$lib/components/ui/badge/index.js';
  import { Server, Boxes, Settings2, Brain, Radio, ArrowRight } from '@lucide/svelte';

  const links = [
    { key: 'serve', title: 'settings.tabs.serve', desc: 'settings.overview.serve.desc', icon: Server },
    { key: 'providers', title: 'settings.tabs.providers', desc: 'settings.overview.providers.desc', icon: Boxes },
    { key: 'app', title: 'settings.tabs.app', desc: 'settings.overview.app.desc', icon: Settings2 },
    { key: 'memory', title: 'settings.tabs.memory', desc: 'settings.overview.memory.desc', icon: Brain },
    { key: 'channels', title: 'settings.tabs.channels', desc: 'settings.overview.channels.desc', icon: Radio }
  ];
</script>

<div class="overview-page">
  <div class="overview-grid">
    {#each links as link}
      <Card
        class="overview-card"
        role="button"
        tabindex="0"
        onclick={() => navigate(`/settings/${link.key}`)}
        onkeydown={(event) => {
          if (event.key === 'Enter' || event.key === ' ') {
            event.preventDefault();
            navigate(`/settings/${link.key}`);
          }
        }}
      >
        <CardContent class="overview-card-content">
          <span class="overview-card-icon">
            <svelte:component this={link.icon} size={18} aria-hidden="true" />
          </span>
          <div class="overview-card-text">
            <strong>{$t(link.title)}</strong>
            <span>{$t(link.desc)}</span>
          </div>
          <ArrowRight size={16} aria-hidden="true" />
        </CardContent>
      </Card>
    {/each}
  </div>

  <Card class="overview-status-card">
    <CardHeader>
      <CardTitle>{$t('settings.runtime.title')}</CardTitle>
      <CardDescription>{$t('settings.runtime.hint')}</CardDescription>
    </CardHeader>
    <CardContent>
      <dl class="overview-kv">
        <div class="overview-kv-item">
          <dt>{$t('settings.runtime.version')}</dt>
          <dd>{$health?.version || 'dev'}</dd>
        </div>
        <div class="overview-kv-item">
          <dt>{$t('settings.runtime.listen')}</dt>
          <dd>{$status?.listen || '—'}</dd>
        </div>
        <div class="overview-kv-item">
          <dt>{$t('settings.runtime.sessions')}</dt>
          <dd>{$status?.sessions ?? $health?.sessions ?? 0}</dd>
        </div>
        <div class="overview-kv-item">
          <dt>Cron</dt>
          <dd>
            {$cronInfo?.enabled === false
              ? $t('common.disabledState')
              : ($cronInfo?.running ? $t('common.running') : $t('common.idle'))}
          </dd>
        </div>
        <div class="overview-kv-item">
          <dt>Memory</dt>
          <dd>
            {$memoryInfo?.enabled === false
              ? $t('common.disabledState')
              : ($memoryInfo?.path || $t('common.uninitialized'))}
          </dd>
        </div>
        <div class="overview-kv-item">
          <dt>{$t('settings.runtime.api')}</dt>
          <dd>
            <Badge variant={$features.api ? 'success' : 'secondary'}>
              {$features.api ? $t('common.enabled') : $t('common.disabled')}
            </Badge>
          </dd>
        </div>
      </dl>
    </CardContent>
  </Card>

  <Features />
</div>
