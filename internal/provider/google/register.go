package google

import (
	"os"
	"os/exec"
	"strings"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/platform"
	"github.com/oschina/mothx/internal/provider"
)

func init() {
	provider.Register("google-gemini", func(cfg *config.ProviderConfig) (provider.Provider, error) {
		if cfg == nil {
			return NewGeminiProvider("", ""), nil
		}
		return NewGeminiProviderWithModelsAndProxy(resolveAPIKey(cfg), cfg.BaseURL, cfg.HTTPProxy, convertModels("google-gemini", cfg.Models))
	})
	provider.Register("google-vertex", func(cfg *config.ProviderConfig) (provider.Provider, error) {
		if cfg == nil {
			return NewVertexProvider("", ""), nil
		}
		return NewVertexProviderWithModelsAndProxy(resolveAPIKey(cfg), cfg.BaseURL, cfg.HTTPProxy, convertModels("google-vertex", cfg.Models))
	})
}

func resolveAPIKey(cfg *config.ProviderConfig) string {
	if cfg == nil {
		return ""
	}
	key := cfg.APIKey
	if strings.HasPrefix(key, "!") {
		if os.Getenv("VIBECODING_ALLOW_SHELL_CONFIG") != "1" {
			return key
		}
		return resolveProviderShellCommand(key[1:])
	}
	if strings.HasPrefix(key, "${") && strings.HasSuffix(key, "}") {
		return os.Getenv(key[2 : len(key)-1])
	}
	return key
}

func resolveProviderShellCommand(cmd string) string {
	if cmd == "" {
		return ""
	}
	var out []byte
	var err error
	if platform.IsWindows() {
		out, err = exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", cmd).Output()
	} else {
		out, err = exec.Command("sh", "-c", cmd).Output()
	}
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func convertModels(providerName string, models []config.ModelConfig) []*provider.Model {
	if len(models) == 0 {
		return DefaultModels(providerName)
	}
	result := make([]*provider.Model, 0, len(models))
	for _, m := range models {
		input := m.Input
		if len(input) == 0 {
			input = []string{"text", "image"}
		}
		result = append(result, &provider.Model{
			ID:            m.ID,
			Name:          m.Name,
			Provider:      providerName,
			Reasoning:     m.Reasoning,
			Input:         input,
			ContextWindow: m.ContextWindow,
			MaxTokens:     m.MaxTokens,
			MaxTokensSet:  m.MaxTokensWasSet(),
			Temperature:   m.Temperature,
			TopP:          m.TopP,
			Compat:        toCompat(m.Compat),
		})
	}
	return result
}

func toCompat(c *config.ModelCompat) *provider.ModelCompat {
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

func cloneBool(v *bool) *bool {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}
