package config

import (
	"fmt"
	"strings"
)

// ACPAgentProfilePatch is the narrow, secret-free profile edit surface exposed
// to management projections. It only covers behavior metadata (model, driver,
// bridge intent, tightened capabilities); argv, URLs, environment names, and
// trust flags stay owned by the operator's settings.json.
type ACPAgentProfilePatch struct {
	// Driver replaces the ACP flavor. An empty value resets to generic-acp.
	Driver *string `json:"driver,omitempty"`
	// Model replaces the external Agent's default model. An empty value resets
	// to the worker's own default behavior.
	Model *string `json:"model,omitempty"`
	// MCPBridge replaces the bridge mode. An empty value resets to auto.
	MCPBridge *string `json:"mcpBridge,omitempty"`
	// CapabilityOverrides replaces the tightening map. An empty map clears it.
	CapabilityOverrides *map[string]bool `json:"capabilityOverrides,omitempty"`
}

// PatchACPAgentProfile applies one management patch to an existing profile and
// persists the complete list through SaveACPAgentProfiles. Validation is the
// same rule set Runtime resolution applies, so a patch can never store a value
// Runtime would reject (including a widening capability override).
func PatchACPAgentProfile(profileID string, patch ACPAgentProfilePatch) ([]ACPAgentProfile, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return nil, fmt.Errorf("ACP agent profile id is required")
	}
	settings, err := LoadGlobalSettingsOrDefault()
	if err != nil {
		return nil, fmt.Errorf("load global settings: %w", err)
	}
	profiles := make([]ACPAgentProfile, 0, len(settings.ACPAgents))
	found := false
	for _, profile := range settings.ACPAgents {
		if strings.TrimSpace(profile.ID) != profileID {
			profiles = append(profiles, profile)
			continue
		}
		if found {
			return nil, fmt.Errorf("duplicate ACP agent profile id %q", profileID)
		}
		found = true
		if patch.Driver != nil {
			profile.Driver = strings.ToLower(strings.TrimSpace(*patch.Driver))
		}
		if patch.Model != nil {
			profile.Model = strings.TrimSpace(*patch.Model)
		}
		if patch.MCPBridge != nil {
			profile.MCPBridge = strings.ToLower(strings.TrimSpace(*patch.MCPBridge))
		}
		if patch.CapabilityOverrides != nil {
			overrides := make(map[string]bool, len(*patch.CapabilityOverrides))
			for name, enabled := range *patch.CapabilityOverrides {
				overrides[strings.TrimSpace(name)] = enabled
			}
			profile.CapabilityOverrides = overrides
		}
		profiles = append(profiles, profile)
	}
	if !found {
		return nil, fmt.Errorf("ACP agent profile %q was not found", profileID)
	}
	if err := SaveACPAgentProfiles(profiles); err != nil {
		return nil, err
	}
	return profiles, nil
}
