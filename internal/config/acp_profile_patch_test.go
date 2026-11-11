package config

import "testing"

func TestPatchACPAgentProfileUpdatesOnlyBehaviorMetadata(t *testing.T) {
	t.Setenv("MOTHX_DIR", t.TempDir())
	initial := DefaultSettings()
	initial.ACPAgents = []ACPAgentProfile{{
		ID: "codex", Title: "Codex", Command: []string{"/opt/codex/bin/codex", "acp"},
		Enabled: true, PermissionPolicy: ACPPermissionAutoAllowOnce,
		TerminalAccess: true, AllowParallelBaseWorkspaceWrites: true,
	}}
	if err := SaveACPAgentProfiles(initial.ACPAgents); err != nil {
		t.Fatal(err)
	}

	driver := "codex-acp"
	model := "  fast-model  "
	bridge := ACPMCPBridgeOff
	overrides := map[string]bool{"promptImage": false, "terminal": false}
	profiles, err := PatchACPAgentProfile("codex", ACPAgentProfilePatch{
		Driver: &driver, Model: &model, MCPBridge: &bridge, CapabilityOverrides: &overrides,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 {
		t.Fatalf("profiles = %#v", profiles)
	}
	patched := profiles[0]
	if patched.EffectiveDriver() != "codex-acp" || patched.EffectiveMCPBridge() != ACPMCPBridgeOff {
		t.Fatalf("patch not applied: %#v", patched)
	}
	if patched.EffectiveModel() != "fast-model" {
		t.Fatalf("model patch not normalized: %q", patched.Model)
	}
	if len(patched.CapabilityOverrides) != 2 || patched.CapabilityOverrides["promptImage"] {
		t.Fatalf("overrides = %#v", patched.CapabilityOverrides)
	}
	// Unrelated trust fields and argv are preserved.
	if len(patched.Command) != 2 || patched.Command[0] != "/opt/codex/bin/codex" ||
		!patched.TerminalAccess || !patched.AllowParallelBaseWorkspaceWrites {
		t.Fatalf("patch changed unrelated fields: %#v", patched)
	}

	reloaded, err := LoadGlobalSettingsOrDefault()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ACPAgents[0].EffectiveDriver() != "codex-acp" || reloaded.ACPAgents[0].EffectiveModel() != "fast-model" {
		t.Fatalf("patch was not persisted: %#v", reloaded.ACPAgents)
	}

	// An empty model patch restores the worker's default behavior.
	emptyModel := "   "
	if _, err := PatchACPAgentProfile("codex", ACPAgentProfilePatch{Model: &emptyModel}); err != nil {
		t.Fatalf("clear model patch: %v", err)
	}
	cleared, err := LoadGlobalSettingsOrDefault()
	if err != nil {
		t.Fatal(err)
	}
	if cleared.ACPAgents[0].EffectiveModel() != "" {
		t.Fatalf("empty model patch did not reset the profile: %#v", cleared.ACPAgents[0])
	}

	// A widening override is rejected and leaves the stored profile untouched.
	widening := map[string]bool{"promptImage": true}
	if _, err := PatchACPAgentProfile("codex", ACPAgentProfilePatch{CapabilityOverrides: &widening}); err == nil {
		t.Fatal("widening capability override was accepted")
	}
	after, err := LoadGlobalSettingsOrDefault()
	if err != nil {
		t.Fatal(err)
	}
	if after.ACPAgents[0].CapabilityOverrides["promptImage"] {
		t.Fatalf("rejected patch changed stored state: %#v", after.ACPAgents)
	}

	// An unknown bridge mode is rejected before any write, and the stored
	// profile keeps its previous value.
	unknownBridge := "sometimes"
	if _, err := PatchACPAgentProfile("codex", ACPAgentProfilePatch{MCPBridge: &unknownBridge}); err == nil {
		t.Fatal("unknown mcpBridge was accepted")
	}
	afterInvalidBridge, err := LoadGlobalSettingsOrDefault()
	if err != nil {
		t.Fatal(err)
	}
	if afterInvalidBridge.ACPAgents[0].EffectiveMCPBridge() != ACPMCPBridgeOff {
		t.Fatalf("rejected patch changed stored bridge mode: %#v", afterInvalidBridge.ACPAgents)
	}

	if _, err := PatchACPAgentProfile("missing", ACPAgentProfilePatch{Driver: &driver}); err == nil {
		t.Fatal("unknown profile patch was accepted")
	}
}
