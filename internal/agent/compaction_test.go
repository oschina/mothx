package agent

import (
	"testing"

	"github.com/oschina/mothx/internal/config"
	ctxpkg "github.com/oschina/mothx/internal/context"
	"github.com/oschina/mothx/internal/provider"
)

func TestCompactionSettingsFromConfigCopiesAllFields(t *testing.T) {
	in := config.CompactionSettings{
		Enabled:          true,
		ReserveTokens:    12345,
		KeepRecentTokens: 54321,
		Tokenizer:        "deepseek",
		TokenizerModel:   "deepseek-v3",
		Template:         "custom-template",
	}

	got := CompactionSettingsFromConfig(in)

	if got.Enabled != in.Enabled {
		t.Errorf("Enabled = %v, want %v", got.Enabled, in.Enabled)
	}
	if got.ReserveTokens != in.ReserveTokens {
		t.Errorf("ReserveTokens = %d, want %d", got.ReserveTokens, in.ReserveTokens)
	}
	if got.KeepRecentTokens != in.KeepRecentTokens {
		t.Errorf("KeepRecentTokens = %d, want %d", got.KeepRecentTokens, in.KeepRecentTokens)
	}
	if got.Tokenizer != in.Tokenizer {
		t.Errorf("Tokenizer = %q, want %q", got.Tokenizer, in.Tokenizer)
	}
	if got.TokenizerModel != in.TokenizerModel {
		t.Errorf("TokenizerModel = %q, want %q", got.TokenizerModel, in.TokenizerModel)
	}
	if got.Template != in.Template {
		t.Errorf("Template = %q, want %q", got.Template, in.Template)
	}
}

func TestCompactionSettingsFromConfigPreservesDisabled(t *testing.T) {
	got := CompactionSettingsFromConfig(config.CompactionSettings{Enabled: false})
	if got.Enabled {
		t.Error("explicitly disabled compaction must stay disabled")
	}
}

func TestAgentManagerUpdateRuntimeConfigSyncsCompactionSettings(t *testing.T) {
	oldModel := &provider.Model{ID: "old-model", Name: "Old", Provider: "old-provider"}
	oldProvider := provider.NewMockProvider("old-provider", []*provider.Model{oldModel}, nil)
	newModel := &provider.Model{ID: "new-model", Name: "New", Provider: "new-provider"}
	newProvider := provider.NewMockProvider("new-provider", []*provider.Model{newModel}, nil)

	settings := &config.Settings{SessionDir: t.TempDir()}
	settings.Compaction = config.CompactionSettings{
		Enabled:          true,
		ReserveTokens:    12345,
		KeepRecentTokens: 54321,
		Tokenizer:        "deepseek",
		TokenizerModel:   "deepseek-v3",
		Template:         "custom-template",
	}

	m := NewAgentManager(NewAgentFactory(oldProvider, oldModel, config.DefaultSettings(), nil, "", "", nil, compactionSettings(), nil))
	m.UpdateRuntimeConfig(newProvider, "new-provider", newModel, settings, nil)

	a, err := m.Create(AgentOptions{ID: "future"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cfg, ok := runtimeConfigOfManagedAgent(a)
	if !ok {
		t.Fatal("future agent does not expose runtime config")
	}
	// Agent constructors normalize zero-valued limits, so compare against the
	// normalized form of the converted settings.
	want := ctxpkg.NormalizeCompactionSettings(CompactionSettingsFromConfig(settings.Compaction))
	if cfg.CompactionSettings != want {
		t.Fatalf("CompactionSettings = %#v, want %#v", cfg.CompactionSettings, want)
	}
}
