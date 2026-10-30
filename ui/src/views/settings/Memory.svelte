<script>
  import { memory, memoryInfo, setError, setNotice, clearBanners } from '../../lib/stores.js';
  import { putJSON } from '../../lib/api.js';
  import { t } from '../../lib/preferences.js';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Card, CardContent } from '$lib/components/ui/card/index.js';
  import { Badge } from '$lib/components/ui/badge/index.js';
  import { Save } from '@lucide/svelte';

  $: disabled = $memoryInfo?.enabled === false;
  $: memoryPath = $memoryInfo?.path || $t('settings.memory.notInitialized');

  async function save() {
    clearBanners();
    try {
      const saved = await putJSON('/api/memory', { content: $memory });
      memoryInfo.set(saved);
      memory.set(saved?.content || '');
      setNotice($t('settings.memory.saved'));
    } catch (err) {
      setError(err);
    }
  }
</script>

<Card class="settings-card settings-save-card">
  <CardContent class="settings-save-content">
    <div class="settings-save-lead">
      <h2 class="settings-save-title">Memory</h2>
      <p class="settings-save-hint">{memoryPath}</p>
    </div>
    <Button type="button" variant="outline" size="sm" onclick={save} disabled={disabled}>
      <Save size={14} aria-hidden="true" />
      <span>{$t('common.save')}</span>
    </Button>
  </CardContent>
</Card>

<Card class="settings-card">
  <CardContent>
    <div class="memory-head">
      <div class="memory-head-copy">
        <h3>{$t('settings.memory.editor')}</h3>
        <p>{$t('settings.memory.editorHint')}</p>
      </div>
      <Badge variant={disabled ? 'secondary' : 'success'}>
        {disabled ? $t('common.disabledState') : $t('common.enabled')}
      </Badge>
    </div>
    <label class="memory-editor-label" for="memory-editor">{$t('settings.memory.content')}</label>
    <textarea
      id="memory-editor"
      class="settings-textarea memory-textarea"
      bind:value={$memory}
      disabled={disabled}
      spellcheck="false"
      placeholder={$t('settings.memory.placeholder')}
    ></textarea>
  </CardContent>
</Card>

<style>
  .memory-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 14px;
  }
  .memory-head-copy { min-width: 0; }
  .memory-head h3 { margin: 0 0 3px; font-size: 14px; font-weight: 600; color: var(--text); }
  .memory-head p { margin: 0; font-size: 12px; line-height: 1.5; color: var(--text-muted); }
  .memory-editor-label {
    display: block;
    margin-bottom: 6px;
    color: var(--text-secondary);
    font-size: 12px;
    font-weight: 500;
  }
  .memory-textarea {
    box-sizing: border-box;
    width: 100%;
    min-height: 260px;
    max-height: 60vh;
    font-family: var(--font-mono);
    font-size: 13px;
    line-height: 1.6;
  }
</style>
