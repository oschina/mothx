<script>
  import { serveConfig, refreshAll, setError, setNotice, clearBanners } from '../../lib/stores.js';
  import { putJSON } from '../../lib/api.js';
  import { t } from '../../lib/preferences.js';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Input } from '$lib/components/ui/input/index.js';
  import { Switch } from '$lib/components/ui/switch/index.js';
  import { Card, CardContent } from '$lib/components/ui/card/index.js';
  import { Plus, Save, Trash2 } from '@lucide/svelte';
  import SettingsSection from './SettingsSection.svelte';
  import SettingsField from './SettingsField.svelte';

  let defaultWorkDir = '';
  let restrictWorkDirs = false;
  let allowedWorkDirs = [];

  $: parseConfig($serveConfig);

  function parseConfig(raw) {
    try {
      const cfg = JSON.parse(raw);
      defaultWorkDir = readDefaultWorkDir(cfg);
      const rawDirs = readAllowedWorkDirs(cfg);
      allowedWorkDirs = Array.isArray(rawDirs) ? rawDirs.map((dir) => String(dir ?? '')) : [];
      restrictWorkDirs = allowedWorkDirs.some((dir) => dir.trim());
    } catch {
      defaultWorkDir = '';
      restrictWorkDirs = false;
      allowedWorkDirs = [];
    }
  }

  function readDefaultWorkDir(cfg) {
    for (const source of [cfg, cfg?.api, cfg?.gateway]) {
      if (!source || typeof source !== 'object') continue;
      const value = source.defaultWorkDir || source.workDir || source.workingDir;
      if (value) return value;
    }
    return '';
  }

  function readAllowedWorkDirs(cfg) {
    for (const source of [cfg, cfg?.api, cfg?.gateway]) {
      if (!source || typeof source !== 'object') continue;
      if (Object.prototype.hasOwnProperty.call(source, 'allowedWorkDirs')) {
        return source.allowedWorkDirs;
      }
    }
    return undefined;
  }

  function addAllowed() {
    restrictWorkDirs = true;
    allowedWorkDirs = [...allowedWorkDirs, ''];
  }

  function removeAllowed(index) {
    allowedWorkDirs = allowedWorkDirs.filter((_, i) => i !== index);
  }

  async function save() {
    clearBanners();
    try {
      const cfg = JSON.parse($serveConfig);
      const nextDefaultWorkDir = defaultWorkDir.trim();
      if (nextDefaultWorkDir) cfg.defaultWorkDir = nextDefaultWorkDir;
      else delete cfg.defaultWorkDir;
      delete cfg.workDir;
      clearLegacyWorkDirFields(cfg.api);
      clearLegacyWorkDirFields(cfg.gateway);

      const filtered = allowedWorkDirs.map((d) => d.trim()).filter(Boolean);
      if (restrictWorkDirs && filtered.length > 0) cfg.allowedWorkDirs = filtered;
      else delete cfg.allowedWorkDirs;
      clearAllowedWorkDirs(cfg.api);
      clearAllowedWorkDirs(cfg.gateway);

      const saved = await putJSON('/api/serve/config', cfg);
      serveConfig.set(JSON.stringify(saved, null, 2));
      await refreshAll();
      setNotice($t('settings.workdir.saved'));
    } catch (err) {
      setError(err);
    }
  }

  function clearLegacyWorkDirFields(source) {
    if (!source || typeof source !== 'object') return;
    delete source.defaultWorkDir;
    delete source.workDir;
    delete source.workingDir;
  }

  function clearAllowedWorkDirs(source) {
    if (!source || typeof source !== 'object') return;
    delete source.allowedWorkDirs;
  }

  function handleKeydown(event) {
    if (event.key === 'Enter') {
      event.preventDefault();
      save();
    }
  }
</script>

<Card class="settings-card settings-save-card">
  <CardContent class="settings-save-content">
    <div class="settings-save-lead">
      <h2 class="settings-save-title">{$t('settings.workdir.title')}</h2>
      <p class="settings-save-hint">{$t('settings.workdir.mainHint')}</p>
    </div>
    <Button type="button" variant="outline" size="sm" onclick={save}>
      <Save size={14} aria-hidden="true" />
      <span>{$t('common.save')}</span>
    </Button>
  </CardContent>
</Card>

<SettingsSection title={$t('settings.workdir.default')} description={$t('settings.workdir.defaultHint')}>
  <div class="settings-form-grid">
    <SettingsField label="" className="full">
      <Input bind:value={defaultWorkDir} onkeydown={handleKeydown} placeholder="/home/user/projects" />
    </SettingsField>
  </div>
</SettingsSection>

<SettingsSection title={$t('settings.workdir.allowed')} description={$t('settings.workdir.allowedHint')}>
  <SettingsField label="" className="full">
    <label class="workdir-restrict-row">
      <span class="settings-switch-title">{$t('settings.workdir.restrict')}</span>
      <Switch bind:checked={restrictWorkDirs} aria-label={$t('settings.workdir.restrict')} />
    </label>
    <p class="settings-field-hint">{$t('settings.workdir.restrictHint')}</p>
  </SettingsField>

  {#if !restrictWorkDirs}
    <p class="settings-empty-hint">{$t('settings.workdir.noWhitelist')}</p>
  {:else if allowedWorkDirs.length === 0}
    <p class="settings-empty-hint">{$t('settings.workdir.denyAll')}</p>
  {:else}
    <ul class="workdir-list">
      {#each allowedWorkDirs as dir, i (i)}
        <li class="workdir-list-item">
          <Input bind:value={allowedWorkDirs[i]} placeholder="/home/user/projects" />
          <Button type="button" variant="ghost" size="icon-xs" onclick={() => removeAllowed(i)} title={$t('common.remove')} aria-label={$t('common.remove')}>
            <Trash2 size={14} aria-hidden="true" />
          </Button>
        </li>
      {/each}
    </ul>
  {/if}
  <p class="settings-field-hint">{$t('settings.workdir.arrayHint')}</p>
  <div class="settings-form-actions">
    <Button type="button" variant="outline" size="sm" onclick={addAllowed}>
      <Plus size={14} aria-hidden="true" />
      <span>{$t('common.add')}</span>
    </Button>
  </div>
</SettingsSection>

<style>
  .workdir-restrict-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    min-height: 32px;
  }
  .workdir-list {
    display: grid;
    gap: 8px;
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .workdir-list-item {
    display: grid;
    grid-template-columns: 1fr auto;
    gap: 8px;
    align-items: center;
  }
  .settings-empty-hint {
    margin: 0;
    padding: 12px;
    border-radius: 8px;
    background: var(--bg-secondary);
    color: var(--text-muted);
    font-size: 13px;
    line-height: 1.5;
  }
  .settings-form-actions {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-top: 12px;
  }
</style>
