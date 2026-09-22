package agentruntime

import (
	"testing"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/tools"
	"github.com/oschina/mothx/internal/workflow"
)

func toolGroupNames() []string {
	names := append([]string{}, agent.SubAgentToolNames()...)
	names = append(names, "delegate_subagent")
	names = append(names, workflow.ToolNames()...)
	return names
}

func assertToolsPresent(t *testing.T, registry *tools.Registry, names []string, want bool) {
	t.Helper()
	for _, name := range names {
		_, ok := registry.Get(name)
		if ok != want {
			t.Fatalf("tool %q present = %v, want %v", name, ok, want)
		}
	}
}

func TestSynchronizeToolGroupsLifecycle(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir(), nil)
	manager := &agent.AgentManager{}
	rt := &SessionRuntime{Registry: registry}

	enabled := ToolGroupPolicy{MultiAgent: true, Delegate: true, Workflows: true}
	SynchronizeToolGroups(rt, registry, enabled, manager)
	assertToolsPresent(t, registry, toolGroupNames(), true)

	// Idempotent: re-syncing the same policy keeps the set stable.
	SynchronizeToolGroups(rt, registry, enabled, manager)
	assertToolsPresent(t, registry, toolGroupNames(), true)

	// Disabling every group removes every tool.
	SynchronizeToolGroups(rt, registry, ToolGroupPolicy{}, manager)
	assertToolsPresent(t, registry, toolGroupNames(), false)

	// A nil manager removes every group even when the policy asks for tools.
	SynchronizeToolGroups(rt, registry, enabled, nil)
	assertToolsPresent(t, registry, toolGroupNames(), false)
}

func TestSynchronizeToolGroupsPerToolSelection(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir(), nil)
	manager := &agent.AgentManager{}
	rt := &SessionRuntime{Registry: registry}

	SynchronizeToolGroups(rt, registry, ToolGroupPolicy{
		MultiAgent:    true,
		SubAgentTools: map[string]bool{"subagent_spawn": true, "subagent_wait": true},
		Workflows:     true,
		WorkflowTools: map[string]bool{"workflow_run": true},
	}, manager)
	assertToolsPresent(t, registry, []string{"subagent_spawn", "subagent_wait", "workflow_run"}, true)
	assertToolsPresent(t, registry, []string{"subagent_status", "subagent_send", "subagent_answer", "subagent_destroy", "workflow_lint", "workflow_status", "workflow_cancel"}, false)

	// Re-syncing (for example after a manager swap) keeps the explicit
	// selection instead of resurrecting the full canonical set.
	SynchronizeToolGroups(rt, registry, ToolGroupPolicy{
		MultiAgent:    true,
		SubAgentTools: map[string]bool{"subagent_spawn": true, "subagent_wait": true},
		Workflows:     true,
		WorkflowTools: map[string]bool{"workflow_run": true},
	}, &agent.AgentManager{})
	assertToolsPresent(t, registry, []string{"subagent_status", "subagent_send", "subagent_answer", "subagent_destroy", "workflow_lint"}, false)
}

func TestSynchronizeToolGroupsTeamAuthority(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir(), nil)
	manager := &agent.AgentManager{}
	rt := &SessionRuntime{Registry: registry, Expert: &ExpertBinding{Team: true}}

	// A bound expert team forces the full sub-agent toolset and ignores both
	// the requested capability and per-tool switches; the workflow selection
	// still applies.
	SynchronizeToolGroups(rt, registry, ToolGroupPolicy{
		MultiAgent:    false,
		SubAgentTools: map[string]bool{"subagent_spawn": true},
		Workflows:     true,
		WorkflowTools: map[string]bool{"workflow_run": true},
	}, manager)
	assertToolsPresent(t, registry, agent.SubAgentToolNames(), true)
	assertToolsPresent(t, registry, []string{"workflow_run"}, true)
	assertToolsPresent(t, registry, []string{"workflow_lint", "workflow_status", "workflow_cancel"}, false)
}
