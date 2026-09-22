package cron

import (
	"log"
	"strings"

	"github.com/oschina/mothx/internal/agentruntime"
)

// SetMaintenancePolicy installs the resolved Runtime maintenance policy this
// scheduler projects and executes. Callers that never set one keep the defaults,
// so maintenance stays available to every entry point without each of them
// having to opt in.
func (s *Scheduler) SetMaintenancePolicy(policy agentruntime.MaintenancePolicy) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maintenance = &policy
}

// maintenancePolicy returns the installed policy or the Runtime default.
func (s *Scheduler) maintenancePolicy() agentruntime.MaintenancePolicy {
	if s != nil {
		s.mu.Lock()
		policy := s.maintenance
		s.mu.Unlock()
		if policy != nil {
			return *policy
		}
	}
	return agentruntime.DefaultMaintenancePolicy()
}

// ensureMaintenanceJob projects the Runtime-owned maintenance policy onto this
// scheduler's own store: enabled means the job exists on the configured cadence,
// disabled means it is gone. Cron stays the sole owner of job lifecycle (claims,
// stale recovery, status, next-run), the Runtime owns what the job does, and no
// adapter has to know maintenance exists.
//
// An existing job keeps its run history, counters, and enabled flag; only its
// schedule follows configuration. That keeps a paused job paused (an operator
// choice) while making settings authoritative for cadence and for turning the
// whole projection off.
func (s *Scheduler) ensureMaintenanceJob() {
	if s == nil || s.store == nil {
		return
	}
	policy := s.maintenancePolicy()
	id := agentruntime.MaintenanceStorageReconcileJobID()
	existing, err := s.store.Get(id)
	if !policy.ReclaimAttachmentStorage {
		if err == nil {
			if deleteErr := s.store.Delete(id); deleteErr != nil && !isMissingCronJobError(deleteErr) {
				log.Printf("[cron] remove disabled maintenance job: %v", deleteErr)
			}
		} else if !isMissingCronJobError(err) {
			log.Printf("[cron] inspect maintenance job: %v", err)
		}
		return
	}
	if s.sessionDir == "" {
		// Without a session directory there is no private store to reconcile, and
		// scheduling a job that could only fail would pollute the shared store.
		return
	}
	schedule := strings.TrimSpace(policy.StorageReconcileSchedule)
	if schedule == "" {
		schedule = agentruntime.MaintenanceStorageReconcileSchedule
	}
	if err == nil {
		if existing.Schedule == schedule {
			return
		}
		updated := *existing
		if !normalizeMaintenanceSchedule(&updated, schedule) {
			return
		}
		if updateErr := s.store.Update(updated); updateErr != nil {
			log.Printf("[cron] update maintenance schedule: %v", updateErr)
		}
		return
	}
	if !isMissingCronJobError(err) {
		log.Printf("[cron] inspect maintenance job: %v", err)
		return
	}
	job := CronJob{
		ID:       id,
		Name:     agentruntime.MaintenanceStorageReconcileJobName,
		Prompt:   "Runtime-owned maintenance; never executed as an agent prompt.",
		Schedule: schedule,
		Mode:     "yolo",
		Enabled:  true,
	}
	if !normalizeMaintenanceSchedule(&job, schedule) {
		return
	}
	if _, createErr := s.store.Create(job); createErr != nil {
		// Several processes sharing one store can race to create it; the loser
		// sees the job already scheduled.
		log.Printf("[cron] project maintenance job: %v", createErr)
	}
}

// normalizeMaintenanceSchedule stamps the next run for the requested cadence and
// falls back to the Runtime default when the configured value cannot be parsed.
// The schedule grammar belongs to this package, so an invalid setting is reported
// and corrected here instead of being validated a second time elsewhere.
func normalizeMaintenanceSchedule(job *CronJob, schedule string) bool {
	job.Schedule = schedule
	if err := NormalizeJobSchedule(job); err == nil {
		return true
	} else if schedule != agentruntime.MaintenanceStorageReconcileSchedule {
		log.Printf("[cron] invalid maintenance schedule %q (%v); using %s", schedule, err, agentruntime.MaintenanceStorageReconcileSchedule)
		job.Schedule = agentruntime.MaintenanceStorageReconcileSchedule
		return NormalizeJobSchedule(job) == nil
	}
	return false
}

// UserVisibleJobs removes Runtime-owned maintenance jobs from a job list headed
// for a user-facing surface (the cron tool, the serve API, and ACP management).
// Those jobs are host storage housekeeping: they are scheduled, claimed, and
// completed through the same lifecycle as any other job, but they are not
// automation tasks the user authored, their prompt is not a prompt, and a front-
// end that rendered them would offer edit/delete controls for something the user
// cannot meaningfully evaluate. Filtering the projection keeps the scheduler's
// authoritative store untouched.
func UserVisibleJobs(jobs []CronJob) []CronJob {
	visible := make([]CronJob, 0, len(jobs))
	for _, job := range jobs {
		if agentruntime.IsMaintenanceCronJobID(job.ID) {
			continue
		}
		visible = append(visible, job)
	}
	return visible
}

// isMissingCronJobError recognizes the package's own not-found wording. The
// stores report a missing job as a formatted error rather than a sentinel, and
// maintenance must not delete or recreate on an unrelated store failure.
func isMissingCronJobError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not found")
}
