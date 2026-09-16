# 常见问题 (FAQ)

> 来自真实用户的提问和解决方案

---

## 🚀 安装与启动

### Q: 安装后运行 `mothx` 提示 "command not found"

**A:** 这通常是因为安装路径不在 PATH 环境变量中。

```bash
# 检查安装位置
which mothx || where mothx

# 如果使用 npm 安装，检查 npm 全局路径
npm root -g

# 解决方案：将安装路径添加到 PATH
# Linux/macOS (添加到 ~/.bashrc 或 ~/.zshrc)
export PATH="$HOME/.local/bin:$PATH"

# 或者重新安装到系统路径
sudo npm install -g mothx-installer
```

### Q: npm 安装失败，提示权限错误

**A:** 不要使用 sudo 安装 npm 全局包，改用以下方式：

```bash
# 方案一：修改 npm 全局目录
mkdir -p ~/.npm-global
npm config set prefix '~/.npm-global'
export PATH=~/.npm-global/bin:$PATH

# 方案二：使用 npx 直接运行
npx mothx

# 方案三：使用一键安装脚本（Linux/macOS）
curl -fsSL https://mothx.net/install.sh | bash
```

```bat
:: Windows 命令提示符
curl.exe -fsSL https://mothx.net/install.bat -o install.bat && install.bat
```

### Q: 启动后没有任何反应，光标闪烁

**A:** 可能是终端不兼容或 TUI 渲染问题：

```bash
# 方案一：使用 print 模式（非交互）
mothx -P "hello"

# 方案二：检查终端支持
echo $TERM  # 应该是 xterm-256color 或类似

# 方案三：尝试其他终端
# 推荐：iTerm2 (macOS), Windows Terminal (Windows), Alacritty (Linux)
```

---

## 🔑 API 密钥与连接

### Q: 提示 "API key not found" 或 "Unauthorized"

**A:** 检查 API 密钥配置：

```bash
# 方案一：使用环境变量
export DEEPSEEK_API_KEY=sk-...
# 或
export OPENAI_API_KEY=sk-...

# 方案二：检查配置文件
cat ~/.mothx/settings.json

# 方案三：运行诊断
mothx doctor
```

### Q: 提示 "connection timeout" 或 "network error"

**A:** 网络连接问题，常见于国内用户访问国外 API：

```bash
# 方案一：使用代理
export HTTPS_PROXY=http://127.0.0.1:7890

# 方案二：使用国内提供商
# 在 settings.json 中配置 DeepSeek（国内可直连）
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

# 方案三：检查 DNS
nslookup api.deepseek.com
```

### Q: 提示 "rate limit exceeded"

**A:** API 调用频率超限：

```bash
# 方案一：等待几分钟后重试

# 方案二：配置重试机制
{
  "retry": {
    "enabled": true,
    "maxRetries": 5,
    "baseDelayMs": 3000
  }
}

# 方案三：切换到其他提供商
mothx --provider openai --model gpt-4o
```

---

## 💰 成本与计费

### Q: 使用 MothX 会花多少钱？

**A:** MothX 本身免费开源，费用来自 LLM API 调用：

| 提供商 | 大致价格（每百万 token） |
|--------|------------------------|
| DeepSeek V4 Flash | ¥1-2 |
| DeepSeek V4 Pro | ¥4-8 |
| GPT-4o | $2.5-10 |
| Claude Sonnet | $3-15 |

**省钱技巧：**
- 使用 `deepseek-v4-flash`（默认）最便宜
- 开启缓存命中优化（自动）
- 使用 `/compact` 压缩上下文
- 避免发送过长的代码文件

### Q: 如何查看 token 使用量？

**A:** TUI 底部状态栏会显示：
- 缓存命中率
- 当前轮 token 使用量
- 累计 token 使用量

```bash
# 使用 debug 模式查看详细信息
mothx --debug
```

### Q: 如何降低 API 成本？

**A:**

1. **使用便宜的模型**：`deepseek-v4-flash` 性价比最高
2. **开启缓存**：重复的 prompt 前缀会被缓存
3. **压缩上下文**：使用 `/compact` 命令
4. **限制输出长度**：为对应厂商的模型设置 `maxTokens`
5. **使用 Plan 模式**：先分析再执行，减少无效调用

---

## 🎮 使用模式

### Q: Plan、Agent、YOLO 模式有什么区别？

**A:**

| 模式 | 文件操作 | 网络 | Bash 审批 | 适用场景 |
|------|---------|------|----------|---------|
| Plan | 只读 | ❌ | N/A | 分析代码、制定计划 |
| Agent | 读写 | ❌ | 需要 | 日常开发（推荐） |
| YOLO | 完全 | ✅ | 不需要 | 系统管理、自动化 |

**建议：** 日常使用 Agent 模式，需要网络或系统操作时临时切换 YOLO。

### Q: 为什么 Agent 模式下 bash 命令被拒绝？

**A:** Agent 模式默认需要审批 bash 命令：

```bash
# 方案一：交互时输入 y 批准

# 方案二：配置白名单（自动批准）
{
  "approval": {
    "bashWhitelist": ["go ", "make ", "git ", "npm ", "ls ", "cat "],
    "bashBlacklist": ["rm -rf", "sudo"]
  }
}

# 方案三：切换到 YOLO 模式
/mode yolo
```

### Q: 如何让 AI 只读代码不修改？

**A:** 使用 Plan 模式：

```bash
# 命令行
mothx --mode plan

# 交互式
/mode plan

# 或按 Tab 键循环切换
```

---

## 🧠 模型与思考

### Q: 什么时候该用思考模式？

**A:**

| 场景 | 推荐思考级别 |
|------|-------------|
| 简单问答 | off |
| 代码生成 | low - medium |
| 复杂重构 | high |
| 架构设计 | xhigh |
| 调试 bug | medium |

```bash
# 切换思考级别
/think          # 循环切换
Tab             # 快捷键
mothx -t high  # 命令行指定
```

### Q: 思考模式为什么没有效果？

**A:** 不是所有模型都支持思考模式：

```bash
# 支持思考模式的模型
mothx --provider deepseek --model deepseek-v4-pro -t high
mothx --provider openai --model o1 -t high
mothx --provider anthropic --model claude-3-5-sonnet -t high

# 不支持的模型会忽略思考参数
mothx --model deepseek-v4-flash -t high  # 无效
```

### Q: 如何切换到其他模型？

**A:**

```bash
# 临时切换
mothx --provider openai --model gpt-4o

# 交互式切换
/model gpt-4o
/model  # 查看可用模型

# 永久修改默认模型
# 编辑 ~/.mothx/settings.json
{
  "defaultProvider": "openai",
  "defaultModel": "gpt-4o"
}
```

---

## 📝 会话管理

### Q: 对话太长，AI 开始遗忘上下文怎么办？

**A:**

```bash
# 方案一：压缩上下文（保留关键信息）
/compact

# 方案二：开启自动压缩
{
  "compaction": {
    "enabled": true,
    "reserveTokens": 16384,
    "keepRecentTokens": 20000
  }
}

# 方案三：开启新会话
/clear
```

### Q: 如何恢复之前的对话？

**A:**

```bash
# 继续最近的会话
mothx -c

# 列出所有会话
/sessions

# 恢复特定会话
/sessions set abc123

# 或命令行
mothx --resume abc123
```

### Q: 会话存储占用太多空间怎么办？

**A:**

```bash
# 查看会话大小
du -sh ~/.mothx/sessions/

# 删除旧会话（同时删除句柄文件和 SQLite 记录）
/sessions del abc123

# 手动清理前先备份完整 session 根目录，其中包含 sessions.db
cp -a ~/.mothx/sessions ~/backups/sessions
```

### Q: 启动时提示 “Database migration error”（数据库迁移失败）

**A:**

当 `sessions.db` 的 schema 已经无法被当前版本迁移时，MothX 不会卡死：它会先把旧数据库完整备份到同一目录下的
`sessions.db.migration-failed-<时间戳>.bak`，再新建一个空的 `sessions.db`，并在启动界面和日志里明确提示迁移失败。

- 旧会话数据都在备份文件里，没有被删除；新库是空的，可以立即继续使用。
- 其他正在共享同一会话目录的 mothx 进程（本机 UDP 广播）会收到 `database_rebuilt` 通知并自动丢弃旧的数据库句柄，不会继续往已被替换的文件里写；广播只在 loopback 网络上发送，不会离开本机。
- 想恢复旧数据：先修复迁移失败的原因（例如用旧版本 MothX 打开该库），再把 `.bak` 改回 `sessions.db`。
- 确认不再需要旧数据后，可以直接删除 `.bak` 文件。

---

## 🛠️ 工具使用

### Q: AI 执行命令被拒绝，提示 "permission denied"

**A:** 检查文件权限和沙箱设置：

```bash
# 方案一：检查文件权限
ls -la <file>

# 方案二：检查沙箱配置
mothx doctor

# 方案三：临时禁用沙箱
mothx --no-sandbox

# 方案四：切换到 YOLO 模式
/mode yolo
```

### Q: grep/find 工具找不到结果

**A:** 可能是路径或模式问题：

```bash
# 确保在项目目录下运行
cd /path/to/your/project
mothx

# 使用绝对路径
mothx -P "在 /path/to/project 中搜索 TODO"

# 检查 .gitignore 是否排除了目标文件
```

### Q: 如何让 AI 不要自动执行命令？

**A:**

```bash
# 方案一：使用 Plan 模式
/mode plan

# 方案二：在 prompt 中明确说明
mothx -P "分析这段代码，不要执行任何命令"

# 方案三：配置审批
{
  "approval": {
    "bashWhitelist": [],
    "confirmBeforeWrite": true
  }
}
```

---

## 🔒 安全与隐私

### Q: MothX 会上传我的代码吗？

**A:** MothX 本身不会上传代码，但会将你的 prompt 发送到配置的 LLM API：

- **本地处理**：文件读取、工具执行都在本地
- **API 调用**：prompt 内容会发送到 LLM 提供商
- **建议**：不要在 prompt 中包含敏感信息（密码、密钥等）

### Q: 如何防止 AI 删除重要文件？

**A:**

```bash
# 方案一：使用 Plan 模式（只读）
/mode plan

# 方案二：配置黑名单
{
  "approval": {
    "bashBlacklist": ["rm -rf", "rm -r", "sudo"]
  }
}

# 方案三：使用 Git 版本控制
git add -A && git commit -m "backup before AI changes"

# 方案四：启用沙箱
mothx --sandbox
```

### Q: 沙箱模式不工作？

**A:** 沙箱仅支持 Linux：

```bash
# 检查是否安装 bubblewrap
bwrap --version

# 安装
sudo apt install bubblewrap      # Debian/Ubuntu
sudo dnf install bubblewrap      # Fedora
sudo pacman -S bubblewrap        # Arch

# macOS/Windows 用户可以使用 WSL2
```

---

## 💻 IDE 集成

### Q: VS Code 中看不到 MothX

**A:** 检查 ACP 配置：

```json
// .vscode/settings.json 或全局 settings.json
{
  "acp.agents": {
    "mothx": {
      "command": "mothx",
      "args": ["acp", "--mode", "agent"]
    }
  }
}
```

确保：
1. MothX 已安装并在 PATH 中
2. VS Code 版本支持 ACP
3. 重启 VS Code

### Q: JetBrains IDE 集成不工作

**A:**

1. 打开 `Settings → Tools → ACP Agents`
2. 添加 Agent：
   - Name: `MothX`
   - Command: `mothx`
   - Arguments: `acp --mode agent`
3. 点击 Test 验证连接
4. 重启 IDE

---

## 🌐 Serve 模式

### Q: 如何将 MothX 作为 API 服务器？

**A:**

```bash
# 启动 Serve
mothx serve

# 配置文件 ~/.mothx/serve.json
{
  "port": 8080,
  "auth": {
    "token": "your-secret-token"
  }
}

# 调用 API
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer your-secret-token" \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek-v4-flash","messages":[{"role":"user","content":"hello"}]}'
```

### Q: Serve 支持哪些 API？

**A:** 兼容 OpenAI Chat Completions API：

- `/v1/chat/completions` - 聊天补全
- `/v1/models` - 模型列表
- 支持 SSE 流式响应
- 支持所有配置的提供商和模型

---

## 📱 消息平台

### Q: 如何部署为微信/飞书机器人？

**A:**

```bash
# 启动 Serve 模式
mothx serve

# 配置文件 ~/.mothx/serve.json
{
  "platform": "wechat",  // 或 "feishu"
  "appId": "your-app-id",
  "appSecret": "your-app-secret",
  "defaultProvider": "deepseek"
}
```

详见 [Serve 模式](serve.md) 文档。

---

## 🔧 故障排查

### Q: 运行 `mothx doctor` 报错

**A:** doctor 命令会检查：

```bash
mothx doctor
```

常见问题：
- **Config**: 配置文件格式错误 → 检查 JSON 语法。正常启动时，MothX 会将无效的 `settings.json` 备份为 `.bak_<时间戳>`，并在 stderr 中打印其绝对路径；可从该备份恢复有效配置。如果错误中包含 `backup failed`，请检查文件及目录权限或文件系统是否为只读。
- **Provider**: API 密钥缺失或无效 → 重新配置
- **Sandbox**: bubblewrap 未安装 → 安装或忽略
- **MCP**: MCP 服务器配置错误 → 检查 mcp.json

### Q: 如何查看详细日志？

**A:**

```bash
# 启用 debug 模式
mothx --debug

# 日志会显示
- API 请求/响应详情
- 工具执行过程
- 错误堆栈信息
```

### Q: 提示 "context window exceeded"

**A:** 上下文超出模型限制：

```bash
# 方案一：压缩上下文
/compact

# 方案二：开启自动压缩
{
  "compaction": {
    "enabled": true
  }
}

# 方案三：使用更大上下文的模型
mothx --model deepseek-v4-pro  # 1M context

# 方案四：清除对话重新开始
/clear
```

### Q: 工具执行卡住不动

**A:**

```bash
# 方案一：按 Esc 中止当前操作

# 方案二：检查是否有交互式命令阻塞
# 例如：git push 可能需要输入密码

# 方案三：配置非交互模式
{
  "env": {
    "GIT_TERMINAL_PROMPT": "0",
    "DEBIAN_FRONTEND": "noninteractive"
  }
}
```

---

## 🆚 与其他工具对比

### Q: MothX 和 Claude Code 有什么区别？

**A:**

| 特性 | MothX | Claude Code |
|------|-----------|-------------|
| 价格 | 免费开源 | 付费 |
| 模型 | 多提供商 | 仅 Anthropic |
| 沙箱 | ✅ bwrap | ❌ |
| 会话管理 | ✅ 完整 | 有限 |
| IDE 集成 | ✅ ACP | ✅ |
| 消息平台 | ✅ 微信/飞书 | ❌ |
| Serve | ✅ OpenAI 兼容 | ❌ |

### Q: MothX 和 Cursor 有什么区别？

**A:**

- **MothX**：终端工具，轻量级，适合命令行用户
- **Cursor**：IDE，图形界面，适合喜欢 GUI 的用户

选择建议：
- 喜欢终端 → MothX
- 喜欢图形界面 → Cursor
- 需要 API 服务 → MothX Serve

---

## 📚 更多资源

- [5 分钟快速上手](quick-start-tutorial.md)
- [核心特性详解](features-overview.md)
- [使用场景](use-cases.md)
- [配置详解](configuration.md)
- [工具参考](tools.md)

---

<p align="center">
  <strong>还有问题？在 GitHub 上提问！</strong><br>
  <a href="https://gitee.com/startvibecoding/mothx/issues">Gitee Issues</a> · <a href="https://gitee.com/startvibecoding/mothx">Gitee 仓库</a>
</p>
