package runtime

import (
	"context"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

// BackgroundRequest is an external message handed to the serve-owned durable
// Responses coordinator. It is independent of a concrete UI or channel.
type BackgroundRequest struct {
	Context   context.Context
	SessionID string
	WorkDir   string
	Platform  string
	UserID    string
	ModelID   string
	Mode      string
	// RunID and Input are Runtime-owned identity/content. Callers must accept
	// any attachment streams through their SessionRuntime before submitting.
	RunID string
	Input agentruntime.RunInput
	// InitialHistory and SystemPrompt carry client-owned chat context when an
	// OpenAI-compatible chat request is handed to the durable coordinator.
	InitialHistory []provider.Message
	SystemPrompt   string
	Temperature    *float64
	TopP           *float64
	MaxTokens      int
	// IdempotencyKey lets an at-least-once caller safely retry submission.
	// The key is persisted in the existing run-event data, so no schema change
	// is required.
	IdempotencyKey string
	// IdempotencyScope aligns external submissions that can use more than one
	// execution driver. Empty preserves the generic "external" scope.
	IdempotencyScope string
	Progress         func(string)
}

// BackgroundSubmitter transfers ownership of a request to a durable
// background coordinator and returns its local run ID.
type BackgroundSubmitter func(BackgroundRequest) (string, error)

// BackgroundRunDriver is the remote lifecycle implemented by a provider
// runtime. Serve coordinates locks, tools, approvals and transcript updates;
// a driver owns only provider-specific start/continue/poll/cancel operations.
type BackgroundRunDriver interface {
	Start(context.Context, string, string, provider.ChatParams) (*session.ResponseRun, error)
	Continue(context.Context, string, string, *session.ResponseRun, []provider.Message, provider.ChatParams) (*session.ResponseRun, error)
	Get(context.Context, string, string) (*session.ResponseRun, error)
	Cancel(context.Context, string, string) error
}
