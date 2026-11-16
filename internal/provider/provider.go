package provider

import (
	"context"
	"strings"
)

// Provider is the interface that all LLM providers must implement.
type Provider interface {
	// Chat sends a chat request and returns a channel of streaming events.
	Chat(ctx context.Context, params ChatParams) <-chan StreamEvent

	// Name returns the provider's name (e.g. "openai", "anthropic").
	Name() string

	// API returns the protocol/API type (e.g. "openai-chat", "anthropic-messages").
	API() string

	// Models returns the list of available models.
	Models() []*Model

	// GetModel returns a model by ID, or nil if not found.
	GetModel(id string) *Model
}

// NativeTitleProvider is implemented by providers whose endpoint can generate a
// short conversation title inside a normal request. Callers enable it through
// ChatParams.TitleEnabled and read the result from StreamEvent.Title.
type NativeTitleProvider interface {
	SupportsNativeTitle() bool
}

// SupportsNativeTitleBaseURL reports whether a base URL targets an endpoint
// that implements the in-band conversation-title extension. Only DeepSeek's
// Messages, Responses, and Chat Completions endpoints document it today.
func SupportsNativeTitleBaseURL(baseURL string) bool {
	return strings.Contains(strings.ToLower(baseURL), "api.deepseek.com")
}
