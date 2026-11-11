<script>
  import { onMount } from 'svelte';
  import { t } from '../../lib/preferences.js';
  import { loadACPWorkerProfileCatalog, patchACPWorkerProfile } from '../../lib/stores.js';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Input } from '$lib/components/ui/input/index.js';
  import { Card, CardHeader, CardTitle, CardDescription, CardContent } from '$lib/components/ui/card/index.js';

  let profiles = [];
  let capabilityNames = [];
  let drafts = {};
  let loading = true;
  let busy = '';
  let message = '';
  let error = '';

  function draftFor(profile) {
    const overrides = {};
    for (const name of profile.capabilityOverrides || []) overrides[name] = true;
    return {
      model: profile.model || '',
      driver: profile.driver || 'generic-acp',
      mcpBridge: profile.mcpBridge || 'auto',
      overrides
    };
  }

  function apply(profilesNext, names) {
    profiles = profilesNext;
    if (names) capabilityNames = names;
    const next = {};
    for (const profile of profiles) next[profile.id] = draftFor(profile);
    drafts = next;
  }

  async function load() {
    loading = true;
    error = '';
    try {
      const catalog = await loadACPWorkerProfileCatalog();
      apply(catalog.profiles, catalog.capabilityNames);
    } catch (err) {
      error = String(err?.message || err);
    } finally {
      loading = false;
    }
  }

  function toggleOverride(id, name) {
    const draft = drafts[id];
    if (!draft) return;
    if (draft.overrides[name]) {
      delete draft.overrides[name];
    } else {
      draft.overrides[name] = true;
    }
    drafts = { ...drafts };
  }

  async function save(profile) {
    const draft = drafts[profile.id];
    if (!draft) return;
    busy = profile.id;
    message = '';
    error = '';
    const overrides = {};
    for (const name of Object.keys(draft.overrides)) overrides[name] = false;
    try {
      const profilesNext = await patchACPWorkerProfile(profile.id, {
        model: draft.model,
        driver: draft.driver,
        mcpBridge: draft.mcpBridge,
        capabilityOverrides: overrides
      });
      apply(profilesNext);
      message = $t('settings.acpWorkers.saved');
    } catch (err) {
      error = $t('settings.acpWorkers.saveFailed', { e: String(err?.message || err) });
    } finally {
      busy = '';
    }
  }

  onMount(load);
</script>

<div class="acp-workers-page">
  <p class="settings-page-hint">{$t('settings.acpWorkers.hint')}</p>
  {#if message}<p class="settings-page-message">{message}</p>{/if}
  {#if error}<p class="settings-page-error">{error}</p>{/if}
  {#if loading}
    <p class="settings-page-hint">{$t('settings.acpWorkers.loading')}</p>
  {:else if profiles.length === 0}
    <p class="settings-page-hint">{$t('settings.acpWorkers.empty')}</p>
  {:else}
    <div class="acp-workers-list">
      {#each profiles as profile (profile.id)}
        {@const draft = drafts[profile.id] || draftFor(profile)}
        <Card class="settings-card">
          <CardHeader>
            <CardTitle>{profile.title || profile.id}</CardTitle>
            <CardDescription>
              {profile.id} · {profile.transport}
              {#if profile.invalidReason}<span class="acp-worker-invalid"> {profile.invalidReason}</span>{/if}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <div class="settings-form-grid">
              <label class="settings-field">
                <span class="settings-field-label">{$t('settings.acpWorkers.model')}</span>
                <Input bind:value={draft.model} placeholder={$t('settings.acpWorkers.modelPlaceholder')} />
              </label>
              <label class="settings-field">
                <span class="settings-field-label">{$t('settings.acpWorkers.driver')}</span>
                <Input bind:value={draft.driver} placeholder="generic-acp" />
              </label>
              <label class="settings-field">
                <span class="settings-field-label">{$t('settings.acpWorkers.bridge')}</span>
                <select class="settings-select" bind:value={draft.mcpBridge}>
                  <option value="auto">auto</option>
                  <option value="off">off</option>
                </select>
              </label>
              <div class="full">
                <span class="settings-field-label">{$t('settings.acpWorkers.overrides')}</span>
                <div class="acp-worker-overrides">
                  {#each capabilityNames as name}
                    <label class="acp-worker-override">
                      <input
                        type="checkbox"
                        checked={Boolean(draft.overrides[name])}
                        on:change={() => toggleOverride(profile.id, name)}
                      />
                      <span>{name}</span>
                    </label>
                  {/each}
                </div>
              </div>
              <div class="full acp-worker-actions">
                <Button size="sm" disabled={busy === profile.id} onclick={() => save(profile)}>
                  {$t('settings.acpWorkers.save')}
                </Button>
              </div>
            </div>
          </CardContent>
        </Card>
      {/each}
    </div>
  {/if}
</div>
