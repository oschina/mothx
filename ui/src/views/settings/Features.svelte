<script>
  import { features } from '../../lib/stores.js';
  import { t } from '../../lib/preferences.js';
  import { Card, CardHeader, CardTitle, CardDescription, CardContent } from '$lib/components/ui/card/index.js';
  import { Badge } from '$lib/components/ui/badge/index.js';

  const rows = [
    { key: 'api', label: 'settings.features.api.label', desc: 'settings.features.api.desc' },
    { key: 'webUI', label: 'settings.features.webUI.label', desc: 'settings.features.webUI.desc' },
    { key: 'cron', label: 'settings.features.cron.label', desc: 'settings.features.cron.desc' },
    { key: 'memory', label: 'settings.features.memory.label', desc: 'settings.features.memory.desc' },
    { key: 'multiAgent', label: 'settings.features.multiAgent.label', desc: 'settings.features.multiAgent.desc' }
  ];
</script>

<Card class="overview-features-card">
  <CardHeader>
    <CardTitle>{$t('settings.tabs.features')}</CardTitle>
    <CardDescription>{$t('settings.features.hint')}</CardDescription>
  </CardHeader>
  <CardContent>
    <table class="overview-features-table">
      <thead>
        <tr>
          <th>{$t('settings.features.feature')}</th>
          <th>{$t('settings.features.status')}</th>
          <th class="wide">{$t('settings.features.description')}</th>
        </tr>
      </thead>
      <tbody>
        {#each rows as row}
          <tr>
            <td>{$t(row.label)}</td>
            <td>
              <Badge variant={$features[row.key] ? 'success' : 'secondary'}>
                {$features[row.key] ? $t('common.enabled') : $t('common.disabled')}
              </Badge>
            </td>
            <td class="wide hint">{$t(row.desc)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </CardContent>
</Card>
