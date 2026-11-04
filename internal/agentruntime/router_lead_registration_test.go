package agentruntime

import (
	"context"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/config"
)

// A Router session installs the delegation surface (subagent_spawn,
// delegate_subagent) even though none of the adapter's multi-agent,
// delegate, or workflow flags is set. The lead agent must therefore be
// registered into the AgentManager: AgentManager.Create resolves the parent
// ID against the registered agents and fails with
// "parent agent <id> not found" otherwise.
//
// The capability decision belongs to the Runtime so every entry point agrees.
func TestManagerBackedToolsEnabledForRouterSession(t *testing.T) {
	workDir, sessionDir := t.TempDir(), t.TempDir()
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false
	settings.SessionDir = sessionDir
	settings.SkillsDir = t.TempDir()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/opt/codex/bin/codex", "acp"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}}

	manager, err := CreateSession(CreateSessionOptions{WorkDir: workDir, SessionDir: sessionDir})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	runtime, err := (Builder{Settings: settings}).Build(context.Background(), BuildOptions{
		ID: manager.GetHeader().ID, Source: SourceTUI, WorkDir: workDir, Manager: manager,
	})
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	t.Cleanup(runtime.Close)

	// Before activation there is no Router mode, so an ordinary session with
	// every capability flag off has no manager-backed surface.
	if runtime.RouterActive() {
		t.Fatal("a fresh session reported Router as active")
	}
	if ManagerBackedToolsEnabled(runtime, false, false, false) {
		t.Fatal("a plain session reported manager-backed tools as enabled")
	}

	if err := runtime.ActivateRouter(context.Background(), sessionDir, settings, RouterBinding{
		ACPProfileID:     "codex",
		ApprovalStrategy: config.ACPPermissionAutoAllowOnce,
	}); err != nil {
		t.Fatalf("activate router: %v", err)
	}
	if !runtime.RouterActive() {
		t.Fatal("the Runtime did not resolve Router after activation")
	}
	// The delegation surface exists in Router mode even though every adapter
	// capability flag is off. Adapters must consult this predicate instead of
	// their own flags, or the lead is never registered.
	if !ManagerBackedToolsEnabled(runtime, false, false, false) {
		t.Fatal("a Router session did not report manager-backed tools as enabled")
	}

	// The manager must resolve a parent that the adapter registered.
	mgr, err := NewAgentManager(AgentManagerOptions{
		Runtime: runtime, Provider: nil, Model: nil, Settings: settings,
	})
	if err == nil && mgr != nil {
		t.Fatal("NewAgentManager accepted a missing provider")
	}
}

// The Router lead must be findable by the manager so the delegation tools can
// resolve their parent. This is the invariant behind the reported
// "parent agent agent-master not found" failure.
func TestRouterLeadIsFindableInManager(t *testing.T) {
	workDir, sessionDir := t.TempDir(), t.TempDir()
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false
	settings.SessionDir = sessionDir
	settings.SkillsDir = t.TempDir()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/opt/codex/bin/codex", "acp"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}}

	manager, err := CreateSession(CreateSessionOptions{WorkDir: workDir, SessionDir: sessionDir})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	runtime, err := (Builder{Settings: settings}).Build(context.Background(), BuildOptions{
		ID: manager.GetHeader().ID, Source: SourceTUI, WorkDir: workDir, Manager: manager,
	})
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	t.Cleanup(runtime.Close)
	if err := runtime.ActivateRouter(context.Background(), sessionDir, settings, RouterBinding{
		ACPProfileID:     "codex",
		ApprovalStrategy: config.ACPPermissionAutoAllowOnce,
	}); err != nil {
		t.Fatalf("activate router: %v", err)
	}

	p, providerName, model := mockBuildModel()
	agentMgr, err := NewAgentManager(AgentManagerOptions{
		Runtime: runtime, Provider: p, ProviderName: providerName, Model: model, Settings: settings,
	})
	if err != nil {
		t.Fatalf("create agent manager: %v", err)
	}

	lead := agent.NewAgentAdapter(agent.New(agent.Config{
		ID:       "agent-master",
		Mode:     ModeRouter,
		Provider: p,
		Model:    model,
		Settings: settings,
		Session:  manager,
	}, runtime.Registry))
	agentMgr.Register(lead)

	if _, ok := agentMgr.Get("agent-master"); !ok {
		t.Fatal("the registered Router lead is not findable in the manager")
	}

	// A child creation that resolves the parent must not fail with
	// "parent agent ... not found".
	if _, err := agentMgr.Create(agent.AgentOptions{
		ParentID:      "agent-master",
		ExternalAgent: "codex",
		WorkDir:       workDir,
	}); err != nil {
		if containsParentNotFound(err.Error()) {
			t.Fatalf("the Router lead was not resolvable as a parent: %v", err)
		}
	}

	// The unresolvable-parent case must still fail closed, so a missing
	// registration is never silently tolerated.
	if _, err := agentMgr.Create(agent.AgentOptions{
		ParentID:      "agent-missing",
		ExternalAgent: "codex",
		WorkDir:       workDir,
	}); err == nil {
		t.Fatal("creating a child under an unknown parent was accepted")
	} else if !containsParentNotFound(err.Error()) {
		t.Fatalf("unexpected error for an unknown parent: %v", err)
	}
}

func containsParentNotFound(message string) bool {
	return strings.Contains(message, "parent agent") && strings.Contains(message, "not found")
}

// A session with no Runtime must not panic.
func TestManagerBackedToolsEnabledNilRuntime(t *testing.T) {
	if ManagerBackedToolsEnabled(nil, false, false, false) {
		t.Fatal("a nil Runtime reported manager-backed tools as enabled")
	}
	if !ManagerBackedToolsEnabled(nil, false, true, false) {
		t.Fatal("an explicitly requested delegate session was not enabled")
	}
}
