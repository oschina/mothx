package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSettingsProjectSupportsFalseAndZeroOverrides(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	defer os.Chdir(oldWd)
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Setenv("MOTHX_DIR", tmpDir+"/config")
	t.Setenv("VIBECODING_PROVIDER", "")
	t.Setenv("VIBECODING_MODEL", "")
	t.Setenv("VIBECODING_MODE", "")
	t.Setenv("VIBECODING_THINKING", "")

	if err := os.MkdirAll(filepath.Dir(ProjectSettingsPath()), 0700); err != nil {
		t.Fatalf("mkdir project config dir: %v", err)
	}
	data := []byte(`{
		"maxContextTokens": 0,
		"webSearch": {"baseUrl": "https://search.example.com", "token": "search-token"},
		"contextFiles": {"enabled": false},
		"compaction": {"enabled": false, "reserveTokens": 0, "keepRecentTokens": 0},
		"retry": {"enabled": false, "maxRetries": 0, "baseDelayMs": 0}
	}`)
	if err := os.WriteFile(ProjectSettingsPath(), data, 0600); err != nil {
		t.Fatalf("write project settings: %v", err)
	}

	s, err := LoadSettings()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if s.MaxContextTokens != 0 {
		t.Fatalf("max context token zero override not applied: %d", s.MaxContextTokens)
	}
	if s.WebSearch.BaseURL != "https://search.example.com" || s.WebSearch.Token != "search-token" {
		t.Fatalf("webSearch = %#v, want baseUrl/token override", s.WebSearch)
	}
	if s.ContextFiles.Enabled {
		t.Fatal("contextFiles.enabled=false override not applied")
	}
	if s.Compaction.Enabled || s.Compaction.ReserveTokens != 0 || s.Compaction.KeepRecentTokens != 0 {
		t.Fatalf("compaction zero/false overrides not applied: %#v", s.Compaction)
	}
	if s.Retry.Enabled || s.Retry.MaxRetries != 0 || s.Retry.BaseDelayMs != 0 {
		t.Fatalf("retry zero/false overrides not applied: %#v", s.Retry)
	}
}
