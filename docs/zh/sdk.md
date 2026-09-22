# SDK 集成指南

MothX 提供了一个公共 Go 包（`github.com/oschina/mothx/agent`），允许你将 AI 编码 Agent 嵌入到自己的应用中。本指南涵盖：

1. [公共 Agent 包](#公共-agent-包) — 类型、接口和 Builder API
2. [实现自定义 Provider](#实现自定义-provider) — 接入自有 LLM 后端
3. [构建和运行 Agent](#构建和运行-agent) — 创建 Agent 并处理事件流
4. [事件类型](#事件类型) — 理解事件流
5. [子 Agent 模式](#子-agent-模式) — 将任务委派给子 Agent

---

## 公共 Agent 包

导入路径：

```go
import "github.com/oschina/mothx/agent"
```

该包**仅包含公共类型和接口**，不依赖任何 internal 包。定义了以下核心类型：

| 类型 | 说明 |
|------|------|
| `Agent` | 所有 Agent 实现必须满足的接口 |
| `Provider` | LLM 后端接口 |
| `Builder` | 流式 API，用于创建 Agent 实例 |
| `Event` / `EventType` | Agent 事件流类型 |
| `Message` / `ContentBlock` | 对话消息类型 |
| `ChatParams` / `StreamEvent` | LLM 请求/响应类型 |
| `ModelInfo` / `ModelCompat` | 模型元数据和兼容性标志 |
| `BaseProvider` | 可嵌入的辅助类型，提供通用 Provider 方法 |

### Agent 接口

```go
type Agent interface {
    // ID 返回 Agent 的唯一标识符
    ID() AgentID

    // ParentID 返回父 Agent 的 ID，顶层 Agent 返回空值
    ParentID() AgentID

    // Run 处理用户消息并以流式方式返回事件
    Run(ctx context.Context, userMsg string) <-chan Event

    // RunWithMessages 使用显式消息历史进行处理
    RunWithMessages(ctx context.Context, messages []Message) <-chan Event

    // Abort 发送停止处理信号
    Abort()

    // GetMessages 返回当前消息历史的副本
    GetMessages() []Message

    // SetMessages 替换消息历史
    SetMessages(msgs []Message)

    // GetContext 返回当前 Agent 上下文的副本
    GetContext() *AgentContext

    // SetContext 替换 Agent 上下文
    SetContext(ctx *AgentContext)

    // GetContextUsage 返回当前上下文窗口使用情况
    GetContextUsage() *ContextUsage

    // LoadHistoryMessages 加载历史消息到 Agent 上下文
    LoadHistoryMessages(messages []Message)

    // HandleApprovalResponse 处理用户的审批响应
    HandleApprovalResponse(approvalID string, approved bool)
}
```

### Provider 接口

```go
type Provider interface {
    // Chat 发送聊天请求，返回流式事件 channel
    Chat(ctx context.Context, params ChatParams) <-chan StreamEvent

    // Name 返回 Provider 名称（如 "openai"、"anthropic"）
    Name() string

    // Models 返回可用模型列表
    Models() []ModelInfo

    // GetModel 根据 ID 返回模型，未找到返回 nil
    GetModel(id string) *ModelInfo
}
```

---

## 实现自定义 Provider

要接入自有的 LLM 后端，实现 `agent.Provider` 接口即可。嵌入 `agent.BaseProvider` 可免费获得 `Name()` / `Models()` / `GetModel()` 的实现：

```go
package mybackend

import (
    "context"

    "github.com/oschina/mothx/agent"
)

type MyProvider struct {
    agent.BaseProvider
    apiKey string
}

func NewMyProvider(apiKey string) *MyProvider {
    models := []agent.ModelInfo{
        {
            ID:            "my-model-v1",
            Name:          "My Model V1",
            Provider:      "mybackend",
            ContextWindow: 128000,
            MaxTokens:     8192,
        },
    }
    return &MyProvider{
        BaseProvider: agent.NewBaseProvider("mybackend", models),
        apiKey:       apiKey,
    }
}

func (p *MyProvider) Chat(ctx context.Context, params agent.ChatParams) <-chan agent.StreamEvent {
    ch := make(chan agent.StreamEvent, 100)

    go func() {
        defer close(ch)

        // 1. 发送 StreamStart
        ch <- agent.StreamEvent{Type: agent.StreamStart}

        // 2. 调用你的 LLM API，流式返回响应...
        // 对于每个文本片段：
        ch <- agent.StreamEvent{
            Type:      agent.StreamTextDelta,
            TextDelta: "来自我的模型的回复！",
        }

        // 3. 如果模型请求工具调用：
        // ch <- agent.StreamEvent{
        //     Type: agent.StreamToolCall,
        //     ToolCall: &agent.ToolCallBlock{
        //         ID:        "call_1",
        //         Name:      "bash",
        //         Arguments: []byte(`{"command":"ls"}`),
        //     },
        // }

        // 4. 报告用量
        ch <- agent.StreamEvent{
            Type: agent.StreamUsage,
            Usage: &agent.Usage{
                InputTokens:  100,
                OutputTokens: 50,
                TotalTokens:  150,
            },
        }

        // 5. 发送完成信号
        ch <- agent.StreamEvent{
            Type:       agent.StreamDone,
            StopReason: "end_turn",
        }
    }()

    return ch
}
```

你也可以使用 Builder 上的 `WithProviderByName()` 方法，通过厂商名、Base URL、API 类型和 API Key 直接解析内置 Provider，无需自己实现 `Provider`：

```go
a, err := agent.NewBuilder().
    WithProviderByName("openai", "", "openai-chat", os.Getenv("OPENAI_API_KEY")).
    WithModel("gpt-4o").
    Build()
```

---

## 构建和运行 Agent

使用 `Builder` 流式 API 创建 Agent：

```go
package main

import (
    "context"
    "fmt"
    "os"

    "github.com/oschina/mothx/agent"
    _ "github.com/oschina/mothx/internal/agent" // 注册内部 builder
)

func main() {
    a, err := agent.NewBuilder().
        WithProvider(mybackend.NewMyProvider(os.Getenv("MY_API_KEY"))).
        WithModel("my-model-v1").
        WithMode("agent").                          // "plan"、"agent" 或 "yolo"
        WithWorkDir("/home/user/project").
        WithThinkingLevel(agent.ThinkingMedium).
        WithMaxTokens(16384).
        WithMaxIterations(200).
        WithToolExecutionMode("parallel").           // "parallel" 或 "sequential"
        WithMaxToolConcurrency(10).
        WithSystemPromptExtra("专注于 Go 代码。").
        WithCompaction(true, 16384).
        WithApprovalHandler(func(toolCallID, toolName string, args map[string]any) bool {
            fmt.Printf("批准执行 %s？[y/n] ", toolName)
            var input string
            fmt.Scanln(&input)
            return input == "y"
        }).
        Build()
    if err != nil {
        panic(err)
    }

    ctx := context.Background()
    events := a.Run(ctx, "列出这个项目中所有的 Go 文件")

    for event := range events {
        switch event.Type {
        case agent.EventTextDelta:
            fmt.Print(event.TextDelta)
        case agent.EventThinkDelta:
            // 思考内容（可选）
        case agent.EventToolCall:
            fmt.Printf("\n[工具: %s]\n", event.ToolCall.Name)
        case agent.EventToolExecutionEnd:
            fmt.Printf("[结果: %s]\n", truncate(event.ToolResult, 200))
        case agent.EventToolApprovalRequest:
            // 处理审批（参见 Builder.WithApprovalHandler）
        case agent.EventRunFinished:
            switch event.Status {
            case agent.TaskSuccess:
                fmt.Printf("\n--- 完成 (原因: %s) ---\n", event.StopReason)
            case agent.TaskCanceled:
                fmt.Fprintln(os.Stderr, "已取消")
            default:
                fmt.Fprintf(os.Stderr, "运行以状态 %s 结束: %v\n", event.Status, event.Error)
            }
        // EventDone 和 EventError 是兼容旧版的事件。
        }
    }
}

func truncate(s string, n int) string {
    if len(s) > n {
        return s[:n] + "..."
    }
    return s
}
```

### Builder 选项

| 方法 | 默认值 | 说明 |
|------|--------|------|
| `WithProvider(p)` | *必填* | LLM Provider |
| `WithProviderByName(vendor, baseURL, api, apiKey)` | — | 解析内置 Provider |
| `WithModel(id)` | 第一个模型 | 模型 ID |
| `WithMode(mode)` | `"yolo"` | `"plan"` / `"agent"` / `"yolo"` / `"os"` |
| `WithWorkDir(dir)` | `os.Getwd()` | 工作目录 |
| `WithThinkingLevel(level)` | `ThinkingMedium` | `Off` / `Minimal` / `Low` / `Medium` / `High` / `XHigh` |
| `WithMaxTokens(n)` | `16384` | 最大输出 token 数 |
| `WithMaxIterations(n)` | `200` | 循环迭代安全上限 |
| `WithToolExecutionMode(m)` | `"parallel"` | `"parallel"` / `"sequential"` |
| `WithMaxToolConcurrency(n)` | `10` | 每批 tool call 的本地并发上限；`1` 等价于串行 |
| `WithTools(names)` | 全部 | 过滤可用工具 |
| `WithSystemPromptExtra(s)` | `""` | 额外的系统提示词上下文 |
| `WithSandbox(bool)` | `false` | 启用沙箱隔离 |
| `WithSessionDir(dir)` | `~/.mothx/sessions` | 会话持久化目录 |
| `WithCompaction(enabled, reserve)` | `true, 16384` | 上下文压缩设置 |
| `WithMultiAgent(bool)` | `false` | 启用子 Agent 工具 |
| `WithApprovalHandler(fn)` | nil | 自定义工具审批回调 |

---

## 事件类型

`Event` 事件流遵循 Agent 生命周期：

```
EventAgentStart
  └─ EventTurnStart
       ├─ EventTextDelta（流式文本）
       ├─ EventThinkDelta（流式思考）
       ├─ EventToolCall（工具请求）
       ├─ EventToolExecutionStart → EventToolExecutionEnd
       ├─ EventToolResult
       ├─ EventToolApprovalRequest → EventToolApprovalResponse
       ├─ EventPlanUpdate
       └─ EventUsage
  └─ EventTurnEnd
  └─ ...（如果有工具调用则继续更多 turn）
  └─ EventRunFinished   ← 唯一规范终态事件（Status）
  └─ EventDone / EventError  ← 兼容旧版，在 EventRunFinished 之后发出
EventAgentEnd
```

每次运行恰好发出一个 `EventRunFinished`，位于任何旧版终态事件与
`EventAgentEnd` 之前。其 `Status` 是任务结果的唯一事实来源：

| TaskStatus | 含义 |
|------------|------|
| `success` | 运行正常完成并达成目标 |
| `incomplete` | 运行在达成目标前停止（输出/上下文限制、最大迭代、卡死检测），无硬性错误 |
| `failed` | 运行因错误终止（`Error` 携带详情） |
| `canceled` | 运行被用户、超时或 context 取消 |

消费者应基于 `EventRunFinished.Status` 判定结果；事件流在未发出终态事件时
关闭不能视为成功。

| 事件类型 | 关键字段 | 说明 |
|----------|----------|------|
| `EventAgentStart` | — | Agent 开始处理 |
| `EventAgentEnd` | `Messages` | Agent 处理完成，包含最终消息历史 |
| `EventTurnStart` | — | 新的 LLM turn 开始 |
| `EventTurnEnd` | `TurnMessage`, `ContextUsage` | turn 完成 |
| `EventTextDelta` | `TextDelta` | LLM 增量文本输出 |
| `EventThinkDelta` | `ThinkDelta` | LLM 增量思考输出 |
| `EventToolCall` | `ToolCall`, `ToolArgs` | LLM 请求工具调用 |
| `EventToolExecutionStart` | `ToolCallID`, `ToolName`, `ToolArgs` | 工具执行开始 |
| `EventToolExecutionEnd` | `ToolCallID`, `ToolResult`, `ToolDiff`, `ToolError` | 工具执行完成 |
| `EventToolResult` | `ToolCallID`, `ToolResult` | 工具结果已记录 |
| `EventToolApprovalRequest` | `ApprovalID`, `ApprovalTool`, `ApprovalArgs` | 工具需要用户审批 |
| `EventPlanUpdate` | `Plan` | 结构化任务计划更新 |
| `EventUsage` | `Usage`, `ContextUsage` | Token 用量报告 |
| `EventDone` | `StopReason`, `Usage` | Agent 循环完成（旧版事件；建议改用 `EventRunFinished.Status`） |
| `EventError` | `Error`, `StopReason` | 发生错误（旧版事件；建议改用 `EventRunFinished.Status`） |
| `EventRunFinished` | `Status`, `StopReason`, `Usage`, `Error`, `Attachments`, `ContextUsage` | 唯一规范终态事件；`Status` 为 `success` / `incomplete` / `failed` / `canceled` |
| `EventCompactionStart/End` | `StatusMessage` | 上下文压缩生命周期 |

---

## 子 Agent 模式

子 Agent 模式允许主 Agent 将有明确边界的独立子任务委派给并行运行的子 Agent。通过 CLI（`--multi-agent`）或 SDK（`WithMultiAgent(true)`）启用。

### 架构概览

```
┌─────────────────────────────────────────────────┐
│                 主 Agent (Main)                   │
│  - 完整的系统提示词、工具、上下文                   │
│  - 编排者角色                                     │
│  - 拥有 subagent_* 工具                           │
├─────────────────────────────────────────────────┤
│         AgentManager                             │
│   ┌──────────┐  ┌──────────┐  ┌──────────┐     │
│   │ 子Agent  │  │ 子Agent  │  │ 子Agent  │     │
│   │   #1     │  │   #2     │  │   #3     │     │
│   │ (搜索)   │  │ (审查)   │  │ (测试)   │     │
│   └──────────┘  └──────────┘  └──────────┘     │
│         ↑            ↑             ↑             │
│     独立的        独立的        独立的             │
│     上下文、      上下文、      上下文、           │
│     注册表、      注册表、      注册表、           │
│     会话          会话          会话               │
└─────────────────────────────────────────────────┘
```

### 核心组件

| 组件 | 包 | 说明 |
|------|-----|------|
| `AgentManager` | `internal/agent` | 管理所有 Agent 实例的生命周期，追踪父子关系，执行策略 |
| `AgentFactory` | `internal/agent` | 以一致的配置创建 Agent，每个 Agent 拥有独立的工具注册表 |
| `EventRouter` | `internal/agent` | 按 `AgentID` 路由事件到对应处理器或全局处理器 |
| `SubAgentPolicy` | `internal/agent` | 安全约束：最多 5 个子 Agent、允许的模式、每个 Agent 超时 10 分钟 |
| `subagent_*` 工具 | `internal/agent` | 主 Agent 用来创建/管理子 Agent 的工具 |

### 子 Agent 工具

启用多 Agent 模式后，主 Agent 会获得四个工具：

#### `subagent_spawn`

创建并启动一个有明确边界的子 Agent 任务。

```json
{
  "task": "搜索 src/ 目录下已废弃函数 X 的所有使用",
  "mode": "agent",
  "work_dir": "/home/user/project",
  "tools": ["read", "grep", "find", "ls"],
  "max_iterations": 50,
  "system_prompt_extra": "仅关注 src/ 目录"
}
```

返回一个用于轮询的 handle：

```json
{
  "handle": "agent-1",
  "status": "running",
  "timeout": "10m0s"
}
```

#### `subagent_status`

查询子 Agent 的状态和结果：

```json
{
  "handle": "agent-1"
}
```

返回：

```json
{
  "handle": "agent-1",
  "status": "done",
  "message_count": 12,
  "last_response": "找到 3 处函数 X 的使用: ...",
  "updated_at": "2025-05-28T10:30:00Z"
}
```

可能的状态值：`"ready"`、`"running"`、`"done"`、`"error"`。

#### `subagent_send`

向运行中的子 Agent 发送后续消息：

```json
{
  "handle": "agent-1",
  "message": "也检查一下 test/ 目录"
}
```

#### `subagent_destroy`

销毁已完成的子 Agent 并释放资源：

```json
{
  "handle": "agent-1"
}
```

### 子 Agent 策略和约束

| 约束 | 默认值 | 说明 |
|------|--------|------|
| 最大子 Agent 数 | 5 | 每个父 Agent 最多并发子 Agent 数 |
| 允许的模式 | `["plan", "agent", "yolo"]` | 子 Agent 默认继承父模式，否则回退到 `yolo` |
| 单个 Agent 超时 | 10 分钟 | 每个子 Agent 有独立的超时时间 |
| 总超时 | 30 分钟 | 所有子 Agent 的全局超时 |
| 嵌套 | 禁止 | 子 Agent **不能**创建自己的子 Agent |
| 沙箱 | 继承 | 子 Agent 继承父 Agent 的沙箱配置 |

### 子 Agent 隔离

每个子 Agent 运行时拥有**完全隔离的状态**：

- **独立工具注册表** — 拥有自己的 `tools.Registry`，包含独立的 `workDir`、`Sandbox` 和 `JobManager`
- **独立消息历史** — 独立的对话上下文
- **独立会话** — 独立的会话存储
- **工具过滤** — `subagent_*` 工具从子 Agent 的注册表中移除，防止嵌套
- **额外上下文** — 包含 `SubAgentOperatingContract`，指示子 Agent 在任务范围内工作

### SDK 用法：启用多 Agent 模式

```go
a, err := agent.NewBuilder().
    WithProvider(myProvider).
    WithModel("claude-sonnet-4-20250514").
    WithMode("agent").
    WithMultiAgent(true). // 启用子 Agent 工具
    Build()
```

设置 `WithMultiAgent(true)` 后，Agent 的系统提示词将包含子 Agent 编排指令，`subagent_spawn/status/send/destroy` 工具将变为可用。

### 子 Agent 的事件路由

子 Agent 的事件携带子 Agent 的 `AgentID`。使用 `EventRouter` 将事件分发到正确的处理器：

```go
// 内部使用示例（仅供参考）
router := agent.NewEventRouter()

// 为特定 Agent 注册处理器
router.RegisterAgent("agent-1", agent.RouterEventHandlerFunc(func(e agent.Event) error {
    fmt.Printf("[%s] %v\n", e.AgentID, e.Type)
    return nil
}))

// 注册全局处理器，接收所有 Agent 的事件
router.RegisterGlobal(agent.RouterEventHandlerFunc(func(e agent.Event) error {
    // 记录所有 Agent 的事件
    return nil
}))
```

### 子 Agent 最佳实践

1. **为独立工作创建子 Agent** — 子 Agent 最适合并行代码搜索、审查、测试或调查等互不依赖的任务。
2. **给出清晰的范围** — 每个子 Agent 的任务应包含：做什么、在哪里找、产出什么、何时停止。
3. **限制工具** — 将工具限制为任务所需（例如搜索任务只需只读工具）。
4. **轮询并验证** — 不要盲目信任子 Agent 的结果。使用 `subagent_status` 检查后验证重要结论。
5. **及时清理** — 始终对已完成的 Agent 调用 `subagent_destroy` 释放资源。
6. **避免过度委派** — 小型、顺序或高度有状态的工作直接在主 Agent 中完成更好。

---

## 外部工具（嵌入式使用）

将 MothX 嵌入到自己的应用中时，可以将宿主应用提供的能力暴露给 Agent，与内置编码工具并存（或完全替代）。

### ExternalTool 接口

```go
// ExternalTool 由嵌入方实现，用于暴露自定义工具。
type ExternalTool interface {
    Name() string
    Description() string
    Parameters() json.RawMessage
    Execute(ctx context.Context, params map[string]any) (*ExternalToolResult, error)
}
```

### ExternalToolResult

```go
type ExternalToolResult struct {
    Text     string          // 纯文本结果
    Error    string          // 错误消息（如有）
    Contents []ContentBlock  // 可选的富内容块
}
```

### 注册外部工具

```go
import (
    "github.com/oschina/mothx/agent"
    _ "github.com/oschina/mothx/bootstrap" // 嵌入时必须
)

a, err := agent.NewBuilder().
    WithProvider(myProvider).
    WithModel("my-model").
    WithExternalTools(myDatabaseTool, myAPITool).  // 注册自定义工具
    Build()
```

也可以禁用所有内置工具，仅使用宿主提供的工具：

```go
a, err := agent.NewBuilder().
    WithProvider(myProvider).
    WithModel("my-model").
    WithoutBuiltinTools().           // 禁用 read, write, bash 等
    WithExternalTools(myCustomTool). // 仅自定义工具
    Build()
```

### 系统提示词贡献

可选地实现 `ExternalToolPromptInfo` 来贡献系统提示词信息：

```go
type ExternalToolPromptInfo interface {
    PromptSnippet() string     // 关于工具的简短上下文
    PromptGuidelines() string  // LLM 使用指南
}
```

### Bootstrap 包

外部模块必须空白导入 `bootstrap` 包一次，以注册内部 builder 和 provider 解析 hook（因为 internal 包无法被直接导入）：

```go
import _ "github.com/oschina/mothx/bootstrap"
```

### 审批转发

子 Agent 中需要审批的工具调用（例如 agent 模式下的 `bash`）会被转发到父 Agent 的事件通道。父 TUI 或审批处理器会看到携带子 Agent `AgentID` 的 `EventToolApprovalRequest` 事件，用户可以在单一界面上审批/拒绝所有 Agent 的工具调用。

---

## 内部架构参考

供需要了解内部接线的开发者参考：

```
agent/                          # 公共包（导入这个）
  ├── types.go                  # Agent、Message、Event 类型
  ├── provider.go               # Provider、ChatParams、StreamEvent 类型
  └── builder.go                # Builder API → 调用 buildInternal

internal/agent/                 # 内部实现
  ├── agent.go                  # 核心 Agent 循环
  ├── factory.go                # AgentFactory（创建具有独立注册表的 Agent）
  │   └── init() { SetBuilderFunc(buildFromPublicBuilder) }
  ├── bridge.go                 # 类型转换器（公共 ↔ 内部）
  │   ├── ProviderAdapter       # 包装公共 Provider → 内部
  │   └── AgentAdapter          # 包装内部 Agent → 公共
  ├── manager.go                # AgentManager（生命周期、父子关系追踪）
  ├── subagent.go               # subagent_spawn/status/send/destroy 工具
  ├── router.go                 # EventRouter（按 Agent + 全局分发）
  └── system_prompt.go          # 系统提示词构建器
```

`internal/agent/bridge.go` 中的桥接层自动完成公共和内部类型的转换：

- `agent.Builder.Build()` → 调用 `buildFromPublicBuilder()` → 创建内部 `Agent` → 包装为 `AgentAdapter` → 返回 `agent.Agent`
- 公共 `Provider` → `ProviderAdapter` → 内部 `provider.Provider`
- 内部 `Event` → `EventToPublic()` → 公共 `agent.Event`
- 内部 `Message` → `MessageToPublic()` → 公共 `agent.Message`（及反向）
