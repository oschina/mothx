# Development Guide

This document describes how to contribute code to MothX.

## Development Environment Setup

### System Requirements

- Go 1.27+
- Git
- Make (optional)
- bubblewrap (optional, for sandbox testing)

### Get Source Code

```bash
git clone https://github.com/oschina/mothx.git
cd mothx
```

### Install Dependencies

```bash
go mod download
```

### Build Project

```bash
# Build
make build

# Install to $GOPATH/bin
make install
```

### Run Tests

```bash
# Run all tests
make test

# Run tests for specific package
go test ./internal/tools/

# Run specific test
go test -run TestReadTool ./internal/tools/
```

## Project Structure

```
mothx/
├── agent/                     # Public SDK types (Agent, Provider, Builder)
├── bootstrap/                 # Public SDK wiring into internal implementations
├── cmd/mothx/                 # CLI entry point and subcommands
├── desktop/                   # Electron desktop client (pure ACP)
├── ui/                        # Svelte Web UI embedded into Serve
├── internal/
│   ├── agent/                 # Core Agent loop, events, prompts, sub-agent tools
│   ├── agentruntime/          # Shared front-end-neutral Runtime and run lifecycle
│   ├── acp/                   # ACP server and mothx.dev extensions
│   ├── a2a/                   # A2A server and remote-agent master dispatch
│   ├── architecture/          # Architecture guard tests
│   ├── browser/               # vibe-browser tool
│   ├── config/                # settings.json / serve.json
│   ├── context/, contextfiles/# Token budget and project context
│   ├── cron/                  # Scheduled task store and scheduler
│   ├── dao/                   # Sole SQL/query/row-mapping owner
│   ├── db/                    # Connection/transaction/migration lifecycle
│   ├── esm/                   # Enable Supervisor Mode
│   ├── expert/                # Expert Team bundle discovery/validation
│   ├── imageproc/             # Image normalization
│   ├── mcp/                   # MCP integration
│   ├── memory/, messaging/    # Channel memory and platform abstraction
│   ├── platform/              # Cross-platform helpers and assets
│   ├── provider/              # Protocol providers, vendor adapters, factory
│   ├── sandbox/               # Sandbox abstraction
│   ├── serve/                 # OpenAI API, Web UI, channels, hooks, cron
│   ├── session/               # Session domain, migrations, event store
│   ├── skills/, skillhub/     # Skills and marketplace
│   ├── stats/                 # Usage statistics
│   ├── systeminit/            # AGENTS.md generation
│   ├── tools/                 # Built-in tools
│   ├── tui/                   # Bubble Tea TUI
│   └── workflow/              # JavaScript workflow runtime
├── example/                   # Public SDK examples
└── docs/                      # Bilingual docs, generated indexes, site
```

## Core Interfaces

### Provider Interface

```go
type Provider interface {
    Name() string
    Models() []*Model
    GetModel(id string) *Model
    Chat(ctx context.Context, params ChatParams) <-chan StreamEvent
}
```

### Tool Interface

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

## Agent Runtime Rules

Production Agent construction and execution follow the shared Runtime boundary:

- Build Agents only through `SessionRuntime.BuildAgent`, `BuildTransientAgent`, or
  `agentruntime.NewAgentManager`. Do not add adapter-local `agent.Config`
  assembly or call `agent.New`/`agent.NewWithLoopConfig`.
- Resolve source, mode, capabilities, tools, sandbox, approval, MCP, and run
  policy once with `ResolveSource`/`ResolvePolicy`; adapters project the result.
- Use `ExecutionRuntime`/`RunStore` for durable runs and exactly one canonical
  terminal event. Do not create a second run state machine.
- Use `DecisionService`/`DecisionRecord` for approvals and questions.
- Put all SQL in `internal/dao`; use `internal/db` only for connection,
  transaction, and migration lifecycle.
- Keep entry points thin: TUI, CLI, WebUI/API, Desktop/ACP, channels, Cron, ESM,
  and A2A may decode protocols and render events, but not own sessions, run
  lifecycle, attachments, artifacts, or policy.
- Run `go test ./internal/architecture` after moving production construction or
  persistence call sites.

## Adding New Tools

### Step 1: Create Tool File

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
    return "Short one-line description for the system prompt"
}

func (t *MyTool) PromptGuidelines() []string {
    return []string{"Use this tool when ..."}
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

### Step 2: Register Tool

Add the canonical name to `builtinToolOrder` and its constructor to
`builtinToolFactories()` in `internal/tools/tool.go`. `RegisterDefaults`,
`RegisterDefaultsWithPlanTool`, and `RegisterFiltered` all derive from those two
lists, so the tool is registered consistently.

### Step 3: Describe it for the system prompt

Return a concise `PromptSnippet` and targeted `PromptGuidelines` from the tool.
The shared Runtime builds the Available tools and Guidelines sections from those
methods; do not add per-adapter prompt text.

### Step 4: Write Tests

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

## Adding Provider Support

Most new services should be added as vendor adapters, not new protocol
providers. If the service speaks OpenAI Chat Completions or Anthropic Messages,
reuse the generic provider and register vendor defaults in `internal/provider`.

### Add an OpenAI/Anthropic-Compatible Vendor

1. Create `internal/provider/vendor_myvendor.go`.
2. Register URL detection and defaults with `RegisterVendorAdapter`.
3. Add model `compat` flags only for behavior that differs from the generic protocol.
4. Add focused tests in `internal/provider` and, if request formatting changes, in `internal/provider/openai` or `internal/provider/anthropic`.

```go
package provider

func init() {
    RegisterVendorAdapter(simpleVendorAdapter{
        name:           "myvendor",
        domains:        []string{"api.myvendor.example"},
        thinkingFormat: "deepseek", // optional
        defaultAPI:     "openai-chat",
    })
}
```

Provider creation for CLI, ACP, WebUI/Serve, and Desktop goes through
`internal/provider/factory`; Agent construction goes through `agentruntime`.
Do not add vendor-specific creation code to adapters or assemble a complete
`agent.Config` outside the shared Runtime.

### Add a New Protocol Provider

Only add a new provider package when the service has a native protocol that is
not covered by OpenAI Chat Completions or Anthropic Messages.

1. Create `internal/provider/myprotocol`.
2. Implement `provider.Provider`.
3. Add construction support in `internal/provider/factory`.
4. Keep settings JSON compatibility stable.
5. Add provider and factory tests.

## Testing

### Run All Tests

```bash
make test
make test-desktop     # Electron main/preload/renderer tests
```

### Run Tests for Specific Package

```bash
go test ./internal/agent/
go test ./internal/tools/
```

### Generate Coverage Report

```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## Code Standards

### Formatting

```bash
make fmt
```

Or manually:

```bash
gofmt -w .
goimports -w .
```

### Linting

```bash
make lint
```

### Naming Conventions

- Package names: lowercase words, e.g., `tools`, `agent`
- Interface names: verbs or nouns, e.g., `Provider`, `Tool`
- Struct names: PascalCase, e.g., `ReadTool`, `AgentConfig`
- Function names: PascalCase, e.g., `NewAgent`, `Execute`
- Variable names: camelCase, e.g., `workdir`, `maxTokens`

### Error Handling

```go
// Good practice
result, err := doSomething()
if err != nil {
    return fmt.Errorf("do something: %w", err)
}

// Bad practice
result, _ := doSomething()
```

## Git Workflow

### Commit Convention

Use Conventional Commits:

```
<type>(<scope>): <subject>

[optional body]

[optional footer]
```

Types:
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation
- `refactor`: Refactoring
- `test`: Tests
- `chore`: Miscellaneous

Examples:

```
feat(tools): add new search tool
fix(agent): fix streaming issue
docs(readme): update installation guide
```

### Pull Request

1. Fork project
2. Create feature branch
3. Commit changes
4. Run tests
5. Create Pull Request

## Debugging

### Enable Debug Logging

```bash
mothx --debug
```

### Using dlv

```bash
# Install dlv
go install github.com/go-delve/delve/cmd/dlv@latest

# Debug
dlv debug ./cmd/mothx -- --debug
```

## Release Process

### Version Numbers

Use Semantic Versioning:

```
MAJOR.MINOR.PATCH

Example: 1.0.0, 1.1.0, 1.0.1
```

### Create Release

```bash
# Update version number
git tag -a v1.0.0 -m "Release v1.0.0"

# Push tags
git push --tags

# Build release packages
make build-all
```

### Publish to npm

```bash
# Publish release version
make npm-publish-all

# Publish pre-release version
make npm-publish-pre
```

Users can install via npm:

```bash
npm install -g mothx-installer
```

### Publish to PyPI

```bash
# Install publishing tools
python3 -m pip install --user "setuptools>=77.0.0" build twine

# Publish release wheels
make pypi-publish

# Publish pre-release wheels
make pypi-publish-pre
```

Users can install via PyPI:

```bash
pipx install mothx-installer
```

## Frequently Asked Questions

### Q: How to add a new tool?

A: See [Adding New Tools](#adding-new-tools) section.

### Q: How to add a new Provider?

A: See [Adding New Providers](#adding-new-providers) section.

### Q: What to do if tests fail?

A:
1. Check Go version
2. Run `go mod tidy`
3. Check error logs

### Q: How to debug sandbox issues?

A:
1. Use `--debug` flag
2. Check if bwrap is installed: `bwrap --version`
3. Check system logs
