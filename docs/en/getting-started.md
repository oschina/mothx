# Quick Start

This guide helps you get started with MothX in 5 minutes.

## System Requirements

- **Operating System**: Linux, macOS, Windows 10/11
- **Go**: 1.24+ (when building from source)
- **Optional**: bubblewrap (for sandbox functionality)

## Installation

### Method 1: One-line Install (Recommended)

The script uses an existing Node.js installation when available. If Node.js is
missing, it installs the latest Node.js LTS release first and then installs the
latest MothX package through npm.

**Linux/macOS:**

```bash
curl -fsSL https://mothx.net/install.sh | bash
```

**Windows (Command Prompt):**

```bat
curl.exe -fsSL https://mothx.net/install.bat -o install.bat && install.bat
```

Run the command in Command Prompt (`cmd.exe`). The installer checks for Node.js, installs the latest Node.js LTS when needed, and then installs MothX globally through npm.

**Uninstall:**

```bash
# npm
npm uninstall -g mothx-installer

# PyPI
pipx uninstall mothx-installer

# Linux/macOS
curl -fsSL https://mothx.net/install.sh | bash -s -- --uninstall

# Windows
npm uninstall -g mothx-installer
```

The online installers ultimately run `npm install -g mothx-installer`.

### Method 4: Docker (GHCR)

```bash
# Default Ubuntu image, runs as root
docker run --rm -it --entrypoint bash ghcr.io/oschina/mothx:latest

# Run MothX in the current workspace
docker run --rm -it -v "$PWD:/workspace" -w /workspace ghcr.io/oschina/mothx:latest
```

Available tags:

- `latest` / `ubuntu` - default Ubuntu image
- `debian`, `fedora`, `alpine` - distro variants
- `vX.Y.Z`, `vX.Y.Z-ubuntu`, `vX.Y.Z-debian`, `vX.Y.Z-fedora`, `vX.Y.Z-alpine` - release tags

Published architectures: `linux/amd64`, `linux/arm64`

The container runs as `root` by default and includes `bash`, `git`, `curl`, `ca-certificates`, and `openssh-client`.

### Method 5: Go Install

```bash
go install github.com/oschina/mothx/cmd/mothx@latest
```

### Method 6: Build from Source

```bash
# Clone repository
git clone https://github.com/oschina/mothx.git
cd mothx

# Build
make build

# Binary is located at bin/mothx
```

### Method 7: Install to System

```bash
# After building from source
make install
```

## Configure API Keys

### Option 1: Environment Variables

```bash
# DeepSeek
export DEEPSEEK_API_KEY=sk-...
```

### Option 2: Settings File

Or add keys directly to your settings.json:

```json
{
  "providers": {
    "deepseek-openai": {
      "vendor": "deepseek",
      "api": "openai-chat",
      "baseUrl": "https://api.deepseek.com",
      "apiKey": "sk-..."
    }
  }
}
```

The optional `vendor` field selects a vendor adapter. If it is omitted, MothX detects the vendor from `baseUrl` when possible and otherwise falls back to the generic provider selected by `api`. See the [Configuration Guide](configuration.md) for details.

## First Run

### Interactive Mode

```bash
# Start interactive session
mothx
```

### Non-Interactive Mode

```bash
# Single question
mothx -p "Explain what this code does"

# Read from stdin
echo "Write a Hello World" | mothx -P
```

### Specify Model

```bash
# Use DeepSeek-V4-Flash
mothx --provider deepseek-openai --model deepseek-v4-flash

# Use DeepSeek-V4-Pro
mothx --provider deepseek-openai --model deepseek-v4-pro
```

### Multi-Agent Mode

```bash
# Enable sub-agent tools and multi-agent commands
mothx --multi-agent

# ACP sessions can opt in too
mothx acp --multi-agent
```

Multi-agent mode registers `subagent_*` tools for delegated work. Cron command entry points are available in TUI multi-agent workflows.

### Expert Teams

Bind a reusable single expert or a coordinated team to a session:

```bash
# Start with the built-in software team
mothx --expert software-company
```

In TUI, use `/expert list`, `/expert show <id>`, `/expert bind <id>`,
`/expert unbind`, and `/expert switch <id>`. Switching between two experts
creates a session fork so the original identity and history remain intact.
Team experts automatically enable the session's member-dispatch capability.
See [Expert Teams](expert-teams.md) for lifecycle, ESM, and local-bundle details.

### Delegate Mode

```bash
# Enable blocking single sub-agent delegation
mothx --delegate

# Toggle it at runtime in TUI
/delegate on
/delegate status
/delegate off
```

Delegate mode registers `delegate_subagent`, a synchronous tool for one bounded subtask at a time. It is useful for multi-step investigations or focused verification where the main agent only needs a summarized result.

### A2A Master Mode

```bash
# Generate sample config
mothx --init-a2a-master-config

# Enable master mode
mothx --enable-a2a-master
```

A2A Master mode lets you manage multiple remote A2A agents, with the LLM automatically dispatching tasks via the `a2a_dispatch` tool. See [A2A Protocol](a2a.md) for details.

## Choose Mode

MothX provides three modes:

```bash
# Plan mode - read-only analysis
mothx --mode plan

# Agent mode - standard read/write
mothx --mode agent

# YOLO mode - full access (default)
mothx --mode yolo
```

| Mode | File System | Network | Use Case |
|------|------------|---------|----------|
| **Plan** | Read-only | ✗ | Analysis, planning |
| **Agent** | Read/Write | ✗ | Daily development |
| **YOLO** | Full | ✓ | System-level operations |

## Basic Interaction

### Common Commands

```bash
/mode plan      # Switch to Plan mode
/mode agent     # Switch to Agent mode
/model          # View current model
/think          # Toggle thinking level
/clear          # Clear conversation
/help           # Show help
/quit           # Exit
```

### Keyboard Shortcuts

| Shortcut | Function |
|----------|----------|
| `Enter` | Submit the current prompt |
| `Alt+Enter` / `Ctrl+J` | Insert a newline in the prompt editor |
| `Tab` | Cycle mode (`plan` → `agent` → `yolo`) |
| `Esc` | Abort the current operation, approval, or question prompt |
| `Ctrl+O` | Open the latest tool/details modal; press again, `Esc`, or `q` to close |
| `Ctrl+E` | Open the ESM progress panel; press again, `Esc`, or `q` to close |
| `Ctrl+G` | Toggle simple/full event display; simple is the default |
| `Up` / `Down` | Move within multiline input; browse prompt history at the first/last input line; scroll an open details/progress panel |
| `PgUp` / `PgDn` | Page through an open details/progress panel |
| `Home` / `End` | Move to the start/end of the current input line; jump to top/bottom in an open details/progress panel |

Completed conversation blocks are printed to the terminal's native scrollback, so use your terminal's normal mouse wheel, trackpad, or scrollbar to review older transcript output.

## Usage Examples

### Code Explanation

```bash
mothx -P "Explain the purpose of main.go"
```

### Code Generation

```bash
mothx -P "Write a Go HTTP server"
```

### File Operations

```bash
mothx -P "Create a README.md in the current directory"
```

### Continue Session

```bash
# Continue most recent session
mothx --continue

# Resume specific session
mothx --resume <session-id>
```

## Skills System

Skills are reusable prompt snippets that help enforce project conventions:

```bash
# List available skills
> /skills

# Activate a skill
> /skill my-conventions
```

Create skills by adding `SKILL.md` files:
- **Global**: `~/.mothx/skills/<name>/SKILL.md` (available for all projects)
- **Project**: `.skills/<name>/SKILL.md` (project-specific, overrides global)

See the [Skills System](skills.md) documentation for details.

## IDE Integration

MothX can be integrated into your IDE via the Agent Client Protocol (ACP):

### VS Code

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

### JetBrains IDEs

Navigate to `Settings → Tools → ACP Agents` and add:
- **Name**: MothX
- **Command**: `mothx`
- **Arguments**: `acp --mode agent`

See the [ACP Protocol](acp.md) documentation for details.

## Troubleshooting

If something doesn't work, run the built-in diagnostics:

```bash
mothx doctor
```

This checks your environment, config files, providers, sandbox, MCP, sessions, skills, and context files, and reports any issues.

## Next Steps

- Read the [Configuration Guide](configuration.md) to customize settings
- Check the [Tool Reference](tools.md) to learn about available tools
- Try [multi-agent mode](cli-reference.md#multi-agent-mode) for delegated investigation and cron command entry points
- Try [delegate mode](cli-reference.md#delegate-mode) for blocking single sub-agent investigation tasks
- Understand the [Security Model](security.md) to protect your system
- Explore the [Skills System](skills.md) to create reusable prompt snippets
- Set up [IDE Integration](acp.md) with VS Code or JetBrains
- Check out [Scenarios & Walkthroughs](scenarios.md) for practical usage examples
