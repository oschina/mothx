package provider

const (
	// HostedToolWebSearch is the provider-native web-search capability. It is
	// independent from the local DeepSeek web_search/web_fetch tools.
	HostedToolWebSearch = "web_search"
	// HostedToolOpenAIResponsesWebSearch is an internal name for the native
	// OpenAI Responses capability. Its wire type is still "web_search".
	HostedToolOpenAIResponsesWebSearch   = "openai_responses_web_search"
	HostedToolWebSearchAnthropicMessages = "web_search_20250305"
	HostedToolImageGeneration            = "image_generation"
)

// HostedToolType maps a provider-neutral hosted tool name to its
// provider-specific wire type. The mapping depends on the API family, not the
// vendor name.
func HostedToolType(providerType, name string) string {
	switch name {
	case HostedToolWebSearch:
		switch providerType {
		case "responses", "openai-responses":
			return HostedToolWebSearch
		case "messages", "anthropic-messages":
			return HostedToolWebSearchAnthropicMessages
		}
	case HostedToolOpenAIResponsesWebSearch:
		switch providerType {
		case "responses", "openai-responses":
			return HostedToolWebSearch
		}
	case HostedToolImageGeneration:
		switch providerType {
		case "responses", "openai-responses":
			return HostedToolImageGeneration
		}
	}
	return ""
}

// HostedWebSearchToolType is retained for callers that only need the
// historical web_search mapping.
func HostedWebSearchToolType(providerType, name string) string {
	return HostedToolType(providerType, name)
}
