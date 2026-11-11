// Package workerdriver resolves the typed capability surface of one external
// ACP worker. It is deliberately a leaf package: it knows driver identities,
// capability names, and the advertised ACP capability shape, but it never reads
// settings, launches a process, or owns a session. Runtime (agentruntime) builds
// the input from the configured profile, the initialize result, and its own
// policy, then persists the resolved result.
package workerdriver

import "sort"

// WorkerDriver identifies one ACP flavor. A driver is behavior only: it may
// describe verified protocol quirks, but it must never grant authorization,
// sandbox policy, concurrency, or a capability the Agent did not advertise.
type WorkerDriver string

// Built-in driver identities. Only generic-acp is required to exist; the others
// are added when real interop exposes a protocol gap that generic ACP cannot
// describe.
const (
	WorkerDriverGenericACP WorkerDriver = "generic-acp"
	WorkerDriverCodexACP   WorkerDriver = "codex-acp"
	WorkerDriverClaudeCode WorkerDriver = "claude-code-acp"
	WorkerDriverOpenCode   WorkerDriver = "opencode-acp"
	WorkerDriverCopilot    WorkerDriver = "copilot-acp"
)

// WorkerCapabilities is the resolved capability set of one ACP worker. It is
// the only capability model business code may read; adapters and callers must
// not branch on the driver identity.
type WorkerCapabilities struct {
	SessionResume         bool `json:"sessionResume"`
	SessionLoad           bool `json:"sessionLoad"`
	SessionClose          bool `json:"sessionClose"`
	SessionFork           bool `json:"sessionFork"`
	AdditionalDirectories bool `json:"additionalDirectories"`

	PromptImage           bool `json:"promptImage"`
	PromptAudio           bool `json:"promptAudio"`
	PromptEmbeddedContext bool `json:"promptEmbeddedContext"`

	MCPHTTP  bool `json:"mcpHttp"`
	MCPSSE   bool `json:"mcpSse"`
	MCPStdio bool `json:"mcpStdio"`

	FSReadText  bool `json:"fsReadText"`
	FSWriteText bool `json:"fsWriteText"`
	Terminal    bool `json:"terminal"`

	ModelConfigOption bool `json:"modelConfigOption"`
	ThoughtStream     bool `json:"thoughtStream"`
	NativeSubagents   bool `json:"nativeSubagents"`
	// ActiveSteering is reserved for a future ACP driver. ACP has no steering
	// primitive today, so it stays false.
	ActiveSteering bool `json:"activeSteering"`

	// TerminalStatusQuality is "strong", "weak", or "none". It describes how
	// much the Agent's terminal status can be trusted, not whether it can run
	// commands.
	TerminalStatusQuality string `json:"terminalStatusQuality"`
	// Source records how the result was produced: "negotiated" (initialize
	// advertised the capability set), "profile" (a driver baseline supplied it),
	// or "degraded" (policy tightened the result).
	Source string `json:"source"`
	// Degradations names every capability the policy or transport removed, in a
	// stable order, so child metadata and diagnostics can explain a downgrade.
	Degradations []string `json:"degradations,omitempty"`
}

// MCPBridge reports whether the Runtime should inject the loopback HTTP MCP
// bridge for this worker. SSE/stdio are diagnostics only; this release only
// implements loopback HTTP.
func (c WorkerCapabilities) MCPBridge() bool { return c.MCPHTTP }

// Profile is the primitive, settings-free projection of an ACP worker profile
// that the resolver needs. Runtime owns the conversion so this package never
// imports internal/config.
type Profile struct {
	// ID is used only in degradation messages, never for behavior.
	ID string
	// Driver is the configured driver identity. Empty means generic-acp.
	Driver WorkerDriver
	// Remote reports a transport that cannot reach host loopback.
	Remote bool
	// TerminalAccess is the profile-level executable trust opt-in.
	TerminalAccess bool
	// MCPBridge is "auto" (default) or "off".
	MCPBridge string
	// CapabilityOverrides can only tighten: a true value is rejected by
	// configuration validation, so the resolver only applies false values.
	CapabilityOverrides map[string]bool
}

// Policy is the Runtime's authorization decision for one worker invocation. It
// is the final arbiter: a capability enabled by profile or advertisement still
// requires the matching policy grant.
type Policy struct {
	// BridgeAvailable reports that the process has a live loopback bridge
	// listener this worker could reach.
	BridgeAvailable bool
	// FSReadAuthorized allows the read-only filesystem bridge.
	FSReadAuthorized bool
	// FSWriteAuthorized allows the scoped write bridge (write role plus a
	// non-empty write scope, already resolved by Runtime).
	FSWriteAuthorized bool
	// TerminalAuthorized allows the terminal bridge. The profile's
	// TerminalAccess is a separate, additional requirement.
	TerminalAuthorized bool
}

// ResolveInput is the complete three-way intersection input.
type ResolveInput struct {
	Profile    Profile
	Advertised map[string]any
	Policy     Policy
}

// Adapter resolves one ACP driver. Adapters are looked up by identity through
// Lookup; business code never branches on the driver string.
type Adapter interface {
	Driver() WorkerDriver
	Baseline() WorkerCapabilities
	Resolve(in ResolveInput) WorkerCapabilities
}

// NormalizeBounds returns the resolved capabilities as a canonical, stable copy.
func (c WorkerCapabilities) NormalizeBounds() WorkerCapabilities {
	out := c
	out.Degradations = append([]string(nil), c.Degradations...)
	sort.Strings(out.Degradations)
	return out
}

// capabilityOverrideNames is the closed set of capability keys a settings
// profile may tighten. Keeping the set explicit prevents a typo from becoming a
// silently ignored policy line.
var capabilityOverrideNames = map[string]bool{
	"sessionResume": true, "sessionLoad": true, "sessionClose": true, "sessionFork": true,
	"additionalDirectories": true, "promptImage": true, "promptAudio": true,
	"promptEmbeddedContext": true, "mcpHttp": true, "mcpSse": true, "mcpStdio": true,
	"fsReadText": true, "fsWriteText": true, "terminal": true,
	"modelConfigOption": true, "thoughtStream": true, "nativeSubagents": true,
}

// KnownCapabilityOverride reports whether key names a supported capability.
func KnownCapabilityOverride(key string) bool { return capabilityOverrideNames[key] }

// CapabilityOverrideNames returns the sorted, closed set of capability keys a
// settings profile may tighten. Management projections render exactly this list
// so a UI cannot offer a key the resolver would ignore.
func CapabilityOverrideNames() []string {
	names := make([]string, 0, len(capabilityOverrideNames))
	for name := range capabilityOverrideNames {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
