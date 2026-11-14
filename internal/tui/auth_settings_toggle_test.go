package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/tui/components/editor"
)

func newSettingsToggleTestApp(t *testing.T) (*App, string) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("MOTHX_DIR", tmpDir)
	path := filepath.Join(tmpDir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"defaultProvider":"deepseek-openai"}`), 0600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	a := &App{
		settings: config.DefaultSettings(),
		width:    80,
		mode:     "agent",
		input:    editor.New(80),
		auth: authDialogState{
			Open: true,
			View: authViewSettingsWebSearch,
			Mode: "settings",
		},
	}
	return a, path
}

func persistedWebSearchEnabled(t *testing.T, path string) *bool {
	t.Helper()
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var patch struct {
		WebSearch struct {
			Enabled *bool `json:"enabled"`
		} `json:"webSearch"`
	}
	if err := json.Unmarshal(out, &patch); err != nil {
		t.Fatalf("parse settings %s: %v", out, err)
	}
	return patch.WebSearch.Enabled
}

func TestSettingsToggleEnablesDefaultOffWebSearch(t *testing.T) {
	a, path := newSettingsToggleTestApp(t)

	a.selectSettingsFieldValue("webSearch.enabled")
	if !a.settings.IsWebSearchEnabled() {
		t.Fatal("web search stayed disabled after first toggle")
	}
	if v := persistedWebSearchEnabled(t, path); v == nil || !*v {
		t.Fatalf("enabled web search was not persisted, got %v", v)
	}

	// The persisted value must survive a reload so the settings panel and the
	// Agent runtime agree on the same effective state.
	reloaded, err := config.LoadSettings()
	if err != nil {
		t.Fatalf("reload settings: %v", err)
	}
	if !reloaded.IsWebSearchEnabled() {
		t.Fatal("web search was persisted but reloads as disabled")
	}

	a.selectSettingsFieldValue("webSearch.enabled")
	if a.settings.IsWebSearchEnabled() {
		t.Fatal("web search stayed enabled after second toggle")
	}
	if v := persistedWebSearchEnabled(t, path); v == nil || *v {
		t.Fatalf("disabled web search was not persisted, got %v", v)
	}
}

func TestToggleSettingsBoolPtrAlwaysReturnsExplicitValue(t *testing.T) {
	boolPtr := func(v bool) *bool { return &v }
	cases := []struct {
		name         string
		value        *bool
		defaultValue bool
		want         bool
	}{
		{name: "auto default off turns on", value: nil, defaultValue: false, want: true},
		{name: "auto default on turns off", value: nil, defaultValue: true, want: false},
		{name: "explicit off turns on", value: boolPtr(false), defaultValue: false, want: true},
		{name: "explicit on turns off", value: boolPtr(true), defaultValue: true, want: false},
		{name: "override on keeps toggling", value: boolPtr(true), defaultValue: false, want: false},
		{name: "override off keeps toggling", value: boolPtr(false), defaultValue: true, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := toggleSettingsBoolPtr(tc.value, tc.defaultValue)
			if got == nil {
				t.Fatal("toggle returned nil; an explicit value is required to survive reload")
			}
			if *got != tc.want {
				t.Fatalf("toggle = %v, want %v", *got, tc.want)
			}
		})
	}
}
