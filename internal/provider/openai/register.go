package openai

import (
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
)

func resolveOpenAIModels(cfg *config.ProviderConfig) []*provider.Model {
	if cfg != nil && len(cfg.Models) > 0 {
		models := make([]*provider.Model, 0, len(cfg.Models))
		for _, m := range cfg.Models {
			input := m.Input
			if len(input) == 0 {
				input = []string{"text", "image"}
			}
			var cost provider.ModelPricing
			if m.Cost != nil {
				cost = provider.ModelPricing{
					Input:      m.Cost.Input,
					Output:     m.Cost.Output,
					CacheRead:  m.Cost.CacheRead,
					CacheWrite: m.Cost.CacheWrite,
				}
			}
			models = append(models, &provider.Model{
				ID:            m.ID,
				Name:          m.Name,
				Provider:      "openai",
				Reasoning:     m.Reasoning,
				Input:         input,
				Cost:          cost,
				ContextWindow: m.ContextWindow,
				MaxTokens:     m.MaxTokens,
				MaxTokensSet:  m.MaxTokensWasSet(),
				Temperature:   m.Temperature,
				TopP:          m.TopP,
				Compat:        convertCompat(m.Compat),
			})
		}
		return models
	}
	return DefaultModels()
}

func convertCompat(c *config.ModelCompat) *provider.ModelCompat {
	if c == nil {
		return nil
	}
	return &provider.ModelCompat{
		ThinkingFormat:                      c.ThinkingFormat,
		RequiresReasoningContentOnAssistant: c.RequiresReasoningContentOnAssistant || c.RequiresReasoningContentOnAssistantMessages,
		ForceAdaptiveThinking:               c.ForceAdaptiveThinking,
		ParseReasoningInContent:             c.ParseReasoningInContent,
		SupportsDeveloperRole:               cloneBool(c.SupportsDeveloperRole),
		SupportsStore:                       cloneBool(c.SupportsStore),
		SupportsResponses:                   cloneBool(c.SupportsResponses),
		SupportsPreviousResponseID:          cloneBool(c.SupportsPreviousResponseID),
		SupportsConversation:                cloneBool(c.SupportsConversation),
		SupportsBackground:                  cloneBool(c.SupportsBackground),
		SupportsStructuredOutput:            cloneBool(c.SupportsStructuredOutput),
		SupportsServiceTier:                 cloneBool(c.SupportsServiceTier),
		SupportsParallelToolCalls:           cloneBool(c.SupportsParallelToolCalls),
		SupportsToolChoice:                  cloneBool(c.SupportsToolChoice),
		SupportsHostedTools:                 cloneBoolMap(c.SupportsHostedTools),
		SupportedInclude:                    append([]string(nil), c.SupportedInclude...),
		SupportsReasoningEffort:             cloneBool(c.SupportsReasoningEffort),
		SupportsStrictMode:                  cloneBool(c.SupportsStrictMode),
		MaxTokensField:                      c.MaxTokensField,
		DisableSamplingParams:               cloneBool(c.DisableSamplingParams),
		SupportsCacheControlOnTools:         cloneBool(c.SupportsCacheControlOnTools),
		SupportsLongCacheRetention:          cloneBool(c.SupportsLongCacheRetention),
		SupportsPromptCacheKey:              cloneBool(c.SupportsPromptCacheKey),
		SupportsReasoningSummary:            cloneBool(c.SupportsReasoningSummary),
		SendSessionAffinityHeaders:          c.SendSessionAffinityHeaders,
		SupportsEagerToolInputStreaming:     cloneBool(c.SupportsEagerToolInputStreaming),
	}
}

func cloneBoolMap(src map[string]bool) map[string]bool {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]bool, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func cloneBool(v *bool) *bool {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

// init registers the generic OpenAI-compatible provider factory in the global
// provider registry so that agent.Builder.WithProviderByName (which resolves
// through provider.ResolveProvider) can construct OpenAI-style providers.
func init() {
	factory := func(cfg *config.ProviderConfig) (provider.Provider, error) {
		if cfg == nil {
			return NewProvider("", ""), nil
		}
		p, err := NewProviderWithModelsAndProxy(cfg.APIKey, cfg.BaseURL, cfg.HTTPProxy, resolveOpenAIModels(cfg))
		if err != nil {
			return nil, err
		}
		p.SetMaxImagesPerRequest(cfg.MaxImagesPerRequest)
		if cfg.API == "openai-responses" || cfg.API == "responses" {
			p.SetUseResponsesAPI(true)
			if err := p.SetResponsesConfig(cfg.Responses); err != nil {
				return nil, err
			}
		}
		return p, nil
	}
	provider.Register("openai", factory)
	provider.Register("openai-chat", factory)
	provider.Register("openai-responses", factory)
	provider.Register("responses", factory)
}
