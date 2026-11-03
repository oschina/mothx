package tui

import (
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
)

// TestRouterCommandWithoutArgumentOpensPanel locks the UI contract: `/router`
// is a management surface, while `/router <id>` still activates directly.
func TestRouterCommandWithoutArgumentOpensPanel(t *testing.T) {
	app := newRouterCommandApp(t)
	if app.routerPanel.Open {
		t.Fatal("panel opened before the command ran")
	}
	app.handleCommand("/router")
	if !app.routerPanel.Open {
		t.Fatal("/router did not open the ACP worker panel")
	}
	if app.mode == agentruntime.ModeRouter {
		t.Fatal("/router without an argument must not activate Router")
	}
	if _, ok := app.runtime.RouterBinding(); ok {
		t.Fatal("/router without an argument persisted a Router binding")
	}
}

// TestRouterPanelProjectsRuntimeEligibility proves the panel does not compute
// eligibility itself: its status column comes from the shared Runtime catalog.
func TestRouterPanelProjectsRuntimeEligibility(t *testing.T) {
	app := newRouterCommandApp(t)
	app.openRouterPanel()

	app.routerPanel.Profiles = []config.ACPAgentProfile{
		{ID: "codex", Title: "Codex", Command: []string{"/opt/codex/bin/codex", "acp"}, Enabled: true, PermissionPolicy: config.ACPPermissionAutoAllowOnce},
		{ID: "helper", Command: []string{"/opt/helper/bin/helper"}, Enabled: false, PermissionPolicy: config.ACPPermissionAutoAllowOnce},
		{ID: "broken", Command: []string{" "}, Enabled: true},
	}
	descriptors := app.routerPanelDescriptors()
	if !descriptors["codex"].RouterEligible {
		t.Fatalf("enabled non-interactive profile projected as ineligible: %#v", descriptors["codex"])
	}
	if descriptors["helper"].RouterEligible {
		t.Fatal("a disabled profile projected as Router-eligible")
	}
	if descriptors["broken"].InvalidReason == "" {
		t.Fatal("an empty command[0] was not projected as invalid")
	}
}

// TestRouterPanelSaveUsesSharedValidation proves the panel cannot persist a
// profile set that the shared config owner would reject.
func TestRouterPanelSaveUsesSharedValidation(t *testing.T) {
	app := newRouterCommandApp(t)
	t.Setenv("MOTHX_DIR", t.TempDir())
	app.openRouterPanel()

	app.beginRouterPanelCreate()
	app.routerPanel.Draft = config.ACPAgentProfile{
		ID:      "empty",
		Command: []string{"  "},
		Enabled: true,
	}
	app.routerPanel.Roles = map[config.ACPAgentRole]bool{config.ACPRoleInvestigate: true}
	app.saveRouterPanelDraft()

	if app.routerPanel.Error == "" {
		t.Fatal("the panel accepted a profile with an empty command[0]")
	}
	if !app.routerPanel.Editing {
		t.Fatal("a rejected save closed the editor form")
	}
	if settings, err := config.LoadGlobalSettingsOrDefault(); err != nil {
		t.Fatal(err)
	} else if len(settings.ACPAgents) != 0 {
		t.Fatalf("a rejected save persisted %#v", settings.ACPAgents)
	}
}

// TestRouterPanelActivationGoesThroughRuntimeFence proves the panel's activate
// action uses the shared activation path and its idle/source policy checks: a
// profile that Runtime does not consider eligible must be rejected there, not
// filtered by the panel.
func TestRouterPanelActivationGoesThroughRuntimeFence(t *testing.T) {
	app := newRouterCommandApp(t)
	t.Setenv("MOTHX_DIR", t.TempDir())
	app.openRouterPanel()

	// Persist the profile as disabled, then reload so the panel and the Runtime
	// settings agree on the same durable state.
	disabled := []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/opt/codex/bin/codex", "acp"}, Enabled: false,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}}
	if err := config.SaveACPAgentProfiles(disabled); err != nil {
		t.Fatal(err)
	}
	app.refreshSettingsAfterRouterPanelSave()
	app.loadRouterPanelProfiles()
	app.routerPanel.Cursor = 0
	app.activateRouterFromPanel()

	if app.mode == agentruntime.ModeRouter {
		t.Fatal("the panel activated Router for a disabled profile")
	}
	if app.routerPanel.Error == "" {
		t.Fatal("the panel did not surface the Runtime rejection")
	}
	if _, ok := app.runtime.RouterBinding(); ok {
		t.Fatal("a rejected activation persisted a Router binding")
	}
}

// TestRouterPanelRoleToggleKeepsACeiling guards the invariant that a profile
// can never be saved with an empty role ceiling.
func TestRouterPanelRoleToggleKeepsACeiling(t *testing.T) {
	app := newRouterCommandApp(t)
	app.openRouterPanel()
	app.beginRouterPanelCreate()

	app.toggleRouterPanelRole(config.ACPRoleInvestigate)
	if len(app.selectedRouterPanelRoles()) != 1 {
		t.Fatalf("clearing the last role was allowed: %#v", app.selectedRouterPanelRoles())
	}
	if !strings.Contains(app.routerPanel.Message, "role") && app.routerPanel.Message == "" {
		t.Fatal("no feedback was shown when the last role toggle was refused")
	}
}
