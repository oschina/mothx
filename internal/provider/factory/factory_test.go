package factory

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/provider/anthropic"
)

func TestParseQualifiedModel(t *testing.T) {
	providerName, modelID, err := ParseQualifiedModel("openai/gpt-5/coding")
	if err != nil {
		t.Fatalf("ParseQualifiedModel: %v", err)
	}
	if providerName != "openai" || modelID != "gpt-5/coding" {
		t.Fatalf("got %q/%q", providerName, modelID)
	}
	if _, _, err := ParseQualifiedModel("gpt-5"); err == nil {
		t.Fatal("expected provider/model validation error")
	}
}

func TestResolveModelRejectsInvalidAndForeignModels(t *testing.T) {
	p := provider.NewMockProvider("openai", []*provider.Model{{ID: "valid", Provider: "openai"}}, nil)
	if _, err := ResolveModel(p, "openai", "missing"); err == nil {
		t.Fatal("expected invalid model error")
	}
	if _, err := ResolveModel(p, "openai", "anthropic/valid"); err == nil {
		t.Fatal("expected foreign provider error")
	}
	model, err := ResolveModel(p, "openai", "openai/valid")
	if err != nil || model == nil || model.ID != "valid" {
		t.Fatalf("resolve qualified model: %#v, %v", model, err)
	}
}

func TestCreateAppliesExplicitVendorDefaults(t *testing.T) {
	settings := config.DefaultSettings()
	settings.Providers = map[string]*config.ProviderConfig{
		"custom-deepseek": {
			Vendor:  "deepseek",
			BaseURL: "https://example.com/v1",
			APIKey:  "fake-key",
			API:     "openai-chat",
			Models: []config.ModelConfig{
				{ID: "m1", Name: "M1", Reasoning: true},
			},
		},
	}
	settings.DefaultProvider = "custom-deepseek"
	settings.DefaultModel = "m1"

	p, model, err := Create(settings, "", "")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if p.Name() != "openai" {
		t.Fatalf("provider name = %q, want openai", p.Name())
	}
	if model == nil || model.ID != "m1" {
		t.Fatalf("model = %#v, want m1", model)
	}
}

func TestCreateUnknownModelUsesSharedDraftDefaults(t *testing.T) {
	settings := &config.Settings{Providers: map[string]*config.ProviderConfig{
		"custom": {
			APIKey: "fake-key", BaseURL: "https://example.com/v1", API: "openai-chat",
			Models: []config.ModelConfig{{ID: "configured", Name: "Configured"}},
		},
	}}

	_, model, err := Create(settings, "custom", "totally-unknown-model")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if model.ContextWindow != 256000 || !model.Reasoning {
		t.Fatalf("unknown model defaults = %#v", model)
	}
	if len(model.Input) != 1 || model.Input[0] != "text" {
		t.Fatalf("unknown model input = %#v, want [text]", model.Input)
	}
}

func TestConvertModelConfigsPreservesCompat(t *testing.T) {
	supportsReasoningEffort := false
	models := ConvertModelConfigs("test", []config.ModelConfig{
		{
			ID:        "m1",
			Name:      "M1",
			Reasoning: true,
			Compat: &config.ModelCompat{
				ThinkingFormat:          "deepseek",
				SupportsReasoningEffort: &supportsReasoningEffort,
				MaxTokensField:          "max_completion_tokens",
			},
		},
	})
	if len(models) != 1 {
		t.Fatalf("len(models) = %d, want 1", len(models))
	}
	compat := models[0].Compat
	if compat == nil {
		t.Fatal("compat = nil")
	}
	if compat.ThinkingFormat != "deepseek" {
		t.Fatalf("ThinkingFormat = %q, want deepseek", compat.ThinkingFormat)
	}
	if compat.SupportsReasoningEffort == nil || *compat.SupportsReasoningEffort {
		t.Fatalf("SupportsReasoningEffort = %#v, want false", compat.SupportsReasoningEffort)
	}
	if compat.MaxTokensField != "max_completion_tokens" {
		t.Fatalf("MaxTokensField = %q, want max_completion_tokens", compat.MaxTokensField)
	}
}

func TestCreateOpenAIResponsesProvider(t *testing.T) {
	settings := &config.Settings{
		Providers: map[string]*config.ProviderConfig{
			"openai-responses-test": {
				APIKey:  "fake-key",
				BaseURL: "https://api.openai.com/v1",
				API:     "openai-responses",
				Responses: config.ResponsesConfig{
					ReasoningSummary:     "concise",
					PromptCacheKey:       "custom-cache-key",
					PromptCacheRetention: "24h",
				},
				Models: []config.ModelConfig{
					{ID: "gpt-test", Name: "GPT Test"},
				},
			},
		},
	}

	p, model, err := Create(settings, "openai-responses-test", "gpt-test")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if p == nil {
		t.Fatal("provider is nil")
	}
	if model == nil || model.ID != "gpt-test" {
		t.Fatalf("model = %#v, want gpt-test", model)
	}
}

func TestCreateGoogleGeminiProvider(t *testing.T) {
	settings := &config.Settings{
		Providers: map[string]*config.ProviderConfig{
			"gemini-test": {
				APIKey:  "fake-key",
				BaseURL: "https://generativelanguage.googleapis.com/v1beta/models",
				API:     "google-gemini",
				Models: []config.ModelConfig{
					{ID: "gemini-test", Name: "Gemini Test", Reasoning: true},
				},
			},
		},
	}

	p, model, err := Create(settings, "gemini-test", "gemini-test")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if p.Name() != "google-gemini" {
		t.Fatalf("provider name = %q, want google-gemini", p.Name())
	}
	if model == nil || model.ID != "gemini-test" {
		t.Fatalf("model = %#v, want gemini-test", model)
	}
}

func TestCreateGoogleVertexProvider(t *testing.T) {
	settings := &config.Settings{
		Providers: map[string]*config.ProviderConfig{
			"vertex-test": {
				APIKey:  "fake-token",
				BaseURL: "https://aiplatform.googleapis.com/v1/projects/test/locations/global/publishers/google/models",
				API:     "google-vertex",
				Models: []config.ModelConfig{
					{ID: "gemini-test", Name: "Gemini Test", Reasoning: true},
				},
			},
		},
	}

	p, model, err := Create(settings, "vertex-test", "gemini-test")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if p.Name() != "google-vertex" {
		t.Fatalf("provider name = %q, want google-vertex", p.Name())
	}
	if model == nil || model.ID != "gemini-test" {
		t.Fatalf("model = %#v, want gemini-test", model)
	}
}

func TestCreateProviderRejectsInvalidHTTPProxy(t *testing.T) {
	settings := &config.Settings{
		Providers: map[string]*config.ProviderConfig{
			"bad-proxy": {
				APIKey:    "fake-key",
				BaseURL:   "https://api.openai.com/v1",
				API:       "openai-chat",
				HTTPProxy: "http://[::1",
				Models: []config.ModelConfig{
					{ID: "gpt-test", Name: "GPT Test"},
				},
			},
		},
	}

	if _, _, err := Create(settings, "bad-proxy", "gpt-test"); err == nil {
		t.Fatal("expected invalid http proxy error")
	}
}

func TestConvertModelConfigsSupportsReferenceReasoningAlias(t *testing.T) {
	models := ConvertModelConfigs("test", []config.ModelConfig{
		{
			ID:   "m1",
			Name: "M1",
			Compat: &config.ModelCompat{
				RequiresReasoningContentOnAssistantMessages: true,
			},
		},
	})
	compat := models[0].Compat
	if compat == nil || !compat.RequiresReasoningContentOnAssistant {
		t.Fatalf("RequiresReasoningContentOnAssistant = %#v, want true", compat)
	}
}

func TestCreateFallbackToFirstModel(t *testing.T) {
	settings := &config.Settings{
		Providers: map[string]*config.ProviderConfig{
			"custom-provider": {
				APIKey:  "fake-key",
				BaseURL: "https://api.openai.com/v1",
				API:     "openai",
				Models: []config.ModelConfig{
					{ID: "model-one", Name: "Model One"},
					{ID: "model-two", Name: "Model Two"},
				},
			},
		},
		DefaultProvider: "custom-provider",
		DefaultModel:    "model-two",
	}

	// When provider is specified but modelID is "", it should fall back to the first model under the provider (model-one).
	_, model, err := Create(settings, "custom-provider", "")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if model == nil || model.ID != "model-one" {
		t.Fatalf("model = %#v, want model-one", model)
	}

	// When built-in provider is specified but modelID is "", it should fall back to the first model of that built-in provider.
	p2, model2, err := Create(settings, "openai", "")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	available := p2.Models()
	if len(available) == 0 {
		t.Fatal("expected built-in openai to have models")
	}
	if model2 == nil || model2.ID != available[0].ID {
		t.Fatalf("model = %#v, want first model %s", model2, available[0].ID)
	}
}

func TestCreateAppliesMaxContextTokensOverride(t *testing.T) {
	settings := &config.Settings{
		Providers: map[string]*config.ProviderConfig{
			"custom-provider": {
				APIKey:  "fake-key",
				BaseURL: "https://api.openai.com/v1",
				API:     "openai-chat",
				Models: []config.ModelConfig{
					{ID: "model-one", Name: "Model One", ContextWindow: 100000},
				},
			},
		},
		MaxContextTokens: 12345,
	}

	_, model, err := Create(settings, "custom-provider", "model-one")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if model.ContextWindow != 12345 {
		t.Fatalf("ContextWindow = %d, want 12345", model.ContextWindow)
	}
}

func TestCreateIncludesBuiltinOnlyModelsForConfiguredProvider(t *testing.T) {
	settings := &config.Settings{
		Providers: map[string]*config.ProviderConfig{
			"moark": {Models: []config.ModelConfig{{ID: "custom-model", Name: "Custom Model"}}},
		},
	}
	p, _, err := Create(settings, "moark", "kimi-k3")
	if err != nil {
		t.Fatal(err)
	}
	if p.GetModel("kimi-k3") == nil {
		t.Fatal("builtin-only kimi-k3 missing")
	}
	if p.GetModel("kimi-k3").ContextWindow != 1000000 {
		t.Fatalf("builtin kimi-k3 context = %d", p.GetModel("kimi-k3").ContextWindow)
	}
}

func TestCreateWithOptionsAppliesBuiltinAnthropicCacheControl(t *testing.T) {
	enabled := true
	settings := &config.Settings{}
	p, _, err := CreateWithOptions(settings, "anthropic", "claude-opus-4-5", Options{BuiltinAnthropicCacheControl: &enabled})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	ap, ok := p.(*anthropic.Provider)
	if !ok {
		t.Fatalf("provider type = %T, want *anthropic.Provider", p)
	}
	if !ap.IsCacheControlEnabled() {
		t.Fatal("cache control not enabled via BuiltinAnthropicCacheControl")
	}
}

func TestCreateWithOptionsExplicitCacheControlWinsOverOption(t *testing.T) {
	enabled := true
	disabled := false
	settings := &config.Settings{
		Providers: map[string]*config.ProviderConfig{
			"anthropic": {CacheControl: &disabled},
		},
	}
	p, _, err := CreateWithOptions(settings, "anthropic", "claude-opus-4-5", Options{BuiltinAnthropicCacheControl: &enabled})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	ap, ok := p.(*anthropic.Provider)
	if !ok {
		t.Fatalf("provider type = %T, want *anthropic.Provider", p)
	}
	if ap.IsCacheControlEnabled() {
		t.Fatal("explicit cacheControl=false overridden by BuiltinAnthropicCacheControl")
	}
}

func TestCreateLeavesAnthropicCacheControlOffByDefault(t *testing.T) {
	settings := &config.Settings{}
	p, _, err := Create(settings, "anthropic", "claude-opus-4-5")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	ap, ok := p.(*anthropic.Provider)
	if !ok {
		t.Fatalf("provider type = %T, want *anthropic.Provider", p)
	}
	if ap.IsCacheControlEnabled() {
		t.Fatal("cache control enabled without config or option")
	}
}

func TestCreateRejectsUnknownProvider(t *testing.T) {
	settings := &config.Settings{}
	_, _, err := Create(settings, "not-a-provider", "some-model")
	if err == nil {
		t.Fatal("expected unknown provider error")
	}
	if !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("error = %v, want unknown provider", err)
	}
}

func TestCreatePreservesUserSetModelMaxTokensMarker(t *testing.T) {
	settings := config.DefaultSettings()
	if err := json.Unmarshal([]byte(`{
		"providers": {
			"custom-provider": {
				"apiKey": "fake-key",
				"baseUrl": "https://api.openai.com/v1",
				"api": "openai-chat",
				"models": [
					{"id": "model-one", "name": "Model One", "contextWindow": 100000, "maxTokens": 4096}
				]
			}
		}
	}`), settings); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}

	_, model, err := Create(settings, "custom-provider", "model-one")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if model.MaxTokens != 4096 {
		t.Fatalf("MaxTokens = %d, want 4096", model.MaxTokens)
	}
	if !model.MaxTokensSet {
		t.Fatal("MaxTokensSet = false, want true")
	}
}

func TestCreateAppliesMaxContextTokensOverrideForBuiltinProvider(t *testing.T) {
	settings := config.DefaultSettings()
	settings.MaxContextTokens = 54321

	_, model, err := Create(settings, "openai", "gpt-4o")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if model.ContextWindow != 54321 {
		t.Fatalf("ContextWindow = %d, want 54321", model.ContextWindow)
	}
}

func TestCreatePreservesUnknownModelID(t *testing.T) {
	settings := &config.Settings{
		Providers: map[string]*config.ProviderConfig{
			"custom-provider": {
				APIKey:  "fake-key",
				BaseURL: "https://api.openai.com/v1",
				API:     "openai-chat",
				Models: []config.ModelConfig{
					{ID: "model-one", Name: "Model One"},
				},
			},
		},
	}

	_, model, err := Create(settings, "custom-provider", "missing-model")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if model == nil || model.ID != "missing-model" {
		t.Fatalf("model = %#v, want missing-model", model)
	}
}

func TestResolvedModelsMatchesFactoryProviderList(t *testing.T) {
	settings := config.DefaultSettings()
	settings.Providers = map[string]*config.ProviderConfig{
		// A configured provider without explicit models keeps its built-in preset list.
		"anthropic": {APIKey: "fake-key"},
		"custom": {
			BaseURL: "https://example.com/v1",
			APIKey:  "fake-key",
			API:     "openai-chat",
			Models:  []config.ModelConfig{{ID: "m1", Name: "M1"}},
		},
	}

	builtin := ResolvedModels(settings, "anthropic")
	if len(builtin) == 0 {
		t.Fatal("expected built-in anthropic preset models")
	}
	sawPreset := false
	for _, m := range builtin {
		if m.Provider != "anthropic" {
			t.Fatalf("model %q provider = %q, want anthropic", m.ID, m.Provider)
		}
		if m.ID == "claude-sonnet-4-5" {
			sawPreset = true
		}
	}
	if !sawPreset {
		t.Fatal("built-in preset model missing from resolved list")
	}

	// The resolved list is exactly what a factory-created provider exposes (the TUI path).
	p, _, err := Create(settings, "custom", "m1")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	resolved := ResolvedModels(settings, "custom")
	exposed := p.Models()
	if len(resolved) != len(exposed) {
		t.Fatalf("resolved %d models, provider exposes %d", len(resolved), len(exposed))
	}
	for i := range resolved {
		if resolved[i].ID != exposed[i].ID || resolved[i].Provider != exposed[i].Provider {
			t.Fatalf("model %d = %q/%q, provider has %q/%q", i, resolved[i].ID, resolved[i].Provider, exposed[i].ID, exposed[i].Provider)
		}
	}
	if len(resolved[0].Input) != 1 || resolved[0].Input[0] != "text" {
		t.Fatalf("input = %#v, want [text]", resolved[0].Input)
	}

	if got := ResolvedModels(settings, ""); got != nil {
		t.Fatalf("empty provider = %#v, want nil", got)
	}
	if got := ResolvedModels(nil, "anthropic"); got != nil {
		t.Fatalf("nil settings = %#v, want nil", got)
	}
	if got := ResolvedModels(settings, "unknown-no-preset"); len(got) != 0 {
		t.Fatalf("unknown provider = %#v, want empty", got)
	}
}

func TestSortProviderIDsUsesSharedPriority(t *testing.T) {
	ids := []string{"zz-custom", "openai", "moark", "anthropic", "aa-custom"}
	SortProviderIDs(ids)
	want := []string{"moark", "openai", "anthropic", "aa-custom", "zz-custom"}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids = %v, want %v", ids, want)
		}
	}
}
