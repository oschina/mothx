package agentruntime

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/tools"
)

// newRouterToolRuntime builds a Runtime bound to a Router worker, so the tool
// projection can be exercised the way Agent construction does it.
func newRouterToolRuntime(t *testing.T, profileID string) (*SessionRuntime, *tools.Registry) {
	t.Helper()
	runtime := newRouterBuildRuntime(t)
	settings := runtime.SettingsSnapshot()
	if profileID != "" {
		settings.ACPAgents = append(settings.ACPAgents, config.ACPAgentProfile{
			ID: profileID, Command: []string{"/opt/" + profileID, "acp"}, Enabled: true,
			PermissionPolicy: config.ACPPermissionAutoAllowOnce,
			Roles:            []config.ACPAgentRole{config.ACPRoleInvestigate},
		})
		runtime.mu.Lock()
		runtime.resourceSettings = settings
		runtime.Mode = ModeRouter
		runtime.mu.Unlock()
		if profileID != "" {
			if err := runtime.SetRouterBinding(RouterBinding{
				ACPProfileID:     profileID,
				ApprovalStrategy: config.ACPPermissionAutoAllowOnce,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	registry := tools.NewRegistry(t.TempDir(), nil)
	manager := agent.NewAgentManager(nil)
	SynchronizeToolGroups(runtime, registry, ToolGroupPolicy{Delegate: true, Mode: ModeRouter}, manager)
	return runtime, registry
}

// The Router target must reach the model. Without this the model guesses a
// plausible name (a user reported it requesting "codex" while bound to
// "opencode") and every delegation fails.
func TestDelegateToolCarriesActivatedRouterWorker(t *testing.T) {
	runtime, registry := newRouterToolRuntime(t, "opencode")
	defer runtime.Close()

	tool, ok := registry.Get("delegate_subagent")
	if !ok {
		t.Fatal("delegate_subagent was not registered")
	}
	delegate, ok := tool.(*agent.DelegateSubAgentTool)
	if !ok {
		t.Fatalf("delegate_subagent has unexpected type %T", tool)
	}
	target, ok := delegate.RouterTarget()
	if !ok {
		t.Fatal("the activated Router worker was not published to the tool")
	}
	if target.ID != "opencode" {
		t.Fatalf("published target = %q, want opencode", target.ID)
	}

	// The description is what the model reads first.
	if !strings.Contains(delegate.Description(), "opencode") {
		t.Fatalf("tool description does not name the worker: %s", delegate.Description())
	}
	// The schema must constrain agent, not merely mention it in prose.
	var schema struct {
		Properties map[string]struct {
			Enum        []string `json:"enum"`
			Const       string   `json:"const"`
			Description string   `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(delegate.Parameters(), &schema); err != nil {
		t.Fatalf("parameters are not valid JSON: %v", err)
	}
	agentField, ok := schema.Properties["agent"]
	if !ok {
		t.Fatal("agent property missing from the schema")
	}
	listed := append([]string(nil), agentField.Enum...)
	if agentField.Const != "" {
		listed = append(listed, agentField.Const)
	}
	found := false
	for _, value := range listed {
		if value == "opencode" {
			found = true
		}
	}
	if !found {
		t.Fatalf("agent schema does not pin the activated worker: enum=%v const=%q desc=%q", agentField.Enum, agentField.Const, agentField.Description)
	}
}

// The guidance must name the rule too, because guidelines are rendered into the
// system prompt where the model reads the contract.
func TestDelegateToolGuidanceNamesRouterRequirement(t *testing.T) {
	_, registry := newRouterToolRuntime(t, "opencode")
	tool, ok := registry.Get("delegate_subagent")
	if !ok {
		t.Fatal("delegate_subagent was not registered")
	}
	joined := strings.Join(tool.PromptGuidelines(), "\n")
	if !strings.Contains(joined, "Router") || !strings.Contains(joined, "agent=") {
		t.Fatalf("guidelines do not state the Router agent requirement: %s", joined)
	}
}

// Leaving Router mode must clear the target, or a stale profile ID would keep
// being advertised in a normal session.
func TestRouterTargetClearedOutsideRouterMode(t *testing.T) {
	runtime, registry := newRouterToolRuntime(t, "opencode")
	defer runtime.Close()

	runtime.mu.Lock()
	runtime.Mode = ModeYolo
	runtime.mu.Unlock()
	manager := agent.NewAgentManager(nil)
	SynchronizeToolGroups(runtime, registry, ToolGroupPolicy{Delegate: true, Mode: ModeYolo}, manager)

	tool, ok := registry.Get("delegate_subagent")
	if !ok {
		t.Fatal("delegate_subagent is missing after leaving Router mode")
	}
	delegate, ok := tool.(*agent.DelegateSubAgentTool)
	if !ok {
		t.Fatalf("delegate_subagent has unexpected type %T", tool)
	}
	if target, ok := delegate.RouterTarget(); ok {
		t.Fatalf("a stale Router target survived leaving Router mode: %#v", target)
	}
}
