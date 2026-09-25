// ACP v1 wire types (JSON-RPC 2.0, newline-delimited JSON over stdio).
// Mirrors internal/acp/acp.go; keep additive and camelCase.

export interface RpcError {
  code: number;
  message: string;
  data?: unknown;
}

export interface RpcMessage {
  jsonrpc: '2.0';
  id?: number | string | null;
  method?: string;
  params?: unknown;
  result?: unknown;
  error?: RpcError;
}

export interface ClientInfo {
  name: string;
  title?: string;
  version?: string;
}

export interface WorkspaceSpec {
  cwd: string;
  additionalDirectories?: string[];
}

export interface InitializeParams {
  protocolVersion: number;
  clientCapabilities?: {
    fs?: { readTextFile?: boolean; writeTextFile?: boolean };
    terminal?: boolean;
    elicitation?: { form?: Record<string, never>; url?: Record<string, never> };
    session?: { configOptions?: { boolean?: Record<string, never> } };
  };
  clientInfo?: ClientInfo;
  _meta?: {
    mothx?: {
      workspace?: WorkspaceSpec;
      surface?: string;
      parentSessionId?: string;
    };
  };
}

export interface SessionConfigOptionChoice {
  value: string;
  name: string;
  description?: string;
}

export interface SessionConfigOption {
  type: string;
  id: string;
  name: string;
  description?: string;
  category?: string;
  currentValue: string;
  options?: SessionConfigOptionChoice[];
}

export interface SessionMode {
  id: string;
  name: string;
  description?: string;
}

export interface SessionModeState {
  currentModeId: string;
  availableModes: SessionMode[];
}

export interface InitializeResult {
  protocolVersion: number;
  agentCapabilities: {
    loadSession?: boolean;
    promptCapabilities?: { image?: boolean; audio?: boolean; embeddedContext?: boolean };
    sessionCapabilities?: Record<string, unknown>;
    mcpCapabilities?: { http?: boolean; sse?: boolean };
    _meta?: Record<string, unknown>;
  };
  agentInfo?: ClientInfo;
  authMethods?: unknown[];
  _meta?: Record<string, unknown>;
}

export interface NewSessionResult {
  sessionId: string;
  parentSessionId?: string;
  modes?: SessionModeState;
  configOptions?: SessionConfigOption[];
}

export interface ContentBlock {
  type: string;
  text?: string;
  mimeType?: string;
  data?: string;
  name?: string;
  title?: string;
  description?: string;
  uri?: string;
  size?: number;
}

export interface ToolCallDiffContent {
  type: 'diff';
  path: string;
  oldText: string | null;
  newText: string;
}

export interface ToolCallBlockContent {
  type: 'content';
  content?: ContentBlock;
}

export type ToolCallContent = ToolCallDiffContent | ToolCallBlockContent;

export interface PlanEntry {
  content: string;
  priority: string;
  status: string;
}

export interface AvailableCommand {
  name: string;
  description?: string;
  input?: unknown;
  _meta?: Record<string, unknown>;
}

export interface UsageCost {
  amount: number;
  currency: string;
}

// session/update discriminated by the sessionUpdate field.
export interface SessionUpdate {
  sessionUpdate: string;
  messageId?: string;
  configId?: string;
  value?: string;
  configOptions?: SessionConfigOption[];
  currentModeId?: string;
  content?: ContentBlock | ToolCallContent[] | null;
  toolCallId?: string;
  locations?: { path: string }[];
  title?: string;
  kind?: string;
  status?: string;
  rawInput?: Record<string, unknown>;
  rawOutput?: Record<string, unknown>;
  used?: number;
  size?: number;
  cost?: UsageCost;
  entries?: PlanEntry[];
  availableCommands?: AvailableCommand[];
  updatedAt?: string;
  // P0 artifact projection (docs/proposal/desktop-acp-frontend-gap-proposal.md).
  artifactId?: string;
  filename?: string;
  mediaType?: string;
  runId?: string;
  _meta?: Record<string, unknown>;
}

export interface SessionUpdateNotification {
  sessionId: string;
  update: SessionUpdate;
}

// _mothx/session_event extension payload.
export interface SessionEvent {
  sessionId: string;
  event: string;
  status?: string;
  message?: string;
  error?: string;
  errorInfo?: {
    code?: string;
    type?: string;
    message?: string;
    retryable?: boolean;
    retryMode?: string;
    [key: string]: unknown;
  };
  [key: string]: unknown;
}

// mothx/worktree/status extension payload: the additive projection of a
// canonical Runtime worktree lifecycle event (pending|ready|failed|removed).
export interface WorktreeStatusNotification {
  worktree?: {
    id?: string;
    name?: string;
    branch?: string;
    directory?: string;
    status?: string;
    error?: string;
  };
}

export interface PermissionToolCall {
  toolCallId: string;
  title?: string;
  kind?: string;
  status?: string;
  rawInput?: Record<string, unknown>;
}

export interface PermissionOption {
  optionId: string;
  name: string;
  kind: string;
}

export interface RequestPermissionParams {
  sessionId: string;
  toolCall: PermissionToolCall;
  options: PermissionOption[];
}

export interface QuestionOption {
  id: string;
  label: string;
}

// mothx/requestQuestion payload (initialized wire clients).
export interface RequestQuestionParams {
  sessionId?: string;
  prompt?: string;
  question?: string;
  options?: QuestionOption[] | string[];
  explanation?: string;
  multi?: boolean;
  title?: string;
  placeholder?: string;
  timeoutMs?: number;
}

export interface ListedSession {
  sessionId: string;
  cwd: string;
  additionalDirectories?: string[];
  title?: string;
  provider: string;
  model: string;
  mode?: string;
  thoughtLevel?: string;
  parentSessionId?: string;
  updatedAt?: string;
  _meta?: Record<string, unknown>;
}

export interface ListSessionsResult {
  sessions: ListedSession[];
  nextCursor?: string;
}

export interface DoctorCheck {
  id?: string;
  title?: string;
  status?: string;
  detail?: string;
  fix?: string;
  [key: string]: unknown;
}

export interface DoctorResult {
  version?: string;
  ok?: boolean;
  checks?: DoctorCheck[];
  [key: string]: unknown;
}

// ACP startup error line: `MOTHX_ACP_ERROR {json}` on stderr.
export interface AcpStartupError {
  code: string;
  message: string;
  fix?: string;
}
