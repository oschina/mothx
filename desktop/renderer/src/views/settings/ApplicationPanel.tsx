// Agent 应用配置面板:mothx/manage/application 的表单投影。密钥与内容
// 不回显;保存走 application/patch,由共享 Runtime 落盘。

import { useEffect, useState } from 'react';

import { Field, FieldGrid, ManageCard, ManageHeader, ManageWorkspace, OptionSelect, ToggleField, UnsupportedRow } from '@/components/manage-primitives';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { t } from '@/core/i18n';
import {
  applicationLines,
  integerOr,
  loadApplication,
  saveApplication,
  type ApplicationSettingsView,
} from '@/core/manage-api';
import { hasFeature } from '@/core/state';
import { toast } from '@/core/ui-host';
import { useAppSelector } from '@/hooks/useAppState';

interface ApplicationForm {
  defaultMode: string;
  enablePlanTool: boolean;
  enableArtifact: boolean;
  enableACPArtifact: boolean;
  authored: boolean;
  updateCheck: boolean;
  contextEnabled: boolean;
  extraFiles: string;
  compactionEnabled: boolean;
  reserveTokens: string;
  keepRecentTokens: string;
  tokenizer: string;
  tokenizerModel: string;
  compactionTemplate: string;
  webSearchEnabled: boolean;
  webSearchBackend: string;
  webSearchProvider: string;
  webSearchBaseURL: string;
  webSearchToken: string;
  webSearchTokenConfigured: boolean;
  clearWebSearchToken: boolean;
  toolMode: string;
  toolConcurrency: string;
  imageEnabled: boolean;
  imageProvider: string;
  imageAPI: string;
  imageBaseURL: string;
  imageModel: string;
  imageToken: string;
  imageTokenConfigured: boolean;
  clearImageToken: boolean;
  retryEnabled: boolean;
  maxRetries: string;
  baseDelay: string;
  statusLineEnabled: boolean;
  statusType: string;
  statusCommand: string;
  statusPadding: string;
  statusRefresh: string;
  statusTimeout: string;
  statusFallback: string;
  sandboxEnabled: boolean;
  allowNetwork: boolean;
  protectGit: boolean;
  sandboxLevel: string;
  bwrapPath: string;
  tmpSize: string;
  allowedRead: string;
  allowedWrite: string;
  deniedPaths: string;
  confirmWrite: boolean;
  bashWhitelist: string;
  bashBlacklist: string;
}

function fromView(view: ApplicationSettingsView): ApplicationForm {
  const defaults = view.defaults || {};
  const context = view.contextFiles || {};
  const compaction = view.compaction || {};
  const tools = view.toolExecution || {};
  const webSearch = view.webSearch || {};
  const image = view.imageGeneration || {};
  const retry = view.retry || {};
  const statusLine = view.statusLine || {};
  const sandbox = view.sandbox || {};
  const approval = view.approval || {};
  return {
    defaultMode: defaults.defaultMode || 'yolo',
    enablePlanTool: defaults.enablePlanTool === true,
    enableArtifact: defaults.enableArtifact === true,
    enableACPArtifact: defaults.enableACPArtifact === true,
    authored: defaults.authored === true,
    updateCheck: defaults.updateCheck !== false,
    contextEnabled: context.enabled !== false,
    extraFiles: (context.extraFiles || []).join('\n'),
    compactionEnabled: compaction.enabled !== false,
    reserveTokens: String(compaction.reserveTokens ?? 0),
    keepRecentTokens: String(compaction.keepRecentTokens ?? 0),
    tokenizer: compaction.tokenizer || '',
    tokenizerModel: compaction.tokenizerModel || '',
    compactionTemplate: compaction.template || '',
    webSearchEnabled: webSearch.enabled === true,
    webSearchBackend: webSearch.backend || 'deepseek',
    webSearchProvider: webSearch.provider || '',
    webSearchBaseURL: webSearch.baseUrl || '',
    webSearchToken: '',
    webSearchTokenConfigured: webSearch.tokenConfigured === true,
    clearWebSearchToken: false,
    toolMode: tools.mode || 'parallel',
    toolConcurrency: String(tools.maxConcurrency ?? 1),
    imageEnabled: image.enabled === true,
    imageProvider: image.provider || '',
    imageAPI: image.apiType || '',
    imageBaseURL: image.baseUrl || '',
    imageModel: image.model || '',
    imageToken: '',
    imageTokenConfigured: image.tokenConfigured === true,
    clearImageToken: false,
    retryEnabled: retry.enabled !== false,
    maxRetries: String(retry.maxRetries ?? 0),
    baseDelay: String(retry.baseDelayMs ?? 0),
    statusLineEnabled: statusLine.enabled === true,
    statusType: statusLine.type || '',
    statusCommand: statusLine.command || '',
    statusPadding: String(statusLine.padding ?? 0),
    statusRefresh: String(statusLine.refreshInterval ?? 0),
    statusTimeout: String(statusLine.timeoutMs ?? 0),
    statusFallback: statusLine.fallback || '',
    sandboxEnabled: sandbox.enabled === true,
    allowNetwork: sandbox.allowNetwork === true,
    protectGit: sandbox.protectGit === true,
    sandboxLevel: sandbox.level || 'none',
    bwrapPath: sandbox.bwrapPath || '',
    tmpSize: sandbox.tmpSize || '',
    allowedRead: (sandbox.allowedRead || []).join('\n'),
    allowedWrite: (sandbox.allowedWrite || []).join('\n'),
    deniedPaths: (sandbox.deniedPaths || []).join('\n'),
    confirmWrite: approval.confirmBeforeWrite === true,
    bashWhitelist: (approval.bashWhitelist || []).join('\n'),
    bashBlacklist: (approval.bashBlacklist || []).join('\n'),
  };
}

function toPatch(form: ApplicationForm): Record<string, unknown> {
  return {
    defaults: {
      defaultMode: form.defaultMode,
      enablePlanTool: form.enablePlanTool,
      enableArtifact: form.enableArtifact,
      enableACPArtifact: form.enableACPArtifact,
      authored: form.authored,
      updateCheck: form.updateCheck,
    },
    contextFiles: { enabled: form.contextEnabled, extraFiles: applicationLines(form.extraFiles) },
    compaction: {
      enabled: form.compactionEnabled,
      reserveTokens: integerOr(form.reserveTokens, 0),
      keepRecentTokens: integerOr(form.keepRecentTokens, 0),
      tokenizer: form.tokenizer.trim(),
      tokenizerModel: form.tokenizerModel.trim(),
      template: form.compactionTemplate,
    },
    toolExecution: { mode: form.toolMode, maxConcurrency: integerOr(form.toolConcurrency, 1) },
    webSearch: {
      enabled: form.webSearchEnabled,
      backend: form.webSearchBackend,
      provider: form.webSearchProvider.trim(),
      baseUrl: form.webSearchBaseURL.trim(),
      ...(form.webSearchToken.trim()
        ? { token: form.webSearchToken.trim() }
        : form.clearWebSearchToken
          ? { token: '' }
          : {}),
    },
    imageGeneration: {
      enabled: form.imageEnabled,
      provider: form.imageProvider.trim(),
      apiType: form.imageAPI.trim(),
      baseUrl: form.imageBaseURL.trim(),
      model: form.imageModel.trim(),
      ...(form.imageToken.trim()
        ? { token: form.imageToken.trim() }
        : form.clearImageToken
          ? { token: '' }
          : {}),
    },
    retry: {
      enabled: form.retryEnabled,
      maxRetries: integerOr(form.maxRetries, 0),
      baseDelayMs: integerOr(form.baseDelay, 0),
    },
    statusLine: {
      enabled: form.statusLineEnabled,
      type: form.statusType.trim(),
      command: form.statusCommand.trim(),
      padding: integerOr(form.statusPadding, 0),
      refreshInterval: integerOr(form.statusRefresh, 0),
      timeoutMs: integerOr(form.statusTimeout, 0),
      fallback: form.statusFallback.trim(),
    },
    sandbox: {
      enabled: form.sandboxEnabled,
      level: form.sandboxLevel.trim(),
      bwrapPath: form.bwrapPath.trim(),
      allowNetwork: form.allowNetwork,
      allowedRead: applicationLines(form.allowedRead),
      allowedWrite: applicationLines(form.allowedWrite),
      deniedPaths: applicationLines(form.deniedPaths),
      tmpSize: form.tmpSize.trim(),
      protectGit: form.protectGit,
    },
    approval: {
      confirmBeforeWrite: form.confirmWrite,
      bashWhitelist: applicationLines(form.bashWhitelist),
      bashBlacklist: applicationLines(form.bashBlacklist),
    },
  };
}

export function ApplicationPanel() {
  const [form, setForm] = useState<ApplicationForm | null>(null);
  const [saving, setSaving] = useState(false);
  const supported = hasFeature('manageApplicationSettings');
  const ready = useAppSelector((state) => state.connection.state === 'ready');

  useEffect(() => {
    if (!ready) return;
    let cancelled = false;
    void loadApplication().then((view) => {
      if (!cancelled && view) setForm(fromView(view));
    });
    return () => {
      cancelled = true;
    };
  }, [ready]);

  if (!supported) return <UnsupportedRow text={t('manage.unsupported')} />;
  if (!form) return <UnsupportedRow text="…" />;

  const set = <K extends keyof ApplicationForm>(key: K, value: ApplicationForm[K]) =>
    setForm((current) => (current ? { ...current, [key]: value } : current));

  const save = async () => {
    setSaving(true);
    try {
      const updated = await saveApplication(toPatch(form));
      if (updated) setForm(fromView(updated));
      toast(t('settings.applicationSaved'));
    } catch (error) {
      toast(error instanceof Error ? error.message : String(error));
    } finally {
      setSaving(false);
    }
  };

  return (
    <ManageWorkspace>
      <ManageHeader
        eyebrow={t('settings.application')}
        title={t('settings.applicationTitle')}
        desc={t('settings.applicationDesc')}
        actions={
          <Button disabled={saving} onClick={() => void save()}>
            {saving ? t('settings.applicationSaving') : t('settings.applicationSave')}
          </Button>
        }
      />

      <ManageCard title={t('settings.applicationDefaults')} desc={t('settings.applicationDefaultsDesc')}>
        <FieldGrid>
          <Field label={t('settings.applicationDefaultMode')}>
            <OptionSelect value={form.defaultMode} options={['agent', 'plan', 'yolo', 'os']} onChange={(value) => set('defaultMode', value)} />
          </Field>
          <ToggleField label={t('settings.applicationEnablePlanTool')} checked={form.enablePlanTool} onChange={(value) => set('enablePlanTool', value)} />
          <ToggleField label={t('settings.applicationEnableArtifact')} checked={form.enableArtifact} onChange={(value) => set('enableArtifact', value)} />
          <ToggleField label={t('settings.applicationEnableACPArtifact')} checked={form.enableACPArtifact} onChange={(value) => set('enableACPArtifact', value)} />
          <ToggleField label={t('settings.applicationAuthored')} checked={form.authored} onChange={(value) => set('authored', value)} />
          <ToggleField label={t('settings.applicationUpdateCheck')} checked={form.updateCheck} onChange={(value) => set('updateCheck', value)} />
        </FieldGrid>
      </ManageCard>

      <ManageCard title={t('settings.applicationContext')} desc={t('settings.applicationContextDesc')}>
        <FieldGrid>
          <ToggleField label={t('settings.applicationContextFiles')} checked={form.contextEnabled} onChange={(value) => set('contextEnabled', value)} />
          <ToggleField label={t('settings.applicationCompaction')} checked={form.compactionEnabled} onChange={(value) => set('compactionEnabled', value)} />
          <Field label={t('settings.applicationReserveTokens')}>
            <Input type="number" min={0} step={1} value={form.reserveTokens} onChange={(event) => set('reserveTokens', event.target.value)} />
          </Field>
          <Field label={t('settings.applicationKeepRecentTokens')}>
            <Input type="number" min={0} step={1} value={form.keepRecentTokens} onChange={(event) => set('keepRecentTokens', event.target.value)} />
          </Field>
          <Field label={t('settings.applicationTokenizer')}>
            <Input value={form.tokenizer} onChange={(event) => set('tokenizer', event.target.value)} />
          </Field>
          <Field label={t('settings.applicationTokenizerModel')}>
            <Input value={form.tokenizerModel} onChange={(event) => set('tokenizerModel', event.target.value)} />
          </Field>
          <Field label={t('settings.applicationExtraFiles')} full>
            <Textarea rows={3} className="min-h-[74px] resize-y leading-snug" value={form.extraFiles} onChange={(event) => set('extraFiles', event.target.value)} />
          </Field>
          <Field label={t('settings.applicationCompactionTemplate')} full>
            <Textarea rows={3} className="min-h-[74px] resize-y leading-snug" value={form.compactionTemplate} onChange={(event) => set('compactionTemplate', event.target.value)} />
          </Field>
        </FieldGrid>
      </ManageCard>

      <ManageCard title={t('settings.applicationTools')} desc={t('settings.applicationToolsDesc')}>
        <FieldGrid>
          <ToggleField label={t('settings.applicationWebSearch')} checked={form.webSearchEnabled} onChange={(value) => set('webSearchEnabled', value)} />
          <Field label={t('settings.applicationWebSearchBackend')}>
            <OptionSelect
              value={form.webSearchBackend || 'deepseek'}
              options={['deepseek', 'yuanbao']}
              onChange={(value) => {
                set('webSearchBackend', value);
                // Endpoint and credential belong to one backend. Clear the
                // stored token too, otherwise a DeepSeek key would silently
                // authenticate against the Yuanbao endpoint (and vice versa).
                set('webSearchBaseURL', '');
                set('webSearchToken', '');
                set('clearWebSearchToken', true);
              }}
            />
          </Field>
          <Field label={t('settings.applicationWebSearchProvider')}>
            <Input
              value={form.webSearchProvider}
              disabled={form.webSearchBackend === 'yuanbao'}
              onChange={(event) => set('webSearchProvider', event.target.value)}
            />
          </Field>
          <Field label={t('settings.applicationWebSearchBaseUrl')}>
            <Input
              value={form.webSearchBaseURL}
              placeholder={form.webSearchBackend === 'yuanbao' ? 'https://api.wsa.cloud.tencent.com' : 'https://api.deepseek.com'}
              onChange={(event) => set('webSearchBaseURL', event.target.value)}
            />
          </Field>
          <Field label={t('settings.applicationWebSearchToken')}>
            <Input
              type="password"
              value={form.webSearchToken}
              placeholder={form.webSearchTokenConfigured ? t('settings.applicationWebSearchTokenConfigured') : (form.webSearchBackend === 'yuanbao' ? '${TENCENTCLOUD_WSA_APIKEY}' : '${DEEPSEEK_API_KEY}')}
              onChange={(event) => set('webSearchToken', event.target.value)}
            />
          </Field>
          <ToggleField label={t('settings.applicationWebSearchClearToken')} checked={form.clearWebSearchToken} onChange={(value) => set('clearWebSearchToken', value)} />
          <Field label={t('settings.applicationToolExecutionMode')}>
            <OptionSelect value={form.toolMode} options={['parallel', 'sequential']} onChange={(value) => set('toolMode', value)} />
          </Field>
          <Field label={t('settings.applicationToolConcurrency')}>
            <Input type="number" min={1} step={1} value={form.toolConcurrency} onChange={(event) => set('toolConcurrency', event.target.value)} />
          </Field>
        </FieldGrid>
      </ManageCard>

      <ManageCard title={t('settings.applicationImage')} desc={t('settings.applicationImageDesc')}>
        <FieldGrid>
          <ToggleField label={t('settings.applicationImageEnabled')} checked={form.imageEnabled} onChange={(value) => set('imageEnabled', value)} />
          <Field label={t('settings.applicationImageProvider')}>
            <Input value={form.imageProvider} onChange={(event) => set('imageProvider', event.target.value)} />
          </Field>
          <Field label={t('settings.applicationImageAPI')}>
            <Input value={form.imageAPI} onChange={(event) => set('imageAPI', event.target.value)} />
          </Field>
          <Field label={t('settings.applicationImageBaseURL')}>
            <Input value={form.imageBaseURL} onChange={(event) => set('imageBaseURL', event.target.value)} />
          </Field>
          <Field label={t('settings.applicationImageModel')}>
            <Input value={form.imageModel} onChange={(event) => set('imageModel', event.target.value)} />
          </Field>
          <Field label={t('settings.applicationImageToken')}>
            <Input
              type="password"
              value={form.imageToken}
              placeholder={form.imageTokenConfigured ? t('settings.applicationTokenConfigured') : t('settings.applicationTokenUnset')}
              onChange={(event) => set('imageToken', event.target.value)}
            />
          </Field>
          <ToggleField label={t('settings.applicationClearToken')} checked={form.clearImageToken} onChange={(value) => set('clearImageToken', value)} />
        </FieldGrid>
      </ManageCard>

      <ManageCard title={t('settings.applicationRuntime')} desc={t('settings.applicationRuntimeDesc')}>
        <FieldGrid>
          <ToggleField label={t('settings.applicationRetry')} checked={form.retryEnabled} onChange={(value) => set('retryEnabled', value)} />
          <Field label={t('settings.applicationMaxRetries')}>
            <Input type="number" min={0} step={1} value={form.maxRetries} onChange={(event) => set('maxRetries', event.target.value)} />
          </Field>
          <Field label={t('settings.applicationBaseDelay')}>
            <Input type="number" min={0} step={1} value={form.baseDelay} onChange={(event) => set('baseDelay', event.target.value)} />
          </Field>
          <ToggleField label={t('settings.applicationStatusLine')} checked={form.statusLineEnabled} onChange={(value) => set('statusLineEnabled', value)} />
          <Field label={t('settings.applicationStatusType')}>
            <Input value={form.statusType} onChange={(event) => set('statusType', event.target.value)} />
          </Field>
          <Field label={t('settings.applicationStatusCommand')} full>
            <Input value={form.statusCommand} onChange={(event) => set('statusCommand', event.target.value)} />
          </Field>
          <Field label={t('settings.applicationStatusPadding')}>
            <Input type="number" min={0} step={1} value={form.statusPadding} onChange={(event) => set('statusPadding', event.target.value)} />
          </Field>
          <Field label={t('settings.applicationStatusRefresh')}>
            <Input type="number" min={0} step={1} value={form.statusRefresh} onChange={(event) => set('statusRefresh', event.target.value)} />
          </Field>
          <Field label={t('settings.applicationStatusTimeout')}>
            <Input type="number" min={0} step={1} value={form.statusTimeout} onChange={(event) => set('statusTimeout', event.target.value)} />
          </Field>
          <Field label={t('settings.applicationStatusFallback')}>
            <Input value={form.statusFallback} onChange={(event) => set('statusFallback', event.target.value)} />
          </Field>
        </FieldGrid>
      </ManageCard>

      <ManageCard title={t('settings.applicationSafety')} desc={t('settings.applicationSafetyDesc')}>
        <FieldGrid>
          <ToggleField label={t('settings.applicationSandbox')} checked={form.sandboxEnabled} onChange={(value) => set('sandboxEnabled', value)} />
          <ToggleField label={t('settings.applicationAllowNetwork')} checked={form.allowNetwork} onChange={(value) => set('allowNetwork', value)} />
          <ToggleField label={t('settings.applicationProtectGit')} checked={form.protectGit} onChange={(value) => set('protectGit', value)} />
          <Field label={t('settings.applicationSandboxLevel')}>
            <Input value={form.sandboxLevel} onChange={(event) => set('sandboxLevel', event.target.value)} />
          </Field>
          <Field label={t('settings.applicationBwrapPath')}>
            <Input value={form.bwrapPath} onChange={(event) => set('bwrapPath', event.target.value)} />
          </Field>
          <Field label={t('settings.applicationTmpSize')}>
            <Input value={form.tmpSize} onChange={(event) => set('tmpSize', event.target.value)} />
          </Field>
          <Field label={t('settings.applicationAllowedRead')} full>
            <Textarea rows={3} className="min-h-[74px] resize-y leading-snug" value={form.allowedRead} onChange={(event) => set('allowedRead', event.target.value)} />
          </Field>
          <Field label={t('settings.applicationAllowedWrite')} full>
            <Textarea rows={3} className="min-h-[74px] resize-y leading-snug" value={form.allowedWrite} onChange={(event) => set('allowedWrite', event.target.value)} />
          </Field>
          <Field label={t('settings.applicationDeniedPaths')} full>
            <Textarea rows={3} className="min-h-[74px] resize-y leading-snug" value={form.deniedPaths} onChange={(event) => set('deniedPaths', event.target.value)} />
          </Field>
          <ToggleField label={t('settings.applicationConfirmWrite')} checked={form.confirmWrite} onChange={(value) => set('confirmWrite', value)} />
          <Field label={t('settings.applicationBashWhitelist')} full>
            <Textarea rows={3} className="min-h-[74px] resize-y leading-snug" value={form.bashWhitelist} onChange={(event) => set('bashWhitelist', event.target.value)} />
          </Field>
          <Field label={t('settings.applicationBashBlacklist')} full>
            <Textarea rows={3} className="min-h-[74px] resize-y leading-snug" value={form.bashBlacklist} onChange={(event) => set('bashBlacklist', event.target.value)} />
          </Field>
        </FieldGrid>
      </ManageCard>
    </ManageWorkspace>
  );
}
