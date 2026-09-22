# 🚀 MothX 5-Minute Quick Start

> Skip the long docs — let's get you coding!

## Step 1: Install (30 seconds)

```bash
# Option 1: npm (recommended, auto-downloads correct binary)
npm install -g mothx-installer

# Option 2: PyPI
pipx install mothx-installer

# Option 3: One-line install (Linux/macOS)
curl -fsSL https://mothx.net/install.sh | bash
```

```bat
:: Option 4: One-line install (Windows Command Prompt)
curl.exe -fsSL https://mothx.net/install.bat -o install.bat && install.bat
```

```bash
# Option 5: Go install
go install github.com/oschina/mothx/cmd/mothx@latest
```

**Uninstall:**

```bash
# npm
npm uninstall -g mothx-installer

# PyPI
pipx uninstall mothx-installer

# Linux/macOS (one-line install)
curl -fsSL https://mothx.net/install.sh | bash -s -- --uninstall
```

## Step 2: Configure API Key (30 seconds)

```bash
# Set DeepSeek API key (default provider)
export DEEPSEEK_API_KEY=sk-...

# Or use OpenAI
export OPENAI_API_KEY=sk-...

# Or use Anthropic
export ANTHROPIC_API_KEY=sk-ant-...
```

## Step 3: Run! (10 seconds)

```bash
# Start interactive session
mothx

# Or ask directly
mothx -P "Hello, MothX!"
```

That's it — you're coding with AI!

---

## 🎮 Three Modes, Switch Anytime

| Mode | Command | Use Case |
|------|---------|----------|
| 🗒️ **Plan** | `mothx --mode plan` | Read-only analysis, safe exploration |
| 🔧 **Agent** | `mothx --mode agent` | Standard development |
| 🚀 **YOLO** | `mothx --mode yolo` | Full freedom, no restrictions (default) |

In interactive mode, press `Tab` or type `/mode plan|agent|yolo` to switch.

---

## 💡 Common Use Cases

### 📝 Code Generation
```bash
mothx -P "Write a Go HTTP server with RESTful API"
mothx -P "Create a Python scraper for news headlines"
mothx -P "Generate a React component with search box and list"
```

### 🔍 Code Understanding
```bash
mothx -P "Explain what main.go does"
mothx -P "What does this regex mean?"
mothx -P "Analyze the architecture of this project"
```

### 🛠️ Code Refactoring
```bash
mothx -P "Refactor this function to use generics"
mothx -P "Optimize this code for performance"
mothx -P "Split this class into smaller modules"
```

### 🧪 Test Generation
```bash
mothx -P "Write unit tests for UserService"
mothx -P "Generate integration test cases"
mothx -P "Write an end-to-end test"
```

### 📚 Documentation
```bash
mothx -P "Generate JSDoc comments for this function"
mothx -P "Write a README.md"
mothx -P "Generate API documentation"
```

---

## ⌨️ Keyboard Shortcuts

| Shortcut | Function |
|----------|----------|
| `Enter` | Submit prompt |
| `Alt+Enter` | New line |
| `Tab` | Switch mode |
| `Esc` | Abort current operation |
| `Ctrl+O` | Open tool details |
| `Ctrl+G` | Toggle simple/full event display (simple by default) |
| `Up/Down` | Browse history |
| `Ctrl+C` | Cancel/clear input |

---

## 🔧 Common Commands

```bash
/mode plan      # Switch to Plan mode
/mode agent     # Switch to Agent mode
/mode yolo      # Switch to YOLO mode
/model          # Show current model
/think          # Toggle thinking level
/clear          # Clear conversation
/help           # Show help
/quit           # Exit
```

---

## 🎯 Real-World Examples

### Example 1: Create a New Project
```bash
mothx -P "Create an Express.js project with user auth and database connection"
```

### Example 2: Debug Code
```bash
mothx -P "This code throws TypeError: Cannot read property 'map' of undefined, help me fix it"
```

### Example 3: Code Review
```bash
mothx --mode plan "Review the code in current directory, find potential issues"
```

### Example 4: Generate Config Files
```bash
mothx -P "Generate a Docker Compose file with Node.js, PostgreSQL, and Redis"
```

### Example 5: Write Regex
```bash
mothx -P "Write a regex to match Chinese phone numbers"
```

---

## 🛡️ Safety Tips

- **Plan mode**: Read-only, won't modify files — great for exploration
- **Agent mode**: Read/write, but bash commands require approval (configurable whitelist)
- **YOLO mode**: Full freedom — use with caution!

By default, dangerous commands (like `rm -rf`, `sudo`) are blocked by the blacklist.

---

## 📱 Advanced Usage

### IDE Integration
```bash
# VS Code: Add to settings.json
{
  "acp.agents": {
    "mothx": {
      "command": "mothx",
      "args": ["acp", "--mode", "agent"]
    }
  }
}
```

### API Server
```bash
# Start OpenAI-compatible HTTP server
mothx serve
```

### Chatbot
```bash
# Deploy as WeChat/Feishu bot
mothx serve
```

---

## 🆘 Need Help?

```bash
# Run diagnostics
mothx doctor

# Show help
mothx --help

# Enable debug mode
mothx --debug
```

---

## 📖 More Resources

- [Full Documentation](../../README.md) — All features explained
- [Configuration Guide](configuration.md) — Customize settings
- [Tool Reference](tools.md) — All built-in tools
- [Scenarios & Walkthroughs](scenarios.md) — More real-world examples
- [FAQ](faq.md) — Common questions answered

---

<p align="center">
  <strong>🎉 Congratulations! You've mastered the basics of MothX.</strong><br>
  <strong>Now, start your AI coding journey!</strong>
</p>
