<script>
  import { settings, setError, setNotice, clearBanners } from '../../lib/stores.js';
  import { putJSON } from '../../lib/api.js';
  import { t } from '../../lib/preferences.js';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Input } from '$lib/components/ui/input/index.js';
  import { Switch } from '$lib/components/ui/switch/index.js';
  import { Card, CardContent } from '$lib/components/ui/card/index.js';
  import { Plus, Save, Trash2 } from '@lucide/svelte';
  import SettingsField from './SettingsField.svelte';

  let form = { defaultMarket: 'yuandun', defaultInstallScope: 'project', officialHandles: [], markets: [] };
  let last = '';
  $: if ($settings !== last) { last = $settings; load($settings); }

  function load(raw) {
    try {
      const cfg = JSON.parse(raw || '{}');
      const hub = cfg.skillHub || {};
      form = {
        defaultMarket: hub.defaultMarket || 'yuandun',
        defaultInstallScope: hub.defaultInstallScope || 'project',
        officialHandles: hub.officialHandles || [],
        markets: hub.markets || []
      };
    } catch (e) {
      setError(e);
    }
  }

  function addMarket() {
    form.markets = [...form.markets, { id: '', name: '', siteURL: '', apiURL: '', enabled: true, apiToken: '' }];
  }

  function removeMarket(i) {
    form.markets = form.markets.filter((_, n) => n !== i);
  }

  async function save() {
    clearBanners();
    try {
      const cfg = JSON.parse($settings || '{}');
      cfg.skillHub = form;
      const saved = await putJSON('/api/settings', cfg);
      settings.set(JSON.stringify(saved, null, 2));
      setNotice($t('settings.skillhub.saved'));
    } catch (e) {
      setError(e);
    }
  }
</script>

<Card class="settings-card settings-save-card">
  <CardContent class="settings-save-content">
    <div class="settings-save-lead">
      <h2 class="settings-save-title">SkillHub</h2>
      <p class="settings-save-hint">{$t('settings.skillhub.hint')}</p>
    </div>
    <Button type="button" variant="outline" size="sm" onclick={save}>
      <Save size={14} aria-hidden="true" />
      <span>{$t('settings.skillhub.save')}</span>
    </Button>
  </CardContent>
</Card>

<Card class="settings-card">
  <CardContent>
  <div class="settings-form-grid">
    <SettingsField label={$t('settings.skillhub.defaultMarket')}>
      <Input bind:value={form.defaultMarket} />
    </SettingsField>
    <SettingsField label={$t('settings.skillhub.installScope')}>
      <select bind:value={form.defaultInstallScope} class="settings-select">
        <option value="project">{$t('settings.skillhub.project')}</option>
        <option value="global">{$t('settings.skillhub.global')}</option>
      </select>
    </SettingsField>
    <SettingsField label={$t('settings.skillhub.officialHandles')} className="full">
      <Input value={form.officialHandles.join(', ')} onchange={(e) => form.officialHandles = e.currentTarget.value.split(',').map((v) => v.trim()).filter(Boolean)} />
    </SettingsField>
  </div>

  <div class="markets-section">
    <div class="markets-head">
      <h3>{$t('settings.skillhub.markets')}</h3>
      <Button type="button" variant="outline" size="sm" onclick={addMarket}>
        <Plus size={14} aria-hidden="true" />
        <span>{$t('settings.skillhub.addMarket')}</span>
      </Button>
    </div>
    <div class="markets-grid">
      {#each form.markets as market, i}
        <Card class="market-card">
          <CardContent class="market-card-content">
            <div class="market-fields">
              <SettingsField label={$t('settings.skillhub.id')}>
                <Input bind:value={market.id} placeholder="yuandun" />
              </SettingsField>
              <SettingsField label={$t('settings.skillhub.name')}>
                <Input bind:value={market.name} />
              </SettingsField>
              <SettingsField label={$t('settings.skillhub.siteURL')}>
                <Input bind:value={market.siteURL} />
              </SettingsField>
              <SettingsField label={$t('settings.skillhub.apiURL')}>
                <Input bind:value={market.apiURL} />
              </SettingsField>
              <SettingsField label={$t('settings.skillhub.bearerToken')}>
                <Input type="password" bind:value={market.apiToken} />
              </SettingsField>
              <div class="market-enabled-row">
                <span class="settings-field-label">{$t('settings.skillhub.enabled')}</span>
                <Switch bind:checked={market.enabled} aria-label={$t('settings.skillhub.enabled')} />
              </div>
            </div>
            <div class="market-actions">
              <Button type="button" variant="ghost" size="icon-xs" onclick={() => removeMarket(i)} title={$t('common.remove')} aria-label={$t('common.remove')}>
                <Trash2 size={14} aria-hidden="true" />
              </Button>
            </div>
          </CardContent>
        </Card>
      {/each}
    </div>
  </div>

  </CardContent>
</Card>

<style>
  .markets-section { margin-top: 16px; }
  .markets-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 12px;
  }
  .markets-head h3 { margin: 0; font-size: 14px; font-weight: 600; color: var(--text); }
  .markets-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(320px, 1fr)); gap: 12px; }
  :global(.market-card) { border-radius: 8px; }
  :global(.market-card-content) { padding: 14px !important; }
  .market-fields { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }
  .market-fields > :global(.settings-field):nth-child(3),
  .market-fields > :global(.settings-field):nth-child(4),
  .market-fields > :global(.settings-field):nth-child(5) { grid-column: 1 / -1; }
  .market-enabled-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    grid-column: 1 / -1;
    min-height: 32px;
  }
  .market-actions {
    display: flex;
    justify-content: flex-end;
    margin-top: 12px;
    padding-top: 12px;
    border-top: 1px solid var(--border-subtle);
  }
  .settings-form-actions { margin-top: 16px; }
  @media (max-width: 640px) {
    .market-fields { grid-template-columns: 1fr; }
  }
</style>
