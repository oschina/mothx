# ✨ MothX 核心特性详解

> 一个工具，满足你所有的 AI 编程需求。

---

## 🤖 多提供商支持

### 支持的提供商

| 提供商 | 模型 | API 格式 |
|--------|------|----------|
| **DeepSeek**（默认） | deepseek-v4-flash, deepseek-v4-pro | OpenAI Chat / Anthropic Messages |
| **OpenAI** | GPT-4o, o1 等 | OpenAI Chat |
| **Anthropic** | Claude Sonnet, Opus 等 | Anthropic Messages |
| **厂商适配器** | Google Gemini、Google Vertex、小米、火山引擎/豆包、Kimi、MiniMax、Seed、Qianfan、Bailian、Gitee、阶跃星辰、OpenRouter、Together、Groq、Fireworks、Mistral、GitHub Copilot、Cloudflare AI Gateway、Cloudflare Workers AI、Amazon Bedrock、Z.AI、LongCat、AMD Radeon 等 | OpenAI Chat 或 Anthropic Messages |
| **自定义** | 任何兼容模型 | 通用 OpenAI Chat 或 Anthropic Messages fallback |

### 快速切换

```bash
# 使用 DeepSeek
mothx --provider deepseek-openai --model deepseek-v4-flash

# 使用 OpenAI
mothx --provider openai --model gpt-4o

# 使用 Anthropic
mothx --provider anthropic --model claude-3-5-sonnet-20241022
```

### 厂商适配器

MothX 支持 25+ 厂商适配器，包括：

- **国内**：小米、火山引擎/豆包、Kimi、MiniMax、Seed、Qianfan、Bailian、Gitee、阶跃星辰、Z.AI、LongCat、AMD Radeon
- **国际**：Google Gemini、Google Vertex、OpenRouter、Together、Groq、Fireworks、Mistral、GitHub Copilot、Cloudflare AI Gateway、Cloudflare Workers AI、Amazon Bedrock

无需额外配置，只需设置 API 密钥即可使用。

---

## ⚡ 极速响应

### SSE 流式传输

MothX 使用 SSE（Server-Sent Events）流式传输，实时返回 token，无需等待完整响应。

### 缓存命中优化

- **实时缓存命中率显示**：在 TUI 底部显示缓存命中百分比
- **每轮缓存统计**：每轮对话的缓存命中详情
- **统一缓存指标**：TUI 和 print 模式使用一致的统计口径

### 性能对比

| 特性 | MothX | 其他工具 |
|------|-----------|----------|
| 流式传输 | ✅ SSE | 部分支持 |
| 缓存命中率 | ✅ 实时显示 | ❌ 不显示 |
| Token 统计 | ✅ 统一口径 | ❌ 不一致 |

---

## 🧠 思考模式

### 支持的模型

- **DeepSeek**：deepseek-v4-pro（支持思考模式）
- **OpenAI**：o1 系列（支持思考模式）
- **Anthropic**：Claude 系列（支持思考模式）

### 思考级别

```bash
# 设置思考级别
mothx --thinking off      # 关闭思考
mothx --thinking minimal  # 最小思考
mothx --thinking low      # 低思考
mothx --thinking medium   # 中等思考
mothx --thinking high     # 高思考
mothx --thinking xhigh    # 超高思考
```

### 使用场景

- **复杂问题**：使用 `high` 或 `xhigh` 获得更深入的推理
- **简单问题**：使用 `off` 或 `minimal` 获得更快的响应
- **调试代码**：使用 `medium` 平衡速度和质量

---

## 🛡️ 沙箱隔离

### bwrap 进程级隔离

MothX 使用 [bubblewrap](https://github.com/containers/bubblewrap) 实现进程级隔离，确保安全执行。

### 三种模式

![三种安全模式](assets/image/modes-zh.webp)

| 模式 | 文件系统 | 网络 | 用途 |
|------|---------|------|------|
| **Plan** | 只读 | ❌ | 分析、规划 |
| **Agent** | 读写 | ❌ | 日常开发 |
| **YOLO** | 完全 | ✅ | 系统级操作 |

### 安全特性

- **bashBlacklist 优先**：黑名单命令（如 `rm -rf`、`sudo`）始终被拦截
- **审批门控**：Agent 模式下，bash 命令需要审批
- **`--print` 快速失败**：遇到需审批命令时直接失败，不会卡住

### 安装 bwrap

```bash
# Debian/Ubuntu
sudo apt install bubblewrap

# Fedora
sudo dnf install bubblewrap

# Arch
sudo pacman -S bubblewrap
```

---

## 📝 会话管理

### SQLite 持久化

MothX 使用 SQLite 存储会话历史。支持：

- **交互式选择器**：`/sessions` 打开对话框，支持上下导航、回车切换、`n` 新建、`d` 删除
- **延迟创建**：TUI 启动时延迟创建会话，直到用户发送第一条消息
- **分支**：从任意节点创建新分支
- **压缩**：自动压缩旧会话，节省空间
- **树形结构**：可视化会话树

### TUI 会话选择器

在 TUI 中使用 `/sessions` 打开交互式会话选择器：

- **Up/Down**：导航会话
- **Enter**：切换到选中的会话
- **n**：新建会话
- **d**：删除选中的会话
- **Esc**：关闭选择器

文本命令也可用：
```bash
/sessions ls        # 列出会话
/sessions set <id>  # 切换到会话
/sessions clear     # 重新开始
/sessions del <id>  # 删除会话
```

### 会话命令

```bash
# 继续最近的会话
mothx --continue

# 恢复特定会话
mothx --resume <session-id>

# 使用特定会话句柄文件
mothx --session <session-file.db>
```

### 会话存储位置

| 平台 | 位置 |
|------|------|
| Linux/macOS | `~/.mothx/sessions/` |
| Windows | `%APPDATA%\mothx\sessions\` |

---

## 🧩 技能系统

### 什么是技能？

技能是可复用的提示片段，用于：

- **项目约定**：编码规范、命名规则、架构模式
- **团队共享**：统一团队的开发风格
- **快速上手**：新成员快速了解项目规范

### 创建技能

```bash
# 全局技能（所有项目可用）
~/.mothx/skills/<name>/SKILL.md

# 项目技能（覆盖全局）
.skills/<name>/SKILL.md
```

### 使用技能

```bash
# 列出可用技能
> /skills

# 激活技能
> /skill my-conventions

# 加载技能引用
> /skill_ref my-references
```

### 在线 Skill 市场

MothX 兼容 SkillHub / ClawHub，支持：

- **技能安装**：一键安装在线技能
- **Cron 基础设施**：定时执行技能
- **社区共享**：分享你的技能

---

## 📊 统计面板

### 使用统计

MothX 内置统计面板，用于跟踪 token 使用量、请求数和费用。

```bash
# 启动 Web 面板（默认 127.0.0.1:7878）
mothx stats

# 在终端打印统计
mothx stats --cli

# 使用备用数据库
mothx stats --db ~/.mothx/sessions/sessions.db
```

### 功能特性

- **纯 HTML/CSS/JS**：无外部依赖，图表通过 `<canvas>` 绘制
- **总体概览**：请求数、token、费用、时长
- **时间序列图**：可视化使用趋势
- **按厂商/模型分类**：查看最常用的提供商和模型
- **协议与厂商分离**：区分厂商（公司）和协议（API 格式）
- **筛选**：按时间范围（今日/本周/本月/全部）、厂商和协议
- **最近请求表**：分页的最近 API 调用列表

### CLI 模式

```bash
# 在终端直接打印统计
mothx stats --cli
```

显示：
- 总 token、请求数、费用、时长
- 按厂商分类统计
- 按模型分类统计
- 最近 10 条请求

---

## 💻 IDE 集成

### ACP 协议

MothX 支持 Agent Client Protocol (ACP)，可集成到：

- **VS Code**
- **Zed**
- **JetBrains IDEs**（IntelliJ IDEA、WebStorm 等）

### VS Code 集成

在 `settings.json` 中添加：

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

### JetBrains 集成

导航到 `Settings → Tools → ACP Agents` 并添加：

- **Name**: MothX
- **Command**: `mothx`
- **Arguments**: `acp --mode agent`

---

## 🌐 Serve 模式

### OpenAI 兼容 HTTP API

MothX 可以作为 OpenAI 兼容的 HTTP 服务器使用：

```bash
# 启动 serve
mothx serve

# 配置文件
~/.mothx/serve.json
.mothx/serve.json
```

### 使用场景

- **团队共享**：团队成员共享 AI 编程助手
- **CI/CD 集成**：在 CI/CD 流程中使用 AI
- **API 服务**：提供 AI 编程 API 服务

---

## 📱 消息平台

### 微信/飞书/WebSocket

MothX 可以部署为聊天机器人：

```bash
# 启动消息通道
mothx serve

# 配置文件
~/.mothx/serve.json
.mothx/serve.json
```

### 功能特性

- **多用户支持**：每个用户独立会话
- **持久化会话**：会话历史自动保存
- **进度回调**：实时显示工具执行进度
- **思考显示**：显示 AI 的思考过程
- **微信 QR 登录**：扫码认证，支持登录状态轮询
- **热重载配置**：通过 Web UI 保存设置后自动重载 provider/model 配置

---

## 🤝 多 Agent

### 子 Agent 委托

```bash
# 启用多 Agent 模式
mothx --multi-agent

# 子 Agent 工具
subagent_spawn    # 创建子 Agent
subagent_status   # 查看子 Agent 状态
subagent_send     # 发送消息给子 Agent
subagent_destroy  # 销毁子 Agent
```

### Delegate 模式

```bash
# 启用一个阻塞式委托子 Agent
mothx --delegate

# 在 TUI 或 Serve 会话中切换
/delegate on
/delegate status
/delegate off
```

`--delegate` 会注册 `delegate_subagent`，这是一次只运行一个边界清晰调查任务的同步工具。适合主 Agent 只需要最终摘要结果的多步探索任务。

### A2A 协议

```bash
# 启用 A2A Master 模式
mothx --enable-a2a-master

# 生成示例配置
mothx --init-a2a-master-config
```

### 动态 Workflow

```bash
# 启用 Workflow 模式
mothx --workflows

# Workflow 工具
workflow_run      # 运行 JavaScript workflow
workflow_status   # 查看 workflow 状态
workflow_cancel   # 取消 workflow 运行
```

使用 JavaScript DSL 编排多个 worker agent，支持 phase 阶段划分、parallel 并行执行、并发控制、结果汇总和运行日志。适合代码审计、架构调研、多角色评审、生成-评审循环等复杂多智能体场景。

详见 [Workflow 模式](workflow.md) 文档了解完整语法和最佳实践。

### 使用场景

- **大型任务**：将大任务拆分成多个子任务
- **并行执行**：多个子 Agent 并行工作
- **远程协作**：通过 A2A 协议管理远程 Agent

---

## 🎨 丰富 TUI

### 功能特性

- **Markdown 渲染**：实时渲染 Markdown 内容
- **语法高亮**：代码块语法高亮
- **思考显示**：在完整事件模式或详情弹窗中查看 AI 的思考过程
- **工具弹窗**：查看工具执行详情（Ctrl+O）
- **事件显示模式**：默认简洁视图将工具折叠为单行摘要并隐藏例行生命周期事件；Ctrl+G 切换完整事件视图（Ctrl+O 始终打开原始工具详情）
- **多行输入**：Alt+Enter/Ctrl+J 插入换行，Up/Down 在边界时浏览历史
- **状态栏**：显示缓存命中率、token 统计、上下文使用量、耗时
- **粘性待办列表**：活跃的 plan 步骤在流式传输时保持可见
- **原生滚动**：完成的转录块打印到终端滚动缓冲区

### 快捷键

| 快捷键 | 功能 |
|--------|------|
| `Enter` | 提交提示 |
| `Alt+Enter` / `Ctrl+J` | 插入换行 |
| `Tab` | 循环模式（plan → agent → yolo） |
| `Esc` | 中止当前操作、审批或问题 |
| `Ctrl+O` | 打开/关闭工具详情弹窗 |
| `Ctrl+G` | 切换简洁/完整事件显示 |
| `Up` / `Down` | 在输入中移动；在边界时浏览历史；滚动工具弹窗 |
| `PgUp` / `PgDn` | 翻页工具弹窗 |
| `Home` / `End` | 输入行首/行尾；工具弹窗顶部/底部 |

### 斜杠命令

| 命令 | 描述 |
|------|------|
| `/systeminit [guidance]` | 生成或刷新项目 `AGENTS.md` |
| `/reload` | 以新 session 重启（TUI） |
| `/btw <问题>` | 不中断主任务提问旁路问题 |
| `/alloweditpath [add\|remove\|clear] <glob>` | 管理可编辑路径白名单 |
| `/allowautoedit [on\|off] [global]` | 切换 Agent 模式全自动编辑 |

---

## 🔒 安全可控

### 审批机制

```json
{
  "approval": {
    "bashWhitelist": ["go ", "make ", "git ", "npm "],
    "bashBlacklist": ["rm -rf", "sudo"],
    "confirmBeforeWrite": true
  }
}
```

### 项目级 Bash 自动审批

`allow.json` 支持项目级 bash 自动审批规则：

```json
{
  "bashCommands": ["make test"],
  "bashPrefixes": ["go test ", "go build "]
}
```

- `bashCommands`：精确命令匹配
- `bashPrefixes`：前缀匹配（尾部空格有意义）
- 设置级 `bashBlacklist` 优先级高于项目允许规则

### 交互式审批对话框

当需要审批时，TUI 显示一个对话框：

- **批准一次**：仅此次运行
- **拒绝**：拒绝命令
- **始终允许此命令**：持久化到 `.mothx/allow.json`
- **始终允许命令前缀**：持久化前缀到 `.mothx/allow.json`

### 安全特性

- **bashBlacklist 优先**：黑名单命令始终被拦截
- **YOLO 模式安全**：即使在 YOLO 模式下，黑名单仍然有效
- **`--print` 快速失败**：遇到需审批命令时直接失败
- **项目允许规则**：通过 `.mothx/allow.json` 配置项目级 bash 自动审批

---

## 📊 统一缓存指标

### 实时显示

- **缓存命中率**：在 TUI 底部实时显示
- **Token 统计**：每轮对话的 token 使用详情
- **统一口径**：TUI 和 print 模式使用一致的统计

### 使用场景

- **成本优化**：监控缓存命中率，优化 API 调用成本
- **性能监控**：监控 token 使用情况，优化响应速度

---

## 🐞 一致的调试输出

### `--debug` 模式

```bash
# 启用调试模式
mothx --debug

# 调试输出包括
- Provider 请求/响应
- ACP 模式调试
- 工具执行详情
- 本地 pprof：http://127.0.0.1:6060/debug/pprof/
```

### 使用场景

- **问题排查**：快速定位 API 调用问题
- **开发调试**：调试自定义工具或技能
- **性能分析**：分析 API 调用性能

---

## 🩺 环境诊断

### `doctor` 子命令

```bash
# 运行环境诊断
mothx doctor
```

### 检查项目

- **配置文件**：检查 settings.json、serve.json、serve.json
- **Provider**：检查 API 密钥和连接
- **沙箱**：检查 bwrap 安装和配置
- **MCP**：检查 MCP 服务器配置
- **会话**：检查会话存储
- **技能**：检查技能加载
- **上下文文件**：检查 AGENTS.md、CLAUDE.md 等

### 版本更新通知

MothX 会通过 npm registry 检测是否有新版本，并在有可用更新时给出非阻塞提醒。检测在后台进行，只在本地保存 24 小时冷却时间戳，前台不会阻塞。

可通过 `settings.json` 中设置 `"updateCheck": false` 关闭，或通过 `VIBECODING_NO_UPDATE_CHECK=1` 关闭。

---

## 🆕 当前版本能力补充

以下能力已集成到当前运行时，使用时请以配置和模型能力为准：

- **OpenAI Responses**：支持原生 response item 回放、reasoning、结构化输出、hosted tools、prompt cache，以及可选的 `responses.background` 持久后台运行。后台任务可查询、取消、重连、放弃，并在 Serve 重启后恢复。
- **Web UI 管理**：提供会话搜索与分页、运行状态与取消、审批中心、工具结果、技能启用、MCP 配置、Provider/Serve 设置和 `/stats` 统计页面。
- **MCP**：支持 stdio、streamable HTTP 和 legacy SSE
- **并行工具执行**：单个 agent 回合内的多个本地函数/自定义工具调用通过有界并行工作池执行。调用按模型声明的顺序开始（后面的调用绝不会早于前面的调用开始，即便前面的调用仍在等待审批），随后并发重叠执行，因此只有完成顺序不受限制。`toolExecution` 设置控制 `mode`（`parallel`/`sequential`）和 `maxConcurrency`（默认 `10`）。TUI、WebUI、Serve、channels、ACP 及子 Agent 统一使用。
；可编辑全局与项目级 `mcp.json`，并在新 Serve 会话中加载。
- **消息通道**：支持微信、飞书和 Webhook；后台运行会持久化事件，断线或重启后可补投结果。WebSocket `/ws/runs`、`/ws/logs` 是 Web UI 事件流，不是独立消息通道。
- **项目状态文件**：当前项目级状态统一位于 `.mothx/`，包括 `settings.json`、`serve.json`、`allow.json`、`memory.md` 和技能目录。旧 `.vibe` 路径不再自动迁移。
- **模型能力校验**：图片、附件、Responses hosted tools 和 reasoning 参数会按当前模型兼容性校验，不支持的组合会在请求前报错。

---

## 🎯 总结

MothX 是一个**全能终端 AI 编程助手**，具有以下核心优势：

1. **一个二进制文件搞定一切**：无需安装多个工具
2. **多提供商支持**：25+ 厂商适配器，一键切换
3. **安全可控**：沙箱隔离、审批门控、项目级自动审批规则
4. **丰富的功能**：会话管理、技能系统、统计面板、工作流编排、IDE 集成
5. **高性能**：SSE 流式传输、缓存命中优化

**准备好开始了吗？** [5 分钟快速上手](../zh/quick-start-tutorial.md)
