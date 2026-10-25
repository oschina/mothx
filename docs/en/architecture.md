# System Architecture

![MothX Architecture Overview](assets/image/architecture-en.webp)

> All entry points (TUI, CLI, WebUI/Serve, Desktop/ACP, and messaging channels) feed one front-end-neutral Agent Runtime, which owns sessions, runs, decisions, input/content, attachments/artifacts, MCP, skills, and policy resolution, and reuses one core Agent loop plus the provider abstraction, SQLite sessions, sandbox, stats, memory, and workflow infrastructure.


## Project Structure

```
mothx/
├── agent/                       # Public SDK types (Agent, Provider, Builder)
├── bootstrap/                   # Public SDK wiring into internal implementations
├── cmd/mothx/                   # CLI entry point and subcommands
├── desktop/                     # Electron desktop client (pure ACP projection)
├── ui/                          # Svelte Web UI embedded into Serve
├── internal/
│   ├── agent/                   # Core Agent loop, events, prompts, sub-agent tools
│   ├── agentruntime/            # Shared front-end-neutral Runtime: SessionRuntime,
│   │                            # ExecutionRuntime, decisions, artifacts, MCP,
│   │                            # knowledge bases, experts, worktrees
│   ├── acp/                     # ACP v1 server + mothx.dev extensions
│   ├── a2a/                     # A2A server and remote-agent master dispatch
│   ├── architecture/            # Static architecture guard tests
│   ├── browser/                 # vibe-browser automation tool
│   ├── config/                  # settings.json / serve.json schemas and defaults
│   ├── context/, contextfiles/  # Token budget and AGENTS.md/CLAUDE.md loading
│   ├── cron/                    # Scheduled task store and scheduler
│   ├── dao/                     # Sole owner of SQL/query builders/row mapping
│   ├── db/                      # Connection, transaction, and migration lifecycle
│   ├── esm/                     # Enable Supervisor Mode objective loop
│   ├── expert/                  # Expert Team bundle discovery and validation
│   ├── imageproc/               # Image normalization and resizing
│   ├── mcp/                     # MCP client/server integration
│   ├── memory/                  # memory.md tool for messaging channels
│   ├── messaging/               # WeChat/Feishu platform abstraction
│   ├── platform/                # Cross-platform helpers and embedded assets
│   ├── provider/                # Protocol providers, vendor adapters, factory
│   ├── sandbox/                 # Sandbox abstraction (bwrap, none)
│   ├── serve/                   # OpenAI API, Web UI, channels, hooks, cron, memory
│   ├── session/                 # Session domain, migrations, event store,
│   │                            # attachments, knowledge store
│   ├── skills/, skillhub/       # Skills and online marketplace
│   ├── stats/                   # Usage statistics dashboard and queries
│   ├── systeminit/              # AGENTS.md generation
│   ├── tools/                   # Built-in tool implementations
│   ├── tui/                     # Bubble Tea terminal UI
│   └── workflow/                # JavaScript workflow runtime
├── example/                     # Public SDK examples
└── docs/                        # Bilingual docs, generated indexes, site
```

## Running Modes

MothX supports 6 running modes, all sharing the same Agent, Provider, Tools,
and Session infrastructure:

```
┌─────────────────────────────────────────────────────────────────────────┐
│                        MothX Running Modes                         │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐                  │
│  │  TUI (default)│  │  Print Mode   │  │  ACP stdio   │                  │
│  │  mothx   │  │  mothx   │  │  mothx   │                  │
│  │              │  │  -p "..."     │  │  acp          │                  │
│  └──────────────┘  └──────────────┘  └──────────────┘                  │
│                                                                         │
│  ┌──────────────┐  ┌──────────────┐  ┌────────────┐                  │
│  │ Serve Mode   │  │ A2A Standalone│  │ A2A Master │                  │
│  │ mothx serve  │  │ mothx a2a     │  │ --enable-  │                  │
│  │              │  │ start         │  │ a2a-master │                  │
│  └──────────────┘  └──────────────┘  └────────────┘                  │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

## Core Components

### 0. Shared Agent Runtime

The Agent Runtime is the single boundary between front ends and the Agent Core.
TUI, CLI, WebUI/API, Desktop/ACP, WeChat/Feishu, Cron, ESM, and A2A adapters decode
their own protocol and project events; they do not own execution semantics.

- **Construction:** `SessionRuntime.BuildAgent`, `BuildTransientAgent`, and
  `agentruntime.NewAgentManager` are the only production Agent construction paths.
- **Policy:** source, effective mode, capabilities, tools, sandbox, approval,
  MCP, skills, reasoning, and run policy are resolved once through
  `ResolveSource`/`ResolvePolicy`. Product default mode is `yolo`.
- **Execution:** `ExecutionRuntime` + `RunStore` own durable run begin, reattach,
  update, cancel, finish, recovery, and the single canonical terminal event.
- **Decisions:** `DecisionService`/`DecisionRecord` own approval and question
  identity, deadlines, first-response-wins, replay, and expiry.
- **Input and artifacts:** all text/image/audio/file input normalizes into the
  Runtime input contract; attachments and generated artifacts are Runtime-owned
  and backed by `internal/session`.
- **Resources:** context/skills/rules, Expert Teams, MCP clients, knowledge
  services, worktrees, and coordinated shutdown are owned by the Runtime.
- **Admission:** one per-session submission coordinator and fenced execution
  lease decide whether a new run may start; adapters may only project that state.

### 1. Provider System

Provider is an abstraction layer for interacting with LLM APIs. All running modes
use `internal/provider/factory` for provider creation, which applies vendor adapter
defaults before constructing the generic OpenAI-compatible or Anthropic-compatible
protocol provider.

```
┌─────────────────────────────────────────────────────────────┐
│                      Provider Interface                      │
├─────────────────────────────────────────────────────────────┤
│  Chat(ctx, params) <-chan StreamEvent                       │
│  Models() []*Model                                          │
│  GetModel(id string) *Model                                 │
│  Name() string                                              │
└─────────────────────────────────────────────────────────────┘
                              │
                              │
            ┌─────────────────┴─────────────────┐
            ▼                                   ▼
    ┌───────────────────┐             ┌───────────────────┐
    │ Vendor Adapters   │             │ Generic Fallback  │
    │ vendor_*.go       │             │ openai/anthropic  │
    └───────────────────┘             └───────────────────┘
```

Vendor resolution order:

1. Explicit `vendor` field in provider config
2. Base URL detection
3. Generic fallback based on `api`

#### StreamEvent Types

```go
type StreamEvent struct {
    Type        EventType
    TextDelta   string      // Text delta
    ThinkingDelta string    // Thinking delta
    ToolCall    *ToolCall   // Tool call
    Usage       *Usage      // Token usage
    Error       error       // Error
}
```

### 2. Agent Loop

Agent is the core logic that coordinates Provider, Tools, and Session. All running
modes reuse the same Agent loop — the difference is only the input source (terminal,
HTTP, messaging, A2A, stdio) and output target.

```
┌─────────────────────────────────────────────────────────────┐
│                       Agent Loop                             │
├─────────────────────────────────────────────────────────────┤
│  1. Build system prompt (mode + tools + context files + skills)
│  2. Send messages to Provider                               │
│  3. Process stream events (text, thinking, tool calls)      │
│  4. Execute tools and collect results                       │
│  5. Add tool results to messages                            │
│  6. Repeat until complete                                   │
└─────────────────────────────────────────────────────────────┘
```

#### Execution Flow

```
User Input (TUI / CLI / HTTP / Messaging / A2A / ACP stdio / Desktop)
    │
    ▼
┌───────────────┐
│ Build Context │ ← System Prompt + Tools + Context Files + Skills
└───────┬───────┘
        │
        ▼
┌───────────────┐
│  Provider     │ ← LLM API (OpenAI/Anthropic)
│  Chat()       │
└───────┬───────┘
        │
        ▼
┌───────────────┐     ┌───────────────┐
│ Stream Events │────▶│ Tool Calls?   │
└───────┬───────┘     └───────┬───────┘
        │                     │
        │                     ▼
        │              ┌───────────────┐
        │              │ Execute Tools │
        │              └───────┬───────┘
        │                     │
        │                     ▼
        │              ┌───────────────┐
        └──────────────│ Append Results│
                       └───────────────┘
```

### 3. Multi-Agent Runtime

Multi-agent mode is opt-in with `--multi-agent`. When enabled, the main agent
gets the `subagent_spawn`, `subagent_status`, `subagent_send`, and
`subagent_destroy` tools. Child agents have isolated messages, context, session,
registry, and job manager state.

```
Main Agent
    │
    ├── agentruntime.NewAgentManager creates child agents
    ├── EventRouter routes events by AgentID
    └── subagent_* tools manage async child work
```

Child agents cannot create nested sub-agents because their registries filter out
the `subagent_*` tools.

### 4. A2A Protocol

The A2A (Agent-to-Agent) protocol enables different AI agents to discover,
communicate, and collaborate with each other.

```
┌───────────────────────────────────────────────────────────────────┐
│                     A2A Protocol Architecture                      │
├───────────────────────────────────────────────────────────────────┤
│                                                                   │
│  ┌──────────────────┐          ┌──────────────────┐              │
│  │   A2A Server     │          │   A2A Client     │              │
│  │  (mothx)    │ ◄──────► │  (any agent)     │              │
│  │                  │  JSON-RPC │                  │              │
│  │  /a2a            │  2.0     │  SendMessage()   │              │
│  │  /a2a/send       │  + SSE   │  GetTask()       │              │
│  │  /a2a/task       │          │  CancelTask()    │              │
│  │  /a2a/events     │          │  GetAgentCard()  │              │
│  └──────────────────┘          └──────────────────┘              │
│                                                                   │
│  Task lifecycle: submitted → working → completed/failed/canceled   │
│                                                                   │
│  Run with: mothx a2a start (default port 8093)                    │
│                                                                   │
└───────────────────────────────────────────────────────────────────┘
```

#### A2A Master Mode

A2A Master mode is enabled via `--enable-a2a-master`. It loads a remote agent
list from `a2a-list.json` and registers an `a2a_dispatch` tool for the LLM
to automatically dispatch tasks.

```
┌───────────────────────────────────────────────────────────────┐
│                   A2A Master Mode                              │
├───────────────────────────────────────────────────────────────┤
│                                                               │
│  a2a-list.json                                                │
│  ┌─────────────────────────────────────────┐                  │
│  │ agents:                                 │                  │
│  │   - name: code-reviewer                 │                  │
│  │     url: http://review:8093             │                  │
│  │   - name: ci-agent                      │                  │
│  │     url: http://ci:8093                 │                  │
│  └─────────────────────────────────────────┘                  │
│           │                                                   │
│           ▼                                                   │
│  ┌──────────────────┐                                         │
│  │   A2AManager     │ ← loads agent list                      │
│  └────────┬─────────┘                                         │
│           │                                                   │
│           ▼                                                   │
│  ┌──────────────────┐                                         │
│  │  a2a_dispatch    │ ← registered as LLM tool                │
│  │  (agent_name,    │                                         │
│  │   message)       │                                         │
│  └────────┬─────────┘                                         │
│           │                                                   │
│           ▼                                                   │
│  ┌──────────────────┐  ┌──────────────────┐                  │
│  │  code-reviewer   │  │  ci-agent        │                  │
│  │  http://review   │  │  http://ci       │                  │
│  │  :8093           │  │  :8093           │                  │
│  └──────────────────┘  └──────────────────┘                  │
│                                                               │
└───────────────────────────────────────────────────────────────┘
```

### 5. Serve Mode

`internal/serve/openaiapi/` implements the OpenAI-compatible HTTP API used by
serve mode.

```
┌─────────────────────────────────────────────────────────────┐
│                    Serve Architecture                       │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  OpenAI-compatible clients (curl, SDK, any tool)             │
│       │                                                     │
│       ▼                                                     │
│  ┌──────────────────────────────────────────┐               │
│  │  HTTP API (net/http)                   │               │
│  │  POST /v1/chat/completions               │               │
│  └──────────────────────────────────────────┘               │
│       │                                                     │
│       ▼                                                     │
│  ┌──────────────────────────────────────────┐               │
│  │  Agent Loop (shared)                     │               │
│  │  + Tools + Session + Sandbox + Skills     │               │
│  └──────────────────────────────────────────┘               │
│                                                             │
│  Config: serve.json (global ~/.mothx/ or project .mothx/)  │
│  Security: Bearer token + allowedWorkDirs + sandbox          │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### 6. Channels Messaging Serve

`internal/serve/channels/` implements the messaging channel runtime for WeChat,
Feishu, and WebSocket.

```
┌─────────────────────────────────────────────────────────────┐
│                    Channels Architecture                        │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐                  │
│  │  WeChat   │  │  Feishu   │  │ WebSocket │                  │
│  └─────┬────┘  └─────┬────┘  └─────┬────┘                  │
│        │             │             │                         │
│        └─────────────┼─────────────┘                         │
│                      ▼                                       │
│  ┌──────────────────────────────────────────┐               │
│  │  Channels Dispatcher                       │               │
│  │  (per-user session, yolo mode default)   │               │
│  └──────────────────────────────────────────┘               │
│       │                                                     │
│       ▼                                                     │
│  ┌──────────────────────────────────────────┐               │
│  │  Agent Loop (shared)                     │               │
│  │  + Tools + Session + Sandbox + Skills     │               │
│  └──────────────────────────────────────────┘               │
│                                                             │
│  Config: serve.json                                         │
│  Session: <sessionDir>/channels/<platform>/<user_id>/        │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### 7. Cron Scheduler

The `internal/cron` package provides a `sessions.db`-backed cron store and
scheduler. Jobs are stored in the `cron_jobs` table, bound to sessions, and can
execute through agent tools or remote A2A servers.

```
┌─────────────────────────────────────────────────────────────┐
│                    Cron Scheduler                             │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌──────────────────┐                                       │
│  │  CronStore       │ ← sessions.db / cron_jobs             │
│  │  (SQLiteCronStore)                                      │
│  └────────┬─────────┘                                       │
│           │                                                 │
│           ▼                                                 │
│  ┌──────────────────┐                                       │
│  │  Scheduler       │ ← periodic polling (default 30s)      │
│  └────────┬─────────┘                                       │
│           │                                                 │
│     ┌─────┴─────┐                                           │
│     ▼           ▼                                           │
│  ┌───────┐  ┌───────────┐                                   │
│  │SubAgent│  │A2A Server │                                   │
│  │(local) │  │(remote)   │  ← --a2a-target flag              │
│  └───────┘  └───────────┘                                   │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### 8. Tool System

Tools are the way Agent interacts with the external world. All running modes
share the same tool registry.

```
┌─────────────────────────────────────────────────────────────┐
│                    Tool Interface                            │
├─────────────────────────────────────────────────────────────┤
│  Name() string                                              │
│  Description() string                                       │
│  Parameters() json.RawMessage                               │
│  Execute(ctx, params) (*ToolResult, error)                  │
└─────────────────────────────────────────────────────────────┘
                              │
        ┌─────────────────────┼─────────────────────┐
        │                     │                     │
        ▼                     ▼                     ▼
┌───────────────┐   ┌───────────────┐   ┌───────────────┐
│  File Tools   │   │  Search Tools │   │  System Tools │
│  - read       │   │  - grep       │   │  - bash       │
│  - write      │   │  - find       │   │  - ls         │
│  - edit       │   │               │   │  - jobs       │
└───────────────┘   └───────────────┘   │  - kill       │
                                        └───────────────┘
┌───────────────┐   ┌───────────────┐   ┌───────────────┐
│  Planning     │   │  Skills       │   │  A2A Master   │
│  - plan       │   │  - skill_ref  │   │  - a2a_       │
│               │   │               │   │    dispatch   │
└───────────────┘   └───────────────┘   └───────────────┘
```

### 9. Session Management

Sessions use SQLite storage with tree structure and branching support.

```
┌─────────────────────────────────────────────────────────────┐
│                    Session Storage                          │
├─────────────────────────────────────────────────────────────┤
│  sessions.db                                                │
│  ├── sessions                                               │
│  │   ├── id                                                 │
│  │   ├── cwd                                                │
│  │   ├── timestamp                                          │
│  │   ├── parent_session                                     │
│  │   └── version                                            │
│  └── entries                                                │
│      ├── seq                                                │
│      ├── session_id                                         │
│      ├── id                                                 │
│      ├── type                                               │
│      ├── parent_id                                          │
│      ├── timestamp                                          │
│      └── data                                               │
└─────────────────────────────────────────────────────────────┘
```

#### Session Types

| type | Description |
|------|-------------|
| `session` | Session metadata |
| `message` | User/assistant messages |
| `model_change` | Model change record |
| `compaction` | Context compression record |
| `label` | Session label |

### 10. Sandbox System

Sandbox implements process isolation through bubblewrap (bwrap).

```
┌─────────────────────────────────────────────────────────────┐
│                     Sandbox Manager                          │
├─────────────────────────────────────────────────────────────┤
│  SetLevel(level)                                            │
│  GetActive() *Sandbox                                       │
│  Execute(cmd) (stdout, stderr, error)                       │
└─────────────────────────────────────────────────────────────┘
                              │
        ┌─────────────────────┼─────────────────────┐
        │                     │                     │
        ▼                     ▼                     ▼
┌───────────────┐   ┌───────────────┐   ┌───────────────┐
│  LevelNone    │   │ LevelStandard │   │  LevelStrict  │
│ (Unrestricted)│   │ (Project R/W) │   │ (Project R/O) │
└───────────────┘   └───────────────┘   └───────────────┘
```

### 11. TUI System

Terminal user interface based on Bubble Tea.

```
┌─────────────────────────────────────────────────────────────┐
│                        TUI App                              │
├─────────────────────────────────────────────────────────────┤
│  Startup Header                                             │
│  Vibe logo │ version │ provider/model │ cwd                 │
├─────────────────────────────────────────────────────────────┤
│  Native terminal scrollback                                 │
│  Completed transcript blocks are printed with Program.Println│
├─────────────────────────────────────────────────────────────┤
│  Managed live view                                          │
│  Live assistant/thinking stream, sticky plan todos, loading  │
│  indicator, prompt input, footer, and multi-agent tab bar    │
├─────────────────────────────────────────────────────────────┤
│  Tool details modal                                         │
│  Ctrl+O opens a scrollable full-screen details view          │
└─────────────────────────────────────────────────────────────┘
```

Key TUI behaviors:

- Completed messages are printed to the terminal's native scrollback so the terminal owns history scrolling and text selection.
- The managed Bubble Tea view focuses on live streaming content, multiline input, footer status, loading state, and active plan/todo context.
- The input editor supports multiline prompt composition: `Enter` submits, `Alt+Enter` / `Ctrl+J` insert newlines, and `Up` / `Down` move within the input before browsing history at boundaries.
- The default TUI transcript is a simple event view: each tool call is one concise line, routine lifecycle events are hidden, and a running tool is replaced by its completed summary. `Ctrl+G` switches to the full event view; `Ctrl+O` opens the latest tool/details modal with raw arguments, output, diffs, and activity timeline.
- In multi-agent mode, a tab bar shows active agents and their status when more than one agent exists.

## Configuration Files

| File | Location | Purpose |
|------|----------|---------|
| `settings.json` | `~/.mothx/` or `.mothx/` | Core settings (provider, model, mode, etc.) |
| `serve.json` | `~/.mothx/` or `.mothx/` | Unified serve configuration |
| `a2a.json` | `~/.mothx/` or `.mothx/` | A2A server configuration |
| `a2a-list.json` | `~/.mothx/` or `.mothx/` | A2A Master remote agent list |
| `mcp.json` | `~/.mothx/` or `.mothx/` | MCP server configuration |
| `memory.md` | project root or `~/.mothx/` | Persistent memory |
| `sessions.db` | session directory | Sessions, stats, events, and session-bound cron jobs |

## Data Flow

### Complete Request Flow

```
1. User input (from TUI / HTTP / Messaging / A2A / ACP stdio)
   │
   ▼
2. Input layer captures
   │
   ▼
3. Agent.Run(ctx, input)
   │
   ▼
4. Build system prompt
   ├── Mode prompt (plan/agent/yolo)
   ├── Tool definitions (JSON Schema)
   ├── Context files (AGENTS.md, CLAUDE.md)
   └── Skills context
   │
   ▼
5. Build message history
   ├── Historical messages (from Session)
   └── New user message
   │
   ▼
6. Provider.Chat(ctx, params)
   │
   ▼
7. SSE streaming response
   ├── TextDelta → Display text
   ├── ThinkingDelta → Display thinking
   └── ToolCall → Execute tool (incl. a2a_dispatch)
   │
   ▼
8. Tool execution (via Sandbox)
   │
   ▼
9. Collect tool results
   │
   ▼
10. Continue conversation (back to step 5)
   │
   ▼
11. Complete, save session
```

## Key Design Decisions

### 1. Interface Abstraction

Use interface abstraction for Provider and Tool, facilitating extension and testing.

### 2. Stream Processing

Use Channel for streaming responses, providing real-time feedback.

### 3. Session Tree

Use tree structure for session storage, supporting branching and restoration.

### 4. Layered Configuration

Support global and project configuration, with project configuration overriding global.

### 5. Sandbox Isolation

Implement process-level isolation through bubblewrap, protecting system security.

### 6. Public SDK Package

The `agent/` package exposes public Go types (`Agent`, `Provider`, `Builder`) so
external applications can embed an Agent without depending on internal packages.
`bootstrap/` wires those public interfaces to the internal Runtime. See
[SDK Integration Guide](sdk.md) for usage details.

### 7. Shared Agent Runtime and Agent Loop

All running modes (TUI, CLI, WebUI/Serve, Channels, A2A, ACP, Desktop) reuse the
same Agent Runtime and Agent loop. Adapters differ only in protocol decoding and
event projection. Resource ownership, run lifecycle, decisions, and policy live
in the Runtime so behavior cannot drift between entry points.
