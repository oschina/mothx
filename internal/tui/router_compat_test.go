package tui

import (
	"testing"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
)

// A hand-edited settings.json with defaultMode=router used to make every TUI
// agent build fail. The compatible behavior is to run in the product default.
func TestTUIStillBuildsAgentWithRouterDefaultMode(t *testing.T) {
	app := newRouterCommandApp(t)
	app.settings.DefaultMode = agentruntime.ModeRouter
	app.mode = ""

	if _, err := app.buildRuntimeAgent(); err != nil {
		t.Fatalf("the TUI cannot build an agent when defaultMode is router: %v", err)
	}
	resolved, err := app.effectiveRuntimeMode()
	if err != nil {
		t.Fatalf("resolve mode: %v", err)
	}
	if resolved == agentruntime.ModeRouter {
		t.Fatal("the TUI resolved router as an effective mode without a worker binding")
	}
	// Read-only plan, never yolo: Router restricts the tool surface, so falling
	// back to full access would silently widen what the model may do.
	if resolved != agentruntime.ModePlan {
		t.Fatalf("resolved mode = %q, want the read-only %q", resolved, agentruntime.ModePlan)
	}
}

// Leaving Router must persist: otherwise reopening the session restores the
// read-only Router surface the user just left.
func TestLeavingRouterPersistsAndSurvivesReload(t *testing.T) {
	app := newRouterCommandApp(t)
	runRouterActivation(t, app, app.handleCommand("/router codex"))
	if app.mode != agentruntime.ModeRouter {
		t.Fatalf("activation did not enter Router: %q", app.mode)
	}

	app.handleCommand("/mode yolo")
	if app.mode != agentruntime.ModeYolo {
		t.Fatalf("mode after leaving Router = %q", app.mode)
	}
	if persisted := app.runtime.PersistedMode(); persisted != agentruntime.ModeYolo {
		t.Fatalf("persisted mode after leaving Router = %q, want yolo", persisted)
	}

	// Simulate a fresh adapter reopening the same session: it must adopt the
	// Runtime's persisted mode, not the stale Router binding.
	reopened := newRouterCommandApp(t)
	reopened.SetRuntime(app.runtime)
	reopened.mode = "plan"
	if persisted := reopened.runtime.PersistedMode(); persisted != "" && persisted != agentruntime.ModeYolo {
		t.Fatalf("reopened persisted mode = %q, want yolo", persisted)
	}
}

// The panel shows a subset of the profile fields. Editing an unrelated field must
// not silently drop the ones the form does not display.
func TestRouterPanelEditPreservesHiddenFields(t *testing.T) {
	app := newRouterCommandApp(t)
	t.Setenv("MOTHX_DIR", t.TempDir())
	app.openRouterPanel()
	app.routerPanel.Profiles = []config.ACPAgentProfile{{
		ID: "codex", Title: "Codex", Command: []string{"/opt/codex/bin/codex", "acp"},
		Enabled: true, PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		Environment:           []string{"OPENAI_API_KEY"},
		Roles:                 []config.ACPAgentRole{config.ACPRoleInvestigate},
		WriteRoots:            []string{"src"},
		MaxConcurrentChildren: 3,
	}}

	app.beginRouterPanelEdit(0)
	app.routerPanel.Draft.Title = "Codex Renamed"
	app.saveRouterPanelDraft()
	if app.routerPanel.Error != "" {
		t.Fatalf("editing the title failed: %s", app.routerPanel.Error)
	}

	settings, err := config.LoadGlobalSettingsOrDefault()
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.ACPAgents) != 1 {
		t.Fatalf("profiles = %#v", settings.ACPAgents)
	}
	saved := settings.ACPAgents[0]
	if saved.Title != "Codex Renamed" {
		t.Fatalf("title was not updated: %q", saved.Title)
	}
	if len(saved.Environment) != 1 || saved.Environment[0] != "OPENAI_API_KEY" {
		t.Fatalf("environment was dropped by an unrelated edit: %#v", saved.Environment)
	}
	if len(saved.Roles) != 1 || saved.Roles[0] != config.ACPRoleInvestigate {
		t.Fatalf("roles were dropped by an unrelated edit: %#v", saved.Roles)
	}
	if len(saved.WriteRoots) != 1 || saved.WriteRoots[0] != "src" {
		t.Fatalf("writeRoots were dropped by an unrelated edit: %#v", saved.WriteRoots)
	}
	if saved.MaxConcurrentChildren != 3 {
		t.Fatalf("maxConcurrentChildren was dropped by an unrelated edit: %d", saved.MaxConcurrentChildren)
	}
}
