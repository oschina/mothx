# CLI Reference

## Overview

```
mothx [flags] [message...]
```

## Command Line Arguments

### Basic Parameters

| Parameter | Short | Default | Description |
|-----------|-------|---------|-------------|
| `--provider` | `-p` | Default from config file | LLM provider (deepseek-openai, deepseek-anthropic or custom name) |
| `--model` | `-m` | Default from config file | Model ID |
| `--mode` | `-M` | `yolo` | Run mode (plan, agent, yolo, os); falls back to `settings.json` `defaultMode` |
| `--thinking` | `-t` | `off` | Thinking level (off, minimal, low, medium, high, xhigh) |
| `--multi-agent` | - | `false` | Enable multi-agent tools and commands |
| `--delegate` | - | `false` | Enable delegation mode (blocking single sub-agent tool) |
| `--workflows` | - | `false` | Enable JavaScript workflow tools and `/workflows` commands |
| `--artifact` | - | `false` | Enable generated artifact publishing for this TUI/CLI process |

### Session Management

| Parameter | Short | Description |
|-----------|-------|-------------|
| `--continue` | `-c` | Continue most recent session |
| `--resume` | `-r` | Resume session by ID or path |
| `--session` | - | Use specific session ID or `.db` handle file |

### Output Control

| Parameter | Short | Description |
|-----------|-------|-------------|
| `--print` | `-P` | Non-interactive mode, print response and exit. If a tool would require approval, the command exits with an error instead of auto-approving. |
| `--json` | - | Stream print-mode output as one JSON object per line (NDJSON) to stdout (all status/diagnostics still go to stderr, easy to pipe into `jq`). Requires `-P`. |
| `--verbose` | - | Verbose output |
| `--debug` | - | Enable debug logging, provider request/response debug output, and local pprof at `127.0.0.1:6060` |

### Security

| Parameter | Description |
|-----------|-------------|
| `--sandbox` | Enable sandbox (bubblewrap) |
| `--no-sandbox` | Disable sandbox (deprecated, disabled by default) |

### Other

| Parameter | Short | Description |
|-----------|-------|-------------|
| `--init-serve` | - | Create `serve.json` config template |
| `--init-a2a-master-config` | - | Create `a2a-list.json` config template |
| `--enable-a2a-master` | - | Enable A2A master mode (remote agent dispatch) |
| `--force` | - | Force overwrite existing files (used with `--init-*`) |
| `--version` | `-v` | Show version |
| `--help` | `-h` | Show help |

## Subcommands

### `acp` - Agent Client Protocol Server

Run MothX as an ACP-compliant stdio agent for IDE integration.

```
mothx acp [flags]
```

Supports VS Code, JetBrains IDEs, and any ACP-compatible editor.

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--provider` | `-p` | From config | LLM provider |
| `--model` | `-m` | From config | Model ID |
| `--mode` | `-M` | `yolo` | Run mode (plan, agent, yolo, os); falls back to `settings.json` `defaultMode` |
| `--thinking` | `-t` | From config | Thinking level |
| `--sandbox` | - | false | Enable sandbox |
| `--verbose` | - | false | Verbose output |
| `--debug` | - | false | Debug logging and local pprof |
| `--multi-agent` | - | false | Enable multi-agent tools for ACP sessions |
| `--delegate` | - | false | Enable delegation mode for ACP sessions |
| `--workflows` | - | false | Enable JavaScript workflow tools for ACP sessions |
| `--artifact` | - | false | Enable generated artifact publishing for this ACP/Desktop process |

See the [ACP Protocol](acp.md) documentation for IDE integration details.

### `a2a` - A2A Protocol Server

Run the standalone A2A (Agent-to-Agent) protocol server.

```
mothx a2a [command]
```

| Subcommand | Description |
|------------|-------------|
| `start` | Start A2A server |
| `stop` | Stop A2A server |
| `status` | Show server status |
| `card` | Show/generate Agent Card |
| `send <message>` | Send task to remote A2A server |
| `discover <url>` | Discover remote Agent Card |
| `--init-a2a-config` | Create `a2a.json` config template |
| `--force` | Force overwrite existing config file |

See [A2A Protocol](a2a.md) documentation for details.

### `serve` - Unified Server

Start MothX as a unified server exposing an OpenAI-compatible Chat Completions API, Web UI, and optional WeChat/Feishu/WebSocket messaging channels.

```
mothx serve [flags]
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--port` | - | `127.0.0.1:7872` | Listen port or address, e.g. `7872` or `0.0.0.0:7872` (overrides serve.json) |
| `--config` | - | - | Path to serve.json |
| `--web-ui-dir` | - | Built-in assets | Override embedded Serve Web UI assets with a frontend directory |
| `--unsafe` | - | false | Disable auth and bind Serve to all interfaces |
| `--work-dir` | - | Current directory | Default working directory |
| `--provider` | `-p` | From config | LLM provider |
| `--model` | `-m` | From config | Model ID |
| `--sandbox` | - | false | Enable sandbox (bwrap) |
| `--multi-agent` | - | false | Enable multi-agent tools |
| `--delegate` | - | false | Enable delegation mode |
| `--workflows` | - | false | Enable JavaScript workflow tools |
| `--web-search` | - | false | Enable configured local web search for Serve sessions |
| `--browser` | - | false | Enable browser automation for Serve sessions |
| `--artifact` | - | false | Enable generated artifact publishing for WebUI/API sessions |
| `--enable-a2a-master` | - | false | Enable A2A master mode for remote-agent dispatch |
| `--lobster` | - | false | Enable yolo mode, disable sandbox, and enable sub-agents |
| `--verbose` | - | false | Verbose output |
| `--debug` | - | false | Debug logging and local pprof |

| Subcommand | Description |
|------------|-------------|
| `init-config [global|project]` | Create `serve.json` config template |
| `--force` | Force overwrite existing config file |

See [Serve Mode](serve.md) documentation for details.

### `stats` - Usage Statistics

Start the usage dashboard, or print token and request statistics directly in the terminal.

```
mothx stats [flags]
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--addr` | - | `127.0.0.1:7878` | Listen address for the web dashboard |
| `--db` | - | Default sessions database | Path to `sessions.db` |
| `--cli` | - | false | Print stats in the terminal instead of starting the web server |

Examples:
```bash
mothx stats
mothx stats --cli
mothx stats --cli --db ~/.mothx/sessions/sessions.db
```

### `pure` - Archive the Sessions Database

Move the shared `sessions.db` aside and create a fresh, empty database in its place. Nothing is deleted: every moved file is renamed next to the new database as `sessions.db.pure-<timestamp>.bak`, and the SQLite sidecars keep their suffix (`...bak-wal`, `...bak-shm`, `...bak-journal`), so the previous sessions stay recoverable. A sidecar whose database is already gone - from an interrupted reset or a hand-deleted file - is archived the same way, so the fresh database can never inherit it.

Only `sessions.db` and its sidecars are archived. Everything else in the session directory stays where it is, and each kind stays for its own reason: `channels/` holds **separate session roots**, each with its own `sessions.db` and session handles; every `knowledge-bases/<id>.db` **is** that knowledge base's own authoritative store (its configuration row and its rebuildable index live inside that file, and bases are enumerated from the directory, not from the shared database); and `artifacts/` holds attachment content that the archived database used to describe. Of the three, only unreferenced attachment storage is reclaimable - the rest is live data that a sessions-database reset does not invalidate. See the reclamation rules below.

```
mothx pure [flags]
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--session-dir` | - | Configured session directory | Session directory to reset |
| `--force` | - | false | Reset even while another `mothx` process holds an active session run |
| `--prune-unreferenced` | - | false | Also reclaim attachment storage no row references and that is already past the retention window |

Examples:
```bash
# Archive the default sessions database and start fresh
mothx pure

# Reset a specific session directory
mothx pure --session-dir ~/.mothx/sessions

# Reset despite an active run in another process
mothx pure --force

# Reset and immediately reclaim unreferenced attachment storage
mothx pure --prune-unreferenced
```

The command refuses to start while a session run is active in that directory, naming the session, the owning pid, and its run. That check reads execution leases, so an idle `mothx` process that only holds the database open is not detected - stop the other processes yourself, or pass `--force`. A database this process cannot open at all is reported as unknown rather than busy and still resets, since that is the case `pure` exists for.

Attachment storage becomes unreachable once the rows that described it are archived, and the same happens when a session is deleted or an intake fails after its content was written. Reclamation never relies on a reset: the Runtime sweeps the private store while accepting new attachments (at most once an hour per process) and once a day through its own cron maintenance job, and removes only directories that no row references **and** that were last written before the attachment retention window plus a 24-hour grace period - eight days by default with the 7-day retention. That floor is why reclamation can never destroy something a restored archive could still serve. `--prune-unreferenced` runs the same pass immediately and reports what it reclaimed and what it kept as too young; `channels/` and `knowledge-bases/` are never touched by it. The scheduled pass is configured by the optional `maintenance` section of `settings.json` (`reclaimAttachmentStorage`, `storageReconcileSchedule`); see [configuration](configuration.md#maintenance).

### `doctor` - Environment Diagnostics

Diagnose your MothX environment: OS info, config files, providers, models, sandbox, MCP, and more.

```
mothx doctor
```

The command checks your installation, configuration, provider connectivity, and sandbox availability.

### `systeminit` - Project AGENTS.md Generator

Generate or refresh a project `AGENTS.md` file that documents project conventions for AI agents.

```
mothx systeminit [guidance...]
```

This CLI subcommand runs non-interactively. In TUI and ACP, `/systeminit` runs interactively and uses the `question` tool to ask clarifying questions before generating the file.

| Argument | Description |
|----------|-------------|
| `guidance...` | Optional trailing guidance (e.g., `ask me in Chinese, write AGENTS.md in English`) |

Examples:
```bash
# Generate AGENTS.md with default behavior
mothx systeminit

# Generate with custom guidance
mothx systeminit ask me in Chinese, write in English
```

Checks performed:
- **Environment**: OS/arch, Go version, shell, home/working directory
- **Configuration Files**: Validates settings, serve, and MCP config files with parse checks
- **Providers & Models**: Lists configured providers with masked API keys, models with context window/max tokens/reasoning flags; verifies default provider initialization
- **Sandbox**: Checks bubblewrap availability and version
- **MCP Servers**: Lists configured MCP servers
- **Sessions**: Shows session directory and entry count
- **Skills**: Shows global and project skills directories
- **Context Files**: Discovers AGENTS.md, CLAUDE.md, CURSOR.md, .cursorrules, CONVENTIONS.md

```bash
mothx doctor
```

Sample output:
```
  MothX Doctor
  ─────────────────

  Environment
    ✅ OS / Arch — linux/amd64
    ✅ Go version — go1.24.4
    ✅ Shell — /bin/bash
    ✅ Home directory — /home/user
    ✅ Working directory — /home/user/project

  Configuration Files
    ✅ Global settings — /home/user/.mothx/settings.json (1.2 KB)
    ⏭️  Project settings — .mothx/settings.json (not found)
    ...

  Providers & Models
    ✅ Default provider — deepseek-openai
    ✅ Default model — deepseek-v4-flash
    ✅ Provider: deepseek-openai — api=openai-chat, base=https://api.deepseek.com, key=sk-a****xyz
    ✅   └─ deepseek-v4-flash — ctx=1M, max=384K ★ default
    ✅ Provider init — deepseek-openai/deepseek-v4-flash created successfully

  Result: All 15 checks passed
```

## Usage Examples

### Basic Usage

```bash
# Interactive mode
mothx

# With initial prompt
mothx -P "Explain this codebase"

# Non-interactive print mode (`-p` selects a provider; `-P` enables printing)
mothx -P "Write a Hello World"
```

### JSON Output

```bash
# Stream NDJSON (one line per event) and reassemble the assistant text
mothx -P --json "Summarize this repo" | jq -r 'select(.type=="text_delta").text' | tr -d '\n'

# Capture the assistant text in a script
summary=$(mothx -P --json "Describe in one sentence" | jq -r 'select(.type=="text_delta").text' | tr -d '\n')

# Inspect the whole typed event stream
mothx -P --json "Summarize this repo" | jq -s '.'
```

`--json` only takes effect with `-P` (print mode). stdout emits an NDJSON (JSON Lines) stream — each agent event is its own JSON object on one line, so consumers can read line-by-line and react in real time. Event types include `start` (provider/model/mode), `text_delta`, `think_delta`, `tool_call`, `tool_execution_start`/`tool_execution_end`, `tool_result`, `plan_update`, `usage`, `context_usage`, `compaction_start`/`compaction_end`, and a terminating `done` or `error` event. All progress and debug output still goes to stderr, so stdout stays pure NDJSON.

### Specify Provider and Model

```bash
# Use DeepSeek (OpenAI API)
mothx --provider deepseek-openai --model deepseek-v4-flash

# Use DeepSeek (Anthropic API)
mothx -p deepseek-anthropic -m deepseek-v4-flash

# Use custom provider
mothx --provider my-custom-provider
```

### Choose Mode

```bash
# Plan mode - read-only analysis
mothx --mode plan

# Agent mode - standard read/write
mothx -M agent

# YOLO mode - full access (default)
mothx -M yolo
```

### Multi-Agent Mode

```bash
# Enable sub-agent tools and multi-agent commands
mothx --multi-agent

# ACP sessions can also opt in
mothx acp --multi-agent
```

When enabled, MothX registers the `subagent_*` tools and exposes multi-agent workflows such as delegated background investigation. Cron command entry points also depend on multi-agent mode.

### Expert Teams

```bash
# Bind an expert or team while starting a session
mothx --expert software-company
```

`--expert <bundle-id>` resolves a local expert bundle through the shared
Runtime. A team bundle enables its declared member dispatch capability for
that session; a single expert changes only the lead identity. In TUI, use
`/expert list|show <id>|bind <id>|unbind|switch <id>`. Replacing a bound
expert uses a session fork rather than rewriting the source session. See
[Expert Teams](expert-teams.md) for the complete behavior.

### Delegate Mode

```bash
# Enable blocking single sub-agent delegation
mothx --delegate

# ACP sessions can also opt in
mothx acp --delegate

# Serve can opt in
mothx serve --delegate
```

Delegate mode registers the `delegate_subagent` tool for synchronous, blocking sub-agent delegation. Unlike multi-agent (which runs async sub-agents in parallel), delegate mode runs one sub-agent at a time and waits for completion. Use it for bounded investigation tasks where the parent only needs a summarized result.

You can toggle delegation at runtime via `/delegate [on|off|status]` in TUI or serve slash commands.

### Thinking Levels

```bash
# Disable thinking
mothx --thinking off

# Medium level
mothx -t medium

# Highest level
mothx --thinking xhigh
```

### Session Management

```bash
# Continue most recent session
mothx --continue
mothx -c

# Resume specific session
mothx --resume session-abc123
mothx -r ~/.mothx/sessions/--encoded-working-directory--/20260625-120000_abcd1234.db

# Use specific session handle file
mothx --session ./20260625-120000_abcd1234.db
```

In the TUI, startup without `--continue`, `--resume`, or `--session` does not create an empty session immediately. A new session is created when the first user message is sent. When an existing session is continued, resumed, or selected, its history is shown in the normal terminal scrollback.

### Sandbox

```bash
# Enable sandbox
mothx --sandbox

# Disable sandbox (default)
mothx
```

### Pipe Input

```bash
# Read from stdin
echo "Explain this code" | mothx -P

# Read from file contents directly
mothx -p "Explain this file: main.go"
```

### ACP Server

```bash
# Start ACP server (for IDE integration)
mothx acp

# ACP with specific model
mothx acp --provider deepseek-openai --model deepseek-v4-flash

# ACP with sandbox
mothx acp --sandbox --mode agent
```

## Interactive Commands

Commands available during interactive sessions:

### Mode & Model

| Command | Description |
|---------|-------------|
| `/mode [plan\|agent\|yolo]` | Switch or show current mode |
| `/model [model_id]` | Switch or show current model |
| `/think` | Cycle thinking level |
| `/compact` | Trigger context compaction |
| `/delegate [on\|off\|status]` | Toggle or show delegate mode |
| `/systeminit [guidance]` | Generate or refresh project `AGENTS.md` |
| `/reload` | Restart with a fresh session (TUI) |
| `/btw <question>` | Ask a side question without interrupting main task |
| `/settings` | Configure settings.json groups, including providers, defaults, behavior, and approval |
| `/alloweditpath [add <glob>\|remove <glob>\|clear]` | Manage auto-edit path whitelist |
| `/allowautoedit [on\|off] [global]` | Toggle full auto-edit in agent mode |
| `/agent list` | List sub-agents in multi-agent mode |
| `/agent switch <id>` | Switch the active sub-agent |
| `/agent destroy <id>` | Destroy a sub-agent |

### Session Management

| Command | Description |
|---------|-------------|
| `/sessions` | Open the interactive session picker |
| `/sessions ls` | List sessions for the current project |
| `/sessions set <id>` | Switch to a session by ID prefix |
| `/sessions clear` | Start fresh; the new session is created on the next message |
| `/sessions del <id>` | Delete a session by ID prefix |
| `/clear` | Clear conversation |

The `/sessions` picker supports Up/Down to select, Enter to switch, `n` to start fresh, `d` to delete the selected session, and Esc to close.

### Skills

| Command | Description |
|---------|-------------|
| `/skills` | List available skills |
| `/skill <name>` | Activate a skill by name |
| `/skill:<name>` | Activate a skill (alternative syntax) |

### General

| Command | Description |
|---------|-------------|
| `/help` | Show help |
| `/quit` | Exit |

## Keyboard Shortcuts

| Shortcut | Function |
|----------|----------|
| `Enter` | Submit the current prompt |
| `Alt+Enter` / `Ctrl+J` | Insert a newline in the prompt editor |
| `Tab` | Cycle mode (`plan` → `agent` → `yolo`) |
| `Esc` | Abort current operation, approval, or question prompt |
| `Ctrl+O` | Open latest tool/details modal; press again, `Esc`, or `q` to close |
| `Ctrl+E` | Open the ESM progress panel; press again, `Esc`, or `q` to close |
| `Ctrl+G` | Toggle simple/full event display; simple is the default |
| `Up` / `Down` | Move within multiline input; browse prompt history at the first/last input line; scroll an open details/progress panel |
| `PgUp` / `PgDn` | Page through an open details/progress panel |
| `Home` / `End` | Move to the start/end of the current input line; jump to top/bottom in an open details/progress panel |

## Environment Variables

Default settings can be overridden via environment variables:

| Variable | Description |
|----------|-------------|
| `DEEPSEEK_API_KEY` | DeepSeek API key |
| `MOTHX_DIR` | Override config directory |
| `VIBECODING_PROVIDER` | Override default provider |
| `VIBECODING_MODEL` | Override default model |
| `VIBECODING_MODE` | Override default mode |
| `VIBECODING_THINKING` | Override default thinking level |
| `VIBECODING_USER_AGENT` | Custom User-Agent string |

## Exit Codes

| Code | Description |
|------|-------------|
| 0 | Success |
| 1 | General error |
| 2 | Usage error |
| 130 | User interrupt (Ctrl+C) |
