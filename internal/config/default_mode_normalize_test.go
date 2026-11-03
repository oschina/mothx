package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
)

// The mode vocabulary is duplicated as literals in settings.go because
// agentruntime imports config. These tests pin the literals to the real
// constants so a rename cannot silently disable the compatibility fallback.
func TestConfigModeLiteralsMatchRuntimeVocabulary(t *testing.T) {
	if agentruntime.ModeRouter != "router" {
		t.Fatalf("settings.go normalizes the literal %q for router, but agentruntime.ModeRouter = %q", "router", agentruntime.ModeRouter)
	}
	if agentruntime.ModePlan != "plan" {
		t.Fatalf("settings.go falls back to the literal %q, but agentruntime.ModePlan = %q", "plan", agentruntime.ModePlan)
	}
}

// A settings.json carrying defaultMode=router must not make every session
// unbuildable. Router needs a per-session worker binding that cannot exist at
// default-resolution time, so loading has to fall back to the product default.
func TestLoadSettingsFallsBackFromRouterDefaultMode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MOTHX_DIR", dir)

	raw, err := json.Marshal(map[string]any{"defaultMode": agentruntime.ModeRouter})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.json")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	settings, _, err := config.LoadSettingsWithMeta()
	if err != nil {
		t.Fatalf("a settings.json with defaultMode=router failed to load: %v", err)
	}
	// Read-only plan, not yolo: Router is read-only, so widening to full access
	// would change the user's intent into a permission grant.
	if settings.DefaultMode != agentruntime.ModePlan {
		t.Fatalf("defaultMode = %q, want the read-only plan fallback", settings.DefaultMode)
	}

	// The other load path must behave identically, or a different entry point
	// would keep the unbuildable value.
	plain, err := config.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings failed: %v", err)
	}
	if plain.DefaultMode != agentruntime.ModePlan {
		t.Fatalf("LoadSettings defaultMode = %q, want %q", plain.DefaultMode, agentruntime.ModePlan)
	}
}

// The fallback must be narrow: every genuinely usable default mode is preserved.
func TestLoadSettingsPreservesUsableDefaultModes(t *testing.T) {
	for _, mode := range []string{agentruntime.ModePlan, agentruntime.ModeAgent, agentruntime.ModeYolo, agentruntime.ModeOS} {
		dir := t.TempDir()
		t.Setenv("MOTHX_DIR", dir)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(map[string]any{"defaultMode": mode})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
		settings, err := config.LoadSettings()
		if err != nil {
			t.Fatalf("defaultMode %q failed to load: %v", mode, err)
		}
		if settings.DefaultMode != mode {
			t.Fatalf("defaultMode %q became %q", mode, settings.DefaultMode)
		}
	}
}
