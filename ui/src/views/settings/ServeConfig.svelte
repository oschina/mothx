<script>
  import { serveConfig, setError, setNotice, clearBanners, refreshAll } from '../../lib/stores.js';
  import { postJSON, putJSON } from '../../lib/api.js';
  import { t } from '../../lib/preferences.js';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Input } from '$lib/components/ui/input/index.js';
  import { Switch } from '$lib/components/ui/switch/index.js';
  import { Card, CardContent } from '$lib/components/ui/card/index.js';
  import { Save } from '@lucide/svelte';
  import SettingsSection from './SettingsSection.svelte';
  import SettingsField from './SettingsField.svelte';
  import SettingsSwitch from './SettingsSwitch.svelte';
  import ListEditor from './ListEditor.svelte';

  let form = defaultForm();
  let jsonDraft = '';
  let parseError = '';
  let advancedOpen = false;
  let lastRaw = '';

  $: syncFromStore($serveConfig);

  function defaultForm() {
    return {
      features: {
        webUI: true,
        openAIAPI: true,
        cron: true,
        memory: true,
        multiAgent: false,
        wechat: false,
        feishu: false
      },
      webUI: { enabled: true, dir: 'ui/dist' },
      api: {
        listen: '127.0.0.1:7872',
        systemPromptMode: 'append',
        requestTimeoutSeconds: 1800,
        maxConcurrentRequests: '',
        logLevel: 'info',
        enableWebSearch: false,
        enableBrowser: false,
        enableArtifact: false,
        enableA2AMaster: false,
        enableDelegate: false,
        enableWorkflows: false,
        enableSubAgents: false,
        auth: { enabled: false, tokens: [] },
        sandbox: { enabled: false, level: '' },
        session: { idleTimeoutSeconds: 1800, maxSessions: '' },
        cors: { enabled: false, allowOrigins: ['*'] },
        toolVisibility: { mode: 'content', detail: 'collapsed' }
      },
      cron: { enabled: true, interval: 30 },
      memory: { enabled: true, path: '' },
      security: { smartApprovals: true },
      agent: {
        maxTurns: 180,
        budgetPressure: true,
        contextPressure: true,
        budgetPressureThreshold: 0.2,
        contextPressureThreshold: 0.55
      },
      hooks: { preToolCall: '', postToolCall: '' },
      channels: {
        artifact: false,
        wechat: { enabled: false, credPath: '', workDir: '', autoTyping: true },
        feishu: { enabled: false, appID: '', appSecret: '', workDir: '' }
      },
      lobsterMode: false
    };
  }

  function syncFromStore(raw) {
    if (raw === lastRaw) return;
    lastRaw = raw;
    jsonDraft = raw || '{}';
    try {
      const cfg = JSON.parse(raw || '{}');
      form = formFromConfig(cfg);
      parseError = '';
    } catch (err) {
      parseError = err instanceof Error ? err.message : String(err);
      form = defaultForm();
    }
  }

  function formFromConfig(cfg = {}) {
    const base = defaultForm();
    const api = cfg.api || {};
    const features = cfg.features || {};
    const webUI = cfg.webUI || {};
    const cron = cfg.cron || {};
    const memory = cfg.memory || {};
    const security = cfg.security || {};
    const agent = cfg.agent || {};
    const hooks = cfg.hooks || {};
    const channels = cfg.channels || {};
    const wechat = channels.wechat || {};
    const feishu = channels.feishu || {};

    return {
      features: {
        webUI: readBool(features.webUI, webUI.enabled, base.features.webUI),
        openAIAPI: readBool(features.openAIAPI, base.features.openAIAPI),
        cron: readBool(features.cron, cron.enabled, base.features.cron),
        memory: readBool(features.memory, memory.enabled, base.features.memory),
        multiAgent: readBool(features.multiAgent, api.enableSubAgents, base.features.multiAgent),
        wechat: readBool(features.wechat, wechat.enabled, base.features.wechat),
        feishu: readBool(features.feishu, feishu.enabled, base.features.feishu)
      },
      webUI: {
        enabled: readBool(webUI.enabled, features.webUI, base.webUI.enabled),
        dir: stringValue(webUI.dir, base.webUI.dir)
      },
      api: {
        listen: stringValue(api.listen, base.api.listen),
        systemPromptMode: stringValue(api.systemPromptMode, base.api.systemPromptMode),
        requestTimeoutSeconds: numberValue(api.requestTimeoutSeconds, base.api.requestTimeoutSeconds),
        maxConcurrentRequests: optionalNumber(api.maxConcurrentRequests),
        logLevel: stringValue(api.logLevel, base.api.logLevel),
        enableWebSearch: readBool(api.enableWebSearch, cfg.webSearch, false),
        enableBrowser: readBool(api.enableBrowser, cfg.browser, false),
        enableArtifact: readBool(api.enableArtifact, cfg.artifact, false),
        enableA2AMaster: readBool(api.enableA2AMaster, cfg.a2aMaster, false),
        enableDelegate: readBool(api.enableDelegate, false),
        enableWorkflows: readBool(api.enableWorkflows, false),
        enableSubAgents: readBool(api.enableSubAgents, features.multiAgent, false),
        auth: {
          enabled: readBool(cfg.auth?.enabled, api.auth?.enabled, false),
          tokens: arrayValue(cfg.auth?.tokens, api.auth?.tokens)
        },
        sandbox: {
          enabled: readBool(api.sandbox?.enabled, false),
          level: stringValue(api.sandbox?.level, '')
        },
        session: {
          idleTimeoutSeconds: numberValue(api.session?.idleTimeoutSeconds, base.api.session.idleTimeoutSeconds),
          maxSessions: optionalNumber(api.session?.maxSessions)
        },
        cors: {
          enabled: readBool(api.cors?.enabled, false),
          allowOrigins: arrayValue(api.cors?.allowOrigins, ['*'])
        },
        toolVisibility: {
          mode: stringValue(api.toolVisibility?.mode, base.api.toolVisibility.mode),
          detail: stringValue(api.toolVisibility?.detail, base.api.toolVisibility.detail)
        }
      },
      cron: {
        enabled: readBool(cron.enabled, features.cron, base.cron.enabled),
        interval: numberValue(cron.interval, base.cron.interval)
      },
      memory: {
        enabled: readBool(memory.enabled, features.memory, base.memory.enabled),
        path: stringValue(memory.path, '')
      },
      security: {
        smartApprovals: readBool(security.smart_approvals, base.security.smartApprovals)
      },
      agent: {
        maxTurns: numberValue(agent.max_turns, base.agent.maxTurns),
        budgetPressure: readBool(agent.budget_pressure, base.agent.budgetPressure),
        contextPressure: readBool(agent.context_pressure, base.agent.contextPressure),
        budgetPressureThreshold: numberValue(agent.budget_pressure_threshold, base.agent.budgetPressureThreshold),
        contextPressureThreshold: numberValue(agent.context_pressure_threshold, base.agent.contextPressureThreshold)
      },
      hooks: {
        preToolCall: stringValue(hooks.pre_tool_call, ''),
        postToolCall: stringValue(hooks.post_tool_call, '')
      },
      channels: {
        artifact: readBool(channels.artifact, false),
        wechat: {
          enabled: readBool(wechat.enabled, features.wechat, false),
          credPath: stringValue(wechat.cred_path, ''),
          workDir: stringValue(wechat.work_dir, ''),
          autoTyping: readBool(wechat.auto_typing, true)
        },
        feishu: {
          enabled: readBool(feishu.enabled, features.feishu, false),
          appID: stringValue(feishu.app_id, ''),
          appSecret: stringValue(feishu.app_secret, ''),
          workDir: stringValue(feishu.work_dir, '')
        }
      },
      lobsterMode: readBool(cfg.lobsterMode, false)
    };
  }

  function readBool(...values) {
    for (const value of values) {
      if (typeof value === 'boolean') return value;
    }
    return Boolean(values[values.length - 1]);
  }

  function stringValue(value, fallback = '') {
    return typeof value === 'string' ? value : fallback;
  }

  function numberValue(value, fallback = 0) {
    const n = Number(value);
    return Number.isFinite(n) ? n : fallback;
  }

  function optionalNumber(value) {
    const n = Number(value);
    return Number.isFinite(n) && n > 0 ? n : '';
  }

  function arrayValue(value, fallback = []) {
    if (!Array.isArray(value)) return [...fallback];
    return value.map((item) => String(item ?? ''));
  }

  function ensureObject(parent, key) {
    if (!parent[key] || typeof parent[key] !== 'object' || Array.isArray(parent[key])) {
      parent[key] = {};
    }
    return parent[key];
  }

  function cleanList(values = []) {
    return values.map((item) => String(item || '').trim()).filter(Boolean);
  }

  function writeOptionalNumber(target, key, value) {
    const n = Number(value);
    if (Number.isFinite(n) && n > 0) target[key] = n;
    else delete target[key];
  }

  function buildConfigForSave() {
    const cfg = JSON.parse(jsonDraft || '{}');
    const features = ensureObject(cfg, 'features');
    const api = ensureObject(cfg, 'api');
    const webUI = ensureObject(cfg, 'webUI');
    const auth = ensureObject(cfg, 'auth');
    const sandbox = ensureObject(api, 'sandbox');
    const session = ensureObject(api, 'session');
    const cors = ensureObject(api, 'cors');
    const toolVisibility = ensureObject(api, 'toolVisibility');
    const cron = ensureObject(cfg, 'cron');
    const memory = ensureObject(cfg, 'memory');
    const security = ensureObject(cfg, 'security');
    const agent = ensureObject(cfg, 'agent');
    const hooks = ensureObject(cfg, 'hooks');
    const channels = ensureObject(cfg, 'channels');
    const wechat = ensureObject(channels, 'wechat');
    const feishu = ensureObject(channels, 'feishu');

    features.webUI = Boolean(form.features.webUI);
    features.openAIAPI = Boolean(form.features.openAIAPI);
    features.cron = Boolean(form.features.cron);
    features.memory = Boolean(form.features.memory);
    features.multiAgent = Boolean(form.features.multiAgent);
    features.wechat = Boolean(form.features.wechat);
    features.feishu = Boolean(form.features.feishu);

    webUI.enabled = Boolean(form.features.webUI);
    webUI.dir = form.webUI.dir.trim() || 'ui/dist';

    api.listen = form.api.listen.trim() || '127.0.0.1:7872';
    api.systemPromptMode = form.api.systemPromptMode || 'append';
    api.requestTimeoutSeconds = numberValue(form.api.requestTimeoutSeconds, 1800);
    writeOptionalNumber(api, 'maxConcurrentRequests', form.api.maxConcurrentRequests);
    api.logLevel = form.api.logLevel || 'info';
    api.enableWebSearch = Boolean(form.api.enableWebSearch);
    api.enableBrowser = Boolean(form.api.enableBrowser);
    api.enableArtifact = Boolean(form.api.enableArtifact);
    api.enableA2AMaster = Boolean(form.api.enableA2AMaster);
    cfg.webSearch = Boolean(form.api.enableWebSearch);
    cfg.browser = Boolean(form.api.enableBrowser);
    cfg.artifact = Boolean(form.api.enableArtifact);
    cfg.a2aMaster = Boolean(form.api.enableA2AMaster);
    api.enableDelegate = Boolean(form.api.enableDelegate);
    api.enableWorkflows = Boolean(form.api.enableWorkflows);
    api.enableSubAgents = Boolean(form.features.multiAgent || form.api.enableSubAgents);

    auth.enabled = Boolean(form.api.auth.enabled);
    auth.tokens = cleanList(form.api.auth.tokens);
    delete api.auth;
    sandbox.enabled = Boolean(form.api.sandbox.enabled);
    if (form.api.sandbox.level) sandbox.level = form.api.sandbox.level;
    else delete sandbox.level;
    session.idleTimeoutSeconds = numberValue(form.api.session.idleTimeoutSeconds, 1800);
    writeOptionalNumber(session, 'maxSessions', form.api.session.maxSessions);
    cors.enabled = Boolean(form.api.cors.enabled);
    cors.allowOrigins = cleanList(form.api.cors.allowOrigins);
    toolVisibility.mode = form.api.toolVisibility.mode || 'content';
    toolVisibility.detail = form.api.toolVisibility.detail || 'collapsed';

    cron.enabled = Boolean(form.features.cron);
    cron.interval = numberValue(form.cron.interval, 30);
    memory.enabled = Boolean(form.features.memory);
    if (form.memory.path.trim()) memory.path = form.memory.path.trim();
    else delete memory.path;

    security.smart_approvals = Boolean(form.security.smartApprovals);

    agent.max_turns = numberValue(form.agent.maxTurns, 180);
    agent.budget_pressure = Boolean(form.agent.budgetPressure);
    agent.context_pressure = Boolean(form.agent.contextPressure);
    agent.budget_pressure_threshold = numberValue(form.agent.budgetPressureThreshold, 0.2);
    agent.context_pressure_threshold = numberValue(form.agent.contextPressureThreshold, 0.55);

    hooks.pre_tool_call = form.hooks.preToolCall.trim();
    hooks.post_tool_call = form.hooks.postToolCall.trim();

    wechat.enabled = Boolean(form.features.wechat);
    channels.artifact = Boolean(form.channels.artifact);
    wechat.cred_path = form.channels.wechat.credPath.trim();
    wechat.work_dir = form.channels.wechat.workDir.trim();
    wechat.auto_typing = Boolean(form.channels.wechat.autoTyping);

    feishu.enabled = Boolean(form.features.feishu);
    feishu.app_id = form.channels.feishu.appID.trim();
    feishu.app_secret = form.channels.feishu.appSecret.trim();
    feishu.work_dir = form.channels.feishu.workDir.trim();

    cfg.lobsterMode = Boolean(form.lobsterMode);
    return cfg;
  }

  async function save() {
    clearBanners();
    try {
      const next = buildConfigForSave();
      const authTokens = cleanList(next.auth?.tokens);
      if (next.auth?.enabled && authTokens.length === 0) {
        throw new Error($t('settings.serve.authTokenRequired'));
      }
      const saved = await putJSON('/api/serve/config', next);
      if (saved?.auth?.enabled) {
        await postJSON('/api/auth/login', { password: authTokens[0] });
      }
      const text = JSON.stringify(saved, null, 2);
      lastRaw = text;
      jsonDraft = text;
      serveConfig.set(text);
      form = formFromConfig(saved);
      parseError = '';
      // Refresh status so feature flags reflect the new serve config.
      await refreshAll();
      setNotice($t('settings.serve.saved'));
    } catch (err) {
      setError(err);
    }
  }

  function addList(path) {
    if (path === 'tokens') {
      form = { ...form, api: { ...form.api, auth: { ...form.api.auth, tokens: [...form.api.auth.tokens, ''] } } };
      return;
    }
    if (path === 'origins') {
      form = { ...form, api: { ...form.api, cors: { ...form.api.cors, allowOrigins: [...form.api.cors.allowOrigins, ''] } } };
    }
  }

  function removeList(path, index) {
    if (path === 'tokens') {
      form = { ...form, api: { ...form.api, auth: { ...form.api.auth, tokens: form.api.auth.tokens.filter((_, i) => i !== index) } } };
      return;
    }
    if (path === 'origins') {
      form = { ...form, api: { ...form.api, cors: { ...form.api.cors, allowOrigins: form.api.cors.allowOrigins.filter((_, i) => i !== index) } } };
    }
  }
</script>

{#if parseError}
  <p class="settings-parse-error">{$t('settings.app.parseError', { error: parseError })}</p>
{/if}

<Card class="settings-card settings-save-card">
  <CardContent class="settings-save-content">
    <div class="settings-save-lead">
      <h2 class="settings-save-title">{$t('settings.tabs.serve')}</h2>
      <p class="settings-save-hint">{$t('settings.serve.runtimeHint')}</p>
    </div>
    <Button type="button" variant="outline" size="sm" onclick={save}>
      <Save size={14} aria-hidden="true" />
      <span>{$t('common.save')}</span>
    </Button>
  </CardContent>
</Card>

<SettingsSection title={$t('settings.serve.sections.runtime')} description={$t('settings.serve.runtimeHint')}>
  <div class="settings-form-grid">
    <SettingsField label={$t('settings.serve.listen')}>
      <Input bind:value={form.api.listen} placeholder="127.0.0.1:7872" />
    </SettingsField>
    <SettingsField label={$t('settings.serve.webuiDir')}>
      <Input bind:value={form.webUI.dir} placeholder="ui/dist" />
    </SettingsField>
    <SettingsField label={$t('settings.serve.timeout')}>
      <Input type="number" min="1" bind:value={form.api.requestTimeoutSeconds} />
    </SettingsField>
    <SettingsField label={$t('settings.serve.maxConcurrent')}>
      <Input type="number" min="0" bind:value={form.api.maxConcurrentRequests} placeholder="unlimited" />
    </SettingsField>
  </div>
</SettingsSection>

<SettingsSection title={$t('settings.serve.sections.features')} description={$t('settings.serve.featuresHint')}>
  <div class="settings-form-grid">
    <SettingsSwitch title="Web UI" bind:checked={form.features.webUI} />
    <SettingsSwitch title="OpenAI API" bind:checked={form.features.openAIAPI} />
    <SettingsSwitch title="Cron" bind:checked={form.features.cron} />
    <SettingsSwitch title="Memory" bind:checked={form.features.memory} />
    <SettingsSwitch title="Multi-agent" bind:checked={form.features.multiAgent} />
    <SettingsSwitch title="Delegate" bind:checked={form.api.enableDelegate} />
    <SettingsSwitch title="Web Search" bind:checked={form.api.enableWebSearch} />
    <SettingsSwitch title="Browser" bind:checked={form.api.enableBrowser} />
    <SettingsSwitch title={$t('settings.serve.artifact')} bind:checked={form.api.enableArtifact} />
    <SettingsSwitch title="A2A Master" bind:checked={form.api.enableA2AMaster} />
    <SettingsSwitch title="Workflows" bind:checked={form.api.enableWorkflows} />
    <SettingsSwitch title="Lobster mode" bind:checked={form.lobsterMode} />
  </div>
</SettingsSection>

<SettingsSection title={$t('settings.serve.sections.output')} description={$t('settings.serve.outputHint')}>
  <div class="settings-form-grid">
    <SettingsField label={$t('settings.serve.toolMode')}>
      <select bind:value={form.api.toolVisibility.mode} class="settings-select">
        <option value="content">content</option>
        <option value="sse_event">sse_event</option>
        <option value="none">none</option>
      </select>
    </SettingsField>
    <SettingsField label={$t('settings.serve.toolDetail')}>
      <select bind:value={form.api.toolVisibility.detail} class="settings-select">
        <option value="collapsed">collapsed</option>
        <option value="expanded">expanded</option>
      </select>
    </SettingsField>
    <SettingsField label={$t('settings.serve.systemPromptMode')}>
      <select bind:value={form.api.systemPromptMode} class="settings-select">
        <option value="append">append</option>
        <option value="ignore">ignore</option>
      </select>
    </SettingsField>
    <SettingsField label={$t('settings.serve.logLevel')}>
      <select bind:value={form.api.logLevel} class="settings-select">
        <option value="debug">debug</option>
        <option value="info">info</option>
        <option value="warn">warn</option>
        <option value="error">error</option>
      </select>
    </SettingsField>
  </div>
</SettingsSection>

<SettingsSection title={$t('settings.serve.sections.security')} description={$t('settings.serve.securityHint')}>
  <div class="settings-form-grid">
    <SettingsSwitch title={$t('settings.serve.auth')} bind:checked={form.api.auth.enabled} />
    <SettingsSwitch title={$t('settings.serve.sandbox')} bind:checked={form.api.sandbox.enabled} />
    <SettingsField label={$t('settings.serve.sandboxLevel')}>
      <select bind:value={form.api.sandbox.level} class="settings-select">
        <option value="">auto</option>
        <option value="none">none</option>
        <option value="standard">standard</option>
        <option value="strict">strict</option>
      </select>
    </SettingsField>
    <SettingsSwitch title={$t('settings.serve.smartApprovals')} bind:checked={form.security.smartApprovals} />
    <SettingsSwitch title="CORS" bind:checked={form.api.cors.enabled} />
  </div>
  <div class="settings-lists-grid">
    <ListEditor
      title={$t('settings.serve.tokens')}
      list={form.api.auth.tokens}
      type="password"
      onAdd={() => addList('tokens')}
      onRemove={(i) => removeList('tokens', i)}
    />
    <ListEditor
      title={$t('settings.serve.corsOrigins')}
      list={form.api.cors.allowOrigins}
      onAdd={() => addList('origins')}
      onRemove={(i) => removeList('origins', i)}
    />
  </div>
</SettingsSection>

<SettingsSection title={$t('settings.serve.sections.sessions')} description={$t('settings.serve.sessionsHint')}>
  <div class="settings-form-grid">
    <SettingsField label={$t('settings.serve.idleTimeout')}>
      <Input type="number" min="1" bind:value={form.api.session.idleTimeoutSeconds} />
    </SettingsField>
    <SettingsField label={$t('settings.serve.maxSessions')}>
      <Input type="number" min="0" bind:value={form.api.session.maxSessions} placeholder="unlimited" />
    </SettingsField>
  </div>
</SettingsSection>

<SettingsSection title={$t('settings.serve.sections.automation')} description={$t('settings.serve.automationHint')}>
  <div class="settings-form-grid">
    <SettingsSwitch title="Cron" bind:checked={form.features.cron} />
    <SettingsField label={$t('settings.serve.cronInterval')}>
      <Input type="number" min="1" bind:value={form.cron.interval} />
    </SettingsField>
    <SettingsSwitch title="Memory" bind:checked={form.features.memory} />
    <SettingsField label={$t('settings.serve.memoryPath')}>
      <Input bind:value={form.memory.path} placeholder=".mothx/memory.md" />
    </SettingsField>
  </div>
</SettingsSection>

<SettingsSection title={$t('settings.serve.sections.agent')} description={$t('settings.serve.agentHint')}>
  <div class="settings-form-grid">
    <SettingsField label={$t('settings.serve.maxTurns')}>
      <Input type="number" min="1" bind:value={form.agent.maxTurns} />
    </SettingsField>
    <SettingsField label={$t('settings.serve.budgetThreshold')}>
      <Input type="number" min="0" max="1" step="0.01" bind:value={form.agent.budgetPressureThreshold} />
    </SettingsField>
    <SettingsField label={$t('settings.serve.contextThreshold')}>
      <Input type="number" min="0" max="1" step="0.01" bind:value={form.agent.contextPressureThreshold} />
    </SettingsField>
    <SettingsSwitch title={$t('settings.serve.budgetPressure')} bind:checked={form.agent.budgetPressure} />
    <SettingsSwitch title={$t('settings.serve.contextPressure')} bind:checked={form.agent.contextPressure} />
  </div>
</SettingsSection>

<SettingsSection title={$t('settings.serve.sections.channels')} description={$t('settings.serve.channelsHint')}>
  <div class="settings-form-grid">
    <SettingsSwitch title={$t('settings.serve.channelArtifact')} bind:checked={form.channels.artifact} />
    <SettingsSwitch title="WeChat" bind:checked={form.features.wechat} />
    <SettingsField label={$t('settings.serve.wechatCred')}>
      <Input bind:value={form.channels.wechat.credPath} placeholder="wechat-cred.json" />
    </SettingsField>
    <SettingsField label={$t('settings.serve.wechatWorkDir')}>
      <Input bind:value={form.channels.wechat.workDir} placeholder="/home/user/project" />
    </SettingsField>
    <SettingsSwitch title={$t('settings.serve.autoTyping')} bind:checked={form.channels.wechat.autoTyping} />
    <SettingsSwitch title="Feishu" bind:checked={form.features.feishu} />
    <SettingsField label={$t('settings.serve.feishuAppID')}>
      <Input bind:value={form.channels.feishu.appID} />
    </SettingsField>
    <SettingsField label={$t('settings.serve.feishuAppSecret')}>
      <Input type="password" bind:value={form.channels.feishu.appSecret} />
    </SettingsField>
    <SettingsField label={$t('settings.serve.feishuWorkDir')}>
      <Input bind:value={form.channels.feishu.workDir} placeholder="/home/user/project" />
    </SettingsField>
  </div>
</SettingsSection>

<SettingsSection title={$t('settings.serve.sections.hooks')} description={$t('settings.serve.hooksHint')}>
  <div class="settings-form-grid">
    <SettingsField label={$t('settings.serve.preToolCall')}>
      <Input bind:value={form.hooks.preToolCall} placeholder="/path/to/pre-hook.sh" />
    </SettingsField>
    <SettingsField label={$t('settings.serve.postToolCall')}>
      <Input bind:value={form.hooks.postToolCall} placeholder="/path/to/post-hook.sh" />
    </SettingsField>
  </div>
</SettingsSection>

<Card class="settings-card settings-advanced-card">
  <details class="settings-advanced-details" bind:open={advancedOpen}>
    <summary>
      <span class="settings-advanced-title">{$t('settings.serve.advancedJson')}</span>
      <span class="settings-advanced-hint">{$t('settings.serve.advancedJsonHint')}</span>
    </summary>
    <textarea class="settings-textarea" bind:value={jsonDraft} spellcheck="false"></textarea>
  </details>
</Card>
