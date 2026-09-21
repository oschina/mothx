<script>
  import { settings, modelCatalog, setError, setNotice, clearBanners, refreshModelCatalog, resetSelectedModelToDefault, refreshAll, isMobile } from '../../lib/stores.js';
  import { postJSON, putJSON } from '../../lib/api.js';
  import { get } from 'svelte/store';
  import { t } from '../../lib/preferences.js';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Input } from '$lib/components/ui/input/index.js';

  import { Card, CardHeader, CardTitle, CardDescription, CardContent, CardAction } from '$lib/components/ui/card/index.js';
  import ListEditor from './ListEditor.svelte';
  import SearchSelect from './SearchSelect.svelte';
  import SettingsSection from './SettingsSection.svelte';
  import SettingsSwitch from './SettingsSwitch.svelte';
  import SettingsField from './SettingsField.svelte';
  import ProviderEditorDetail from './ProviderEditorDetail.svelte';
  import { Save, Plus, Trash2, X, AlertCircle, Check, Database } from '@lucide/svelte';
  import { Badge } from '$lib/components/ui/badge/index.js';

  const API_TYPE_OPTIONS = ['openai-chat', 'openai-responses', 'anthropic-messages', 'google-gemini', 'google-vertex'];

  export let section = 'app';

  let form = defaultForm();
  let jsonDraft = '{}';
  let parseError = '';
  let lastRaw = '';
  let selectedProviderID = '';
  let providerSearchTerm = '';
  let discoveredModels = [];
  let showModelPicker = false;
  let loadingDiscoveredModels = false;
  let modelTestStates = {};

  $: isProviderSettings = section === 'providers';
  $: syncFromStore($settings);
  $: currentProvider = form.providers.find((item) => item.id === selectedProviderID) || form.providers[0] || null;
  $: currentProviderInherited = inheritedModelsForProvider(currentProvider, $modelCatalog.models);
  $: filteredProviders = filterProviders(form.providers, providerSearchTerm);
  $: defaultProvider = form.providers.find((item) => item.id === form.defaults.defaultProvider) || null;
  // Default provider/model choices reuse the server-resolved catalog behind the
  // new-session model picker (GET /api/models/catalog), so built-in preset
  // providers and their default models stay selectable here too; unsaved form
  // edits are appended so they can be chosen before saving.
  $: catalogProviders = ($modelCatalog.providers || []).map(String).filter(Boolean);
  $: defaultModelOptions = mergedModelOptions(form.defaults.defaultProvider, defaultProvider, $modelCatalog.models);
  $: webSearchProviders = providersSupportingWebSearch(form.providers);
  $: webSearchProvider = form.providers.find((item) => item.id === form.webSearch.provider) || null;
  $: webSearchModelOptions = modelOptionsForProvider(webSearchProvider);
  $: webSearchProviderMissing = Boolean(form.webSearch.provider) && !webSearchProviders.some((item) => item.id === form.webSearch.provider);
  $: webSearchTypeOptions = apiTypeOptionsForProvider(webSearchProvider, form.webSearch.providerType);
  $: webSearchModelMissing = Boolean(form.webSearch.model) && !webSearchModelOptions.some((model) => model.id === form.webSearch.model);
  $: defaultProviderMissing = Boolean(form.defaults.defaultProvider)
    && !form.providers.some((item) => item.id === form.defaults.defaultProvider)
    && !catalogProviders.includes(form.defaults.defaultProvider);
  $: defaultModelMissing = Boolean(form.defaults.defaultModel) && !defaultModelOptions.some((model) => model.id === form.defaults.defaultModel);
  $: defaultProviderOptions = [
    { value: '', label: $t('common.uninitialized') },
    ...(defaultProviderMissing ? [{ value: form.defaults.defaultProvider, label: form.defaults.defaultProvider }] : []),
    ...mergedProviderOptions(form.providers, catalogProviders)
  ];
  $: defaultModelSelectOptions = [
    { value: '', label: $t('common.uninitialized') },
    ...(defaultModelMissing ? [{ value: form.defaults.defaultModel, label: form.defaults.defaultModel }] : []),
    ...defaultModelOptions.map((model) => ({
      value: model.id,
      label: model.name && model.name !== model.id ? `${model.id} - ${model.name}` : model.id
    }))
  ];

  function defaultForm() {
    return {
      defaults: {
        defaultProvider: '',
        defaultModel: '',
        defaultMode: 'yolo',
        defaultThinkingLevel: 'medium',
        theme: 'dark',
        enablePlanTool: '',
        enableArtifact: false,
        enableACPArtifact: false,
        authored: false,
        updateCheck: '',
        skillsDir: '',
        sessionDir: '',
        shellPath: '',
        shellCommandPrefix: ''
      },
      webSearch: { enabled: '', provider: '', providerType: '', model: '' },
      toolExecution: { mode: 'parallel', maxConcurrency: 10 },
      imageGeneration: { enabled: '', provider: '', apiType: '', baseUrl: '', token: '', model: '' },
      statusLine: { enabled: false, type: 'command', command: '', padding: 0, refreshInterval: '', timeoutMs: '', fallback: '' },
      contextFiles: { enabled: true, extraFiles: [] },
      compaction: {
        enabled: true,
        reserveTokens: 16384,
        keepRecentTokens: 20000,
        tokenizer: '',
        tokenizerModel: '',
        template: '',
      },
      sandbox: {
        enabled: false,
        level: 'none',
        bwrapPath: '',
        allowNetwork: false,
        allowedRead: [],
        allowedWrite: [],
        deniedPaths: [],
        passEnv: [],
        tmpSize: ''
      },
      retry: { enabled: true, maxRetries: 5, baseDelayMs: 3000 },
      approval: { bashWhitelist: [], bashBlacklist: [], confirmBeforeWrite: '' },
      providers: []
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
      if (!selectedProviderID || !form.providers.some((item) => item.id === selectedProviderID)) {
        selectedProviderID = cfg.defaultProvider || form.providers[0]?.id || '';
      }
    } catch (err) {
      parseError = err instanceof Error ? err.message : String(err);
      form = defaultForm();
      selectedProviderID = '';
    }
  }

  function formFromConfig(cfg = {}) {
    const base = defaultForm();
    return {
      defaults: {
        defaultProvider: stringValue(cfg.defaultProvider, ''),
        defaultModel: stringValue(cfg.defaultModel, ''),
        defaultMode: stringValue(cfg.defaultMode, base.defaults.defaultMode),
        defaultThinkingLevel: stringValue(cfg.defaultThinkingLevel, base.defaults.defaultThinkingLevel),
        theme: stringValue(cfg.theme, base.defaults.theme),
        enablePlanTool: triBool(cfg.enablePlanTool),
        enableArtifact: cfg.enableArtifact === true,
        enableACPArtifact: cfg.enableACPArtifact === true,
        authored: Boolean(cfg.authored),
        updateCheck: triBool(cfg.updateCheck),
        skillsDir: stringValue(cfg.skillsDir, ''),
        sessionDir: stringValue(cfg.sessionDir, ''),
        shellPath: stringValue(cfg.shellPath, ''),
        shellCommandPrefix: stringValue(cfg.shellCommandPrefix, '')
      },
      webSearch: {
        enabled: triBool(cfg.webSearch?.enabled),
        provider: stringValue(cfg.webSearch?.provider, ''),
        providerType: stringValue(cfg.webSearch?.providerType, ''),
        model: stringValue(cfg.webSearch?.model, '')
      },
      toolExecution: {
        mode: stringValue(cfg.toolExecution?.mode, base.toolExecution.mode),
        maxConcurrency: positiveNumber(cfg.toolExecution?.maxConcurrency, base.toolExecution.maxConcurrency)
      },
      imageGeneration: {
        enabled: triBool(cfg.imageGeneration?.enabled),
        provider: stringValue(cfg.imageGeneration?.provider, ''),
        apiType: stringValue(cfg.imageGeneration?.apiType, ''),
        baseUrl: stringValue(cfg.imageGeneration?.baseUrl, ''),
        token: stringValue(cfg.imageGeneration?.token, ''),
        model: stringValue(cfg.imageGeneration?.model, '')
      },
      statusLine: {
        enabled: Boolean(cfg.statusLine?.enabled),
        type: stringValue(cfg.statusLine?.type, base.statusLine.type),
        command: stringValue(cfg.statusLine?.command, ''),
        padding: numberValue(cfg.statusLine?.padding, base.statusLine.padding),
        refreshInterval: optionalNumber(cfg.statusLine?.refreshInterval),
        timeoutMs: optionalNumber(cfg.statusLine?.timeoutMs),
        fallback: stringValue(cfg.statusLine?.fallback, '')
      },
      contextFiles: {
        enabled: readBool(cfg.contextFiles?.enabled, base.contextFiles.enabled),
        extraFiles: arrayValue(cfg.contextFiles?.extraFiles)
      },
      compaction: {
        enabled: readBool(cfg.compaction?.enabled, base.compaction.enabled),
        reserveTokens: numberValue(cfg.compaction?.reserveTokens, base.compaction.reserveTokens),
        keepRecentTokens: numberValue(cfg.compaction?.keepRecentTokens, base.compaction.keepRecentTokens),
        tokenizer: stringValue(cfg.compaction?.tokenizer, ''),
        tokenizerModel: stringValue(cfg.compaction?.tokenizerModel, ''),
        template: stringValue(cfg.compaction?.template, ''),
      },
      sandbox: {
        enabled: readBool(cfg.sandbox?.enabled, base.sandbox.enabled),
        level: stringValue(cfg.sandbox?.level, base.sandbox.level),
        bwrapPath: stringValue(cfg.sandbox?.bwrapPath, ''),
        allowNetwork: Boolean(cfg.sandbox?.allowNetwork),
        allowedRead: arrayValue(cfg.sandbox?.allowedRead),
        allowedWrite: arrayValue(cfg.sandbox?.allowedWrite),
        deniedPaths: arrayValue(cfg.sandbox?.deniedPaths),
        passEnv: arrayValue(cfg.sandbox?.passEnv),
        tmpSize: stringValue(cfg.sandbox?.tmpSize, '')
      },
      retry: {
        enabled: readBool(cfg.retry?.enabled, base.retry.enabled),
        maxRetries: numberValue(cfg.retry?.maxRetries, base.retry.maxRetries),
        baseDelayMs: numberValue(cfg.retry?.baseDelayMs, base.retry.baseDelayMs)
      },
      approval: {
        bashWhitelist: arrayValue(cfg.approval?.bashWhitelist),
        bashBlacklist: arrayValue(cfg.approval?.bashBlacklist),
        confirmBeforeWrite: triBool(cfg.approval?.confirmBeforeWrite)
      },
      providers: providersFromConfig(cfg.providers || {})
    };
  }

  function providersFromConfig(providers = {}) {
    return Object.entries(providers)
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([id, provider]) => ({
        id,
        raw: { ...(provider || {}) },
        vendor: stringValue(provider?.vendor, ''),
        apiKey: stringValue(provider?.apiKey, ''),
        baseUrl: stringValue(provider?.baseUrl, ''),
        httpProxy: stringValue(provider?.httpProxy, ''),
        maxImagesPerRequest: imageLimitValue(provider?.maxImagesPerRequest),
        forceHTTP11: Boolean(provider?.forceHTTP11),
        api: stringValue(provider?.api, ''),
        thinkingFormat: stringValue(provider?.thinkingFormat, ''),
        cacheControl: triBool(provider?.cacheControl),
        headers: mapToPairs(provider?.headers),
        responses: {
          reasoningSummary: stringValue(provider?.responses?.reasoningSummary, ''),
          promptCacheEnabled: triBool(provider?.responses?.promptCacheEnabled),
          promptCacheKey: stringValue(provider?.responses?.promptCacheKey, ''),
          promptCacheRetention: stringValue(provider?.responses?.promptCacheRetention, ''),
          toolChoice: stringValue(provider?.responses?.toolControl?.choice, ''),
          toolParallel: triBool(provider?.responses?.toolControl?.parallel),
          toolMaxCalls: optionalNumber(provider?.responses?.toolControl?.maxCalls)
        },
        models: arrayValue(provider?.models).map((model) => modelFromConfig(model)).filter((model) => model.id || model.name)
      }));
  }

  function modelFromConfig(model = {}) {
    return {
      raw: { ...(model || {}) },
      id: stringValue(model?.id, ''),
      name: stringValue(model?.name, ''),
      reasoning: Boolean(model?.reasoning),
      contextWindow: optionalNumber(model?.contextWindow),
      maxTokens: optionalNumber(model?.maxTokens),
      input: arrayValue(model?.input).join(', '),
      temperature: optionalNumber(model?.temperature),
      topP: optionalNumber(model?.top_p),
      allowSampling: model?.compat?.disableSamplingParams === false,
      supportsToolChoice: triBool(model?.compat?.supportsToolChoice),
      supportsParallelToolCalls: triBool(model?.compat?.supportsParallelToolCalls)
    };
  }

  function buildConfigForSave() {
    const cfg = JSON.parse(jsonDraft || '{}');

    if (isProviderSettings) {
      cfg.defaultProvider = form.defaults.defaultProvider.trim();
      cfg.defaultModel = form.defaults.defaultModel.trim();
      cfg.defaultThinkingLevel = form.defaults.defaultThinkingLevel || 'medium';
      cfg.providers = providersToConfig(form.providers);
      return cfg;
    }

    cfg.defaultMode = form.defaults.defaultMode || 'yolo';
    cfg.theme = form.defaults.theme || 'dark';
    writeTriBool(cfg, 'enablePlanTool', form.defaults.enablePlanTool);
    cfg.enableArtifact = Boolean(form.defaults.enableArtifact);
    cfg.enableACPArtifact = Boolean(form.defaults.enableACPArtifact);
    cfg.authored = Boolean(form.defaults.authored);
    writeTriBool(cfg, 'updateCheck', form.defaults.updateCheck);
    writeString(cfg, 'skillsDir', form.defaults.skillsDir);
    writeString(cfg, 'sessionDir', form.defaults.sessionDir);
    writeString(cfg, 'shellPath', form.defaults.shellPath);
    writeString(cfg, 'shellCommandPrefix', form.defaults.shellCommandPrefix);

    cfg.webSearch = ensureObject(cfg, 'webSearch');
    writeTriBool(cfg.webSearch, 'enabled', form.webSearch.enabled);
    writeString(cfg.webSearch, 'provider', form.webSearch.provider);
    writeString(cfg.webSearch, 'providerType', form.webSearch.providerType);
    writeString(cfg.webSearch, 'model', form.webSearch.model);

    cfg.toolExecution = ensureObject(cfg, 'toolExecution');
    cfg.toolExecution.mode = ['parallel', 'sequential'].includes(form.toolExecution.mode)
      ? form.toolExecution.mode
      : 'parallel';
    cfg.toolExecution.maxConcurrency = positiveNumber(form.toolExecution.maxConcurrency, 10);

    cfg.imageGeneration = ensureObject(cfg, 'imageGeneration');
    writeTriBool(cfg.imageGeneration, 'enabled', form.imageGeneration.enabled);
    writeString(cfg.imageGeneration, 'provider', form.imageGeneration.provider);
    writeString(cfg.imageGeneration, 'apiType', form.imageGeneration.apiType);
    writeString(cfg.imageGeneration, 'baseUrl', form.imageGeneration.baseUrl);
    writeString(cfg.imageGeneration, 'token', form.imageGeneration.token);
    writeString(cfg.imageGeneration, 'model', form.imageGeneration.model);

    cfg.statusLine = ensureObject(cfg, 'statusLine');
    cfg.statusLine.enabled = Boolean(form.statusLine.enabled);
    writeString(cfg.statusLine, 'type', form.statusLine.type);
    writeString(cfg.statusLine, 'command', form.statusLine.command);
    cfg.statusLine.padding = numberValue(form.statusLine.padding, 0);
    writeOptionalNumber(cfg.statusLine, 'refreshInterval', form.statusLine.refreshInterval);
    writeOptionalNumber(cfg.statusLine, 'timeoutMs', form.statusLine.timeoutMs);
    writeString(cfg.statusLine, 'fallback', form.statusLine.fallback);

    cfg.contextFiles = ensureObject(cfg, 'contextFiles');
    cfg.contextFiles.enabled = Boolean(form.contextFiles.enabled);
    writeList(cfg.contextFiles, 'extraFiles', form.contextFiles.extraFiles);

    cfg.compaction = ensureObject(cfg, 'compaction');
    cfg.compaction.enabled = Boolean(form.compaction.enabled);
    cfg.compaction.reserveTokens = numberValue(form.compaction.reserveTokens, 0);
    cfg.compaction.keepRecentTokens = numberValue(form.compaction.keepRecentTokens, 0);
    writeString(cfg.compaction, 'tokenizer', form.compaction.tokenizer);
    writeString(cfg.compaction, 'tokenizerModel', form.compaction.tokenizerModel);
    writeString(cfg.compaction, 'template', form.compaction.template);

    cfg.sandbox = ensureObject(cfg, 'sandbox');
    cfg.sandbox.enabled = Boolean(form.sandbox.enabled);
    cfg.sandbox.level = form.sandbox.level || 'none';
    cfg.sandbox.allowNetwork = Boolean(form.sandbox.allowNetwork);
    writeString(cfg.sandbox, 'bwrapPath', form.sandbox.bwrapPath);
    writeString(cfg.sandbox, 'tmpSize', form.sandbox.tmpSize);
    writeList(cfg.sandbox, 'allowedRead', form.sandbox.allowedRead);
    writeList(cfg.sandbox, 'allowedWrite', form.sandbox.allowedWrite);
    writeList(cfg.sandbox, 'deniedPaths', form.sandbox.deniedPaths);
    writeList(cfg.sandbox, 'passEnv', form.sandbox.passEnv);

    cfg.retry = ensureObject(cfg, 'retry');
    cfg.retry.enabled = Boolean(form.retry.enabled);
    cfg.retry.maxRetries = numberValue(form.retry.maxRetries, 0);
    cfg.retry.baseDelayMs = numberValue(form.retry.baseDelayMs, 0);

    cfg.approval = ensureObject(cfg, 'approval');
    writeList(cfg.approval, 'bashWhitelist', form.approval.bashWhitelist);
    writeList(cfg.approval, 'bashBlacklist', form.approval.bashBlacklist);
    writeTriBool(cfg.approval, 'confirmBeforeWrite', form.approval.confirmBeforeWrite);

    return cfg;
  }

  function providersToConfig(providers = []) {
    const out = {};
    for (const provider of providers) {
      const id = provider.id.trim();
      if (!id) continue;
      const raw = { ...(provider.raw || {}) };
      writeString(raw, 'vendor', provider.vendor);
      writeString(raw, 'apiKey', provider.apiKey);
      writeString(raw, 'baseUrl', provider.baseUrl);
      writeString(raw, 'httpProxy', provider.httpProxy);
      writeImageLimit(raw, 'maxImagesPerRequest', provider.maxImagesPerRequest);
      if (provider.forceHTTP11) raw.forceHTTP11 = true;
      else delete raw.forceHTTP11;
      writeString(raw, 'api', provider.api);
      writeString(raw, 'thinkingFormat', provider.thinkingFormat);
      writeTriBool(raw, 'cacheControl', provider.cacheControl);
      writeMap(raw, 'headers', provider.headers);
      raw.responses = ensureObject(raw, 'responses');
      writeString(raw.responses, 'reasoningSummary', provider.responses.reasoningSummary);
      writeTriBool(raw.responses, 'promptCacheEnabled', provider.responses.promptCacheEnabled);
      writeString(raw.responses, 'promptCacheKey', provider.responses.promptCacheKey);
      writeString(raw.responses, 'promptCacheRetention', provider.responses.promptCacheRetention);
      const toolControl = ensureObject(raw.responses, 'toolControl');
      writeString(toolControl, 'choice', provider.responses.toolChoice);
      writeTriBool(toolControl, 'parallel', provider.responses.toolParallel);
      writeOptionalNumber(toolControl, 'maxCalls', provider.responses.toolMaxCalls);
      if (Object.keys(toolControl).length === 0) delete raw.responses.toolControl;
      if (Object.keys(raw.responses).length === 0) delete raw.responses;
      raw.models = provider.models.map(modelToConfig).filter((model) => model.id);
      out[id] = raw;
    }
    return out;
  }

  function modelToConfig(model) {
    const raw = { ...(model.raw || {}) };
    raw.id = model.id.trim();
    raw.name = model.name.trim() || raw.id;
    if (model.reasoning) raw.reasoning = true;
    else delete raw.reasoning;
    writeOptionalNumber(raw, 'contextWindow', model.contextWindow);
    writeOptionalNumber(raw, 'maxTokens', model.maxTokens);
    const input = csvList(model.input);
    if (input.length > 0) raw.input = input;
    else delete raw.input;
    writeOptionalFloat(raw, 'temperature', model.temperature);
    writeOptionalFloat(raw, 'top_p', model.topP);
    // Compat flags are copied so editing one never mutates the loaded raw object.
    const compat = raw.compat && typeof raw.compat === 'object' && !Array.isArray(raw.compat) ? { ...raw.compat } : {};
    writeTriBool(compat, 'disableSamplingParams', model.allowSampling ? 'false' : '');
    writeTriBool(compat, 'supportsToolChoice', model.supportsToolChoice);
    writeTriBool(compat, 'supportsParallelToolCalls', model.supportsParallelToolCalls);
    if (Object.keys(compat).length > 0) raw.compat = compat;
    else delete raw.compat;
    return raw;
  }

  async function save() {
    clearBanners();
    try {
      const next = buildConfigForSave();
      const saved = await putJSON('/api/settings', next);
      settings.set(JSON.stringify(saved, null, 2));
      if (isProviderSettings) {
        await refreshModelCatalog();
        resetSelectedModelToDefault();
      }
      await refreshAll();
      setNotice($t(isProviderSettings ? 'settings.providers.saved' : 'settings.app.saved'));
    } catch (err) {
      setError(err);
    }
  }

  function addProvider() {
    const id = uniqueProviderID('provider');
    form.providers = [...form.providers, {
      id,
      raw: {},
      vendor: '',
      apiKey: '',
      baseUrl: '',
      httpProxy: '',
      maxImagesPerRequest: '',
      forceHTTP11: false,
      api: 'openai-chat',
      thinkingFormat: '',
      cacheControl: '',
      headers: [],
      responses: { reasoningSummary: '', promptCacheEnabled: '', promptCacheKey: '', promptCacheRetention: '', toolChoice: '', toolParallel: '', toolMaxCalls: '' },
      models: []
    }];
    selectedProviderID = id;
  }

  function removeProvider(provider) {
    form.providers = form.providers.filter((item) => item !== provider);
    if (selectedProviderID === provider.id) selectedProviderID = form.providers[0]?.id || '';
  }

  function selectDefaultProvider(value) {
    form.defaults.defaultProvider = value;
    const provider = form.providers.find((item) => item.id === value) || null;
    const models = mergedModelOptions(value, provider, get(modelCatalog)?.models);
    if (!models.some((model) => model.id === form.defaults.defaultModel)) {
      form.defaults.defaultModel = models[0]?.id || '';
    }
    form = form;
  }

  function selectDefaultModel(value) {
    form.defaults.defaultModel = value;
    form = form;
  }

  function selectWebSearchProvider(value) {
    form.webSearch.provider = value;
    const provider = form.providers.find((item) => item.id === value) || null;
    form.webSearch.providerType = provider?.api || '';
    const models = modelOptionsForProvider(provider);
    if (!models.some((model) => model.id === form.webSearch.model)) {
      form.webSearch.model = models[0]?.id || '';
    }
    form = form;
  }

  function selectWebSearchType(value) {
    form.webSearch.providerType = value;
    form = form;
  }

  function selectWebSearchModel(value) {
    form.webSearch.model = value;
    form = form;
  }

  function renameProvider(provider, value) {
    provider.id = value.trim();
    selectedProviderID = provider.id;
    form = form;
  }

  function addModel(provider) {
    const defaults = newModelDefaults();
    provider.models = [...provider.models, {
      raw: {},
      id: '',
      name: '',
      reasoning: defaults.reasoning,
      contextWindow: defaults.contextWindow,
      maxTokens: defaults.maxTokens,
      input: defaults.input,
      temperature: '',
      topP: '',
      allowSampling: false,
      supportsToolChoice: '',
      supportsParallelToolCalls: ''
    }];
    form = form;
  }

  function newModelDefaults() {
    const defaults = get(modelCatalog)?.modelDefaults || {};
    return {
      reasoning: defaults.reasoning !== false,
      contextWindow: Number(defaults.contextWindow) > 0 ? defaults.contextWindow : 256000,
      maxTokens: Number(defaults.maxTokens) > 0 ? defaults.maxTokens : '',
      input: Array.isArray(defaults.input) && defaults.input.length ? defaults.input.join(', ') : 'text'
    };
  }

  function applyModelIDPreset(provider, model) {
    const id = String(model?.id || '').trim();
    // A non-empty name marks an existing/discovered model or a row the user
    // already customized. Only seed a newly entered ID.
    if (!id || String(model?.name || '').trim()) return;
    const catalog = get(modelCatalog) || {};
    const models = Array.isArray(catalog.models) ? catalog.models : [];
    const preset = models.find((item) => item?.provider === provider.id && item?.id === id)
      || models.find((item) => item?.id === id)
      || catalog.modelDefaults
      || {};
    model.name = String(preset.name || id);
    model.reasoning = preset.reasoning !== false;
    model.contextWindow = Number(preset.contextWindow) > 0 ? preset.contextWindow : 256000;
    model.maxTokens = Number(preset.maxTokens) > 0 ? preset.maxTokens : '';
    model.input = Array.isArray(preset.input) && preset.input.length ? preset.input.join(', ') : 'text';
    form = form;
  }

  function removeModel(provider, index) {
    provider.models = provider.models.filter((_, i) => i !== index);
    modelTestStates = {};
    form = form;
  }

  async function fetchProviderModels(provider) {
    clearBanners();
    if (!provider?.baseUrl?.trim() || !provider?.api?.trim()) {
      setError($t('settings.app.modelsFetchRequired'));
      return;
    }
    loadingDiscoveredModels = true;
    discoveredModels = [];
    showModelPicker = true;
    try {
      const result = await postJSON('/api/provider/models', providerProbePayload(provider));
      discoveredModels = Array.isArray(result?.data) ? result.data : [];
      if (discoveredModels.length === 0) setError($t('settings.app.modelsFetchEmpty'));
    } catch (err) {
      showModelPicker = false;
      setError(err);
    } finally {
      loadingDiscoveredModels = false;
    }
  }

  function addDiscoveredModel(provider, discovered) {
    const id = String(discovered?.id || '').trim();
    if (!id || provider.models.some((model) => model.id.trim() === id)) return;
    const nextProvider = {
      ...provider,
      models: [...provider.models, {
        raw: {},
        id,
        name: String(discovered.name || id),
        reasoning: Boolean(discovered.reasoning),
        contextWindow: Number(discovered.contextWindow) > 0 ? discovered.contextWindow : '',
        maxTokens: Number(discovered.maxTokens) > 0 ? discovered.maxTokens : '',
        input: Array.isArray(discovered.input) && discovered.input.length ? discovered.input.join(', ') : 'text',
        temperature: '',
        topP: '',
        allowSampling: false,
        supportsToolChoice: '',
        supportsParallelToolCalls: ''
      }]
    };
    form.providers = form.providers.map((p) => (p === provider ? nextProvider : p));
  }

  function discoveredModelAdded(provider, discovered) {
    const id = String(discovered?.id || '').trim();
    return Boolean(id && provider.models.some((model) => model.id.trim() === id));
  }

  async function testProviderModel(provider, model, index) {
    const key = `${provider.id}:${index}`;
    modelTestStates = { ...modelTestStates, [key]: { loading: true } };
    try {
      const result = await postJSON('/api/provider/test', { ...providerProbePayload(provider), model: model.id.trim() });
      modelTestStates = { ...modelTestStates, [key]: { ok: result?.ok === true, message: $t('settings.app.modelTestPassed') } };
    } catch (err) {
      modelTestStates = { ...modelTestStates, [key]: { ok: false, message: err instanceof Error ? err.message : String(err) } };
    }
  }

  function providerProbePayload(provider) {
    return {
      api: provider.api,
      baseUrl: provider.baseUrl,
      apiKey: provider.apiKey,
      httpProxy: provider.httpProxy,
      forceHTTP11: provider.forceHTTP11,
      headers: pairsToMap(provider.headers)
    };
  }

  function addHeader(provider) {
    provider.headers = [...provider.headers, { key: '', value: '' }];
    form = form;
  }

  function removeHeader(provider, index) {
    provider.headers = provider.headers.filter((_, i) => i !== index);
    form = form;
  }

  function addListItem(list) {
    list.push('');
    form = form;
  }

  function removeListItem(list, index) {
    list.splice(index, 1);
    form = form;
  }

  function filterProviders(providers = [], term = '') {
    const query = term.trim().toLowerCase();
    if (!query) return providers;
    return providers.filter((provider) => {
      const hay = `${provider.id || ''} ${provider.vendor || ''} ${provider.baseUrl || ''} ${provider.api || ''}`.toLowerCase();
      return hay.includes(query);
    });
  }

  function providersSupportingWebSearch(providers = []) {
    return providers.filter((provider) => supportsWebSearchAPI(provider?.api));
  }

  function supportsWebSearchAPI(api) {
    return api === 'openai-responses' || api === 'anthropic-messages';
  }

  function modelOptionsForProvider(provider) {
    const seen = new Set();
    const models = [];
    for (const model of provider?.models || []) {
      const id = String(model?.id || '').trim();
      if (!id || seen.has(id)) continue;
      seen.add(id);
      models.push({ id, name: String(model?.name || '').trim() });
    }
    return models;
  }

  function mergedProviderOptions(providers, catalogProviderIDs) {
    const seen = new Set();
    const options = [];
    const add = (id) => {
      const value = String(id || '').trim();
      if (!value || seen.has(value)) return;
      seen.add(value);
      options.push({ value, label: value });
    };
    for (const provider of providers || []) add(provider?.id);
    for (const id of catalogProviderIDs || []) add(id);
    return options;
  }

  // Catalog models come first so the dropdown matches the new-session picker
  // order; models that only exist in the unsaved form are appended after them.
  function mergedModelOptions(providerID, provider, catalogModels) {
    const id = String(providerID || '').trim();
    const seen = new Set();
    const options = [];
    if (id) {
      for (const model of catalogModels || []) {
        if (String(model?.provider || '').trim() !== id) continue;
        const modelID = String(model?.id || '').trim();
        if (!modelID || seen.has(modelID)) continue;
        seen.add(modelID);
        options.push({ id: modelID, name: String(model?.name || '').trim() });
      }
    }
    for (const model of modelOptionsForProvider(provider)) {
      if (seen.has(model.id)) continue;
      seen.add(model.id);
      options.push(model);
    }
    return options;
  }

  // Catalog-only models (typically built-in preset defaults) that are not
  // explicitly configured for this provider. They keep the provider settings
  // list consistent with the new-session picker; they render read-only and are
  // never persisted because only provider.models is serialized on save.
  function inheritedModelsForProvider(provider, catalogModels) {
    const providerID = String(provider?.id || '').trim();
    if (!providerID) return [];
    const configured = new Set();
    for (const model of provider?.models || []) {
      const modelID = String(model?.id || '').trim();
      if (modelID) configured.add(modelID);
    }
    const seen = new Set();
    const inherited = [];
    for (const model of catalogModels || []) {
      if (String(model?.provider || '').trim() !== providerID) continue;
      const modelID = String(model?.id || '').trim();
      if (!modelID || configured.has(modelID) || seen.has(modelID)) continue;
      seen.add(modelID);
      inherited.push({
        id: modelID,
        name: String(model?.name || '').trim(),
        input: Array.isArray(model?.input) ? model.input.join(', ') : String(model?.input || '').trim()
      });
    }
    return inherited;
  }

  function apiTypeOptionsForProvider(provider, current = '') {
    const seen = new Set();
    const options = [];
    for (const value of [provider?.api, current, ...API_TYPE_OPTIONS]) {
      const id = String(value || '').trim();
      if (!id || seen.has(id)) continue;
      seen.add(id);
      options.push(id);
    }
    return options;
  }

  function uniqueProviderID(prefix) {
    let n = 1;
    let id = prefix;
    const used = new Set(form.providers.map((item) => item.id));
    while (used.has(id)) {
      n += 1;
      id = `${prefix}-${n}`;
    }
    return id;
  }

  function triBool(value) {
    if (typeof value !== 'boolean') return '';
    return value ? 'true' : 'false';
  }

  function boolFromTri(value) {
    if (value === 'true') return true;
    if (value === 'false') return false;
    return undefined;
  }

  function stringValue(value, fallback = '') {
    return typeof value === 'string' ? value : fallback;
  }

  function numberValue(value, fallback = 0) {
    const n = Number(value);
    return Number.isFinite(n) ? n : fallback;
  }

  function positiveNumber(value, fallback = 1) {
    const n = Number(value);
    return Number.isFinite(n) && n > 0 ? Math.floor(n) : fallback;
  }

  function optionalNumber(value) {
    const n = Number(value);
    return Number.isFinite(n) && n > 0 ? n : '';
  }

  function imageLimitValue(value) {
    if (value === '' || value === null || value === undefined) return '';
    const n = Number(value);
    return Number.isInteger(n) && n >= -1 ? n : '';
  }

  function readBool(...values) {
    for (const value of values) {
      if (typeof value === 'boolean') return value;
    }
    return Boolean(values[values.length - 1]);
  }

  function arrayValue(value, fallback = []) {
    if (!Array.isArray(value)) return [...fallback];
    return value.map((item) => item && typeof item === 'object' ? item : String(item ?? ''));
  }

  function mapToPairs(value) {
    if (!value || typeof value !== 'object' || Array.isArray(value)) return [];
    return Object.entries(value).map(([key, val]) => ({ key, value: String(val ?? '') }));
  }

  function cleanList(values = []) {
    return values.map((item) => String(item || '').trim()).filter(Boolean);
  }

  function csvList(value = '') {
    return String(value || '').split(',').map((item) => item.trim()).filter(Boolean);
  }

  function pairsToMap(pairs = []) {
    const out = {};
    for (const pair of pairs) {
      const key = String(pair?.key || '').trim();
      if (key) out[key] = String(pair?.value || '');
    }
    return out;
  }

  function ensureObject(parent, key) {
    if (!parent[key] || typeof parent[key] !== 'object' || Array.isArray(parent[key])) {
      parent[key] = {};
    }
    return parent[key];
  }

  function writeString(target, key, value) {
    const text = String(value || '').trim();
    if (text) target[key] = text;
    else delete target[key];
  }

  function writeTriBool(target, key, value) {
    const bool = boolFromTri(value);
    if (bool === undefined) delete target[key];
    else target[key] = bool;
  }

  function writeOptionalNumber(target, key, value) {
    const n = Number(value);
    if (Number.isFinite(n) && n > 0) target[key] = n;
    else delete target[key];
  }

  function writeImageLimit(target, key, value) {
    if (value === '' || value === null || value === undefined) {
      delete target[key];
      return;
    }
    const n = Number(value);
    if (Number.isInteger(n) && n >= -1) target[key] = n;
    else delete target[key];
  }

  function writeOptionalFloat(target, key, value) {
    const n = Number(value);
    if (Number.isFinite(n)) target[key] = n;
    else delete target[key];
  }

  function writeList(target, key, values) {
    const list = cleanList(values);
    if (list.length > 0) target[key] = list;
    else delete target[key];
  }

  function writeMap(target, key, pairs) {
    const out = {};
    for (const pair of pairs || []) {
      const k = String(pair.key || '').trim();
      if (!k) continue;
      out[k] = String(pair.value || '');
    }
    if (Object.keys(out).length > 0) target[key] = out;
    else delete target[key];
  }
</script>

<Card class="settings-card settings-save-card">
  <CardContent class="settings-save-content">
    <div class="settings-save-lead">
      <h2 class="settings-save-title">{$t(isProviderSettings ? 'settings.tabs.providers' : 'settings.tabs.app')}</h2>
      <p class="settings-save-hint">{$t(isProviderSettings ? 'settings.providers.hint' : 'settings.app.hint')}</p>
    </div>
    <Button variant="outline" size="sm" type="button" onclick={save}>
      <Save size={14} aria-hidden="true" />
      <span>{$t('common.save')}</span>
    </Button>
  </CardContent>
</Card>

{#if parseError}
  <p class="settings-parse-error">
    <AlertCircle size={14} aria-hidden="true" />
    <span>{$t('settings.app.parseError', { error: parseError })}</span>
  </p>
{/if}

{#if !isProviderSettings}
  <SettingsSection title={$t('settings.app.sections.defaults')} description={$t('settings.app.defaultsHint')}>
    <div class="settings-form-grid">
      <SettingsField label={$t('settings.app.defaultMode')}>
        <select bind:value={form.defaults.defaultMode} class="settings-select">
          <option value="plan">plan</option>
          <option value="agent">agent</option>
          <option value="yolo">yolo</option>
          <option value="os">os</option>
        </select>
      </SettingsField>
      <SettingsField label={$t('settings.app.enablePlanTool')}>
        <select bind:value={form.defaults.enablePlanTool} class="settings-select">
          <option value="">{$t('common.uninitialized')}</option>
          <option value="true">{$t('common.enabled')}</option>
          <option value="false">{$t('common.disabled')}</option>
        </select>
      </SettingsField>
      <SettingsSwitch title={$t('settings.app.enableArtifact')} bind:checked={form.defaults.enableArtifact} />
      <SettingsSwitch title={$t('settings.app.enableACPArtifact')} bind:checked={form.defaults.enableACPArtifact} />
      <SettingsSwitch title={$t('settings.app.authored')} bind:checked={form.defaults.authored} />
      <SettingsField label={$t('settings.app.updateCheck')}>
        <select bind:value={form.defaults.updateCheck} class="settings-select">
          <option value="">{$t('common.uninitialized')}</option>
          <option value="true">{$t('common.enabled')}</option>
          <option value="false">{$t('common.disabled')}</option>
        </select>
      </SettingsField>
      <SettingsField label={$t('settings.app.skillsDir')} className="full">
        <Input bind:value={form.defaults.skillsDir} />
      </SettingsField>
      <SettingsField label={$t('settings.app.sessionDir')} className="full">
        <Input bind:value={form.defaults.sessionDir} />
      </SettingsField>
      <SettingsField label={$t('settings.app.shellPath')} className="full">
        <Input bind:value={form.defaults.shellPath} />
      </SettingsField>
      <SettingsField label={$t('settings.app.shellPrefix')} className="full">
        <Input bind:value={form.defaults.shellCommandPrefix} />
      </SettingsField>
    </div>
  </SettingsSection>

  <SettingsSection title={$t('settings.app.sections.context')} description={$t('settings.app.contextHint')}>
    <div class="settings-form-grid">
      <SettingsSwitch title={$t('settings.app.contextFiles')} bind:checked={form.contextFiles.enabled} />
      <SettingsSwitch title={$t('settings.app.compaction')} bind:checked={form.compaction.enabled} />
      <SettingsField label={$t('settings.app.reserveTokens')}>
        <Input type="number" min="0" bind:value={form.compaction.reserveTokens} />
      </SettingsField>
      <SettingsField label={$t('settings.app.keepRecentTokens')}>
        <Input type="number" min="0" bind:value={form.compaction.keepRecentTokens} />
      </SettingsField>
      <SettingsField label={$t('settings.app.tokenizer')}>
        <Input bind:value={form.compaction.tokenizer} />
      </SettingsField>
      <SettingsField label={$t('settings.app.tokenizerModel')}>
        <Input bind:value={form.compaction.tokenizerModel} />
      </SettingsField>
      <SettingsField label={$t('settings.app.compactionTemplate')} className="full">
        <textarea class="settings-textarea" bind:value={form.compaction.template}></textarea>
      </SettingsField>
      <div class="full">
        <ListEditor
          title={$t('settings.app.extraFiles')}
          list={form.contextFiles.extraFiles}
          onAdd={() => addListItem(form.contextFiles.extraFiles)}
          onRemove={(i) => removeListItem(form.contextFiles.extraFiles, i)}
        />
      </div>
    </div>
  </SettingsSection>

  <SettingsSection title={$t('settings.app.sections.tools')} description={$t('settings.app.toolsHint')}>
    <div class="settings-form-grid">
      <SettingsField label={$t('settings.app.webSearch')}>
        <select bind:value={form.webSearch.enabled} class="settings-select">
          <option value="">{$t('common.uninitialized')}</option>
          <option value="true">{$t('common.enabled')}</option>
          <option value="false">{$t('common.disabled')}</option>
        </select>
      </SettingsField>
      <SettingsField label={$t('settings.app.webSearchProvider')}>
        <select value={form.webSearch.provider} on:change={(event) => selectWebSearchProvider(event.currentTarget.value)} class="settings-select">
          <option value="">{$t('common.uninitialized')}</option>
          {#if webSearchProviderMissing}
            <option value={form.webSearch.provider}>{form.webSearch.provider}</option>
          {/if}
          {#each webSearchProviders as provider}
            <option value={provider.id}>{provider.id}</option>
          {/each}
        </select>
      </SettingsField>
      <SettingsField label={$t('settings.app.webSearchType')}>
        <select
          value={form.webSearch.providerType}
          disabled={webSearchTypeOptions.length === 0 && !form.webSearch.providerType}
          on:change={(event) => selectWebSearchType(event.currentTarget.value)}
          class="settings-select"
        >
          <option value="">{$t('common.uninitialized')}</option>
          {#each webSearchTypeOptions as apiType}
            <option value={apiType}>{apiType}</option>
          {/each}
        </select>
      </SettingsField>
      <SettingsField label={$t('settings.app.webSearchModel')}>
        <select
          value={form.webSearch.model}
          disabled={webSearchModelOptions.length === 0 && !form.webSearch.model}
          on:change={(event) => selectWebSearchModel(event.currentTarget.value)}
          class="settings-select"
        >
          <option value="">{$t('common.uninitialized')}</option>
          {#if webSearchModelMissing}
            <option value={form.webSearch.model}>{form.webSearch.model}</option>
          {/if}
          {#each webSearchModelOptions as model}
            <option value={model.id}>{model.name && model.name !== model.id ? `${model.id} - ${model.name}` : model.id}</option>
          {/each}
        </select>
      </SettingsField>
      <SettingsField label={$t('settings.app.toolExecutionMode')}>
        <select bind:value={form.toolExecution.mode} class="settings-select">
          <option value="parallel">{$t('settings.app.toolExecutionMode.parallel')}</option>
          <option value="sequential">{$t('settings.app.toolExecutionMode.sequential')}</option>
        </select>
        <span class="settings-field-hint">{$t('settings.app.toolExecutionModeHint')}</span>
      </SettingsField>
      <SettingsField label={$t('settings.app.toolExecutionMaxConcurrency')}>
        <Input type="number" min="1" step="1" bind:value={form.toolExecution.maxConcurrency} />
      </SettingsField>
    </div>
  </SettingsSection>

  <SettingsSection title={$t('settings.app.sections.imageGeneration')} description={$t('settings.app.imageGenerationHint')}>
    <div class="settings-form-grid">
      <SettingsField label={$t('settings.app.imageGeneration.enabled')}>
        <select bind:value={form.imageGeneration.enabled} class="settings-select">
          <option value="">{$t('common.uninitialized')}</option>
          <option value="true">{$t('common.enabled')}</option>
          <option value="false">{$t('common.disabled')}</option>
        </select>
      </SettingsField>
      <SettingsField label={$t('settings.app.imageGeneration.provider')}>
        <Input bind:value={form.imageGeneration.provider} placeholder="openai" />
      </SettingsField>
      <SettingsField label={$t('settings.app.imageGeneration.apiType')}>
        <select bind:value={form.imageGeneration.apiType} class="settings-select">
          <option value="">openai-images</option>
          <option value="openai-images">openai-images</option>
          <option value="openai-responses">openai-responses</option>
        </select>
      </SettingsField>
      <SettingsField label={$t('settings.app.imageGeneration.baseUrl')}>
        <Input bind:value={form.imageGeneration.baseUrl} placeholder="https://api.openai.com/v1" />
      </SettingsField>
      <SettingsField label={$t('settings.app.imageGeneration.token')}>
        <Input type="password" bind:value={form.imageGeneration.token} placeholder={'${OPENAI_API_KEY}'} />
      </SettingsField>
      <SettingsField label={$t('settings.app.imageGeneration.model')}>
        <Input bind:value={form.imageGeneration.model} placeholder="gpt-image-1" />
      </SettingsField>
    </div>
  </SettingsSection>

  <SettingsSection title={$t('settings.app.sections.runtime')} description={$t('settings.app.runtimeHint')}>
    <div class="settings-form-grid">
      <SettingsSwitch title={$t('settings.app.retry')} bind:checked={form.retry.enabled} />
      <SettingsField label={$t('settings.app.maxRetries')}>
        <Input type="number" min="0" bind:value={form.retry.maxRetries} />
      </SettingsField>
      <SettingsField label={$t('settings.app.baseDelay')}>
        <Input type="number" min="0" bind:value={form.retry.baseDelayMs} />
      </SettingsField>
      <SettingsSwitch title={$t('settings.app.statusLine')} bind:checked={form.statusLine.enabled} />
      <SettingsField label={$t('settings.app.statusLineType')}>
        <Input bind:value={form.statusLine.type} />
      </SettingsField>
      <SettingsField label={$t('settings.app.statusLineCommand')}>
        <Input bind:value={form.statusLine.command} />
      </SettingsField>
      <SettingsField label={$t('settings.app.statusLineTimeout')}>
        <Input type="number" min="0" bind:value={form.statusLine.timeoutMs} />
      </SettingsField>
      <SettingsField label={$t('settings.app.statusLineFallback')}>
        <Input bind:value={form.statusLine.fallback} />
      </SettingsField>
    </div>
  </SettingsSection>

  <SettingsSection title={$t('settings.app.sections.safety')} description={$t('settings.app.safetyHint')}>
    <div class="settings-form-grid">
      <SettingsSwitch title={$t('settings.app.sandbox')} bind:checked={form.sandbox.enabled} />
      <SettingsSwitch title={$t('settings.app.allowNetwork')} bind:checked={form.sandbox.allowNetwork} />
      <SettingsField label={$t('settings.app.sandboxLevel')}>
        <Input bind:value={form.sandbox.level} />
      </SettingsField>
      <SettingsField label={$t('settings.app.bwrapPath')}>
        <Input bind:value={form.sandbox.bwrapPath} />
      </SettingsField>
      <SettingsField label={$t('settings.app.tmpSize')}>
        <Input bind:value={form.sandbox.tmpSize} />
      </SettingsField>
      <SettingsField label={$t('settings.app.confirmBeforeWrite')}>
        <select bind:value={form.approval.confirmBeforeWrite} class="settings-select">
          <option value="">{$t('common.uninitialized')}</option>
          <option value="true">{$t('common.enabled')}</option>
          <option value="false">{$t('common.disabled')}</option>
        </select>
      </SettingsField>
    </div>
    <div class="settings-lists-grid">
      <ListEditor title={$t('settings.app.allowedRead')} list={form.sandbox.allowedRead} onAdd={() => addListItem(form.sandbox.allowedRead)} onRemove={(i) => removeListItem(form.sandbox.allowedRead, i)} />
      <ListEditor title={$t('settings.app.allowedWrite')} list={form.sandbox.allowedWrite} onAdd={() => addListItem(form.sandbox.allowedWrite)} onRemove={(i) => removeListItem(form.sandbox.allowedWrite, i)} />
      <ListEditor title={$t('settings.app.deniedPaths')} list={form.sandbox.deniedPaths} onAdd={() => addListItem(form.sandbox.deniedPaths)} onRemove={(i) => removeListItem(form.sandbox.deniedPaths, i)} />
      <ListEditor title={$t('settings.app.passEnv')} list={form.sandbox.passEnv} onAdd={() => addListItem(form.sandbox.passEnv)} onRemove={(i) => removeListItem(form.sandbox.passEnv, i)} />
      <ListEditor title={$t('settings.app.bashWhitelist')} list={form.approval.bashWhitelist} onAdd={() => addListItem(form.approval.bashWhitelist)} onRemove={(i) => removeListItem(form.approval.bashWhitelist, i)} />
      <ListEditor title={$t('settings.app.bashBlacklist')} list={form.approval.bashBlacklist} onAdd={() => addListItem(form.approval.bashBlacklist)} onRemove={(i) => removeListItem(form.approval.bashBlacklist, i)} />
    </div>
  </SettingsSection>
{:else}
  <SettingsSection class="provider-defaults-section" title={$t('settings.providers.sections.defaults')} description={$t('settings.providers.defaultsHint')}>
    <div class="settings-form-grid">
      <SettingsField label={$t('settings.app.defaultProvider')}>
        <SearchSelect
          className="provider-choice"
          value={form.defaults.defaultProvider}
          options={defaultProviderOptions}
          placeholder={$t('common.uninitialized')}
          ariaLabel={$t('settings.app.defaultProvider')}
          on:change={(event) => selectDefaultProvider(event.detail)}
        />
      </SettingsField>
      <SettingsField label={$t('settings.app.defaultModel')}>
        <SearchSelect
          className="model-choice"
          value={form.defaults.defaultModel}
          options={defaultModelSelectOptions}
          placeholder={$t('common.uninitialized')}
          ariaLabel={$t('settings.app.defaultModel')}
          disabled={defaultModelOptions.length === 0 && !form.defaults.defaultModel}
          on:change={(event) => selectDefaultModel(event.detail)}
        />
      </SettingsField>
      <SettingsField label={$t('settings.app.thinking')}>
        <select bind:value={form.defaults.defaultThinkingLevel} class="settings-select">
          <option value="off">off</option>
          <option value="minimal">minimal</option>
          <option value="low">low</option>
          <option value="medium">medium</option>
          <option value="high">high</option>
          <option value="xhigh">xhigh</option>
          <option value="max">max</option>
        </select>
      </SettingsField>
    </div>
  </SettingsSection>

  <Card class="settings-card provider-editor-card">
    <CardHeader>
      <CardTitle>{$t('settings.app.sections.providers')}</CardTitle>
      {#if !$isMobile}
        <CardAction>
          <Button variant="outline" size="sm" type="button" onclick={addProvider}>
            <Plus size={14} aria-hidden="true" />
            <span>{$t('common.add')}</span>
          </Button>
        </CardAction>
      {/if}
      <CardDescription>{$t('settings.app.providersHint', { count: form.providers.length })}</CardDescription>
    </CardHeader>
    <CardContent>
      {#if form.providers.length === 0}
        <p class="empty">{$t('settings.app.noProviders')}</p>
      {:else if !$isMobile}
        <div class="provider-editor">
          <aside class="provider-list-shell">
            <div class="provider-list-search">
              <Input bind:value={providerSearchTerm} placeholder={$t('settings.app.searchProviders')} aria-label={$t('settings.app.searchProviders')} />
            </div>
            <div class="provider-list">
              {#if filteredProviders.length === 0}
                <p class="empty provider-list-empty">{$t('settings.app.noProviders')}</p>
              {:else}
                {#each filteredProviders as provider (provider.id)}
                  {@const isSelected = provider.id === selectedProviderID}
                  <Button
                    type="button"
                    variant="ghost"
                    size="default"
                    class="provider-list-item {isSelected ? 'active' : ''}"
                    aria-current={isSelected ? 'true' : undefined}
                    title={provider.id || $t('settings.app.unnamedProvider')}
                    onclick={() => (selectedProviderID = provider.id)}
                  >
                    <span class="provider-list-lead">
                      <span class="provider-list-check" aria-hidden="true" data-selected={isSelected}>
                        {#if isSelected}
                          <Check size={14} />
                        {:else}
                          <Database size={14} />
                        {/if}
                      </span>
                      <span class="provider-list-label">{provider.id || $t('settings.app.unnamedProvider')}</span>
                    </span>
                    <Badge variant="secondary">{$t('settings.app.modelCount', { count: mergedModelOptions(provider.id, provider, $modelCatalog.models).length })}</Badge>
                  </Button>
                {/each}
              {/if}
            </div>
          </aside>
          {#if currentProvider}
            <ProviderEditorDetail
              provider={currentProvider}
              {modelTestStates}
              inheritedModels={currentProviderInherited}
              {loadingDiscoveredModels}
              isMobileDetail={false}
              onRename={renameProvider}
              onAddHeader={addHeader}
              onRemoveHeader={removeHeader}
              onAddModel={addModel}
              onModelIDCommit={applyModelIDPreset}
              onRemoveModel={removeModel}
              onFetchModels={fetchProviderModels}
              onTestModel={testProviderModel}
              onRemoveProvider={removeProvider}
              apiTypeOptionsForProvider={apiTypeOptionsForProvider}
            />
          {/if}
        </div>
      {:else}
        <div class="provider-mobile-shell">
          <div class="provider-mobile-picker">
            <SearchSelect
              className="provider-mobile-select"
              value={selectedProviderID}
              options={form.providers.map((provider) => ({ value: provider.id, label: provider.id || $t('settings.app.unnamedProvider') }))}
              placeholder={$t('settings.app.selectProvider')}
              ariaLabel={$t('settings.app.providerID')}
              on:change={(event) => (selectedProviderID = event.detail)}
            />
            <Button variant="outline" size="sm" type="button" onclick={addProvider}>
              <Plus size={14} aria-hidden="true" />
              <span>{$t('common.add')}</span>
            </Button>
          </div>
          {#if currentProvider}
            <ProviderEditorDetail
              provider={currentProvider}
              {modelTestStates}
              inheritedModels={currentProviderInherited}
              {loadingDiscoveredModels}
              isMobileDetail={true}
              onRename={renameProvider}
              onAddHeader={addHeader}
              onRemoveHeader={removeHeader}
              onAddModel={addModel}
              onModelIDCommit={applyModelIDPreset}
              onRemoveModel={removeModel}
              onFetchModels={fetchProviderModels}
              onTestModel={testProviderModel}
              onRemoveProvider={removeProvider}
              apiTypeOptionsForProvider={apiTypeOptionsForProvider}
            />
          {/if}
        </div>
      {/if}
    </CardContent>

  </Card>
{/if}

{#if showModelPicker}
  <div
    class="provider-model-modal-overlay"
    role="dialog"
    aria-modal="true"
    aria-label={$t('settings.app.fetchModels')}
    tabindex="-1"
    on:click={(event) => event.currentTarget === event.target && (showModelPicker = false)}
    on:keydown={(event) => event.key === 'Escape' && (showModelPicker = false)}
  >
    <div class="provider-model-modal">
      <header>
        <div>
          <h3>{$t('settings.app.fetchModels')}</h3>
          <span class="hint">{$t('settings.app.fetchModelsHint')}</span>
        </div>
        <Button variant="ghost" size="icon-xs" type="button" onclick={() => (showModelPicker = false)} aria-label={$t('common.close')}>
          <X size={14} aria-hidden="true" />
        </Button>
      </header>
      {#if loadingDiscoveredModels}
        <p class="empty">{$t('common.loading')}</p>
      {:else if discoveredModels.length === 0}
        <p class="empty">{$t('settings.app.modelsFetchEmpty')}</p>
      {:else}
        <div class="provider-model-picker-list">
          {#each discoveredModels as discovered (discovered.id)}
            <div class="provider-model-picker-row">
              <div>
                <strong>{discovered.id}</strong>
                {#if discovered.name && discovered.name !== discovered.id}<span>{discovered.name}</span>{/if}
              </div>
              <Button variant="outline" size="sm" type="button" disabled={discoveredModelAdded(currentProvider, discovered)} onclick={() => addDiscoveredModel(currentProvider, discovered)}>
                {discoveredModelAdded(currentProvider, discovered) ? $t('settings.app.modelAdded') : $t('common.add')}
              </Button>
            </div>
          {/each}
        </div>
      {/if}
    </div>
  </div>
{/if}

<Card class="settings-card settings-advanced-card">
  <details class="settings-advanced-details">
    <summary>
      <span class="settings-advanced-title">{$t('settings.app.advancedJson')}</span>
      <span class="settings-advanced-hint">{$t('settings.app.advancedJsonHint')}</span>
    </summary>
    <textarea class="code" bind:value={jsonDraft} spellcheck="false"></textarea>
  </details>
</Card>
