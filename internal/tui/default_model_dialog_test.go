package tui

import (
	"testing"

	"github.com/oschina/mothx/internal/config"
	providerfactory "github.com/oschina/mothx/internal/provider/factory"
)

// factoryModelIDs returns the model IDs a factory-created provider exposes —
// the exact list the /model dialog renders via a.provider.Models().
func factoryModelIDs(t *testing.T, settings *config.Settings, providerID string) []string {
	t.Helper()
	p, _, err := providerfactory.Create(settings, providerID, "")
	if err != nil {
		t.Fatalf("create provider %q: %v", providerID, err)
	}
	ids := make([]string, 0, len(p.Models()))
	for _, m := range p.Models() {
		ids = append(ids, m.ID)
	}
	return ids
}

func TestDefaultModelDialogListsFactoryResolvedCatalog(t *testing.T) {
	settings := config.DefaultSettings()
	// A settings entry that declares its own models replaces the raw settings
	// list; the resolved catalog keeps built-in-only presets available, which
	// is what the /model dialog shows.
	settings.Providers["anthropic"] = &config.ProviderConfig{
		APIKey: "test-key",
		Models: []config.ModelConfig{{ID: "custom-model", Name: "Custom Model"}},
	}

	a := &App{settings: settings}
	got := a.defaultModelModelIDs("anthropic")
	want := factoryModelIDs(t, settings, "anthropic")

	if len(got) != len(want) {
		t.Fatalf("dialog lists %d models, /model provider lists %d:\n got %v\nwant %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("model %d = %q, want %q (full list %v)", i, got[i], want[i], got)
		}
	}
	if got[0] != "custom-model" {
		t.Fatalf("first model = %q, want runtime override custom-model first", got[0])
	}
	sawPreset := false
	for _, id := range got {
		if id == "claude-sonnet-4-5" {
			sawPreset = true
		}
	}
	if !sawPreset {
		t.Fatalf("built-in preset model missing from dialog list: %v", got)
	}

	// The old raw-settings path would have listed only the runtime override.
	if raw := settings.GetProviderConfig("anthropic").Models; len(raw) != 1 {
		t.Fatalf("test precondition broken: raw settings models = %d, want 1", len(raw))
	}
}

func TestDefaultModelDialogListsPresetModelsForUnconfiguredProvider(t *testing.T) {
	settings := config.DefaultSettings()
	// Simulate a user entry that only configures credentials, the way a sparse
	// settings.json override reaches the runtime map.
	settings.Providers["deepseek-openai"] = &config.ProviderConfig{APIKey: "test-key"}

	a := &App{settings: settings}
	got := a.defaultModelModelIDs("deepseek-openai")
	want := factoryModelIDs(t, settings, "deepseek-openai")

	if len(got) == 0 {
		t.Fatal("expected built-in preset models for credential-only provider entry")
	}
	if len(got) != len(want) {
		t.Fatalf("dialog lists %d models, /model provider lists %d:\n got %v\nwant %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("model %d = %q, want %q", i, got[i], want[i])
		}
	}

	// Unknown providers without presets stay empty instead of erroring.
	if ids := a.defaultModelModelIDs("no-such-provider"); len(ids) != 0 {
		t.Fatalf("unknown provider ids = %v, want empty", ids)
	}
}
