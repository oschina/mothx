package agentruntime

import (
	"encoding/json"
	"strings"

	"github.com/oschina/mothx/internal/agentruntime/workerdriver"
	"github.com/oschina/mothx/internal/config"
)

// resolveWorkerCapabilities is the one Runtime entry point that turns a
// configured profile, the Agent's initialize advertisement, and the invocation
// policy into the typed capability model. Business code must read the result;
// it must never re-probe the raw AgentCapabilities map or branch on the driver
// identity.
func resolveWorkerCapabilities(profile config.ACPAgentProfile, advertised map[string]any, policy workerCapabilityPolicy) workerdriver.WorkerCapabilities {
	driver := workerdriver.WorkerDriver(profile.EffectiveDriver())
	adapter := workerdriver.Lookup(driver)
	return adapter.Resolve(workerdriver.ResolveInput{
		Profile: workerdriver.Profile{
			ID:                  profile.ID,
			Driver:              driver,
			Remote:              profile.IsRemote(),
			TerminalAccess:      profile.TerminalAccess,
			MCPBridge:           profile.EffectiveMCPBridge(),
			CapabilityOverrides: profile.CapabilityOverrides,
		},
		Advertised: advertised,
		Policy: workerdriver.Policy{
			BridgeAvailable:    policy.BridgeAvailable,
			FSReadAuthorized:   policy.FSReadAuthorized,
			FSWriteAuthorized:  policy.FSWriteAuthorized,
			TerminalAuthorized: policy.TerminalAuthorized,
		},
	})
}

// workerCapabilityPolicy is the Runtime authorization input for one worker
// invocation. It carries no profile fields: authorization and configuration are
// deliberately separate inputs.
type workerCapabilityPolicy struct {
	// BridgeAvailable reports that this process has a live loopback bridge the
	// worker could reach.
	BridgeAvailable bool
	// FSReadAuthorized allows the read-only filesystem bridge.
	FSReadAuthorized bool
	// FSWriteAuthorized allows the scoped write bridge.
	FSWriteAuthorized bool
	// TerminalAuthorized allows the terminal bridge (in addition to the
	// profile's TerminalAccess opt-in).
	TerminalAuthorized bool
}

func workerCapabilityPolicyFromInvocation(invocation ACPWorkerInvocation) workerCapabilityPolicy {
	return workerCapabilityPolicy{
		BridgeAvailable:    invocation.MCPBridgeAvailable,
		FSReadAuthorized:   invocation.ReadTextFile,
		FSWriteAuthorized:  invocation.WriteTextFile,
		TerminalAuthorized: invocation.TerminalAccess,
	}
}

// promptCapabilities projects only the prompt-content subset. It is used where
// only content negotiation is needed (the prompt builder) so bridge or
// filesystem policy cannot accidentally change how an attachment is encoded.
func promptCapabilities(advertised map[string]any) workerdriver.WorkerCapabilities {
	return workerdriver.Lookup(workerdriver.WorkerDriverGenericACP).Resolve(workerdriver.ResolveInput{
		Profile:    workerdriver.Profile{ID: "prompt", Driver: workerdriver.WorkerDriverGenericACP},
		Advertised: advertised,
		Policy: workerdriver.Policy{
			FSReadAuthorized:   true,
			FSWriteAuthorized:  true,
			TerminalAuthorized: true,
		},
	})
}

func workerCapabilitiesJSON(caps workerdriver.WorkerCapabilities) string {
	encoded, err := json.Marshal(caps)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func workerCapabilitiesMetadata(caps workerdriver.WorkerCapabilities) map[string]any {
	var out map[string]any
	if err := json.Unmarshal([]byte(workerCapabilitiesJSON(caps)), &out); err != nil {
		return nil
	}
	return out
}

func workerDegradationsJSON(caps workerdriver.WorkerCapabilities) string {
	encoded, err := json.Marshal(caps.Degradations)
	if err != nil || len(caps.Degradations) == 0 {
		return ""
	}
	return string(encoded)
}

// workerMCPBridgeState is the compact child-metadata projection of the bridge.
func workerMCPBridgeState(caps workerdriver.WorkerCapabilities, profile config.ACPAgentProfile) string {
	switch {
	case profile.EffectiveMCPBridge() == config.ACPMCPBridgeOff:
		return "disabled"
	case profile.IsRemote():
		return "unreachable"
	case caps.MCPHTTP:
		return "available"
	default:
		return "unavailable"
	}
}

// workerCapabilitySummary is the bounded, model-facing capability sentence
// published in the Router management tool description. It must stay short: the
// full capability JSON is diagnostic metadata, never prompt content.
func workerCapabilitySummary(caps workerdriver.WorkerCapabilities) string {
	parts := make([]string, 0, 6)
	if caps.MCPHTTP {
		parts = append(parts, "report/ask")
	}
	if caps.PromptImage || caps.PromptAudio || caps.PromptEmbeddedContext {
		parts = append(parts, "attachments")
	}
	if caps.FSWriteText {
		parts = append(parts, "write")
	}
	if caps.Terminal {
		parts = append(parts, "terminal")
	}
	if caps.SessionResume || caps.SessionLoad {
		parts = append(parts, "resume")
	}
	if len(parts) == 0 {
		return "text-only"
	}
	summary := parts[0]
	for _, part := range parts[1:] {
		summary += ", " + part
	}
	return summary
}

// publishedWorkerCapabilities resolves the model-facing capability baseline for
// one bound profile without starting a worker. It reuses the exact resolver the
// invocation path uses, with the current bridge/policy state, and enriches the
// result with the latest initialize advertisement when this session has already
// negotiated with that profile.
func publishedWorkerCapabilities(rt *SessionRuntime, profile config.ACPAgentProfile) workerdriver.WorkerCapabilities {
	bridge := rt.workerBridge()
	advertised := rt.workerAdvertisedCapabilities(profile.ID)
	return resolveWorkerCapabilities(profile, advertised, workerCapabilityPolicy{
		BridgeAvailable: bridge != nil && bridge.Available(),
		// The summary describes what the worker can do when the task grants it;
		// per-turn write scope and role policy still decide the actual invocation.
		FSReadAuthorized:   true,
		FSWriteAuthorized:  true,
		TerminalAuthorized: true,
	})
}

// publishedWorkerCapabilitySummary renders the model-facing summary and names the
// reason a capability is unavailable. Bridge losses (report/ask) name the
// transport/override cause; non-bridge losses (attachments/resume/write/
// terminal) name whether the Agent never advertised the capability or a
// capabilityOverrides entry removed it, so the model never assumes a capability
// the worker will reject.
func publishedWorkerCapabilitySummary(caps workerdriver.WorkerCapabilities, profile config.ACPAgentProfile, bridgeAvailable bool, advertised map[string]any) string {
	summary := workerCapabilitySummary(caps)
	reasons := publishedWorkerCapabilityLossReasons(caps, profile, advertised)
	switch {
	case profile.EffectiveMCPBridge() == config.ACPMCPBridgeOff:
		reasons = append([]string{"no MCP bridge: profile mcpBridge=off"}, reasons...)
	case profile.IsRemote():
		reasons = append([]string{"no MCP bridge: remote transport cannot reach loopback"}, reasons...)
	case !bridgeAvailable:
		reasons = append([]string{"no MCP bridge: no live listener"}, reasons...)
	case !caps.MCPHTTP:
		bridgeReason := "no MCP bridge: capability degraded"
		for _, degradation := range caps.Degradations {
			if strings.Contains(degradation, "mcpHttp") {
				bridgeReason = "no MCP bridge: " + degradation
				break
			}
		}
		reasons = append([]string{bridgeReason}, reasons...)
	}
	if len(reasons) == 0 {
		return summary
	}
	return summary + "; " + strings.Join(reasons, "; ")
}

// publishedWorkerCapabilityLossReasons explains non-bridge capability losses.
// Only causes the caller can act on are reported: an advertisement that omits a
// capability, or a capabilityOverrides entry that explicitly disabled it. A
// profile that never opted into terminal access is expected configuration, not
// a degradation, so it is not reported here.
func publishedWorkerCapabilityLossReasons(caps workerdriver.WorkerCapabilities, profile config.ACPAgentProfile, advertised map[string]any) []string {
	var reasons []string
	if !caps.PromptImage && !caps.PromptAudio && !caps.PromptEmbeddedContext {
		if overrides := capabilityOverrideDegradations(caps, "promptImage", "promptAudio", "promptEmbeddedContext"); len(overrides) > 0 {
			reasons = append(reasons, "attachments: "+strings.Join(overrides, ", "))
		} else if len(advertised) > 0 && !advertisesPromptMedia(advertised) {
			reasons = append(reasons, "attachments: worker did not advertise image/audio/embeddedContext")
		}
	}
	if !caps.SessionResume && !caps.SessionLoad {
		if overrides := capabilityOverrideDegradations(caps, "sessionResume", "sessionLoad"); len(overrides) > 0 {
			reasons = append(reasons, "resume: "+strings.Join(overrides, ", "))
		} else if len(advertised) > 0 && !advertisesSessionResume(advertised) {
			reasons = append(reasons, "resume: worker did not advertise resume/load")
		}
	}
	if !caps.FSWriteText {
		if overrides := capabilityOverrideDegradations(caps, "fsWriteText"); len(overrides) > 0 {
			reasons = append(reasons, "write: "+strings.Join(overrides, ", "))
		} else {
			reasons = append(reasons, "write: bridge not authorized")
		}
	}
	if !caps.Terminal {
		if overrides := capabilityOverrideDegradations(caps, "terminal"); len(overrides) > 0 {
			reasons = append(reasons, "terminal: "+strings.Join(overrides, ", "))
		}
	}
	return reasons
}

func capabilityOverrideDegradations(caps workerdriver.WorkerCapabilities, names ...string) []string {
	var matches []string
	for _, degradation := range caps.Degradations {
		for _, name := range names {
			if strings.HasPrefix(degradation, name+":") {
				matches = append(matches, degradation)
				break
			}
		}
	}
	return matches
}

func advertisesPromptMedia(advertised map[string]any) bool {
	promptCaps, ok := advertised["promptCapabilities"].(map[string]any)
	if !ok {
		return false
	}
	for _, key := range []string{"image", "audio", "embeddedContext"} {
		if value, ok := promptCaps[key].(bool); ok && value {
			return true
		}
	}
	return false
}

func advertisesSessionResume(advertised map[string]any) bool {
	sessionCaps, _ := advertised["sessionCapabilities"].(map[string]any)
	if sessionCaps != nil {
		for _, key := range []string{"resume", "load"} {
			if _, ok := sessionCaps[key]; ok {
				return true
			}
		}
	}
	if value, ok := advertised["loadSession"].(bool); ok && value {
		return true
	}
	return false
}

// recordWorkerAdvertisedCapabilities keeps the last initialize advertisement for
// one profile so later Router summaries can report attachment/resume support
// that only the Agent can advertise. It is a bounded, in-memory projection.
func (r *SessionRuntime) recordWorkerAdvertisedCapabilities(profileID string, advertised map[string]any) {
	if r == nil {
		return
	}
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return
	}
	cloned := cloneWorkerAdvertised(advertised)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.workerAdvertised == nil {
		r.workerAdvertised = map[string]map[string]any{}
	}
	r.workerAdvertised[profileID] = cloned
}

func (r *SessionRuntime) workerAdvertisedCapabilities(profileID string) map[string]any {
	if r == nil {
		return nil
	}
	profileID = strings.TrimSpace(profileID)
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneWorkerAdvertised(r.workerAdvertised[profileID])
}

func cloneWorkerAdvertised(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		switch typed := value.(type) {
		case map[string]any:
			cloned[key] = cloneWorkerAdvertised(typed)
		case []any:
			cloned[key] = append([]any(nil), typed...)
		default:
			cloned[key] = value
		}
	}
	return cloned
}
