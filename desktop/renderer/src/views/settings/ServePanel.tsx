// Serve 配置面板:mothx/manage/serve 投影。保存写入全局 serve.json,供未来
// mothx serve 启动使用;不会重启或改变当前 ACP Desktop Runtime。

import { useEffect, useState } from 'react';

import { Field, FieldGrid, ManageCard, ManageHeader, ManageWorkspace, OptionSelect, ToggleField, UnsupportedRow } from '@/components/manage-primitives';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { t } from '@/core/i18n';
import { floatOr, integerOr, loadServe, saveServe, type ServeConfigView } from '@/core/manage-api';
import { hasFeature } from '@/core/state';
import { toast } from '@/core/ui-host';
import { useAppSelector } from '@/hooks/useAppState';

interface ServeForm {
  listen: string;
  webUIDir: string;
  requestTimeout: string;
  backgroundRunMax: string;
  maxConcurrent: string;
  logLevel: string;
  defaultMode: string;
  defaultThinking: string;
  systemPromptMode: string;
  webUI: boolean;
  openAIAPI: boolean;
  multiAgent: boolean;
  cronFeature: boolean;
  memoryFeature: boolean;
  enableDelegate: boolean;
  enableWorkflows: boolean;
  enableWebSearch: boolean;
  enableBrowser: boolean;
  enableArtifact: boolean;
  enableA2AMaster: boolean;
  toolMode: string;
  toolDetail: string;
  cronEnabled: boolean;
  cronInterval: string;
  memoryEnabled: boolean;
  memoryPath: string;
  smartApprovals: boolean;
  idleTimeout: string;
  maxSessions: string;
  maxTurns: string;
  budgetPressure: boolean;
  contextPressure: boolean;
  budgetThreshold: string;
  contextThreshold: string;
  runStaleTimeout: string;
  runMaxDuration: string;
  agentBackgroundRunMax: string;
  lobsterMode: boolean;
}

function fromView(view: ServeConfigView): ServeForm {
  const api = view.api || {};
  const features = view.features || {};
  const webUI = view.webUI || {};
  const cron = view.cron || {};
  const memory = view.memory || {};
  const security = view.security || {};
  const agent = view.agent || {};
  return {
    listen: api.listen || '',
    webUIDir: webUI.dir || '',
    requestTimeout: String(api.requestTimeoutSeconds ?? 1),
    backgroundRunMax: String(api.backgroundRunMaxSeconds ?? 1),
    maxConcurrent: String(api.maxConcurrentRequests ?? 0),
    logLevel: api.logLevel || 'info',
    defaultMode: api.defaultMode || 'yolo',
    defaultThinking: api.defaultThinkingLevel || 'medium',
    systemPromptMode: api.systemPromptMode || 'append',
    webUI: features.webUI === true,
    openAIAPI: features.openAIAPI === true,
    multiAgent: features.multiAgent === true,
    cronFeature: features.cron === true,
    memoryFeature: features.memory === true,
    enableDelegate: api.enableDelegate === true,
    enableWorkflows: api.enableWorkflows === true,
    enableWebSearch: api.enableWebSearch === true,
    enableBrowser: api.enableBrowser === true,
    enableArtifact: api.enableArtifact === true,
    enableA2AMaster: api.enableA2AMaster === true,
    toolMode: api.toolVisibility?.mode || 'content',
    toolDetail: api.toolVisibility?.detail || 'collapsed',
    cronEnabled: cron.enabled === true,
    cronInterval: String(cron.interval ?? 1),
    memoryEnabled: memory.enabled === true,
    memoryPath: memory.path || '',
    smartApprovals: security.smartApprovals === true,
    idleTimeout: String(api.session?.idleTimeoutSeconds ?? 1800),
    maxSessions: String(api.session?.maxSessions ?? 0),
    maxTurns: String(agent.maxTurns ?? 1),
    budgetPressure: agent.budgetPressure === true,
    contextPressure: agent.contextPressure === true,
    budgetThreshold: String(agent.budgetPressureThreshold ?? 0.2),
    contextThreshold: String(agent.contextPressureThreshold ?? 0.55),
    runStaleTimeout: String(agent.runStaleTimeoutSeconds ?? 1),
    runMaxDuration: String(agent.runMaxDurationSeconds ?? 1),
    agentBackgroundRunMax: String(agent.backgroundRunMaxSecs ?? 1),
    lobsterMode: view.lobsterMode === true,
  };
}

function toPatch(form: ServeForm): Record<string, unknown> {
  return {
    api: {
      listen: form.listen.trim(),
      defaultMode: form.defaultMode,
      defaultThinkingLevel: form.defaultThinking,
      enableSubAgents: form.multiAgent,
      enableDelegate: form.enableDelegate,
      enableWorkflows: form.enableWorkflows,
      enableWebSearch: form.enableWebSearch,
      enableBrowser: form.enableBrowser,
      enableArtifact: form.enableArtifact,
      enableA2AMaster: form.enableA2AMaster,
      toolVisibility: { mode: form.toolMode, detail: form.toolDetail },
      systemPromptMode: form.systemPromptMode,
      requestTimeoutSeconds: integerOr(form.requestTimeout, 1),
      backgroundRunMaxSeconds: integerOr(form.backgroundRunMax, 1),
      maxConcurrentRequests: integerOr(form.maxConcurrent, 0),
      logLevel: form.logLevel,
      session: {
        idleTimeoutSeconds: integerOr(form.idleTimeout, 1),
        maxSessions: integerOr(form.maxSessions, 0),
      },
    },
    features: {
      webUI: form.webUI,
      openAIAPI: form.openAIAPI,
      multiAgent: form.multiAgent,
      cron: form.cronFeature,
      memory: form.memoryFeature,
    },
    webUI: { enabled: form.webUI, dir: form.webUIDir.trim() },
    cron: { enabled: form.cronFeature, interval: integerOr(form.cronInterval, 1) },
    memory: { enabled: form.memoryFeature, path: form.memoryPath.trim() },
    security: { smartApprovals: form.smartApprovals },
    agent: {
      maxTurns: integerOr(form.maxTurns, 1),
      budgetPressure: form.budgetPressure,
      contextPressure: form.contextPressure,
      budgetPressureThreshold: floatOr(form.budgetThreshold, 0, 1),
      contextPressureThreshold: floatOr(form.contextThreshold, 0, 1),
      runStaleTimeoutSeconds: integerOr(form.runStaleTimeout, 1),
      runMaxDurationSeconds: integerOr(form.runMaxDuration, 1),
      backgroundRunMaxSecs: integerOr(form.agentBackgroundRunMax, 1),
    },
    lobsterMode: form.lobsterMode,
  };
}

export function ServePanel() {
  const ready = useAppSelector((state) => state.connection.state === 'ready');
  const supported = hasFeature('manageServeConfig');
  const [form, setForm] = useState<ServeForm | null>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!ready || !supported) return;
    let cancelled = false;
    void loadServe().then((view) => {
      if (!cancelled && view) setForm(fromView(view));
    });
    return () => {
      cancelled = true;
    };
  }, [ready, supported]);

  if (!supported) return <UnsupportedRow text={t('manage.unsupported')} />;
  if (!form) return <UnsupportedRow text="…" />;

  const set = <K extends keyof ServeForm>(key: K, value: ServeForm[K]) =>
    setForm((current) => (current ? { ...current, [key]: value } : current));

  const save = async () => {
    setSaving(true);
    try {
      const updated = await saveServe(toPatch(form));
      setForm(fromView(updated));
      toast(t('settings.serveSaved'));
    } catch (error) {
      toast(error instanceof Error ? error.message : String(error));
    } finally {
      setSaving(false);
    }
  };

  return (
    <ManageWorkspace>
      <ManageHeader
        eyebrow={t('settings.serveGroup')}
        title={t('settings.serveTitle')}
        desc={t('settings.serveDesc')}
        actions={
          <Button disabled={saving} onClick={() => void save()}>
            {saving ? t('settings.serveSaving') : t('settings.serveSave')}
          </Button>
        }
      />

      <ManageCard title={t('settings.serveRuntime')} desc={t('settings.serveRuntimeDesc')}>
        <FieldGrid>
          <Field label={t('settings.serveListen')}>
            <Input value={form.listen} onChange={(event) => set('listen', event.target.value)} />
          </Field>
          <Field label={t('settings.serveWebUIDir')}>
            <Input value={form.webUIDir} onChange={(event) => set('webUIDir', event.target.value)} />
          </Field>
          <Field label={t('settings.serveRequestTimeout')}>
            <Input type="number" min={1} value={form.requestTimeout} onChange={(event) => set('requestTimeout', event.target.value)} />
          </Field>
          <Field label={t('settings.serveBackgroundRunMax')}>
            <Input type="number" min={1} value={form.backgroundRunMax} onChange={(event) => set('backgroundRunMax', event.target.value)} />
          </Field>
          <Field label={t('settings.serveMaxConcurrent')}>
            <Input type="number" min={0} value={form.maxConcurrent} onChange={(event) => set('maxConcurrent', event.target.value)} />
          </Field>
          <Field label={t('settings.serveLogLevel')}>
            <OptionSelect value={form.logLevel} options={['debug', 'info', 'warn', 'error']} onChange={(value) => set('logLevel', value)} />
          </Field>
          <Field label={t('settings.serveDefaultMode')}>
            <OptionSelect value={form.defaultMode} options={['yolo', 'agent', 'plan', 'os']} onChange={(value) => set('defaultMode', value)} />
          </Field>
          <Field label={t('settings.serveDefaultThinking')}>
            <OptionSelect value={form.defaultThinking} options={['off', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max']} onChange={(value) => set('defaultThinking', value)} />
          </Field>
          <Field label={t('settings.serveSystemPromptMode')}>
            <OptionSelect value={form.systemPromptMode} options={['append', 'ignore']} onChange={(value) => set('systemPromptMode', value)} />
          </Field>
        </FieldGrid>
      </ManageCard>

      <ManageCard title={t('settings.serveFeatures')} desc={t('settings.serveFeaturesDesc')}>
        <FieldGrid>
          <ToggleField label={t('settings.serveWebUI')} checked={form.webUI} onChange={(value) => set('webUI', value)} />
          <ToggleField label={t('settings.serveOpenAIAPI')} checked={form.openAIAPI} onChange={(value) => set('openAIAPI', value)} />
          <ToggleField label={t('settings.serveMultiAgent')} checked={form.multiAgent} onChange={(value) => set('multiAgent', value)} />
          <ToggleField label={t('settings.serveCronFeature')} checked={form.cronFeature} onChange={(value) => set('cronFeature', value)} />
          <ToggleField label={t('settings.serveMemoryFeature')} checked={form.memoryFeature} onChange={(value) => set('memoryFeature', value)} />
        </FieldGrid>
      </ManageCard>

      <ManageCard title={t('settings.serveCapabilities')} desc={t('settings.serveCapabilitiesDesc')}>
        <FieldGrid>
          <ToggleField label={t('settings.serveEnableDelegate')} checked={form.enableDelegate} onChange={(value) => set('enableDelegate', value)} />
          <ToggleField label={t('settings.serveEnableWorkflows')} checked={form.enableWorkflows} onChange={(value) => set('enableWorkflows', value)} />
          <ToggleField label={t('settings.serveEnableWebSearch')} checked={form.enableWebSearch} onChange={(value) => set('enableWebSearch', value)} />
          <ToggleField label={t('settings.serveEnableBrowser')} checked={form.enableBrowser} onChange={(value) => set('enableBrowser', value)} />
          <ToggleField label={t('settings.serveEnableArtifact')} checked={form.enableArtifact} onChange={(value) => set('enableArtifact', value)} />
          <ToggleField label={t('settings.serveEnableA2AMaster')} checked={form.enableA2AMaster} onChange={(value) => set('enableA2AMaster', value)} />
        </FieldGrid>
      </ManageCard>

      <ManageCard title={t('settings.serveOutput')} desc={t('settings.serveOutputDesc')}>
        <FieldGrid>
          <Field label={t('settings.serveToolMode')}>
            <OptionSelect value={form.toolMode} options={['content', 'sse_event', 'none']} onChange={(value) => set('toolMode', value)} />
          </Field>
          <Field label={t('settings.serveToolDetail')}>
            <OptionSelect value={form.toolDetail} options={['collapsed', 'expanded']} onChange={(value) => set('toolDetail', value)} />
          </Field>
        </FieldGrid>
      </ManageCard>

      <ManageCard title={t('settings.serveAutomation')} desc={t('settings.serveAutomationDesc')}>
        <FieldGrid>
          <ToggleField label={t('settings.serveCronEnabled')} checked={form.cronFeature} onChange={(value) => set('cronFeature', value)} />
          <Field label={t('settings.serveCronInterval')}>
            <Input type="number" min={1} value={form.cronInterval} onChange={(event) => set('cronInterval', event.target.value)} />
          </Field>
          <ToggleField label={t('settings.serveMemoryEnabled')} checked={form.memoryFeature} onChange={(value) => set('memoryFeature', value)} />
          <Field label={t('settings.serveMemoryPath')}>
            <Input value={form.memoryPath} onChange={(event) => set('memoryPath', event.target.value)} />
          </Field>
        </FieldGrid>
      </ManageCard>

      <ManageCard title={t('settings.serveSecurity')} desc={t('settings.serveSecurityDesc')}>
        <FieldGrid>
          <ToggleField label={t('settings.serveSmartApprovals')} checked={form.smartApprovals} onChange={(value) => set('smartApprovals', value)} />
        </FieldGrid>
      </ManageCard>

      <ManageCard title={t('settings.serveSessions')} desc={t('settings.serveSessionsDesc')}>
        <FieldGrid>
          <Field label={t('settings.serveIdleTimeout')}>
            <Input type="number" min={1} value={form.idleTimeout} onChange={(event) => set('idleTimeout', event.target.value)} />
          </Field>
          <Field label={t('settings.serveMaxSessions')}>
            <Input type="number" min={0} placeholder={t('settings.serveMaxSessionsHint')} value={form.maxSessions} onChange={(event) => set('maxSessions', event.target.value)} />
          </Field>
        </FieldGrid>
      </ManageCard>

      <ManageCard title={t('settings.serveAgent')} desc={t('settings.serveAgentDesc')}>
        <FieldGrid>
          <Field label={t('settings.serveMaxTurns')}>
            <Input type="number" min={1} value={form.maxTurns} onChange={(event) => set('maxTurns', event.target.value)} />
          </Field>
          <ToggleField label={t('settings.serveBudgetPressure')} checked={form.budgetPressure} onChange={(value) => set('budgetPressure', value)} />
          <ToggleField label={t('settings.serveContextPressure')} checked={form.contextPressure} onChange={(value) => set('contextPressure', value)} />
          <Field label={t('settings.serveBudgetThreshold')}>
            <Input type="number" min={0} max={1} step={0.01} value={form.budgetThreshold} onChange={(event) => set('budgetThreshold', event.target.value)} />
          </Field>
          <Field label={t('settings.serveContextThreshold')}>
            <Input type="number" min={0} max={1} step={0.01} value={form.contextThreshold} onChange={(event) => set('contextThreshold', event.target.value)} />
          </Field>
          <Field label={t('settings.serveRunStaleTimeout')}>
            <Input type="number" min={1} value={form.runStaleTimeout} onChange={(event) => set('runStaleTimeout', event.target.value)} />
          </Field>
          <Field label={t('settings.serveRunMaxDuration')}>
            <Input type="number" min={1} value={form.runMaxDuration} onChange={(event) => set('runMaxDuration', event.target.value)} />
          </Field>
          <Field label={t('settings.serveAgentBackgroundRunMax')}>
            <Input type="number" min={1} value={form.agentBackgroundRunMax} onChange={(event) => set('agentBackgroundRunMax', event.target.value)} />
          </Field>
        </FieldGrid>
      </ManageCard>

      <ManageCard title={t('settings.serveLobsterMode')} desc={t('settings.serveLobsterModeDesc')}>
        <FieldGrid>
          <ToggleField label={t('settings.serveLobsterMode')} checked={form.lobsterMode} onChange={(value) => set('lobsterMode', value)} />
        </FieldGrid>
      </ManageCard>
    </ManageWorkspace>
  );
}
