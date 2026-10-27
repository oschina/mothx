// Read-only sub-agent projection helpers. The list/status/transcript facts are
// Runtime-owned and fetched over ACP; this module only keeps the current UI
// projection and re-uses the ordinary transcript item shape.

import { desktop, invoke } from './api';
import { emit, hasFeature, state, type SubagentInfo, type ToolCallContentShape, type TranscriptItem } from './state';

interface SubagentListResult {
  agents?: SubagentInfo[];
}

interface SubagentMessagesResult {
  agentId?: string;
  updates?: Record<string, unknown>[];
}

export type SubagentStatusKind = 'running' | 'completed' | 'failed' | 'cancelled' | 'incomplete';

// A long-lived team/ESM session can spawn many children. Keep running agents
// and the newest terminal ones; older terminal projections are recoverable from
// the Runtime list if the dialog is reopened.
const MAX_SUBAGENTS_PER_SESSION = 200;


export function subagentStatusKind(status: string): SubagentStatusKind {
  switch (status) {
    case 'done':
    case 'completed':
      return 'completed';
    case 'error':
    case 'failed':
      return 'failed';
    case 'canceled':
    case 'cancelled':
      return 'cancelled';
    case 'incomplete':
      return 'incomplete';
    case 'started':
    case 'ready':
    case 'running':
    case '':
      return 'running';
    default:
      return 'running';
  }
}

export function isSubagentRunning(info: SubagentInfo): boolean {
  return subagentStatusKind(info.status) === 'running';
}

export function subagentLabel(info: SubagentInfo): string {
  const name = info.memberDisplayName || info.memberId || info.agentId;
  return info.memberEmoji ? `${info.memberEmoji} ${name}` : name;
}

export function sessionSubagents(sessionId: string | null | undefined): SubagentInfo[] {
  if (!sessionId) return [];
  return state.subagents[sessionId] || [];
}

export function subagentSummary(list: SubagentInfo[]): { total: number; running: number; completed: number; failed: number } {
  let running = 0;
  let completed = 0;
  let failed = 0;
  for (const info of list) {
    const status = subagentStatusKind(info.status);
    if (status === 'running') running += 1;
    else if (status === 'completed') completed += 1;
    else if (status === 'failed') failed += 1;
  }
  return { total: list.length, running, completed, failed };
}

function capSubagents(list: SubagentInfo[]): SubagentInfo[] {
  if (list.length <= MAX_SUBAGENTS_PER_SESSION) return list;
  const running = list.filter(isSubagentRunning);
  if (running.length >= MAX_SUBAGENTS_PER_SESSION) return running.slice(-MAX_SUBAGENTS_PER_SESSION);
  const terminal = list.filter((info) => !isSubagentRunning(info));
  return [...running, ...terminal.slice(-(MAX_SUBAGENTS_PER_SESSION - running.length))];
}

export function forgetSessionSubagents(sessionId: string | null | undefined): void {
  if (!sessionId || !state.subagents[sessionId]) return;
  const next = { ...state.subagents };
  delete next[sessionId];
  state.subagents = next;
}

function mergeSubagent(previous: SubagentInfo | undefined, next: SubagentInfo): SubagentInfo {
  return {
    ...previous,
    ...next,
    status: subagentStatusKind(next.status),
  };
}

// refreshSubagents refreshes a session's child list from the canonical ACP
// projection. A failed read keeps the last known list instead of inventing
// idle or empty state.
export async function refreshSubagents(sessionId: string | null | undefined): Promise<void> {
  if (!sessionId || !hasFeature('subagentPanel')) return;
  try {
    const result = await invoke<SubagentListResult>('mothx/subagent/list', { sessionId });
    const previous = state.subagents[sessionId] || [];
    const byId = new Map(previous.map((info) => [info.agentId, info]));
    const agents = capSubagents((result.agents || []).map((info) => mergeSubagent(byId.get(info.agentId), info)));
    state.subagents = { ...state.subagents, [sessionId]: agents };
    emit();
  } catch (error) {
    desktop.log(`mothx/subagent/list failed: ${error instanceof Error ? error.message : String(error)}`);
  }
}

// applySubagentEvent updates the live list projection for any session. It is
// safe for background sessions: the map is keyed by session ID and is never
// rendered into the active transcript by this function.
export function applySubagentEvent(event: Record<string, unknown>): void {
  const sessionId = String(event.sessionId || '');
  const agentId = String(event.agentId || '');
  if (!sessionId || !agentId) return;
  const status = subagentStatusKind(String(event.status || 'running'));
  const previous = state.subagents[sessionId] || [];
  const index = previous.findIndex((info) => info.agentId === agentId);
  const existing = index >= 0 ? previous[index] : undefined;
  const next: SubagentInfo = mergeSubagent(existing, {
    agentId,
    parentAgentId: event.parentAgentId ? String(event.parentAgentId) : existing?.parentAgentId,
    memberId: event.memberId ? String(event.memberId) : existing?.memberId,
    expertId: event.expertId ? String(event.expertId) : existing?.expertId,
    memberDisplayName: event.memberDisplayName ? String(event.memberDisplayName) : existing?.memberDisplayName,
    memberEmoji: event.memberEmoji ? String(event.memberEmoji) : existing?.memberEmoji,
    memberRole: event.memberRole ? String(event.memberRole) : existing?.memberRole,
    status,
    messageCount: existing?.messageCount,
  });
  const agents = capSubagents(index >= 0 ? previous.map((info, at) => (at === index ? next : info)) : [...previous, next]);
  state.subagents = { ...state.subagents, [sessionId]: agents };
  emit();
}

// fetchSubagentTranscript asks ACP for one child's read-only transcript in the
// same session-update vocabulary the parent chat uses.
export async function fetchSubagentTranscript(sessionId: string, agentId: string): Promise<TranscriptItem[]> {
  const result = await invoke<SubagentMessagesResult>('mothx/subagent/messages', { sessionId, agentId });
  return projectSubagentUpdates(result.updates || []);
}

function contentText(content: unknown): string {
  if (!content) return '';
  if (typeof content === 'string') return content;
  const block = content as { type?: string; text?: string };
  if (block.type === 'text' && typeof block.text === 'string') return block.text;
  return '';
}

// mergeSubagentTranscript keeps local disclosure choices (tool expansion and
// reasoning expansion) while a running child transcript is refreshed.
export function mergeSubagentTranscript(previous: TranscriptItem[], next: TranscriptItem[]): TranscriptItem[] {
  const byKey = new Map(previous.map((item) => [item.key, item]));
  return next.map((item) => {
    const prior = byKey.get(item.key);
    if (!prior) return item;
    if (item.kind === 'tool' && prior.kind === 'tool') return { ...item, open: prior.open };
    if (item.kind === 'thought' && prior.kind === 'thought') return { ...item, open: prior.open };
    return item;
  });
}

// projectSubagentUpdates reduces the ACP session updates produced for a child
// agent into the same TranscriptItem union TranscriptEntry renders. It is a
// pure, local projection; it never mutates the parent session transcript.
export function projectSubagentUpdates(updates: Record<string, unknown>[]): TranscriptItem[] {
  const items: TranscriptItem[] = [];
  const byKey = new Map<string, TranscriptItem>();
  const find = (key: string) => byKey.get(key);
  const upsert = (item: TranscriptItem) => {
    const existing = byKey.get(item.key);
    if (existing) Object.assign(existing, item);
    else {
      byKey.set(item.key, item);
      items.push(item);
    }
  };

  for (const update of updates) {
    const kind = String(update.sessionUpdate || '');
    switch (kind) {
      case 'user_message_chunk': {
        const messageId = String(update.messageId || `subagent-user-${items.length}`);
        const text = contentText(update.content);
        const key = `user:${messageId}`;
        const existing = find(key);
        if (existing && existing.kind === 'user') existing.text += text;
        else upsert({ kind: 'user', key, text });
        break;
      }
      case 'agent_message_chunk': {
        const messageId = String(update.messageId || `subagent-agent-${items.length}`);
        const text = contentText(update.content);
        const key = `agent:${messageId}`;
        const existing = find(key);
        if (existing && existing.kind === 'agent') existing.text += text;
        else upsert({ kind: 'agent', key, text });
        break;
      }
      case 'agent_thought_chunk': {
        const messageId = String(update.messageId || `subagent-thought-${items.length}`);
        const text = contentText(update.content);
        const key = `thought:${messageId}`;
        const existing = find(key);
        if (existing && existing.kind === 'thought') existing.text += text;
        else upsert({ kind: 'thought', key, text, open: false });
        break;
      }
      case 'tool_call': {
        const toolCallId = String(update.toolCallId || '');
        if (!toolCallId) break;
        upsert({
          kind: 'tool',
          key: `tool:${toolCallId}`,
          toolCallId,
          title: String(update.title || toolCallId),
          toolKind: String(update.kind || 'other'),
          status: String(update.status || 'pending'),
          open: false,
          rawInput: (update.rawInput as Record<string, unknown>) || undefined,
          contents: [],
          locations: (update.locations as { path: string }[]) || undefined,
        });
        break;
      }
      case 'tool_call_update': {
        const toolCallId = String(update.toolCallId || '');
        if (!toolCallId) break;
        const key = `tool:${toolCallId}`;
        let item = find(key);
        if (!item || item.kind !== 'tool') {
          item = {
            kind: 'tool',
            key,
            toolCallId,
            title: String(update.title || toolCallId),
            toolKind: 'other',
            status: 'pending',
            open: false,
            contents: [],
          };
          upsert(item);
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
        break;
      }
      case 'plan': {
        const entries = (update.entries as { content: string; priority: string; status: string }[]) || [];
        upsert({ kind: 'plan', key: `subagent-plan:${items.length}`, entries });
        break;
      }
      case 'artifact': {
        const artifactId = String(update.artifactId || '');
        const filename = String(update.filename || artifactId || 'artifact');
        upsert({
          kind: 'artifact',
          key: `artifact:${artifactId || filename}`,
          artifactId,
          filename,
          artifactKind: String(update.kind || 'file'),
          mediaType: update.mediaType ? String(update.mediaType) : undefined,
          size: typeof update.size === 'number' ? update.size : undefined,
        });
        break;
      }
      default:
        break;
    }
  }
  return items;
}
