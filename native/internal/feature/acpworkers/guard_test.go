package acpworkers

import (
	"testing"

	"github.com/oschina/mothx/native/internal/feature/hosttest"
)

func TestPanelCurrentGuardsVisibilityAndGeneration(t *testing.T) {
	panel := NewPanel()
	h := hosttest.New()
	h.SettingsVisible = true
	h.SettingsTabName = "acpWorkers"
	h.Reload = 7
	panel.Seq = 5

	if !panel.Current(h, 5, 7) {
		t.Fatal("fresh response rejected")
	}
	if panel.Current(h, 4, 7) {
		t.Fatal("older sequence accepted")
	}
	if panel.Current(h, 5, 6) {
		t.Fatal("older connection accepted")
	}
	h.SettingsVisible = false
	if panel.Current(h, 5, 7) {
		t.Fatal("closed settings accepted")
	}
	h.SettingsVisible = true
	h.SettingsTabName = "other"
	if panel.Current(h, 5, 7) {
		t.Fatal("wrong settings tab accepted")
	}
}

func TestPanelInvalidateBumpsSequence(t *testing.T) {
	panel := NewPanel()
	panel.Loaded = true
	panel.Seq = 3

	panel.Invalidate()

	if panel.Seq != 4 || panel.Loaded {
		t.Fatalf("invalidate did not reset the projection: seq=%d loaded=%v", panel.Seq, panel.Loaded)
	}
}

func TestPanelCapableFollowsFeatureKey(t *testing.T) {
	panel := NewPanel()
	h := hosttest.New()
	if panel.Capable(h) {
		t.Fatal("panel capable without manageACPProfiles")
	}
	h.Features["manageACPProfiles"] = true
	if !panel.Capable(h) {
		t.Fatal("panel not capable with manageACPProfiles")
	}
}
