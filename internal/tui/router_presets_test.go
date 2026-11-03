package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
)

// `a` must open the preset picker, not an empty form: the user should not have to
// know each tool's ACP launch recipe before they can add a worker.
func TestRouterPanelCreateStartsWithPresetPicker(t *testing.T) {
	app := newRouterCommandApp(t)
	app.openRouterPanel()

	app.handleRouterPanelKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if !app.routerPanel.ChoosingPreset {
		t.Fatal("`a` did not open the preset picker")
	}
	if !app.routerPanel.Editing {
		t.Fatal("the form is not active while the picker is shown")
	}
}

// Choosing a preset must fill command, id, title, roles, and permission so the
// saved profile is immediately usable.
func TestRouterPanelPresetFillsUsableProfile(t *testing.T) {
	app := newRouterCommandApp(t)
	t.Setenv("MOTHX_DIR", t.TempDir())
	app.openRouterPanel()
	app.beginRouterPanelCreate()

	presets := agentruntime.ACPAgentPresets()
	index := -1
	for i, preset := range presets {
		if preset.ID == "opencode" {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatal("opencode preset missing")
	}
	app.routerPanel.PresetCursor = index
	app.handleRouterPanelKey(tea.KeyMsg{Type: tea.KeyEnter})

	if app.routerPanel.ChoosingPreset {
		t.Fatal("choosing a preset left the picker open")
	}
	if got := strings.Join(app.routerPanel.Draft.Command, " "); got != "opencode acp" {
		t.Fatalf("preset command = %q", got)
	}
	if app.routerPanel.Draft.ID != "opencode" || app.routerPanel.Draft.Title == "" {
		t.Fatalf("preset did not fill id/title: %#v", app.routerPanel.Draft)
	}
	if len(app.selectedRouterPanelRoles()) == 0 {
		t.Fatal("preset left the role ceiling empty")
	}

	app.saveRouterPanelDraft()
	if app.routerPanel.Error != "" {
		t.Fatalf("saving a preset failed: %s", app.routerPanel.Error)
	}
	settings, err := config.LoadGlobalSettingsOrDefault()
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.ACPAgents) != 1 || settings.ACPAgents[0].ID != "opencode" {
		t.Fatalf("preset was not persisted: %#v", settings.ACPAgents)
	}
}

// The multi-token argument list must survive the panel: this is the whole reason
// `opencode acp` needs an argv, not a single string.
func TestRouterPanelPresetKeepsArguments(t *testing.T) {
	app := newRouterCommandApp(t)
	t.Setenv("MOTHX_DIR", t.TempDir())
	app.openRouterPanel()
	app.beginRouterPanelCreate()

	for i, preset := range agentruntime.ACPAgentPresets() {
		if preset.ID == "copilot" {
			app.routerPanel.PresetCursor = i
			break
		}
	}
	app.handleRouterPanelKey(tea.KeyMsg{Type: tea.KeyEnter})
	if len(app.routerPanel.Draft.Command) < 2 {
		t.Fatalf("preset arguments were dropped: %#v", app.routerPanel.Draft.Command)
	}
	want := append([]string(nil), app.routerPanel.Draft.Command...)

	app.saveRouterPanelDraft()
	if app.routerPanel.Error != "" {
		t.Fatalf("save failed: %s", app.routerPanel.Error)
	}
	settings, err := config.LoadGlobalSettingsOrDefault()
	if err != nil {
		t.Fatal(err)
	}
	persisted := settings.ACPAgents[0].Command
	if len(persisted) != len(want) {
		t.Fatalf("persisted argv = %#v, want %#v", persisted, want)
	}
	for i := range want {
		if persisted[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q", i, persisted[i], want[i])
		}
	}
}

// The last picker row must open a blank form for an agent that is not listed.
func TestRouterPanelPresetCustomRow(t *testing.T) {
	app := newRouterCommandApp(t)
	app.openRouterPanel()
	app.beginRouterPanelCreate()

	app.routerPanel.PresetCursor = len(agentruntime.ACPAgentPresets())
	app.handleRouterPanelKey(tea.KeyMsg{Type: tea.KeyEnter})

	if app.routerPanel.ChoosingPreset {
		t.Fatal("the custom row left the picker open")
	}
	if len(app.routerPanel.Draft.Command) != 0 || app.routerPanel.Draft.ID != "" {
		t.Fatalf("the custom row prefilled a profile: %#v", app.routerPanel.Draft)
	}
}

// Esc from the picker must not leave a half-open form behind.
func TestRouterPanelPresetPickerCancel(t *testing.T) {
	app := newRouterCommandApp(t)
	app.openRouterPanel()
	app.beginRouterPanelCreate()

	app.handleRouterPanelKey(tea.KeyMsg{Type: tea.KeyEsc})
	if app.routerPanel.ChoosingPreset || app.routerPanel.Editing {
		t.Fatal("Esc left the create flow active")
	}
	if !app.routerPanel.Open {
		t.Fatal("Esc closed the whole panel instead of just the form")
	}
}

// The picker must be reachable and navigable without a profile already existing:
// this is the first-run path.
func TestRouterPanelPresetPickerOnEmptyList(t *testing.T) {
	app := newRouterCommandApp(t)
	// The panel loads profiles from global settings, so isolate the config dir or
	// another test's write would decide whether this list starts empty.
	t.Setenv("MOTHX_DIR", t.TempDir())
	app.openRouterPanel()
	app.routerPanel.Profiles = nil
	rendered := stripANSI(app.renderRouterPanel())
	if !strings.Contains(rendered, "a") {
		t.Log("empty-state hint missing the add key")
	}
	app.handleRouterPanelKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if !app.routerPanel.ChoosingPreset {
		t.Fatal("cannot start the create flow from an empty profile list")
	}
	app.handleRouterPanelKey(tea.KeyMsg{Type: tea.KeyDown})
	app.handleRouterPanelKey(tea.KeyMsg{Type: tea.KeyDown})
	app.handleRouterPanelKey(tea.KeyMsg{Type: tea.KeyEnter})
	if app.routerPanel.ChoosingPreset {
		t.Fatal("selecting the third preset did not open the form")
	}
}
