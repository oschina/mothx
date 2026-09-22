package agent

import (
	"fmt"
	"os"
)

// Builder provides a fluent API for creating Agent instances.
// External developers use this to instantiate the built-in Agent without
// depending on internal packages.
//
// Usage:
//
//	a, err := agent.NewBuilder().
//	    WithProvider(myProvider).
//	    WithModel("gpt-4").
//	    WithMode("yolo").
//	    WithWorkDir("/home/user/project").
//	    Build()
type Builder struct {
	provider            Provider
	modelID             string
	mode                string
	workDir             string
	thinkingLevel       ThinkingLevel
	maxTokens           int
	systemPromptExtra   string
	maxIterations       int
	toolExecutionMode   string
	maxToolConcurrency  int
	tools               []string
	sandboxEnabled      bool
	sessionDir          string
	compactionEnabled   bool
	compactionReserve   int
	multiAgent          bool
	delegateMode        bool
	approvalHandler     func(toolCallID, toolName string, args map[string]any) bool
	externalTools       []ExternalTool
	disableBuiltinTools bool
	err                 error
}

// DefaultMaxToolConcurrency is the default number of local tool calls that may
// execute concurrently in one agent turn.
const DefaultMaxToolConcurrency = 10

// NewBuilder creates a new Builder with sensible defaults.
func NewBuilder() *Builder {
	return &Builder{
		mode:               "yolo",
		thinkingLevel:      ThinkingMedium,
		maxTokens:          16384,
		maxIterations:      200,
		toolExecutionMode:  "parallel",
		maxToolConcurrency: DefaultMaxToolConcurrency,
		compactionEnabled:  true,
		compactionReserve:  16384,
	}
}

// WithProvider sets the LLM provider.
func (b *Builder) WithProvider(p Provider) *Builder {
	b.provider = p
	b.err = nil
	return b
}

// WithModel sets the model ID.
func (b *Builder) WithModel(modelID string) *Builder {
	b.modelID = modelID
	return b
}

// WithMode sets the agent mode: "plan", "agent", "yolo", or "os".
func (b *Builder) WithMode(mode string) *Builder {
	b.mode = mode
	return b
}

// WithWorkDir sets the working directory.
func (b *Builder) WithWorkDir(dir string) *Builder {
	b.workDir = dir
	return b
}

// WithThinkingLevel sets the thinking/reasoning level.
func (b *Builder) WithThinkingLevel(level ThinkingLevel) *Builder {
	b.thinkingLevel = level
	return b
}

// WithMaxTokens sets the maximum output tokens.
func (b *Builder) WithMaxTokens(n int) *Builder {
	b.maxTokens = n
	return b
}

// WithSystemPromptExtra adds extra context to the system prompt.
func (b *Builder) WithSystemPromptExtra(extra string) *Builder {
	b.systemPromptExtra = extra
	return b
}

// WithMaxIterations sets the safety limit for agent loop iterations.
func (b *Builder) WithMaxIterations(n int) *Builder {
	b.maxIterations = n
	return b
}

// WithToolExecutionMode sets how tool calls are executed: "sequential" or "parallel".
func (b *Builder) WithToolExecutionMode(mode string) *Builder {
	b.toolExecutionMode = mode
	return b
}

// WithMaxToolConcurrency sets the maximum number of local tool calls that may
// execute concurrently in one agent turn. Non-positive values use the default.
func (b *Builder) WithMaxToolConcurrency(n int) *Builder {
	b.maxToolConcurrency = n
	return b
}

// WithTools sets a filter for available tools. Empty means all tools.
func (b *Builder) WithTools(tools []string) *Builder {
	b.tools = tools
	return b
}

// WithSandbox enables or disables sandboxing.
func (b *Builder) WithSandbox(enabled bool) *Builder {
	b.sandboxEnabled = enabled
	return b
}

// WithSessionDir sets the session persistence directory. Empty keeps the
// platform default, which the internal builder resolves at Build time.
func (b *Builder) WithSessionDir(dir string) *Builder {
	b.sessionDir = dir
	return b
}

// WithCompaction configures context compaction.
func (b *Builder) WithCompaction(enabled bool, reserveTokens int) *Builder {
	b.compactionEnabled = enabled
	b.compactionReserve = reserveTokens
	return b
}

// WithMultiAgent enables multi-agent mode.
func (b *Builder) WithMultiAgent(enabled bool) *Builder {
	b.multiAgent = enabled
	return b
}

// WithDelegateMode enables blocking single sub-agent delegation mode.
func (b *Builder) WithDelegateMode(enabled bool) *Builder {
	b.delegateMode = enabled
	return b
}

// WithApprovalHandler sets a custom approval handler for tool calls.
func (b *Builder) WithApprovalHandler(h func(toolCallID, toolName string, args map[string]any) bool) *Builder {
	b.approvalHandler = h
	return b
}

// WithExternalTools registers host-provided custom tools. These are exposed to
// the agent in addition to the built-in tools, unless WithoutBuiltinTools is
// also set, in which case only the external tools are available.
func (b *Builder) WithExternalTools(tools ...ExternalTool) *Builder {
	b.externalTools = append(b.externalTools, tools...)
	return b
}

// WithoutBuiltinTools disables all built-in coding tools (read/write/edit/bash/...).
// Use together with WithExternalTools to run an agent that may only use the
// host-provided tools. This is the recommended mode for embedding the agent as
// a controlled orchestration layer over an application's own tool set.
func (b *Builder) WithoutBuiltinTools() *Builder {
	b.disableBuiltinTools = true
	return b
}

// Build creates and returns an Agent instance.
// Returns an error if required fields are missing.
func (b *Builder) Build() (Agent, error) {
	if b.err != nil {
		return nil, b.err
	}
	if b.provider == nil {
		return nil, fmt.Errorf("agent: provider is required (use WithProvider)")
	}
	if b.workDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("agent: get working directory: %w", err)
		}
		b.workDir = wd
	}
	if b.modelID == "" {
		models := b.provider.Models()
		if len(models) == 0 {
			return nil, fmt.Errorf("agent: no models available from provider %q", b.provider.Name())
		}
		b.modelID = models[0].ID
	}

	// Delegate to internal builder
	if buildInternal == nil {
		return nil, fmt.Errorf("agent: internal builder is not registered; blank-import github.com/oschina/mothx/bootstrap before calling Build")
	}
	return buildInternal(b)
}

// buildInternal is set by internal/agent/init.go to avoid import cycles.
// The internal package calls agent.SetBuilderFunc() at init time.
var buildInternal func(b *Builder) (Agent, error)

// SetBuilderFunc registers the internal builder function.
// Called by internal/agent package at init time.
func SetBuilderFunc(fn func(b *Builder) (Agent, error)) {
	buildInternal = fn
}

// BuilderConfig is the read-only snapshot of Builder state.
// It is used by the internal package to construct the agent without
// exposing Builder fields directly.
type BuilderConfig struct {
	Provider            Provider
	ModelID             string
	Mode                string
	WorkDir             string
	ThinkingLevel       ThinkingLevel
	MaxTokens           int
	MaxTokensUserSet    bool
	SystemPromptExtra   string
	MaxIterations       int
	ToolExecutionMode   string
	MaxToolConcurrency  int
	Tools               []string
	SandboxEnabled      bool
	SessionDir          string
	CompactionEnabled   bool
	CompactionReserve   int
	MultiAgent          bool
	DelegateMode        bool
	ApprovalHandler     func(toolCallID, toolName string, args map[string]any) bool
	ExternalTools       []ExternalTool
	DisableBuiltinTools bool
}

// Config returns a read-only snapshot of the Builder's current configuration.
// Called by the internal builder function to extract settings without
// exporting individual fields.
func (b *Builder) Config() BuilderConfig {
	return BuilderConfig{
		Provider:            b.provider,
		ModelID:             b.modelID,
		Mode:                b.mode,
		WorkDir:             b.workDir,
		ThinkingLevel:       b.thinkingLevel,
		MaxTokens:           b.maxTokens,
		MaxTokensUserSet:    b.maxTokens > 0,
		SystemPromptExtra:   b.systemPromptExtra,
		MaxIterations:       b.maxIterations,
		ToolExecutionMode:   b.toolExecutionMode,
		MaxToolConcurrency:  b.maxToolConcurrency,
		Tools:               b.tools,
		SandboxEnabled:      b.sandboxEnabled,
		SessionDir:          b.sessionDir,
		CompactionEnabled:   b.compactionEnabled,
		CompactionReserve:   b.compactionReserve,
		MultiAgent:          b.multiAgent,
		DelegateMode:        b.delegateMode,
		ApprovalHandler:     b.approvalHandler,
		ExternalTools:       b.externalTools,
		DisableBuiltinTools: b.disableBuiltinTools,
	}
}

// resolveProviderFunc is set by internal/provider to avoid import cycles.
var resolveProviderFunc func(vendor, baseURL, api, apiKey string) (Provider, error)

// SetResolveProviderFunc registers the provider resolution function.
func SetResolveProviderFunc(fn func(vendor, baseURL, api, apiKey string) (Provider, error)) {
	resolveProviderFunc = fn
}

// WithProviderByName creates a provider from vendor/baseURL/api/apiKey configuration.
// This is a convenience method that delegates to the internal provider registry.
func (b *Builder) WithProviderByName(vendor, baseURL, api, apiKey string) *Builder {
	if resolveProviderFunc == nil {
		b.err = fmt.Errorf("agent: provider resolution is not registered; blank-import github.com/oschina/mothx/bootstrap before calling WithProviderByName")
		b.provider = nil
		return b
	}
	p, err := resolveProviderFunc(vendor, baseURL, api, apiKey)
	if err != nil {
		b.err = fmt.Errorf("agent: resolve provider: %w", err)
		b.provider = nil
		return b
	}
	b.err = nil
	b.provider = p
	return b
}
