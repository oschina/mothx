// 转录投影:所有 session/update 与 _mothx/session_event 都投影到
// state.transcript(DOM 无关)。React 组件按 key 渲染;本模块只做状态归约、
// 反向请求(审批/提问)决议与制品打开等 ACP 动作。

import { acp, desktop, invoke } from './api';
import { requestChatScroll } from './bus';
import { getLocale, t } from './i18n';
import { emit, setSessionStatus, state, type ToolCallContentShape, type TranscriptItem, type UsageCacheProjection } from './state';
import { previewImage, toast } from './ui-host';

export type ToolItem = Extract<TranscriptItem, { kind: 'tool' }>;
export type PlanItem = Extract<TranscriptItem, { kind: 'plan' }>;
export type ArtifactItem = Extract<TranscriptItem, { kind: 'artifact' }>;
export type PermissionItem = Extract<TranscriptItem, { kind: 'permission' }>;
export type QuestionItem = Extract<TranscriptItem, { kind: 'question' }>;
export type DecisionItem = PermissionItem | QuestionItem;

export type RunStatusVariant = 'idle' | 'planning' | 'working' | 'pending' | 'completed' | 'failed' | 'cancelled';

export const STATUS_VARIANT: Record<string, RunStatusVariant> = {
  idle: 'idle',
  loading: 'planning',
  planning: 'planning',
  working: 'working',
  pending: 'pending',
  completed: 'completed',
  failed: 'failed',
  cancelled: 'cancelled',
};

export function clearTranscript(sessionId: string | null): void {
  state.transcript = [];
  state.transcriptSessionId = sessionId;
  state.transcriptNextCursor = '';
  state.transcriptLoading = false;
  state.currentPlanKey = null;
  state.artifactRunCount = 0;
  state.pendingUserKey = null;
  state.usage = null;
}

function upsert(item: TranscriptItem): void {
  const existing = state.transcript.find((entry) => entry.key === item.key);
  if (existing) Object.assign(existing, item);
  else state.transcript.push(item);
}

// Prepends an older ACP transcript page without inventing a second message
// format. 已渲染条目的展开(open)选择保留在条目对象上,分页不覆盖。
export function applyTranscriptPage(sessionId: string, updates: Record<string, unknown>[], prepend: boolean): void {
  if (!matchesTranscript(sessionId) || updates.length === 0) return;
  const existing = new Set(state.transcript.map((item) => item.key));
  const pageItems: TranscriptItem[] = [];
  const originalPush = state.transcript.push.bind(state.transcript);
  pageOverlay = pageItems;
  state.transcript.push = (...items: TranscriptItem[]) => {
    for (const item of items) {
      if (!existing.has(item.key)) {
        existing.add(item.key);
        pageItems.push(item);
      }
    }
    return state.transcript.length;
  };
  try {
    for (const update of updates) applySessionUpdate(sessionId, update);
  } finally {
    pageOverlay = null;
    state.transcript.push = originalPush;
  }
  const resolved: TranscriptItem[] = [];
  const byKey = new Map<string, TranscriptItem>();
  for (const item of pageItems) {
    const prior = byKey.get(item.key);
    if (!prior) {
      byKey.set(item.key, item);
      resolved.push(item);
    } else if ((prior.kind === 'agent' || prior.kind === 'user' || prior.kind === 'thought') && prior.kind === item.kind) {
      prior.text += item.text;
    } else {
      Object.assign(prior, item);
    }
  }
  if (prepend && resolved.length > 0) {
    state.transcript.unshift(...resolved);
  } else if (!prepend) {
    state.transcript.push(...resolved);
  }
  emit();
}

// 分页回放期间,同页先创建的条目(如 tool_call)必须能被后续 update(如
// tool_call_update)找到并就地改写;否则 update 会造出被丢弃的重复对象,
// 状态修改丢失,历史 tool 卡永远停留在初始 pending(排队中)。
let pageOverlay: TranscriptItem[] | null = null;

function findItem(key: string): TranscriptItem | undefined {
  if (pageOverlay) {
    const hit = pageOverlay.find((entry) => entry.key === key);
    if (hit) return hit;
  }
  return state.transcript.find((entry) => entry.key === key);
}

// ===== session/update 投影 =====

export function applySessionUpdate(sessionId: string, update: Record<string, unknown>): void {
  if (state.transcriptSessionId && state.transcriptSessionId !== sessionId) {
    // 非当前转录会话的事件只维护侧边栏/标题等元数据。
    applyMetaOnlyUpdate(sessionId, update);
    return;
  }
  const kind = String(update.sessionUpdate || '');
  switch (kind) {
    case 'user_message_chunk': {
      const messageId = String(update.messageId || `user-${state.transcript.length}`);
      const text = contentText(update.content);
      const key = `user:${messageId}`;
      // 乐观气泡归并:发送时本地先插入的气泡被 server 回显认领。
      if (state.pendingUserKey) {
        const index = state.transcript.findIndex((entry) => entry.key === state.pendingUserKey);
        state.pendingUserKey = null;
        if (index >= 0) {
          state.transcript.splice(index, 1, { kind: 'user', key, text });
          emit();
          requestChatScroll();
          return;
        }
      }
      const existing = findItem(key);
      if (existing && existing.kind === 'user') existing.text += text;
      else upsert({ kind: 'user', key, text });
      emit();
      requestChatScroll();
      return;
    }
    case 'agent_message_chunk': {
      const messageId = String(update.messageId || `agent-${state.transcript.length}`);
      const text = contentText(update.content);
      const key = `agent:${messageId}`;
      const existing = findItem(key);
      if (existing && existing.kind === 'agent') existing.text += text;
      else upsert({ kind: 'agent', key, text });
      emit();
      requestChatScroll();
      return;
    }
    case 'agent_thought_chunk': {
      const messageId = String(update.messageId || `thought-${state.transcript.length}`);
      const text = contentText(update.content);
      const key = `thought:${messageId}`;
      const existing = findItem(key);
      if (existing && existing.kind === 'thought') existing.text += text;
      else upsert({ kind: 'thought', key, text, open: false });
      emit();
      requestChatScroll();
      return;
    }
    case 'tool_call': {
      const toolCallId = String(update.toolCallId || '');
      if (!toolCallId) return;
      upsert({
        kind: 'tool',
        key: `tool:${toolCallId}`,
        toolCallId,
        title: String(update.title || toolCallId),
        toolKind: String(update.kind || 'other'),
        status: String(update.status || 'pending'),
        // Local disclosure state only. Streaming ACP updates must not overwrite
        // the user's choice to inspect (or hide) a tool execution.
        open: false,
        rawInput: (update.rawInput as Record<string, unknown>) || undefined,
        contents: [],
        locations: (update.locations as { path: string }[]) || undefined,
      });
      emit();
      requestChatScroll();
      return;
    }
    case 'tool_call_update': {
      const toolCallId = String(update.toolCallId || '');
      if (!toolCallId) return;
      const key = `tool:${toolCallId}`;
      let item = findItem(key);
      if (!item || item.kind !== 'tool') {
        item = { kind: 'tool', key, toolCallId, title: String(update.title || toolCallId), toolKind: 'other', status: 'pending', open: false, contents: [] };
        state.transcript.push(item);
      }
      if (update.title) item.title = String(update.title);
      if (update.kind) item.toolKind = String(update.kind);
      if (update.status) item.status = String(update.status);
      if (update.rawInput) item.rawInput = update.rawInput as Record<string, unknown>;
      if (update.locations) item.locations = update.locations as { path: string }[];
      const contents = update.content;
      if (Array.isArray(contents) && contents.length > 0) {
        item.contents = contents as ToolCallContentShape[];
      }
      if (item.status === 'completed' || item.status === 'failed') maybeAddToolArtifactFallback(item);
      emit();
      requestChatScroll();
      return;
    }
    case 'plan': {
      const entries = (update.entries as { content: string; priority: string; status: string }[]) || [];
      const key = state.currentPlanKey || `plan:${Date.now()}`;
      state.currentPlanKey = key;
      const meta = (update._meta as Record<string, unknown> | undefined)?.['mothx.dev'] as Record<string, string> | undefined;
      upsert({ kind: 'plan', key, entries, title: meta?.title, note: meta?.note });
      emit();
      requestChatScroll();
      return;
    }
    case 'artifact': {
      // P0-1 制品投影(docs/proposal/desktop-acp-frontend-gap-proposal.md)。
      const artifactId = String(update.artifactId || '');
      const filename = String(update.filename || artifactId || 'artifact');
      const key = `artifact:${artifactId || filename}`;
      upsert({
        kind: 'artifact',
        key,
        artifactId,
        filename,
        artifactKind: String(update.kind || 'file'),
        mediaType: update.mediaType ? String(update.mediaType) : undefined,
        size: typeof update.size === 'number' ? update.size : undefined,
      });
      state.artifactRunCount += 1;
      emit();
      requestChatScroll();
      return;
    }
    case 'usage_update': {
      state.usage = {
        used: Number(update.used || 0),
        size: Number(update.size || 0),
        cost: update.cost ? Number((update.cost as { amount?: number }).amount || 0) : state.usage?.cost,
        cache: usageCacheProjection(update) ?? state.usage?.cache,
      };
      emit();
      return;
    }
    case 'available_commands_update': {
      state.availableCommands = (update.availableCommands as typeof state.availableCommands) || [];
      emit();
      return;
    }
    case 'current_mode_update': {
      state.currentMode = String(update.currentModeId || state.currentMode);
      emit();
      return;
    }
    case 'config_option_update': {
      const options = (update.configOptions as typeof state.configOptions) || [];
      if (options.length > 0) {
        state.configOptions = options;
        const mode = options.find((option) => option.id === 'mode');
        if (mode) state.currentMode = mode.currentValue;
      }
      emit();
      return;
    }
    case 'session_info_update': {
      applyTitleUpdate(sessionId, String(update.title || ''));
      return;
    }
    default:
      return;
  }
}

function usageCacheProjection(update: Record<string, unknown>): UsageCacheProjection | null {
  const dev = (update._meta as Record<string, unknown> | undefined)?.['mothx.dev'] as Record<string, unknown> | undefined;
  const totalInputTokens = Number(dev?.totalInputTokens || 0);
  if (!(totalInputTokens > 0)) return null;
  return {
    cacheRead: Number(dev?.cacheRead || 0),
    cacheWrite: Number(dev?.cacheWrite || 0),
    totalInputTokens,
  };
}

function applyMetaOnlyUpdate(sessionId: string, update: Record<string, unknown>): void {
  const kind = String(update.sessionUpdate || '');
  if (kind === 'session_info_update') applyTitleUpdate(sessionId, String(update.title || ''));
  if (kind === 'available_commands_update') {
    state.availableCommands = (update.availableCommands as typeof state.availableCommands) || [];
    emit();
  }
  if (kind === 'config_option_update' && sessionId === state.activeSessionId) {
    const options = (update.configOptions as typeof state.configOptions) || [];
    if (options.length > 0) state.configOptions = options;
    emit();
  }
}

function applyTitleUpdate(sessionId: string, title: string): void {
  if (!title) return;
  const session = state.sessions.find((entry) => entry.sessionId === sessionId);
  if (session) session.title = title;
  if (sessionId === state.activeSessionId) state.activeTitle = title;
  emit();
}

function contentText(content: unknown): string {
  if (!content) return '';
  if (typeof content === 'string') return content;
  const block = content as { type?: string; text?: string };
  if (block.type === 'text' && typeof block.text === 'string') return block.text;
  return '';
}

// publish_artifact 工具调用的降级投影:P0 artifact 事件缺席时也能看到制品。
function maybeAddToolArtifactFallback(item: ToolItem): void {
  if (!item.title.startsWith('publish_artifact')) return;
  if (item.status !== 'completed') return;
  const already = state.transcript.some(
    (entry) => entry.kind === 'artifact' && !entry.fromToolCall && entry.filename === String((item.rawInput?.filename as string) || basename(String(item.rawInput?.path || ''))),
  );
  if (already) return;
  const path = String(item.rawInput?.path || '');
  const filename = String(item.rawInput?.filename || basename(path) || 'artifact');
  upsert({ kind: 'artifact', key: `artifact-tool:${item.toolCallId}`, artifactId: '', filename, artifactKind: String(item.rawInput?.kind || 'auto'), fromToolCall: true, size: undefined, mediaType: undefined });
}

function basename(path: string): string {
  const parts = path.split(/[\\/]/);
  return parts[parts.length - 1] || path;
}

// ===== _mothx/session_event 投影 =====

export function applySessionEvent(event: Record<string, unknown>): void {
  const sessionId = String(event.sessionId || '');
  const name = String(event.event || '');
  if (name === 'run_status') {
    // Phase 1.1:run 生命周期投影,侧边栏状态点的协议源。
    const status = String(event.status || '');
    const mapped = status === 'running' ? 'working' : status;
    if (mapped) setSessionStatus(sessionId, mapped);
    emit();
    return;
  }
  if (name === 'workspace') {
    // Desktop does not use ACP's optional process-wide workspace window.
    // Session cwd remains the only work-directory projection in the UI.
    return;
  }
  if (name === 'decision_deadline') {
    const requestId = String(event.requestId || '');
    const remaining = Number(event.remainingMs || 0);
    const item = state.transcript.find(
      (entry) => (entry.kind === 'permission' || entry.kind === 'question') && entry.requestId === requestId && !entry.resolved,
    );
    if (item && (item.kind === 'permission' || item.kind === 'question')) {
      item.deadline = Date.now() + remaining;
      emit();
    }
    return;
  }
  if (name === 'subagent') {
    if (!matchesTranscript(sessionId)) return;
    const agentId = String(event.agentId || '');
    if (!agentId) return;
    const status = String(event.status || 'started');
    const memberName = String(event.memberDisplayName || event.memberId || event.title || event.agentId);
    const emoji = String(event.memberEmoji || '');
    upsert({
      kind: 'subagent', key: `subagent:${agentId}`, agentId, status,
      title: emoji ? `${emoji} ${memberName}` : memberName,
      role: event.memberRole ? String(event.memberRole) : undefined,
      expertId: event.expertId ? String(event.expertId) : undefined,
    });
    emit();
    requestChatScroll();
    return;
  }
  if (name === 'terminal') {
    const status = String(event.status || 'completed');
    if (state.runningSessionId && state.runningSessionId === sessionId) {
      state.promptInFlight = false;
      state.runningSessionId = null;
    } else {
      state.promptInFlight = false;
    }
    if (status === 'completed') {
      state.runStatus = 'completed';
      setSessionStatus(sessionId, 'completed');
    } else if (status === 'cancelled') {
      state.runStatus = 'cancelled';
      setSessionStatus(sessionId, 'cancelled');
    } else {
      state.runStatus = 'failed';
      setSessionStatus(sessionId, 'failed');
      const info = event.errorInfo as { code?: string; message?: string; retryable?: boolean } | undefined;
      if (matchesTranscript(sessionId)) {
        upsert({
          kind: 'error',
          key: `error:${sessionId}:${Date.now()}`,
          message: String(event.error || info?.message || status),
          code: info?.code,
          retryable: info?.retryable,
        });
        emit();
      }
    }
    state.currentPlanKey = null;
    emit();
    requestChatScroll();
    return;
  }
  if (!matchesTranscript(sessionId)) return;
  if (name === 'status') {
    upsert({ kind: 'status', key: `status:${sessionId}:${Date.now()}`, text: String(event.message || ''), spin: true });
    emit();
    requestChatScroll();
    return;
  }
  if (name === 'retry') {
    const message = String((event as { message?: string }).message || '');
    upsert({ kind: 'status', key: `retry:${sessionId}:${Date.now()}`, text: t('chat.retrying', { m: message }), spin: true });
    emit();
    return;
  }
  if (name === 'compaction_start') {
    upsert({ kind: 'status', key: `compaction:${sessionId}:${Date.now()}`, text: t('chat.compaction'), spin: true });
    emit();
    return;
  }
}

function matchesTranscript(sessionId: string): boolean {
  return !state.transcriptSessionId || state.transcriptSessionId === sessionId;
}

// ===== 反向请求(审批/提问) =====

export function applyReverseRequest(id: number | string, method: string, params: Record<string, unknown>): void {
  const requestId = String(id);
  if (method === 'session/request_permission') {
    const toolCall = (params.toolCall || {}) as { toolCallId?: string; title?: string; kind?: string; rawInput?: Record<string, unknown> };
    const options = (params.options || []) as { optionId: string; name: string; kind: string }[];
    const sessionId = String(params.sessionId || state.activeSessionId || '');
    if (!matchesTranscript(sessionId)) return;
    state.runStatus = 'pending';
    setSessionStatus(sessionId, 'pending');
    upsert({
      kind: 'permission',
      key: `permission:${requestId}`,
      requestId,
      sessionId,
      title: String(toolCall.title || toolCall.toolCallId || 'tool'),
      toolKind: String(toolCall.kind || 'other'),
      rawInput: toolCall.rawInput,
      options,
    });
    emit();
    requestChatScroll();
    return;
  }
  if (method === 'mothx/requestQuestion' || method === '_mothx/request_question' || method === 'elicitation/create') {
    const sessionId = String(params.sessionId || state.activeSessionId || '');
    if (!matchesTranscript(sessionId)) return;
    const promptText = String(params.prompt || params.question || params.message || '');
    const explanation = String(params.explanation || params.placeholder || '');
    const rawOptions = (params.options || []) as { id?: string; label?: string }[] | string[];
    const options = rawOptions.map((option) =>
      typeof option === 'string' ? { id: option, label: option } : { id: String(option.id ?? option.label ?? ''), label: String(option.label ?? option.id ?? '') },
    );
    state.runStatus = 'pending';
    setSessionStatus(sessionId, 'pending');
    upsert({
      kind: 'question',
      key: `question:${requestId}`,
      requestId,
      sessionId,
      prompt: promptText,
      explanation: explanation || undefined,
      options,
    });
    emit();
    requestChatScroll();
  }
}

export function resolvePermission(item: PermissionItem, optionId: string): void {
  item.resolved = optionId;
  acp.respond(item.requestId, { outcome: { outcome: 'selected', optionId } });
  finishDecision(item.sessionId);
  emit();
}

export function cancelPermission(item: PermissionItem): void {
  item.resolved = t('chat.cancelled');
  acp.respond(item.requestId, { outcome: { outcome: 'cancelled' } });
  finishDecision(item.sessionId);
  emit();
}

export function resolveQuestion(item: QuestionItem, answer: string): void {
  item.resolved = answer;
  acp.respond(item.requestId, { answer, ok: true });
  finishDecision(item.sessionId);
  emit();
}

export function cancelQuestion(item: QuestionItem): void {
  item.resolved = t('chat.cancelled');
  acp.respond(item.requestId, { cancelled: true });
  finishDecision(item.sessionId);
  emit();
}

function finishDecision(sessionId: string): void {
  const stillPending = state.transcript.some(
    (entry) => ((entry.kind === 'permission' || entry.kind === 'question') && !entry.resolved) && entry.sessionId === sessionId,
  );
  if (!stillPending) {
    state.runStatus = state.promptInFlight ? 'working' : 'completed';
    if (sessionId) setSessionStatus(sessionId, state.promptInFlight ? 'working' : 'completed');
  }
}

// 运行被取消/终止时,把未决决策标记为已取消(server 端也会 $/cancel_request)。
export function terminalizePendingDecisions(): void {
  let changed = false;
  for (const entry of state.transcript) {
    if ((entry.kind === 'permission' || entry.kind === 'question') && !entry.resolved) {
      entry.resolved = t('chat.cancelled');
      acp.cancelReverse(entry.requestId);
      changed = true;
    }
  }
  if (changed) emit();
}

// ===== 纯展示助手(组件层共用) =====

export function toolSummary(item: ToolItem): string {
  const input = item.rawInput || {};
  const command = input.command;
  if (typeof command === 'string' && command !== '') return command;
  const path = input.path;
  if (typeof path === 'string' && path !== '') return path;
  const pattern = input.pattern;
  if (typeof pattern === 'string' && pattern !== '') return pattern;
  return item.title;
}

export function statusLabel(status: string): string {
  switch (status) {
    case 'pending': return getLocale() === 'zh' ? '排队中' : 'pending';
    case 'in_progress': return getLocale() === 'zh' ? '执行中' : 'running';
    case 'completed': return getLocale() === 'zh' ? '完成' : 'done';
    case 'failed': return getLocale() === 'zh' ? '失败' : 'failed';
    default: return status;
  }
}

export function prettyInput(input: Record<string, unknown>): string {
  const command = input.command;
  if (typeof command === 'string' && command !== '') {
    const extra = Object.keys(input).filter((key) => key !== 'command');
    return extra.length === 0 ? command : JSON.stringify(input, null, 2);
  }
  return JSON.stringify(input, null, 2);
}

export type DiffLineType = 'add' | 'del' | 'context';
export interface DiffLine {
  type: DiffLineType;
  text: string;
}

// 行级 LCS diff;超大文件降级为整段展示。返回纯数据,由组件渲染。
export function buildDiffLines(oldText: string | null, newText: string): DiffLine[] {
  const lines: DiffLine[] = [];
  const newLines = newText.split('\n');
  if (oldText === null) {
    for (const line of newLines.slice(0, 400)) lines.push({ type: 'add', text: `+ ${line}` });
    return lines;
  }
  const oldLines = oldText.split('\n');
  if (oldLines.length > 1500 || newLines.length > 1500) {
    for (const line of oldLines.slice(0, 200)) lines.push({ type: 'del', text: `- ${line}` });
    lines.push({ type: 'context', text: '…' });
    for (const line of newLines.slice(0, 200)) lines.push({ type: 'add', text: `+ ${line}` });
    return lines;
  }
  const rows = oldLines.length;
  const cols = newLines.length;
  const dp: number[][] = Array.from({ length: rows + 1 }, () => new Array<number>(cols + 1).fill(0));
  for (let i = rows - 1; i >= 0; i -= 1) {
    for (let j = cols - 1; j >= 0; j -= 1) {
      dp[i][j] = oldLines[i] === newLines[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
    }
  }
  let i = 0;
  let j = 0;
  let budget = 800;
  while (i < rows && j < cols && budget > 0) {
    if (oldLines[i] === newLines[j]) {
      lines.push({ type: 'context', text: `  ${oldLines[i]}` });
      i += 1;
      j += 1;
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      lines.push({ type: 'del', text: `- ${oldLines[i]}` });
      i += 1;
    } else {
      lines.push({ type: 'add', text: `+ ${newLines[j]}` });
      j += 1;
    }
    budget -= 1;
  }
  while (i < rows && budget > 0) {
    lines.push({ type: 'del', text: `- ${oldLines[i]}` });
    i += 1;
    budget -= 1;
  }
  while (j < cols && budget > 0) {
    lines.push({ type: 'add', text: `+ ${newLines[j]}` });
    j += 1;
    budget -= 1;
  }
  if (budget <= 0) lines.push({ type: 'context', text: '…' });
  return lines;
}

export function planStepClass(status: string): 'done' | 'doing' | 'failed' | '' {
  switch (status) {
    case 'completed':
    case 'done':
      return 'done';
    case 'in_progress':
    case 'running':
      return 'doing';
    case 'failed':
      return 'failed';
    default:
      return '';
  }
}

export function deadlineText(deadline: number): string {
  const remaining = deadline - Date.now();
  if (remaining <= 0) return t('chat.deadlineExpired');
  const totalSeconds = Math.ceil(remaining / 1000);
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  const label = minutes > 0 ? `${minutes}:${String(seconds).padStart(2, '0')}` : `${seconds}s`;
  return t('chat.deadline', { t: label });
}

export function formatBytes(size?: number): string {
  if (!size || size <= 0) return '';
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / 1024 / 1024).toFixed(1)} MB`;
}

// ===== 制品打开 =====

export async function openArtifact(item: ArtifactItem): Promise<void> {
  return openArtifactMeta(state.transcriptSessionId || state.activeSessionId, item);
}

export async function openArtifactMeta(
  sessionId: string | null,
  item: { artifactId: string; filename: string; mediaType?: string; artifactKind: string; size?: number; fromToolCall?: boolean },
): Promise<void> {
  if (item.fromToolCall || !item.artifactId) {
    // A mutable worktree path is not an artifact capability. Older tool-only
    // updates cannot be opened directly by the renderer; canonical artifacts
    // must be fetched through ACP with their attachment ID.
    desktop.log(`artifact open skipped without ACP attachment ID: ${item.filename}`);
    toast(t('artifact.notSupported'));
    return;
  }
  try {
    const result = await invoke<{ filename?: string; mediaType?: string; size?: number; contentBase64?: string }>(
      'mothx/attachment/fetch',
      { sessionId, attachmentId: item.artifactId },
    );
    const data = result?.contentBase64 || '';
    const mediaType = result?.mediaType || item.mediaType || 'application/octet-stream';
    if (mediaType.startsWith('image/')) {
      previewImage(`data:${mediaType};base64,${data}`);
      return;
    }
    toast(t('artifact.saved'));
    desktop.log(`artifact fetched via ACP: ${item.filename} (${mediaType})`);
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    const rpcCode = (error as { code?: number }).code;
    const dataCode = (error as { data?: { code?: string } }).data?.code || '';
    if (rpcCode === -32601) toast(t('artifact.notSupported'));
    else if (dataCode === 'attachment_too_large') toast(t('artifact.openFailed', { e: dataCode }));
    else toast(t('artifact.openFailed', { e: message }));
  }
}

// 复制完整转录(聊天头部动作)。
export function transcriptAsMarkdown(): string {
  const lines: string[] = [];
  for (const item of state.transcript) {
    switch (item.kind) {
      case 'user':
        lines.push(`## 👤 User\n\n${item.text}\n`);
        break;
      case 'agent':
        lines.push(`## 🤖 MothX\n\n${item.text}\n`);
        break;
      case 'thought':
        lines.push(`<details><summary>${t('chat.thinking')}</summary>\n\n${item.text}\n\n</details>\n`);
        break;
      case 'tool':
        lines.push(`> 🔧 ${item.title} — ${item.status}\n`);
        break;
      case 'plan':
        lines.push(item.entries.map((entry) => `- [${entry.status}] ${entry.content}`).join('\n') + '\n');
        break;
      case 'artifact':
        lines.push(`> 📦 artifact: ${item.filename}\n`);
        break;
      case 'error':
        lines.push(`> ❌ ${item.message}\n`);
        break;
      default:
        break;
    }
  }
  return lines.join('\n');
}
