package agentruntime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/config"
)

// MaintenancePolicy is the resolved intent for Runtime-owned maintenance work.
// It is produced once from configuration (`MaintenancePolicyFromSettings`) and
// then carried by whoever schedules or executes the work, so no layer decides
// separately whether maintenance runs or how often.
type MaintenancePolicy struct {
	// ReclaimAttachmentStorage gates the private attachment-store reconciliation.
	ReclaimAttachmentStorage bool
	// StorageReconcileSchedule is the schedule expression the scheduler should
	// project for that reconciliation. It is validated where the schedule grammar
	// lives (internal/cron), not here.
	StorageReconcileSchedule string
}

// DefaultMaintenancePolicy is what applies when configuration says nothing: daily
// attachment storage reclamation.
func DefaultMaintenancePolicy() MaintenancePolicy {
	return MaintenancePolicy{
		ReclaimAttachmentStorage: true,
		StorageReconcileSchedule: MaintenanceStorageReconcileSchedule,
	}
}

// MaintenancePolicyFromSettings resolves maintenance configuration into one
// policy. A nil settings object is not an error: it means every default.
func MaintenancePolicyFromSettings(settings *config.Settings) MaintenancePolicy {
	policy := DefaultMaintenancePolicy()
	if settings == nil {
		return policy
	}
	policy.ReclaimAttachmentStorage = settings.IsAttachmentStorageReclaimEnabled()
	if schedule := settings.AttachmentStorageReclaimSchedule(); schedule != "" {
		policy.StorageReconcileSchedule = schedule
	}
	return policy
}

// MaintenanceCronJobPrefix namespaces Runtime-owned maintenance jobs inside the
// shared cron store. The scheduler claims and completes them exactly like any
// other job (same claim, stale-recovery, status, and next-run lifecycle), but
// their work is executed by the Runtime instead of an agent prompt, so no
// adapter can turn maintenance into a scheduled model run.
//
// These jobs carry no session ID, which keeps them out of every session-scoped
// cron listing and out of the cron tool: they are host maintenance, not user
// tasks the front-ends should offer to edit.
const MaintenanceCronJobPrefix = "mothx-maintenance:"

const (
	// MaintenanceStorageReconcileSchedule reclaims unreferenced attachment
	// storage once a day. Reclamation is already age-bounded by the attachment
	// retention window, so a daily pass is enough to keep a long-running host
	// from accumulating bytes it can no longer address, and frequent passes would
	// only add directory walks.
	MaintenanceStorageReconcileSchedule = "@daily"
	// MaintenanceStorageReconcileJobName labels the projected job for logs and
	// direct store inspection.
	MaintenanceStorageReconcileJobName = "Reclaim unreferenced attachment storage"
)

// MaintenanceStorageReconcileJobID is the stable identity of the attachment
// storage reconciliation job.
func MaintenanceStorageReconcileJobID() string {
	return MaintenanceCronJobPrefix + "artifact-storage"
}

// IsMaintenanceCronJobID reports whether a persisted job belongs to the
// maintenance namespace and therefore must be executed by RunMaintenanceCronJob.
func IsMaintenanceCronJobID(jobID string) bool {
	return strings.HasPrefix(strings.TrimSpace(jobID), MaintenanceCronJobPrefix)
}

// RunMaintenanceCronJob executes one namespaced maintenance job against a
// session directory and reports the outcome as a cron job response.
//
// It always claims any ID inside the maintenance namespace, including ones it
// does not recognize: an unknown maintenance job must fail with a status instead
// of falling through to the ordinary agent path, where its prompt would be run
// as a model turn. handled is false only for job IDs outside the namespace, so a
// shared scheduler can keep routing its other work.
func RunMaintenanceCronJob(ctx context.Context, sessionDir, jobID string, policy MaintenancePolicy) (bool, string, error) {
	jobID = strings.TrimSpace(jobID)
	if !IsMaintenanceCronJobID(jobID) {
		return false, "", nil
	}
	if strings.TrimSpace(sessionDir) == "" {
		return true, "", fmt.Errorf("maintenance job %s requires a session directory", jobID)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if jobID != MaintenanceStorageReconcileJobID() {
		return true, "", fmt.Errorf("unknown maintenance job %q", jobID)
	}
	if !policy.ReclaimAttachmentStorage {
		// The scheduler normally removes the job when maintenance is disabled, but a
		// row persisted by an older process must still not reclaim anything.
		return true, "attachment storage reclamation is disabled by configuration", nil
	}

	reconciliation, err := ReconcileArtifactStorage(ctx, sessionDir, DefaultAttachmentPolicy(), time.Now().UTC())
	if err != nil {
		return true, "", err
	}
	return true, fmt.Sprintf(
		"reclaimed %d unreferenced attachment directories (%d bytes) older than %s; kept %d too recent, %d referenced, %d unrecognized",
		reconciliation.Removed, reconciliation.Freed, reconciliation.AgeFloor.Format(time.RFC3339),
		reconciliation.SkippedYoung, reconciliation.SkippedReferenced, reconciliation.SkippedUnrecognized,
	), nil
}
