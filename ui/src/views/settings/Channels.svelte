<script>
  import { onMount, onDestroy } from 'svelte';
  import { channels, sessions, sessionBindings, serveConfig, refreshAll, setError, setNotice, clearBanners } from '../../lib/stores.js';
  import { del, postJSON, putJSON, patchJSON, request } from '../../lib/api.js';
  import { canRetryDelivery, deliveryFailureLabel, isDeliveryFailureTransient, listDeliveryFailures, retryDelivery } from '../../lib/deliveries.js';
  import { t } from '../../lib/preferences.js';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Input } from '$lib/components/ui/input/index.js';
  import { Switch } from '$lib/components/ui/switch/index.js';
  import { Badge } from '$lib/components/ui/badge/index.js';
  import { Card, CardContent } from '$lib/components/ui/card/index.js';
  import { Save, Settings2, ScanLine, Trash2, X, RefreshCw, Ban } from '@lucide/svelte';
  import Modal from '../../components/Modal.svelte';
  import SettingsSection from './SettingsSection.svelte';
  import SettingsField from './SettingsField.svelte';

  let form = defaultForm();
  let lastRaw = '';
  let parseError = '';
  // Runtime-owned delivery failures: null while loading, [] when nothing needs
  // attention. Only a failed transport-level operation is offered a retry.
  let deliveries = null;
  let deliveriesError = '';
  let deliveryRetrying = '';
  let saving = false;
  let feishuOpen = false;
  let feishuDraft = defaultForm().feishu;
  let wechatOpen = false;
  let wechatLogin = null;
  let wechatPoll = null;
  let bindingPoll = null;
  let channelBindings = [];
  let selectedBinding = { wechat: '', feishu: '' };
  let selectedIdentity = { wechat: '', feishu: '' };
  let channelTools = { wechat: [], feishu: [] };
  let toolAppliesTo = { wechat: 'next_run', feishu: 'next_run' };
  let toolCatalog = { wechat: [], feishu: [] };
  let toolCatalogReady = { wechat: false, feishu: false };
  let toolSaving = { wechat: false, feishu: false };
  let toolCatalogGeneration = { wechat: 0, feishu: 0 };
  let toolStateGeneration = { wechat: 0, feishu: 0 };
  let configRequestGeneration = 0;

  $: syncFromStore($serveConfig);

  // Keep the selectors in sync when a channel message creates or changes a
  // binding after this view has mounted. The global store is refreshed by the
  // session/channel runtime, while channelBindings remains the local fallback
  // for older runtimes that do not expose the binding-list endpoint.
  $: if (Array.isArray($sessionBindings) && $sessionBindings !== channelBindings) {
    channelBindings = $sessionBindings;
    syncSelectedBindingState();
  }

  onDestroy(() => {
    stopWechatPolling();
    if (bindingPoll) clearInterval(bindingPoll);
  });

  async function loadToolCatalog(platform) {
    const generation = ++toolCatalogGeneration[platform];
    toolCatalogReady[platform] = false;
    toolCatalogReady = toolCatalogReady;
    try {
      const data = await request(`/api/session-tools/catalog?platform=${platform}`);
      if (generation !== toolCatalogGeneration[platform]) return;
      toolCatalog[platform] = data?.tools || [];
      toolCatalog = toolCatalog;
      toolCatalogReady[platform] = true;
      toolCatalogReady = toolCatalogReady;
    } catch (err) {
      if (generation !== toolCatalogGeneration[platform]) return;
      toolCatalog[platform] = [];
      toolCatalog = toolCatalog;
      setError(err);
    }
  }

  async function refreshChannelBindings() {
    let bindingError = null;
    let nextBindings = [];
    try {
      const data = await request('/api/session-bindings');
      nextBindings = data?.bindings || [];
    } catch (err) {
      bindingError = err;
    }

    // Older serve runtimes may not expose the dedicated binding list yet;
    // session rows still carry the same channel metadata, so use them as a
    // compatibility fallback instead of rendering empty selectors.
    if (nextBindings.length === 0) {
      let sessionRows = $sessions || [];
      try {
        const sessionData = await request('/api/sessions?limit=1000');
        sessionRows = sessionData?.sessions || sessionRows;
      } catch (err) {
        if (!bindingError) bindingError = err;
      }
      nextBindings = sessionRows
        .filter((item) => (item.channelType === 'wechat' || item.channelType === 'feishu') && item.channelId)
        .map((item) => ({
          sessionId: item.id,
          channelType: item.channelType,
          channelId: item.channelId
        }));
    }

    const changed = JSON.stringify(nextBindings) !== JSON.stringify(channelBindings);
    channelBindings = nextBindings;
    if (changed) {
      sessionBindings.set(channelBindings);
      syncSelectedBindingState();
    }
    if (bindingError && channelBindings.length === 0) {
      setError(bindingError);
    }
    return changed;
  }

  async function loadChannelBindings() {
    await refreshChannelBindings();

    // Keep binding/session selection usable even if a tool catalog or tool
    // settings request is temporarily unavailable.
    await Promise.all([
      loadToolCatalog('wechat'),
      loadToolCatalog('feishu'),
      loadSelectedChannelTools('wechat'),
      loadSelectedChannelTools('feishu')
    ]);
  }

  async function loadSelectedChannelTools(platform) {
    const generation = ++toolStateGeneration[platform];
    const sessionID = selectedBinding[platform];
    try {
      const data = sessionID
        ? await request(`/api/sessions/${encodeURIComponent(sessionID)}/channel-tools`)
        : { tools: [], appliesTo: 'next_run' };
      if (generation !== toolStateGeneration[platform] || sessionID !== selectedBinding[platform]) return;
      channelTools[platform] = data?.tools || [];
      toolAppliesTo[platform] = data?.appliesTo || 'next_run';
      channelTools = channelTools;
      toolAppliesTo = toolAppliesTo;
    } catch (err) {
      if (generation !== toolStateGeneration[platform]) return;
      channelTools[platform] = [];
      channelTools = channelTools;
      setError(err);
    }
  }

  onMount(() => {
    loadChannelBindings();
    loadDeliveries();
    // Channel sessions are created when a Feishu/WeChat message first arrives,
    // and that path does not emit a binding_changed management event. Poll the
    // small binding endpoint while this settings view is open so a new identity
    // appears without requiring a full page reload.
    bindingPoll = setInterval(() => {
      refreshChannelBindings().catch(() => {});
    }, 3000);
  });

  async function loadDeliveries() {
    deliveriesError = '';
    try {
      // Bound channel sessions own their deliveries; while nothing is selected the
      // endpoint reports every session, which is the honest fallback.
      deliveries = await listDeliveryFailures(selectedBinding.wechat || selectedBinding.feishu || '');
    } catch (err) {
      deliveries = [];
      deliveriesError = err instanceof Error ? err.message : String(err);
    }
  }

  async function retryDeliveryOperation(operationId) {
    clearBanners();
    deliveryRetrying = operationId;
    try {
      const reopened = await retryDelivery(operationId);
      setNotice($t(reopened ? 'settings.channels.deliveriesRetried' : 'settings.channels.deliveriesNotRetried'));
      await loadDeliveries();
    } catch (err) {
      setError(err);
    } finally {
      deliveryRetrying = '';
    }
  }

  async function saveChannelTools(platform) {
    const sessionID = resolveSelectedBinding(platform);
    if (!sessionID) {
      setError($t('settings.channels.toolSessionRequired'));
      return;
    }
    if (!toolCatalogReady[platform]) {
      setError($t('settings.channels.toolsLoading'));
      return;
    }
    const configured = new Map(channelTools[platform].map((item) => [item.name || item.toolName, item.requestedEnabled ?? item.enabled]));
    const tools = toolCatalog[platform].map((tool) => ({
      name: tool.name,
      enabled: toolAvailable(tool) && (configured.has(tool.name) ? configured.get(tool.name) : tool.default !== false)
    }));
    const generation = ++toolStateGeneration[platform];
    toolSaving[platform] = true;
    toolSaving = toolSaving;
    try {
      const saved = await putJSON(`/api/sessions/${encodeURIComponent(sessionID)}/channel-tools`, { tools });
      if (generation !== toolStateGeneration[platform] || sessionID !== selectedBinding[platform]) return;
      channelTools[platform] = saved?.tools || tools;
      toolAppliesTo[platform] = saved?.appliesTo || 'next_run';
      channelTools = channelTools;
      toolAppliesTo = toolAppliesTo;
      await loadSelectedChannelTools(platform);
      setNotice($t('settings.channels.saved'));
    } catch (err) {
      if (generation === toolStateGeneration[platform]) setError(err);
    } finally {
      toolSaving[platform] = false;
      toolSaving = toolSaving;
    }
  }

  function syncSelectedBindingState() {
    const nextIdentity = { ...selectedIdentity };
    const nextBinding = { ...selectedBinding };
    for (const platform of ['wechat', 'feishu']) {
      const bindings = channelBindings.filter((item) => item.channelType === platform);
      if (!bindings.some((item) => item.channelId === nextIdentity[platform])) {
        nextIdentity[platform] = bindings[0]?.channelId || '';
      }
      const binding = bindings.find((item) => item.channelId === nextIdentity[platform]);
      nextBinding[platform] = binding?.sessionId || '';
    }
    selectedIdentity = nextIdentity;
    selectedBinding = nextBinding;
  }

  function resolveSelectedBinding(platform) {
    if (selectedBinding[platform]) return selectedBinding[platform];
    const identity = selectedIdentity[platform];
    const binding = channelBindings.find((item) => item.channelType === platform && item.channelId === identity);
    if (!binding?.sessionId) return '';
    selectedBinding[platform] = binding.sessionId;
    selectedBinding = selectedBinding;
    return binding.sessionId;
  }

  function identityOptions(platform) {
    return channelBindings.filter((item) => item.channelType === platform);
  }

  async function selectChannelIdentity(platform, channelID) {
    selectedIdentity[platform] = channelID;
    selectedIdentity = selectedIdentity;
    const binding = channelBindings.find((item) => item.channelType === platform && item.channelId === channelID);
    selectedBinding[platform] = binding?.sessionId || '';
    selectedBinding = selectedBinding;
    await loadSelectedChannelTools(platform);
  }
  // These are reactive-derived: Svelte only tracks dependencies referenced in
  // the expression, so we pass $sessions/channelBindings/selectedIdentity in
  // explicitly. Calling bindingOptions('feishu') from a template would only
  // see the literal string and never re-render when bindings load, leaving the
  // session <select> blank.
  $: feishuBindingOptions = buildBindingOptions('feishu', $sessions, channelBindings, selectedIdentity);
  $: wechatBindingOptions = buildBindingOptions('wechat', $sessions, channelBindings, selectedIdentity);

  function buildBindingOptions(platform, sessions, bindings, identity) {
    const options = [...(sessions || [])];
    const boundSessionID = bindings.find((item) =>
      item.channelType === platform && item.channelId === identity[platform]
    )?.sessionId;
    // /api/sessions is intentionally paginated. Keep a selected bound session
    // visible even when it falls outside the current page of recent sessions.
    if (boundSessionID && !options.some((item) => item?.id === boundSessionID)) {
      options.unshift({ id: boundSessionID, title: $t('settings.channels.boundSessionTitle', { id: boundSessionID }), bound: true });
    }
    return options;
  }

  $: syncBoundSessionSelection(channelBindings, $sessions);

  function syncBoundSessionSelection(bindings) {
    let changed = false;
    for (const platform of ['wechat', 'feishu']) {
      const platformBindings = bindings.filter((item) => item.channelType === platform);
      const binding = platformBindings.find((item) => item.channelId === selectedIdentity[platform]);
      if (binding && selectedBinding[platform] !== binding.sessionId) {
        selectedBinding[platform] = binding.sessionId;
        changed = true;
      }
    }
    if (changed) selectedBinding = selectedBinding;
  }

  async function selectBinding(platform, sessionID) {
    const currentBinding = channelBindings.find((binding) => binding.channelType === platform && binding.channelId === selectedIdentity[platform]);
    if (!currentBinding) {
      setError($t('settings.channels.noBindingToTransfer'));
      return;
    }
    if (currentBinding.sessionId !== sessionID) {
      try {
        await putJSON(`/api/sessions/${encodeURIComponent(sessionID)}/bindings`, {
          channelType: platform,
          channelId: currentBinding.channelId,
          fromSessionId: currentBinding.sessionId,
          toSessionId: sessionID
        });
      } catch (err) {
        setError(err);
        return;
      }
    }
    selectedBinding[platform] = sessionID;
    try {
      await loadChannelBindings();
      setNotice($t('settings.channels.saved'));
    } catch (err) {
      setError(err);
    }
  }

  // These take the per-platform states/catalog arrays as explicit args so the
  // template expressions reference channelTools/toolCatalog and Svelte
  // re-evaluates them after a save/refresh. Reading those stores inside the
  // function body (as before) would never re-run the checkbox on load.
  function toolEnabled(platform, name, states, catalog) {
    const configured = (states || []).find((item) => (item.name || item.toolName) === name);
    if (configured) return configured.requestedEnabled ?? configured.enabled;
    return (catalog || []).find((item) => item.name === name)?.default !== false;
  }

  function toolAvailable(tool) {
    return tool?.available !== false;
  }

  function toolUnavailableReason(tool) {
    return tool?.unavailableReason || $t('settings.channels.toolUnavailable');
  }

  function toolState(platform, name, states) {
    return (states || []).find((item) => (item.name || item.toolName) === name);
  }

  function toolStateLabel(platform, name, states) {
    const state = toolState(platform, name, states);
    if (!state) return '';
    if (!state.available) return '';
    if (state.registered) return '✓';
    if (state.willRegister || state.effectiveEnabled) return '⏳';
    return '○';
  }

  function toolStateTitle(platform, name, states) {
    const state = toolState(platform, name, states);
    if (!state) return '';
    if (!state.available) return $t('settings.channels.toolUnavailable');
    if (state.registered) return $t('settings.channels.toolRegistered');
    if (state.willRegister || state.effectiveEnabled) return $t('settings.channels.toolNextRun');
    return $t('settings.channels.toolDisabled');
  }

  function toggleTool(platform, name, enabled) {
    const current = channelTools[platform].filter((item) => (item.name || item.toolName) !== name);
    channelTools[platform] = [...current, { name, requestedEnabled: enabled }];
    channelTools = channelTools;
  }

  function defaultForm() {
    return {
      wechat: { enabled: false, credPath: '', workDir: '', autoTyping: true },
      feishu: { enabled: false, appID: '', appSecret: '', workDir: '' }
    };
  }

  function syncFromStore(raw) {
    if (raw === lastRaw) return;
    lastRaw = raw;
    try {
      const cfg = JSON.parse(raw || '{}');
      form = {
        wechat: {
          enabled: Boolean(cfg.channels?.wechat?.enabled ?? cfg.features?.wechat),
          credPath: stringValue(cfg.channels?.wechat?.credPath),
          workDir: stringValue(cfg.channels?.wechat?.workDir),
          autoTyping: readBool(cfg.channels?.wechat?.autoTyping, true)
        },
        feishu: {
          enabled: Boolean(cfg.channels?.feishu?.enabled ?? cfg.features?.feishu),
          appID: stringValue(cfg.channels?.feishu?.appId),
          appSecret: stringValue(cfg.channels?.feishu?.appSecret),
          workDir: stringValue(cfg.channels?.feishu?.workDir)
        }
      };
      parseError = '';
    } catch (err) {
      parseError = err instanceof Error ? err.message : String(err);
      form = defaultForm();
    }
  }

  function stringValue(value) {
    return typeof value === 'string' ? value : '';
  }

  function readBool(value, fallback) {
    return typeof value === 'boolean' ? value : fallback;
  }

  function statusFor(name) {
    return $channels.find((item) => item.name === name) || { name, enabled: false, connected: false };
  }

  function statusLabel(name) {
    const status = statusFor(name);
    if (!status.enabled) return $t('common.disabledState');
    return status.connected ? $t('common.connected') : $t('common.disconnected');
  }

  function channelPayload(platform) {
    if (platform === 'wechat') {
      return {
        enabled: Boolean(form.wechat.enabled),
        credPath: String(form.wechat.credPath || '').trim(),
        workDir: String(form.wechat.workDir || '').trim(),
        autoTyping: Boolean(form.wechat.autoTyping)
      };
    }
    return {
      enabled: Boolean(form.feishu.enabled),
      appId: String(form.feishu.appID || '').trim(),
      appSecret: String(form.feishu.appSecret || '').trim(),
      workDir: String(form.feishu.workDir || '').trim()
    };
  }

  async function saveChannelConfig(platform, noticeKey = 'settings.channels.saved') {
    clearBanners();
    const generation = ++configRequestGeneration;
    saving = true;
    try {
      await patchJSON(`/api/serve/config/channels/${platform}`, channelPayload(platform));
      if (generation !== configRequestGeneration) return;
      await refreshAll();
      if (noticeKey) setNotice($t(noticeKey));
    } catch (err) {
      if (generation === configRequestGeneration) setError(err);
      throw err;
    } finally {
      if (generation === configRequestGeneration) saving = false;
    }
  }

  function openFeishu() {
    feishuDraft = cloneChannel(form.feishu);
    feishuOpen = true;
  }

  function closeFeishu() {
    feishuOpen = false;
  }

  async function saveFeishu() {
    if (feishuDraft.enabled && (!feishuDraft.appID.trim() || !feishuDraft.appSecret.trim())) {
      setError($t('settings.channels.feishuKeyRequired'));
      return;
    }
    form.feishu = cloneChannel(feishuDraft);
    form = form;
    await saveChannelConfig('feishu', 'settings.channels.feishuSaved');
    feishuOpen = false;
  }

  function cloneChannel(channel) {
    return { ...channel };
  }

  async function saveWechatConfig() {
    await saveChannelConfig('wechat', 'settings.channels.wechatSaved');
  }

  async function disableWechat() {
    form.wechat.enabled = false;
    form = form;
    await saveChannelConfig('wechat', 'settings.channels.wechatDisabled');
  }

  async function startWechatLogin() {
    clearBanners();
    wechatOpen = true;
    wechatLogin = { state: 'starting' };
    try {
      await saveChannelConfig('wechat', '');
      wechatLogin = await postJSON('/api/channels/wechat/login', {});
      startWechatPolling();
    } catch (err) {
      setError(err);
    }
  }

  function startWechatPolling() {
    stopWechatPolling();
    wechatPoll = window.setInterval(loadWechatLogin, 1800);
    loadWechatLogin();
  }

  function stopWechatPolling() {
    if (wechatPoll) window.clearInterval(wechatPoll);
    wechatPoll = null;
  }

  async function loadWechatLogin() {
    try {
      wechatLogin = await request('/api/channels/wechat/login');
      if (wechatLogin?.state === 'confirmed') {
        stopWechatPolling();
        form.wechat.enabled = true;
        form = form;
        await refreshAll();
        setNotice($t('settings.channels.wechatEnabled'));
      } else if (wechatLogin?.state === 'error' || wechatLogin?.state === 'cancelled') {
        stopWechatPolling();
      }
    } catch (err) {
      stopWechatPolling();
      setError(err);
    }
  }

  async function closeWechatLogin() {
    stopWechatPolling();
    if (wechatLogin && !['confirmed', 'error', 'cancelled'].includes(wechatLogin.state)) {
      try {
        await del('/api/channels/wechat/login');
      } catch {
        // Closing the modal should not mask the current page state.
      }
    }
    wechatOpen = false;
  }

  function qrEmbedSrc(value) {
    const text = String(value || '').trim();
    if (!text || text.startsWith('/') || text.startsWith('http://') || text.startsWith('https://') || text.startsWith('data:')) return text;
    return `data:image/png;base64,${text}`;
  }

  function openWechatQRTab() {
    const url = wechatLogin?.qrOpenUrl || wechatLogin?.qrUrl || '';
    if (!url) return;
    const win = window.open(qrEmbedSrc(url), '_blank');
    if (!win) setError($t('settings.channels.popupBlocked'));
  }
</script>

{#if parseError}
  <p class="settings-parse-error">{$t('settings.app.parseError', { error: parseError })}</p>
{/if}

<div class="channels-refresh-bar">
  <Button type="button" variant="outline" size="sm" onclick={refreshAll}>
    <RefreshCw size={14} aria-hidden="true" />
    <span>{$t('common.refresh')}</span>
  </Button>
</div>

<div class="channel-settings-grid">
  <Card class="channel-config-card">
    <div class="channel-card-head">
      <div>
        <h3>Feishu</h3>
        <span class="hint">{$t('settings.channels.feishuHint')}</span>
      </div>
      <Badge variant={statusFor('feishu').connected ? 'default' : 'secondary'}>
        {statusLabel('feishu')}
      </Badge>
    </div>
    <CardContent class="channel-card-body">
      <dl class="channel-kv">
        <dt>App ID</dt><dd>{form.feishu.appID || $t('common.uninitialized')}</dd>
        <dt>{$t('sessions.workDir')}</dt><dd>{form.feishu.workDir || $t('common.uninitialized')}</dd>
      </dl>
      <div class="channel-session-select">
        <span class="channel-select-label">Session {$t('settings.channels.identity')}</span>
        <select class="settings-select" bind:value={selectedIdentity.feishu} onchange={(event) => selectChannelIdentity('feishu', event.currentTarget.value)}>
          <option value="">{$t('settings.channels.unboundIdentity')}</option>
          {#each identityOptions('feishu') as item (item.channelId)}<option value={item.channelId}>{item.channelId}</option>{/each}
        </select>
        <span class="channel-select-label">Session</span>
        <select class="settings-select" bind:value={selectedBinding.feishu} disabled={!selectedIdentity.feishu} onchange={(event) => selectBinding('feishu', event.currentTarget.value)}>
          <option value="" disabled>{$t('settings.channels.unboundSession')}</option>
          {#each feishuBindingOptions as item (item.id)}
            <option value={item.id}>{item.title || item.preview || item.id} · {item.id}</option>
          {/each}
        </select>
      </div>
      <div class="channel-tools-config">
        <div class="channel-tools-head">
          <span>{$t('settings.channels.sessionTools')}</span>
          <span class="hint">{$t('settings.channels.sessionToolsHint')}</span>
        </div>
        <div class="hint">{toolAppliesTo.feishu === 'current' ? $t('settings.channels.toolAppliesCurrent') : $t('settings.channels.toolAppliesNext')}</div>
        <div class="channel-tools-list">
          {#each toolCatalog.feishu as tool}<label class="channel-tool-toggle" title={toolAvailable(tool) ? tool.name : toolUnavailableReason(tool)}><input type="checkbox" disabled={!toolAvailable(tool)} checked={toolEnabled('feishu', tool.name, channelTools.feishu, toolCatalog.feishu)} onchange={(e) => toggleTool('feishu', tool.name, e.currentTarget.checked)} /> <span>{tool.name}</span>{#if !toolAvailable(tool)}<small class="sub tool-state-icon" aria-label={toolUnavailableReason(tool)} title={toolUnavailableReason(tool)}><Ban size={12} aria-hidden="true" /></small>{:else if toolStateLabel('feishu', tool.name, channelTools.feishu)} <small class="sub tool-state-icon" title={toolStateTitle('feishu', tool.name, channelTools.feishu)}>{toolStateLabel('feishu', tool.name, channelTools.feishu)}</small>{/if}</label>{/each}
        </div>
        <div class="channel-tools-actions"><Button type="button" variant="outline" size="sm" disabled={toolSaving.feishu || !selectedBinding.feishu || !toolCatalogReady.feishu} onclick={() => saveChannelTools('feishu')}>{$t('settings.channels.saveTools')}</Button></div>
      </div>
      <div class="channel-card-actions">
        <Button type="button" variant="outline" size="sm" onclick={openFeishu}>
          <Settings2 size={14} aria-hidden="true" />
          <span>{$t('settings.channels.configure')}</span>
        </Button>
      </div>
    </CardContent>
  </Card>

  <Card class="channel-config-card">
    <div class="channel-card-head">
      <div>
        <h3>WeChat</h3>
        <span class="hint">{$t('settings.channels.wechatHint')}</span>
      </div>
      <Badge variant={statusFor('wechat').connected ? 'default' : 'secondary'}>
        {statusLabel('wechat')}
      </Badge>
    </div>
    <CardContent class="channel-card-body">
      <div class="channel-form-grid">
        <SettingsField label={$t('settings.serve.wechatCred')}>
          <Input bind:value={form.wechat.credPath} placeholder="wechat-credentials.json" />
        </SettingsField>
        <SettingsField label={$t('settings.serve.wechatWorkDir')}>
          <Input bind:value={form.wechat.workDir} placeholder="/home/user/project" />
        </SettingsField>
        <div class="channel-switch-row">
          <span class="settings-field-label">{$t('settings.serve.autoTyping')}</span>
          <Switch bind:checked={form.wechat.autoTyping} aria-label={$t('settings.serve.autoTyping')} />
        </div>
      </div>
      <div class="channel-session-select">
        <span class="channel-select-label">Session {$t('settings.channels.identity')}</span>
        <select class="settings-select" bind:value={selectedIdentity.wechat} onchange={(event) => selectChannelIdentity('wechat', event.currentTarget.value)}>
          <option value="">{$t('settings.channels.unboundIdentity')}</option>
          {#each identityOptions('wechat') as item (item.channelId)}<option value={item.channelId}>{item.channelId}</option>{/each}
        </select>
        <span class="channel-select-label">Session</span>
        <select class="settings-select" bind:value={selectedBinding.wechat} disabled={!selectedIdentity.wechat} onchange={(event) => selectBinding('wechat', event.currentTarget.value)}>
          <option value="" disabled>{$t('settings.channels.unboundSession')}</option>
          {#each wechatBindingOptions as item (item.id)}
            <option value={item.id}>{item.title || item.preview || item.id} · {item.id}</option>
          {/each}
        </select>
      </div>
      <div class="channel-tools-config">
        <div class="channel-tools-head">
          <span>{$t('settings.channels.sessionTools')}</span>
          <span class="hint">{$t('settings.channels.sessionToolsHint')}</span>
        </div>
        <div class="hint">{toolAppliesTo.wechat === 'current' ? $t('settings.channels.toolAppliesCurrent') : $t('settings.channels.toolAppliesNext')}</div>
        <div class="channel-tools-list">
          {#each toolCatalog.wechat as tool}<label class="channel-tool-toggle" title={toolAvailable(tool) ? tool.name : toolUnavailableReason(tool)}><input type="checkbox" disabled={!toolAvailable(tool)} checked={toolEnabled('wechat', tool.name, channelTools.wechat, toolCatalog.wechat)} onchange={(e) => toggleTool('wechat', tool.name, e.currentTarget.checked)} /> <span>{tool.name}</span>{#if !toolAvailable(tool)}<small class="sub tool-state-icon" aria-label={toolUnavailableReason(tool)} title={toolUnavailableReason(tool)}><Ban size={12} aria-hidden="true" /></small>{:else if toolStateLabel('wechat', tool.name, channelTools.wechat)} <small class="sub tool-state-icon" title={toolStateTitle('wechat', tool.name, channelTools.wechat)}>{toolStateLabel('wechat', tool.name, channelTools.wechat)}</small>{/if}</label>{/each}
        </div>
        <div class="channel-tools-actions"><Button type="button" variant="outline" size="sm" disabled={toolSaving.wechat || !selectedBinding.wechat || !toolCatalogReady.wechat} onclick={() => saveChannelTools('wechat')}>{$t('settings.channels.saveTools')}</Button></div>
      </div>
      <div class="channel-card-actions">
        <Button type="button" variant="outline" size="sm" disabled={saving} onclick={saveWechatConfig}>
          <Save size={14} aria-hidden="true" />
          <span>{$t('common.save')}</span>
        </Button>
        <Button type="button" variant="secondary" size="sm" disabled={saving} onclick={startWechatLogin}>
          <ScanLine size={14} aria-hidden="true" />
          <span>{$t(form.wechat.enabled ? 'settings.channels.wechatRelogin' : 'settings.channels.wechatScanEnable')}</span>
        </Button>
        {#if form.wechat.enabled}
          <Button type="button" variant="ghost" size="sm" disabled={saving} onclick={disableWechat}>
            <Trash2 size={14} aria-hidden="true" />
            <span>{$t('common.disable')}</span>
          </Button>
        {/if}
      </div>
    </CardContent>
  </Card>

  <Card class="channel-config-card">
    <div class="channel-card-head">
      <div>
        <h3>{$t('settings.channels.deliveriesTitle')}</h3>
        <span class="hint">{$t('settings.channels.deliveriesHint')}</span>
      </div>
      <Button type="button" variant="outline" size="sm" onclick={loadDeliveries}>
        <RefreshCw size={14} aria-hidden="true" />
        <span>{$t('settings.channels.deliveriesRefresh')}</span>
      </Button>
    </div>
    <CardContent class="channel-card-body">
      {#if deliveriesError}
        <p class="settings-parse-error">{deliveriesError}</p>
      {:else if deliveries === null}
        <p class="hint">{$t('settings.channels.deliveriesLoading')}</p>
      {:else if deliveries.length === 0}
        <p class="hint">{$t('settings.channels.deliveriesEmpty')}</p>
      {:else}
        <ul class="delivery-failure-list">
          {#each deliveries as failure (failure.operationId)}
            <li class="delivery-failure-row">
              <div class="delivery-failure-meta">
                <div>
                  {deliveryFailureLabel(failure)}
                  {#if !isDeliveryFailureTransient(failure)}
                    <span class="hint">{failure.failureCode || $t('settings.channels.deliveriesPermanent')}</span>
                  {/if}
                </div>
                <div class="hint">{$t('settings.channels.deliveriesAttempts')}: {failure.attemptCount ?? 0}{failure.updatedAt ? ` · ${failure.updatedAt}` : ''}</div>
                <div class="hint">{failure.operationId}</div>
              </div>
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={deliveryRetrying !== '' || !canRetryDelivery(failure)}
                onclick={() => retryDeliveryOperation(failure.operationId)}
              >
                {deliveryRetrying === failure.operationId ? $t('settings.channels.deliveriesRetrying') : $t('settings.channels.deliveriesRetry')}
              </Button>
            </li>
          {/each}
        </ul>
      {/if}
      <p class="hint">{$t('settings.channels.deliveriesScope')}</p>
    </CardContent>
  </Card>
</div>

{#if feishuOpen}
  <Modal open={feishuOpen} title={$t('settings.channels.feishuConfig')} className="channel-modal-overlay" on:close={closeFeishu}>
    <div class="channel-modal">
      <header>
        <div>
          <h3>{$t('settings.channels.feishuConfig')}</h3>
          <span class="hint">{$t('settings.channels.feishuConfigHint')}</span>
        </div>
        <Button type="button" variant="ghost" size="sm" onclick={closeFeishu}>
          <X size={14} aria-hidden="true" />
          <span>{$t('common.close')}</span>
        </Button>
      </header>
      <div class="channel-modal-form">
        <div class="channel-switch-row">
          <span class="settings-field-label">{$t('common.enabled')}</span>
          <Switch bind:checked={feishuDraft.enabled} aria-label={$t('common.enabled')} />
        </div>
        <SettingsField label={$t('settings.serve.feishuAppID')}>
          <Input bind:value={feishuDraft.appID} />
        </SettingsField>
        <SettingsField label={$t('settings.serve.feishuAppSecret')}>
          <Input type="password" bind:value={feishuDraft.appSecret} />
        </SettingsField>
        <SettingsField label={$t('settings.serve.feishuWorkDir')}>
          <Input bind:value={feishuDraft.workDir} placeholder="/home/user/project" />
        </SettingsField>
      </div>
      <footer>
        <Button type="button" variant="outline" size="sm" onclick={closeFeishu}>{$t('common.cancel')}</Button>
        <Button type="button" variant="outline" size="sm" disabled={saving} onclick={saveFeishu}>{$t('common.save')}</Button>
      </footer>
    </div>
  </Modal>
{/if}

{#if wechatOpen}
  <Modal open={wechatOpen} title={$t('settings.channels.wechatLogin')} className="channel-modal-overlay" on:close={closeWechatLogin}>
    <div class="channel-modal qr-modal">
      <header>
        <div>
          <h3>{$t('settings.channels.wechatLogin')}</h3>
          <span class="hint">{$t('settings.channels.wechatLoginHint')}</span>
        </div>
        <Button type="button" variant="ghost" size="sm" onclick={closeWechatLogin}>
          <X size={14} aria-hidden="true" />
          <span>{$t('common.close')}</span>
        </Button>
      </header>
      <div class="qr-panel">
        <p class="empty">
          {#if wechatLogin?.qrOpenUrl || wechatLogin?.qrUrl}
            {$t('settings.channels.wechatOpenQrHint')}
          {:else if wechatLogin?.state === 'starting'}
            {$t('common.loading')}
          {:else}
            {$t('settings.channels.wechatNoQr')}
          {/if}
        </p>
        <div class="qr-status">
          <Badge variant={wechatLogin?.state === 'confirmed' ? 'default' : (wechatLogin?.state === 'error' || wechatLogin?.state === 'cancelled' ? 'destructive' : 'secondary')}>
            {$t(`settings.channels.wechatState.${wechatLogin?.state || 'idle'}`)}
          </Badge>
          {#if wechatLogin?.userId}
            <code>{wechatLogin.userId}</code>
          {/if}
          {#if wechatLogin?.error}
            <p class="error-text">{wechatLogin.error}</p>
          {/if}
        </div>
      </div>
      <footer>
        <Button type="button" variant="outline" size="sm" onclick={closeWechatLogin}>{$t('dirBrowser.cancel')}</Button>
        <Button type="button" variant="outline" size="sm" disabled={!wechatLogin?.qrUrl && !wechatLogin?.qrOpenUrl} onclick={openWechatQRTab}>{$t('settings.channels.openQrTab')}</Button>
        <Button type="button" variant="outline" size="sm" onclick={startWechatLogin}>{$t('settings.channels.refreshQr')}</Button>
      </footer>
    </div>
  </Modal>
{/if}

<style>
  .channels-refresh-bar { display: flex; justify-content: flex-end; margin-bottom: 12px; }
  .channel-card-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 14px 16px;
    border-bottom: 1px solid var(--border-subtle);
  }
  .channel-card-head h3 { margin: 0; font-size: 14px; }
  .channel-card-head .hint { font-size: 12px; color: var(--text-muted); }
  .channel-kv {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 4px 12px;
    font-size: 13px;
    margin-bottom: 12px;
  }
  .channel-kv dt { color: var(--text-muted); }
  .channel-kv dd { margin: 0; color: var(--text); word-break: break-all; }
  .channel-form-grid { display: grid; grid-template-columns: 1fr; gap: 12px; margin-bottom: 12px; }
  .channel-switch-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    min-height: 32px;
  }
  .channel-select-label { font-size: 12px; color: var(--text-secondary); font-weight: 500; }
  .delivery-failure-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
  .delivery-failure-row {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    padding: 8px 10px;
    border: 1px solid var(--border-subtle);
    border-radius: 8px;
  }
  .delivery-failure-meta { min-width: 0; font-size: 13px; display: flex; flex-direction: column; gap: 2px; }
  .delivery-failure-meta > div { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>
