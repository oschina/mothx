<script>
  import { onMount } from 'svelte';
  import { request, putJSON } from '../lib/api.js';
  import { setError, setNotice, clearBanners } from '../lib/stores.js';
  import { t } from '../lib/preferences.js';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Input } from '$lib/components/ui/input/index.js';
  import { Plus, Trash2 } from '@lucide/svelte';
  import SettingsSection from '../views/settings/SettingsSection.svelte';
  import SettingsField from '../views/settings/SettingsField.svelte';

  export let endpoint = '/api/mcp';
  export let title = '';
  export let hint = '';

  let servers = [];
  let loading = true;
  let saving = false;
  let dirty = false;

  const pair = () => ({ name: '', value: '' });
  const emptyServer = () => ({ name: '', type: 'stdio', command: '', url: '', messageUrl: '', args: [], headers: [], env: [] });
  const basicTemplate = () => ({ name: 'example-stdio', type: 'stdio', command: '/absolute/path/to/mcp-server', url: '', messageUrl: '', args: [], headers: [], env: [] });
  const fullTemplate = () => ([
    { name: 'local-stdio', type: 'stdio', command: '/absolute/path/to/mcp-server', url: '', messageUrl: '', args: ['--port', '8080'], headers: [], env: [{ name: 'API_KEY', value: 'replace-me' }] },
    { name: 'remote-http', type: 'http', command: '', url: 'https://mcp.example.com', messageUrl: '', args: [], headers: [{ name: 'Authorization', value: 'Bearer replace-me' }], env: [] },
    { name: 'legacy-sse', type: 'sse', command: '', url: 'https://legacy.example.com/sse', messageUrl: 'https://legacy.example.com/messages', args: [], headers: [{ name: 'Authorization', value: 'Bearer replace-me' }], env: [] }
  ]);

  onMount(load);

  function normalizeServer(server = {}) {
    return {
      name: server.name || '', type: server.type || 'stdio', command: server.command || '', url: server.url || '', messageUrl: server.messageUrl || '',
      args: Array.isArray(server.args) ? [...server.args] : [],
      headers: Array.isArray(server.headers) ? server.headers.map((item) => ({ name: item?.name || '', value: item?.value || '' })) : [],
      env: Array.isArray(server.env) ? server.env.map((item) => ({ name: item?.name || '', value: item?.value || '' })) : []
    };
  }

  async function load() {
    loading = true;
    try {
      const cfg = await request(endpoint);
      servers = Array.isArray(cfg?.mcpServers) ? cfg.mcpServers.map(normalizeServer) : [];
      dirty = false;
    } catch (err) {
      setError(err);
    } finally {
      loading = false;
    }
  }

  function markDirty() {
    dirty = true;
    clearBanners();
  }

  function addServer(server = emptyServer()) { servers = [...servers, server]; markDirty(); }
  function removeServer(index) { servers = servers.filter((_, i) => i !== index); markDirty(); }
  function useBasicTemplate() { servers = [basicTemplate()]; markDirty(); }
  function useFullTemplate() { servers = fullTemplate(); markDirty(); }
  function updateArg(server, index, value) { server.args[index] = value; servers = servers; markDirty(); }
  function addArg(server) { server.args = [...server.args, '']; servers = servers; markDirty(); }
  function removeArg(server, index) { server.args = server.args.filter((_, i) => i !== index); servers = servers; markDirty(); }
  function updatePair(server, field, index, key, value) { server[field][index][key] = value; servers = servers; markDirty(); }
  function addPair(server, field) { server[field] = [...server[field], pair()]; servers = servers; markDirty(); }
  function removePair(server, field, index) { server[field] = server[field].filter((_, i) => i !== index); servers = servers; markDirty(); }

  function updateServer(server, key, value) {
    server[key] = value;
    servers = servers;
    markDirty();
  }

  async function save() {
    if (servers.some((server) => !server.name.trim())) {
      setError(new Error($t('settings.mcp.nameRequired')));
      return;
    }
    saving = true;
    clearBanners();
    try {
      const saved = await putJSON(endpoint, { mcpServers: servers });
      servers = Array.isArray(saved?.mcpServers) ? saved.mcpServers.map(normalizeServer) : [];
      dirty = false;
      setNotice($t('settings.mcp.saved'));
    } catch (err) {
      setError(err);
    } finally {
      saving = false;
    }
  }
</script>

<SettingsSection {title} description={hint}>
  <div class="mcp-actions">
    <Button type="button" variant="outline" size="sm" onclick={useBasicTemplate} disabled={loading}>
      {$t('settings.mcp.basicTemplate')}
    </Button>
    <Button type="button" variant="outline" size="sm" onclick={useFullTemplate} disabled={loading}>
      {$t('settings.mcp.fullTemplate')}
    </Button>
    <Button type="button" variant="outline" size="sm" onclick={() => addServer()} disabled={loading}>
      <Plus size={14} aria-hidden="true" />
      <span>{$t('settings.mcp.addServer')}</span>
    </Button>
    <div class="mcp-save-wrap">
      <Button type="button" variant="outline" size="sm" onclick={save} disabled={loading || saving || !dirty}>
        {saving ? $t('common.saving') : $t('common.save')}
      </Button>
    </div>
  </div>

  {#if loading}
    <p class="mcp-loading">{$t('common.loading')}</p>
  {:else if servers.length === 0}
    <div class="mcp-empty">
      <strong>{$t('settings.mcp.empty')}</strong>
      <span>{$t('settings.mcp.emptyHint')}</span>
      <Button type="button" variant="outline" size="sm" onclick={() => addServer()}>
        <Plus size={14} aria-hidden="true" />
        <span>{$t('settings.mcp.addServer')}</span>
      </Button>
    </div>
  {:else}
    <div class="mcp-server-list">
      {#each servers as server, serverIndex (server)}
        <section class="mcp-server-card">
          <header class="mcp-server-head">
            <strong>{server.name || `${$t('settings.mcp.server')} ${serverIndex + 1}`}</strong>
            <Button type="button" variant="ghost" size="sm" onclick={() => removeServer(serverIndex)}>
              {$t('common.remove')}
            </Button>
          </header>
          <div class="mcp-fields">
            <SettingsField label={$t('settings.mcp.name')}>
              <Input value={server.name} oninput={(event) => updateServer(server, 'name', event.currentTarget.value)} placeholder="filesystem" />
            </SettingsField>
            <SettingsField label={$t('settings.mcp.transport')}>
              <select value={server.type} onchange={(event) => updateServer(server, 'type', event.currentTarget.value)} class="settings-select">
                <option value="stdio">stdio</option>
                <option value="http">HTTP</option>
                <option value="sse">SSE</option>
              </select>
            </SettingsField>
            {#if server.type === 'stdio'}
              <SettingsField label={$t('settings.mcp.command')} className="full">
                <Input value={server.command} oninput={(event) => updateServer(server, 'command', event.currentTarget.value)} placeholder="/absolute/path/to/mcp-server" />
              </SettingsField>
            {:else}
              <SettingsField label={$t('settings.mcp.url')} className="full">
                <Input value={server.url} oninput={(event) => updateServer(server, 'url', event.currentTarget.value)} placeholder="https://mcp.example.com" />
              </SettingsField>
              {#if server.type === 'sse'}
                <SettingsField label={$t('settings.mcp.messageUrl')} className="full">
                  <Input value={server.messageUrl} oninput={(event) => updateServer(server, 'messageUrl', event.currentTarget.value)} placeholder="https://mcp.example.com/messages" />
                </SettingsField>
              {/if}
            {/if}
          </div>

          {#if server.type === 'stdio'}
            <div class="mcp-list-section">
              <div class="mcp-list-head">
                <strong>{$t('settings.mcp.args')}</strong>
                <Button type="button" variant="ghost" size="sm" onclick={() => addArg(server)}>
                  <Plus size={14} aria-hidden="true" />
                  <span>{$t('common.add')}</span>
                </Button>
              </div>
              {#each server.args as arg, index}
                <div class="mcp-list-row">
                  <Input value={arg} oninput={(event) => updateArg(server, index, event.currentTarget.value)} placeholder="--argument" />
                  <Button type="button" variant="ghost" size="icon-xs" onclick={() => removeArg(server, index)} title={$t('common.remove')} aria-label={$t('common.remove')}>
                    <Trash2 size={14} aria-hidden="true" />
                  </Button>
                </div>
              {/each}
            </div>
          {/if}

          <div class="mcp-pairs">
            <div class="mcp-list-section">
              <div class="mcp-list-head">
                <strong>{$t('settings.mcp.headers')}</strong>
                <Button type="button" variant="ghost" size="sm" onclick={() => addPair(server, 'headers')}>
                  <Plus size={14} aria-hidden="true" />
                  <span>{$t('common.add')}</span>
                </Button>
              </div>
              {#each server.headers as item, index}
                <div class="mcp-pair-row">
                  <Input value={item.name} oninput={(event) => updatePair(server, 'headers', index, 'name', event.currentTarget.value)} placeholder={$t('settings.mcp.headerName')} />
                  <Input value={item.value} oninput={(event) => updatePair(server, 'headers', index, 'value', event.currentTarget.value)} placeholder={$t('settings.mcp.value')} />
                  <Button type="button" variant="ghost" size="icon-xs" onclick={() => removePair(server, 'headers', index)} title={$t('common.remove')} aria-label={$t('common.remove')}>
                    <Trash2 size={14} aria-hidden="true" />
                  </Button>
                </div>
              {/each}
            </div>
            {#if server.type === 'stdio'}
              <div class="mcp-list-section">
                <div class="mcp-list-head">
                  <strong>{$t('settings.mcp.env')}</strong>
                  <Button type="button" variant="ghost" size="sm" onclick={() => addPair(server, 'env')}>
                    <Plus size={14} aria-hidden="true" />
                    <span>{$t('common.add')}</span>
                  </Button>
                </div>
                {#each server.env as item, index}
                  <div class="mcp-pair-row">
                    <Input value={item.name} oninput={(event) => updatePair(server, 'env', index, 'name', event.currentTarget.value)} placeholder="API_KEY" />
                    <Input value={item.value} oninput={(event) => updatePair(server, 'env', index, 'value', event.currentTarget.value)} placeholder={$t('settings.mcp.value')} />
                    <Button type="button" variant="ghost" size="icon-xs" onclick={() => removePair(server, 'env', index)} title={$t('common.remove')} aria-label={$t('common.remove')}>
                      <Trash2 size={14} aria-hidden="true" />
                    </Button>
                  </div>
                {/each}
              </div>
            {/if}
          </div>
        </section>
      {/each}
    </div>
  {/if}
  <p class="mcp-note">{$t('settings.mcp.applyHint')}</p>
</SettingsSection>

<style>
  .mcp-actions { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; }
  .mcp-save-wrap { margin-left: auto; }
  .mcp-loading { padding: 40px 18px; color: var(--text-muted); font-size: 13px; }
  .mcp-empty {
    display: grid;
    justify-items: center;
    gap: 10px;
    margin-bottom: 4px;
    padding: 30px 18px;
    border: 1px dashed var(--border);
    border-radius: 10px;
    background: var(--bg-secondary);
    box-shadow: none;
    color: var(--text-muted);
    font-size: 13px;
  }
  .mcp-empty strong { color: var(--text); font-size: 13px; }
  .mcp-empty span { max-width: 480px; line-height: 1.55; text-align: center; }
  .mcp-server-list { display: grid; gap: 12px; }
  .mcp-server-card { padding: 14px; border: 1px solid var(--border-subtle); border-radius: 8px; background: var(--bg-secondary); }
  .mcp-server-head { display: flex; align-items: center; justify-content: space-between; gap: 10px; margin-bottom: 12px; }
  .mcp-server-head strong { font-size: 13px; }
  .mcp-fields { display: grid; grid-template-columns: minmax(160px, 1fr) minmax(130px, .45fr); gap: 12px; }
  .mcp-fields > :global(.settings-field.full) { grid-column: 1 / -1; }
  .mcp-list-section { display: grid; gap: 8px; margin-top: 12px; }
  .mcp-list-head { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
  .mcp-list-head strong { color: var(--text-secondary); font-size: 12px; }
  .mcp-list-row, .mcp-pair-row { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 8px; align-items: center; }
  .mcp-pair-row { grid-template-columns: minmax(0, .7fr) minmax(0, 1.3fr) auto; }
  .mcp-pairs { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; }
  .mcp-note { margin: 14px 0 0; color: var(--text-muted); font-size: 12px; line-height: 1.55; }
  @media (max-width: 640px) {
    .mcp-fields, .mcp-pairs { grid-template-columns: 1fr; }
    .mcp-pair-row { grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) auto; }
  }
</style>
