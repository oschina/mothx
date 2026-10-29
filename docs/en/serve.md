# Serve Mode

`mothx serve` is the only server entry point. It starts the unified runtime for:

- OpenAI-compatible `/v1/chat/completions` API
- Durable OpenAI Responses background-run management for providers configured with `api: "openai-responses"` and `responses.background: true`
- Web UI management panel (chat interface, session management, settings editor)
- WeChat, Feishu, WebSocket messaging channels
- Cron scheduled tasks, Memory persistent memory, Hooks
- Stats usage statistics dashboard
- Multi-Agent sub-agent support

```bash
# Start Serve (default 127.0.0.1:7872)
mothx serve

# Specify port and work directory
mothx serve --port 8080 --work-dir /path/to/project

# Bind to external interface (allow access from other machines)
mothx serve --port 0.0.0.0:8080 --work-dir /path/to/project

# Disable auth and bind to all interfaces for a trusted network
mothx serve --unsafe --work-dir /path/to/project

# Initialize configuration files
mothx serve init-config global   # generates ~/.mothx/serve.json
mothx serve init-config project  # generates .mothx/serve.json
`--unsafe` is an explicit high-risk mode: it disables authentication and changes the current process to listen on all interfaces. Do not use it on an untrusted network; prefer loopback plus Bearer-token authentication.

`/v1/chat/completions` accepts standard OpenAI Chat Completions fields and returns standard JSON/SSE data frames. VibeCoding-specific `x_*` request and response fields are not supported. WebUI session selection, runtime capabilities, skills, approvals, and run events use the structured `/api/...` endpoints and WebSocket streams instead.

## Configuration

Configuration lives in `serve.json`:

- Global: `~/.mothx/serve.json`
- Project: `.mothx/serve.json`
- Custom path: `mothx serve --config /path/to/serve.json`

The project config overlays the global config.

### Core Configuration Fields

```json
{
  "api": {
    "listen": "127.0.0.1:7872",
    "auth": {
      "enabled": true,
      "tokens": ["your-secret-token"]
    },
    "defaultWorkDir": "/path/to/project",
  },
  "features": {
    "multiAgent": true,
    "workflows": true
  },
  "artifact": false,
  "sandbox": {
    "enabled": false
  },
  "channels": {
    "artifact": false,
    "wechat": { "enabled": false },
    "feishu": { "enabled": false }
  },
  "webUI": {
    "enabled": true
  },
  "cron": {
    "enabled": true
  },
  "memory": {
    "path": ".mothx/memory.md"
  },
  "allowedWorkDirs": ["/path/to/project"],
  "agent": {
    "mode": "yolo"
  }
}
```

Artifact publishing is disabled by default. Top-level `artifact` controls WebUI/API sessions; `channels.artifact` independently controls WeChat and Feishu sessions.

### Configuration Hot-Reload

After saving settings via the Web UI, Serve automatically hot-reloads provider/model configuration without requiring a restart.

## Network Access

To allow access from other machines, bind Serve to an external interface with `--port 0.0.0.0:8080` or set `"listen": "0.0.0.0:8080"` in `serve.json`. Enable Bearer token auth before exposing Serve beyond loopback. For trusted local networks only, `--unsafe` disables auth and binds loopback/default listens to `0.0.0.0` for the current process.

When `api.auth.enabled` is true, the Web UI displays a login page. Enter any value from `api.auth.tokens` as its password. A successful login stores an HttpOnly, SameSite-Strict browser cookie; API clients can continue using `Authorization: Bearer <token>` unchanged.

## Security

Security is controlled by three independent layers:

1. **Bearer Token Auth**: `api.token` or `security.token`
2. **Work Directory Whitelist**: `api.allowedWorkDirs`
3. **Sandbox Isolation**: `sandbox.enabled` (bwrap)

## Web UI

Access `http://127.0.0.1:7872` to open the Web UI. When authentication is enabled, first sign in with an `api.auth.tokens` token. The Web UI provides:

- **Chat Interface**: SSE streaming output, tool call/result rendering, plan cards, and a session runtime menu for `plan`, `agent`, and `yolo` modes. Submissions preserve persisted conversation history; optional images, session tool toggles, skills, and explicit mode can be sent with a run. Runtime mode and capability changes apply immediately from the server's authoritative response, while session-list refreshes remain best-effort. The composer reflects server-side queued/running states after reconnect.
- **Dynamic Workbench**: `/chat` supports session tabs, up to four split panes, menu/drag splitting, persisted layout, and a single-pane mobile projection. Every pane owns its composer, model selection, draft, approvals, and trajectory view. Panes render the canonical `sessionRunStates` projection directly, and an all-tabs overflow menu keeps every open session reachable. Use `?workbench=0` or the "Dynamic workbench layout" setting to return to the single-session layout; events are always consumed by the same `session-stream` dispatcher.
- **Interactive Terminal**: when `/api/status` reports `features.terminal`, the workbench toolbar can open an interactive PTY for the focused session's cwd (xterm.js over WebSocket). The terminal is a local shell with the serve process user's privileges, is not protected by the sandbox, and inherits the full serve process environment; it creates no Run, writes no transcript, and takes no Runtime lease. It is available on a loopback listener, or on a non-loopback listener when auth is enabled with a token; an unauthenticated non-loopback listener returns `terminal_unsafe`.
- **Approval Center**: Review pending tool approvals, approve once or deny, persist command/path allow rules, and inspect the session approval audit history.
- **Session Management**: Pagination, keyboard shortcuts, historical sessions, runtime snapshots, capability toggles, and reconnect-safe approval state. Historical sessions are sorted by last-used time with the most recently replied-to session at the top; the session-management page shows each session's last reply time. Active runs remain protected after a page refresh until they reach a terminal state. `GET /api/sessions` is bounded: without `limit` it returns the first 100 sessions plus `total`, `limit`, `offset`, and `hasMore`, and `scope=active`/`/api/sessions/active` project only the in-memory session pool instead of loading all history.
- **Responses Background Runs**: When an `openai-responses` provider enables `responses.background`, Serve submits supported requests as remote background tasks, persists their response lineage and archived output, polls them through completion, resumes recoverable runs after restart, and executes local function/custom-tool calls. The authenticated run API supports `GET /api/responses/runs/{localRunID}?session_id={sessionID}` plus `cancel`, `reconnect`, and `abandon` actions; see [Configuration](configuration.md#responses-field).
- **Settings Editor**: Provider/Model configuration, Defaults, Web Search, MCP, Context Files, Compaction, Sandbox, Retry, Approval, Provider Config. Provider, app, and Serve configuration changes refresh related status and tool availability without restarting the server. MCP settings edit the global `mcp.json`; the chat toolbar exposes project-level MCP configuration for the active session.
- **DeepSeek Web Search fallback**: when enabled, the shared Runtime registers a local `web_search` tool backed by DeepSeek's official Search API or Tencent Yuanbao SearchPro (selectable with `webSearch.backend`) for providers without native hosted search. The DeepSeek backend also registers `web_fetch`. OpenAI Responses keeps its provider-native hosted web search, and Anthropic Messages uses its provider-native hosted search while `webSearch` is enabled; both take precedence over the local `web_search` function.
- **Channel Management**: WeChat QR login and Feishu configuration
- **WebSocket streams**: `/ws/runs` and `/ws/logs` are Serve event streams used by the Web UI; they are not a separate messaging channel
- **Settings**: Memory, WorkDir, Logs, SkillHub, environment, and MCP management are available in addition to provider and agent settings
- **Serve Config**: Features, API, Cron, Memory, Security, Agent, Hooks, Channels, Lobster Mode

### Terminal configuration

The optional `terminal` section of `serve.json` controls the interactive terminal; omitting it keeps the automatic policy (loopback enabled; non-loopback enabled only with configured auth tokens):

```json
{
  "terminal": {
    "enabled": null,
    "shell": "",
    "maxSessions": 8,
    "maxPerSession": 2,
    "scrollbackBytes": 1048576,
    "maxOutputBytesPerSec": 1048576,
    "allowedOrigins": []
  }
}
```

An empty `allowedOrigins` means strict same-origin; reverse proxies or custom front ends must list their origin explicitly. The terminal status bar continuously shows that the shell is outside the sandbox and inherits the serve process user/environment, warns that it shares the focused session's cwd with a possibly running Agent (with a stronger notice when the canonical execution snapshot is local or in another process), surfaces the placeholder-auth-token warning reported by `/api/status`, and reports server-side output backpressure instead of treating it as a disconnect. A page reload reattaches the detached shell recorded for that tab instead of leaking it and creating a second terminal; once the shell exits or the id is lost, Reconnect creates a fresh terminal. Browser cookie sessions receive no `terminalToken` in the create response; only auth-disabled or Bearer-authenticated clients get one for non-browser WebSocket attach. Serve logs terminal lifecycle metadata only (`terminal_created`/`terminal_exited`/`terminal_killed` with id, session, cwd, and exit code), never input or output. Disabling the terminal closes existing shells and rejects new WebSocket attaches. On Linux the shell carries `Pdeathsig`, so it is terminated if the serve process dies; on Windows the backend uses ConPTY (Windows 10 1809+ or Windows 11) and older systems report `terminal_unsupported`. The terminal shares the serve auth token: holding the token is equivalent to owning that user's shell. That is the access boundary, not a UI toggle; use a separate OS user or container when stronger isolation is required instead of trimming terminal environment variables or relying on the sandbox.

### Screenshots

**Chat Interface** — SSE streaming, tool call/result rendering, plan cards, and mode menu:

![Web UI Chat](assets/image/webui-chat.webp)

**Session Management** — paginated history, runtime snapshots, capability toggles:

![Web UI Sessions](assets/image/webui-sessions.webp)

**Settings Editor** — Provider/Model, Defaults, Web Search, Compaction, Sandbox, Approval:

![Web UI Settings](assets/image/webui-settings.webp)

**Skills** — browse and load project/global skills:

![Web UI Skills](assets/image/webui-skills.webp)

**Cron** — scheduled task management:

![Web UI Cron](assets/image/webui-cron.webp)

The list shows user-authored tasks only. One Runtime-owned maintenance job (`mothx-maintenance:artifact-storage`, daily) runs in the same scheduler to reclaim unreferenced attachment storage; it is hidden from this view and from name lookup, and is re-created whenever the scheduler starts. See [`mothx pure`](cli-reference.md#pure---archive-the-sessions-database).


## Messaging Channels

### WeChat

- QR code login support (scan-to-authenticate)
- Login status polling with error handling
- API endpoint: `/api/channels/wechat/login`

### Feishu

- Supports appId/appSecret/workspace/allowedUsers configuration
- Automatic message routing and session persistence

### WebSocket event streams

The Web UI uses authenticated `/ws/runs` and `/ws/logs` streams for run events and logs. These endpoints are not configured as a standalone messaging channel.

## Stats Dashboard

The Serve Web UI exposes usage statistics at `/stats` (for example, `http://127.0.0.1:7872/stats`). The standalone `mothx stats` command is separate and defaults to `127.0.0.1:7878`.

![Web UI Stats](assets/image/webui-stats.webp)

## CLI Flags

| Flag | Description | Default |
|------|-------------|--------|
| `--port` | Listen address (host:port) | `127.0.0.1:7872` |
| `--work-dir` | Working directory | Current directory |
| `--config` | Configuration file path | `~/.mothx/serve.json` or `.mothx/serve.json` |
| `--web-ui-dir` | Web UI static assets directory | Built-in path |
| `--unsafe` | Disable auth and bind Serve to all interfaces | Disabled |
| `--debug` | Enable pprof profiling server | Disabled |
