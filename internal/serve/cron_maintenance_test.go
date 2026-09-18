package serve

import (
	"path/filepath"
	"testing"

	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/config"
)

// TestCronMaintenancePolicyReadsGlobalSettings closes the last seam of the
// maintenance switch: serve resolves the policy it hands the scheduler from the
// operator's settings.json, so the projection and the execution check agree with
// what the user configured rather than with a serve-side default.
func TestCronMaintenancePolicyReadsGlobalSettings(t *testing.T) {
	t.Setenv("MOTHX_DIR", filepath.Join(t.TempDir(), "global"))

	if policy := cronMaintenancePolicy(); !policy.ReclaimAttachmentStorage ||
		policy.StorageReconcileSchedule != agentruntime.MaintenanceStorageReconcileSchedule {
		t.Fatalf("default policy = %#v, want reclamation enabled on the Runtime cadence", policy)
	}

	if err := config.SaveGlobalSettingsPatch(map[string]any{
		"maintenance": map[string]any{"reclaimAttachmentStorage": false, "storageReconcileSchedule": "@every 6h"},
	}); err != nil {
		t.Fatal(err)
	}
	policy := cronMaintenancePolicy()
	if policy.ReclaimAttachmentStorage {
		t.Fatalf("policy after disabling maintenance = %#v", policy)
	}
	if policy.StorageReconcileSchedule != "@every 6h" {
		t.Fatalf("policy schedule = %q, want the configured cadence", policy.StorageReconcileSchedule)
	}
}
