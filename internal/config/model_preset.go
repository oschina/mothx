package config

import (
	"sort"
	"strings"
)

// DefaultModelContextWindow is the conservative context-window fallback used
// when a model ID is not present in the built-in catalog.
const DefaultModelContextWindow = 256000

// PresetModelConfig returns the best available draft defaults for modelID.
// The current provider wins, followed by an exact model-ID match elsewhere in
// the built-in catalog. Unknown IDs receive safe generic defaults. This helper
// is for creating new model entries; it does not alter existing configuration.
func PresetModelConfig(providerID, modelID string, runtime *Settings) ModelConfig {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return genericModelConfig(modelID)
	}
	if providerID != "" {
		if resolved := ResolveModelConfig(providerID, modelID, runtime); resolved != nil {
			return completeModelPreset(*resolved, modelID)
		}
	}
	if preset := defaultModelConfigByID(modelID); preset != nil {
		return completeModelPreset(*preset, modelID)
	}
	return genericModelConfig(modelID)
}

func defaultModelConfigByID(modelID string) *ModelConfig {
	providerIDs := make([]string, 0, len(defaultProviderConfigs))
	for providerID := range defaultProviderConfigs {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)

	// Preserve exact model-ID semantics first, then tolerate providers that
	// return the same identifier with different casing.
	for _, caseInsensitive := range []bool{false, true} {
		for _, providerID := range providerIDs {
			providerConfig := defaultProviderConfigs[providerID]
			if providerConfig == nil {
				continue
			}
			for i := range providerConfig.Models {
				candidate := providerConfig.Models[i]
				matched := candidate.ID == modelID
				if caseInsensitive {
					matched = strings.EqualFold(candidate.ID, modelID)
				}
				if matched {
					copy := cloneModelConfig(candidate)
					return &copy
				}
			}
		}
	}
	return nil
}

func completeModelPreset(model ModelConfig, requestedID string) ModelConfig {
	model = cloneModelConfig(model)
	model.ID = requestedID
	if strings.TrimSpace(model.Name) == "" {
		model.Name = requestedID
	}
	if model.ContextWindow <= 0 {
		model.ContextWindow = DefaultModelContextWindow
	}
	if len(model.Input) == 0 {
		model.Input = []string{"text"}
	}
	return model
}

func genericModelConfig(modelID string) ModelConfig {
	return ModelConfig{
		ID:            modelID,
		Name:          modelID,
		Reasoning:     true,
		ContextWindow: DefaultModelContextWindow,
		Input:         []string{"text"},
	}
}
