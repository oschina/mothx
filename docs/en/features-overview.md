# ✨ MothX Core Features Explained

> One tool to meet all your AI coding needs.

---

## 🤖 Multi-Provider Support

### Supported Providers

| Provider | Models | API Format |
|----------|--------|------------|
| **DeepSeek** (default) | deepseek-v4-flash, deepseek-v4-pro | OpenAI Chat / Anthropic Messages |
| **OpenAI** | GPT-4o, o1, etc. | OpenAI Chat |
| **Anthropic** | Claude Sonnet, Opus, etc. | Anthropic Messages |
| **LongCat** | LongCat-2.0 (1M context, 128K output) | OpenAI Chat / Anthropic Messages |
| **Vendor Adapters** | Google Gemini, Google Vertex, Xiaomi, Volcengine/Doubao, Kimi, MiniMax, Seed, Qianfan, Bailian, Gitee, StepFun, OpenRouter, Together, Groq, Fireworks, Mistral, GitHub Copilot, Cloudflare AI Gateway, Cloudflare Workers AI, Amazon Bedrock, Z.AI, LongCat, AMD Radeon, and more | OpenAI Chat or Anthropic Messages |
| **Custom** | Any compatible model | Generic OpenAI Chat or Anthropic Messages fallback |

### Quick Switch

```bash
# Use DeepSeek
mothx --provider deepseek-openai --model deepseek-v4-flash

# Use OpenAI
mothx --provider openai --model gpt-4o

# Use Anthropic
mothx --provider anthropic --model claude-3-5-sonnet-20241022
```

### Vendor Adapters

MothX supports 25+ vendor adapters, including:

- **China**: Xiaomi, Volcengine/Doubao, Kimi, MiniMax, Seed, Qianfan, Bailian, Gitee, StepFun, Z.AI, LongCat, AMD Radeon
- **International**: Google Gemini, Google Vertex, OpenRouter, Together, Groq, Fireworks, Mistral, GitHub Copilot, Cloudflare AI Gateway, Cloudflare Workers AI, Amazon Bedrock

No additional configuration needed — just set the API key and start using.

---

## ⚡ Lightning Fast

### SSE Streaming

MothX uses SSE (Server-Sent Events) streaming to return tokens in real-time, no need to wait for complete responses.

### Cache Hit Optimization

- **Real-time cache hit rate display**: Shows cache hit percentage in the TUI footer
- **Per-turn cache statistics**: Detailed cache hit info for each conversation turn
- **Unified cache metrics**: Consistent statistics across TUI and print modes

### Performance Comparison

| Feature | MothX | Other Tools |
|---------|-----------|-------------|
| Streaming | ✅ SSE | Partial support |
| Cache hit rate | ✅ Real-time display | ❌ Not displayed |
| Token statistics | ✅ Unified metrics | ❌ Inconsistent |

---

## 🧠 Think Mode

### Supported Models

- **DeepSeek**: deepseek-v4-pro (supports thinking mode)
- **OpenAI**: o1 series (supports thinking mode)
- **Anthropic**: Claude series (supports thinking mode)

### Thinking Levels

```bash
# Set thinking level
mothx --thinking off      # No thinking
mothx --thinking minimal  # Minimal thinking
mothx --thinking low      # Low thinking
mothx --thinking medium   # Medium thinking
mothx --thinking high     # High thinking
mothx --thinking xhigh    # Ultra high thinking
```

### Use Cases

- **Complex problems**: Use `high` or `xhigh` for deeper reasoning
- **Simple problems**: Use `off` or `minimal` for faster responses
- **Debugging code**: Use `medium` for balanced speed and quality

---

## 🛡️ Sandboxed

### bwrap Process Isolation

MothX uses [bubblewrap](https://github.com/containers/bubblewrap) for process-level isolation, ensuring secure execution.

### Three Modes

![Three Safety Modes](assets/image/modes-en.webp)

| Mode | File System | Network | Use Case |
|------|------------|---------|----------|
| **Plan** | Read-only | ❌ | Analysis, planning |
| **Agent** | Read/Write | ❌ | Daily development |
| **YOLO** | Full | ✅ | System-level operations |

### Security Features

- **bashBlacklist priority**: Blacklisted commands (like `rm -rf`, `sudo`) are always blocked
- **Approval gates**: In Agent mode, bash commands require approval
- **`--print` fast fail**: Exits immediately when approval is needed, won't hang

### Installing bwrap

```bash
# Debian/Ubuntu
sudo apt install bubblewrap

# Fedora
sudo dnf install bubblewrap

# Arch
sudo pacman -S bubblewrap
```

---

## 📝 Session Management

### SQLite Persistence

MothX uses SQLite-backed storage for persistent session history. Features include:

- **Interactive picker**: `/sessions` opens a dialog with Up/Down navigation, Enter to switch, `n` for new, `d` for delete
- **Lazy creation**: TUI startup defers session creation until the first user message
- **Branching**: Create new branches from any node
- **Compaction**: Automatically compact old sessions to save space
- **Tree structure**: Visualize session trees

### Session Commands

```bash
# Continue most recent session
mothx --continue

# Resume specific session
mothx --resume <session-id>

# Use specific session handle file
mothx --session <session-file.db>
```

### TUI Session Picker

In the TUI, use `/sessions` to open the interactive session picker:

- **Up/Down**: Navigate sessions
- **Enter**: Switch to selected session
- **n**: Start a new session
- **d**: Delete selected session
- **Esc**: Close picker

Text commands are also available:
```bash
/sessions ls        # List sessions
/sessions set <id>  # Switch to session
/sessions clear     # Start fresh
/sessions del <id>  # Delete session
```

### Session Storage Location

| Platform | Location |
|----------|----------|
| Linux/macOS | `~/.mothx/sessions/` |
| Windows | `%APPDATA%\mothx\sessions\` |

---

## 🧩 Skills System

### What Are Skills?

Skills are reusable prompt snippets for:

- **Project conventions**: Coding standards, naming rules, architecture patterns
- **Team sharing**: Unify team development style
- **Quick onboarding**: New members quickly understand project conventions

### Creating Skills

```bash
# Global skills (available for all projects)
~/.mothx/skills/<name>/SKILL.md

# Project skills (override global)
.skills/<name>/SKILL.md
```

### Using Skills

```bash
# List available skills
> /skills

# Activate a skill
> /skill my-conventions

# Load skill reference
> /skill_ref my-references
```

### Online Skill Marketplace

MothX is compatible with SkillHub / ClawHub, supporting:

- **Skill installation**: One-click install online skills
- **Cron infrastructure**: Scheduled skill execution
- **Community sharing**: Share your skills

---

## 📊 Stats Dashboard

### Usage Statistics

MothX includes a built-in stats dashboard for tracking token usage, requests, and costs.

```bash
# Start web dashboard (default 127.0.0.1:7878)
mothx stats

# Print stats in terminal
mothx stats --cli

# Use alternate database
mothx stats --db ~/.mothx/sessions/sessions.db
```

### Features

- **Pure HTML/CSS/JS**: No external dependencies, charts drawn on `<canvas>`
- **Overall summary**: Requests, tokens, cost, duration
- **Time-series charts**: Visualize usage over time
- **Per-provider/model breakdowns**: See which providers and models you use most
- **Protocol + Vendor split**: Separate vendor (company) from protocol (API format)
- **Filtering**: By time range (today/week/month/all), vendor, and protocol
- **Recent requests table**: Paginated list of recent API calls

### CLI Mode

```bash
# Print stats directly in terminal
mothx stats --cli
```

Shows:
- Total tokens, requests, cost, duration
- Per-provider breakdown
- Per-model breakdown
- 10 most recent requests

---

## 💻 IDE Integration

### ACP Protocol

MothX supports Agent Client Protocol (ACP), integrating with:

- **VS Code**
- **Zed**
- **JetBrains IDEs** (IntelliJ IDEA, WebStorm, etc.)

### VS Code Integration

Add to `settings.json`:

```json
{
  "acp.agents": {
    "mothx": {
      "command": "mothx",
      "args": ["acp", "--mode", "agent", "--multi-agent"]
    }
  }
}
```

### JetBrains Integration

Navigate to `Settings → Tools → ACP Agents` and add:

- **Name**: MothX
- **Command**: `mothx`
- **Arguments**: `acp --mode agent`

---

## 🌐 Serve Mode

### OpenAI-Compatible HTTP API

MothX can be used as an OpenAI-compatible HTTP server:

```bash
# Start serve
mothx serve

# Configuration files
~/.mothx/serve.json
.mothx/serve.json
```

### Use Cases

- **Team sharing**: Team members share AI coding assistant
- **CI/CD integration**: Use AI in CI/CD pipelines
- **API service**: Provide AI coding API service

---

## 📱 Messaging Platforms

### WeChat/Feishu/WebSocket

MothX can be deployed as a chatbot:

```bash
# Start messaging channels
mothx serve

# Configuration files
~/.mothx/serve.json
.mothx/serve.json
```

### Features

- **Multi-user support**: Independent sessions per user
- **Persistent sessions**: Session history automatically saved
- **Progress callbacks**: Real-time tool execution progress
- **Thinking display**: Show AI's thinking process
- **WeChat QR Login**: Scan-to-authenticate with login status polling
- **Configuration Hot-Reload**: Auto-reload provider/model config after saving via Web UI

---

## 🤝 Multi-Agent

### Sub-Agent Delegation

```bash
# Enable multi-agent mode
mothx --multi-agent

# Sub-agent tools
subagent_spawn    # Create sub-agent
subagent_status   # Check sub-agent status
subagent_send     # Send message to sub-agent
subagent_destroy  # Destroy sub-agent
```

### Delegate Mode

```bash
# Enable one blocking delegated sub-agent
mothx --delegate

# Toggle in TUI or Serve sessions
/delegate on
/delegate status
/delegate off
```

`--delegate` registers `delegate_subagent`, a synchronous tool for one bounded investigation at a time. Use it when the main agent only needs a summarized result from a focused multi-step task.

### A2A Protocol

```bash
# Enable A2A Master mode
mothx --enable-a2a-master

# Generate sample config
mothx --init-a2a-master-config
```

### Dynamic Workflows

```bash
# Enable Workflow mode
mothx --workflows

# Workflow tools
workflow_run      # Run an JavaScript workflow
workflow_status   # Check workflow status
workflow_cancel   # Cancel a workflow run
```

Orchestrate multiple worker agents using an JavaScript DSL, with phase grouping, parallel execution, concurrency control, result fan-in, and run logs. Ideal for code audits, architecture research, multi-role reviews, generate-critic loops, and other complex multi-agent scenarios.

See the [Workflow Mode](workflow.md) documentation for full syntax and best practices.

### Use Cases

- **Large tasks**: Split large tasks into multiple subtasks
- **Parallel execution**: Multiple sub-agents working in parallel
- **Remote collaboration**: Manage remote agents via A2A protocol

---

## 🎨 Rich TUI

### Features

- **Markdown rendering**: Real-time Markdown content rendering
- **Syntax highlighting**: Code block syntax highlighting
- **Thinking display**: Show AI's thinking process in full event mode or the details modal
- **Tool modals**: View tool execution details (Ctrl+O)
- **Event display modes**: The default simple view keeps one-line tool summaries and hides routine lifecycle noise; Ctrl+G switches to the full event view (Ctrl+O always opens raw tool details)
- **Multiline input**: Alt+Enter/Ctrl+J for newlines, Up/Down for history at boundaries
- **Status bar**: Show cache hit rate, token statistics, context usage, elapsed time
- **Sticky todo list**: Active plan steps remain visible while streaming
- **Native scrollback**: Completed transcript blocks print to terminal scrollback

### Keyboard Shortcuts

| Shortcut | Function |
|----------|----------|
| `Enter` | Submit prompt |
| `Alt+Enter` / `Ctrl+J` | Insert newline |
| `Tab` | Cycle mode (plan → agent → yolo) |
| `Esc` | Abort current operation, approval, or question |
| `Ctrl+O` | Open/close tool details modal |
| `Ctrl+G` | Toggle simple/full event display |
| `Up` / `Down` | Move in input; browse history at boundaries; scroll tool modal |
| `PgUp` / `PgDn` | Page through tool modal |
| `Home` / `End` | Start/end of input line; top/bottom of tool modal |

### Slash Commands

| Command | Description |
|---------|-------------|
| `/systeminit [guidance]` | Generate or refresh project `AGENTS.md` |
| `/reload` | Restart with fresh session (TUI only) |
| `/btw <question>` | Ask a side question without interrupting main task |
| `/alloweditpath [add\|remove\|clear] <glob>` | Manage auto-edit path whitelist |
| `/allowautoedit [on\|off] [global]` | Toggle full auto-edit in agent mode |

---

## 🔒 Security Control

### Approval Mechanism

```json
{
  "approval": {
    "bashWhitelist": ["go ", "make ", "git ", "npm "],
    "bashBlacklist": ["rm -rf", "sudo"],
    "confirmBeforeWrite": true
  }
}
```

### Project-Level Bash Auto-Approval

`allow.json` supports project-level bash auto-approval rules:

```json
{
  "bashCommands": ["make test"],
  "bashPrefixes": ["go test ", "go build "]
}
```

- `bashCommands`: Exact command match
- `bashPrefixes`: Prefix match (trailing spaces are significant)
- Settings-level `bashBlacklist` takes precedence over project allow rules

### Interactive Approval Dialog

When approval is required, the TUI shows a dialog with:

- **Approve Once**: Run this time only
- **Deny**: Reject the command
- **Always Allow Exact Command**: Persist to `.mothx/allow.json`
- **Always Allow Command Prefix**: Persist prefix to `.mothx/allow.json`

### Security Features

- **bashBlacklist priority**: Blacklisted commands are always blocked
- **YOLO mode safety**: Even in YOLO mode, blacklist still applies
- **`--print` fast fail**: Exits immediately when approval is needed
- **Project allow rules**: Per-project bash auto-approval via `.mothx/allow.json`

---

## 📊 Unified Cache Metrics

### Real-time Display

- **Cache hit rate**: Real-time display in TUI footer
- **Token statistics**: Detailed token usage per conversation turn
- **Unified metrics**: Consistent statistics across TUI and print modes

### Use Cases

- **Cost optimization**: Monitor cache hit rate to optimize API call costs
- **Performance monitoring**: Monitor token usage to optimize response speed

---

## 🐞 Consistent Debug Output

### `--debug` Mode

```bash
# Enable debug mode
mothx --debug

# Debug output includes
- Provider request/response
- ACP mode debugging
- Tool execution details
- Local pprof at http://127.0.0.1:6060/debug/pprof/
```

### Use Cases

- **Troubleshooting**: Quickly locate API call issues
- **Development debugging**: Debug custom tools or skills
- **Performance analysis**: Analyze API call performance

---

## 🩺 Environment Diagnostics

### `doctor` Subcommand

```bash
# Run environment diagnostics
mothx doctor
```

### Check Items

- **Configuration files**: Check settings.json, serve.json, serve.json
- **Provider**: Check API keys and connections
- **Sandbox**: Check bwrap installation and configuration
- **MCP**: Check MCP server configuration
- **Sessions**: Check session storage
- **Skills**: Check skill loading
- **Context files**: Check AGENTS.md, CLAUDE.md, etc.

### Update Notifications

MothX checks for newer releases via npm registry and shows a non-blocking reminder when an update is available. Checks run in the background, keep only a local 24h cooldown timestamp, and never block the foreground.

Disable via `settings.json` with `"updateCheck": false`, or via `VIBECODING_NO_UPDATE_CHECK=1`.

---

## 🆕 Current Release Capabilities

The following capabilities are integrated into the current runtime; actual availability depends on configuration and model compatibility:

- **OpenAI Responses**: Supports native response item replay, reasoning, structured output, hosted tools, prompt cache, and optional durable background runs through `responses.background`. Background runs can be queried, canceled, reconnected, or abandoned and recover after Serve restarts.
- **Web UI management**: Provides session search and pagination, run status and cancellation, an approval center, tool results, skill activation, MCP configuration, Provider/Serve settings, and a `/stats` dashboard.
- **MCP**: Supports stdio, streamable HTTP, and legacy SSE.
- **Parallel tool execution**: Local function and custom tool calls within one agent turn run through a bounded parallel worker pool. Call starts are reported in the model's declared order without blocking execution, so calls still overlap and only the completion order is unrestricted. The `toolExecution` setting controls `mode` (`parallel`/`sequential`) and `maxConcurrency` (default `10`). Shared by TUI, WebUI, Serve, channels, ACP, and sub-agents.
 Global and project `mcp.json` files can be edited and are loaded into new Serve sessions.
- **Messaging channels**: Supports WeChat, Feishu, and Webhooks. Background runs persist events and can deliver results after reconnect or restart. WebSocket `/ws/runs` and `/ws/logs` are Web UI event streams, not standalone messaging channels.
- **Project state files**: Current project state is under `.mothx/`, including `settings.json`, `serve.json`, `allow.json`, `memory.md`, and skill directories. Legacy `.vibe` paths are no longer migrated automatically.
- **Model capability validation**: Images, attachments, Responses hosted tools, and reasoning parameters are validated against the selected model before requests are sent; unsupported combinations fail early.

---

## 🎯 Summary

MothX is an **all-in-one terminal AI coding assistant** with these core advantages:

1. **One binary does it all**: No need to install multiple tools
2. **Multi-provider support**: 20+ vendor adapters, one-click switching
3. **Security control**: Sandbox isolation, approval gates, blacklist protection
4. **Rich features**: Session management, skills system, IDE integration
5. **High performance**: SSE streaming, cache hit optimization

**Ready to get started?** [5-Minute Quick Start](quick-start-tutorial.md)
