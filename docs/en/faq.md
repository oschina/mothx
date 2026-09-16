# Frequently Asked Questions (FAQ)

> Real questions from real users, with practical solutions

---

## 🚀 Installation & Startup

### Q: After installation, running `mothx` shows "command not found"

**A:** This usually means the installation path isn't in your PATH environment variable.

```bash
# Check installation location
which mothx || where mothx

# If installed via npm, check npm global path
npm root -g

# Solution: Add installation path to PATH
# Linux/macOS (add to ~/.bashrc or ~/.zshrc)
export PATH="$HOME/.local/bin:$PATH"

# Or reinstall to system path
sudo npm install -g mothx-installer
```

### Q: npm installation fails with permission error

**A:** Don't use sudo for npm global packages. Try these alternatives:

```bash
# Option 1: Change npm global directory
mkdir -p ~/.npm-global
npm config set prefix '~/.npm-global'
export PATH=~/.npm-global/bin:$PATH

# Option 2: Use npx to run directly
npx mothx

# Option 3: Use the one-line installer (Linux/macOS)
curl -fsSL https://mothx.net/install.sh | bash
```

```bat
:: Windows Command Prompt
curl.exe -fsSL https://mothx.net/install.bat -o install.bat && install.bat
```

### Q: After starting, nothing happens, cursor just blinks

**A:** This could be a terminal compatibility or TUI rendering issue:

```bash
# Option 1: Use print mode (non-interactive)
mothx -P "hello"

# Option 2: Check terminal support
echo $TERM  # Should be xterm-256color or similar

# Option 3: Try a different terminal
# Recommended: iTerm2 (macOS), Windows Terminal (Windows), Alacritty (Linux)
```

---

## 🔑 API Keys & Connection

### Q: Shows "API key not found" or "Unauthorized"

**A:** Check your API key configuration:

```bash
# Option 1: Use environment variables
export DEEPSEEK_API_KEY=sk-...
# or
export OPENAI_API_KEY=sk-...

# Option 2: Check config file
cat ~/.mothx/settings.json

# Option 3: Run diagnostics
mothx doctor
```

### Q: Shows "connection timeout" or "network error"

**A:** Network connection issues, common for users behind firewalls:

```bash
# Option 1: Use proxy
export HTTPS_PROXY=http://127.0.0.1:7890

# Option 2: Use a provider that's accessible from your region
# Configure in settings.json
{
  "providers": {
    "deepseek": {
      "vendor": "deepseek",
      "api": "openai-chat",
      "baseUrl": "https://api.deepseek.com",
      "apiKey": "sk-..."
    }
  },
  "defaultProvider": "deepseek"
}

# Option 3: Check DNS
nslookup api.deepseek.com
```

### Q: Shows "rate limit exceeded"

**A:** API call frequency limit exceeded:

```bash
# Option 1: Wait a few minutes and retry

# Option 2: Configure retry mechanism
{
  "retry": {
    "enabled": true,
    "maxRetries": 5,
    "baseDelayMs": 3000
  }
}

# Option 3: Switch to another provider
mothx --provider openai --model gpt-4o
```

---

## 💰 Cost & Billing

### Q: How much does it cost to use MothX?

**A:** MothX itself is free and open source. Costs come from LLM API calls:

| Provider | Approximate Price (per million tokens) |
|----------|---------------------------------------|
| DeepSeek V4 Flash | $0.15-0.30 |
| DeepSeek V4 Pro | $0.60-1.20 |
| GPT-4o | $2.50-10 |
| Claude Sonnet | $3-15 |

**Cost-saving tips:**
- Use `deepseek-v4-flash` (default) for best value
- Enable cache hit optimization (automatic)
- Use `/compact` to compress context
- Avoid sending very long code files

### Q: How to check token usage?

**A:** The TUI status bar shows:
- Cache hit rate
- Current turn token usage
- Cumulative token usage

```bash
# Use debug mode for detailed info
mothx --debug
```

### Q: How to reduce API costs?

**A:**

1. **Use cheaper models**: `deepseek-v4-flash` has the best value
2. **Enable caching**: Repeated prompt prefixes are cached
3. **Compress context**: Use `/compact` command
4. **Limit output length**: Set `maxTokens` for the relevant provider model
5. **Use Plan mode**: Analyze first, then execute

---

## 🎮 Usage Modes

### Q: What's the difference between Plan, Agent, and YOLO modes?

**A:**

| Mode | File Ops | Network | Bash Approval | Use Case |
|------|----------|---------|---------------|----------|
| Plan | Read-only | ❌ | N/A | Code analysis, planning |
| Agent | Read/Write | ❌ | Required | Daily development (recommended) |
| YOLO | Full | ✅ | Not required | System admin, automation |

**Recommendation:** Use Agent mode for daily work, switch to YOLO when you need network or system operations.

### Q: Why are bash commands rejected in Agent mode?

**A:** Agent mode requires approval for bash commands by default:

```bash
# Option 1: Type 'y' to approve when prompted

# Option 2: Configure whitelist (auto-approve)
{
  "approval": {
    "bashWhitelist": ["go ", "make ", "git ", "npm ", "ls ", "cat "],
    "bashBlacklist": ["rm -rf", "sudo"]
  }
}

# Option 3: Switch to YOLO mode
/mode yolo
```

### Q: How to make AI only read code, not modify it?

**A:** Use Plan mode:

```bash
# Command line
mothx --mode plan

# Interactive
/mode plan

# Or press Tab to cycle through modes
```

---

## 🧠 Models & Thinking

### Q: When should I use thinking mode?

**A:**

| Scenario | Recommended Thinking Level |
|----------|---------------------------|
| Simple Q&A | off |
| Code generation | low - medium |
| Complex refactoring | high |
| Architecture design | xhigh |
| Debugging | medium |

```bash
# Switch thinking level
/think          # Cycle through levels
Tab             # Keyboard shortcut
mothx -t high  # Specify on command line
```

### Q: Why doesn't thinking mode work?

**A:** Not all models support thinking mode:

```bash
# Models that support thinking mode
mothx --provider deepseek --model deepseek-v4-pro -t high
mothx --provider openai --model o1 -t high
mothx --provider anthropic --model claude-3-5-sonnet -t high

# Unsupported models ignore thinking parameters
mothx --model deepseek-v4-flash -t high  # No effect
```

### Q: How to switch to a different model?

**A:**

```bash
# Temporary switch
mothx --provider openai --model gpt-4o

# Interactive switch
/model gpt-4o
/model  # View available models

# Permanent change to default model
# Edit ~/.mothx/settings.json
{
  "defaultProvider": "openai",
  "defaultModel": "gpt-4o"
}
```

---

## 📝 Session Management

### Q: Conversation is too long, AI starts forgetting context

**A:**

```bash
# Option 1: Compress context (keeps key info)
/compact

# Option 2: Enable auto-compaction
{
  "compaction": {
    "enabled": true,
    "reserveTokens": 16384,
    "keepRecentTokens": 20000
  }
}

# Option 3: Start a new session
/clear
```

### Q: How to resume a previous conversation?

**A:**

```bash
# Continue most recent session
mothx -c

# List all sessions
/sessions

# Switch to specific session
/sessions set abc123

# Or via command line
mothx --resume abc123
```

### Q: Session storage is taking up too much space

**A:**

```bash
# Check session sizes
du -sh ~/.mothx/sessions/

# Delete old sessions (removes both handle file and SQLite records)
/sessions del abc123

# Back up the whole session root before manual cleanup; it contains sessions.db
cp -a ~/.mothx/sessions ~/backups/sessions
```

### Q: Shows "Database migration error" on startup

**A:**

When `sessions.db` can no longer be migrated by the current version, MothX does not get stuck: it first backs the
old database up next to it as `sessions.db.migration-failed-<timestamp>.bak`, creates a new empty `sessions.db`, and
reports the migration failure in the startup notice and the logs.

- The previous sessions are all in the backup file; nothing was deleted, and the new database is usable immediately.
- Other mothx processes sharing that session directory receive a local UDP notice (`database_rebuilt`) and drop their old database handles, so they stop writing to the replaced file; the broadcast stays on the loopback network and never leaves the host.
- To get the old data back, fix the cause of the migration failure (for example open the database with the older MothX build), then rename the `.bak` file back to `sessions.db`.
- Once the old data is no longer needed, delete the `.bak` file.

---

## 🛠️ Tool Usage

### Q: AI command execution rejected, shows "permission denied"

**A:** Check file permissions and sandbox settings:

```bash
# Option 1: Check file permissions
ls -la <file>

# Option 2: Check sandbox configuration
mothx doctor

# Option 3: Temporarily disable sandbox
mothx --no-sandbox

# Option 4: Switch to YOLO mode
/mode yolo
```

### Q: grep/find tools return no results

**A:** Could be path or pattern issues:

```bash
# Make sure you're in the project directory
cd /path/to/your/project
mothx

# Use absolute paths
mothx -P "Search for TODO in /path/to/project"

# Check if .gitignore is excluding target files
```

### Q: How to prevent AI from automatically executing commands?

**A:**

```bash
# Option 1: Use Plan mode (read-only)
/mode plan

# Option 2: Be explicit in your prompt
mothx -P "Analyze this code, don't execute any commands"

# Option 3: Configure approval
{
  "approval": {
    "bashWhitelist": [],
    "confirmBeforeWrite": true
  }
}
```

---

## 🔒 Security & Privacy

### Q: Does MothX upload my code?

**A:** MothX itself doesn't upload code, but it sends your prompts to the configured LLM API:

- **Local processing**: File reads, tool execution happen locally
- **API calls**: Prompt content is sent to LLM providers
- **Recommendation**: Don't include sensitive info (passwords, keys) in prompts

### Q: How to prevent AI from deleting important files?

**A:**

```bash
# Option 1: Use Plan mode (read-only)
/mode plan

# Option 2: Configure blacklist
{
  "approval": {
    "bashBlacklist": ["rm -rf", "rm -r", "sudo"]
  }
}

# Option 3: Use Git version control
git add -A && git commit -m "backup before AI changes"

# Option 4: Enable sandbox
mothx --sandbox
```

### Q: Sandbox mode not working?

**A:** Sandbox is Linux-only:

```bash
# Check if bubblewrap is installed
bwrap --version

# Install
sudo apt install bubblewrap      # Debian/Ubuntu
sudo dnf install bubblewrap      # Fedora
sudo pacman -S bubblewrap        # Arch

# macOS/Windows users can use WSL2
```

---

## 💻 IDE Integration

### Q: Can't see MothX in VS Code

**A:** Check ACP configuration:

```json
// .vscode/settings.json or global settings.json
{
  "acp.agents": {
    "mothx": {
      "command": "mothx",
      "args": ["acp", "--mode", "agent"]
    }
  }
}
```

Make sure:
1. MothX is installed and in PATH
2. VS Code version supports ACP
3. Restart VS Code

### Q: JetBrains IDE integration not working

**A:**

1. Open `Settings → Tools → ACP Agents`
2. Add Agent:
   - Name: `MothX`
   - Command: `mothx`
   - Arguments: `acp --mode agent`
3. Click Test to verify connection
4. Restart IDE

---

## 🌐 Serve Mode

### Q: How to use MothX as an API server?

**A:**

```bash
# Start Serve
mothx serve

# Config file ~/.mothx/serve.json
{
  "port": 8080,
  "auth": {
    "token": "your-secret-token"
  }
}

# Call API
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer your-secret-token" \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek-v4-flash","messages":[{"role":"user","content":"hello"}]}'
```

### Q: What APIs does Serve support?

**A:** Compatible with OpenAI Chat Completions API:

- `/v1/chat/completions` - Chat completion
- `/v1/models` - Model list
- Supports SSE streaming
- Supports all configured providers and models

---

## 📱 Messaging Platforms

### Q: How to deploy as a WeChat/Feishu bot?

**A:**

```bash
# Start Channels mode
mothx serve

# Config file ~/.mothx/serve.json
{
  "platform": "wechat",  // or "feishu"
  "appId": "your-app-id",
  "appSecret": "your-app-secret",
  "defaultProvider": "deepseek"
}
```

See [Serve Mode](serve.md) documentation for details.

---

## 🔧 Troubleshooting

### Q: `mothx doctor` shows errors

**A:** The doctor command checks:

```bash
mothx doctor
```

Common issues:
- **Config**: Config file format error → Check the JSON syntax. During normal startup, MothX backs up an invalid `settings.json` to `.bak_<timestamp>` and prints its absolute path to stderr. You can recover valid values from that backup. If the error includes `backup failed`, check file and directory permissions or whether the filesystem is read-only.
- **Provider**: API key missing or invalid → Reconfigure
- **Sandbox**: bubblewrap not installed → Install or ignore
- **MCP**: MCP server config error → Check mcp.json

### Q: How to view detailed logs?

**A:**

```bash
# Enable debug mode
mothx --debug

# Logs will show
- API request/response details
- Tool execution process
- Error stack traces
```

### Q: Shows "context window exceeded"

**A:** Context exceeds model limit:

```bash
# Option 1: Compress context
/compact

# Option 2: Enable auto-compaction
{
  "compaction": {
    "enabled": true
  }
}

# Option 3: Use a model with larger context
mothx --model deepseek-v4-pro  # 1M context

# Option 4: Clear conversation and start fresh
/clear
```

### Q: Tool execution hangs

**A:**

```bash
# Option 1: Press Esc to abort current operation

# Option 2: Check for interactive commands blocking
# Example: git push may require password input

# Option 3: Configure non-interactive mode
{
  "env": {
    "GIT_TERMINAL_PROMPT": "0",
    "DEBIAN_FRONTEND": "noninteractive"
  }
}
```

---

## 🆚 Comparison with Other Tools

### Q: What's the difference between MothX and Claude Code?

**A:**

| Feature | MothX | Claude Code |
|---------|-----------|-------------|
| Price | Free & Open Source | Paid |
| Models | Multi-provider | Anthropic only |
| Sandbox | ✅ bwrap | ❌ |
| Session Management | ✅ Full | Limited |
| IDE Integration | ✅ ACP | ✅ |
| Messaging | ✅ WeChat/Feishu | ❌ |
| Serve | ✅ OpenAI compatible | ❌ |

### Q: What's the difference between MothX and Cursor?

**A:**

- **MothX**: Terminal tool, lightweight, great for CLI users
- **Cursor**: IDE with GUI, great for users who prefer graphical interfaces

Choose based on:
- Prefer terminal → MothX
- Prefer GUI → Cursor
- Need API service → MothX Serve

---

## 📚 More Resources

- [5-Minute Quick Start](quick-start-tutorial.md)
- [Features Overview](features-overview.md)
- [Use Cases](use-cases.md)
- [Configuration Guide](configuration.md)
- [Tool Reference](tools.md)

---

<p align="center">
  <strong>Still have questions? Ask on GitHub!</strong><br>
  <a href="https://github.com/startvibecoding/mothx/issues">GitHub Issues</a> · <a href="https://github.com/startvibecoding/mothx/discussions">GitHub Discussions</a>
</p>
