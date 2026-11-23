package channels

import (
	"testing"

	"github.com/oschina/mothx/native/internal/feature/hosttest"
)

func TestPanelCurrentGuardsVisibilityAndGeneration(t *testing.T) {
	panel := NewPanel()
	h := hosttest.New()
	h.SettingsVisible = true
	h.SettingsTabName = "channels"
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
}

func TestDeliveriesCurrentGuardsVisibilityAndGeneration(t *testing.T) {
	panel := NewPanel()
	h := hosttest.New()
	h.SettingsVisible = true
	h.SettingsTabName = "channels"
	h.Reload = 7
	panel.DeliveriesSeq = 5

	if !panel.DeliveriesCurrent(h, 5, 7) {
		t.Fatal("fresh deliveries response rejected")
	}
	if panel.DeliveriesCurrent(h, 4, 7) || panel.DeliveriesCurrent(h, 5, 6) {
		t.Fatal("stale deliveries response accepted")
	}
}

func TestPanelInvalidateBumpsSequences(t *testing.T) {
	panel := NewPanel()
	panel.Loaded = true
	panel.DeliveriesLoaded = true
	panel.Seq = 3
	panel.DeliveriesSeq = 4

	panel.Invalidate()

	if panel.Seq != 4 || panel.Loaded || panel.DeliveriesSeq != 5 || panel.DeliveriesLoaded {
		t.Fatalf("invalidate did not reset projections: seq=%d loaded=%v deliveriesSeq=%d deliveriesLoaded=%v",
			panel.Seq, panel.Loaded, panel.DeliveriesSeq, panel.DeliveriesLoaded)
	}
}

func TestPanelCapableFollowsFeatureKey(t *testing.T) {
	panel := NewPanel()
	h := hosttest.New()
	if panel.Capable(h) {
		t.Fatal("panel capable without manageChannels")
	}
	h.Features["manageChannels"] = true
	if !panel.Capable(h) {
		t.Fatal("panel not capable with manageChannels")
	}
}
