# Agent Client Protocol (ACP)

## Overview

MothX implements the **Agent Client Protocol (ACP)**, a JSON-RPC based protocol that allows AI coding agents to be integrated directly into IDEs and editors. ACP enables a client-server architecture where your IDE acts as the client and MothX runs as a background agent.

## Supported IDEs

### Visual Studio Code (VS Code)

MothX can be used as an ACP agent in VS Code through compatible extensions. To configure, add the following to your VS Code settings:

```json
{
  "your-acp-extension.servers": {
    "mothx": {
      "type": "agent",
      "command": "mothx",
      "args": ["acp"],
      "env": {}
    }
  }
}
```

Make sure `mothx` is available in your `PATH`, or use the full path to the binary.

### JetBrains IDEs (IntelliJ IDEA, GoLand, WebStorm, etc.)

JetBrains IDEs also support ACP agents. Configure in your IDE settings:

```json
{
  "acpAgentServers": {
    "mothx": {
      "command": "mothx",
      "args": ["acp"]
    }
  }
}
```

## Using ACP Mode

### Starting the ACP Server

```bash
# Basic ACP server (stdin/stdout)
mothx acp

# With specific provider and model
mothx acp --provider deepseek-openai --model deepseek-v4-flash

# With sandbox enabled
mothx acp --sandbox

# Specify mode
mothx acp --mode agent

# Enable multi-agent tools
mothx acp --multi-agent
```

### ACP Command Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--provider` | `-p` | Default from config | LLM provider |
| `--model` | `-m` | Default from config | Model ID |
| `--mode` | `-M` | `yolo` | Run mode (plan, agent, yolo, os); falls back to `settings.json` `defaultMode` |
| `--thinking` | `-t` | From config | Thinking level |
| `--sandbox` | - | false | Enable sandbox |
| `--verbose` | - | false | Verbose output |
| `--debug` | - | false | Debug logging and local pprof |
| `--multi-agent` | - | false | Enable sub-agent tools and multi-agent workflows |
| `--workflows` | - | false | Enable JavaScript workflow tools |

## Protocol Details

ACP uses JSON-RPC 2.0 over stdio and follows the ACP v1 schema. MothX-specific behavior is isolated in `mothx/*` and `_mothx/*` methods plus `_meta` extension declarations; standard ACP objects receive no non-extension custom fields.

### Methods

| Method | Description |
|--------|-------------|
| `initialize` | Handshake and capability negotiation |
| `mothx/doctor` | Return machine-readable installation/provider diagnostics; does not require a session |
| `session/new` | Create a new session |
| `session/load` | Load an existing session |
| `session/list` | List persisted sessions, optionally filtered by working directory |
| `session/resume` | Restore a session without replaying history |
| `session/prompt` | Send a prompt to the agent |
| `session/cancel` | Cancel an active prompt |
| `session/close` | Cancel active work and release session runtime resources |
| `session/delete` | Delete an inactive session from persisted session history |
| `$/cancel_request` | Cancel an in-flight request by JSON-RPC request ID |
| `session/update` | Server notification: session state changes |

### Capabilities

MothX advertises the following ACP capabilities during initialization:

- **Load Session**: Load and continue previous sessions
- **Prompt Capabilities**: Text and `resource_link`; image, audio, and embedded resource inputs are not advertised
- **Session Capabilities**: Load, list, resume, close, and delete sessions
- **MCP Capabilities**: stdio / http / sse transport supported
- **Multi-Agent Workflows**: Available when the ACP server is started with `--multi-agent`

### Notifications

The server sends `session/update` notifications with the following event types:

| Update Type | Description |
|-------------|-------------|
| `agent_message_chunk` | text delta from the agent |
| `agent_thought_chunk` | thinking/reasoning delta |
| `user_message_chunk` | historical user message |
| `tool_call` | tool being called |
| `tool_call_update` | tool status update (pending/in_progress/completed/failed) |
| `usage_update` | Current context usage, context window size, and cumulative cost; plus the additive cache projection below |

`usage_update` keeps the standard ACP shape. When `initialize._meta["mothx.dev"].features` advertises `usageCacheProjection`, the notification additionally carries `_meta["mothx.dev"]` with the session-cumulative `cacheRead`, `cacheWrite`, and `totalInputTokens`; the cache hit ratio is `cacheRead / totalInputTokens`, the same denominator the Runtime uses for `Usage.TotalInputTokens`. Both the cumulative cost and these totals are rebuilt from the canonical persisted history on `session/load`, and the extension is omitted until the first usage event.

`session/load` replays the full conversation history before returning.

## MothX Extensions

MothX keeps non-standard interactions separate from ACP. Clients use these methods only after the matching capability appears in `agentCapabilities._meta["mothx.dev"]`; ACP clients that do not support the extension can ignore it without affecting the standard session flow.

`initialize.agentInfo` identifies the product as `mothx` / `MothX` and carries the build version. The top-level `initialize._meta["mothx.dev"]` (also mirrored in `agentCapabilities._meta` for compatibility) advertises the stable feature list and `doctor: true`. `mothx/doctor` accepts `{}` or `{ "cwd": "/absolute/path" }` and returns `{ ok, version, summary, checks }`; checks use stable IDs and never include API key values. `mothx doctor --json` emits that same response as its only stdout output. If ACP exits before initialization, stderr includes one `MOTHX_ACP_ERROR { ... }` line with a stable error code and a short fix.

| Method | Purpose |
|------|------|
| `_mothx/request_question` | Multiple-choice question request; clients return `{ "answer": "option text" }` |
| `_mothx/session_event` | MothX runtime status, turn, and context-compaction events |

## MCP Server Integration

MothX supports connecting to **MCP (Model Context Protocol)** servers during ACP sessions. This allows the agent to access external tools and data sources.

ACP sessions use the same MCP connection and tool-registration runtime as normal CLI/TUI sessions. The difference is that ACP clients pass `mcpServers` during session creation/loading, while normal CLI/TUI sessions load `mcp.json` at process startup.

### Configuring MCP Servers

MCP servers are configured by the IDE client and passed to MothX when creating or loading sessions. The configuration format:

```json
{
  "mcpServers": [
    {
      "name": "my-database",
      "type": "stdio",
      "command": "mcp-server",
      "args": ["--port", "8080"],
      "env": [
        {"name": "DB_URL", "value": "postgres://localhost/mydb"}
      ]
    },
    {
      "name": "remote-tools",
      "type": "http",
      "url": "https://mcp.example.com",
      "headers": [
        {"name": "Authorization", "value": "Bearer ${TOKEN}"}
      ]
    },
    {
      "name": "legacy-sse",
      "type": "sse",
      "url": "https://legacy.example.com/sse",
      "messageUrl": "https://legacy.example.com/messages"
    }
  ]
}
```

### MCP Tool Registration

When an MCP server is connected, MothX automatically discovers and registers all tools exposed by the server. The tools are registered with the naming convention `mcp_<server_name>_<tool_name>`, allowing the agent to use them alongside built-in tools.

Registration happens before the agent freezes its system prompt and tool definitions for the session. MCP server changes therefore require creating/loading a new ACP session with the updated `mcpServers` payload.

In addition to `tools/*`, MothX now also discovers:

- `resources/*`: exposed as MCP resource read tools
- `prompts/*`: exposed as MCP prompt rendering tools

### MCP Transport Support

Supported transports:

- `stdio`: `command` may be an executable name resolved through `PATH`, or a direct path; use `args` for command arguments
- `http`: streamable HTTP endpoint via `url`
- `sse`: legacy SSE stream via `url` plus `messageUrl` for client POSTs

Additional notes:

- MCP server names must be unique within one session
- `headers` can be passed for `http` / `sse` transports
- `sampling/createMessage` is bridged to the current ACP provider/model and returns assistant text content
- MCP progress/logging/cancel notifications are surfaced as structured ACP `tool_call_update` events

## Permission System

In ACP mode, the agent can request user permissions for tool execution. The IDE client receives `session/request_permission` notifications and can respond with allow/reject decisions.

```
Client                                    Server (mothx acp)
  │                                           │
  │  ── session/request_permission ────────▶  │
  │      (tool_call_id, title, options)       │
  │                                           │
  │  ◀── JSON-RPC response ─────────────────  │
  │      (outcome: allow-once / reject-once)  │
```

## Example: VS Code Integration

### Step 1: Install MothX

```bash
npm install -g mothx-installer
# or
go install github.com/oschina/mothx/cmd/mothx@latest
```

### Step 2: Configure VS Code

Add to your VS Code `settings.json`:

```json
{
  "acp.agents": {
    "mothx": {
      "command": "mothx",
      "args": ["acp", "--mode", "agent"],
      "description": "MothX AI Assistant"
    }
  }
}
```

### Step 3: Start using

Open your project in VS Code, launch the ACP agent, and start asking questions or requesting code changes directly from your editor.

## Example: JetBrains IDE Integration

### Step 1: Install MothX

```bash
npm install -g mothx-installer
```

### Step 2: Configure in JetBrains IDE

Navigate to `Settings → Tools → ACP Agents` and add a new agent:

- **Name**: MothX
- **Command**: `mothx`
- **Arguments**: `acp --mode agent`

Or add to `.idea/workspace.xml`:

```xml
<component name="ACPSettings">
  <option name="agents">
    <list>
      <ACPAgentSetting>
        <option name="name" value="Vibecoding" />
        <option name="command" value="mothx" />
        <option name="args" value="acp --mode agent" />
      </ACPAgentSetting>
    </list>
  </option>
</component>
```

### Step 3: Start using

Use the ACP tool window in your JetBrains IDE to interact with MothX.
