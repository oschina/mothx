# 开发指南

本文档介绍如何为 MothX 贡献代码。

## 开发环境搭建

### 系统要求

- Go 1.27+
- Git
- Make (可选)
- bubblewrap (可选，用于沙箱测试)

### 获取源码

```bash
git clone https://gitee.com/oschina/mothx.git
cd mothx
```

### 安装依赖

```bash
go mod download
```

### 构建项目

```bash
# 构建
make build

# 安装到 $GOPATH/bin
make install
```

### 运行测试

```bash
# 运行所有测试
make test

# 运行特定包的测试
go test ./internal/tools/

# 运行特定测试
go test -run TestReadTool ./internal/tools/
```

## 项目结构

```
mothx/
├── agent/                     # 公共 SDK 类型（Agent、Provider、Builder）
├── bootstrap/                 # 将公共 SDK 接口接到内部实现
├── cmd/mothx/                 # CLI 入口与子命令
├── desktop/                   # Electron 桌面客户端（纯 ACP）
├── ui/                        # 内嵌到 Serve 的 Svelte Web UI
├── internal/
│   ├── agent/                 # 核心 Agent 循环、事件、提示词、子 Agent 工具
│   ├── agentruntime/          # 前端中立的共享 Runtime 与 Run 生命周期
│   ├── acp/                   # ACP 服务器与 mothx.dev 扩展
│   ├── a2a/                   # A2A 服务器与远程 Agent master 调度
│   ├── architecture/          # 架构护栏测试
│   ├── browser/               # vibe-browser 工具
│   ├── config/                # settings.json / serve.json
│   ├── context/, contextfiles/# Token 预算与项目上下文
│   ├── cron/                  # 定时任务存储与调度器
│   ├── dao/                   # 唯一 SQL/查询/行映射 owner
│   ├── db/                    # 连接/事务/迁移生命周期
│   ├── esm/                   # Enable Supervisor Mode
│   ├── expert/                # 主角团包发现与校验
│   ├── imageproc/             # 图像归一化
│   ├── mcp/                   # MCP 集成
│   ├── memory/, messaging/    # 通道记忆与平台抽象
│   ├── platform/              # 跨平台辅助与资源
│   ├── provider/              # 协议 provider、vendor 适配器、factory
│   ├── sandbox/               # 沙箱抽象
│   ├── serve/                 # OpenAI API、Web UI、channels、hooks、cron
│   ├── session/               # 会话领域、迁移、事件存储
│   ├── skills/, skillhub/     # 技能与市场
│   ├── stats/                 # 用量统计
│   ├── systeminit/            # AGENTS.md 生成
│   ├── tools/                 # 内置工具
│   ├── tui/                   # Bubble Tea TUI
│   └── workflow/              # JavaScript workflow 运行时
├── example/                   # 公共 SDK 示例
└── docs/                      # 双语文档、生成索引与站点
```

## 核心接口

### Provider 接口

```go
type Provider interface {
    Name() string
    Models() []*Model
    GetModel(id string) *Model
    Chat(ctx context.Context, params ChatParams) <-chan StreamEvent
}
```

### Tool 接口

```go
type Tool interface {
    Name() string
    Description() string
    PromptSnippet() string
    PromptGuidelines() []string
    Parameters() json.RawMessage
    Execute(ctx context.Context, params map[string]any) (ToolResult, error)
}
```

## Agent Runtime 规则

生产代码的 Agent 构造与执行遵循共享 Runtime 边界：

- 只能通过 `SessionRuntime.BuildAgent`、`BuildTransientAgent` 或
  `agentruntime.NewAgentManager` 创建 Agent；不要在适配器中拼装完整
  `agent.Config`，也不要直接调用 `agent.New`/`agent.NewWithLoopConfig`。
- 通过 `ResolveSource`/`ResolvePolicy` 统一解析一次 source、模式、能力、工具、
  沙箱、审批、MCP 与运行策略；适配器只投影结果。
- 持久化 Run 使用 `ExecutionRuntime`/`RunStore`，并且只产生一个规范终态事件；
  不要实现第二套 Run 状态机。
- 审批与提问使用 `DecisionService`/`DecisionRecord`。
- 所有 SQL 放在 `internal/dao`；`internal/db` 只负责连接、事务与迁移生命周期。
- 保持入口层轻薄：TUI、CLI、WebUI/API、Desktop/ACP、channels、Cron、ESM 与
  A2A 可以解码协议和渲染事件，但不能拥有会话、Run 生命周期、附件、制品或策略。
- 移动生产构造或持久化调用点后，运行 `go test ./internal/architecture`。

## 添加新工具

### 步骤 1: 创建工具文件

```go
// internal/tools/mytool.go
package tools

import (
    "context"
    "encoding/json"
)

type MyTool struct {
    workdir string
}

func NewMyTool(workdir string) *MyTool {
    return &MyTool{workdir: workdir}
}

func (t *MyTool) Name() string {
    return "mytool"
}

func (t *MyTool) Description() string {
    return "Description of my tool"
}

func (t *MyTool) PromptSnippet() string {
    return "系统提示词中的一行简短描述"
}

func (t *MyTool) PromptGuidelines() []string {
    return []string{"当……时使用该工具"}
}

func (t *MyTool) Parameters() json.RawMessage {
    return json.RawMessage(`{
        "type": "object",
        "properties": {
            "param1": {
                "type": "string",
                "description": "First parameter"
            }
        },
        "required": ["param1"]
    }`)
}

func (t *MyTool) Execute(ctx context.Context, params map[string]any) (ToolResult, error) {
    param1, _ := params["param1"].(string)
    if param1 == "" {
        return ToolResult{}, fmt.Errorf("param1 is required")
    }
    return NewTextToolResult("result"), nil
}
```

### 步骤 2: 注册工具

在 `internal/tools/tool.go` 的 `builtinToolOrder` 中加入规范名称，并在
`builtinToolFactories()` 中注册构造函数。`RegisterDefaults`、
`RegisterDefaultsWithPlanTool` 与 `RegisterFiltered` 都从这两个列表派生，
因此注册会保持一致。

### 步骤 3: 为系统提示词提供描述

从工具返回简洁的 `PromptSnippet` 与有针对性的 `PromptGuidelines`。共享 Runtime
会用这些方法构建 Available tools 与 Guidelines 段落；不要在各适配器中重复添加
提示词文本。

### 步骤 4: 编写测试

```go
// internal/tools/mytool_test.go
package tools

import (
    "context"
    "testing"
)

func TestMyTool_Execute(t *testing.T) {
    tool := NewMyTool("/tmp")
    result, err := tool.Execute(context.Background(), map[string]any{"param1": "value"})
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if result.Text != "expected" {
        t.Errorf("expected 'expected', got %q", result.Text)
    }
}
```

## 添加 Provider 支持

大多数新服务应作为厂商适配器接入，而不是新增协议 provider。如果服务兼容 OpenAI Chat Completions 或 Anthropic Messages，应复用通用 provider，并在 `internal/provider` 中注册厂商默认值。

### 添加 OpenAI/Anthropic 兼容厂商

1. 创建 `internal/provider/vendor_myvendor.go`。
2. 使用 `RegisterVendorAdapter` 注册 URL 识别和默认值。
3. 只有当模型行为与通用协议不一致时，才增加模型 `compat` 标志。
4. 在 `internal/provider` 添加聚焦测试；如果请求格式变化，再补 `internal/provider/openai` 或 `internal/provider/anthropic` 测试。

```go
package provider

func init() {
    RegisterVendorAdapter(simpleVendorAdapter{
        name:           "myvendor",
        domains:        []string{"api.myvendor.example"},
        thinkingFormat: "deepseek", // 可选
        defaultAPI:     "openai-chat",
    })
}
```

CLI、ACP、WebUI/Serve 与 Desktop 的 provider 创建统一走 `internal/provider/factory`；Agent 构造统一走 `agentruntime`。不要在适配器中添加厂商专用创建逻辑，也不要在共享 Runtime 之外拼装完整 `agent.Config`。

### 添加新的协议 Provider

只有当服务使用 OpenAI Chat Completions / Anthropic Messages 之外的原生协议时，才新增 provider 包。

1. 创建 `internal/provider/myprotocol`。
2. 实现 `provider.Provider`。
3. 在 `internal/provider/factory` 增加创建逻辑。
4. 保持 settings JSON 兼容。
5. 添加 provider 和 factory 测试。

## 测试

### 运行所有测试

```bash
make test
make test-desktop     # Electron main/preload/renderer 测试
```

### 运行特定包的测试

```bash
go test ./internal/agent/
go test ./internal/tools/
```

### 生成覆盖率报告

```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码规范

### 格式化

```bash
make fmt
```

或手动:

```bash
gofmt -w .
goimports -w .
```

### Lint

```bash
make lint
```

### 命名规范

- 包名: 小写单词，如 `tools`, `agent`
- 接口名: 动词或名词，如 `Provider`, `Tool`
- 结构体名: 大驼峰，如 `ReadTool`, `AgentConfig`
- 函数名: 大驼峰，如 `NewAgent`, `Execute`
- 变量名: 小驼峰，如 `workdir`, `maxTokens`

### 错误处理

```go
// 好的做法
result, err := doSomething()
if err != nil {
    return fmt.Errorf("do something: %w", err)
}

// 不好的做法
result, _ := doSomething()
```

## Git 工作流

### Commit 规范

使用 Conventional Commits:

```
<type>(<scope>): <subject>

[optional body]

[optional footer]
```

类型:
- `feat`: 新功能
- `fix`: Bug 修复
- `docs`: 文档
- `refactor`: 重构
- `test`: 测试
- `chore`: 杂项

示例:

```
feat(tools): add new search tool
fix(agent): fix streaming issue
docs(readme): update installation guide
```

### Pull Request

1. Fork 项目
2. 创建功能分支
3. 提交更改
4. 运行测试
5. 创建 Pull Request

## 调试

### 启用调试日志

```bash
mothx --debug
```

### 使用 dlv

```bash
# 安装 dlv
go install github.com/go-delve/delve/cmd/dlv@latest

# 调试
dlv debug ./cmd/mothx -- --debug
```

## 发布流程

### 版本号

使用 Semantic Versioning:

```
MAJOR.MINOR.PATCH

例如: 1.0.0, 1.1.0, 1.0.1
```

### 创建发布

```bash
# 更新版本号
git tag -a v1.0.0 -m "Release v1.0.0"

# 推送标签
git push --tags

# 构建发布包
make build-all
```

### 发布到 npm

```bash
# 发布正式版本
make npm-publish-all

# 发布预发布版本
make npm-publish-pre
```

用户可以通过 npm 安装:

```bash
npm install -g mothx-installer
```

### 发布到 PyPI

```bash
# 安装发布工具
python3 -m pip install --user "setuptools>=77.0.0" build twine

# 发布正式版本 wheels
make pypi-publish

# 发布预发布版本 wheels
make pypi-publish-pre
```

用户可以通过 PyPI 安装:

```bash
pipx install mothx-installer
```

## 常见问题

### Q: 如何添加新的工具?

A: 参考 [添加新工具](#添加新工具) 章节。

### Q: 如何添加新的 Provider?

A: 参考 [添加新 Provider](#添加新-provider) 章节。

### Q: 测试失败怎么办?

A:
1. 检查 Go 版本
2. 运行 `go mod tidy`
3. 查看错误日志

### Q: 如何调试沙箱问题?

A:
1. 使用 `--debug` 参数
2. 检查 bwrap 是否安装: `bwrap --version`
3. 查看系统日志
