# 系统架构

![MothX 架构总览](assets/image/architecture-zh.webp)

> 入口层（TUI、CLI、WebUI/Serve、Desktop/ACP 与消息通道）统一汇入前端中立的 Agent Runtime，由它拥有会话、运行、决策、输入内容、附件/制品、MCP、技能与策略解析，并向下复用唯一的核心 Agent 循环、Provider 抽象、SQLite 会话、沙箱、统计、记忆与工作流基础设施。


## 项目结构

```
mothx/
├── agent/                       # 公共 SDK 类型（Agent、Provider、Builder）
├── bootstrap/                   # 将公共 SDK 接口接到内部实现
├── cmd/mothx/                   # CLI 入口与子命令
├── desktop/                     # Electron 桌面客户端（纯 ACP 投影）
├── ui/                          # 内嵌到 Serve 的 Svelte Web UI
├── internal/
│   ├── agent/                   # 核心 Agent 循环、事件、提示词、子 Agent 工具
│   ├── agentruntime/            # 前端中立的共享 Runtime：SessionRuntime、
│   │                            # ExecutionRuntime、决策、制品、MCP、
│   │                            # 知识库、主角团、worktree
│   ├── acp/                     # ACP v1 服务器 + mothx.dev 扩展
│   ├── a2a/                     # A2A 服务器与远程 Agent master 调度
│   ├── architecture/            # 静态架构护栏测试
│   ├── browser/                 # vibe-browser 自动化工具
│   ├── config/                  # settings.json / serve.json schema 与默认值
│   ├── context/, contextfiles/  # Token 预算与 AGENTS.md/CLAUDE.md 加载
│   ├── cron/                    # 定时任务存储与调度器
│   ├── dao/                     # 唯一 SQL/查询构造/行映射 owner
│   ├── db/                      # 连接、事务与迁移生命周期
│   ├── esm/                     # Enable Supervisor Mode 目标循环
│   ├── expert/                  # 主角团包发现与校验
│   ├── imageproc/               # 图像归一化与缩放
│   ├── mcp/                     # MCP 客户端/服务器集成
│   ├── memory/                  # 消息通道使用的 memory.md 工具
│   ├── messaging/               # 微信/飞书平台抽象
│   ├── platform/                # 跨平台辅助与内嵌资源
│   ├── provider/                # 协议 provider、vendor 适配器、factory
│   ├── sandbox/                 # 沙箱抽象（bwrap、none）
│   ├── serve/                   # OpenAI API、Web UI、channels、hooks、cron、memory
│   ├── session/                 # 会话领域、迁移、事件存储、附件、知识库存储
│   ├── skills/, skillhub/       # 技能与在线市场
│   ├── stats/                   # 用量统计仪表盘与查询
│   ├── systeminit/              # AGENTS.md 生成
│   ├── tools/                   # 内置工具实现
│   ├── tui/                     # Bubble Tea 终端 UI
│   └── workflow/                # JavaScript workflow 运行时
├── example/                     # 公共 SDK 示例
└── docs/                        # 双语文档、生成索引与站点
```

## 运行模式

MothX 支持 6 种运行模式，共享同一套 Agent、Provider、Tools、Session 基础设施：

```
┌─────────────────────────────────────────────────────────────────────────┐
│                        MothX 运行模式                               │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐                  │
│  │  TUI (默认)   │  │  Print 模式   │  │  ACP stdio   │                  │
│  │  mothx   │  │  mothx   │  │  mothx   │                  │
│  │              │  │  -p "..."     │  │  acp          │                  │
│  └──────────────┘  └──────────────┘  └──────────────┘                  │
│                                                                         │
│  ┌──────────────┐  ┌──────────────┐  ┌────────────┐                  │
│  │ Serve 模式   │  │  A2A 独立模式 │  │ A2A Master │                  │
│  │ mothx serve  │  │ mothx a2a    │  │ --enable-  │                  │
│  │              │  │ start         │  │ a2a-master │                  │
│  └──────────────┘  └──────────────┘  └────────────┘                  │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

## 核心组件

### 0. 共享 Agent Runtime

Agent Runtime 是入口层与 Agent Core 之间的唯一边界。TUI、CLI、WebUI/API、
Desktop/ACP、微信/飞书、Cron、ESM 与 A2A 适配器只解码各自协议并投影事件，
不拥有执行语义。

- **构造**：生产代码只能通过 `SessionRuntime.BuildAgent`、`BuildTransientAgent`
  与 `agentruntime.NewAgentManager` 创建 Agent。
- **策略**：source、有效模式、能力、工具、沙箱、审批、MCP、技能、推理与运行策略
  由 `ResolveSource`/`ResolvePolicy` 统一解析一次。产品默认模式为 `yolo`。
- **执行**：`ExecutionRuntime` + `RunStore` 拥有持久化 Run 的 begin、reattach、
  update、cancel、finish、recovery 以及唯一的规范终态事件。
- **决策**：`DecisionService`/`DecisionRecord` 拥有审批与提问的身份、截止时间、
  首个响应生效、重放与过期。
- **输入与制品**：所有文本/图像/音频/文件输入都归一化到 Runtime 输入契约；
  附件与生成制品由 Runtime 拥有，并由 `internal/session` 持久化。
- **资源**：上下文/技能/规则、主角团、MCP 客户端、知识库服务、worktree 与统一
  关闭由 Runtime 拥有。
- **准入**：每个会话唯一的提交协调器与带 fencing 的执行租约决定新 Run 是否
  可以开始；适配器只能投影该状态。

### 1. Provider 系统

Provider 是与 LLM API 交互的抽象层。所有运行模式的 provider 创建都经过
`internal/provider/factory`，先应用厂商适配默认值，再构造通用 OpenAI
兼容或 Anthropic 兼容协议 provider。

```
┌─────────────────────────────────────────────────────────────┐
│                      Provider Interface                      │
├─────────────────────────────────────────────────────────────┤
│  Chat(ctx, params) <-chan StreamEvent                       │
│  Models() []*Model                                          │
│  GetModel(id string) *Model                                 │
│  Name() string                                              │
└─────────────────────────────────────────────────────────────┘
                              │
                              │
            ┌─────────────────┴─────────────────┐
            ▼                                   ▼
    ┌───────────────────┐             ┌───────────────────┐
    │ 厂商适配器         │             │ 通用 fallback      │
    │ vendor_*.go       │             │ openai/anthropic  │
    └───────────────────┘             └───────────────────┘
```

厂商选择顺序：

1. provider 配置中的显式 `vendor`
2. 根据 Base URL 自动识别
3. 根据 `api` 回退到通用协议 provider

#### StreamEvent 类型

```go
type StreamEvent struct {
    Type        EventType
    TextDelta   string      // 文本增量
    ThinkingDelta string    // 思考增量
    ToolCall    *ToolCall   // 工具调用
    Usage       *Usage      // Token 使用量
    Error       error       // 错误
}
```

### 2. Agent 循环

Agent 是核心逻辑，协调 Provider、Tools 和 Session。所有运行模式复用同一个
Agent 循环，区别在于输入来源（终端、HTTP、消息平台、stdio）和输出目标。

```
┌─────────────────────────────────────────────────────────────┐
│                       Agent Loop                             │
├─────────────────────────────────────────────────────────────┤
│  1. 构建系统提示词 (模式 + 工具 + 上下文文件 + 技能)         │
│  2. 发送消息到 Provider                                      │
│  3. 处理流式事件 (文本、思考、工具调用)                       │
│  4. 执行工具并收集结果                                        │
│  5. 将工具结果添加到消息                                      │
│  6. 重复直到完成                                              │
└─────────────────────────────────────────────────────────────┘
```

#### 执行流程

```
User Input (TUI / HTTP / Messaging / A2A / ACP stdio)
    │
    ▼
┌───────────────┐
│ Build Context │ ← System Prompt + Tools + Context Files + Skills
└───────┬───────┘
        │
        ▼
┌───────────────┐
│  Provider     │ ← LLM API (OpenAI/Anthropic)
│  Chat()       │
└───────┬───────┘
        │
        ▼
┌───────────────┐     ┌───────────────┐
│ Stream Events │────▶│ Tool Calls?   │
└───────┬───────┘     └───────┬───────┘
        │                     │
        │                     ▼
        │              ┌───────────────┐
        │              │ Execute Tools │
        │              └───────┬───────┘
        │                     │
        │                     ▼
        │              ┌───────────────┐
        └──────────────│ Append Results│
                       └───────────────┘
```

### 3. 多 Agent 运行时

多 Agent 模式通过 `--multi-agent` 显式启用。启用后，主 Agent 会获得
`subagent_spawn`、`subagent_status`、`subagent_send`、`subagent_destroy`
工具。子 Agent 拥有独立的 messages、context、session、registry 和 job
manager 状态。

```
Main Agent
    │
    ├── agentruntime.NewAgentManager 创建子 Agent
    ├── EventRouter 按 AgentID 路由事件
    └── subagent_* 工具管理异步子任务
```

子 Agent 的 registry 会过滤 `subagent_*` 工具，因此不能继续创建嵌套子 Agent。

### 4. A2A 协议

A2A（Agent-to-Agent）协议使不同的 AI Agent 能够互相发现、通信和协作。

```
┌───────────────────────────────────────────────────────────────────┐
│                     A2A 协议架构                                    │
├───────────────────────────────────────────────────────────────────┤
│                                                                   │
│  ┌──────────────────┐          ┌──────────────────┐              │
│  │   A2A Server     │          │   A2A Client     │              │
│  │  (mothx)    │ ◄──────► │  (任意 Agent)     │              │
│  │                  │  JSON-RPC │                  │              │
│  │  /a2a            │  2.0     │  SendMessage()   │              │
│  │  /a2a/send       │  + SSE   │  GetTask()       │              │
│  │  /a2a/task       │          │  CancelTask()    │              │
│  │  /a2a/events     │          │  GetAgentCard()  │              │
│  └──────────────────┘          └──────────────────┘              │
│                                                                   │
│  Task 生命周期: submitted → working → completed/failed/canceled    │
│                                                                   │
│  启动方式: mothx a2a start (默认端口 8093)                         │
│                                                                   │
└───────────────────────────────────────────────────────────────────┘
```

#### A2A Master 模式

A2A Master 模式通过 `--enable-a2a-master` 启用，加载 `a2a-list.json`
配置的远程 agent 列表，注册 `a2a_dispatch` tool 让 LLM 自动分发任务。

```
┌───────────────────────────────────────────────────────────────┐
│                   A2A Master 模式                               │
├───────────────────────────────────────────────────────────────┤
│                                                               │
│  a2a-list.json                                                │
│  ┌─────────────────────────────────────────┐                  │
│  │ agents:                                 │                  │
│  │   - name: code-reviewer                 │                  │
│  │     url: http://review:8093             │                  │
│  │   - name: ci-agent                      │                  │
│  │     url: http://ci:8093                 │                  │
│  └─────────────────────────────────────────┘                  │
│           │                                                   │
│           ▼                                                   │
│  ┌──────────────────┐                                         │
│  │   A2AManager     │ ← 加载 agent 列表                        │
│  └────────┬─────────┘                                         │
│           │                                                   │
│           ▼                                                   │
│  ┌──────────────────┐                                         │
│  │  a2a_dispatch    │ ← 注册为 LLM tool                       │
│  │  (agent_name,    │                                         │
│  │   message)       │                                         │
│  └────────┬─────────┘                                         │
│           │                                                   │
│           ▼                                                   │
│  ┌──────────────────┐  ┌──────────────────┐                  │
│  │  code-reviewer   │  │  ci-agent        │                  │
│  │  http://review   │  │  http://ci       │                  │
│  │  :8093           │  │  :8093           │                  │
│  └──────────────────┘  └──────────────────┘                  │
│                                                               │
└───────────────────────────────────────────────────────────────┘
```

### 5. Serve 模式

`internal/serve/openaiapi/` 实现 OpenAI 兼容的 HTTP 网关，暴露标准 Chat Completions API。

```
┌─────────────────────────────────────────────────────────────┐
│                    Serve 架构                               │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  OpenAI 兼容客户端 (curl, SDK, 任意工具)                      │
│       │                                                     │
│       ▼                                                     │
│  ┌──────────────────────────────────────────┐               │
│  │  HTTP Serve (net/http)                 │               │
│  │  POST /v1/chat/completions               │               │
│  └──────────────────────────────────────────┘               │
│       │                                                     │
│       ▼                                                     │
│  ┌──────────────────────────────────────────┐               │
│  │  Agent Loop (复用同一套)                   │               │
│  │  + Tools + Session + Sandbox + Skills     │               │
│  └──────────────────────────────────────────┘               │
│                                                             │
│  配置: serve.json (全局 ~/.mothx/ 或项目 .mothx/)             │
│  安全: Bearer token + allowedWorkDirs + sandbox              │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### 6. Channels 消息平台网关

`internal/serve/channels/` 实现消息通道运行时，支持微信、飞书和 WebSocket。

```
┌─────────────────────────────────────────────────────────────┐
│                    Channels 架构                                │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐                  │
│  │  微信     │  │  飞书     │  │ WebSocket │                  │
│  └─────┬────┘  └─────┬────┘  └─────┬────┘                  │
│        │             │             │                         │
│        └─────────────┼─────────────┘                         │
│                      ▼                                       │
│  ┌──────────────────────────────────────────┐               │
│  │  Channels Dispatcher                       │               │
│  │  (per-user session, yolo mode default)   │               │
│  └──────────────────────────────────────────┘               │
│       │                                                     │
│       ▼                                                     │
│  ┌──────────────────────────────────────────┐               │
│  │  Agent Loop (复用同一套)                   │               │
│  │  + Tools + Session + Sandbox + Skills     │               │
│  └──────────────────────────────────────────┘               │
│                                                             │
│  配置: serve.json                                           │
│  Session: <sessionDir>/channels/<platform>/<user_id>/        │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### 7. Cron 调度器

`internal/cron` 包提供基于 `sessions.db` 的 cron store 和 scheduler，任务写入
`cron_jobs` 表并绑定到 session，可通过 Agent 工具或远程 A2A Server 执行任务。

```
┌─────────────────────────────────────────────────────────────┐
│                    Cron 调度器                                │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌──────────────────┐                                       │
│  │  CronStore       │ ← sessions.db / cron_jobs              │
│  │  (SQLiteCronStore)                                      │
│  └────────┬─────────┘                                       │
│           │                                                 │
│           ▼                                                 │
│  ┌──────────────────┐                                       │
│  │  Scheduler       │ ← 定时轮询 (默认 30s)                  │
│  └────────┬─────────┘                                       │
│           │                                                 │
│     ┌─────┴─────┐                                           │
│     ▼           ▼                                           │
│  ┌───────┐  ┌───────────┐                                   │
│  │ 子Agent│  │ A2A Server│                                   │
│  │ (本地) │  │ (远程)    │  ← --a2a-target 参数               │
│  └───────┘  └───────────┘                                   │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### 8. 工具系统

工具是 Agent 与外部世界交互的方式。所有运行模式共享同一套工具注册表。

```
┌─────────────────────────────────────────────────────────────┐
│                    Tool Interface                            │
├─────────────────────────────────────────────────────────────┤
│  Name() string                                              │
│  Description() string                                       │
│  Parameters() json.RawMessage                               │
│  Execute(ctx, params) (*ToolResult, error)                  │
└─────────────────────────────────────────────────────────────┘
                              │
        ┌─────────────────────┼─────────────────────┐
        │                     │                     │
        ▼                     ▼                     ▼
┌───────────────┐   ┌───────────────┐   ┌───────────────┐
│  File Tools   │   │  Search Tools │   │  System Tools │
│  - read       │   │  - grep       │   │  - bash       │
│  - write      │   │  - find       │   │  - ls         │
│  - edit       │   │               │   │  - jobs       │
└───────────────┘   └───────────────┘   │  - kill       │
                                        └───────────────┘
┌───────────────┐   ┌───────────────┐   ┌───────────────┐
│  Planning     │   │  Skills       │   │  A2A Master   │
│  - plan       │   │  - skill_ref  │   │  - a2a_       │
│               │   │               │   │    dispatch   │
└───────────────┘   └───────────────┘   └───────────────┘
```

### 9. 会话管理

会话使用 SQLite 存储，支持树状结构和分支。

```
┌─────────────────────────────────────────────────────────────┐
│                    Session Storage                          │
├─────────────────────────────────────────────────────────────┤
│  sessions.db                                                │
│  ├── sessions                                               │
│  │   ├── id                                                 │
│  │   ├── cwd                                                │
│  │   ├── timestamp                                          │
│  │   ├── parent_session                                     │
│  │   └── version                                            │
│  └── entries                                                │
│      ├── seq                                                │
│      ├── session_id                                         │
│      ├── id                                                 │
│      ├── type                                               │
│      ├── parent_id                                          │
│      ├── timestamp                                          │
│      └── data                                               │
└─────────────────────────────────────────────────────────────┘
```

#### 会话类型

| type | 描述 |
|------|------|
| `session` | 会话元数据 |
| `message` | 用户/助手消息 |
| `model_change` | 模型切换记录 |
| `compaction` | 上下文压缩记录 |
| `label` | 会话标签 |

### 10. 沙箱系统

沙箱通过 bubblewrap (bwrap) 实现进程隔离。

```
┌─────────────────────────────────────────────────────────────┐
│                     Sandbox Manager                          │
├─────────────────────────────────────────────────────────────┤
│  SetLevel(level)                                            │
│  GetActive() *Sandbox                                       │
│  Execute(cmd) (stdout, stderr, error)                       │
└─────────────────────────────────────────────────────────────┘
                              │
        ┌─────────────────────┼─────────────────────┐
        │                     │                     │
        ▼                     ▼                     ▼
┌───────────────┐   ┌───────────────┐   ┌───────────────┐
│  LevelNone    │   │ LevelStandard │   │  LevelStrict  │
│  (无限制)     │   │ (项目读写)    │   │  (项目只读)   │
└───────────────┘   └───────────────┘   └───────────────┘
```

### 11. TUI 系统

基于 Bubble Tea 的终端用户界面。

```
┌─────────────────────────────────────────────────────────────┐
│                        TUI App                              │
├─────────────────────────────────────────────────────────────┤
│  启动 Header                                                │
│  Vibe logo │ version │ provider/model │ cwd                 │
├─────────────────────────────────────────────────────────────┤
│  终端原生 scrollback                                        │
│  已完成 transcript block 通过 Program.Println 打印到终端历史 │
├─────────────────────────────────────────────────────────────┤
│  受管理的实时视图                                           │
│  实时 assistant/think 流、sticky plan todo、loading 指示器、 │
│  输入框、footer 和多 Agent tab bar                          │
├─────────────────────────────────────────────────────────────┤
│  工具详情 Modal                                             │
│  Ctrl+O 打开可滚动的全屏详情视图                            │
└─────────────────────────────────────────────────────────────┘
```

关键 TUI 行为：

- 已完成消息会打印到终端原生 scrollback，由终端负责历史滚动和文本选择。
- Bubble Tea 管理的视图聚焦实时流式内容、多行输入、Footer 状态、Loading 状态和当前计划/todo 上下文。
- 输入 editor 支持多行 prompt 编写：`Enter` 提交，`Alt+Enter` / `Ctrl+J` 插入换行，`Up` / `Down` 会先在输入内移动，位于边界时才浏览历史。
- TUI 默认使用简洁事件视图：每个工具调用只保留一行摘要，隐藏例行生命周期事件，工具完成后原位替换运行中的条目。`Ctrl+G` 切换完整事件视图；`Ctrl+O` 打开最新工具/详情 Modal，查看原始参数、输出、diff 和活动时间线。
- 多 Agent 模式下，当存在多个 Agent 时会显示 Agent Tab Bar 及其状态。

## 配置文件总览

| 文件 | 位置 | 用途 |
|------|------|------|
| `settings.json` | `~/.mothx/` 或 `.mothx/` | 核心设置（provider、model、mode 等） |
| `serve.json` | `~/.mothx/` 或 `.mothx/` | 统一 serve 配置 |
| `a2a.json` | `~/.mothx/` 或 `.mothx/` | A2A 服务器配置 |
| `a2a-list.json` | `~/.mothx/` 或 `.mothx/` | A2A Master 远程 agent 列表 |
| `mcp.json` | `~/.mothx/` 或 `.mothx/` | MCP 服务器配置 |
| `memory.md` | 项目根目录或 `~/.mothx/` | 持久化记忆 |
| `sessions.db` | session 目录 | 会话、统计、事件和 session 绑定定时任务 |

## 数据流

### 完整请求流程

```
1. 用户输入 (来自 TUI / CLI / HTTP / Messaging / A2A / ACP stdio / Desktop)
   │
   ▼
2. 输入层捕获
   │
   ▼
3. Agent.Run(ctx, input)
   │
   ▼
4. 构建系统提示词
   ├── 模式提示 (plan/agent/yolo)
   ├── 工具定义 (JSON Schema)
   ├── 上下文文件 (AGENTS.md, CLAUDE.md)
   └── 技能上下文
   │
   ▼
5. 构建消息历史
   ├── 历史消息 (from Session)
   └── 新用户消息
   │
   ▼
6. Provider.Chat(ctx, params)
   │
   ▼
7. SSE 流式响应
   ├── TextDelta → 显示文本
   ├── ThinkingDelta → 显示思考
   └── ToolCall → 执行工具 (含 a2a_dispatch)
   │
   ▼
8. 工具执行 (通过 Sandbox)
   │
   ▼
9. 收集工具结果
   │
   ▼
10. 继续对话 (回到步骤 5)
   │
   ▼
11. 完成，保存会话
```

## 关键设计决策

### 1. 接口抽象

使用接口抽象 Provider 和 Tool，便于扩展和测试。

### 2. 流式处理

使用 Channel 实现流式响应，提供实时反馈。

### 3. 会话树

使用树状结构存储会话，支持分支和恢复。

### 4. 分层配置

支持全局和项目配置，项目配置覆盖全局。

### 5. 沙箱隔离

通过 bubblewrap 实现进程级隔离，保护系统安全。

### 6. 公共 SDK 包

`agent/` 包暴露公共 Go 类型（`Agent`、`Provider`、`Builder`），外部应用可以
在不依赖 internal 包的情况下嵌入 Agent；`bootstrap/` 负责把这些公共接口接到
内部 Runtime。详见 [SDK 集成指南](sdk.md)。

### 7. 共享 Agent Runtime 与 Agent 循环

所有运行模式（TUI、CLI、WebUI/Serve、Channels、A2A、ACP、Desktop）复用同一个
Agent Runtime 与 Agent 循环。适配器的差异只在于协议解码与事件投影；资源所有权、
运行生命周期、决策与策略都在 Runtime 中，因此不同入口的行为不会漂移。
