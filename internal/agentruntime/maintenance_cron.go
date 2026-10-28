package agentruntime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/session"
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
	// ReclaimSessionStorage gates Runtime-owned session-storage maintenance.
	ReclaimSessionStorage bool
	// SessionStorageSchedule is the schedule expression for that maintenance.
	SessionStorageSchedule string
	// ToolExecutionRetention bounds completed/abandoned tool-execution
	// idempotency records. Zero keeps them forever. Transcript messages are
	// never governed by this field.
	ToolExecutionRetention time.Duration
	// VacuumSessionStorage opts into VACUUM after pruning.
	VacuumSessionStorage bool
}

// DefaultMaintenancePolicy is what applies when configuration says nothing: daily
// attachment storage reclamation.
func DefaultMaintenancePolicy() MaintenancePolicy {
	return MaintenancePolicy{
		ReclaimAttachmentStorage: true,
		StorageReconcileSchedule: MaintenanceStorageReconcileSchedule,
		ReclaimSessionStorage:    true,
		SessionStorageSchedule:   MaintenanceSessionStorageSchedule,
		ToolExecutionRetention:   0,
		VacuumSessionStorage:     false,
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
	policy.ReclaimSessionStorage = settings.IsSessionStorageReclaimEnabled()
	if schedule := settings.SessionStorageReclaimSchedule(); schedule != "" {
		policy.SessionStorageSchedule = schedule
	}
	policy.ToolExecutionRetention = time.Duration(settings.ToolExecutionRetentionDays()) * 24 * time.Hour
	policy.VacuumSessionStorage = settings.IsSessionStorageVacuumEnabled()
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

	// MaintenanceSessionStorageSchedule runs session-storage maintenance once a
	// day. With the default retention of 0 it only runs PRAGMA optimize and
	// reports storage; pruning and VACUUM are opt-in.
	MaintenanceSessionStorageSchedule = "@daily"
	// MaintenanceSessionStorageJobName labels the projected job.
	MaintenanceSessionStorageJobName = "Maintain session storage"
)

// MaintenanceStorageReconcileJobID is the stable identity of the attachment
// storage reconciliation job.
func MaintenanceStorageReconcileJobID() string {
	return MaintenanceCronJobPrefix + "artifact-storage"
}

// MaintenanceSessionStorageJobID is the stable identity of the session-storage
// maintenance job.
func MaintenanceSessionStorageJobID() string {
	return MaintenanceCronJobPrefix + "session-storage"
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
	switch jobID {
	case MaintenanceStorageReconcileJobID():
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
	case MaintenanceSessionStorageJobID():
		if !policy.ReclaimSessionStorage {
			return true, "session storage maintenance is disabled by configuration", nil
		}
		result, err := session.MaintainSessionStorage(ctx, sessionDir, session.SessionStoragePolicy{
			ToolExecutionRetention: policy.ToolExecutionRetention,
			Vacuum:                 policy.VacuumSessionStorage,
		}, time.Now().UTC())
		if err != nil {
			return true, "", err
		}
		backfill, err := session.BackfillSessionSearchIndex(ctx, sessionDir, session.DefaultSearchIndexBackfillBatch)
		if err != nil {
			return true, "", err
		}
		backfillResult := fmt.Sprintf("backfilled %d session search indexes (%d text rows, %d skipped)", backfill.Sessions, backfill.Rows, backfill.Failed)
		if backfill.More {
			backfillResult += "; more remain for the next run"
		}
		displayBackfill, err := session.BackfillSessionDisplayProjections(ctx, sessionDir, session.DefaultDisplayProjectionBackfillBatch)
		if err != nil {
			return true, "", err
		}
		displayResult := fmt.Sprintf("projected %d session list rows (%d skipped, %d failed)", displayBackfill.Sessions, displayBackfill.Skipped, displayBackfill.Failed)
		if displayBackfill.More {
			displayResult += "; more remain for the next run"
		}
		retention := "retention disabled; every tool-execution record is kept"
		if policy.ToolExecutionRetention > 0 {
			retention = fmt.Sprintf("tool-execution retention %s", policy.ToolExecutionRetention)
		}
		vacuum := "vacuum disabled"
		switch {
		case result.Vacuumed:
			vacuum = "vacuumed"
		case policy.VacuumSessionStorage:
			vacuum = "nothing to vacuum"
		}
		return true, fmt.Sprintf(
			"pruned %d tool-execution records (%s); preserved all %d transcript entries and %d sub-agent entries; %d tool records remain, %d free pages of %d; %s; %s; %s",
			result.PrunedToolRecords, retention, result.Entries, result.SubEntries,
			result.ToolRecords, result.FreelistPages, result.PageCount, vacuum, backfillResult, displayResult,
		), nil
	default:
		return true, "", fmt.Errorf("unknown maintenance job %q", jobID)
	}
}
