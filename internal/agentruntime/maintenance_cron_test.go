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
		got.StorageReconcileSchedule != MaintenanceStorageReconcileSchedule ||
		got.ReclaimSessionStorage == false ||
		got.SessionStorageSchedule != MaintenanceSessionStorageSchedule ||
		got.ToolExecutionRetention != 0 || got.VacuumSessionStorage {
		t.Fatalf("empty settings policy = %#v, want both jobs enabled with transcript retention", got)
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

	sessionOn, vacuumOn := true, true
	got := MaintenancePolicyFromSettings(&config.Settings{Maintenance: &config.MaintenanceSettings{
		ReclaimSessionStorage:      &sessionOn,
		SessionStorageSchedule:     "  @every 12h  ",
		ToolExecutionRetentionDays: 45,
		VacuumSessionStorage:       &vacuumOn,
	}})
	if !got.ReclaimSessionStorage || got.SessionStorageSchedule != "@every 12h" ||
		got.ToolExecutionRetention != 45*24*time.Hour || !got.VacuumSessionStorage {
		t.Fatalf("session storage policy = %#v, want the explicit retention and vacuum", got)
	}
	sessionOff := false
	if got := MaintenancePolicyFromSettings(&config.Settings{Maintenance: &config.MaintenanceSettings{
		ReclaimSessionStorage: &sessionOff,
	}}); got.ReclaimSessionStorage {
		t.Fatalf("explicit false session policy = %#v, want maintenance disabled", got)
	}
}

// TestRunMaintenanceCronJobHonorsDisabledSessionStoragePolicy covers the stale
// job case for the new job: a persisted row must not prune anything when
// configuration has switched session maintenance off.
func TestRunMaintenanceCronJobHonorsDisabledSessionStoragePolicy(t *testing.T) {
	root, _, _ := inputTestSession(t)
	policy := DefaultMaintenancePolicy()
	policy.ReclaimSessionStorage = false

	handled, response, err := RunMaintenanceCronJob(context.Background(), root, MaintenanceSessionStorageJobID(), policy)
	if err != nil || !handled {
		t.Fatalf("disabled session maintenance run = handled:%v err:%v", handled, err)
	}
	if !strings.Contains(response, "disabled") {
		t.Fatalf("response = %q, want an explicit skip reason", response)
	}
}

// TestRunMaintenanceCronJobMaintainsSessionStorage exercises the success path
// end to end: with the default policy nothing is pruned, and the response
// explicitly reports that the transcript was preserved.
func TestRunMaintenanceCronJobMaintainsSessionStorage(t *testing.T) {
	root, _, _ := inputTestSession(t)

	handled, response, err := RunMaintenanceCronJob(context.Background(), root, MaintenanceSessionStorageJobID(), DefaultMaintenancePolicy())
	if err != nil || !handled {
		t.Fatalf("session maintenance run = handled:%v err:%v", handled, err)
	}
	for _, want := range []string{"pruned 0 tool-execution records", "retention disabled", "preserved all", "backfilled 0 session search indexes", "projected 0 session list rows"} {
		if !strings.Contains(response, want) {
			t.Fatalf("response = %q, want %q", response, want)
		}
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
