# 命令行参考

## 概述

```
mothx [flags] [message...]
```

## 命令行参数

### 基本参数

| 参数 | 简写 | 默认值 | 描述 |
|------|------|--------|------|
| `--provider` | `-p` | 配置文件中的默认值 | LLM 提供商 (deepseek-openai, deepseek-anthropic 或自定义名称) |
| `--model` | `-m` | 配置文件中的默认值 | 模型 ID |
| `--mode` | `-M` | `yolo` | 运行模式 (plan, agent, yolo, os)；未指定时回退到 `settings.json` 的 `defaultMode` |
| `--thinking` | `-t` | `off` | 思考级别 (off, minimal, low, medium, high, xhigh) |
| `--multi-agent` | - | `false` | 启用多 Agent 工具和命令 |
| `--delegate` | - | `false` | 启用 Delegate 模式（阻塞式单子 Agent 工具） |
| `--workflows` | - | `false` | 启用 JavaScript workflow 工具和 `/workflows` 命令 |
| `--artifact` | - | `false` | 为当前 TUI/CLI 进程启用生成制品发布 |

### 会话管理

| 参数 | 简写 | 描述 |
|------|------|------|
| `--continue` | `-c` | 继续最近的会话 |
| `--resume` | `-r` | 通过 ID 或路径恢复会话 |
| `--session` | - | 使用特定 session ID 或 `.db` 句柄文件 |

### 输出控制

| 参数 | 简写 | 描述 |
|------|------|------|
| `--print` | `-P` | 非交互模式，打印响应后退出；如果工具调用需要审批，则直接报错退出，不会自动批准 |
| `--json` | - | 与 `-P` 配合使用，将响应以 NDJSON（每事件一行 JSON）流式输出到 stdout（所有状态/诊断信息仍走 stderr，便于用 `jq` 等工具管道处理） |
| `--verbose` | - | 详细输出 |
| `--debug` | - | 启用调试日志、provider 请求/响应调试输出，并在 `127.0.0.1:6060` 启动本地 pprof |

### 安全

| 参数 | 描述 |
|------|------|
| `--sandbox` | 启用沙箱 (bubblewrap) |
| `--no-sandbox` | 禁用沙箱 (已弃用，默认不启用) |

### 其他

| 参数 | 简写 | 描述 |
|------|------|------|
| `--init-serve` | - | 生成 `serve.json` 配置模板 |
| `--init-a2a-master-config` | - | 生成 `a2a-list.json` 配置模板 |
| `--enable-a2a-master` | - | 启用 A2A Master 模式（远程 agent 调度） |
| `--force` | - | 覆盖已存在的配置文件（配合 `--init-*` 使用） |
| `--version` | `-v` | 显示版本 |
| `--help` | `-h` | 显示帮助 |

## 子命令

### `acp` - Agent Client Protocol 服务器

以 ACP 兼容的 stdio 代理模式运行 MothX，用于 IDE 集成。

```
mothx acp [flags]
```

支持 VS Code、JetBrains IDE 以及任何 ACP 兼容的编辑器。

| 标志 | 简写 | 默认值 | 描述 |
|------|------|--------|------|
| `--provider` | `-p` | 配置文件中的默认值 | LLM 提供商 |
| `--model` | `-m` | 配置文件中的默认值 | 模型 ID |
| `--mode` | `-M` | `yolo` | 运行模式 (plan, agent, yolo, os)；未指定时回退到 `settings.json` 的 `defaultMode` |
| `--thinking` | `-t` | 配置文件中的默认值 | 思考级别 |
| `--sandbox` | - | false | 启用沙箱 |
| `--verbose` | - | false | 详细输出 |
| `--debug` | - | false | 调试日志和本地 pprof |
| `--multi-agent` | - | false | 为 ACP 会话启用多 Agent 工具 |
| `--delegate` | - | false | 为 ACP 会话启用 Delegate 模式 |
| `--workflows` | - | false | 为 ACP 会话启用 JavaScript workflow 工具 |
| `--artifact` | - | false | 为当前 ACP/Desktop 进程启用生成制品发布 |

详见 [ACP 协议](acp.md) 文档了解 IDE 集成细节。

### `a2a` - A2A 协议服务器

运行独立的 A2A (Agent-to-Agent) 协议服务器。

```
mothx a2a [command]
```

| 子命令 | 描述 |
|--------|------|
| `start` | 启动 A2A 服务器 |
| `stop` | 停止 A2A 服务器 |
| `status` | 查看服务器状态 |
| `card` | 显示/生成 Agent Card |
| `send <message>` | 向远程 A2A 服务器发送任务 |
| `discover <url>` | 发现远程 Agent Card |
| `--init-a2a-config` | 生成 `a2a.json` 配置模板 |
| `--force` | 覆盖已存在的配置文件 |

详见 [A2A 协议](a2a.md) 文档。

### `serve` - 统一服务

以统一服务模式运行 MothX，暴露 OpenAI 兼容 Chat Completions API、Web UI，以及可选的微信/飞书/WebSocket 消息通道。

```
mothx serve [flags]
```

| 标志 | 简写 | 默认值 | 描述 |
|------|------|--------|------|
| `--port` | - | `127.0.0.1:7872` | 监听端口或地址，例如 `7872` 或 `0.0.0.0:7872`（覆盖 serve.json） |
| `--config` | - | - | serve.json 路径 |
| `--web-ui-dir` | - | 内置资源 | 使用前端目录覆盖内嵌的 Serve Web UI 资源 |
| `--unsafe` | - | false | 关闭认证并绑定到所有网卡 |
| `--work-dir` | - | 当前目录 | 默认工作目录 |
| `--provider` | `-p` | 配置文件中的默认值 | LLM 提供商 |
| `--model` | `-m` | 配置文件中的默认值 | 模型 ID |
| `--sandbox` | - | false | 启用沙箱 (bwrap) |
| `--multi-agent` | - | false | 启用多 Agent 工具 |
| `--delegate` | - | false | 启用 Delegate 模式 |
| `--workflows` | - | false | 启用 JavaScript workflow 工具 |
| `--web-search` | - | false | 为 Serve 会话启用配置的本地 Web 搜索 |
| `--browser` | - | false | 为 Serve 会话启用浏览器自动化 |
| `--artifact` | - | false | 为 WebUI/API 会话启用生成制品发布 |
| `--enable-a2a-master` | - | false | 启用 A2A Master，调度远程 Agent |
| `--lobster` | - | false | 启用 yolo、关闭沙箱并启用子 Agent |
| `--verbose` | - | false | 详细输出 |
| `--debug` | - | false | 调试日志和本地 pprof |

| 子命令 | 描述 |
|--------|------|
| `init-config [global|project]` | 生成 `serve.json` 配置模板 |
| `--force` | 覆盖已存在的配置文件 |

详见 [Serve 模式](serve.md) 文档。

### `stats` - 用量统计

启动用量统计仪表盘，或直接在终端打印 token 和请求统计。

```
mothx stats [flags]
```

| 标志 | 简写 | 默认值 | 描述 |
|------|------|--------|------|
| `--addr` | - | `127.0.0.1:7878` | Web 仪表盘监听地址 |
| `--db` | - | 默认 sessions 数据库 | `sessions.db` 路径 |
| `--cli` | - | false | 在终端打印统计信息，而不是启动 Web 服务器 |

示例：
```bash
mothx stats
mothx stats --cli
mothx stats --cli --db ~/.mothx/sessions/sessions.db
```

### `pure` - 归档会话数据库

将共享的 `sessions.db`（及其 SQLite 附属文件）移走，并在原位置创建一个全新的空数据库。不会删除任何内容：原数据库会以 `sessions.db.pure-<时间戳>.bak` 命名保存在新数据库旁边，其中的会话仍可恢复。

```
mothx pure [flags]
```

| 标志 | 简写 | 默认值 | 描述 |
|------|------|--------|------|
| `--session-dir` | - | 已配置的会话目录 | 要重置的会话目录 |

示例：
```bash
# 归档默认会话数据库并重新开始
mothx pure

# 重置指定的会话目录
mothx pure --session-dir ~/.mothx/sessions
```

运行前请先停止其他所有 `mothx` 进程；若数据库被其他进程占用时被移走，该进程仍会继续写入被移走的文件。

### `doctor` - 环境诊断

诊断你的 MothX 环境：系统信息、配置文件、Provider、模型、沙箱、MCP 等。

```
mothx doctor
```

检查项目：
- **环境**: OS/架构、Go 版本、Shell、Home/工作目录
- **配置文件**: 校验 settings、serve 和 MCP 配置文件，带解析检查
- **Provider 与模型**: 列出已配置的 Provider（API key 脱敏显示）、模型及其上下文窗口/最大 token/推理标志；验证默认 Provider 初始化
- **沙箱**: 检查 bubblewrap 可用性和版本
- **MCP 服务器**: 列出已配置的 MCP 服务器
- **会话**: 显示会话目录和条目数量
- **技能**: 显示全局和项目技能目录
- **上下文文件**: 发现 AGENTS.md、CLAUDE.md、CURSOR.md、.cursorrules、CONVENTIONS.md

```bash
mothx doctor
```

示例输出：
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

### `systeminit` - 项目 AGENTS.md 生成器

生成或刷新项目 `AGENTS.md` 文件，记录项目规范供 AI Agent 使用。

```
mothx systeminit [guidance...]
```

此 CLI 子命令以非交互方式运行。在 TUI 和 ACP 中，`/systeminit` 以交互方式运行，会先使用 `question` 工具提问几个澄清问题，再生成文件。

| 参数 | 描述 |
|------|------|
| `guidance...` | 可选的附加指导（例如 `用中文提问我，用英文写 AGENTS.md`） |

示例：
```bash
# 以默认行为生成 AGENTS.md
mothx systeminit

# 自定义指导生成
mothx systeminit 用中文提问我，用英文写
```

## 使用示例

### 基本使用

```bash
# 交互模式
mothx

# 带初始提示
mothx -P "解释这个代码库"

# 非交互打印模式（`-p` 用于选择 provider，`-P` 开启打印）
mothx -P "写一个 Hello World"
```

### 指定提供商和模型

```bash
# 使用 DeepSeek (OpenAI API)
mothx --provider deepseek-openai --model deepseek-v4-flash

# 使用 DeepSeek (Anthropic API)
mothx -p deepseek-anthropic -m deepseek-v4-flash

# 使用自定义提供商
mothx --provider my-custom-provider
```

### 选择模式

```bash
# Plan 模式 - 只读分析
mothx --mode plan

# Agent 模式 - 标准读写
mothx -M agent

# YOLO 模式 - 完全访问 (默认)
mothx -M yolo
```

### 多 Agent 模式

```bash
# 启用子 Agent 工具和多 Agent 命令
mothx --multi-agent

# ACP 会话也可以启用
mothx acp --multi-agent
```

启用后，MothX 会注册 `subagent_*` 工具，并支持后台委托调查等多 Agent 工作流。Cron 命令入口也依赖多 Agent 模式。

### 主角团

```bash
# 启动会话时绑定主角或团队
mothx --expert software-company
```

`--expert <bundle-id>` 会通过共享 Runtime 解析本地主角包。团队型主角会为该会话启用声明的成员调度能力；单人主角只改变 lead 身份。TUI 中使用 `/expert list|show <id>|bind <id>|unbind|switch <id>`。替换已绑定主角会创建会话分叉，而不会改写源会话。完整行为见[主角团](expert-teams.md)。

### Delegate 模式

```bash
# 启用阻塞式单子 Agent 委托
mothx --delegate

# ACP 会话也可以启用
mothx acp --delegate

# Serve 也可以启用
mothx serve --delegate
```

Delegate 模式会注册 `delegate_subagent` 工具，用于同步阻塞式子 Agent 委托。与可并行异步运行子 Agent 的多 Agent 模式不同，Delegate 模式同一时间只运行一个子 Agent，并等待其完成。适合父 Agent 只需要摘要结果的边界清晰调查任务。

可在 TUI 或 Serve 中通过 `/delegate [on|off|status]` 运行时切换。

### A2A Master 模式

```bash
# 生成示例配置
mothx --init-a2a-master-config

# 启用 master 模式
mothx --enable-a2a-master

# 启用 master 模式 + 详细日志
mothx --enable-a2a-master --verbose
```

启用后，MothX 会加载 `a2a-list.json` 中的远程 agent 列表，注册 `a2a_dispatch` tool，LLM 可自动向远程 agent 分发任务。

### 初始化配置

```bash
# 生成 serve.json 模板
mothx --init-serve

# 生成 a2a.json 模板
mothx a2a --init-a2a-config

# 生成 a2a-list.json 模板
mothx --init-a2a-master-config

# 强制覆盖已存在的文件
mothx --init-serve --force
```

### 思考级别

```bash
# 关闭思考
mothx --thinking off

# 中等级别
mothx -t medium

# 最高级别
mothx --thinking xhigh
```

### 会话管理

```bash
# 继续最近的会话
mothx --continue
mothx -c

# 恢复特定会话
mothx --resume session-abc123
mothx -r ~/.mothx/sessions/--encoded-working-directory--/20260625-120000_abcd1234.db

# 使用特定会话句柄文件
mothx --session ./20260625-120000_abcd1234.db
```

在 TUI 中，如果没有传入 `--continue`、`--resume` 或 `--session`，启动时不会立即创建空 session；第一条用户消息发送时才会创建。继续、恢复或选择已有 session 时，会话历史会展示在正常终端 scrollback 中。

### 沙箱

```bash
# 启用沙箱
mothx --sandbox

# 禁用沙箱 (默认)
mothx
```

### 管道输入

```bash
# 从 stdin 读取
echo "解释这段代码" | mothx -P

# 直接读取文件内容
mothx -p "解释这个文件: main.go"
```

### JSON 输出

```bash
# 流式输出 NDJSON（每事件一行），逐行处理
mothx -P --json "总结这个仓库" | jq -r 'select(.type=="text_delta").text' | tr -d '\n'

# 在脚本中拼接助手文本
summary=$(mothx -P --json "用一句话描述" | jq -r 'select(.type=="text_delta").text' | tr -d '\n')

# 以类型化对象查看整条事件流
mothx -P --json "总结这个仓库" | jq -s '.'
```

`--json` 仅在 `-P` 打印模式下生效。stdout 输出 NDJSON（JSON Lines）流——每个 agent 事件占一行 JSON 对象，可逐行读取、实时响应。事件类型包括 `start`（provider/model/mode）、`text_delta`、`think_delta`、`tool_call`、`tool_execution_start`/`tool_execution_end`、`tool_result`、`plan_update`、`usage`、`context_usage`、`compaction_start`/`compaction_end`，并以 `done` 或 `error` 事件结束。所有进度、调试信息仍走 stderr，stdout 始终为纯 NDJSON。

### ACP 服务器

```bash
# 启动 ACP 服务器（用于 IDE 集成）
mothx acp

# 使用特定模型
mothx acp --provider deepseek-openai --model deepseek-v4-flash

# 启用沙箱
mothx acp --sandbox --mode agent
```

## 交互式命令

在交互会话中可用的命令:

### 模式与模型

| 命令 | 描述 |
|------|------|
| `/mode [plan\|agent\|yolo]` | 切换或显示当前模式 |
| `/model [model_id]` | 切换或显示当前模型 |
| `/think` | 循环切换思考级别 |
| `/compact` | 触发上下文压缩 |
| `/delegate [on\|off\|status]` | 切换或查看 Delegate 模式 |
| `/systeminit [guidance]` | 生成或刷新项目 `AGENTS.md` |
| `/reload` | 以新 session 重启（TUI） |
| `/btw <问题>` | 不中断主任务提问旁路问题 |
| `/settings` | 配置 settings.json 各组，包括 providers、defaults、behavior、审批等 |
| `/alloweditpath [add <glob>\|remove <glob>\|clear]` | 管理可编辑路径白名单 |
| `/allowautoedit [on\|off] [global]` | 切换 Agent 模式全自动编辑 |
| `/agent list` | 在多 Agent 模式下列出子 Agent |
| `/agent switch <id>` | 切换活跃子 Agent |
| `/agent destroy <id>` | 销毁子 Agent |

### 会话管理

| 命令 | 描述 |
|------|------|
| `/sessions` | 打开交互式会话选择框 |
| `/sessions ls` | 列出当前项目的会话 |
| `/sessions set <id>` | 通过 ID 前缀切换到指定会话 |
| `/sessions clear` | 开始新的空白会话；实际 session 会在下一条消息发送时创建 |
| `/sessions del <id>` | 通过 ID 前缀删除会话 |
| `/clear` | 清空对话 |

`/sessions` 选择框支持方向键上下选择、回车切换、`n` 开始新会话、`d` 删除选中会话、Esc 关闭。

### 技能

| 命令 | 描述 |
|------|------|
| `/skills` | 列出可用技能 |
| `/skill <name>` | 激活指定技能 |
| `/skill:<name>` | 激活技能（替代语法） |

### 通用

| 命令 | 描述 |
|------|------|
| `/help` | 显示帮助 |
| `/quit` | 退出 |

## 键盘快捷键

| 快捷键 | 功能 |
|--------|------|
| `Enter` | 提交当前 prompt |
| `Alt+Enter` / `Ctrl+J` | 在 prompt 编辑器中插入换行 |
| `Tab` | 循环切换模式（`plan` → `agent` → `yolo`） |
| `Esc` | 中止当前操作、审批或问题提示 |
| `Ctrl+O` | 打开最新工具/详情 Modal；再次按下、`Esc` 或 `q` 可关闭 |
| `Ctrl+E` | 打开 ESM 进度面板；再次按下、`Esc` 或 `q` 可关闭 |
| `Ctrl+G` | 切换简洁/完整事件显示；默认使用简洁模式 |
| `Up` / `Down` | 在多行输入内移动；位于首/末行时浏览 prompt 历史；详情/进度面板打开时滚动内容 |
| `PgUp` / `PgDn` | 详情/进度面板打开时按页滚动 |
| `Home` / `End` | 移动到当前输入行首/行尾；详情/进度面板打开时跳到顶部/底部 |

## 环境变量

可以通过环境变量覆盖默认设置:

| 变量 | 描述 |
|------|------|
| `DEEPSEEK_API_KEY` | DeepSeek API 密钥 |
| `MOTHX_DIR` | 覆盖配置目录 |
| `VIBECODING_PROVIDER` | 覆盖默认提供商 |
| `VIBECODING_MODEL` | 覆盖默认模型 |
| `VIBECODING_MODE` | 覆盖默认模式 |
| `VIBECODING_THINKING` | 覆盖默认思考级别 |
| `VIBECODING_USER_AGENT` | 自定义 User-Agent 字符串 |

## 退出码

| 码 | 描述 |
|----|------|
| 0 | 成功 |
| 1 | 一般错误 |
| 2 | 用法错误 |
| 130 | 用户中断 (Ctrl+C) |
