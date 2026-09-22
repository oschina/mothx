package factory

import (
	"fmt"
	"sort"
	"strings"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/provider/anthropic"
	"github.com/oschina/mothx/internal/provider/google"
	"github.com/oschina/mothx/internal/provider/openai"
)

// Create creates a provider and model from settings without changing the config schema.
func Create(settings *config.Settings, providerName, modelID string) (provider.Provider, *provider.Model, error) {
	return CreateWithOptions(settings, providerName, modelID, Options{})
}

// Options controls compatibility behavior outside the settings schema.
type Options struct {
	BuiltinAnthropicCacheControl *bool
	// RequireModel makes an explicitly requested model fail when it is not
	// advertised by the provider. Legacy callers retain the historical
	// synthetic-model behavior when this is false.
	RequireModel bool
}

// CreateWithOptions creates a provider and model from settings with runtime-only options.
func CreateWithOptions(settings *config.Settings, providerName, modelID string, opts Options) (provider.Provider, *provider.Model, error) {
	if providerName == "" {
		providerName = settings.DefaultProvider
		if modelID == "" {
			modelID = settings.DefaultModel
		}
	}

	// A provider is usable when it is configured in settings or has a built-in
	// preset; anything else has no base URL or parameters to fall back to.
	if settings.GetProviderConfig(providerName) == nil && config.DefaultProviderConfig(providerName) == nil {
		return nil, nil, fmt.Errorf("unknown provider: %s (add it to settings.json providers section)", providerName)
	}

	pc := config.ResolveProviderConfig(providerName, settings)
	apiKey := settings.ResolveKey(providerName)
	models := ConvertModelConfigs(providerName, pc.Models)
	resolved := provider.ResolveAdapterConfig(pc)
	httpOpts := provider.HTTPClientOptions{
		ProxyURL:    pc.HTTPProxy,
		ForceHTTP11: pc.ForceHTTP11,
	}

	var p provider.Provider
	switch resolved.API {
	case "anthropic-messages":
		ap, err := anthropic.NewProviderWithModelsAndOptions(apiKey, resolved.BaseURL, models, httpOpts)
		if err != nil {
			return nil, nil, err
		}
		if resolved.ThinkingFormat != "" {
			ap.SetThinkingFormat(resolved.ThinkingFormat)
		}
		// An explicit cacheControl config wins; otherwise fall back to the
		// runtime option used by surfaces like ACP to force-enable caching.
		cacheControl := resolved.CacheControl
		if cacheControl == nil {
			cacheControl = opts.BuiltinAnthropicCacheControl
		}
		if cacheControl != nil {
			ap.SetCacheControlEnabled(cacheControl)
		}
		ConfigureRetry(ap, settings)
		p = ap
	case "openai-chat", "openai", "openai-responses", "responses":
		op, err := openai.NewProviderWithModelsAndOptions(apiKey, resolved.BaseURL, models, httpOpts)
		if err != nil {
			return nil, nil, err
		}
		op.SetMaxImagesPerRequest(pc.MaxImagesPerRequest)
		if resolved.ThinkingFormat != "" {
			op.SetThinkingFormat(resolved.ThinkingFormat)
		}
		if resolved.API == "openai-responses" || resolved.API == "responses" {
			op.SetUseResponsesAPI(true)
			if err := op.SetResponsesConfig(pc.Responses); err != nil {
				return nil, nil, fmt.Errorf("invalid Responses API configuration: %w", err)
			}
		}
		ConfigureRetry(op, settings)
		p = op
	case "google-gemini":
		gp, err := google.NewGeminiProviderWithModelsAndOptions(apiKey, resolved.BaseURL, models, httpOpts)
		if err != nil {
			return nil, nil, err
		}
		ConfigureRetry(gp, settings)
		p = gp
	case "google-vertex":
		gp, err := google.NewVertexProviderWithModelsAndOptions(apiKey, resolved.BaseURL, models, httpOpts)
		if err != nil {
			return nil, nil, err
		}
		ConfigureRetry(gp, settings)
		p = gp
	default:
		return nil, nil, fmt.Errorf("unsupported API type: %s (use 'openai-chat', 'openai-responses', 'anthropic-messages', 'google-gemini', or 'google-vertex')", resolved.API)
	}

	ConfigureHeaders(p, settings, providerName)
	var model *provider.Model
	if modelID == "" {
		availableModels := p.Models()
		if len(availableModels) > 0 {
			model = availableModels[0]
		}
	} else {
		model = p.GetModel(modelID)
	}
	if model == nil {
		if modelID == "" {
			return nil, nil, fmt.Errorf("no models available for provider %s", providerName)
		}
		if opts.RequireModel {
			return nil, nil, fmt.Errorf("model %q is not available for provider %s", modelID, providerName)
		}
		preset := config.PresetModelConfig(providerName, modelID, settings)
		return p, applyModelOverrides(&provider.Model{
			ID:            modelID,
			Name:          preset.Name,
			Provider:      providerName,
			Reasoning:     preset.Reasoning,
			Input:         config.CloneStringSlice(preset.Input),
			ContextWindow: preset.ContextWindow,
			MaxTokens:     preset.MaxTokens,
		}, settings), nil
	}
	return p, applyModelOverrides(model, settings), nil
}

// ResolvedModels returns the model list a factory-created provider would
// expose for providerName. It applies the same settings resolution as
// CreateWithOptions (built-in defaults merged with runtime overrides), so
// every surface that lists models — TUI dialogs, the WebUI picker, and HTTP
// endpoints — shares one canonical catalog logic instead of re-parsing raw
// settings.
func ResolvedModels(settings *config.Settings, providerName string) []*provider.Model {
	if settings == nil {
		return nil
	}
	providerName = strings.TrimSpace(providerName)
	if providerName == "" {
		return nil
	}
	pc := config.ResolveProviderConfig(providerName, settings)
	return ConvertModelConfigs(providerName, pc.Models)
}

// ProviderSortPriority ranks well-known providers before custom ones so the
// TUI dialogs and the WebUI provider list share one ordering.
func ProviderSortPriority(id string) int {
	name := strings.ToLower(id)
	switch {
	case strings.Contains(name, "moark"):
		return 10
	case strings.Contains(name, "deepseek"):
		return 20
	case strings.Contains(name, "xiaomi") || strings.Contains(name, "mimo"):
		return 30
	case strings.Contains(name, "doubao") || strings.Contains(name, "volc") || strings.Contains(name, "ark"):
		return 40
	case strings.Contains(name, "openai"):
		return 50
	case strings.Contains(name, "anthropic") || strings.Contains(name, "claude"):
		return 60
	case strings.Contains(name, "google") || strings.Contains(name, "gemini") || strings.Contains(name, "vertex"):
		return 70
	default:
		return 100
	}
}

// SortProviderIDs sorts provider IDs by ProviderSortPriority, then
// alphabetically, in place.
func SortProviderIDs(ids []string) {
	sort.SliceStable(ids, func(i, j int) bool {
		pi, pj := ProviderSortPriority(ids[i]), ProviderSortPriority(ids[j])
		if pi != pj {
			return pi < pj
		}
		return ids[i] < ids[j]
	})
}

// ParseQualifiedModel parses the ACP/HARBOR provider/model spelling. The
// model portion is allowed to contain additional slashes because provider
// model identifiers are opaque to the factory.
func ParseQualifiedModel(raw string) (providerName, modelID string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("requested model is empty")
	}
	providerName, modelID, ok := strings.Cut(raw, "/")
	providerName = strings.TrimSpace(providerName)
	modelID = strings.TrimSpace(modelID)
	if !ok || providerName == "" || modelID == "" {
		return "", "", fmt.Errorf("requested model %q must use provider/model format", raw)
	}
	return providerName, modelID, nil
}

// ResolveModel resolves a session model against an already-created provider.
// A qualified value must refer to the provider owned by that process; model
// switches never replace the provider or its credentials.
func ResolveModel(p provider.Provider, providerName, requested string) (*provider.Model, error) {
	if p == nil {
		return nil, fmt.Errorf("provider is required")
	}
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return nil, fmt.Errorf("model is required")
	}
	modelID := requested
	if strings.Contains(requested, "/") {
		qualifiedProvider, qualifiedModel, err := ParseQualifiedModel(requested)
		if err != nil {
			return nil, err
		}
		if providerName == "" {
			providerName = p.Name()
		}
		if !strings.EqualFold(qualifiedProvider, providerName) && !strings.EqualFold(qualifiedProvider, p.Name()) {
			return nil, fmt.Errorf("model %q belongs to provider %q, current provider is %q", requested, qualifiedProvider, providerName)
		}
		modelID = qualifiedModel
	}
	model := p.GetModel(modelID)
	if model == nil {
		return nil, fmt.Errorf("model %q is not available for provider %q", modelID, providerName)
	}
	return model, nil
}

// QualifiedModel returns the canonical ACP model option value.
func QualifiedModel(providerName string, model *provider.Model) string {
	if model == nil {
		return ""
	}
	if providerName == "" {
		providerName = model.Provider
	}
	if providerName == "" {
		return model.ID
	}
	return providerName + "/" + model.ID
}

func applyModelOverrides(model *provider.Model, settings *config.Settings) *provider.Model {
	if model == nil {
		return nil
	}
	overridden := *model
	overridden.Input = append([]string(nil), model.Input...)
	if model.Compat != nil {
		compat := *model.Compat
		overridden.Compat = &compat
	}
	if settings != nil && settings.MaxContextTokens > 0 {
		overridden.ContextWindow = settings.MaxContextTokens
	}
	return &overridden
}

// modelIDs returns a comma-separated list of model IDs for error messages.
func modelIDs(models []*provider.Model) string {
	ids := make([]string, len(models))
	for i, m := range models {
		ids[i] = m.ID
	}
	return strings.Join(ids, ", ")
}

type retryConfigurable interface {
	SetRetryConfig(cfg *provider.RetryConfig)
}

type headersConfigurable interface {
	SetHeaders(headers map[string]string)
}

// ConfigureRetry sets retry config on a provider if it supports it.
func ConfigureRetry(p provider.Provider, settings *config.Settings) {
	if rc, ok := p.(retryConfigurable); ok {
		rc.SetRetryConfig(&provider.RetryConfig{
			Enabled:     settings.Retry.Enabled,
			MaxRetries:  settings.Retry.MaxRetries,
			BaseDelayMs: settings.Retry.BaseDelayMs,
		})
	}
}

// ConfigureHeaders sets custom provider headers if the provider supports it.
func ConfigureHeaders(p provider.Provider, settings *config.Settings, providerName string) {
	if hc, ok := p.(headersConfigurable); ok {
		hc.SetHeaders(settings.ResolveProviderHeaders(providerName))
	}
}

// ConvertModelConfigs converts config.ModelConfig to provider.Model.
func ConvertModelConfigs(providerName string, models []config.ModelConfig) []*provider.Model {
	result := make([]*provider.Model, 0, len(models))
	for _, m := range models {
		input := m.Input
		if len(input) == 0 {
			input = []string{"text"}
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
		result = append(result, &provider.Model{
			ID:            m.ID,
			Name:          m.Name,
			Provider:      providerName,
			Reasoning:     m.Reasoning,
			Input:         input,
			Cost:          cost,
			ContextWindow: m.ContextWindow,
			MaxTokens:     m.MaxTokens,
			MaxTokensSet:  m.MaxTokensWasSet(),
			Temperature:   config.NormalizeSamplingPtr(m.Temperature),
			TopP:          config.NormalizeSamplingPtr(m.TopP),
			Compat:        convertCompat(m.Compat),
		})
	}
	return result
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
		SupportsDeveloperRole:               cloneBoolPtr(c.SupportsDeveloperRole),
		SupportsStore:                       cloneBoolPtr(c.SupportsStore),
		SupportsResponses:                   cloneBoolPtr(c.SupportsResponses),
		SupportsPreviousResponseID:          cloneBoolPtr(c.SupportsPreviousResponseID),
		SupportsConversation:                cloneBoolPtr(c.SupportsConversation),
		SupportsBackground:                  cloneBoolPtr(c.SupportsBackground),
		SupportsStructuredOutput:            cloneBoolPtr(c.SupportsStructuredOutput),
		SupportsServiceTier:                 cloneBoolPtr(c.SupportsServiceTier),
		SupportsParallelToolCalls:           cloneBoolPtr(c.SupportsParallelToolCalls),
		SupportsToolChoice:                  cloneBoolPtr(c.SupportsToolChoice),
		SupportsHostedTools:                 cloneBoolMap(c.SupportsHostedTools),
		SupportedInclude:                    append([]string(nil), c.SupportedInclude...),
		SupportsReasoningEffort:             cloneBoolPtr(c.SupportsReasoningEffort),
		SupportsStrictMode:                  cloneBoolPtr(c.SupportsStrictMode),
		MaxTokensField:                      c.MaxTokensField,
		DisableSamplingParams:               cloneBoolPtr(c.DisableSamplingParams),
		SupportsCacheControlOnTools:         cloneBoolPtr(c.SupportsCacheControlOnTools),
		SupportsLongCacheRetention:          cloneBoolPtr(c.SupportsLongCacheRetention),
		SupportsPromptCacheKey:              cloneBoolPtr(c.SupportsPromptCacheKey),
		SupportsReasoningSummary:            cloneBoolPtr(c.SupportsReasoningSummary),
		SendSessionAffinityHeaders:          c.SendSessionAffinityHeaders,
		SupportsEagerToolInputStreaming:     cloneBoolPtr(c.SupportsEagerToolInputStreaming),
	}
}

func cloneBoolPtr(v *bool) *bool {
	if v == nil {
		return nil
	}
	copied := *v
	return &copied
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
