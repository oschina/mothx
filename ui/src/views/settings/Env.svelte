<script>
  import { onMount } from 'svelte';
  import { request, patchJSON } from '../../lib/api.js';
  import { setError, setNotice, clearBanners } from '../../lib/stores.js';
  import { t } from '../../lib/preferences.js';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Input } from '$lib/components/ui/input/index.js';
  import { Plus, Save, Trash2 } from '@lucide/svelte';
  import { Badge } from '$lib/components/ui/badge/index.js';
  import { Card, CardContent } from '$lib/components/ui/card/index.js';
  import SettingsField from './SettingsField.svelte';

  let variables = [];
  let drafts = {};
  let newKey = '';
  let newValue = '';
  let loading = true;
  let saving = false;
  let dirty = false;

  $: names = variables.map((v) => v.name).sort();

  onMount(async () => {
    try {
      const result = await request('/api/env');
      variables = result?.variables || [];
      drafts = {};
    } catch (err) {
      setError(err);
    } finally {
      loading = false;
    }
  });

  function markDirty() {
    dirty = true;
    clearBanners();
  }

  function addVariable() {
    const key = newKey.trim();
    if (!key) return;
    if (names.includes(key)) {
      setError(new Error($t('settings.env.duplicate')));
      return;
    }
    variables = [...variables, { name: key, valueConfigured: true }];
    drafts = { ...drafts, [key]: { value: newValue, isNew: true, touched: true, deleted: false } };
    newKey = '';
    newValue = '';
    markDirty();
  }

  function updateValue(key, value) {
    drafts = { ...drafts, [key]: { ...(drafts[key] || {}), value, touched: true, deleted: false } };
    markDirty();
  }

  function removeVariable(key) {
    drafts = { ...drafts, [key]: { ...(drafts[key] || {}), deleted: true, value: '' } };
    markDirty();
  }

  async function save() {
    saving = true;
    clearBanners();
    try {
      const set = [];
      const unset = [];
      for (const name of names) {
        const draft = drafts[name] || {};
        if (draft.deleted) {
          unset.push(name);
          continue;
        }
        if (draft.touched || draft.isNew) {
          set.push({ name, value: draft.value || '' });
        }
      }
      const result = await patchJSON('/api/env', { set, unset });
      variables = result?.variables || [];
      drafts = {};
      dirty = false;
      setNotice($t('settings.env.saved'));
    } catch (err) {
      setError(err);
    } finally {
      saving = false;
    }
  }

  function handleNewKey(event) {
    if (event.key === 'Enter') {
      event.preventDefault();
      addVariable();
    }
  }
</script>

<Card class="settings-card settings-save-card">
  <CardContent class="settings-save-content">
    <div class="settings-save-lead">
      <h2 class="settings-save-title">{$t('settings.env.title')}</h2>
      <p class="settings-save-hint">{$t('settings.env.hint')}</p>
    </div>
    <Button type="button" variant="outline" size="sm" onclick={save} disabled={loading || saving || !dirty}>
      <Save size={14} aria-hidden="true" />
      <span>{saving ? $t('common.saving') : $t('common.save')}</span>
    </Button>
  </CardContent>
</Card>

<Card class="settings-card">
  <CardContent>
  <div class="env-heading">
    <div>
      <h2 class="settings-save-title">{$t('settings.env.variables')}</h2>
      <p class="settings-save-hint">{$t('settings.env.editHint')}</p>
    </div>
    <Badge variant={dirty ? 'warning' : 'secondary'}>
      {names.length} {$t('settings.env.count')}
    </Badge>
  </div>

  {#if loading}
    <p class="settings-empty-hint">{$t('common.loading')}</p>
  {:else}
    <div class="env-add-section">
      <div class="settings-form-grid env-add-grid">
        <SettingsField label={$t('settings.env.name')}>
          <Input bind:value={newKey} onkeydown={handleNewKey} placeholder="MY_VARIABLE" autocomplete="off" />
        </SettingsField>
        <SettingsField label={$t('settings.env.value')}>
          <Input bind:value={newValue} onkeydown={handleNewKey} type="password" placeholder={$t('settings.env.value')} autocomplete="off" />
        </SettingsField>
      </div>
      <div class="settings-form-actions">
        <Button type="button" variant="outline" size="sm" onclick={addVariable} disabled={!newKey.trim()}>
          <Plus size={14} aria-hidden="true" />
          <span>{$t('common.add')}</span>
        </Button>
      </div>
    </div>

    {#if names.length === 0}
      <div class="env-empty">
        <strong>{$t('settings.env.empty')}</strong>
        <span>{$t('settings.env.emptyHint')}</span>
      </div>
    {:else}
      <ul class="env-list">
        {#each names as key (key)}
          {@const draft = drafts[key] || {}}
          {#if !draft.deleted}
            <li class="env-row">
              <code class="env-key">{key}</code>
              <Input
                class="env-value"
                type="password"
                value={draft.value || ''}
                oninput={(event) => updateValue(key, event.currentTarget.value)}
                placeholder={$t('settings.env.valueConfigured')}
                aria-label={`${$t('settings.env.value')}: ${key}`}
              />
              <Button
                type="button"
                variant="ghost"
                size="icon-xs"
                onclick={() => removeVariable(key)}
                title={$t('common.remove')}
                aria-label={$t('common.remove')}
              >
                <Trash2 size={14} aria-hidden="true" />
              </Button>
            </li>
          {/if}
        {/each}
      </ul>
    {/if}
  {/if}

  </CardContent>
</Card>

<style>
  .env-heading {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 12px;
  }
  .env-heading h2 { margin: 0; }
  .env-add-section {
    padding: 12px;
    border-radius: 8px;
    background: var(--bg-secondary);
    margin-bottom: 12px;
  }
  .env-add-grid { margin-bottom: 12px; }
  .env-empty {
    display: grid;
    justify-items: center;
    gap: 6px;
    padding: 36px 20px;
    color: var(--text-muted);
    text-align: center;
  }
  .env-empty strong { color: var(--text-secondary); font-size: 13px; }
  .env-empty span { font-size: 12px; }
  .env-list {
    display: grid;
    list-style: none;
    margin: 0;
    padding: 0;
    border: 1px solid var(--border-subtle);
    border-radius: 8px;
    overflow: hidden;
  }
  .env-row {
    display: grid;
    grid-template-columns: minmax(160px, .8fr) minmax(220px, 1.2fr) auto;
    gap: 12px;
    align-items: center;
    padding: 10px 14px;
    border-bottom: 1px solid var(--border-subtle);
  }
  .env-row:last-child { border-bottom: 0; }
  .env-key {
    color: var(--accent-text);
    font: 600 12px var(--font-mono);
    overflow-wrap: anywhere;
  }
  :global(.env-value) { font-family: var(--font-mono); font-size: 12px; }
  .env-save-actions { margin-top: 16px; }
  .settings-empty-hint {
    margin: 0;
    padding: 12px;
    border-radius: 8px;
    background: var(--bg-secondary);
    color: var(--text-muted);
    font-size: 13px;
  }
  @media (max-width: 640px) {
    .env-row { grid-template-columns: 1fr; gap: 8px; }
  }
</style>
