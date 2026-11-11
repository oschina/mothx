package agentruntime

import (
	"errors"
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

func TestSynchronizeToolGroupsRouterExposesOnlyWorkerMgr(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir(), nil)
	manager := &agent.AgentManager{}
	rt := &SessionRuntime{Mode: ModeRouter, Registry: registry}

	SynchronizeToolGroups(rt, registry, ToolGroupPolicy{}, manager)
	assertToolsPresent(t, registry, []string{"delegate_worker_mgr"}, true)
	assertToolsPresent(t, registry, agent.RouterSubAgentToolNames(), true)
	assertToolsPresent(t, registry, []string{"delegate_subagent", "subagent_spawn", "subagent_answer", "subagent_integrate"}, false)

	// Enabling the ordinary multi-agent capability must not widen Router into
	// send/answer: an external ACP child is one bounded turn with no mailbox.
	SynchronizeToolGroups(rt, registry, ToolGroupPolicy{MultiAgent: true}, manager)
	assertToolsPresent(t, registry, agent.RouterSubAgentToolNames(), true)
	assertToolsPresent(t, registry, []string{"subagent_answer"}, false)

	// Leaving Router removes it again unless the adapter policy asks for it.
	rt.Mode = ModeYolo
	SynchronizeToolGroups(rt, registry, ToolGroupPolicy{}, manager)
	assertToolsPresent(t, registry, []string{"delegate_worker_mgr", "delegate_subagent"}, false)

	// An adapter projecting a locally edited mode (TUI /mode) supplies it through
	// policy.Mode instead of registering tools itself.
	SynchronizeToolGroups(rt, registry, ToolGroupPolicy{Mode: ModeRouter}, manager)
	assertToolsPresent(t, registry, []string{"delegate_worker_mgr"}, true)
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

func TestReconcileManagerBackedToolsCreatesOnlyWhenRequired(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir(), nil)
	rt := &SessionRuntime{Registry: registry}
	created := &agent.AgentManager{}
	builds := 0
	build := func() (*agent.AgentManager, error) {
		builds++
		return created, nil
	}

	// No manager-backed capability: the factory must not run and the groups
	// must be removed.
	manager, err := rt.ReconcileManagerBackedTools(ManagerBackedToolsOptions{Policy: ToolGroupPolicy{}, Build: build})
	if err != nil {
		t.Fatalf("reconcile disabled groups: %v", err)
	}
	if manager != nil || builds != 0 {
		t.Fatalf("disabled groups returned manager=%v builds=%d, want nil/0", manager, builds)
	}
	assertToolsPresent(t, registry, toolGroupNames(), false)

	// Enabling a group creates the manager exactly once.
	policy := ToolGroupPolicy{MultiAgent: true, Delegate: true, Workflows: true}
	manager, err = rt.ReconcileManagerBackedTools(ManagerBackedToolsOptions{Policy: policy, Build: build})
	if err != nil {
		t.Fatalf("reconcile enabled groups: %v", err)
	}
	if manager != created || builds != 1 {
		t.Fatalf("enabled groups returned manager=%v builds=%d, want the built manager and 1", manager, builds)
	}
	assertToolsPresent(t, registry, toolGroupNames(), true)

	// A re-sync reuses the current handle instead of rebuilding it.
	manager, err = rt.ReconcileManagerBackedTools(ManagerBackedToolsOptions{Policy: policy, Manager: manager, Build: build})
	if err != nil {
		t.Fatalf("reconcile reuse: %v", err)
	}
	if manager != created || builds != 1 {
		t.Fatalf("reuse returned manager=%v builds=%d, want the same manager and 1", manager, builds)
	}

	// Rebuild discards the handle and builds a fresh session-scoped manager.
	rebuilt := &agent.AgentManager{}
	builds = 0
	manager, err = rt.ReconcileManagerBackedTools(ManagerBackedToolsOptions{
		Policy: policy, Manager: manager, Rebuild: true,
		Build: func() (*agent.AgentManager, error) {
			builds++
			return rebuilt, nil
		},
	})
	if err != nil {
		t.Fatalf("reconcile rebuild: %v", err)
	}
	if manager != rebuilt || builds != 1 {
		t.Fatalf("rebuild returned manager=%v builds=%d, want the rebuilt manager and 1", manager, builds)
	}

	// Turning every group off drops the manager and removes the tools.
	manager, err = rt.ReconcileManagerBackedTools(ManagerBackedToolsOptions{Policy: ToolGroupPolicy{}, Manager: manager, Build: build})
	if err != nil {
		t.Fatalf("reconcile disable after enable: %v", err)
	}
	if manager != nil {
		t.Fatalf("disabled reconcile kept manager %v", manager)
	}
	assertToolsPresent(t, registry, toolGroupNames(), false)
}

func TestReconcileManagerBackedToolsRouterRequiresManager(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir(), nil)
	rt := &SessionRuntime{Registry: registry, Mode: ModeRouter}
	created := &agent.AgentManager{}
	builds := 0
	manager, err := rt.ReconcileManagerBackedTools(ManagerBackedToolsOptions{
		Policy: ToolGroupPolicy{},
		Build:  func() (*agent.AgentManager, error) { builds++; return created, nil },
	})
	if err != nil {
		t.Fatalf("reconcile router: %v", err)
	}
	if manager != created || builds != 1 {
		t.Fatalf("router reconcile manager=%v builds=%d, want the built manager and 1", manager, builds)
	}
	assertToolsPresent(t, registry, []string{"delegate_worker_mgr"}, true)
	assertToolsPresent(t, registry, agent.RouterSubAgentToolNames(), true)
}

func TestReconcileManagerBackedToolsRequireManager(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir(), nil)
	rt := &SessionRuntime{Registry: registry}
	created := &agent.AgentManager{}
	manager, err := rt.ReconcileManagerBackedTools(ManagerBackedToolsOptions{
		Policy:         ToolGroupPolicy{},
		RequireManager: true,
		Build:          func() (*agent.AgentManager, error) { return created, nil },
	})
	if err != nil {
		t.Fatalf("reconcile required manager: %v", err)
	}
	if manager != created {
		t.Fatalf("required manager = %v, want the built manager", manager)
	}
	// Capabilities are still empty, so the manager-backed surface stays off even
	// though the handle exists for adapter bookkeeping.
	assertToolsPresent(t, registry, toolGroupNames(), false)
}

func TestReconcileManagerBackedToolsPreservesManagerOnBuildError(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir(), nil)
	rt := &SessionRuntime{Registry: registry}
	current := &agent.AgentManager{}
	wantErr := errors.New("provider unavailable")
	manager, err := rt.ReconcileManagerBackedTools(ManagerBackedToolsOptions{
		Policy:  ToolGroupPolicy{MultiAgent: true},
		Manager: current,
		Rebuild: true,
		Build:   func() (*agent.AgentManager, error) { return nil, wantErr },
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("build error = %v, want %v", err, wantErr)
	}
	if manager != current {
		t.Fatalf("build error returned manager %v, want the current handle %v", manager, current)
	}
}

func TestReconcileManagerBackedToolsUsesRuntimeRegistry(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir(), nil)
	rt := &SessionRuntime{Registry: registry}
	manager, err := rt.ReconcileManagerBackedTools(ManagerBackedToolsOptions{
		Policy:  ToolGroupPolicy{Workflows: true},
		Manager: &agent.AgentManager{},
	})
	if err != nil {
		t.Fatalf("reconcile with runtime registry: %v", err)
	}
	if manager == nil {
		t.Fatal("reconcile dropped the supplied manager")
	}
	assertToolsPresent(t, registry, workflow.ToolNames(), true)
}

func TestReconcileManagerBackedToolsRebuildWithoutFactoryFailsClosed(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir(), nil)
	rt := &SessionRuntime{Registry: registry}
	current := &agent.AgentManager{}

	manager, err := rt.ReconcileManagerBackedTools(ManagerBackedToolsOptions{
		Policy:  ToolGroupPolicy{MultiAgent: true},
		Manager: current,
		Rebuild: true,
	})
	if err == nil {
		t.Fatal("rebuild without a factory returned no error")
	}
	if manager != current {
		t.Fatalf("rebuild error returned manager %v, want the current handle %v", manager, current)
	}
}

func TestReconcileManagerBackedToolsNilFactoryResultRemovesGroups(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir(), nil)
	rt := &SessionRuntime{Registry: registry}

	manager, err := rt.ReconcileManagerBackedTools(ManagerBackedToolsOptions{
		Policy: ToolGroupPolicy{MultiAgent: true, Delegate: true, Workflows: true},
		Build:  func() (*agent.AgentManager, error) { return nil, nil },
	})
	if err != nil {
		t.Fatalf("reconcile with nil factory result: %v", err)
	}
	if manager != nil {
		t.Fatalf("nil factory result returned manager %v, want nil", manager)
	}
	assertToolsPresent(t, registry, toolGroupNames(), false)
}

func TestReconcileManagerBackedToolsKeepsManagerWithRunningChild(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir(), nil)
	rt := &SessionRuntime{Registry: registry}
	manager := agent.NewAgentManager(&agent.AgentFactory{})
	if _, err := manager.Create(agent.AgentOptions{ID: "lead"}); err != nil {
		t.Fatalf("create lead: %v", err)
	}
	if _, err := manager.Create(agent.AgentOptions{ID: "child", ParentID: "lead"}); err != nil {
		t.Fatalf("create child: %v", err)
	}
	manager.MarkRunning("child")

	// Capabilities are off, so the tool surface must be empty, but the handle
	// still owns a live child and must not be dropped.
	got, err := rt.ReconcileManagerBackedTools(ManagerBackedToolsOptions{Policy: ToolGroupPolicy{}, Manager: manager})
	if err != nil {
		t.Fatalf("reconcile live child: %v", err)
	}
	if got != manager {
		t.Fatalf("reconcile returned manager %v, want the live handle %v", got, manager)
	}
	assertToolsPresent(t, registry, toolGroupNames(), false)
}

func TestRouterSynchronizationRemovesPriorLocalDelegate(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir(), nil)
	manager := &agent.AgentManager{}
	rt := &SessionRuntime{Mode: ModeRouter, Registry: registry}
	agent.RegisterDelegateSubAgentTool(registry, manager)
	if _, ok := registry.Get("delegate_subagent"); !ok {
		t.Fatal("precondition failed: local delegate was not registered")
	}
	SynchronizeToolGroups(rt, registry, ToolGroupPolicy{Delegate: true}, manager)
	if _, ok := registry.Get("delegate_subagent"); ok {
		t.Fatal("Router synchronization left the local delegate_subagent tool installed")
	}
	if _, ok := registry.Get("delegate_worker_mgr"); !ok {
		t.Fatal("Router synchronization did not install delegate_worker_mgr")
	}
}
