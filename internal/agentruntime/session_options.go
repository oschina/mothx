package agentruntime

import (
	"fmt"
	"sort"
	"strings"

	"github.com/oschina/mothx/internal/provider"
	providerfactory "github.com/oschina/mothx/internal/provider/factory"
)

// SessionConfigOption is the front-end-neutral representation of a mutable
// session setting. ACP serializes this shape directly as a select option, while
// other adapters can render the same catalog in their native UI.
type SessionConfigOption struct {
	Type         string                      `json:"type"`
	ID           string                      `json:"id"`
	Name         string                      `json:"name"`
	Description  string                      `json:"description,omitempty"`
	Category     string                      `json:"category,omitempty"`
	CurrentValue string                      `json:"currentValue"`
	Options      []SessionConfigOptionChoice `json:"options,omitempty"`
}

// SessionConfigOptionChoice is one value in a mutable session option.
type SessionConfigOptionChoice struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// SessionConfigOption IDs are stable protocol-neutral identifiers.
const (
	ConfigOptionProvider      = "provider"
	ConfigOptionModel         = "model"
	ConfigOptionMode          = "mode"
	ConfigOptionThinkingLevel = "thinking_level"
	ConfigOptionSandbox       = "sandbox"
	ConfigOptionBrowser       = "browser"
	ConfigOptionWebSearch     = "web_search"
	// ConfigOptionExpert selects the Runtime-owned expert bundle for a
	// session. Replacing one non-empty value is deliberately rejected by
	// SetExpert; adapters must create a fork with ForkWithExpert instead.
	ConfigOptionExpert = "expert"
)

// ProviderCatalog is the set of providers available to a session runtime.
// Providers are constructed by the adapter/runtime boundary and selected per
// session; the catalog is never exposed as a configuration API.
type ProviderCatalog map[string]provider.Provider

// SessionModelBinding is the persisted/runtime model identity for one session.
type SessionModelBinding struct {
	ProviderName string
	Model        *provider.Model
}

// SessionConfigOptions builds the standard provider, model, mode, and thinking
// catalogs when no provider catalog is available to the caller.
func SessionConfigOptions(providerName string, models []*provider.Model, model *provider.Model, mode string, thinking provider.ThinkingLevel) []SessionConfigOption {
	return SessionConfigOptionsWithProviders(providerName, nil, models, model, mode, thinking)
}

// SessionConfigOptionsWithProviders builds provider, model, mode, and
// thinking catalogs. The model catalog is intentionally scoped to the current
// provider so selecting a provider cascades immediately to its models.
func SessionConfigOptionsWithProviders(providerName string, providers ProviderCatalog, models []*provider.Model, model *provider.Model, mode string, thinking provider.ThinkingLevel) []SessionConfigOption {
	modelChoices := make([]SessionConfigOptionChoice, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, candidate := range models {
		if candidate == nil || candidate.ID == "" {
			continue
		}
		value := providerfactory.QualifiedModel(providerName, candidate)
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		name := candidate.Name
		if name == "" {
			name = candidate.ID
		}
		modelChoices = append(modelChoices, SessionConfigOptionChoice{Value: value, Name: name})
	}
	sort.SliceStable(modelChoices, func(i, j int) bool { return modelChoices[i].Value < modelChoices[j].Value })
	providerChoices := make([]SessionConfigOptionChoice, 0, len(providers))
	providerNames := make([]string, 0, len(providers))
	for name, candidate := range providers {
		name = strings.TrimSpace(name)
		if name == "" || candidate == nil {
			continue
		}
		providerNames = append(providerNames, name)
	}
	sort.Strings(providerNames)
	for _, name := range providerNames {
		providerChoices = append(providerChoices, SessionConfigOptionChoice{Value: name, Name: providerDisplayName(name)})
	}
	options := []SessionConfigOption{
		{Type: "select", ID: ConfigOptionProvider, Name: "Provider", Category: "provider", CurrentValue: providerName, Options: providerChoices},
		{Type: "select", ID: ConfigOptionModel, Name: "Model", Category: "model", CurrentValue: providerfactory.QualifiedModel(providerName, model), Options: modelChoices},
		{Type: "select", ID: ConfigOptionMode, Name: "Mode", Category: "mode", CurrentValue: mode, Options: []SessionConfigOptionChoice{
			{Value: ModeAgent, Name: "Agent"}, {Value: ModePlan, Name: "Plan"}, {Value: ModeYolo, Name: "Yolo"}, {Value: ModeOS, Name: "OS"},
		}},
	}
	if model != nil && model.Reasoning {
		options = append(options, SessionConfigOption{Type: "select", ID: ConfigOptionThinkingLevel, Name: "Thinking level", Category: "thought_level", CurrentValue: string(thinking), Options: []SessionConfigOptionChoice{
			{Value: string(provider.ThinkingOff), Name: "Off"}, {Value: string(provider.ThinkingMinimal), Name: "Minimal"}, {Value: string(provider.ThinkingLow), Name: "Low"},
			{Value: string(provider.ThinkingMedium), Name: "Medium"}, {Value: string(provider.ThinkingHigh), Name: "High"}, {Value: string(provider.ThinkingXHigh), Name: "XHigh"}, {Value: string(provider.ThinkingMax), Name: "Max"},
		}})
	}
	return options
}

func providerDisplayName(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' || r == '.' })
	for i, part := range parts {
		if part == "" {
			continue
		}
		switch strings.ToLower(part) {
		case "openai":
			parts[i] = "OpenAI"
		case "api":
			parts[i] = "API"
		case "agentplan":
			parts[i] = "AgentPlan"
		default:
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	if len(parts) == 0 {
		return name
	}
	return strings.Join(parts, " ")
}

// ValidateThinkingLevel validates a config option value without making
// provider-specific assumptions.
func ValidateThinkingLevel(value string) (provider.ThinkingLevel, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return provider.ThinkingMedium, nil
	}
	level := provider.ThinkingLevel(value)
	switch level {
	case provider.ThinkingOff, provider.ThinkingMinimal, provider.ThinkingLow, provider.ThinkingMedium, provider.ThinkingHigh, provider.ThinkingXHigh, provider.ThinkingMax:
		return level, nil
	default:
		return "", fmt.Errorf("invalid thinking level %q", value)
	}
}
