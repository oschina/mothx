package provider

import "testing"

func TestHostedToolTypeImageGeneration(t *testing.T) {
	if got := HostedToolType("openai-responses", HostedToolImageGeneration); got != HostedToolImageGeneration {
		t.Fatalf("HostedToolType(image_generation) = %q, want %q", got, HostedToolImageGeneration)
	}
	if got := HostedToolType("anthropic-messages", HostedToolImageGeneration); got != "" {
		t.Fatalf("HostedToolType(image_generation on messages) = %q, want empty", got)
	}
}

func TestHostedWebSearchToolType(t *testing.T) {
	tests := []struct {
		name         string
		providerType string
		toolName     string
		want         string
	}{
		{name: "responses web search", providerType: "responses", toolName: HostedToolWebSearch, want: HostedToolWebSearch},
		{name: "openai responses web search", providerType: "openai-responses", toolName: HostedToolWebSearch, want: HostedToolWebSearch},
		{name: "messages web search", providerType: "messages", toolName: HostedToolWebSearch, want: HostedToolWebSearchAnthropicMessages},
		{name: "anthropic messages web search", providerType: "anthropic-messages", toolName: HostedToolWebSearch, want: HostedToolWebSearchAnthropicMessages},
		{name: "native OpenAI Responses web search", providerType: "openai-responses", toolName: HostedToolOpenAIResponsesWebSearch, want: HostedToolWebSearch},
		{name: "native OpenAI web search is not an Anthropic tool", providerType: "anthropic-messages", toolName: HostedToolOpenAIResponsesWebSearch, want: ""},
		{name: "unknown tool", providerType: "responses", toolName: "other", want: ""},
		{name: "unknown provider type", providerType: "other", toolName: HostedToolWebSearch, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HostedWebSearchToolType(tt.providerType, tt.toolName); got != tt.want {
				t.Fatalf("HostedWebSearchToolType(%q, %q) = %q, want %q", tt.providerType, tt.toolName, got, tt.want)
			}
		})
	}
}
