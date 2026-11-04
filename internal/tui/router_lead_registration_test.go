package tui

import (
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/agentruntime"
)

// A Router session exposes the delegation surface (subagent_spawn,
// delegate_subagent) without any of the adapter's multi-agent/delegate/workflow
// flags being set. The lead agent must still be registered into the
// AgentManager, because AgentManager.Create resolves the parent ID against the
// registered agents. Without the registration every spawn fails with
// "parent agent agent-master not found".
func TestRouterSessionRegistersLeadAgentForSpawn(t *testing.T) {
	app := newRouterCommandApp(t)
	// The production session-activation path owns the manager and the tool
	// groups. Bind the session the same way the TUI does before activating a
	// worker.
	if err := app.bindRuntimeSession(app.session); err != nil {
		t.Fatalf("bind runtime session: %v", err)
	}

	// The Router surface is only installed once a worker is activated.
	app.handleCommand("/router codex")
	if app.mode != agentruntime.ModeRouter {
		t.Fatalf("mode = %q, want router", app.mode)
	}
	if app.agentMgr == nil {
		t.Fatal("Router session has no AgentManager, so the delegation tools have no parent")
	}
	if _, ok := app.registry.Get("subagent_spawn"); !ok {
		t.Fatal("Router session did not install the subagent_spawn tool")
	}

	// The lead agent the Router surface spawns from must be registered through
	// the TUI's own registration path.
	app.ensureAgent()
	app.registerManagedAgent()
	leadID := app.agent.ID()
	if leadID != "agent-master" {
		t.Fatalf("lead agent ID = %q, want agent-master", leadID)
	}
	if _, ok := app.agentMgr.Get(leadID); !ok {
		t.Fatal("the Router lead agent was not registered into the manager, so every spawn fails with \"parent agent agent-master not found\"")
	}

	// The manager must resolve that parent when a child is created.
	if _, err := app.agentMgr.Create(agent.AgentOptions{
		ParentID:      leadID,
		ExternalAgent: "codex",
		WorkDir:       app.cwd,
	}); err != nil && strings.Contains(err.Error(), "parent agent") {
		t.Fatalf("spawning from the Router lead failed to resolve the parent: %v", err)
	}
}

// The lead must be registered through the adapter's own registration path, not
// only when a caller registers it manually. Router must reach that path even
// though the multi-agent flag is off.
func TestRouterSessionAgentManagementIsEnabled(t *testing.T) {
	app := newRouterCommandApp(t)
	if err := app.bindRuntimeSession(app.session); err != nil {
		t.Fatalf("bind runtime session: %v", err)
	}

	if app.agentManagementEnabled() {
		t.Fatal("a plain session already reported manager-backed tools as enabled")
	}

	app.handleCommand("/router codex")
	if !app.agentManagementEnabled() {
		t.Fatal("a Router session did not enable the manager-backed tool surface, so the lead is never registered")
	}
}

// Leaving Router must restore the ordinary surface: the delegation tools are
// removed and the session stops claiming manager-backed capabilities.
func TestLeavingRouterRestoresOrdinarySurface(t *testing.T) {
	app := newRouterCommandApp(t)
	if err := app.bindRuntimeSession(app.session); err != nil {
		t.Fatalf("bind runtime session: %v", err)
	}
	app.handleCommand("/router codex")
	if !app.agentManagementEnabled() {
		t.Fatal("Router did not enable the manager-backed tool surface")
	}

	app.handleCommand("/mode plan")
	app.syncToolGroups()
	if app.agentManagementEnabled() {
		t.Fatal("leaving Router kept the manager-backed tool surface enabled")
	}
	if _, ok := app.registry.Get("subagent_spawn"); ok {
		t.Fatal("leaving Router kept the subagent_spawn tool installed")
	}
}

// The TUI must resolve the tool group from the Runtime's mode, not from its
// own mode field. A resumed Router session adopts its persisted mode only
// after the Runtime replayed it, so a stale adapter mode would install the
// wrong group.
func TestRouterToolGroupFollowsRuntimeMode(t *testing.T) {
	app := newRouterCommandApp(t)
	if err := app.bindRuntimeSession(app.session); err != nil {
		t.Fatalf("bind runtime session: %v", err)
	}
	app.handleCommand("/router codex")

	// Simulate a stale adapter mode field while the Runtime still resolves
	// Router. The tool group must follow the Runtime.
	app.mode = agentruntime.ModePlan
	app.syncToolGroups()
	if _, ok := app.registry.Get("subagent_spawn"); !ok {
		t.Fatal("the Router tool group was replaced by a stale adapter mode")
	}
}

// The Runtime is the single owner of the Router mode fact. The adapter must
// read it from there rather than from its own field.
func TestRouterActiveFollowsRuntimeMode(t *testing.T) {
	app := newRouterCommandApp(t)
	if app.runtime.RouterActive() {
		t.Fatal("a plain session reported Router as active")
	}
	app.handleCommand("/router codex")
	if !app.runtime.RouterActive() {
		t.Fatal("the Runtime did not resolve Router after activation")
	}
	if app.mode != agentruntime.ModeRouter {
		t.Fatalf("adapter mode = %q, want router", app.mode)
	}
}

// A session with no Runtime must not panic in the capability predicate.
func TestManagerBackedToolsEnabledHandlesNilRuntime(t *testing.T) {
	if agentruntime.ManagerBackedToolsEnabled(nil, false, false, false) {
		t.Fatal("a nil Runtime reported manager-backed tools as enabled")
	}
	if !agentruntime.ManagerBackedToolsEnabled(nil, true, false, false) {
		t.Fatal("an explicitly requested multi-agent session was not enabled")
	}
}
