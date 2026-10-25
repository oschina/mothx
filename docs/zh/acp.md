# Agent Client Protocol (ACP)

## 概述

MothX 实现了 **Agent Client Protocol (ACP)**，一个基于 JSON-RPC 的协议，允许 AI 编码代理直接集成到 IDE 和编辑器中。ACP 采用客户端-服务器架构，你的 IDE 作为客户端，MothX 作为后台代理运行。

## 支持的 IDE

### Visual Studio Code (VS Code)

MothX 可以通过兼容的扩展作为 ACP 代理在 VS Code 中使用。在你的 VS Code 设置中添加以下配置：

```json
{
  "your-acp-extension.servers": {
    "mothx": {
      "type": "agent",
      "command": "mothx",
      "args": ["acp"],
      "env": {}
    }
  }
}
```

确保 `mothx` 在你的 `PATH` 环境变量中，或使用二进制文件的完整路径。

### JetBrains IDE (IntelliJ IDEA, GoLand, WebStorm 等)

JetBrains IDE 也支持 ACP 代理。在你的 IDE 设置中配置：

```json
{
  "acpAgentServers": {
    "mothx": {
      "command": "mothx",
      "args": ["acp"]
    }
  }
}
```

## 使用 ACP 模式

### 启动 ACP 服务器

```bash
# 基础 ACP 服务器 (stdin/stdout)
mothx acp

# 指定提供商和模型
mothx acp --provider deepseek-openai --model deepseek-v4-flash

# 启用沙箱
mothx acp --sandbox

# 指定模式
mothx acp --mode agent

# 启用多 Agent 工具
mothx acp --multi-agent
```

### ACP 命令行参数

| 参数 | 简写 | 默认值 | 描述 |
|------|------|--------|------|
| `--provider` | `-p` | 配置文件默认值 | LLM 提供商 |
| `--model` | `-m` | 配置文件默认值 | 模型 ID |
| `--mode` | `-M` | `yolo` | 运行模式 (plan, agent, yolo, os)；未指定时回退到 `settings.json` 的 `defaultMode` |
| `--thinking` | `-t` | 配置文件默认值 | 思考级别 |
| `--sandbox` | - | false | 启用沙箱 |
| `--verbose` | - | false | 详细输出 |
| `--debug` | - | false | 调试日志和本地 pprof |
| `--multi-agent` | - | false | 启用子 Agent 工具和多 Agent 工作流 |
| `--delegate` | - | false | 启用阻塞式单子 Agent 委托 |
| `--workflows` | - | false | 启用 JavaScript workflow 工具 |
| `--web-search` | - | false | 启用已配置的本地 Web 搜索工具 |
| `--browser` | - | false | 启用浏览器自动化 |
| `--artifact` | - | false | 为 Desktop/ACP 启用生成制品发布 |
| `--permission-timeout` | - | `5m` | `session/request_permission` 审批超时（Go duration；环境变量 `MOTHX_ACP_PERMISSION_TIMEOUT`） |
| `--question-timeout` | - | `5m` | ACP 提问请求超时（Go duration；环境变量 `MOTHX_ACP_QUESTION_TIMEOUT`） |

## 协议细节

ACP 使用 JSON-RPC 2.0 通过 stdio 进行通信，并遵循 ACP v1 schema。MothX 的业务扩展使用独立的 `mothx/*`、`_mothx/*` 方法和 `_meta` 扩展声明；标准 ACP 对象不会添加非扩展用途的自定义字段。

### 方法

| 方法 | 描述 |
|------|------|
| `initialize` | 握手和能力协商 |
| `mothx/doctor` | 返回机器可读的安装/提供商诊断；无需会话 |
| `session/new` | 创建新会话 |
| `session/load` | 加载已有会话并重放历史 |
| `session/list` | 列出持久化会话，支持按工作目录筛选和游标分页 |
| `session/resume` | 恢复已有会话而不重放历史 |
| `session/fork` | 在当前状态分叉会话 |
| `session/prompt` | 向代理发送提示 |
| `session/cancel` | 取消活动的提示 |
| `session/close` | 取消活动工作并释放该会话的 MCP 等运行时资源 |
| `session/delete` | 从持久化会话列表中删除非活动会话 |
| `session/set_mode` | 切换会话运行模式 |
| `session/set_config_option` | 修改会话配置项（provider、model、mode、thinking、sandbox、browser、web search） |
| `mothx/session/history` | 分页读取持久化会话历史 |
| `mothx/session/draft-config-options` | 读取新会话草稿的初始配置项 |
| `mothx/session/setTitle` | 设置会话标题 |
| `mothx/session/setWorkDir` | 修改会话工作目录 |
| `mothx/session/setMeta` | 设置 Desktop/WebUI 使用的权威会话元数据 |
| `mothx/session/listAll` | 列出所有工作目录的会话 |
| `mothx/session/delete` | 删除会话（`session/delete` 的别名） |
| `mothx/projects/list` | 列出 Desktop/WebUI 项目 |
| `mothx/projects/create` | 创建项目 |
| `mothx/projects/rename` | 重命名项目 |
| `mothx/projects/delete` | 删除项目 |
| `mothx/attachment/list` | 列出会话拥有的附件 |
| `mothx/attachment/fetch` | 获取附件字节 |
| `mothx/subagent/list` | 列出子 Agent 及其状态 |
| `mothx/subagent/messages` | 读取子 Agent 的执行记录 |
| `mothx/worktree/list` | 列出受管理的 git worktree |
| `mothx/worktree/create` | 创建受管理的 worktree |
| `mothx/worktree/reset` | 将 worktree 重置到默认分支 |
| `mothx/worktree/remove` | 删除受管理的 worktree |
| `mothx/worktree/status` | 读取 worktree 状态 |
| `mothx/workspace/extend` | 无需重启 ACP 即可扩展工作区目录 |
| `mothx/manage/*` | 管理投影；见[管理投影](#管理投影) |
| `$/cancel_request` | 按 JSON-RPC 请求 ID 取消进行中的请求 |
| `session/update` | 服务器通知：会话状态变更 |

### 能力

MothX 在初始化时声明以下 ACP 能力：

- **加载会话**: 加载和继续之前的会话
- **提示能力**: 文本、`resource_link`、图像、音频以及嵌入资源/上下文内容；Runtime 会把它们统一归一化到共享输入契约
- **会话能力**: 支持加载、列出、恢复、分叉、关闭、删除会话以及 additional directories
- **MCP 能力**: 支持 stdio / http / sse 传输
- **多 Agent 工作流**: 使用 `--multi-agent` 启动 ACP 服务器后可用；Delegate 模式使用 `--delegate`，workflow 使用 `--workflows`
- **MothX 扩展**: 通过 `initialize._meta["mothx.dev"].features` 声明；客户端必须按这些键 gate 可选方法、面板和投影

### 通知

服务器发送 `session/update` 通知，包含以下事件类型：

| 更新类型 | 描述 |
|-----------|------|
| `agent_message_chunk` | 代理的文本增量 |
| `agent_thought_chunk` | 思考/推理增量 |
| `user_message_chunk` | 历史用户消息 |
| `tool_call` | 正在调用的工具 |
| `tool_call_update` | 工具状态更新 (pending/in_progress/completed/failed) |
| `usage_update` | 当前上下文 token 使用量、上下文窗口和累计成本；另见下方的附加缓存投影 |

`usage_update` 保持 ACP 标准结构。当 `initialize._meta["mothx.dev"].features` 声明 `usageCacheProjection` 时，该通知额外在 `_meta["mothx.dev"]` 中携带会话累计的 `cacheRead`、`cacheWrite` 与 `totalInputTokens`；命中率口径为 `cacheRead / totalInputTokens`，与 Runtime 的 `Usage.TotalInputTokens` 同分母。累计成本与这些累计量都会在 `session/load` 时从权威会话历史重建，并在首条 usage 事件前不下发该扩展。

`session/load` 会在响应前完整重放会话历史。

## MothX 扩展

MothX 将非 ACP 标准的交互隔离为扩展。客户端仅在 `initialize` 响应的 `agentCapabilities._meta["mothx.dev"]` 声明对应能力后使用这些方法；不支持扩展的 ACP 客户端可以忽略它们且不影响标准会话流程。

`initialize.agentInfo` 会以 `mothx` / `MothX` 标识产品，并携带构建版本。顶层 `initialize._meta["mothx.dev"]`（也镜像到 `agentCapabilities._meta` 以兼容现有客户端）声明稳定功能列表和 `doctor: true`。`mothx/doctor` 接受 `{}` 或 `{ "cwd": "/absolute/path" }`，并返回 `{ ok, version, summary, checks }`；检查使用稳定 ID，绝不返回 API key 原文。`mothx doctor --json` 的 stdout 只输出这一份响应。ACP 若在 initialize 前退出，stderr 会包含一行 `MOTHX_ACP_ERROR { ... }`，其中带稳定错误码和简短修复建议。

| 方法 | 用途 |
|------|------|
| `mothx/requestQuestion` | 多选问题请求；客户端响应 `{ "answer": "选项文本" }`。initialize 之前的重放使用旧名 `_mothx/request_question`。 |
| `_mothx/session_event` | 状态、回合、上下文压缩、重试与制品等 MothX 运行时事件 |

### 管理投影

`mothx/manage/*` 是共享 Runtime 与 Serve 服务的附加、按能力 gate 的投影。Desktop 使用它，而不是直接读取 `settings.json`、sessions 数据库或 Serve 配置。

| 分组 | 方法 |
|------|------|
| 应用/设置 | `application/get`、`application/patch`、`settings/get`、`settings/patch`、`env/get`、`env/patch` |
| Serve/渠道 | `serve/get`、`serve/patch`、`channels/get`、`channels/patch` |
| Provider | `providers/list`、`providers/save`、`providers/delete`、`providers/test`、`providers/discover` |
| 技能 | `skills/list`、`skills/set`、`skillhub/*` |
| MCP | `mcp/list`、`mcp/set` |
| 自动化 | `cron/*`、`deliveries/list`、`deliveries/retry`、`memory/get`、`memory/put`、`stats/summary`、`stats/timeseries` |
| 主角 | `experts/list`、`experts/get`、`experts/create`、`experts/update`、`experts/delete` |
| 知识库 | `knowledge-bases/*`（list、get、create、update、delete、scan、status、query、mcp/apply、runs、sources、schedule、clear） |

响应与日志会掩码 provider key 和敏感 header。管理调用不会让 Desktop 成为会话、运行、凭证或调度状态的 owner。

## MCP 服务器集成

MothX 支持在 ACP 会话期间连接 **MCP (Model Context Protocol)** 服务器。这让代理能够访问外部工具和数据源。

ACP 会话与普通 CLI/TUI 会话复用同一套 MCP 连接和工具注册运行时。区别是 ACP 客户端在创建/加载会话时传入 `mcpServers`，普通 CLI/TUI 会话则在进程启动时加载 `mcp.json`。

### 配置 MCP 服务器

MCP 服务器由 IDE 客户端配置，并在创建或加载会话时传递给 MothX。配置格式：

```json
{
  "mcpServers": [
    {
      "name": "my-database",
      "type": "stdio",
      "command": "mcp-server",
      "args": ["--port", "8080"],
      "env": [
        {"name": "DB_URL", "value": "postgres://localhost/mydb"}
      ]
    },
    {
      "name": "remote-tools",
      "type": "http",
      "url": "https://mcp.example.com",
      "headers": [
        {"name": "Authorization", "value": "Bearer ${TOKEN}"}
      ]
    },
    {
      "name": "legacy-sse",
      "type": "sse",
      "url": "https://legacy.example.com/sse",
      "messageUrl": "https://legacy.example.com/messages"
    }
  ]
}
```

### MCP 工具注册

当 MCP 服务器连接后，MothX 自动发现并注册服务器暴露的所有工具。工具按照 `mcp_<server_name>_<tool_name>` 的命名约定注册，代理可以像使用内置工具一样使用它们。

注册发生在 agent 冻结当前会话的 system prompt 和工具定义之前。因此 MCP 服务器变更后，需要用更新后的 `mcpServers` payload 创建或加载新的 ACP 会话。

除 `tools/*` 外，MothX 现在还会发现：

- `resources/*`：注册为 MCP 资源读取工具
- `prompts/*`：注册为 MCP Prompt 渲染工具

### MCP 传输支持

支持的传输类型：

- `stdio`：`command` 可以是通过 `PATH` 查找的命令名，也可以是直接路径；命令参数请放在 `args` 中
- `http`：通过 `url` 连接 streamable HTTP 端点
- `sse`：通过 `url` 连接 legacy SSE 流，并通过 `messageUrl` 发送请求

补充说明：

- 同一会话内 MCP 服务器 `name` 必须唯一
- `http` / `sse` 传输可通过 `headers` 传鉴权头
- `sampling/createMessage` 已桥接到当前 ACP provider/model，并返回 assistant 文本内容
- MCP progress/logging/cancel 通知会以结构化 ACP `tool_call_update` 事件透出

## 权限系统

在 ACP 模式下，代理可以请求用户授权执行工具。IDE 客户端接收 `session/request_permission` 通知，并可以返回允许/拒绝的决定。

```
客户端                                   服务器 (mothx acp)
  │                                           │
  │  ── session/request_permission ────────▶  │
  │      (tool_call_id, title, options)       │
  │                                           │
  │  ◀── JSON-RPC 响应 ────────────────────  │
  │      (outcome: allow-once / reject-once)  │
```

## 示例：VS Code 集成

### 步骤 1：安装 MothX

```bash
npm install -g mothx-installer
# 或
go install github.com/oschina/mothx/cmd/mothx@latest
```

### 步骤 2：配置 VS Code

在你的 VS Code `settings.json` 中添加：

```json
{
  "acp.agents": {
    "mothx": {
      "command": "mothx",
      "args": ["acp", "--mode", "agent"],
      "description": "MothX AI Assistant"
    }
  }
}
```

### 步骤 3：开始使用

在 VS Code 中打开你的项目，启动 ACP 代理，然后直接在编辑器中提问或请求代码更改。

## 示例：JetBrains IDE 集成

### 步骤 1：安装 MothX

```bash
npm install -g mothx-installer
```

### 步骤 2：在 JetBrains IDE 中配置

进入 `设置 → 工具 → ACP Agents` 并添加新代理：

- **名称**: MothX
- **命令**: `mothx`
- **参数**: `acp --mode agent`

或添加到 `.idea/workspace.xml`：

```xml
<component name="ACPSettings">
  <option name="agents">
    <list>
      <ACPAgentSetting>
        <option name="name" value="Vibecoding" />
        <option name="command" value="mothx" />
        <option name="args" value="acp --mode agent" />
      </ACPAgentSetting>
    </list>
  </option>
</component>
```

### 步骤 3：开始使用

使用 JetBrains IDE 中的 ACP 工具窗口与 MothX 交互。
