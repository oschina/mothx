package agent

import (
	"strings"
	"testing"

	"github.com/startvibecoding/mothx/internal/tools"
)

// TestSubAgentToolNamesMatchRegisteredTools pins the canonical sub-agent tool
// list to the registry surface: every adapter that installs or uninstalls these
// tools derives its list from SubAgentToolNames, so a tool added to (or removed
// from) RegisterSubAgentTools without updating the list would silently keep a
// stale entry in one entry point (the N6 class of bug).
func TestSubAgentToolNamesMatchRegisteredTools(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	registry := tools.NewRegistry(t.TempDir(), nil)
	RegisterSubAgentTools(registry, mgr)

	registered := make(map[string]bool)
	for _, tool := range registry.All() {
		if strings.HasPrefix(tool.Name(), "subagent_") {
			registered[tool.Name()] = true
		}
	}
	names := SubAgentToolNames()
	if len(names) == 0 {
		t.Fatal("SubAgentToolNames returned no tools")
	}
	for _, name := range names {
		if !strings.HasPrefix(name, "subagent_") {
			t.Errorf("canonical name %q is not a subagent_* tool", name)
		}
		if !registered[name] {
			t.Errorf("canonical name %q is not registered by RegisterSubAgentTools", name)
		}
	}
	if len(registered) != len(names) {
		t.Fatalf("registered sub-agent tools = %v, canonical list = %v", registered, names)
	}
}

// TestSubAgentToolNamesReturnsAFreshSlice guards the shared definition against
// caller mutation: every adapter loops over the result to unregister tools.
func TestSubAgentToolNamesReturnsAFreshSlice(t *testing.T) {
	names := SubAgentToolNames()
	names[0] = "mutated"
	if got := SubAgentToolNames()[0]; got == "mutated" {
		t.Fatal("SubAgentToolNames shares one mutable slice between callers")
	}
}
