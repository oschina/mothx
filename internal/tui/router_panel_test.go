package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/tui/i18n"
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
	runRouterActivation(t, app, app.activateRouterFromPanel())

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

// Runtime activation reloads the persisted session before writing the binding,
// which can take seconds on a large database. The update loop must stay free
// while that work runs, then apply the result through a message.
func TestRouterPanelActivationRunsOffUpdateLoop(t *testing.T) {
	app := newRouterCommandApp(t)
	app.openRouterPanel()
	app.routerPanel.Profiles = []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/opt/codex/bin/codex", "acp"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}}
	app.routerPanel.Cursor = 0

	cmd := app.activateRouterFromPanel()
	if cmd == nil {
		t.Fatal("activation did not return a deferred command")
	}
	if app.mode == agentruntime.ModeRouter || app.runtime.RouterActive() {
		t.Fatal("activation mutated Router state on the update loop")
	}
	if !app.routerPanel.Activating || !app.routerActivating {
		t.Fatal("panel did not expose the in-flight activation state")
	}

	runRouterActivation(t, app, cmd)
	if app.mode != agentruntime.ModeRouter || !app.runtime.RouterActive() {
		t.Fatal("completed activation did not apply the Runtime-resolved mode")
	}
	if app.routerPanel.Open || app.routerActivating {
		t.Fatal("completed activation did not close the panel and clear its busy state")
	}
}

// Role and write roots are per-delegation metadata, so the profile editor must
// not expose profile-level authorization fields.
func TestRouterPanelOmitsLegacyRoleFields(t *testing.T) {
	app := newRouterCommandApp(t)
	app.openRouterPanel()
	app.beginRouterPanelCreate()
	for _, field := range app.routerPanelFormFields() {
		if field.Key == "roles" || field.Key == "writeRoots" {
			t.Fatalf("legacy profile field %q is still exposed: %#v", field.Key, field)
		}
	}
}

func TestRouterPanelEditsTerminalBridgeOptIn(t *testing.T) {
	app := newRouterCommandApp(t)
	app.openRouterPanel()
	app.beginRouterPanelCreate()
	app.beginRouterPanelCreateFromScratch()

	fields := app.routerPanelFormFields()
	terminalIndex := -1
	for i, field := range fields {
		if field.Key == "terminal" {
			terminalIndex = i
			break
		}
	}
	if terminalIndex < 0 {
		t.Fatal("the profile form does not expose terminal access")
	}
	if app.routerPanel.Draft.TerminalAccess {
		t.Fatal("terminal access must default to off")
	}
	app.routerPanel.FormCursor = terminalIndex
	app.handleRouterPanelKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !app.routerPanel.Draft.TerminalAccess {
		t.Fatal("the terminal field did not toggle opt-in")
	}
	if field := app.routerPanelFormFields()[terminalIndex]; field.Description != app.translator.Text(i18n.MsgRouterPanelValueOn) {
		t.Fatalf("terminal field projection = %q, want enabled", field.Description)
	}
}

// The parallel base-workspace writer opt-in is explicit executable trust, so
// the profile form must expose it and default it to off.
func TestRouterPanelEditsParallelBaseWorkspaceWrites(t *testing.T) {
	app := newRouterCommandApp(t)
	app.openRouterPanel()
	app.beginRouterPanelCreate()
	app.beginRouterPanelCreateFromScratch()

	fields := app.routerPanelFormFields()
	parallelIndex := -1
	for i, field := range fields {
		if field.Key == "parallelWrites" {
			parallelIndex = i
			break
		}
	}
	if parallelIndex < 0 {
		t.Fatal("the profile form does not expose parallel base-workspace writes")
	}
	if app.routerPanel.Draft.AllowParallelBaseWorkspaceWrites {
		t.Fatal("parallel base-workspace writes must default to off")
	}
	app.routerPanel.FormCursor = parallelIndex
	app.handleRouterPanelKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !app.routerPanel.Draft.AllowParallelBaseWorkspaceWrites {
		t.Fatal("the parallel writes field did not toggle opt-in")
	}
	if field := app.routerPanelFormFields()[parallelIndex]; field.Description != app.translator.Text(i18n.MsgRouterPanelValueOn) {
		t.Fatalf("parallel writes field projection = %q, want enabled", field.Description)
	}
}
