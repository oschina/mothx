package openaiapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/config"
)

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
	if _, ok := sess.Registry.Get("delegate_subagent"); !ok {
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
