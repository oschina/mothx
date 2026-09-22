package bootstrap

import (
	"context"
	"strings"
	"testing"

	"github.com/oschina/mothx/agent"
	internalprovider "github.com/oschina/mothx/internal/provider"
)

type modelCaptureInternalProvider struct {
	modelID string
}

func (p *modelCaptureInternalProvider) Chat(ctx context.Context, params internalprovider.ChatParams) <-chan internalprovider.StreamEvent {
	p.modelID = params.ModelID
	ch := make(chan internalprovider.StreamEvent, 1)
	close(ch)
	return ch
}

func (p *modelCaptureInternalProvider) Name() string { return "capture" }

func (p *modelCaptureInternalProvider) API() string { return "openai-chat" }

func (p *modelCaptureInternalProvider) Models() []*internalprovider.Model {
	return []*internalprovider.Model{{ID: "fallback"}}
}

func (p *modelCaptureInternalProvider) GetModel(id string) *internalprovider.Model {
	for _, model := range p.Models() {
		if model.ID == id {
			return model
		}
	}
	return nil
}

func TestProviderBridgePreservesModelID(t *testing.T) {
	internal := &modelCaptureInternalProvider{}
	adapter := &providerAdapter{inner: internal}

	for range adapter.Chat(context.Background(), agent.ChatParams{ModelID: "Kimi-K2.5"}) {
	}

	if internal.modelID != "Kimi-K2.5" {
		t.Fatalf("internal ModelID = %q, want Kimi-K2.5", internal.modelID)
	}
}

type retryMetadataInternalProvider struct {
	modelCaptureInternalProvider
}

func (p *retryMetadataInternalProvider) Chat(ctx context.Context, params internalprovider.ChatParams) <-chan internalprovider.StreamEvent {
	p.modelID = params.ModelID
	ch := make(chan internalprovider.StreamEvent, 1)
	ch <- internalprovider.StreamEvent{
		Type:             internalprovider.StreamRetry,
		RetryAttempt:     2,
		RetryMaxAttempts: 4,
		RetryAfterMS:     1250,
	}
	close(ch)
	return ch
}

func TestProviderBridgePreservesRetryMetadata(t *testing.T) {
	internal := &retryMetadataInternalProvider{}
	adapter := &providerAdapter{inner: internal}

	var retry *agent.StreamEvent
	for event := range adapter.Chat(context.Background(), agent.ChatParams{ModelID: "Kimi-K2.5"}) {
		if event.Type == agent.StreamRetry {
			event := event
			retry = &event
		}
	}
	if retry == nil {
		t.Fatal("missing StreamRetry")
	}
	if retry.RetryAttempt != 2 || retry.RetryMaxAttempts != 4 || retry.RetryAfterMS != 1250 {
		t.Fatalf("retry metadata = %#v", retry)
	}
}

func TestProviderBridgeMapsToolCallEvent(t *testing.T) {
	if got := streamEventTypeToPublic(internalprovider.StreamToolCall); got != agent.StreamToolCall {
		t.Fatalf("StreamToolCall maps to %v, want %v", got, agent.StreamToolCall)
	}
	if got := streamEventTypeToPublic(internalprovider.StreamUsage); got != agent.StreamUsage {
		t.Fatalf("StreamUsage maps to %v, want %v", got, agent.StreamUsage)
	}
	if got := streamEventTypeToPublic(internalprovider.StreamHostedItem); got != agent.StreamHostedItem {
		t.Fatalf("StreamHostedItem maps to %v, want %v", got, agent.StreamHostedItem)
	}
}

// TestBuilderWithProviderByNameResolvesByAPIWhenVendorUnregistered proves that
// blank-importing bootstrap is enough for WithProviderByName: the resolution
// hook and the concrete provider factories are both registered by this
// package's init, without importing any internal package from user code.
func TestBuilderWithProviderByNameResolvesByAPIWhenVendorUnregistered(t *testing.T) {
	a, err := agent.NewBuilder().
		WithProviderByName("unregistered", "https://api.openai.com/v1", "openai-chat", "fake-key").
		WithModel("gpt-4o-mini").
		WithWorkDir(t.TempDir()).
		WithSessionDir(t.TempDir()).
		Build()
	if err != nil {
		t.Fatalf("build agent from bootstrap-registered provider resolution: %v", err)
	}
	if a == nil {
		t.Fatal("expected an agent instance")
	}
}

func TestBuilderWithProviderByNameReportsUnknownAPI(t *testing.T) {
	_, err := agent.NewBuilder().
		WithProviderByName("unregistered", "https://example.com/v1", "unknown-api", "fake-key").
		Build()
	if err == nil || !strings.Contains(err.Error(), "unsupported API type") {
		t.Fatalf("error = %v, want unsupported API type", err)
	}
}
