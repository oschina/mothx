> **Rename notice:** VibeCoding is now **MothX**. Install name: `mothx-installer` (`npm install -g mothx-installer`).

# MothX

🚀 一个二进制文件搞定一切 — 你的终端 AI 编程助手

A terminal-based AI coding assistant. One binary, multiple providers, sandbox, sessions, skills, and more.

## Installation

```bash
npm install -g mothx-installer
```

## Quick Start

```bash
# Set API key
export DEEPSEEK_API_KEY=sk-...

# Start interactive mode
mothx

# Non-interactive (print mode)
mothx -P "Write a hello world in Go"

# Specify provider and model
mothx --provider deepseek-openai --model deepseek-v4-flash

# Change mode
mothx --mode plan    # Read-only planning
mothx --mode agent   # Standard
mothx --mode yolo    # Full access (default)
```

## Features

- ⚙️ **Workflow Mode**: Dynamic JavaScript workflows with phases, parallel execution, and multi-worker coordination
- 🤖 **Multi-Provider**: DeepSeek, OpenAI, Anthropic, Volcengine/Doubao, 20+ vendor adapters
- ⚡ **SSE Streaming**: Real-time token delivery
- 🧠 **Think Mode**: Extended reasoning for complex problems
- 🛡️ **Sandbox**: bwrap process isolation (Linux)
- 📝 **Sessions**: Persistent SQLite-backed history with branching
- 🧩 **Skills**: Reusable prompt snippets
- 💻 **IDE Integration**: ACP protocol for VS Code, JetBrains
- 🌐 **Gateway**: OpenAI-compatible HTTP API
- 📱 **Messaging**: WeChat, Feishu, WebSocket chatbot
- 🤝 **Multi-Agent**: Delegate subtasks, A2A protocol

## Configuration

```bash
# DeepSeek (default)
export DEEPSEEK_API_KEY=sk-...

# OpenAI
export OPENAI_API_KEY=sk-...

# Anthropic
export ANTHROPIC_API_KEY=sk-ant-...
```

Or configure in `~/.mothx/settings.json`:

```json
{
  "defaultProvider": "deepseek-openai",
  "defaultModel": "deepseek-v4-flash"
}
```

## More Information

- **GitHub**: [github.com/oschina/mothx](https://github.com/oschina/mothx)
- **Gitee (国内)**: [gitee.com/oschina/mothx](https://gitee.com/oschina/mothx)
- **Documentation**: [docs](https://github.com/oschina/mothx/tree/main/docs)

## Uninstall

```bash
# npm
npm uninstall -g mothx-installer

# Linux/macOS (one-line install)
curl -fsSL https://mothx.net/install.sh | bash -s -- --uninstall

# Windows (one-line install)
npm uninstall -g mothx-installer
```

## License

MIT
