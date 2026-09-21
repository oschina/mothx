// mothx/manage/* 数据访问层:类型、缓存、能力守卫与加载/变更动作。
// 无发现键时面板显示 unsupported;Provider 密钥只掩码展示，MCP 配置则
// 通过本机 Desktop ↔ ACP IPC 完整编辑，二者都绝不落 Desktop 本地持久化。
// React 设置面板只消费这里的函数,不再自行拼装 ACP 请求。

import { desktop, invoke } from './api';
import { refreshDraftConfigOptions } from './composer';
import { hasFeature, state } from './state';
import { toast } from './ui-host';

export interface ProviderView {
  name: string;
  maskedKey?: string | null;
  baseUrl?: string;
  modelCount?: number;
}
export interface SettingsView {
  defaultProvider?: string;
  defaultModel?: string;
  defaultMode?: string;
  thinkingLevel?: string;
  providers?: ProviderView[];
}
export interface ApplicationSettingsView {
  defaults?: {
    defaultMode?: string;
    enablePlanTool?: boolean;
    enableArtifact?: boolean;
    enableACPArtifact?: boolean;
    authored?: boolean;
    updateCheck?: boolean;
  };
  contextFiles?: { enabled?: boolean; extraFiles?: string[] };
  compaction?: { enabled?: boolean; reserveTokens?: number; keepRecentTokens?: number; tokenizer?: string; tokenizerModel?: string; template?: string };
  toolExecution?: { mode?: string; maxConcurrency?: number };
  webSearch?: { enabled?: boolean; provider?: string; providerType?: string; model?: string };
  imageGeneration?: { enabled?: boolean; provider?: string; apiType?: string; baseUrl?: string; model?: string; tokenConfigured?: boolean };
  retry?: { enabled?: boolean; maxRetries?: number; baseDelayMs?: number };
  statusLine?: { enabled?: boolean; type?: string; command?: string; padding?: number; refreshInterval?: number; timeoutMs?: number; fallback?: string };
  sandbox?: { enabled?: boolean; level?: string; bwrapPath?: string; allowNetwork?: boolean; allowedRead?: string[]; allowedWrite?: string[]; deniedPaths?: string[]; tmpSize?: string; protectGit?: boolean };
  approval?: { bashWhitelist?: string[]; bashBlacklist?: string[]; confirmBeforeWrite?: boolean };
}
export interface ServeConfigAPIView {
  listen?: string;
  defaultMode?: string;
  defaultThinkingLevel?: string;
  enableSubAgents?: boolean;
  enableDelegate?: boolean;
  enableWorkflows?: boolean;
  enableWebSearch?: boolean;
  enableBrowser?: boolean;
  enableArtifact?: boolean;
  enableA2AMaster?: boolean;
  toolVisibility?: { mode?: string; detail?: string };
  systemPromptMode?: string;
  requestTimeoutSeconds?: number;
  backgroundRunMaxSeconds?: number;
  maxConcurrentRequests?: number;
  logLevel?: string;
  session?: { idleTimeoutSeconds?: number; maxSessions?: number };
}
export interface ServeConfigFeaturesView {
  webUI?: boolean;
  openAIAPI?: boolean;
  multiAgent?: boolean;
  cron?: boolean;
  memory?: boolean;
}
export interface ServeConfigWebUIView { enabled?: boolean; dir?: string; }
export interface ServeConfigCronView { enabled?: boolean; interval?: number; }
export interface ServeConfigMemoryView { enabled?: boolean; path?: string; }
export interface ServeConfigSecurityView { smartApprovals?: boolean; }
export interface ServeConfigAgentView {
  maxTurns?: number;
  budgetPressure?: boolean;
  contextPressure?: boolean;
  budgetPressureThreshold?: number;
  contextPressureThreshold?: number;
  runStaleTimeoutSeconds?: number;
  runMaxDurationSeconds?: number;
  backgroundRunMaxSecs?: number;
}
export interface ServeConfigView {
  api?: ServeConfigAPIView;
  features?: ServeConfigFeaturesView;
  webUI?: ServeConfigWebUIView;
  cron?: ServeConfigCronView;
  memory?: ServeConfigMemoryView;
  security?: ServeConfigSecurityView;
  agent?: ServeConfigAgentView;
  lobsterMode?: boolean;
}
export interface ChannelsWechatView {
  enabled?: boolean;
  workDir?: string;
  autoTyping?: boolean;
  credentialConfigured?: boolean;
}
export interface ChannelsFeishuView {
  enabled?: boolean;
  workDir?: string;
  appIDConfigured?: boolean;
  appSecretConfigured?: boolean;
}
export interface ChannelsConfigView {
  artifact?: boolean;
  wechat?: ChannelsWechatView;
  feishu?: ChannelsFeishuView;
}
export interface ChannelsConfigPatch {
  artifact?: boolean;
  wechat?: ChannelsWechatPatch;
  feishu?: ChannelsFeishuPatch;
}
export interface ChannelsWechatPatch {
  enabled?: boolean;
  workDir?: string;
  autoTyping?: boolean;
  credPath?: string;
  clearCredPath?: boolean;
}
export interface ChannelsFeishuPatch {
  enabled?: boolean;
  workDir?: string;
  appId?: string;
  appSecret?: string;
  clearAppId?: boolean;
  clearAppSecret?: boolean;
}
export interface ProviderModelView {
  id?: string;
  name?: string;
  reasoning?: boolean;
  contextWindow?: number;
  maxTokens?: number;
  input?: string[];
  [key: string]: unknown;
}
export interface ProviderConfigShape {
  vendor?: string;
  baseUrl?: string;
  httpProxy?: string;
  forceHTTP11?: boolean;
  api?: string;
  thinkingFormat?: string;
  cacheControl?: boolean;
  maxImagesPerRequest?: number;
  models?: ProviderModelView[];
}
export interface ProviderConfigView {
  id: string;
  provider: ProviderConfigShape;
  maskedKey?: string | null;
  apiKeyConfigured?: boolean;
  isDefault?: boolean;
  globalOverride?: boolean;
}
export interface ProviderCatalog {
  providers?: ProviderView[];
  providerConfigs?: ProviderConfigView[];
  models?: { id?: string; name?: string; provider?: string; reasoning?: boolean; contextWindow?: number; maxTokens?: number; input?: string[] }[];
  modelDefaults?: ProviderModelView;
  defaultProvider?: string;
  defaultModel?: string;
}
export interface SkillHubMarketView {
  id: string;
  name?: string;
  siteURL?: string;
  apiURL?: string;
  enabled?: boolean;
  apiTokenConfigured?: boolean;
}
export interface SkillHubView {
  defaultMarket?: string;
  defaultInstallScope?: string;
  officialHandles?: string[];
  markets?: SkillHubMarketView[];
}
export interface SkillView {
  name: string;
  description?: string;
  source?: string;
  enabled?: boolean;
}
export interface McpServerView {
  name: string;
  type?: string;
  command?: string;
  args?: string[];
  url?: string;
  messageUrl?: string;
  enabled?: boolean;
  headers?: McpNameValue[];
  env?: McpNameValue[];
}
export interface McpNameValue { name: string; value: string; }
export interface StatsSummary {
  sessions?: number;
  runs?: number;
  tokens?: { input?: number; output?: number };
  cost?: number;
}
export interface StatsPoint {
  date?: string;
  runs?: number;
  tokens?: number;
  cost?: number;
}
export interface KnowledgeBaseSpec {
  name: string;
  rootDir: string;
  preprocessProfile: string;
  provider: string;
  model: string;
  mode: string;
  thinkingLevel?: string;
  schedule: string;
  enabled: boolean;
}
export interface KnowledgeBase extends KnowledgeBaseSpec {
  id: string;
  activeSnapshotId?: string;
  createdAt?: string;
  updatedAt?: string;
}
export interface KnowledgeSnapshot {
  id: string;
  status: string;
  fileCount?: number;
  chunkCount?: number;
  nodeCount?: number;
  edgeCount?: number;
  startedAt?: string;
  finishedAt?: string;
  errorSummary?: string;
}
export interface KnowledgeIndexProgressView {
  running: boolean;
  phase?: string;
  filesTotal: number;
  filesDone: number;
  chunks: number;
  startedAt?: string;
  runId?: string;
  error?: string;
}
export interface KnowledgeBaseView {
  knowledgeBase: KnowledgeBase;
  snapshot?: KnowledgeSnapshot | null;
  status?: string;
  indexing?: KnowledgeIndexProgressView;
}
export interface CronJobView {
  id: string;
  name?: string;
  schedule?: string;
  prompt?: string;
  mode?: string;
  enabled?: boolean;
  workDir?: string;
  sessionId?: string;
  a2aTarget?: string;
  runCount?: number;
  lastStatus?: string;
  lastRun?: string;
  nextRun?: string;
  lastError?: string;
  createdAt?: string;
  provider?: string;
  model?: string;
  [key: string]: unknown;
}
export interface EnvVariableView {
  name: string;
  valueConfigured?: boolean;
}
export interface EnvView {
  variables?: EnvVariableView[];
}
export interface EnvPatch {
  set?: { name: string; value: string }[];
  unset?: string[];
}
export interface ExpertLocalizedText { zh?: string; en?: string; }
export interface ExpertMemberMeta {
  id: string;
  name?: ExpertLocalizedText;
  profession?: ExpertLocalizedText;
  avatar?: string;
  role: string;
}
export interface ExpertTeamInfo { leadAgent: string; memberAgents: string[]; }
export interface ExpertManifest {
  schemaVersion?: number;
  name: string;
  expertType?: string;
  agentName?: string;
  displayName?: ExpertLocalizedText;
  categoryId?: string;
  quickPrompts?: ExpertLocalizedText[];
  defaultInitPrompt?: ExpertLocalizedText;
  teamInfo?: ExpertTeamInfo;
  members?: ExpertMemberMeta[];
}
export interface ExpertBundle {
  scope: 'global' | 'project' | 'builtin' | string;
  manifest: ExpertManifest;
  agents: Record<string, string>;
}
export interface ExpertSummary {
  name: string;
  expertType?: string;
  displayName?: ExpertLocalizedText;
  source?: string;
  invalid?: boolean;
  invalidReason?: string;
}
export interface ExpertListView {
  scope: string;
  cwd?: string;
  experts?: ExpertSummary[];
  effectiveExperts?: ExpertSummary[];
  bundle?: ExpertBundle;
}
export interface DoctorCheck {
  id?: string;
  title?: string;
  status?: string;
  detail?: string;
  fix?: string;
}
export interface DoctorResult {
  checks?: DoctorCheck[];
  version?: string;
}

export const cache: {
  settings?: SettingsView;
  application?: ApplicationSettingsView;
  providerCatalog?: ProviderCatalog;
  skillHub?: SkillHubView;
  skills?: SkillView[];
  memory?: string;
  stats?: StatsSummary;
  points?: StatsPoint[];
  cron?: CronJobView[];
  knowledgeBases?: KnowledgeBaseView[];
  serve?: ServeConfigView;
  channels?: ChannelsConfigView;
  env?: EnvView;
  experts?: ExpertListView;
} = {};

export async function guard<T>(feature: string, fn: () => Promise<T>, fallback: T): Promise<T> {
  if (!hasFeature(feature)) return fallback;
  try {
    return await fn();
  } catch (error) {
    toast(error instanceof Error ? error.message : String(error));
    return fallback;
  }
}

// ---- application ----

export async function loadApplication(): Promise<ApplicationSettingsView | undefined> {
  if (!hasFeature('manageApplicationSettings')) return undefined;
  const view = await guard('manageApplicationSettings', () => invoke<ApplicationSettingsView>('mothx/manage/application/get', {}), cache.application);
  cache.application = view || cache.application;
  return cache.application;
}

export async function saveApplication(patch: Record<string, unknown>): Promise<ApplicationSettingsView | undefined> {
  const updated = await invoke<ApplicationSettingsView>('mothx/manage/application/patch', { patch });
  cache.application = updated;
  cache.settings = undefined;
  await refreshDraftConfigOptions();
  return updated;
}

// ---- env ----

export async function loadEnv(): Promise<EnvView | undefined> {
  if (!hasFeature('manageEnv')) return undefined;
  const view = await guard('manageEnv', () => invoke<EnvView>('mothx/manage/env/get', {}), cache.env);
  cache.env = view || cache.env;
  return cache.env;
}

export async function saveEnv(patch: EnvPatch): Promise<EnvView> {
  const updated = await invoke<EnvView>('mothx/manage/env/patch', patch);
  cache.env = updated;
  return updated;
}

// ---- channels ----

export async function loadChannels(): Promise<ChannelsConfigView | undefined> {
  if (!hasFeature('manageChannels')) return undefined;
  const view = await guard('manageChannels', () => invoke<ChannelsConfigView>('mothx/manage/channels/get', {}), cache.channels);
  cache.channels = view || cache.channels;
  return cache.channels;
}

export async function saveChannels(patch: ChannelsConfigPatch): Promise<ChannelsConfigView> {
  const updated = await invoke<ChannelsConfigView>('mothx/manage/channels/patch', { patch });
  cache.channels = updated;
  return updated;
}

// ---- serve ----

export async function loadServe(): Promise<ServeConfigView | undefined> {
  if (!hasFeature('manageServeConfig')) return undefined;
  const view = await guard('manageServeConfig', () => invoke<ServeConfigView>('mothx/manage/serve/get', {}), cache.serve);
  cache.serve = view || cache.serve;
  return cache.serve;
}

export async function saveServe(patch: Record<string, unknown>): Promise<ServeConfigView> {
  const updated = await invoke<ServeConfigView>('mothx/manage/serve/patch', { patch });
  cache.serve = updated;
  return updated;
}

// ---- providers & settings ----

export async function loadSettings(): Promise<SettingsView | undefined> {
  const view = await guard('manageSettings', () => invoke<SettingsView>('mothx/manage/settings/get', {}), cache.settings);
  cache.settings = view || cache.settings;
  return cache.settings;
}

export async function loadProviderCatalog(): Promise<ProviderCatalog | undefined> {
  const catalog = await guard('manageProviders', () => invoke<ProviderCatalog>('mothx/manage/providers/list', {}), cache.providerCatalog);
  cache.providerCatalog = catalog || cache.providerCatalog;
  return cache.providerCatalog;
}

export async function patchSettings(patch: Record<string, unknown>): Promise<SettingsView | null> {
  const view = await guard('manageSettings', () => invoke<SettingsView>('mothx/manage/settings/patch', { patch }), null);
  if (view) {
    cache.settings = view;
    cache.providerCatalog = undefined;
    await refreshDraftConfigOptions();
  }
  return view;
}

export async function saveProvider(request: Record<string, unknown>): Promise<void> {
  await invoke<ProviderCatalog>('mothx/manage/providers/save', request);
  cache.settings = undefined;
  cache.providerCatalog = undefined;
  await refreshDraftConfigOptions();
}

export async function deleteProvider(id: string): Promise<void> {
  await invoke<ProviderCatalog>('mothx/manage/providers/delete', { id });
  cache.settings = undefined;
  cache.providerCatalog = undefined;
  await refreshDraftConfigOptions();
}

export interface ProviderTestResult { ok?: boolean; latencyMs?: number; error?: string }

export async function testProvider(name: string, model?: string): Promise<ProviderTestResult | null> {
  return guard('manageProviders', () => invoke<ProviderTestResult>('mothx/manage/providers/test', { provider: name, model }), null);
}

export interface DiscoverConnectionFields {
  api: string;
  baseUrl: string;
  apiKey: string;
  httpProxy: string;
  forceHTTP11: boolean;
}

export async function discoverProviderModels(fields: DiscoverConnectionFields): Promise<ProviderModelView[]> {
  const result = await invoke<{ models?: ProviderModelView[] }>('mothx/manage/providers/discover', {
    api: fields.api, baseUrl: fields.baseUrl.trim(), apiKey: fields.apiKey.trim(),
    httpProxy: fields.httpProxy.trim(), forceHTTP11: fields.forceHTTP11,
  });
  return result.models || [];
}

// ---- knowledge bases ----

export async function loadKnowledgeBases(): Promise<KnowledgeBaseView[] | undefined> {
  if (!hasFeature('manageKnowledgeBases')) return undefined;
  const result = await guard('manageKnowledgeBases', () => invoke<{ knowledgeBases?: KnowledgeBaseView[] }>('mothx/manage/knowledge-bases/list', {}), null);
  if (result) cache.knowledgeBases = result.knowledgeBases || [];
  return cache.knowledgeBases;
}

export interface KnowledgeBaseDefaults {
  spec: KnowledgeBaseSpec;
  providers: string[];
  models: ProviderCatalog['models'];
}

export async function knowledgeBaseDefaults(): Promise<KnowledgeBaseDefaults> {
  const settings = cache.settings || await guard('manageSettings', () => invoke<SettingsView>('mothx/manage/settings/get', {}), {} as SettingsView);
  cache.settings = settings || cache.settings;
  let catalog = cache.providerCatalog;
  if (!catalog && hasFeature('manageProviders')) {
    catalog = await guard('manageProviders', () => invoke<ProviderCatalog>('mothx/manage/providers/list', {}), undefined);
    cache.providerCatalog = catalog || cache.providerCatalog;
  }
  const providerNames = (catalog?.providers || settings?.providers || []).map((provider) => String(provider.name || '').trim()).filter(Boolean);
  const provider = settings?.defaultProvider || catalog?.defaultProvider || providerNames[0] || '';
  const allModels = catalog?.models || [];
  const model = settings?.defaultModel || catalog?.defaultModel || knowledgeBaseModels(allModels, provider)[0] || '';
  return {
    spec: {
      name: '', rootDir: '', preprocessProfile: 'documents', provider, model,
      mode: settings?.defaultMode || 'yolo', thinkingLevel: settings?.thinkingLevel || '',
      schedule: 'manual', enabled: true,
    },
    providers: providerNames,
    models: allModels,
  };
}

export function knowledgeBaseModels(models: ProviderCatalog['models'], provider: string): string[] {
  return (models || [])
    .filter((model) => String(model.provider || '') === provider)
    .map((model) => String(model.id || '').trim())
    .filter(Boolean);
}

export async function createKnowledgeBase(knowledgeBase: KnowledgeBaseSpec): Promise<void> {
  await invoke('mothx/manage/knowledge-bases/create', { knowledgeBase });
}

export async function updateKnowledgeBase(id: string, knowledgeBase: KnowledgeBaseSpec): Promise<void> {
  await invoke('mothx/manage/knowledge-bases/update', { id, knowledgeBase });
}

export async function scanKnowledgeBase(id: string): Promise<void> {
  await invoke('mothx/manage/knowledge-bases/scan', { id });
}

export async function deleteKnowledgeBase(id: string): Promise<void> {
  await invoke('mothx/manage/knowledge-bases/delete', { id });
}

export function knowledgeBaseMcpName(baseId: string): string {
  return `knowledge-${baseId}`;
}

// The shared ACP Runtime owns the canonical command, arguments, and global
// MCP persistence. Desktop only requests the desired knowledge-base state.
export async function applyKnowledgeBaseMcp(baseId: string, enabled: boolean): Promise<void> {
  await invoke('mothx/manage/knowledge-bases/mcp/apply', { id: baseId, enabled });
}

// ---- skills ----

export async function loadSkills(): Promise<SkillView[] | undefined> {
  if (!hasFeature('manageSkills')) return undefined;
  const skills = await guard('manageSkills', () => invoke<{ skills?: SkillView[] }>('mothx/manage/skills/list', {}).then((r) => r.skills || []), cache.skills);
  cache.skills = skills || cache.skills;
  return cache.skills;
}

export async function setSkillEnabled(name: string, enabled: boolean): Promise<void> {
  await invoke('mothx/manage/skills/set', { name, enabled });
}

// ---- skillhub ----

export async function loadSkillHub(): Promise<SkillHubView | undefined> {
  if (!hasFeature('manageSkillHub')) return undefined;
  const view = await guard('manageSkillHub', () => invoke<SkillHubView>('mothx/manage/skillhub/get', {}), cache.skillHub);
  cache.skillHub = view || cache.skillHub;
  return cache.skillHub;
}

export interface SkillHubPatch {
  defaultMarket?: string;
  defaultInstallScope?: string;
  officialHandles?: string[];
  markets?: {
    id: string;
    name: string;
    siteURL: string;
    apiURL: string;
    enabled: boolean;
    apiToken?: string;
    clearApiToken?: boolean;
  }[];
}

export async function saveSkillHub(patch: SkillHubPatch): Promise<SkillHubView> {
  const updated = await invoke<SkillHubView>('mothx/manage/skillhub/patch', { patch });
  cache.skillHub = updated;
  return updated;
}

// ---- mcp ----

export type McpScope = 'global' | 'project';

export interface McpListResult {
  servers?: McpServerView[];
}

export async function loadMcp(scope: McpScope = 'global', sessionId?: string): Promise<McpServerView[] | undefined> {
  if (!hasFeature('manageMcp')) return undefined;
  const params: Record<string, unknown> = { scope };
  if (sessionId) params.sessionId = sessionId;
  const result = await guard('manageMcp', () => invoke<McpListResult>('mothx/manage/mcp/list', params), null);
  return result?.servers || [];
}

export async function setMcpServers(servers: McpServerView[], scope: McpScope = 'global', sessionId?: string): Promise<void> {
  const params: Record<string, unknown> = { scope, servers };
  if (sessionId) params.sessionId = sessionId;
  await invoke('mothx/manage/mcp/set', params);
}

// ---- memory ----

export async function loadMemory(): Promise<string | undefined> {
  if (!hasFeature('manageMemory')) return undefined;
  const result = await guard('manageMemory', () => invoke<{ content?: string }>('mothx/manage/memory/get', {}), null);
  if (result) cache.memory = result.content || '';
  return cache.memory;
}

export async function saveMemory(content: string): Promise<number> {
  const result = await invoke<{ size?: number }>('mothx/manage/memory/put', { content });
  cache.memory = content;
  return result?.size ?? content.length;
}

// ---- cron ----

export async function loadCronJobs(): Promise<CronJobView[] | undefined> {
  if (!hasFeature('manageCron')) return undefined;
  const result = await guard('manageCron', () => invoke<{ jobs?: CronJobView[] }>('mothx/manage/cron/list', {}), null);
  cache.cron = result?.jobs || cache.cron;
  return cache.cron;
}

export async function createCronJob(job: { name: string; schedule: string; prompt: string; mode: string; enabled: boolean }): Promise<void> {
  await invoke('mothx/manage/cron/create', job);
}

export async function updateCronJob(job: { id: string; name?: string; schedule?: string; prompt?: string; mode?: string; enabled?: boolean }): Promise<void> {
  await invoke('mothx/manage/cron/update', job);
}

export async function runCronJob(id: string): Promise<void> {
  await invoke('mothx/manage/cron/run', { id });
}

export async function removeCronJob(id: string): Promise<void> {
  await invoke('mothx/manage/cron/remove', { id });
}

// ---- stats ----

export async function loadStats(): Promise<{ summary: StatsSummary; points: StatsPoint[] } | undefined> {
  if (!hasFeature('manageStats')) return undefined;
  const summary = await guard('manageStats', () => invoke<StatsSummary>('mothx/manage/stats/summary', {}), cache.stats);
  cache.stats = summary || cache.stats;
  const series = await guard('manageStats', () => invoke<{ points?: StatsPoint[] }>('mothx/manage/stats/timeseries', { group: 'day', days: 14 }).catch(() => ({ points: [] })), { points: [] });
  cache.points = series?.points || [];
  return { summary: cache.stats || {}, points: cache.points };
}

// ---- experts ----

export type ExpertScope = 'global' | 'project';

export function expertWorkspace(): string {
  return state.activeSessionCwd || state.newSessionCwd || '';
}

export async function loadExperts(scope: ExpertScope): Promise<ExpertListView | undefined> {
  if (!hasFeature('manageExperts')) return undefined;
  const cwd = scope === 'project' ? expertWorkspace() : undefined;
  const params: Record<string, unknown> = { scope };
  if (cwd) params.cwd = cwd;
  const view = await guard('manageExperts', () => invoke<ExpertListView>('mothx/manage/experts/list', params), cache.experts);
  cache.experts = view || cache.experts;
  return cache.experts;
}

export async function getExpertBundle(scope: ExpertScope, name: string): Promise<ExpertBundle | undefined> {
  const params: Record<string, unknown> = { scope, name };
  const cwd = scope === 'project' ? expertWorkspace() : undefined;
  if (cwd) params.cwd = cwd;
  const result = await invoke<ExpertListView>('mothx/manage/experts/get', params);
  return result?.bundle;
}

export async function saveExpertBundle(isNew: boolean, bundle: ExpertBundle): Promise<void> {
  const scope = bundle.scope as ExpertScope;
  const params: Record<string, unknown> = { scope };
  const cwd = scope === 'project' ? expertWorkspace() : undefined;
  if (cwd) params.cwd = cwd;
  if (isNew) {
    await invoke<ExpertBundle>('mothx/manage/experts/create', { ...params, bundle });
  } else {
    await invoke<ExpertBundle>('mothx/manage/experts/update', { ...params, bundle });
  }
  cache.experts = undefined;
}

export async function deleteExpert(scope: ExpertScope, name: string): Promise<void> {
  const params: Record<string, unknown> = { scope, name };
  const cwd = scope === 'project' ? expertWorkspace() : undefined;
  if (cwd) params.cwd = cwd;
  await invoke('mothx/manage/experts/delete', params);
  cache.experts = undefined;
}

// ---- doctor ----

export async function runDoctor(): Promise<DoctorResult> {
  return invoke<DoctorResult>('mothx/doctor', { cwd: state.newSessionCwd || undefined });
}

// ---- 共享小工具 ----

export function applicationLines(value: string): string[] {
  return value.split(/\r?\n/).map((entry) => entry.trim()).filter(Boolean);
}

export function integerOr(value: string, min: number): number {
  const parsed = Number(value);
  return Number.isInteger(parsed) && parsed >= min ? parsed : min;
}

export function floatOr(value: string, min: number, max: number): number {
  const parsed = Number(value);
  if (Number.isFinite(parsed) && parsed >= min && parsed <= max) return parsed;
  return min;
}

export function optionalNumber(value: string): number | undefined {
  if (!value.trim()) return undefined;
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : undefined;
}

export function optionalImageLimit(value: string): number | undefined {
  if (!value.trim()) return undefined;
  const parsed = Number(value);
  return Number.isInteger(parsed) && parsed >= -1 ? parsed : undefined;
}

export function providerModelCopies(provider: ProviderConfigView): ProviderModelView[] {
  return (provider.provider.models || []).map((model) => ({ ...model, input: [...(model.input || [])] }));
}

export function buildProviderDraft(provider: ProviderConfigView): ProviderConfigView {
  return {
    id: provider.id,
    provider: {
      vendor: provider.provider.vendor,
      baseUrl: provider.provider.baseUrl,
      httpProxy: provider.provider.httpProxy,
      forceHTTP11: provider.provider.forceHTTP11,
      api: provider.provider.api,
      thinkingFormat: provider.provider.thinkingFormat,
      cacheControl: provider.provider.cacheControl,
      maxImagesPerRequest: provider.provider.maxImagesPerRequest,
      models: providerModelCopies(provider).map((model) => ({ ...model, input: [...(model.input || [])] })),
    },
    maskedKey: provider.maskedKey,
    apiKeyConfigured: provider.apiKeyConfigured,
    isDefault: provider.isDefault,
    globalOverride: provider.globalOverride,
  };
}

export function formatCronTime(value: string): string {
  try {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return value;
    return date.toLocaleString();
  } catch {
    return value;
  }
}

export function chooseDirectoryFor(defaultPath?: string): Promise<string | null> {
  return desktop.chooseDirectory(defaultPath);
}
