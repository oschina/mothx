package skillhub

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/oschina/mothx/native/internal/acp"
	"github.com/oschina/mothx/native/internal/kit"
)

// fakeSkillHubHost avoids importing hosttest, which imports this package for
// the SkillIconElement signature.
type fakeSkillHubHost struct {
	features map[string]bool
	panels   map[string]bool
	reload   int64
	session  string
	page     string
}

func (h *fakeSkillHubHost) Dispatch(func()) {}

func (h *fakeSkillHubHost) FeatureEnabled(name string) bool { return h.features[name] }

func (h *fakeSkillHubHost) ACP() *acp.Client { return nil }

func (h *fakeSkillHubHost) SettingsOpen() bool { return false }

func (h *fakeSkillHubHost) SettingsTab() string { return "" }

func (h *fakeSkillHubHost) ReloadSequence() int64 { return h.reload }

func (h *fakeSkillHubHost) PanelVisible(page, tab string) bool { return h.panels[page+"|"+tab] }

func (h *fakeSkillHubHost) Page() string { return h.page }

func (h *fakeSkillHubHost) CurrentSession() string { return h.session }

func (h *fakeSkillHubHost) StartNewTask() {}

func (h *fakeSkillHubHost) SkillIconElement(*ui.Context, kit.Palette, Skill, float32) {}

func newFakeSkillHubHost() *fakeSkillHubHost {
	return &fakeSkillHubHost{features: make(map[string]bool), panels: make(map[string]bool)}
}

func TestPanelCurrentGuardsVisibilityAndGeneration(t *testing.T) {
	panel := NewPanel()
	h := newFakeSkillHubHost()
	h.panels["skills|skillhub"] = true
	h.reload = 7
	panel.ConfigSeq = 5
	panel.CatalogSeq = 6

	if !panel.ConfigCurrent(h, 5, 7) {
		t.Fatal("fresh config response rejected")
	}
	if !panel.CatalogCurrent(h, 6, 7) {
		t.Fatal("fresh catalog response rejected")
	}
	if panel.ConfigCurrent(h, 4, 7) || panel.ConfigCurrent(h, 5, 6) {
		t.Fatal("stale config response accepted")
	}
	if panel.CatalogCurrent(h, 5, 7) || panel.CatalogCurrent(h, 6, 6) {
		t.Fatal("stale catalog response accepted")
	}
	h.panels["skills|skillhub"] = false
	if panel.ConfigCurrent(h, 5, 7) || panel.CatalogCurrent(h, 6, 7) {
		t.Fatal("hidden marketplace page accepted")
	}
}

func TestPanelInvalidateBumpsSequences(t *testing.T) {
	panel := NewPanel()
	panel.ConfigLoaded = true
	panel.CatalogLoaded = true
	panel.ConfigSeq = 3
	panel.CatalogSeq = 4

	panel.Invalidate()

	if panel.ConfigSeq != 4 || panel.CatalogSeq != 5 || panel.ConfigLoaded || panel.CatalogLoaded {
		t.Fatalf("invalidate did not reset projections: configSeq=%d catalogSeq=%d configLoaded=%v catalogLoaded=%v",
			panel.ConfigSeq, panel.CatalogSeq, panel.ConfigLoaded, panel.CatalogLoaded)
	}
}

func TestCapableFollowsEitherFeatureKey(t *testing.T) {
	panel := NewPanel()
	h := newFakeSkillHubHost()
	if panel.Capable(h) || panel.ConfigCapable(h) || panel.CatalogCapable(h) {
		t.Fatal("panel capable without a SkillHub feature key")
	}
	h.features["manageSkillHub"] = true
	if !panel.ConfigCapable(h) || !panel.Capable(h) {
		t.Fatal("config capability not projected")
	}
	h.features["manageSkillHubCatalog"] = true
	if !panel.CatalogCapable(h) {
		t.Fatal("catalog capability not projected")
	}
}
