// Composer 领域逻辑:附件、prompt content block 组装、发送与取消、下一任务
// 草稿配置(provider/model/mode/expert)。输入统一映射为 ACP content block
// (text / resource_link / resource),走同一条 session/prompt 通道(home 与
// chat 视图共用)。弹出菜单本身是 React 组件,只消费这里的状态与动作。

import { acp, desktop, invoke } from './api';
import { requestChatScroll, requestComposerInjection } from './bus';
import { t } from './i18n';
import { chooseWorkingDirectory, createSession, forkSession, refreshSessions } from './sessions';
import {
  activeSessionWorkspace,
  currentConfigOptions,
  emit,
  hasFeature,
  isReady,
  isSessionRunning,
  newSessionWorkspace,
  sessionOwnsTranscript,
  sessionRunStatus,
  setSessionRunStatus,
  state,
  type AttachmentDraft,
  type SessionConfigOptionShape,
} from './state';
import { terminalizePendingDecisions } from './transcript';
import { toast } from './ui-host';
import { switchView } from './views';

export interface ContentBlock {
  type: string;
  text?: string;
  name?: string;
  uri?: string;
  mimeType?: string;
  data?: string;
  size?: number;
}

const MIME_BY_EXT: Record<string, string> = {
  png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', gif: 'image/gif', webp: 'image/webp', svg: 'image/svg+xml',
  md: 'text/markdown', txt: 'text/plain', log: 'text/plain', json: 'application/json', pdf: 'application/pdf',
  html: 'text/html', css: 'text/css', js: 'text/javascript', ts: 'text/typescript', csv: 'text/csv',
};

function guessMime(path: string): string | undefined {
  const ext = path.split('.').pop()?.toLowerCase() || '';
  return MIME_BY_EXT[ext];
}

function basename(path: string): string {
  const parts = path.split(/[\\/]/);
  return parts[parts.length - 1] || path;
}

function fileUri(path: string): string {
  return `file://${encodeURI(path).replace(/#/g, '%23').replace(/\?/g, '%3F')}`;
}

function insideWorkspace(path: string): boolean {
  // resource_link 是会话输入,边界应是该会话(或待创建会话)的工作目录,
  // 不是 ACP 进程已协商的所有可访问目录。工作区外的文件走内嵌 resource。
  const roots = [activeSessionWorkspace() || newSessionWorkspace()];
  return roots.some((root) => !!root && path.startsWith(root.endsWith('/') ? root : `${root}/`));
}

export async function attachFiles(): Promise<void> {
  const files = await desktop.chooseFiles().catch(() => [] as { path: string; grant: string }[]);
  if (files.length === 0) return;
  for (const { path, grant } of files) {
    const name = basename(path);
    const mimeType = guessMime(path);
    const windows = desktop.platform() === 'win32';
    // resource_link 要求文件位于协商的工作区内;工作区外(以及 Windows 的
    // 盘符路径,file URI 解析存在平台歧义)统一内嵌为 base64 resource。
    if (!windows && insideWorkspace(path)) {
      state.attachments.push({ path, name, mimeType });
      continue;
    }
    const result = await desktop.readFileBase64(grant);
    if (!result.ok) {
      toast(result.error.includes('exceeds') ? t('attach.tooLarge') : t('attach.failed', { e: result.error }));
      continue;
    }
    state.attachments.push({ path, name, mimeType, size: result.size, embedded: true, data: result.data });
  }
  emit();
}

// 剪贴板粘贴图片:内嵌为 base64 resource(没有磁盘路径可引用)。
export function attachPastedFiles(files: File[]): void {
  const images = files.filter((file) => file.type.startsWith('image/'));
  if (images.length === 0) return;
  for (const image of images) {
    const reader = new FileReader();
    reader.onload = () => {
      const dataUrl = String(reader.result || '');
      const comma = dataUrl.indexOf(',');
      if (comma < 0) return;
      state.attachments.push({
        path: `clipboard:${Date.now()}:${image.name || 'image'}`,
        name: image.name || t('attach.pasteName', { n: state.attachments.length + 1 }),
        mimeType: image.type,
        size: image.size,
        embedded: true,
        data: dataUrl.slice(comma + 1),
      });
      emit();
    };
    reader.readAsDataURL(image);
  }
}

export function removeAttachment(path: string): void {
  state.attachments = state.attachments.filter((entry) => entry.path !== path);
  emit();
}

export function buildPromptBlocks(text: string): ContentBlock[] {
  const blocks: ContentBlock[] = [];
  if (text) blocks.push({ type: 'text', text });
  for (const attachment of state.attachments) {
    if (attachment.embedded && attachment.data) {
      blocks.push({ type: 'resource', name: attachment.name, mimeType: attachment.mimeType, data: attachment.data });
    } else {
      blocks.push({ type: 'resource_link', name: attachment.name, uri: fileUri(attachment.path), mimeType: attachment.mimeType });
    }
  }
  return blocks;
}

// 会话创建前用户在 home 选择的 model/provider/mode 暂存,session/new 后应用。
const pendingConfig: { provider?: string; model?: string; mode?: string; expert?: string; thinking_level?: string } = {};

interface ProviderCatalogResult {
  providers?: { name?: string; isDefault?: boolean; apiKeyConfigured?: boolean }[];
  models?: { id?: string; name?: string; provider?: string; reasoning?: boolean; input?: string[] }[];
  defaultProvider?: string;
  defaultModel?: string;
}

interface DraftConfigOptionsResult {
  configOptions?: SessionConfigOptionShape[];
}

export interface ModelChoice {
  value: string;
  name: string;
  description: string;
  input: string[];
  reasoning: boolean;
}

// Ephemeral menu cache of the ACP response. It is not a provider catalog: it
// only lets the selected provider immediately swap to its already-projected
// model choices without inventing local resolution rules.
let draftModelsByProvider: Record<string, ModelChoice[]> = {};
let catalogCapabilities = new Map<string, ModelChoice>();
// ACP 的 providers/list 投影给出的供应商事实:哪些已配置密钥,以及权威顺序
// (默认供应商 → 最近使用 → 未使用的目录优先级)。任务输入区只按它渲染菜单,
// 不自己判断可用性或重排优先级。
let configuredProviders = new Set<string>();
let providerOrder: string[] = [];

// providerIsConfigured reports whether the ACP provider projection marked a
// usable API key. Providers the projection does not know about stay visible so
// an older runtime never loses its picker.
export function providerIsConfigured(name: string): boolean {
  if (!configuredProviders.size) return true;
  return configuredProviders.has(name);
}

// providerRank is the ACP-projected picker position of a provider. Unknown
// providers keep their projected order at the end of the menu.
function providerRank(name: string): number {
  const index = providerOrder.indexOf(name);
  return index >= 0 ? index : providerOrder.length;
}

export function draftModelChoices(provider: string): ModelChoice[] {
  return draftModelsByProvider[provider] || [];
}

export function modelCapability(provider: string, modelId: string): ModelChoice | undefined {
  return catalogCapabilities.get(`${provider}:${modelId}`);
}

// visibleConfigOptions narrows and orders only the provider menu of an ACP
// config option catalog, using the projected provider order so a session picker
// shows the same providers first as the next-task picker. Option semantics
// (values, current value, mode, expert, model list, …) stay as projected.
export function visibleConfigOptions(options: SessionConfigOptionShape[]): SessionConfigOptionShape[] {
  return options.map((option) => {
    if (option.id !== 'provider' || !option.options || option.options.length === 0) return option;
    const usable = option.options.filter((choice) => providerIsConfigured(choice.value));
    // Never hide every provider: a runtime that projects no usable key at all
    // must still leave a selectable menu.
    if (usable.length === 0) return option;
    const ordered = providerOrder.length
      ? [...usable].sort((left, right) => providerRank(left.value) - providerRank(right.value))
      : usable;
    if (ordered.length === option.options.length && ordered.every((choice, index) => choice === option.options?.[index])) return option;
    return { ...option, options: ordered };
  });
}

// Populate the next-task model picker from ACP's provider factory projection.
// The desktop only reshapes the protocol response for a menu; provider/model
// resolution and validation remain in the shared Runtime when the session is
// created and the pending values are applied.
export async function refreshDraftConfigOptions(): Promise<void> {
  if (!isReady() || !hasFeature('manageProviders')) return;
  try {
    const result = await invoke<ProviderCatalogResult>('mothx/manage/providers/list', {});
    const projected = result.providers || [];
    configuredProviders = new Set(projected.filter((provider) => provider.apiKeyConfigured !== false).map((provider) => String(provider.name || '')));
    // 顺序完全采用 Runtime 的权威投影(默认供应商 → 最近使用 → 未使用)。
    providerOrder = projected.map((provider) => String(provider.name || '')).filter(Boolean);
    const defaultProvider = String(result.defaultProvider || '');
    const providerChoices = projected
      .map((provider) => ({ value: String(provider.name || ''), name: String(provider.name || '') }))
      .filter((provider) => provider.value !== '' && providerIsConfigured(provider.value));
    const existingProvider = state.draftConfigOptions.find((option) => option.id === 'provider')?.currentValue;
    const preferredProvider = pendingConfig.provider || existingProvider || defaultProvider;
    const provider = providerChoices.some((choice) => choice.value === preferredProvider) ? preferredProvider : providerChoices[0]?.value || '';
    draftModelsByProvider = {};
    catalogCapabilities = new Map();
    for (const model of result.models || []) {
      if (!model.provider || !model.id) continue;
      const choice: ModelChoice = {
        value: String(model.id),
        name: String(model.name || model.id),
        description: model.reasoning ? t('composer.reasoning') : '',
        input: Array.isArray(model.input) ? model.input.slice() : [],
        reasoning: Boolean(model.reasoning),
      };
      const choices = draftModelsByProvider[model.provider] || [];
      choices.push(choice);
      draftModelsByProvider[model.provider] = choices;
      catalogCapabilities.set(`${model.provider}:${model.id}`, choice);
    }
    const modelChoices = draftModelsByProvider[provider] || [];
    const existingModel = state.draftConfigOptions.find((option) => option.id === 'model')?.currentValue;
    const preferredModel = pendingConfig.model || existingModel || (provider === result.defaultProvider ? result.defaultModel || '' : '');
    const model: string = modelChoices.some((choice) => choice.value === preferredModel) ? preferredModel : modelChoices[0]?.value || '';
    state.draftConfigOptions = [
      { type: 'select', id: 'provider', name: 'Provider', currentValue: provider, options: providerChoices },
      { type: 'select', id: 'model', name: 'Model', currentValue: model, options: modelChoices },
    ];
    const draft = await invoke<DraftConfigOptionsResult>('mothx/session/draft-config-options', { cwd: newSessionWorkspace() });
    for (const option of draft.configOptions || []) {
      if (option.id !== 'provider' && option.id !== 'model') state.draftConfigOptions.push(option);
    }
    if (provider) pendingConfig.provider = provider;
    if (model) pendingConfig.model = model;
    emit();
  } catch (error) {
    desktop.log(`providers/list for next task failed: ${error instanceof Error ? error.message : String(error)}`);
  }
}

function setDraftConfigOption(configId: string, value: string): void {
  if (configId === 'provider') {
    pendingConfig.provider = value;
    const provider = state.draftConfigOptions.find((option) => option.id === 'provider');
    if (provider) provider.currentValue = value;
    const model = state.draftConfigOptions.find((option) => option.id === 'model');
    const choices = draftModelsByProvider[value] || [];
    if (model) {
      model.options = choices.map((choice) => ({ value: choice.value, name: choice.name, description: choice.description }));
    }
    const currentModel = model?.currentValue;
    const preferredModel = pendingConfig.model || currentModel;
    const nextModel = choices.some((choice) => choice.value === preferredModel) ? preferredModel || '' : choices[0]?.value || '';
    if (model) model.currentValue = nextModel;
    pendingConfig.model = nextModel || undefined;
    return;
  }
  if (configId === 'model') {
    pendingConfig.model = value;
  }
  const option = state.draftConfigOptions.find((entry) => entry.id === configId);
  if (option) option.currentValue = value;
}

export async function applyConfigOption(configId: string, value: string): Promise<void> {
  if (configId === 'provider') pendingConfig.provider = value;
  if (configId === 'model') pendingConfig.model = value;
  if (configId === 'mode') pendingConfig.mode = value;
  if (!state.activeSessionId) {
    if (configId === 'expert' || configId === 'thinking_level') pendingConfig[configId] = value;
    setDraftConfigOption(configId, value);
    if (configId === 'mode') state.currentMode = value;
    emit();
    return;
  }
  try {
    const result = await invoke<{ configOptions?: typeof state.configOptions }>('session/set_config_option', {
      sessionId: state.activeSessionId,
      configId,
      value,
    });
    if (Array.isArray(result?.configOptions) && result.configOptions.length > 0) {
      state.configOptions = result.configOptions;
    }
    if (configId === 'mode') state.currentMode = value;
    // When the provider changes on an active session, the Runtime may already
    // have selected a default model. If the current model is not in the new
    // provider's projected options, fall back to the first available model so
    // the Model selector stays in sync without inventing local defaults.
    if (configId === 'provider') {
      const modelOption = state.configOptions.find((option) => option.id === 'model');
      const currentModel = modelOption?.currentValue;
      const hasValidModel = modelOption?.options?.some((choice) => choice.value === currentModel);
      if (!hasValidModel && modelOption?.options && modelOption.options.length > 0) {
        const nextModel = modelOption.options[0].value;
        const modelResult = await invoke<{ configOptions?: typeof state.configOptions }>('session/set_config_option', {
          sessionId: state.activeSessionId,
          configId: 'model',
          value: nextModel,
        });
        if (Array.isArray(modelResult?.configOptions) && modelResult.configOptions.length > 0) {
          state.configOptions = modelResult.configOptions;
        }
      }
    }
  } catch (error) {
    toast(`${configId}: ${error instanceof Error ? error.message : String(error)}`);
  }
  emit();
}

async function flushPendingConfig(sessionId: string): Promise<void> {
  for (const [configId, value] of Object.entries(pendingConfig)) {
    if (!value) continue;
    try {
      const result = await invoke<{ configOptions?: SessionConfigOptionShape[] }>('session/set_config_option', { sessionId, configId, value });
      if (Array.isArray(result?.configOptions) && result.configOptions.length > 0) {
        state.configOptions = result.configOptions;
      }
    } catch (error) {
      desktop.log(`set_config_option ${configId}=${value} failed: ${error instanceof Error ? error.message : String(error)}`);
    }
  }
  pendingConfig.provider = undefined;
  pendingConfig.model = undefined;
  pendingConfig.mode = undefined;
  pendingConfig.expert = undefined;
  pendingConfig.thinking_level = undefined;
}

// sendPrompt 返回 true 表示请求已发出(调用方应清空输入框)。
// 整个“创建会话 + 发送”是单飞临界区:回车连按(或按键重复)不能并发创建两个
// 会话、也不能把同一条消息发两次。运行期由每个会话自己的 Run 状态投影负责,
// 因此后台任务运行时仍然可以新建并发送另一个任务。
let sending = false;

export async function sendPrompt(text: string, source: 'home' | 'chat'): Promise<boolean> {
  const trimmed = text.trim();
  if (!trimmed && state.attachments.length === 0) {
    toast(t('prompt.empty'));
    return false;
  }
  if (!isReady()) {
    toast(t('prompt.notConnected'));
    return false;
  }
  if (sending || isSessionRunning(state.activeSessionId)) {
    toast(t('prompt.busy'));
    return false;
  }
  sending = true;
  try {
    return await dispatchPrompt(trimmed, source);
  } finally {
    sending = false;
  }
}

async function dispatchPrompt(trimmed: string, source: 'home' | 'chat'): Promise<boolean> {
  let sessionId = state.activeSessionId;
  if (!sessionId) {
    // 新建对话先确认工作目录(用户取消则放弃发送)。
    if (!state.dirConfirmed) {
      const picked = await chooseWorkingDirectory();
      if (!picked) return false;
    }
    try {
      const created = await createSession();
      sessionId = created.sessionId;
      state.activeSessionCwd = created.cwd;
    } catch (error) {
      toast(t('prompt.sessionFailed', { e: error instanceof Error ? error.message : String(error) }));
      return false;
    }
    state.activeSessionId = sessionId;
    state.activeTitle = trimmed.length > 40 ? `${trimmed.slice(0, 40)}…` : trimmed || t('chat.newTask');
    state.transcriptSessionId = sessionId;
    await flushPendingConfig(sessionId);
    await refreshSidebarAfterCreate(sessionId);
  }
  const blocks = buildPromptBlocks(trimmed);
  const attachmentNote = state.attachments.map((entry) => entry.name).join(', ');
  state.attachments = [];
  if (source === 'home') switchView('chat');

  // 乐观用户气泡:server 回显 user_message_chunk 后按 messageId 归并。
  const localKey = `user-local:${Date.now()}`;
  state.pendingUserKey = localKey;
  state.transcript.push({ kind: 'user', key: localKey, text: trimmed || attachmentNote });
  state.currentPlanKey = null;
  setSessionRunStatus(sessionId, state.currentMode === 'plan' ? 'planning' : 'working');
  emit();
  requestChatScroll(true);

  const cwd = activeSessionWorkspace();
  if (!cwd) {
    const message = t('prompt.sessionWorkspaceUnavailable');
    setSessionRunStatus(sessionId, 'failed');
    state.transcript.push({ kind: 'error', key: `error-prompt:${Date.now()}`, message });
    terminalizePendingDecisions();
    emit();
    return true;
  }
  const promptSessionId = sessionId;
  invoke<{ stopReason?: string }>('session/prompt', {
    sessionId: promptSessionId,
    prompt: blocks,
    _meta: { mothx: { workspace: { cwd } } },
  })
    .then((result) => {
      const reason = String(result?.stopReason || 'end_turn');
      const current = sessionRunStatus(promptSessionId);
      if (current === 'failed' || current === 'cancelled') return;
      setSessionRunStatus(promptSessionId, reason === 'cancelled' || reason === 'aborted' ? 'cancelled' : 'completed');
    })
    .catch((error: unknown) => {
      const message = error instanceof Error ? error.message : String(error);
      setSessionRunStatus(promptSessionId, 'failed');
      if (sessionOwnsTranscript(promptSessionId)) {
        state.transcript.push({ kind: 'error', key: `error-prompt:${Date.now()}`, message });
      }
      terminalizePendingDecisions();
    })
    .finally(() => {
      if (sessionOwnsTranscript(promptSessionId)) state.currentPlanKey = null;
      emit();
      requestChatScroll();
    });
  return true;
}

async function refreshSidebarAfterCreate(sessionId: string): Promise<void> {
  // session/new 已持久化会话;刷新列表让侧边栏立即出现新任务。
  await refreshSessions();
  const session = state.sessions.find((entry) => entry.sessionId === sessionId);
  if (session && !session.title) session.title = state.activeTitle;
  emit();
}

export function cancelRun(): void {
  // 只取消当前任务自己的 Run;后台任务继续运行。
  const target = state.activeSessionId;
  if (!target || !isSessionRunning(target)) return;
  acp.notify('session/cancel', { sessionId: target });
  setSessionRunStatus(target, 'cancelled');
  terminalizePendingDecisions();
  emit();
}

// chooseSessionExpert preserves Runtime-owned identity semantics. An initial
// bind and an unbind mutate the current idle session; replacing one non-empty
// expert requires the existing fork path.
export function chooseSessionExpert(expertOption: SessionConfigOptionShape, value: string): void {
  const sessionID = state.activeSessionId;
  if (value === expertOption.currentValue) return;
  if (!sessionID) {
    void applyConfigOption('expert', value);
    return;
  }
  if (expertOption.currentValue && value) {
    void forkSession(sessionID, value);
    return;
  }
  void applyConfigOption('expert', value);
}

export function modalityLabel(kind: string): string {
  const key = `composer.capability.${kind}`;
  const translated = t(key);
  return translated === key ? kind : translated;
}

export function injectToComposer(text: string): void {
  requestComposerInjection(text);
}

export type { AttachmentDraft };
