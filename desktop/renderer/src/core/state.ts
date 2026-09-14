// 渲染进程中央状态。会话/项目/运行的权威数据来自 ACP；本地 store 只
// 保存主题、语言和新会话默认目录等 UI 偏好。任务树的分页与展开状态只
// 存在于当前渲染进程，绝不成为另一套 session/project 持久化。

import type { ConnectionState, StoreData } from './api';
import { desktop } from './api';
import { getLocale } from './i18n';

export type RunStatus = 'idle' | 'loading' | 'planning' | 'working' | 'pending' | 'completed' | 'failed' | 'cancelled';

export interface ContentBlockShape {
  type: string;
  text?: string;
  mimeType?: string;
  data?: string;
  name?: string;
  uri?: string;
  size?: number;
}

export interface ToolCallContentShape {
  type: string;
  content?: ContentBlockShape;
  path?: string;
  oldText?: string | null;
  newText?: string;
}

export interface PlanEntryShape {
  content: string;
  priority: string;
  status: string;
}

export interface AvailableCommandShape {
  name: string;
  description?: string;
  _meta?: Record<string, unknown>;
}

export interface SessionConfigOptionShape {
  type: string;
  id: string;
  name: string;
  category?: string;
  currentValue: string;
  options?: { value: string; name: string; description?: string }[];
}

export interface ListedSessionShape {
  sessionId: string;
  cwd: string;
  title?: string;
  provider: string;
  model: string;
  mode?: string;
  thoughtLevel?: string;
  updatedAt?: string;
  _meta?: Record<string, unknown> & {
    pinned?: boolean;
    projectId?: string | null;
    lastRun?: { runId?: string; status?: string; startedAt?: string; finishedAt?: string; active?: boolean };
  };
}

export interface ProjectShape {
  id: string;
  name: string;
  createdAt?: string;
  updatedAt?: string;
  sessionCount?: number;
}

export type SessionListScope = 'all' | 'project' | 'ungrouped';

export interface SessionPageState {
  sessions: ListedSessionShape[];
  nextCursor: string;
  loading: boolean;
  loaded: boolean;
}

export interface NewSessionResultShape {
  sessionId: string;
  parentSessionId?: string;
  modes?: { currentModeId?: string; availableModes?: { id: string; name: string }[] };
  configOptions?: SessionConfigOptionShape[];
	  history?: TranscriptPageShape;
}

export interface TranscriptPageShape {
  sessionId: string;
  updates: Record<string, unknown>[];
  nextCursor?: string;
}

// usage_update 的 mothx.dev 附加投影:会话累计提示词缓存量。命中率口径为
// cacheRead / totalInputTokens,与服务端 Usage.TotalInputTokens 同分母;仅在
// initialize 声明 usageCacheProjection 且已有 usage 时出现。
export type UsageCacheProjection = {
  cacheRead: number;
  cacheWrite: number;
  totalInputTokens: number;
};

export type TranscriptItem =
  | { kind: 'user'; key: string; text: string }
  | { kind: 'agent'; key: string; text: string }
  | { kind: 'thought'; key: string; text: string; open: boolean }
  | {
      kind: 'tool';
      key: string;
      toolCallId: string;
      title: string;
      toolKind: string;
      status: string;
      // Local disclosure state only. Streaming ACP updates must not overwrite
      // the user's choice to inspect (or hide) a tool execution.
      open: boolean;
      rawInput?: Record<string, unknown>;
      contents: ToolCallContentShape[];
      locations?: { path: string }[];
    }
  | { kind: 'plan'; key: string; entries: PlanEntryShape[]; title?: string; note?: string }
  | {
      kind: 'artifact';
      key: string;
      artifactId: string;
      filename: string;
      artifactKind: string;
      mediaType?: string;
      size?: number;
      fromToolCall?: boolean;
    }
  | { kind: 'permission'; key: string; requestId: string; sessionId: string; title: string; toolKind: string; rawInput?: Record<string, unknown>; options: { optionId: string; name: string; kind: string }[]; resolved?: string; deadline?: number }
  | { kind: 'question'; key: string; requestId: string; sessionId: string; prompt: string; explanation?: string; options: { id: string; label: string }[]; resolved?: string; deadline?: number }
  | { kind: 'status'; key: string; text: string; spin: boolean }
  | { kind: 'subagent'; key: string; agentId: string; status: string; title: string; role?: string; expertId?: string }
  | { kind: 'error'; key: string; message: string; code?: string; retryable?: boolean };

export interface AttachmentDraft {
  path: string;
  name: string;
  size?: number;
  mimeType?: string;
  embedded?: boolean;
  data?: string;
}

export interface AppState {
  connection: ConnectionState;
  store: StoreData;
  appInfo: { version: string; platform: string; arch: string; runtimeBinary: string };
  view: string;
  preset: string;
  sessions: ListedSessionShape[];
  sessionsLoading: boolean;
  projects: ProjectShape[];
  // Per-scope session pages, keyed by an in-memory UI key. These are cache
  // projections of mothx/session/listAll, never persisted Desktop state.
  sessionPages: Record<string, SessionPageState>;
  expandedProjectIds: string[];
  historyQuery: string;
  historyScope: SessionListScope;
  historyProjectId: string;
  // 仅供下一次 session/new 使用的默认目录。切换它不能改变已存在会话。
  // connection.workspace 只是 ACP 子进程的启动目录，不能作为目录权限边界。
  newSessionCwd: string;
  // 当前打开会话创建/加载时确定的工作目录。所有该会话的请求都以它为准。
  activeSessionCwd: string;
  dirConfirmed: boolean;
  activeSessionId: string | null;
  activeTitle: string;
  runStatus: RunStatus;
  transcript: TranscriptItem[];
  transcriptSessionId: string | null;
	// Ephemeral ACP transcript paging projection. This never becomes Desktop
	// persistence; the Runtime session remains the canonical history owner.
  transcriptNextCursor: string;
  transcriptLoading: boolean;
  // Provider/model choices for a task that has not created a session yet.
  // They are a UI projection of mothx/manage/providers/list, never a second
  // provider catalog; sessionConfigOptions take over once a session is open.
  draftConfigOptions: SessionConfigOptionShape[];
  configOptions: SessionConfigOptionShape[];
  currentMode: string;
  availableCommands: AvailableCommandShape[];
  usage: { used: number; size: number; cost?: number; cache?: UsageCacheProjection } | null;
  attachments: AttachmentDraft[];
  pendingUserKey: string | null;
  promptInFlight: boolean;
  runningSessionId: string | null;
  currentPlanKey: string | null;
  artifactRunCount: number;
}

export const state: AppState = {
  connection: { state: 'idle', workspace: '' },
  store: { theme: 'light', locale: 'zh', homeBackgroundImage: '', homeBackgroundOpacity: 32, homeBackgroundBlur: 0, homeBackgroundScope: 'app', homeBackgroundFit: 'cover', homeBackgroundPosition: 'center', homeLogoVisible: true, homeLogoImage: '', lastWorkspace: '', recentWorkspaces: [], pinnedSessions: [], sessionStatus: {} },
  appInfo: { version: 'dev', platform: 'linux', arch: '', runtimeBinary: '' },
  view: 'home',
  preset: 'coding',
  sessions: [],
  sessionsLoading: false,
  projects: [],
  sessionPages: {},
  expandedProjectIds: [],
  historyQuery: '',
  historyScope: 'all',
  historyProjectId: '',
  newSessionCwd: '',
  activeSessionCwd: '',
  dirConfirmed: false,
  activeSessionId: null,
  activeTitle: '',
  runStatus: 'idle',
  transcript: [],
  transcriptSessionId: null,
	  transcriptNextCursor: '',
	  transcriptLoading: false,
  draftConfigOptions: [],
  configOptions: [],
  currentMode: 'yolo',
  availableCommands: [],
  usage: null,
  attachments: [],
  pendingUserKey: null,
  promptInFlight: false,
  runningSessionId: null,
  currentPlanKey: null,
  artifactRunCount: 0,
};

type Listener = () => void;
const listeners = new Set<Listener>();

// React 桥接:emit 递增版本号,useSyncExternalStore 以版本为 snapshot,
// 保证就地变更 state 后所有订阅组件统一重渲染(等价于旧的 renderAll)。
let version = 0;

export function subscribe(listener: Listener): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function getStateVersion(): number {
  return version;
}

export function emit(): void {
  version += 1;
  for (const listener of listeners) listener();
}

export function workspace(): string {
  return state.activeSessionCwd || newSessionWorkspace();
}

// 会话工作目录绝不能回退到 ACP 的进程级 workspace。空值代表还没有一个
// 已绑定的会话，调用方此时应先创建或加载 session。
export function activeSessionWorkspace(): string {
  return state.activeSessionCwd;
}

// New sessions use only the Desktop-selected default. The ACP child process
// startup cwd is never a session default, workspace, or access boundary.
export function newSessionWorkspace(): string {
  return state.newSessionCwd || state.store.lastWorkspace || '';
}

export function isReady(): boolean {
  return state.connection.state === 'ready';
}

// 能力发现：initialize._meta.mothx.dev.features（Phase 1 起每组新方法一个发现键）。
export function hasFeature(key: string): boolean {
  const caps = state.connection.agentCapabilities as { _meta?: Record<string, { features?: string[] }> } | undefined;
  const dev = caps?._meta?.['mothx.dev'] || caps?._meta?.mothx;
  return Array.isArray(dev?.features) && dev.features.includes(key);
}

export function currentModelLabel(): string {
  const model = currentConfigOptions().find((option) => option.id === 'model');
  if (!model) return getLocale() === 'zh' ? '模型（新建任务后可选）' : 'Model (after first task)';
  const choice = model.options?.find((entry) => entry.value === model.currentValue);
  return choice?.name || model.currentValue;
}

export function currentProviderLabel(): string {
  const provider = currentConfigOptions().find((option) => option.id === 'provider');
  if (!provider) return '';
  const choice = provider.options?.find((entry) => entry.value === provider.currentValue);
  return choice?.name || provider.currentValue;
}

// A session owns its effective runtime configuration. Before session/new, the
// desktop renders only the ACP-projected draft selection for the next task.
export function currentConfigOptions(): SessionConfigOptionShape[] {
  return state.activeSessionId ? state.configOptions : state.draftConfigOptions;
}

export function sessionTitle(sessionId: string): string {
  const session = state.sessions.find((entry) => entry.sessionId === sessionId);
  return session?.title || sessionId.slice(0, 8);
}

export function setSessionStatus(sessionId: string, status: string): void {
  if (!sessionId) return;
  state.store.sessionStatus[sessionId] = status;
  void desktop.storeSet({ sessionStatus: { [sessionId]: status } }).catch(() => undefined);
}
