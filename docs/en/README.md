# MothX Documentation

<p align="center">
  <img src="assets/mothx-small.png" alt="MothX" width="128" height="128">
</p>

<p align="center">
  <strong>🚀 One Binary to Rule Them All — Your AI Coding Assistant in the Terminal</strong>
</p>

<p align="center">
  <em>Make OSCHINA Tokens Harness eXecution</em>
</p>

<p align="center">
  Stop switching between Claude Code, Codex, Claw, and Channels.<br>
  MothX packs everything into a single file — providers, tools, sandbox, sessions, skills, and more.
</p>

<p align="center">
  <a href="https://www.npmjs.com/package/mothx-installer"><img src="https://img.shields.io/npm/dm/mothx-installer.svg" alt="npm downloads"></a>
  <a href="https://pypi.org/project/mothx-installer/"><img src="https://img.shields.io/pypi/v/mothx-installer.svg" alt="PyPI version"></a>
  <a href="https://github.com/oschina/mothx/releases/latest"><img src="https://img.shields.io/github/release/oschina/mothx.svg" alt="GitHub release"></a>
  <a href="https://opensource.org/licenses/MIT"><img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="License: MIT"></a>
  <a href="https://goreportcard.com/report/github.com/oschina/mothx"><img src="https://goreportcard.com/badge/github.com/oschina/mothx" alt="Go Report Card"></a>
  <a href="https://pkg.go.dev/github.com/oschina/mothx"><img src="https://pkg.go.dev/badge/github.com/oschina/mothx?status.svg" alt="GoDoc"></a>
  <a href="https://github.com/oschina/mothx/network/dependencies"><img src="https://img.shields.io/librariesio/release/github/oschina/mothx" alt="Dependencies"></a>
</p>

> **Rename notice:** MothX was formerly known as VibeCoding. The `vibecoding` command and old installer package names remain compatibility aliases. Configuration is read only from `.mothx` project directories and the MothX global configuration directory; legacy directories are neither read nor migrated.

---

Welcome to the MothX Documentation Center!

## ✨ Why MothX?

**The Problem:** You're juggling multiple AI coding tools — Claude Code for one thing, Codex for another, Claw for something else. Each has its own setup, its own quirks, its own dependencies.

**The Solution:** MothX is the **all-in-one terminal AI coding assistant** that does it all. One binary. One config. Zero hassle.


![MothX vs Competitors](assets/image/comparison-en.webp)

### 🎯 Key Highlights

| Feature | What It Means for You |
|---------|----------------------|
| **🤖 Multi-Provider** | DeepSeek, OpenAI, Anthropic, Volcengine/Doubao, LongCat, and 25+ vendor adapters — switch models instantly |
| **⚡ Lightning Fast** | SSE streaming, real-time token delivery, cache hit optimization |
| **🧠 Think Mode** | Extended reasoning for complex problems (DeepSeek, o1, Claude) |
| **🛡️ Sandboxed** | bwrap process isolation — safe file ops, network control, approval gates |
| **📝 Sessions** | Persistent SQLite-backed history with interactive picker, branching, compaction, and tree structure |
| **🧩 Skills** | Reusable prompt snippets for project conventions — share across teams |
| **📊 Stats Dashboard** | Usage statistics with charts, per-provider/model breakdowns, and CLI mode |
| **🔄 Workflows** | JavaScript DSL for multi-phase, parallel, multi-agent orchestration |
| **💻 IDE Ready** | ACP protocol for VS Code, Zed, JetBrains — native editor integration |
| **🌐 Serve** | OpenAI Chat/Responses APIs, Web UI, MCP, approvals, and durable background runs |
| **📱 Messaging** | WeChat, Feishu, Webhooks, and WebSocket event streams with durable sessions |
| **🤝 Multi-Agent** | Async sub-agents with `--multi-agent`, blocking delegation with `--delegate`, and A2A master mode |
| **🎨 Rich TUI** | Markdown rendering, syntax highlighting, thinking display, tool modals, multiline input |
| **🔒 Security** | Project-level bash auto-approval rules, bashBlacklist > whitelist, interactive approval dialog |

---

## 📚 Documentation

### 🚀 Getting Started
- [5-Minute Quick Start](quick-start-tutorial.md) — Skip the long docs, just start coding!
- [Quick Start](getting-started.md) — Installation, configuration, first run
- [CLI Reference](cli-reference.md) — All commands and flags

### ✨ Core Features
- [Features Overview](features-overview.md) — Multi-provider, sandbox, sessions, skills, and more

### 🎯 Use Cases
- [Use Cases & Real-World Examples](use-cases.md) — From daily development to enterprise deployment

### ⚙️ Configuration
- [Configuration Guide](configuration.md) — Settings, env vars, authentication

### 🏗️ Architecture
- [System Architecture](architecture.md) — Core components, data flow
- [Tool System](tools.md) — Built-in tools guide
- [Skills System](skills.md) — Reusable prompt snippets
- [Online Skill Marketplace](skillhub.md) — SkillHub / ClawHub integration
- [Session Management](sessions.md) — Session storage and management
- [Workflow Mode](workflow.md) — JavaScript DSL for multi-agent orchestration
- [SDK Integration](sdk.md) — Embed MothX agent in your Go applications

### 🔒 Security
- [Security & Sandbox](security.md) — Sandbox modes, permissions, approval

### 💻 IDE Integration
- [ACP Protocol](acp.md) — VS Code, Zed, JetBrains integration

### 🌐 Serve Mode
- [Serve Mode](serve.md) — OpenAI-compatible HTTP API, Web UI, and messaging channels
- [A2A Protocol](a2a.md) — Agent-to-Agent protocol

### 📖 Tutorials
- [Scenarios & Walkthroughs](scenarios.md) — Practical examples
- [FAQ](faq.md) — Common questions answered

### 🇨🇳 中文文档
- [中文文档首页](../zh/README.md) — 完整中文文档

---

## 🚀 Quick Install

```bash
# npm (recommended)
npm install -g mothx-installer

# Compatibility package for this transition release
npm install -g vibecoding-installer

# PyPI
pipx install mothx-installer

# One-line install (Linux/macOS)
curl -fsSL https://mothx.net/install.sh | bash
```

```bat
:: One-line install (Windows Command Prompt)
curl.exe -fsSL https://mothx.net/install.bat -o install.bat && install.bat
```

```bash
# Docker (GHCR, Linux amd64/arm64)
docker run --rm -it --entrypoint bash ghcr.io/oschina/mothx:latest
docker run --rm -it -v "$PWD:/workspace" -w /workspace ghcr.io/oschina/mothx:latest

# Go install
go install github.com/oschina/mothx/cmd/mothx@latest
```

## 🗑️ Uninstall

```bash
# npm
npm uninstall -g mothx-installer

# Compatibility package, if installed before the rename
npm uninstall -g vibecoding-installer

# PyPI
pipx uninstall mothx-installer

# Linux/macOS (one-line install)
curl -fsSL https://mothx.net/install.sh | bash -s -- --uninstall

# Windows (one-line install)
npm uninstall -g mothx-installer
```

## 🎮 Three Modes for Every Situation

```
🗒️  Plan    → Read-only analysis & planning. Safe, sandboxed, no surprises.
🔧  Agent   → Standard read/write. Bash approval required.
🚀  YOLO    → Full system access. No restrictions. (Default)
```

Switch modes anytime with `/mode plan|agent|yolo` or press `Tab`.


![Three Safety Modes](assets/image/modes-en.webp)

## 🎯 Use Cases

### 💻 Daily Development
```bash
mothx -P "Refactor this function to use generics"
mothx -P "Write tests for the UserService struct"
mothx -P "Explain what this regex does"
```

### 🔍 Code Review
```bash
mothx --mode plan "Review this PR and suggest improvements"
```

### 🚀 CI/CD Integration
```bash
mothx -p "Generate changelog from git log" > CHANGELOG.md
```

### 🌐 API Server
```bash
mothx serve  # Start OpenAI-compatible HTTP server
```

### 📱 Chatbot
```bash
mothx serve   # Deploy as WeChat/Feishu bot
```

## 🛠️ Built-in Tools

| Tool | Description |
|------|-------------|
| `read` | Read file content (supports images) |
| `write` | Create/overwrite files (atomic) |
| `edit` | Precise multi-replacement text edits |
| `bash` | Execute shell commands (sync/async, default 45s timeout) |
| `grep` | Search file content (pure-Go ripgrep) |
| `find` | Find files by pattern (pure-Go fd) |
| `ls` | List directory contents |
| `plan` | Publish task plans with steps |
| `question` | Ask user multiple-choice questions |
| `jobs` | Manage background jobs |
| `kill` | Stop background jobs |
| `skill_ref` | Load skill reference files |
| `delegate_subagent` | Blocking single sub-agent delegation |
| `workflow_run` | Run JavaScript workflow orchestration |

## Getting Help

- Use the `/help` command for interactive help
- Check the [CLI Reference](cli-reference.md) for all commands
- Read the [FAQ](faq.md) for common questions
