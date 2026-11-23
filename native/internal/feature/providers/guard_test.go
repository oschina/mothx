package providers

import (
	"testing"

	"github.com/oschina/mothx/native/internal/acp"
)

// fakeHost implements the narrow host contract so feature-package guard logic
// can be tested without the desktop composition shell.
type fakeHost struct {
	features     map[string]bool
	settingsOpen bool
	settingsTab  string
	reloadSeq    int64
}

func (h *fakeHost) Dispatch(func())              {}
func (h *fakeHost) FeatureEnabled(n string) bool { return h.features[n] }
func (h *fakeHost) ACP() *acp.Client             { return nil }
func (h *fakeHost) SettingsOpen() bool           { return h.settingsOpen }
func (h *fakeHost) SettingsTab() string          { return h.settingsTab }
func (h *fakeHost) ReloadSequence() int64        { return h.reloadSeq }

func TestPanelCurrentRejectsStaleResponses(t *testing.T) {
	panel := NewPanel()
	h := &fakeHost{
		features:     map[string]bool{"manageProviders": true},
		settingsOpen: true,
		settingsTab:  "providers",
		reloadSeq:    7,
	}
	panel.Seq = 5

	if !panel.Current(h, 5, 7) {
		t.Fatal("fresh response rejected")
	}
	publishRejected := func(name string, current bool) {
		t.Helper()
		if current {
			t.Fatalf("%s response must be rejected", name)
		}
	}

	publishRejected("older sequence", panel.Current(h, 4, 7))
	publishRejected("older connection", panel.Current(h, 5, 6))

	h.settingsTab = "mcp"
	publishRejected("closed settings tab", panel.Current(h, 5, 7))

	h.settingsTab = "providers"
	h.settingsOpen = false
	publishRejected("closed settings", panel.Current(h, 5, 7))
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

func TestPanelCapabilitiesFollowHostKeys(t *testing.T) {
	panel := NewPanel()
	h := &fakeHost{features: map[string]bool{"manageProviders": true, "manageSettings": true}}
	caps := panel.Capabilities(h)
	if !caps.Catalog || !caps.Settings || caps.Config {
		t.Fatalf("capabilities = %+v", caps)
	}

	h.features["manageProviderConfig"] = true
	if caps := panel.Capabilities(h); !caps.Config {
		t.Fatalf("provider config capability not projected: %+v", caps)
	}
}
