> **Rename notice:** VibeCoding is now **MothX**. PyPI install name: `mothx-installer` (`pipx install mothx-installer` or `pip install mothx-installer`).

# MothX

MothX was formerly known as VibeCoding. Each wheel includes a platform-native `mothx` binary. The `vibecoding` console command is still registered as a compatibility alias during the rename transition.

## Installation

```bash
pip install mothx-installer
```

Or with an isolated environment:

```bash
pipx install mothx-installer
```

After installation, the `mothx` command is available on your `PATH`.

## Quick Start

```bash
# Set your API key
export DEEPSEEK_API_KEY=sk-...

# Start an interactive session
mothx

# Or ask a question directly
mothx -P "Write a Go HTTP server"
```

## Choose Your Mode

Every session runs in one of three modes, switchable anytime with `Tab` or `/mode`:

| Mode | Files | Network | Best For |
|------|-------|---------|----------|
| **Plan** | Read-only | ✗ | Analysis, code review, safe exploration |
| **Agent** | Read/Write | ✗ | Daily development |
| **YOLO** | Full | ✓ | System administration, automation (default) |

```bash
mothx --mode plan     # read-only
mothx --mode agent    # standard
mothx --mode yolo     # full access (default)
```

## Multi-Provider Support

Switch providers with a single flag:

```bash
# DeepSeek (default)
mothx --provider deepseek-openai --model deepseek-v4-flash

# OpenAI
mothx --provider openai --model gpt-4o

# Anthropic
mothx --provider anthropic --model claude-3-5-sonnet-20241022

# 20+ vendor adapters supported: Gemini, Kimi, Volcengine, Mistral,
# OpenRouter, Groq, Bedrock, GitHub Copilot, and more
```

## Session Management

MothX persists conversation history in SQLite with branching support:

```bash
mothx                    # start new session
mothx --continue         # resume last session
mothx --resume <id>      # resume a specific session
mothx --session <file>   # open a specific session file
```

Sessions live in `~/.mothx/sessions/` (Linux/macOS) or `%APPDATA%\mothx\sessions\` (Windows).

## Print / CI Mode

Use MothX in non-interactive mode for scripting and CI/CD:

```bash
mothx -P "Write unit tests for auth.go"
mothx -P "Generate a CHANGELOG from git log" > CHANGELOG.md
echo "Explain this function" | mothx -P
```

Use `--mode plan` for read-only tasks and pair with `--print` to fail fast when approval is required.

## Think Mode

Enable deeper reasoning for complex problems:

```bash
mothx --thinking high "Solve this architecture problem"
```

Levels: `off`, `minimal`, `low`, `medium`, `high`, `xhigh` — toggle in TUI with `/think`.

## Security & Sandboxing

MothX uses [bubblewrap](https://github.com/containers/bubblewrap) for process-level isolation. Three layers of protection:

- **Blacklist** — dangerous commands (`rm -rf`, `sudo`) are always blocked, even in YOLO mode
- **Approval gates** — Agent mode prompts before executing bash commands
- **Whitelist** — pre-approve safe commands in `settings.json`:

```json
{
  "approval": {
    "bashWhitelist": ["go ", "make ", "git ", "npm "],
    "bashBlacklist": ["rm -rf", "sudo"],
    "confirmBeforeWrite": true
  }
}
```

## Skills System

Skills are reusable prompt snippets for project conventions:

```bash
# List skills
> /skills

# Activate a skill
> /skill my-conventions
```

Place `SKILL.md` files in:
- **Global** — `~/.mothx/skills/<name>/SKILL.md`
- **Project** — `.skills/<name>/SKILL.md` (overrides global)

Compatible with [SkillHub](https://github.com/startvibecoding/skillhub) for discovering and sharing community skills.

## IDE Integration

MothX speaks [Agent Client Protocol (ACP)](https://github.com/oschina/mothx/blob/main/docs/en/acp.md), integrating with VS Code, Zed, and JetBrains IDEs.

### VS Code

Add to `.vscode/settings.json`:

```json
{
  "acp.agents": {
    "mothx": {
      "command": "mothx",
      "args": ["acp", "--mode", "agent"]
    }
  }
}
```

### JetBrains (IntelliJ, WebStorm, etc.)

Go to `Settings → Tools → ACP Agents` and add:
- Name: `MothX`
- Command: `mothx`
- Arguments: `acp --mode agent`

## Serve Mode

Run MothX as a unified OpenAI-compatible HTTP API, Web UI, and messaging server:

```bash
mothx serve
```

Config in `~/.mothx/serve.json` or `.mothx/serve.json`. Ideal for team sharing, CI/CD pipelines, messaging channels, and embedding into your own tools.

## Multi-Agent Mode

Enable sub-agent delegation and cron workflows:

```bash
mothx --multi-agent
```

Sub-agent tools: `subagent_spawn`, `subagent_status`, `subagent_send`, `subagent_destroy`.

For a single blocking investigation:

```bash
mothx --delegate
```

## Messaging Channels

Deploy MothX as a chatbot on WeChat, Feishu, or WebSocket:

```bash
mothx serve
```

Messaging channel config lives in `serve.json`. Each user gets an independent, persistent session.

## Keyboard Shortcuts

| Shortcut | Action |
|----------|--------|
| `Enter` | Submit prompt |
| `Alt+Enter` / `Ctrl+J` | Insert newline |
| `Tab` | Cycle mode (plan → agent → yolo) |
| `Esc` | Abort current operation |
| `Ctrl+O` | Open tool details |
| `Ctrl+G` | Toggle compact display |
| `Ctrl+T` | Toggle thinking display |

## Supported Platforms

- Linux x86_64, arm64, ppc64le, s390x
- Linux musl x86_64, arm64
- macOS x86_64, arm64
- Windows x64, arm64

Other platforms (LoongArch64, RISC-V64, *BSD) are available via `npm install -g mothx-installer` or the one-line installer.

## Diagnostics

```bash
mothx doctor
```

Checks config files, API connections, sandbox, MCP servers, sessions, and skills.

## Uninstall

```bash
pip uninstall mothx-installer

# or if you used pipx:
pipx uninstall mothx-installer
```

## Links

- **Homepage** — <https://github.com/oschina/mothx>
- **Documentation** — <https://github.com/oschina/mothx/tree/main/docs>
- **Changelog** — <https://github.com/oschina/mothx/blob/main/docs/en/changelog.md>
- **Issues** — <https://github.com/oschina/mothx/issues>
- **License** — MIT
