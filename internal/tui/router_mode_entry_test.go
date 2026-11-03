package tui

import (
	"testing"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/tools"
	"github.com/oschina/mothx/internal/tui/components/editor"
)

// TestCycleModeNeverEntersRouter locks the adapter-edge contract: Router is not a
// mode a user can switch into. It only exists for one activated external worker,
// so the Tab cycle must not offer it and must not leave it.
func TestCycleModeNeverEntersRouter(t *testing.T) {
	app := NewApp(nil, &provider.Model{ID: "test"}, config.DefaultSettings(), nil,
		tools.NewRegistry(t.TempDir(), nil), "", "", "", nil, "yolo", false, false, nil, nil, nil)

	seen := map[string]bool{}
	for i := 0; i < 8; i++ {
		app.cycleMode()
		if app.mode == agentruntime.ModeRouter {
			t.Fatalf("Tab cycle entered Router at step %d", i)
		}
		seen[app.mode] = true
	}
	for _, want := range []string{agentruntime.ModePlan, agentruntime.ModeAgent, agentruntime.ModeYolo, agentruntime.ModeOS} {
		if !seen[want] {
			t.Fatalf("Tab cycle never reached %q; saw %v", want, seen)
		}
	}
}

// TestCycleModeLeavesRouterWhenAlreadyInIt proves an already-Router session can
// still cycle out instead of getting stuck.
func TestCycleModeLeavesRouterWhenAlreadyInIt(t *testing.T) {
	app := NewApp(nil, &provider.Model{ID: "test"}, config.DefaultSettings(), nil,
		tools.NewRegistry(t.TempDir(), nil), "", "", "", nil, agentruntime.ModeRouter, false, false, nil, nil, nil)
	app.cycleMode()
	if app.mode == agentruntime.ModeRouter {
		t.Fatal("Tab cycle stayed in Router")
	}
}

// TestModeSwitchPersistsThroughRuntime locks the mode-write ownership contract:
// the adapter may not change a.mode without the Runtime, or a session resumed
// from its replay entry would silently return to the previous mode. Leaving
// Router is the highest-risk case, because re-entering it re-applies the
// read-only tool fence and actorizes a worker the user stopped using.
func TestModeSwitchPersistsThroughRuntime(t *testing.T) {
	app := newRouterCommandApp(t)

	app.handleCommand("/router codex")
	if persisted := app.runtime.PersistedMode(); persisted != agentruntime.ModeRouter {
		t.Fatalf("persisted mode after activation = %q, want router", persisted)
	}

	app.handleCommand("/mode plan")
	if app.mode != agentruntime.ModePlan {
		t.Fatalf("adapter mode = %q, want plan", app.mode)
	}
	if persisted := app.runtime.PersistedMode(); persisted != agentruntime.ModePlan {
		t.Fatalf("persisted mode after leaving Router = %q, want plan", persisted)
	}
}

// TestCycleModePersistsThroughRuntime covers the other adapter write path.
func TestCycleModePersistsThroughRuntime(t *testing.T) {
	app := newRouterCommandApp(t)

	app.applyModeSelection(agentruntime.ModePlan)
	app.cycleMode()
	if app.mode != agentruntime.ModeAgent {
		t.Fatalf("adapter mode after Tab = %q, want agent", app.mode)
	}
	if persisted := app.runtime.PersistedMode(); persisted != agentruntime.ModeAgent {
		t.Fatalf("persisted mode after Tab = %q, want agent", persisted)
	}
}

// TestDefaultModeCycleExcludesRouter keeps the settings editor from offering a
// default that cannot work: Router needs a per-session activated binding, so it
// must not be in the defaultMode cycle list. This drives the real settings
// handler, so deleting `router` from the list is the only way to keep it green.
func TestDefaultModeCycleExcludesRouter(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MOTHX_DIR", tmpDir)
	app := &App{
		settings: config.DefaultSettings(),
		width:    80,
		mode:     "yolo",
		auth: authDialogState{
			Open: true,
			View: authViewSettingsBehavior,
			Mode: "settings",
		},
	}
	app.input = editor.New(80)

	for i := 0; i < len(defaultModeCycleValues); i++ {
		app.selectSettingsFieldValue("defaultMode")
		if app.settings.DefaultMode == agentruntime.ModeRouter {
			t.Fatalf("the defaultMode cycle produced Router at step %d", i)
		}
	}
	if app.settings.DefaultMode != "yolo" {
		t.Fatalf("defaultMode cycle did not wrap back to its start, got %q", app.settings.DefaultMode)
	}
}
