package agent

import (
	"context"

	"github.com/oschina/mothx/internal/provider"
)

// Provider is the interface that all LLM providers must implement.
type Provider interface {
	// Chat sends a chat request and returns a channel of streaming events.
	Chat(ctx context.Context, params ChatParams) <-chan StreamEvent

	// Name returns the provider's name (e.g. "openai", "anthropic").
	Name() string

	// Models returns the list of available models.
	Models() []*provider.Model

	// GetModel returns a model by ID, or nil if not found.
	GetModel(id string) *provider.Model
}

// ChatParams holds parameters for a chat request.
type ChatParams struct {
	Messages      []provider.Message
	Tools         []provider.ToolDefinition
	SystemPrompt  string
	ThinkingLevel provider.ThinkingLevel
	MaxTokens     int
	ModelID       string
	Abort         chan struct{}
}

// StreamEvent represents an event from the LLM stream.
type StreamEvent struct {
	Type       StreamEventType
	TextDelta  string
	ThinkDelta string
	ToolCall   *provider.ToolCallBlock
	Usage      *provider.Usage
	StopReason string
	Error      error
}

// StreamEventType identifies the type of stream event.
type StreamEventType int

const (
	StreamStart StreamEventType = iota
	StreamTextDelta
	StreamThinkDelta
	StreamToolCall
	StreamUsage
	StreamDone
	StreamError
)

// ThinkingLevel represents the thinking/reasoning level.
type ThinkingLevel string

const (
	ThinkingOff     ThinkingLevel = "off"
	ThinkingMinimal ThinkingLevel = "minimal"
	ThinkingLow     ThinkingLevel = "low"
	ThinkingMedium  ThinkingLevel = "medium"
	ThinkingHigh    ThinkingLevel = "high"
	ThinkingXHigh   ThinkingLevel = "xhigh"
	ThinkingMax     ThinkingLevel = "max"
)

// Model represents a model configuration.
type Model struct {
	ID            string
	Name          string
	Provider      string
	Reasoning     bool
	Input         []string
	Cost          ModelPricing
	ContextWindow int
	MaxTokens     int
}

// ModelPricing represents the pricing for a model.
type ModelPricing struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
}

// BaseProvider provides common functionality for provider implementations.
type BaseProvider struct {
	name   string
	models []*provider.Model
}

// NewBaseProvider creates a new BaseProvider.
func NewBaseProvider(name string, models []*provider.Model) BaseProvider {
	return BaseProvider{name: name, models: models}
}

// Name returns the provider's name.
func (p *BaseProvider) Name() string {
	return p.name
}

// Models returns the list of available models.
func (p *BaseProvider) Models() []*provider.Model {
	return p.models
}

// GetModel returns a model by ID, or nil if not found.
func (p *BaseProvider) GetModel(id string) *provider.Model {
	for _, m := range p.models {
		if m.ID == id {
			return m
		}
	}
	return nil
}
