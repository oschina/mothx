package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/config"
)

// TestRunMaintenanceCronJobClaimsTheWholeNamespace proves the two properties that
// keep a maintenance job from becoming a model turn: only the Runtime's own
// namespaced IDs are claimed, and an unrecognized one inside the namespace is
// refused loudly instead of being handed to cron's ordinary agent path.
func TestRunMaintenanceCronJobClaimsTheWholeNamespace(t *testing.T) {
	policy := DefaultMaintenancePolicy()
	if handled, _, err := RunMaintenanceCronJob(context.Background(), t.TempDir(), "knowledge-base-index:abc", policy); handled {
		t.Fatal("the maintenance path claimed a job outside its namespace")
	} else if err != nil {
		t.Fatalf("declining a foreign job must not error: %v", err)
	}

	handled, _, err := RunMaintenanceCronJob(context.Background(), t.TempDir(), MaintenanceCronJobPrefix+"unknown-task", policy)
	if !handled {
		t.Fatal("an unknown maintenance-namespaced job was not claimed; it would fall through to the agent path")
	}
	if err == nil || !strings.Contains(err.Error(), "unknown maintenance job") {
		t.Fatalf("unknown maintenance job error = %v, want a refusal", err)
	}

	if _, _, err := RunMaintenanceCronJob(context.Background(), "", MaintenanceStorageReconcileJobID(), policy); err == nil {
		t.Fatal("a maintenance job without a session directory was accepted")
	}
}

// TestRunMaintenanceCronJobHonorsTheDisabledPolicy covers the stale-row case:
// settings turned reclamation off, but a job persisted by an older process still
// exists. The pass must report a skip and leave the aged directory in place.
func TestRunMaintenanceCronJobHonorsTheDisabledPolicy(t *testing.T) {
	root, _, _ := inputTestSession(t)
	policy := DefaultAttachmentPolicy()
	aged := writeArtifactDirectory(t, root, "7123456789abcdef", policy.Retention+artifactReconcileGrace+time.Hour, "stale")

	handled, response, err := RunMaintenanceCronJob(context.Background(), root, MaintenanceStorageReconcileJobID(), MaintenancePolicy{ReclaimAttachmentStorage: false})
	if err != nil || !handled {
		t.Fatalf("disabled maintenance run = handled:%v err:%v", handled, err)
	}
	if !strings.Contains(response, "disabled") {
		t.Fatalf("response = %q, want an explicit skip reason", response)
	}
	if _, statErr := os.Stat(aged); statErr != nil {
		t.Fatalf("a disabled policy still reclaimed storage: %v", statErr)
	}
}

// TestMaintenancePolicyFromSettings pins the compatibility contract: absent
// configuration keeps yesterday's behavior (enabled, daily), and each field
// overrides independently.
func TestMaintenancePolicyFromSettings(t *testing.T) {
	if got := MaintenancePolicyFromSettings(nil); got != DefaultMaintenancePolicy() {
		t.Fatalf("nil settings policy = %#v, want the default", got)
	}
	if got := MaintenancePolicyFromSettings(&config.Settings{}); got.ReclaimAttachmentStorage == false ||
		got.StorageReconcileSchedule != MaintenanceStorageReconcileSchedule {
		t.Fatalf("empty settings policy = %#v, want enabled on the default cadence", got)
	}

	off := false
	if got := MaintenancePolicyFromSettings(&config.Settings{Maintenance: &config.MaintenanceSettings{ReclaimAttachmentStorage: &off}}); got.ReclaimAttachmentStorage {
		t.Fatalf("explicit false policy = %#v, want reclamation disabled", got)
	}
	if got := MaintenancePolicyFromSettings(&config.Settings{Maintenance: &config.MaintenanceSettings{
		StorageReconcileSchedule: "  @every 6h  ",
	}}); got.StorageReconcileSchedule != "@every 6h" || !got.ReclaimAttachmentStorage {
		t.Fatalf("schedule override policy = %#v, want the trimmed cadence with reclamation still enabled", got)
	}
}

func TestRunMaintenanceCronJobReclaimsAgedAttachmentStorage(t *testing.T) {
	root, _, _ := inputTestSession(t)
	policy := DefaultAttachmentPolicy()
	aged := writeArtifactDirectory(t, root, "6123456789abcdef", policy.Retention+artifactReconcileGrace+time.Hour, "stale")

	handled, response, err := RunMaintenanceCronJob(context.Background(), root, MaintenanceStorageReconcileJobID(), DefaultMaintenancePolicy())
	if err != nil || !handled {
		t.Fatalf("maintenance run = handled:%v err:%v", handled, err)
	}
	if !strings.Contains(response, "reclaimed 1 unreferenced attachment directories") {
		t.Fatalf("response = %q, want the reclaimed count", response)
	}
	if _, err := os.Stat(aged); !os.IsNotExist(err) {
		t.Fatalf("the scheduled pass did not remove %s: %v", aged, err)
	}
	if _, err := os.Stat(filepath.Join(root, artifactStorageDirectoryName, "6123456789abcdef")); !os.IsNotExist(err) {
		t.Fatalf("the artifact directory survived: %v", err)
	}
}
