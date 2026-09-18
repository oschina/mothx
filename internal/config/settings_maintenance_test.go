package config

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// TestMaintenanceSettingsDefaultsAndSparsePatch pins the compatibility contract
// for the new maintenance section: an existing settings.json that never mentions
// it keeps yesterday's behavior (reclamation enabled on the Runtime default
// cadence), a zero Settings must not serialize the section, and a patch writes
// only what the caller named.
func TestMaintenanceSettingsDefaultsAndSparsePatch(t *testing.T) {
	data, err := json.Marshal(Settings{})
	if err != nil {
		t.Fatalf("marshal zero settings: %v", err)
	}
	var sparse map[string]json.RawMessage
	if err := json.Unmarshal(data, &sparse); err != nil {
		t.Fatalf("decode marshaled zero settings: %v", err)
	}
	if _, ok := sparse["maintenance"]; ok {
		t.Fatalf("zero settings unexpectedly serialized maintenance: %s", data)
	}

	var absent Settings
	if err := json.Unmarshal([]byte(`{}`), &absent); err != nil {
		t.Fatalf("decode empty settings: %v", err)
	}
	if !absent.IsAttachmentStorageReclaimEnabled() {
		t.Fatal("unset maintenance disabled attachment storage reclamation")
	}
	if got := absent.AttachmentStorageReclaimSchedule(); got != "" {
		t.Fatalf("unset maintenance schedule = %q, want empty so the Runtime default applies", got)
	}
	if defaults := DefaultSettings(); !defaults.IsAttachmentStorageReclaimEnabled() {
		t.Fatal("DefaultSettings must keep reclamation enabled")
	}

	var configured Settings
	if err := json.Unmarshal([]byte(`{"maintenance":{"reclaimAttachmentStorage":false,"storageReconcileSchedule":"  @every 6h  "}}`), &configured); err != nil {
		t.Fatalf("decode maintenance settings: %v", err)
	}
	if configured.IsAttachmentStorageReclaimEnabled() {
		t.Fatal("explicit false did not disable reclamation")
	}
	if got := configured.AttachmentStorageReclaimSchedule(); got != "@every 6h" {
		t.Fatalf("configured schedule = %q, want the trimmed value", got)
	}

	t.Setenv("MOTHX_DIR", filepath.Join(t.TempDir(), "global"))
	if err := SaveGlobalSettingsPatch(map[string]any{
		"theme":       "dark",
		"maintenance": map[string]any{"reclaimAttachmentStorage": false},
	}); err != nil {
		t.Fatalf("save maintenance patch: %v", err)
	}
	settings, err := LoadGlobalSettingsOrDefault()
	if err != nil {
		t.Fatalf("load patched settings: %v", err)
	}
	if settings.IsAttachmentStorageReclaimEnabled() {
		t.Fatalf("patched maintenance settings = %#v, want reclamation disabled", settings.Maintenance)
	}
	if settings.Theme != "dark" {
		t.Fatalf("unrelated patched setting lost: theme=%q", settings.Theme)
	}
	if settings.Maintenance.StorageReconcileSchedule != "" {
		t.Fatalf("patch expanded an untouched field: %#v", settings.Maintenance)
	}
}
