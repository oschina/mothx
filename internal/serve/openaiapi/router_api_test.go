package openaiapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/config"
)

// routerAPITestLead satisfies the manager parent validation for a direct child
// construction test without creating a local Agent.
type routerAPITestLead struct{ id agentpkg.AgentID }

func (a *routerAPITestLead) ID() agentpkg.AgentID       { return a.id }
func (a *routerAPITestLead) ParentID() agentpkg.AgentID { return "" }
func (a *routerAPITestLead) Run(context.Context, string) <-chan agentpkg.Event {
	ch := make(chan agentpkg.Event)
	close(ch)
	return ch
}
func (a *routerAPITestLead) RunWithMessages(ctx context.Context, _ []agentpkg.Message) <-chan agentpkg.Event {
	return a.Run(ctx, "")
}
func (a *routerAPITestLead) Abort()                                  {}
func (a *routerAPITestLead) GetMessages() []agentpkg.Message         { return nil }
func (a *routerAPITestLead) SetMessages([]agentpkg.Message)          {}
func (a *routerAPITestLead) GetContext() *agentpkg.AgentContext      { return nil }
func (a *routerAPITestLead) SetContext(*agentpkg.AgentContext)       {}
func (a *routerAPITestLead) GetContextUsage() *agentpkg.ContextUsage { return nil }
func (a *routerAPITestLead) LoadHistoryMessages([]agentpkg.Message)  {}
func (a *routerAPITestLead) HandleApprovalResponse(string, bool)     {}

// The WebUI projects the shared Runtime catalog for external ACP workers. It
// never reads acpAgents itself, so eligibility and the validation verdict stay
// behind one owner.
func TestListACPWorkerProfilesProjectsRuntimeCatalog(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	srv.settings.ACPAgents = []config.ACPAgentProfile{
		{
			ID: "codex", Title: "Codex", Command: []string{"/opt/codex/bin/codex", "acp"},
			Enabled: true, PermissionPolicy: config.ACPPermissionAutoAllowOnce,
			Roles: []config.ACPAgentRole{config.ACPRoleInvestigate}, MaxConcurrentChildren: 3,
		},
		{
			ID: "helper", Command: []string{"/opt/helper"}, Enabled: false,
			PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		},
		{
			ID: "broken", Command: []string{" "}, Enabled: true,
		},
	}

	profiles := srv.ListACPWorkerProfiles()
	if len(profiles) != 3 {
		t.Fatalf("profiles = %d, want three", len(profiles))
	}
	byID := map[string]ACPWorkerProfileSummary{}
	for _, profile := range profiles {
		byID[profile.ID] = profile
	}
	codex, ok := byID["codex"]
	if !ok {
		t.Fatal("the configured worker is missing from the catalog")
	}
	if !codex.RouterEligible {
		t.Fatalf("an enabled non-interactive worker is not Router-eligible: %#v", codex)
	}
	if codex.MaxConcurrentChildren != 3 {
		t.Fatalf("configured concurrency = %d, want 3", codex.MaxConcurrentChildren)
	}
	if helper, ok := byID["helper"]; !ok || helper.RouterEligible {
		t.Fatalf("a disabled worker is Router-eligible: %#v", helper)
	}
	if broken, ok := byID["broken"]; !ok || broken.InvalidReason == "" {
		t.Fatalf("an invalid worker has no reason: %#v", broken)
	}
}

// The catalog must never leak the executable details or the environment names
// an operator configured.
func TestListACPWorkerProfilesOmitsSecrets(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	srv.settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/opt/codex/bin/codex", "acp"}, Enabled: true,
		Environment: []string{"OPENAI_API_KEY"}, PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}}
	encoded, err := json.Marshal(srv.ListACPWorkerProfiles())
	if err != nil {
		t.Fatalf("encode profiles: %v", err)
	}
	text := string(encoded)
	if strings.Contains(text, "OPENAI_API_KEY") || strings.Contains(text, "/opt/codex") {
		t.Fatalf("the profile projection leaked configuration details: %s", text)
	}
	if strings.Contains(text, `"roles"`) || strings.Contains(text, `"writeRoots"`) {
		t.Fatalf("the profile projection still exposes legacy authorization fields: %s", text)
	}
}

// Router activation must go through the shared Runtime path, so the WebUI
// cannot reach a Router state the TUI or CLI could not.
func TestActivateSessionRouterBindsWorkerThroughRuntime(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	srv.settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/opt/codex/bin/codex", "acp"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		Roles:            []config.ACPAgentRole{config.ACPRoleInvestigate},
	}}

	sessionID := "webui-router"
	if _, err := srv.getOrCreateSession(sessionID, srv.cfg.GetWorkDir()); err != nil {
		t.Fatalf("create session: %v", err)
	}

	state, err := srv.ActivateSessionRouter(sessionID, []string{"codex"})
	if err != nil {
		t.Fatalf("activate router: %v", err)
	}
	if !state.Enabled || state.PrimaryProfile != "codex" {
		t.Fatalf("router state = %#v, want the codex worker bound", state)
	}
	if state.Mode != "router" {
		t.Fatalf("session mode = %q, want router", state.Mode)
	}
	// Activation installs the delegation surface, so the session must own a
	// manager and expose the delegate tool even when its capability flags are
	// all false.
	sess := srv.pool.Get(sessionID)
	if sess == nil {
		t.Fatal("the activated session is not in the pool")
	}
	if sess.AgentMgr == nil {
		t.Fatal("Router activation did not create the session manager")
	}
	if _, ok := sess.Registry.Get("delegate_worker_mgr"); !ok {
		t.Fatal("Router activation did not install delegate_subagent")
	}

	// The binding must survive a fresh read, because the next session attach
	// replays it.
	read, err := srv.GetSessionRouter(sessionID)
	if err != nil {
		t.Fatalf("read router: %v", err)
	}
	if !read.Enabled || len(read.ProfileIDs) != 1 || read.ProfileIDs[0] != "codex" {
		t.Fatalf("router state read back = %#v", read)
	}
}

// An unknown or ineligible worker must be refused by the Runtime, and the
// refusal must not leave a binding behind.
func TestActivateSessionRouterRefusesIneligibleWorker(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	srv.settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "interactive", Command: []string{"/opt/interactive"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionInteractive,
	}}

	sessionID := "webui-router-refused"
	if _, err := srv.getOrCreateSession(sessionID, srv.cfg.GetWorkDir()); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := srv.ActivateSessionRouter(sessionID, []string{"interactive"}); err == nil {
		t.Fatal("an interactive worker was bound to a Router session")
	}
	state, err := srv.GetSessionRouter(sessionID)
	if err != nil {
		t.Fatalf("read router: %v", err)
	}
	if state.Enabled {
		t.Fatalf("a refused activation left a binding: %#v", state)
	}
}

// An empty worker list is a caller error, not an implicit "use the default".
func TestActivateSessionRouterRequiresAWorker(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	sessionID := "webui-router-empty"
	if _, err := srv.getOrCreateSession(sessionID, srv.cfg.GetWorkDir()); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := srv.ActivateSessionRouter(sessionID, nil); err == nil {
		t.Fatal("Router activation accepted an empty worker list")
	}
}

// Leaving Router must return the session to an ordinary mode while keeping the
// binding available for a later re-entry.
func TestDeactivateSessionRouterLeavesRouterMode(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	srv.settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/opt/codex/bin/codex", "acp"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		Roles:            []config.ACPAgentRole{config.ACPRoleInvestigate},
	}}
	sessionID := "webui-router-deactivate"
	if _, err := srv.getOrCreateSession(sessionID, srv.cfg.GetWorkDir()); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := srv.ActivateSessionRouter(sessionID, []string{"codex"}); err != nil {
		t.Fatalf("activate router: %v", err)
	}
	state, err := srv.DeactivateSessionRouter(sessionID)
	if err != nil {
		t.Fatalf("deactivate router: %v", err)
	}
	if state.Mode == "router" {
		t.Fatalf("the session is still in Router mode: %#v", state)
	}
}

// The WebUI profile editor can only narrow behavior metadata; argv and trust
// flags stay untouched, and the response is the same Runtime catalog the list
// endpoint returns.
func TestPatchACPWorkerProfileUpdatesCatalog(t *testing.T) {
	t.Setenv("MOTHX_DIR", t.TempDir())
	if err := config.SaveACPAgentProfiles([]config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/opt/codex/bin/codex", "acp"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}}); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	defer srv.pool.Stop()

	driver := "codex-acp"
	model := "fast-model"
	bridge := config.ACPMCPBridgeOff
	overrides := map[string]bool{"promptImage": false}
	profiles, err := srv.PatchACPWorkerProfile("codex", config.ACPAgentProfilePatch{
		Model: &model, Driver: &driver, MCPBridge: &bridge, CapabilityOverrides: &overrides,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].Model != "fast-model" || profiles[0].Driver != "codex-acp" || profiles[0].MCPBridge != config.ACPMCPBridgeOff {
		t.Fatalf("patched catalog = %#v", profiles)
	}
	if len(profiles[0].CapabilityOverrides) != 1 || profiles[0].CapabilityOverrides[0] != "promptImage" {
		t.Fatalf("overrides = %#v", profiles[0].CapabilityOverrides)
	}
	encoded, _ := json.Marshal(profiles)
	if strings.Contains(string(encoded), "/opt/codex") {
		t.Fatalf("patch response leaked argv: %s", encoded)
	}

	// The patch response is not enough: the live server snapshot must carry the
	// edit so a later catalog read and Router activation use the new model.
	listed := srv.ListACPWorkerProfiles()
	if len(listed) != 1 || listed[0].Model != "fast-model" {
		t.Fatalf("post-patch catalog = %#v, want the edited model", listed)
	}
	srv.mu.RLock()
	live := append([]config.ACPAgentProfile(nil), srv.settings.ACPAgents...)
	srv.mu.RUnlock()
	if len(live) != 1 || live[0].EffectiveModel() != "fast-model" {
		t.Fatalf("live settings profile = %#v, want the edited model", live)
	}

	// Clearing the model must round-trip through the same live snapshot and
	// restore the third-party Agent's default behavior.
	empty := ""
	if _, err := srv.PatchACPWorkerProfile("codex", config.ACPAgentProfilePatch{Model: &empty}); err != nil {
		t.Fatal(err)
	}
	listed = srv.ListACPWorkerProfiles()
	if len(listed) != 1 || listed[0].Model != "" {
		t.Fatalf("cleared catalog = %#v, want an empty model", listed)
	}
	srv.mu.RLock()
	live = append([]config.ACPAgentProfile(nil), srv.settings.ACPAgents...)
	srv.mu.RUnlock()
	if len(live) != 1 || live[0].EffectiveModel() != "" {
		t.Fatalf("live settings after clearing = %#v, want an empty model", live)
	}

	widening := map[string]bool{"promptImage": true}
	if _, err := srv.PatchACPWorkerProfile("codex", config.ACPAgentProfilePatch{CapabilityOverrides: &widening}); err == nil {
		t.Fatal("widening override was accepted")
	}
}

// refreshRouterSessionManagers is the live-settings projection for idle Router
// sessions. It must refresh the manager's captured ACP profile snapshot without
// rebuilding the manager or requiring another activation.
func TestRefreshRouterSessionManagersUpdatesIdleRouterManager(t *testing.T) {
	t.Setenv("MOTHX_DIR", t.TempDir())
	if err := config.SaveACPAgentProfiles([]config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/opt/codex/bin/codex", "acp"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		Roles:            []config.ACPAgentRole{config.ACPRoleInvestigate},
	}}); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	defer srv.pool.Stop()
	loaded, err := config.LoadGlobalSettingsOrDefault()
	if err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	srv.settings.ACPAgents = loaded.ACPAgents
	srv.mu.Unlock()
	sessionID := "webui-router-refresh-direct"
	if _, err := srv.getOrCreateSession(sessionID, srv.cfg.GetWorkDir()); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := srv.ActivateSessionRouter(sessionID, []string{"codex"}); err != nil {
		t.Fatalf("activate router: %v", err)
	}
	sess := srv.pool.Get(sessionID)
	if sess == nil || sess.AgentMgr == nil {
		t.Fatal("activated session has no manager")
	}

	// Publish the edit directly so this test exercises the refresh helper rather
	// than the patch RPC that calls it.
	srv.mu.Lock()
	updated := *srv.settings
	updated.ACPAgents = append([]config.ACPAgentProfile(nil), srv.settings.ACPAgents...)
	updated.ACPAgents[0].Model = "fast-model"
	srv.settings = &updated
	srv.mu.Unlock()
	srv.refreshRouterSessionManagers()

	sess.AgentMgr.Register(&routerAPITestLead{id: "lead"})
	child, err := sess.AgentMgr.Create(agent.AgentOptions{
		ParentID: "lead", WorkDir: sess.WorkDir, ExternalAgent: "codex",
		ExternalTask: "inspect the refreshed model", ExternalRole: "investigate",
		ExternalDeliverable: []string{"findings"},
	})
	if err != nil {
		t.Fatalf("create delegated worker: %v", err)
	}
	status, ok := sess.AgentMgr.Status(child.ID())
	if !ok {
		t.Fatal("delegated worker has no manager status")
	}
	if status.Metadata["model"] != "fast-model" {
		t.Fatalf("delegated worker metadata = %#v, want the refreshed model", status.Metadata)
	}
	if err := sess.AgentMgr.Destroy(child.ID()); err != nil {
		t.Fatalf("destroy delegated worker: %v", err)
	}
}

// A profile edit must reach the manager of a session that was already built
// before the edit and is only activated into Router afterwards. The ACP child
// backend captures its settings snapshot at manager construction, so activation
// has to refresh that snapshot instead of reusing the pre-edit catalog.
func TestPatchACPWorkerProfileRefreshesExistingManagerOnActivation(t *testing.T) {
	t.Setenv("MOTHX_DIR", t.TempDir())
	if err := config.SaveACPAgentProfiles([]config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/opt/codex/bin/codex", "acp"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		Roles:            []config.ACPAgentRole{config.ACPRoleInvestigate},
	}}); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	defer srv.pool.Stop()

	sessionID := "webui-router-refresh"
	sess, err := srv.getOrCreateSession(sessionID, srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	// Build the session manager before the profile edit, mirroring an idle
	// session that already owns a manager for another capability.
	sess.DelegateMode = true
	if err := srv.reconcileSessionAgentManager(sess, false); err != nil {
		t.Fatalf("build pre-edit manager: %v", err)
	}
	if sess.AgentMgr == nil {
		t.Fatal("pre-edit session manager is missing")
	}

	model := "fast-model"
	if _, err := srv.PatchACPWorkerProfile("codex", config.ACPAgentProfilePatch{Model: &model}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.ActivateSessionRouter(sessionID, []string{"codex"}); err != nil {
		t.Fatalf("activate router: %v", err)
	}
	sess = srv.pool.Get(sessionID)
	if sess == nil || sess.AgentMgr == nil {
		t.Fatal("activated session has no manager")
	}
	sess.AgentMgr.Register(&routerAPITestLead{id: "lead"})

	create := func() map[string]any {
		t.Helper()
		child, createErr := sess.AgentMgr.Create(agent.AgentOptions{
			ParentID: "lead", WorkDir: sess.WorkDir, ExternalAgent: "codex",
			ExternalTask: "inspect the refreshed model", ExternalRole: "investigate",
			ExternalDeliverable: []string{"findings"},
		})
		if createErr != nil {
			t.Fatalf("create delegated worker: %v", createErr)
		}
		status, ok := sess.AgentMgr.Status(child.ID())
		if !ok {
			t.Fatal("delegated worker has no manager status")
		}
		if err := sess.AgentMgr.Destroy(child.ID()); err != nil {
			t.Fatalf("destroy delegated worker: %v", err)
		}
		return status.Metadata
	}

	metadata := create()
	if metadata["model"] != "fast-model" {
		t.Fatalf("delegated worker metadata = %#v, want the patched model", metadata)
	}

	// Clearing the model must reach the same live manager and mean "keep the
	// third-party Agent's default" rather than re-selecting an old model.
	empty := ""
	if _, err := srv.PatchACPWorkerProfile("codex", config.ACPAgentProfilePatch{Model: &empty}); err != nil {
		t.Fatal(err)
	}
	metadata = create()
	if model, present := metadata["model"]; present && model != "" {
		t.Fatalf("delegated worker metadata after clearing = %#v, want no model", metadata)
	}
}
