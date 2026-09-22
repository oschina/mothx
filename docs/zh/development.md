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
├── agent/                     # 公共 Agent/Provider 接口和 Builder
├── bootstrap/                 # 嵌入式使用的 bootstrap 包
├── cmd/mothx/            # CLI 入口点
│   ├── main.go                # 主程序
│   └── main_doctor.go         # `doctor` 子命令
├── internal/
│   ├── a2a/                   # A2A 协议服务器和 Master 模式
│   ├── acp/                   # ACP / MCP 集成
│   ├── agent/                 # 核心 Agent 循环
│   │   ├── agent.go           # Agent 主逻辑
│   │   ├── factory.go         # AgentFactory
│   │   ├── manager.go         # AgentManager
│   │   ├── router.go          # EventRouter
│   │   ├── subagent.go        # subagent_* 工具
│   │   ├── events.go          # 事件类型定义
│   │   ├── provider.go        # Provider 适配器
│   │   └── system_prompt.go   # 系统提示词生成
│   ├── config/                # 配置管理
│   ├── context/               # 上下文管理和 token 估算
│   ├── contextfiles/          # 上下文文件加载
│   ├── cron/                  # 定时任务存储和调度器
│   ├── serve/                 # 统一 OpenAI API、Web UI 和消息通道
│   ├── mcp/                   # MCP 服务器集成
│   ├── memory/                # 持久化记忆 (memory.md)
│   ├── messaging/             # 消息平台抽象
│   ├── platform/              # 跨平台兼容性
│   ├── provider/              # LLM Provider 抽象
│   │   ├── anthropic/         # Anthropic Messages API
│   │   ├── google/            # Google Gemini/Vertex API
│   │   ├── openai/            # OpenAI Chat Completions API
│   │   ├── factory/           # 共享 provider/model 构建
│   │   └── vendor*.go         # 厂商适配器注册表
│   ├── sandbox/               # 沙箱抽象 (bwrap, none)
│   ├── session/               # 会话管理 (SQLite)
│   ├── skills/                # 技能系统
│   ├── stats/                 # 用量统计 Web 面板
│   ├── systeminit/            # AGENTS.md 生成
│   ├── tools/                 # 工具实现
│   │   ├── bash.go            # Bash 命令执行
│   │   ├── read.go            # 文件读取
│   │   ├── write.go           # 文件写入
│   │   ├── edit.go            # 文件编辑
│   │   ├── grep.go            # 内容搜索（纯 Go ripgrep）
│   │   ├── find.go            # 文件查找（纯 Go fd）
│   │   ├── ls.go              # 目录列表
│   │   ├── plan.go            # 任务计划
│   │   ├── question.go        # 用户问题澄清
│   │   ├── skill_ref.go       # 技能引用加载
│   │   └── a2a_dispatch.go    # A2A 远程 Agent 分发
│   ├── tui/                   # 终端 UI (BubbleTea)
│   └── workflow/              # 工作流运行时 (JavaScript DSL)
├── example/                   # SDK 示例
└── pkg/sdk/                   # 公共 SDK 接口
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
    Parameters() json.RawMessage
    Execute(ctx context.Context, params json.RawMessage) (string, error)
}
```

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

func (t *MyTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
    var p struct {
        Param1 string `json:"param1"`
    }
    if err := json.Unmarshal(params, &p); err != nil {
        return "", err
    }
    // 实现工具逻辑
    return "result", nil
}
```

### 步骤 2: 注册工具

在 `internal/tools/tool.go` 的 `RegisterDefaults()` 方法中添加:

```go
func (r *Registry) RegisterDefaults() {
    r.Register(&ReadTool{workdir: r.workdir})
    r.Register(&WriteTool{workdir: r.workdir})
    r.Register(&EditTool{workdir: r.workdir})
    r.Register(&BashTool{workdir: r.workdir, sandbox: r.sandbox})
    r.Register(&GrepTool{workdir: r.workdir})
    r.Register(&FindTool{workdir: r.workdir})
    r.Register(&LsTool{workdir: r.workdir})
    r.Register(&MyTool{workdir: r.workdir}) // 添加新工具
}
```

### 步骤 3: 更新系统提示词

在 `internal/agent/system_prompt.go` 中添加工具描述。

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
    params := `{"param1": "value"}`
    
    result, err := tool.Execute(context.Background(), json.RawMessage(params))
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    
    if result != "expected" {
        t.Errorf("expected 'expected', got '%s'", result)
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

CLI 和 ACP 的 provider 创建统一走 `internal/provider/factory`，不要在 `cmd/mothx/main.go` 或 `internal/acp/acp.go` 中添加厂商专用创建逻辑。

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
