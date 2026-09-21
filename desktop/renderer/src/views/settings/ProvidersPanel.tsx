// 供应商与模型面板:全局默认值卡 + 目录/编辑器双栏。数据来自
// mothx/manage/settings 与 mothx/manage/providers 投影;API 密钥只存在于
// 临时表单草稿,绝不进入 Desktop 本地持久化。

import { useCallback, useEffect, useState, type ReactNode } from 'react';
import { Cpu, Plus } from 'lucide-react';

import { ManageWorkspace, UnsupportedRow } from '@/components/manage-primitives';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { RowItem, RowList } from '@/components/layout';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { t } from '@/core/i18n';
import {
  buildProviderDraft,
  deleteProvider,
  discoverProviderModels,
  loadProviderCatalog,
  loadSettings,
  optionalImageLimit,
  optionalNumber,
  patchSettings,
  providerModelCopies,
  saveProvider,
  testProvider,
  type ProviderCatalog,
  type ProviderConfigView,
  type ProviderModelView,
  type SettingsView,
} from '@/core/manage-api';
import { hasFeature } from '@/core/state';
import { confirmDanger, promptModal, toast } from '@/core/ui-host';
import { useAppState } from '@/hooks/useAppState';
import { cn } from '@/lib/utils';

const API_CHOICES = ['openai-chat', 'openai-responses', 'anthropic-messages', 'google-gemini', 'google-vertex'];
const THINKING_LEVELS = ['off', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'];

interface DraftSecret {
  apiKey: string;
  clearKey: boolean;
}

function labeled(label: string, control: ReactNode, className?: string) {
  return (
    <label className={cn('flex min-w-0 flex-col gap-1.5 text-[11px] font-semibold text-muted-foreground', className)}>
      <span>{label}</span>
      {control}
    </label>
  );
}

// ---- 无 manageProviderConfig 能力时的简单供应商行 ----

function ProviderRowsFallback({ settings }: { settings: SettingsView }) {
  const providers = settings.providers || [];
  const reload = useCallback(async () => {
    await loadSettings();
    await loadProviderCatalog();
  }, []);

  const setKey = async (name: string) => {
    const key = await promptModal({
      title: t('settings.keyModalTitle', { n: name }),
      initialValue: '',
      okLabel: t('modal.ok'),
      cancelLabel: t('modal.cancel'),
      secret: true,
    });
    if (!key) return;
    const view = await patchSettings({ providerKey: { name, key } });
    if (view) toast(t('settings.providerSaved'));
  };

  const test = async (name: string) => {
    const result = await testProvider(name);
    if (!result) return;
    if (result.ok) toast(t('settings.testOk', { n: name, ms: result.latencyMs ?? 0 }));
    else toast(t('settings.testFail', { n: name, e: result.error || 'unknown' }));
  };

  if (providers.length === 0) return <UnsupportedRow text={t('manage.unsupported')} />;
  return (
    <RowList>
      {providers.map((provider) => (
        <RowItem
          key={provider.name}
          icon={<Cpu />}
          title={
            <>
              <span>{provider.name}</span>
              {settings.defaultProvider === provider.name ? <Badge variant="accent">{t('settings.isDefault')}</Badge> : null}
            </>
          }
          desc={[provider.maskedKey || 'no-key', provider.baseUrl, provider.modelCount ? `${provider.modelCount} models` : '']
            .filter(Boolean)
            .join(' · ')}
        >
          <Button variant="outline" size="sm" onClick={() => void test(provider.name)}>
            {t('settings.test')}
          </Button>
          <Button variant="outline" size="sm" onClick={() => void setKey(provider.name)}>
            {t('settings.setKey')}
          </Button>
          {settings.defaultProvider !== provider.name ? (
            <Button variant="outline" size="sm" onClick={() => void patchSettings({ defaultProvider: provider.name }).then(reload)}>
              {t('settings.default')}
            </Button>
          ) : null}
        </RowItem>
      ))}
    </RowList>
  );
}

// ---- 全局默认模型卡 ----

function DefaultsCard({ settings, catalog }: { settings: SettingsView; catalog: ProviderCatalog }) {
  const providers = catalog.providerConfigs || [];
  const [provider, setProvider] = useState(settings.defaultProvider || catalog.defaultProvider || providers[0]?.id || '');
  const models = (catalog.models || []).filter((model) => model.provider === provider && model.id);
  const [model, setModel] = useState(settings.defaultModel || catalog.defaultModel || models[0]?.id || '');
  const [thinking, setThinking] = useState(settings.thinkingLevel || 'medium');

  useEffect(() => {
    setProvider(settings.defaultProvider || catalog.defaultProvider || providers[0]?.id || '');
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [settings.defaultProvider, catalog.defaultProvider]);

  const effectiveModel = models.some((entry) => entry.id === model) ? model : models[0]?.id || '';

  return (
    <div className="rounded-[14px] border border-border bg-card p-4 shadow-panel">
      <div className="text-[14px] font-bold leading-snug text-strong">{t('settings.providerDefaults')}</div>
      <div className="mt-1 max-w-[680px] text-[11.5px] leading-snug text-muted-foreground">{t('settings.providerDefaultsDesc')}</div>
      <div className="mt-4 grid grid-cols-[repeat(3,minmax(0,1fr))_auto] items-end gap-2.5 max-[760px]:grid-cols-1">
        {labeled(
          t('settings.defaultProviderLabel'),
          <Select value={provider} onValueChange={(value) => { setProvider(value); setModel(''); }}>
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {providers.map((entry) => (
                <SelectItem key={entry.id} value={entry.id}>
                  {entry.id}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>,
        )}
        {labeled(
          t('settings.defaultModelLabel'),
          <Select value={effectiveModel} onValueChange={setModel}>
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {models.map((entry) => (
                <SelectItem key={String(entry.id)} value={String(entry.id)}>
                  {entry.name && entry.name !== entry.id ? `${entry.id} · ${entry.name}` : String(entry.id)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>,
        )}
        {labeled(
          t('settings.thinkingLabel'),
          <Select value={thinking} onValueChange={setThinking}>
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {THINKING_LEVELS.map((level) => (
                <SelectItem key={level} value={level}>
                  {level}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>,
        )}
        <Button
          className="min-h-[34px] whitespace-nowrap max-[760px]:w-full"
          onClick={() => void patchSettings({ defaultProvider: provider, defaultModel: effectiveModel, thinkingLevel: thinking }).then((view) => view && toast(t('settings.providerSaved')))}
        >
          {t('settings.saveDefaults')}
        </Button>
      </div>
    </div>
  );
}

// ---- 目录 ----

function filteredProviders(catalog: ProviderCatalog, draft: ProviderConfigView | null, scope: 'configured' | 'all', search: string): ProviderConfigView[] {
  const configs = [...(catalog.providerConfigs || [])];
  if (draft && !configs.some((provider) => provider.id === draft.id)) configs.push(draft);
  return configs.filter((provider) => {
    if (scope === 'configured' && !provider.apiKeyConfigured && provider !== draft) return false;
    if (!search) return true;
    return [provider.id, provider.provider.vendor, provider.provider.baseUrl, provider.maskedKey]
      .filter(Boolean)
      .join(' ')
      .toLowerCase()
      .includes(search);
  });
}

function modelDraftDefaults(catalog: ProviderCatalog): ProviderModelView {
  const defaults = catalog.modelDefaults || {};
  return {
    reasoning: defaults.reasoning !== false,
    contextWindow: Number(defaults.contextWindow) > 0 ? defaults.contextWindow : 256000,
    maxTokens: Number(defaults.maxTokens) > 0 ? defaults.maxTokens : undefined,
    input: [...(defaults.input?.length ? defaults.input : ['text'])],
  };
}

export function ProvidersPanel() {
  const appState = useAppState();
  const ready = appState.connection.state === 'ready';
  const supportsWorkspace = hasFeature('manageProviderConfig');
  const supportsAny = hasFeature('manageProviders') || hasFeature('manageSettings');

  const [settings, setSettings] = useState<SettingsView | undefined>();
  const [catalog, setCatalog] = useState<ProviderCatalog | undefined>();
  const [activeProviderID, setActiveProviderID] = useState('');
  const [draft, setDraft] = useState<ProviderConfigView | null>(null);
  const [secret, setSecret] = useState<DraftSecret>({ apiKey: '', clearKey: false });
  const [editorTab, setEditorTab] = useState('connection');
  const [scope, setScope] = useState<'configured' | 'all'>('configured');
  const [search, setSearch] = useState('');
  const [discovering, setDiscovering] = useState(false);
  const [discoverDialogOpen, setDiscoverDialogOpen] = useState(false);
  const [discoveredCandidates, setDiscoveredCandidates] = useState<ProviderModelView[]>([]);
  const [discoverSearch, setDiscoverSearch] = useState('');
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());

  const reload = useCallback(async () => {
    const [nextSettings, nextCatalog] = await Promise.all([loadSettings(), loadProviderCatalog()]);
    setSettings(nextSettings);
    setCatalog(nextCatalog);
    return { nextSettings, nextCatalog };
  }, []);

  useEffect(() => {
    if (!ready || !supportsAny) return;
    let cancelled = false;
    void (async () => {
      const { nextSettings, nextCatalog } = await reload();
      if (cancelled || !nextCatalog) return;
      // 初始选中:当前 → 默认 → 已配置 → 第一个。
      const providers = nextCatalog.providerConfigs || [];
      if (activeProviderID && providers.some((provider) => provider.id === activeProviderID)) return;
      const initial =
        providers.find((provider) => provider.id === nextSettings?.defaultProvider || provider.isDefault) ||
        providers.find((provider) => provider.apiKeyConfigured) ||
        providers[0];
      if (initial) {
        setActiveProviderID(initial.id);
        setDraft(buildProviderDraft(initial));
        setSecret({ apiKey: '', clearKey: false });
        setEditorTab('connection');
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ready, reload, supportsAny]);

  if (!supportsAny) return <UnsupportedRow text={t('manage.unsupported')} />;
  if (!settings && !catalog) return <UnsupportedRow text="…" />;

  if (!supportsWorkspace || !catalog?.providerConfigs) {
    return <ProviderRowsFallback settings={settings || {}} />;
  }

  const selectProvider = (id: string) => {
    const provider = (catalog.providerConfigs || []).find((entry) => entry.id === id);
    if (!provider) return;
    setActiveProviderID(id);
    setDraft(buildProviderDraft(provider));
    setSecret({ apiKey: '', clearKey: false });
    setEditorTab('connection');
  };

  const addProvider = async () => {
    const id = await promptModal({ title: t('settings.newProvider'), initialValue: 'provider', okLabel: t('modal.ok'), cancelLabel: t('modal.cancel') });
    const trimmed = id?.trim();
    if (!trimmed) return;
    const configs = catalog.providerConfigs || [];
    if (configs.some((provider) => provider.id === trimmed)) {
      toast(t('settings.providerIDExists'));
      selectProvider(trimmed);
      return;
    }
    setActiveProviderID(trimmed);
    setDraft({ id: trimmed, provider: { api: 'openai-chat', models: [] }, globalOverride: true });
    setSecret({ apiKey: '', clearKey: false });
    setEditorTab('connection');
  };

  const patchDraft = (fn: (next: ProviderConfigView) => void) => {
    setDraft((current) => {
      if (!current) return current;
      const next: ProviderConfigView = {
        ...current,
        provider: { ...current.provider, models: (current.provider.models || []).map((model) => ({ ...model })) },
      };
      fn(next);
      return next;
    });
  };

  const saveDraft = async () => {
    if (!draft) return;
    const nextID = draft.id.trim();
    if (!nextID) {
      toast(t('settings.providerIDRequired'));
      return;
    }
    const models = providerModelCopies(draft);
    if (models.some((model) => !String(model.id || '').trim())) {
      toast(t('settings.modelIDRequired'));
      return;
    }
    const value: Record<string, unknown> = {
      vendor: draft.provider.vendor?.trim(),
      api: draft.provider.api?.trim(),
      baseUrl: draft.provider.baseUrl?.trim(),
      httpProxy: draft.provider.httpProxy?.trim(),
      forceHTTP11: draft.provider.forceHTTP11 === true,
      thinkingFormat: draft.provider.thinkingFormat?.trim(),
      models,
    };
    const max = draft.provider.maxImagesPerRequest;
    if (max !== undefined) value.maxImagesPerRequest = max;
    const previousProvider = (catalog.providerConfigs || []).find((provider) => provider.id === activeProviderID);
    const request: Record<string, unknown> = { id: nextID, provider: value };
    if (previousProvider && nextID !== previousProvider.id) request.previousId = previousProvider.id;
    // API key 只在临时草稿里;保存后清空,不回显、不落本地。
    if (secret.apiKey.trim()) request.apiKey = secret.apiKey.trim();
    else if (secret.clearKey) request.apiKey = '';
    try {
      await saveProvider(request);
      setActiveProviderID(nextID);
      setDraft(null);
      setSecret({ apiKey: '', clearKey: false });
      await reload();
      toast(t('settings.providerSaved'));
    } catch (error) {
      toast(error instanceof Error ? error.message : String(error));
    }
  };

  const removeOverride = async () => {
    if (!draft) return;
    if (draft.isDefault) return;
    if (!await confirmDanger(t('settings.confirmResetProvider', { n: draft.id }))) return;
    try {
      await deleteProvider(draft.id);
      if (activeProviderID === draft.id) setActiveProviderID('');
      setDraft(null);
      setSecret({ apiKey: '', clearKey: false });
      await reload();
      toast(t('settings.providerReset'));
    } catch (error) {
      toast(error instanceof Error ? error.message : String(error));
    }
  };

  const runTest = async () => {
    if (!draft) return;
    const firstModel = providerModelCopies(draft)[0]?.id;
    const result = await testProvider(draft.id, firstModel ? String(firstModel) : undefined);
    if (!result) return;
    if (result.ok) toast(t('settings.testOk', { n: draft.id, ms: result.latencyMs ?? 0 }));
    else toast(t('settings.testFail', { n: draft.id, e: result.error || 'unknown' }));
  };

  const toggleCandidate = (id: string) => {
    setSelectedIds((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const confirmAddDiscovered = () => {
    if (!draft) return;
    let added = 0;
    patchDraft((next) => {
      const models = next.provider.models || (next.provider.models = []);
      const existingIds = new Set(models.map((entry) => String(entry.id || '')));
      for (const candidate of discoveredCandidates) {
        const id = String(candidate.id || '').trim();
        if (!id || !selectedIds.has(id) || existingIds.has(id)) continue;
        models.push({ ...candidate, id, name: String(candidate.name || id), input: [...(candidate.input || ['text'])] });
        existingIds.add(id);
        added += 1;
      }
    });
    toast(t('settings.modelsDiscovered', { n: added }));
    setDiscoverDialogOpen(false);
    setDiscoveredCandidates([]);
    setSelectedIds(new Set());
    setDiscoverSearch('');
  };

  const discover = async () => {
    if (!draft) return;
    setDiscovering(true);
    try {
      const found = await discoverProviderModels({
        api: draft.provider.api || 'openai-chat',
        baseUrl: draft.provider.baseUrl || '',
        apiKey: secret.apiKey,
        httpProxy: draft.provider.httpProxy || '',
        forceHTTP11: draft.provider.forceHTTP11 === true,
      });
      const existingIds = new Set((draft.provider.models || []).map((entry) => String(entry.id || '')));
      const seen = new Set<string>();
      const candidates: ProviderModelView[] = [];
      for (const model of found) {
        const id = String(model.id || '').trim();
        if (!id || existingIds.has(id) || seen.has(id)) continue;
        seen.add(id);
        candidates.push({ ...model, id, name: String(model.name || id), input: [...(model.input || ['text'])] });
      }
      if (candidates.length === 0) {
        toast(t('settings.noDiscoveredModels'));
        return;
      }
      setDiscoveredCandidates(candidates);
      setSelectedIds(new Set());
      setDiscoverSearch('');
      setDiscoverDialogOpen(true);
    } catch (error) {
      toast(error instanceof Error ? error.message : String(error));
    } finally {
      setDiscovering(false);
    }
  };

  const filteredCandidates = discoveredCandidates.filter((candidate) => {
    const term = discoverSearch.trim().toLowerCase();
    if (!term) return true;
    const id = String(candidate.id || '').toLowerCase();
    const name = String(candidate.name || '').toLowerCase();
    return id.includes(term) || name.includes(term);
  });

  const closeDiscoverDialog = () => {
    setDiscoverDialogOpen(false);
    setDiscoveredCandidates([]);
    setSelectedIds(new Set());
    setDiscoverSearch('');
  };

  const applyModelIDPreset = (index: number) => {
    if (!draft) return;
    const model = draft.provider.models?.[index];
    const id = String(model?.id || '').trim();
    if (!model || !id || String(model.name || '').trim()) return;
    const models = catalog.models || [];
    const preset = models.find((entry) => entry.provider === draft.id && entry.id === id)
      || models.find((entry) => entry.id === id)
      || catalog.modelDefaults
      || {};
    patchDraft((next) => {
      const target = next.provider.models?.[index];
      if (!target) return;
      target.name = String(preset.name || id);
      target.reasoning = preset.reasoning !== false;
      target.contextWindow = Number(preset.contextWindow) > 0 ? preset.contextWindow : 256000;
      target.maxTokens = Number(preset.maxTokens) > 0 ? preset.maxTokens : undefined;
      target.input = [...(preset.input?.length ? preset.input : ['text'])];
    });
  };

  const visible = filteredProviders(catalog, draft, scope, search.trim().toLowerCase());
  const canTest = draft ? (catalog.providerConfigs || []).some((candidate) => candidate.id === draft.id) : false;
  const isNewProvider = draft ? !draft.globalOverride && !(catalog.providerConfigs || []).some((provider) => provider.id === draft.id && provider.globalOverride) : false;
  const draftModels = draft?.provider.models || [];

  return (
    <ManageWorkspace>
      <DefaultsCard settings={settings || {}} catalog={catalog} />

      <div className="grid grid-cols-[minmax(230px,.7fr)_minmax(0,1.8fr)] items-start gap-3.5 max-[760px]:grid-cols-1">
        {/* 目录栏 */}
        <div className="flex max-h-[min(680px,72vh)] min-w-0 flex-col rounded-[14px] border border-border bg-card p-3 shadow-panel max-[760px]:max-h-none">
          <div className="flex items-center justify-between px-0.5 pb-2.5">
            <div className="text-[14px] font-bold text-strong">{t('settings.providerCatalog')}</div>
          </div>
          <div className="flex flex-col gap-2 border-b border-border px-0.5 pb-2.5">
            <Input
              type="search"
              placeholder={t('settings.providerCatalogSearch')}
              value={search}
              onChange={(event) => setSearch(event.target.value)}
            />
            <div className="grid grid-cols-2 gap-[3px] rounded-[9px] bg-placeholder p-[3px]">
              {(['configured', 'all'] as const).map((entry) => (
                <button
                  key={entry}
                  type="button"
                  className={cn(
                    'min-w-0 rounded-md px-[7px] py-[5px] text-center text-[11px] font-semibold text-muted-foreground hover:bg-hoverbg hover:text-foreground',
                    scope === entry && 'bg-card text-strong shadow-panel'
                  )}
                  onClick={() => setScope(entry)}
                >
                  {entry === 'configured' ? t('settings.scopeConfigured') : t('settings.scopeAll')}
                </button>
              ))}
            </div>
          </div>
          <div className="mt-2 flex min-h-[72px] flex-1 flex-col gap-[3px] overflow-auto max-[760px]:max-h-[280px]">
            {visible.length === 0 ? (
              <div className="m-[3px] rounded-[9px] border border-dashed border-borderstrong px-3 py-4 text-center text-[11.5px] leading-normal text-muted-foreground">
                {t('settings.noProviders')}
              </div>
            ) : (
              visible.map((provider) => (
                <div
                  key={provider.id}
                  role="button"
                  tabIndex={0}
                  className={cn(
                    'flex w-full min-w-0 cursor-pointer items-center gap-2 rounded-[9px] border border-transparent p-2 text-left transition-all hover:bg-hoverbg',
                    provider.id === activeProviderID && 'border-[color-mix(in_srgb,var(--primary)_34%,var(--border))] bg-primary/8'
                  )}
                  onClick={() => selectProvider(provider.id)}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter') selectProvider(provider.id);
                  }}
                >
                  <span className="flex size-[29px] shrink-0 items-center justify-center rounded-lg bg-primary/8 text-primary">
                    <Cpu className="size-4" />
                  </span>
                  <span className="flex min-w-0 flex-1 flex-col gap-[3px]">
                    <span className="flex min-w-0 items-center gap-[5px]">
                      <span title={provider.id} className={cn('min-w-0 flex-1 truncate text-[12px] font-bold', provider.id === activeProviderID && 'text-primary')}>{provider.id}</span>
                      {provider.isDefault ? <Badge variant="accent" className="shrink-0">{t('settings.isDefault')}</Badge> : null}
                      {provider.apiKeyConfigured ? <Badge variant="info" className="shrink-0">{t('settings.configured')}</Badge> : null}
                    </span>
                    <span
                      title={[provider.maskedKey || t('settings.noKey'), provider.provider.baseUrl, `${providerModelCopies(provider).length} ${t('settings.models')}`]
                        .filter(Boolean)
                        .join(' · ')}
                      className="block truncate text-[10px] text-faint"
                    >
                      {[provider.maskedKey || t('settings.noKey'), provider.provider.baseUrl, `${providerModelCopies(provider).length} ${t('settings.models')}`]
                        .filter(Boolean)
                        .join(' · ')}
                    </span>
                  </span>
                </div>
              ))
            )}
          </div>
          <Button variant="outline" className="mt-2.5 w-full" onClick={() => void addProvider()}>
            <Plus />
            {t('settings.addProvider')}
          </Button>
        </div>

        {/* 编辑器栏 */}
        <div className="min-w-0 overflow-hidden rounded-[14px] border border-border bg-card shadow-panel">
          {draft === null ? (
            <div className="rounded-[10px] border border-dashed border-borderstrong p-5 text-center text-[11.5px] leading-normal text-muted-foreground">
              {t('settings.noProviderSelected')}
            </div>
          ) : (
            <>
              <div className="flex items-start justify-between gap-3.5 border-b border-border p-4 max-[760px]:flex-col">
                <div className="flex min-w-0 items-center gap-2.5">
                  <span className="flex size-10 shrink-0 items-center justify-center rounded-[11px] border border-[color-mix(in_srgb,var(--primary)_24%,var(--border))] bg-primary/8 text-primary">
                    <Cpu className="size-5" />
                  </span>
                  <div className="min-w-0">
                    <div className="flex items-center gap-1.5 overflow-hidden text-[16px] font-bold leading-tight text-strong">
                      <span className="truncate">{draft.id}</span>
                      {draft.isDefault ? <Badge variant="accent">{t('settings.isDefault')}</Badge> : null}
                    </div>
                    <div className="mt-[3px] max-w-[410px] truncate font-mono text-[11px] text-muted-foreground">
                      {[draft.provider.vendor || '', draft.provider.baseUrl || ''].filter(Boolean).join(' · ') || ' '}
                    </div>
                  </div>
                </div>
                <div className="flex shrink-0 items-center gap-[7px] max-[760px]:w-full">
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <span className="max-[760px]:flex-1">
                        <Button variant="outline" className="w-full max-[760px]:w-full" disabled={!canTest} onClick={() => void runTest()}>
                          {t('settings.test')}
                        </Button>
                      </span>
                    </TooltipTrigger>
                    {!canTest ? <TooltipContent>{t('settings.saveProviderFirst')}</TooltipContent> : null}
                  </Tooltip>
                  <Button className="min-w-[108px] max-[760px]:flex-1" onClick={() => void saveDraft()}>
                    {t('settings.saveProvider')}
                  </Button>
                </div>
              </div>

              <Tabs value={editorTab} onValueChange={setEditorTab} className="gap-0">
                <TabsList className="rounded-none px-3">
                  <TabsTrigger value="connection">{t('settings.connectionTab')}</TabsTrigger>
                  <TabsTrigger value="models">{t('settings.modelsTab')}</TabsTrigger>
                  <TabsTrigger value="advanced">{t('settings.advancedTab')}</TabsTrigger>
                </TabsList>

                <TabsContent value="connection" className="p-4">
                  <div className="grid grid-cols-2 gap-[11px] max-[760px]:grid-cols-1">
                    {labeled(
                      t('settings.providerID'),
                      <Input
                        value={draft.id}
                        disabled={!isNewProvider}
                        onChange={(event) => patchDraft((next) => { next.id = event.target.value.trim() || next.id; })}
                      />,
                    )}
                    {labeled(
                      t('settings.providerVendor'),
                      <Input value={draft.provider.vendor || ''} onChange={(event) => patchDraft((next) => { next.provider.vendor = event.target.value.trim(); })} />,
                    )}
                    {labeled(
                      t('settings.providerAPI'),
                      <Select value={draft.provider.api || 'openai-chat'} onValueChange={(value) => patchDraft((next) => { next.provider.api = value; })}>
                        <SelectTrigger className="w-full">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {API_CHOICES.map((choice) => (
                            <SelectItem key={choice} value={choice}>
                              {choice}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>,
                    )}
                    {labeled(
                      t('settings.providerBaseURL'),
                      <Input value={draft.provider.baseUrl || ''} onChange={(event) => patchDraft((next) => { next.provider.baseUrl = event.target.value.trim(); })} />,
                    )}
                    {labeled(
                      t('settings.providerAPIKey'),
                      <Input
                        type="password"
                        value={secret.apiKey}
                        placeholder={draft.maskedKey || t('settings.keyUnchanged')}
                        onChange={(event) => setSecret((current) => ({ ...current, apiKey: event.target.value }))}
                      />,
                    )}
                    {labeled(
                      t('settings.httpProxy'),
                      <Input value={draft.provider.httpProxy || ''} onChange={(event) => patchDraft((next) => { next.provider.httpProxy = event.target.value.trim(); })} />,
                    )}
                    <div className="flex min-h-[58px] items-start justify-between gap-3 rounded-[9px] border border-border bg-background p-2.5 hover:border-borderstrong">
                      <span className="flex flex-col gap-0.5">
                        <span className="text-[11.5px] font-semibold text-strong">{t('settings.forceHTTP11')}</span>
                        <span className="text-[10px] leading-snug text-muted-foreground">{t('settings.forceHTTP11Desc')}</span>
                      </span>
                      <Switch
                        checked={draft.provider.forceHTTP11 === true}
                        onCheckedChange={(checked) => patchDraft((next) => { next.provider.forceHTTP11 = checked; })}
                        aria-label={t('settings.forceHTTP11')}
                      />
                    </div>
                  </div>
                </TabsContent>

                <TabsContent value="models" className="p-4">
                  <div className="mb-3 flex items-center justify-between gap-2.5">
                    <div className="text-[14px] font-bold text-strong">{t('settings.models')}</div>
                    <div className="flex gap-[7px]">
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() =>
                          patchDraft((next) => {
                            const models = next.provider.models || (next.provider.models = []);
                            models.push({ id: '', name: '', ...modelDraftDefaults(catalog) });
                          })
                        }
                      >
                        {t('settings.addModel')}
                      </Button>
                      <Button variant="outline" size="sm" disabled={discovering} onClick={() => void discover()}>
                        {discovering ? t('settings.discoveringModels') : t('settings.discoverModels')}
                      </Button>
                    </div>
                  </div>
                  {draftModels.length === 0 ? (
                    <div className="text-[11.5px] text-muted-foreground">{t('settings.noModels')}</div>
                  ) : (
                    <div className="mt-3.5 flex flex-col gap-2">
                      {draftModels.map((model, index) => (
                        <ModelRow
                          key={index}
                          model={model}
                          onChange={(next) =>
                            patchDraft((draftNext) => {
                              const models = draftNext.provider.models || [];
                              models[index] = next;
                            })
                          }
                          onModelIDCommit={() => applyModelIDPreset(index)}
                          onRemove={() =>
                            patchDraft((next) => {
                              const models = next.provider.models || [];
                              models.splice(index, 1);
                            })
                          }
                        />
                      ))}
                    </div>
                  )}
                </TabsContent>

                <TabsContent value="advanced" className="p-4">
                  {draft.globalOverride ? (
                    <div className="mt-0 mb-4 flex items-center justify-between gap-3 rounded-[10px] border border-[color-mix(in_srgb,var(--danger)_33%,var(--border))] bg-danger-soft p-3">
                      <div className="text-[12px] font-bold text-danger">{t('settings.dangerZone')}</div>
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <span>
                            <Button variant="destructive" size="sm" disabled={draft.isDefault === true} onClick={() => void removeOverride()}>
                              {t('settings.resetProvider')}
                            </Button>
                          </span>
                        </TooltipTrigger>
                        {draft.isDefault ? <TooltipContent>{t('settings.resetDefaultBlocked')}</TooltipContent> : null}
                      </Tooltip>
                    </div>
                  ) : null}
                  <div className="grid grid-cols-2 gap-[11px] max-[760px]:grid-cols-1">
                    {labeled(
                      t('settings.providerThinkingFormat'),
                      <Input value={draft.provider.thinkingFormat || ''} onChange={(event) => patchDraft((next) => { next.provider.thinkingFormat = event.target.value.trim(); })} />,
                    )}
                    {labeled(
                      t('settings.maxImagesPerRequest'),
                      <Input
                        type="number"
                        value={draft.provider.maxImagesPerRequest === undefined ? '' : String(draft.provider.maxImagesPerRequest)}
                        onChange={(event) => {
                          const parsed = optionalImageLimit(event.target.value);
                          patchDraft((next) => {
                            next.provider.maxImagesPerRequest = parsed;
                          });
                        }}
                      />,
                    )}
                    <div className="flex min-h-[58px] items-start justify-between gap-3 rounded-[9px] border border-border bg-background p-2.5 hover:border-borderstrong">
                      <span className="flex flex-col gap-0.5">
                        <span className="text-[11.5px] font-semibold text-strong">{t('settings.clearProviderKey')}</span>
                        <span className="text-[10px] leading-snug text-muted-foreground">{t('settings.clearProviderKeyDesc')}</span>
                      </span>
                      <Switch
                        checked={secret.clearKey}

                        onCheckedChange={(checked) => setSecret((current) => ({ ...current, clearKey: checked }))}
                        aria-label={t('settings.clearProviderKey')}
                      />
                    </div>
                  </div>
                </TabsContent>
              </Tabs>

              <Dialog
                open={discoverDialogOpen}
                onOpenChange={(open) => {
                  if (!open) closeDiscoverDialog();
                }}
              >
                <DialogContent className="w-[min(560px,92vw)]">
                  <DialogHeader>
                    <DialogTitle>{t('settings.discoverModelsTitle')}</DialogTitle>
                    <DialogDescription>{t('settings.discoverModelsDesc')}</DialogDescription>
                  </DialogHeader>
                  <Input
                    type="search"
                    autoFocus
                    placeholder={t('settings.searchDiscoveredModels')}
                    value={discoverSearch}
                    onChange={(event) => setDiscoverSearch(event.target.value)}
                  />
                  <div className="max-h-[min(420px,48vh)] overflow-y-auto rounded-lg border border-border">
                    {filteredCandidates.length === 0 ? (
                      <div className="px-3 py-6 text-center text-[12px] text-muted-foreground">
                        {t('settings.noDiscoveredModelsMatch')}
                      </div>
                    ) : (
                      <div className="divide-y divide-border">
                        {filteredCandidates.map((candidate) => {
                          const id = String(candidate.id || '');
                          const name = String(candidate.name || id);
                          return (
                            <label key={id} className="flex cursor-pointer items-center gap-3 px-3 py-2.5 hover:bg-hoverbg">
                              <input
                                type="checkbox"
                                className="size-4 shrink-0 accent-primary"
                                checked={selectedIds.has(id)}
                                onChange={() => toggleCandidate(id)}
                              />
                              <span className="min-w-0 flex-1">
                                <span title={id} className="block truncate font-mono text-[12px] font-semibold text-strong">{id}</span>
                                {name !== id ? <span title={name} className="mt-0.5 block truncate text-[11px] text-muted-foreground">{name}</span> : null}
                              </span>
                            </label>
                          );
                        })}
                      </div>
                    )}
                  </div>
                  <DialogFooter className="items-center max-[520px]:flex-col-reverse max-[520px]:items-stretch">
                    <span className="mr-auto text-[11.5px] text-muted-foreground max-[520px]:mr-0">
                      {t('settings.discoveredModelSelection', { n: selectedIds.size })}
                    </span>
                    <Button variant="outline" onClick={closeDiscoverDialog}>{t('modal.cancel')}</Button>
                    <Button disabled={selectedIds.size === 0} onClick={confirmAddDiscovered}>
                      {t('settings.addSelectedModels', { n: selectedIds.size })}
                    </Button>
                  </DialogFooter>
                </DialogContent>
              </Dialog>
            </>
          )}
        </div>
      </div>
    </ManageWorkspace>
  );
}

function ModelRow({ model, onChange, onRemove, onModelIDCommit }: { model: ProviderModelView; onChange: (next: ProviderModelView) => void; onRemove: () => void; onModelIDCommit: () => void }) {
  const set = (patch: Partial<ProviderModelView>) => onChange({ ...model, ...patch });
  return (
    <div className="grid grid-cols-2 items-end gap-[9px] rounded-[10px] border border-border bg-background p-[11px] max-[760px]:grid-cols-1">
      {labeled(t('settings.modelID'), <Input value={String(model.id || '')} onChange={(event) => set({ id: event.target.value })} onBlur={onModelIDCommit} />)}
      {labeled(t('settings.modelName'), <Input value={String(model.name || '')} onChange={(event) => set({ name: event.target.value })} />)}
      {labeled(
        t('settings.modelContext'),
        <Input type="number" value={model.contextWindow ? String(model.contextWindow) : ''} onChange={(event) => set({ contextWindow: optionalNumber(event.target.value) })} />,
      )}
      {labeled(
        t('settings.modelMaxTokens'),
        <Input type="number" value={model.maxTokens ? String(model.maxTokens) : ''} onChange={(event) => set({ maxTokens: optionalNumber(event.target.value) })} />,
      )}
      {labeled(
        t('settings.modelInput'),
        <Input value={(model.input || []).join(', ')} onChange={(event) => set({ input: event.target.value.split(',').map((entry) => entry.trim()).filter(Boolean) })} />,
      )}
      <div className="flex min-h-[58px] items-start justify-between gap-3 rounded-[9px] border border-border bg-card p-2.5">
        <span className="text-[11.5px] font-semibold text-strong">{t('settings.modelReasoning')}</span>
        <Switch checked={model.reasoning === true} onCheckedChange={(checked) => set({ reasoning: checked })} aria-label={t('settings.modelReasoning')} />
      </div>
      <div className="col-span-full flex justify-end max-[760px]:col-auto">
        <Button variant="deny" size="sm" onClick={onRemove}>
          {t('settings.removeModel')}
        </Button>
      </div>
    </div>
  );
}
