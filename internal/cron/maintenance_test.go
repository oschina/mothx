package cron

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/session"
)

// TestSchedulerStartProjectsMaintenanceJobOnce proves the maintenance projection
// is created by the shared scheduler alone (no adapter wiring), is scheduled
// rather than one-shot, carries no session identity so it stays out of every
// session-scoped cron listing, and is not duplicated by a second process sharing
// one store.
func TestSchedulerStartProjectsMaintenanceJobOnce(t *testing.T) {
	sessionDir := t.TempDir()
	first := NewSchedulerWithSessionDir(NewSQLiteCronStore(sessionDir), nil, time.Hour, sessionDir)
	first.Start()
	second := NewSchedulerWithSessionDir(NewSQLiteCronStore(sessionDir), nil, time.Hour, sessionDir)
	second.Start()
	t.Cleanup(func() {
		first.Stop()
		second.Stop()
		_ = session.CloseDatabases()
	})

	jobs, err := NewSQLiteCronStore(sessionDir).List()
	if err != nil {
		t.Fatal(err)
	}
	var maintenance []CronJob
	for _, job := range jobs {
		if agentruntime.IsMaintenanceCronJobID(job.ID) {
			maintenance = append(maintenance, job)
		}
	}
	if len(maintenance) != 1 {
		t.Fatalf("maintenance jobs = %#v, want exactly one across two schedulers on one store", maintenance)
	}
	job := maintenance[0]
	if job.ID != agentruntime.MaintenanceStorageReconcileJobID() || job.Schedule != agentruntime.MaintenanceStorageReconcileSchedule {
		t.Fatalf("maintenance job = %#v, want the Runtime-owned identity and cadence", job)
	}
	if !job.Enabled || job.OneShot {
		t.Fatalf("maintenance job = %#v, want a repeating enabled job", job)
	}
	if job.SessionID != "" {
		t.Fatalf("maintenance job session = %q, want no session identity so it stays out of user listings", job.SessionID)
	}
	if job.NextRun.IsZero() || !job.NextRun.After(time.Now()) {
		t.Fatalf("maintenance job next run = %s, want a future scheduled run", job.NextRun)
	}
}

// TestMaintenanceJobCompletesThroughTheRuntimeNotAnAgent is the end-to-end proof
// that maintenance reuses cron's lifecycle without borrowing its execution: the
// store has no manager and the adapter handler is never consulted, yet the job
// runs the Runtime reconciliation, reclaims the aged directory, and completes
// with the normal success status and next run.
func TestMaintenanceJobCompletesThroughTheRuntimeNotAnAgent(t *testing.T) {
	sessionDir := t.TempDir()
	aged := writeMaintenanceArtifactDirectory(t, sessionDir, "0123456789abcdef", 30*24*time.Hour, "stale bytes")

	store := NewSQLiteCronStore(sessionDir)
	handlerCalls := make(chan CronJob, 1)
	scheduler := NewSchedulerWithSessionDirAndHandler(store, nil, time.Hour, sessionDir, func(_ context.Context, job CronJob) (bool, string, error) {
		handlerCalls <- job
		return false, "", nil
	})
	scheduler.Start()
	t.Cleanup(func() {
		scheduler.Stop()
		_ = session.CloseDatabases()
	})

	if err := scheduler.RunNow(agentruntime.MaintenanceStorageReconcileJobID()); err != nil {
		t.Fatal(err)
	}
	completed := waitForJobStatus(t, store, agentruntime.MaintenanceStorageReconcileJobID(), "success")
	if completed.LastError != "" || completed.RunCount != 1 {
		t.Fatalf("completed maintenance job = %#v, want a clean first run", completed)
	}
	if _, err := os.Stat(aged); !os.IsNotExist(err) {
		t.Fatalf("the scheduled reconciliation did not reclaim %s: %v", aged, err)
	}
	select {
	case job := <-handlerCalls:
		t.Fatalf("maintenance reached the adapter cron handler: %#v", job)
	default:
	}
}

// TestUnknownMaintenanceJobCannotRunItsPrompt covers the refusal path: a job in
// the maintenance namespace that the Runtime does not implement must fail with a
// status, because falling through would execute its prompt as a model turn with
// the job's mode and working directory.
func TestUnknownMaintenanceJobCannotRunItsPrompt(t *testing.T) {
	sessionDir := t.TempDir()
	store := NewSQLiteCronStore(sessionDir)
	id := agentruntime.MaintenanceCronJobPrefix + "not-implemented"
	job := CronJob{ID: id, Name: "not implemented", Prompt: "summarize my sessions", Schedule: "@hourly", Mode: "yolo", Enabled: true}
	if err := NormalizeJobSchedule(&job); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(job); err != nil {
		t.Fatal(err)
	}
	scheduler := NewSchedulerWithSessionDir(store, nil, time.Hour, sessionDir)
	t.Cleanup(func() {
		scheduler.Stop()
		_ = session.CloseDatabases()
	})

	if err := scheduler.RunNow(id); err != nil {
		t.Fatal(err)
	}
	failed := waitForJobStatus(t, store, id, "failed")
	if !strings.Contains(failed.LastError, "unknown maintenance job") {
		t.Fatalf("failed maintenance job = %#v, want the unknown-job refusal recorded", failed)
	}
}

// TestUserVisibleJobsHidesMaintenanceFromEveryUserSurface is the projection
// contract: the scheduler stores and runs the maintenance job, but the cron tool
// listing, name resolution, and the management listings must not render or act on
// it as if it were a user automation.
func TestUserVisibleJobsHidesMaintenanceFromEveryUserSurface(t *testing.T) {
	sessionDir := t.TempDir()
	store := NewSQLiteCronStore(sessionDir)
	if _, err := store.Create(CronJob{ID: "cron-user", Name: "nightly digest", Prompt: "summarize", Schedule: "@daily", Mode: "yolo", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	scheduler := NewSchedulerWithSessionDir(store, nil, time.Hour, sessionDir)
	scheduler.Start()
	t.Cleanup(func() {
		scheduler.Stop()
		_ = session.CloseDatabases()
	})

	all, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("stored jobs = %#v, want the user job plus the projected maintenance job", all)
	}

	tool := NewCronTool(store, scheduler)
	result, err := tool.Execute(context.Background(), map[string]any{"action": "list"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Text, "nightly digest") {
		t.Fatalf("tool listing = %q, want the user job", result.Text)
	}
	if strings.Contains(result.Text, agentruntime.MaintenanceStorageReconcileJobName) ||
		strings.Contains(result.Text, agentruntime.MaintenanceCronJobPrefix) {
		t.Fatalf("tool listing exposed maintenance: %q", result.Text)
	}

	if _, err := tool.findJob("", agentruntime.MaintenanceStorageReconcileJobName); err == nil {
		t.Fatal("a name lookup resolved a maintenance job; its result feeds delete/disable")
	}
}

// TestDisabledMaintenancePolicyRemovesTheProjection covers both directions of
// the settings switch: a scheduler started while maintenance is off creates
// nothing, and one started after it was turned off removes its own job without
// touching anything else in the shared store.
func TestDisabledMaintenancePolicyRemovesTheProjection(t *testing.T) {
	sessionDir := t.TempDir()
	store := NewSQLiteCronStore(sessionDir)
	if _, err := store.Create(CronJob{ID: "cron-keep", Name: "keep me", Prompt: "digest", Schedule: "@daily", Mode: "yolo", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	off := NewSchedulerWithSessionDir(store, nil, time.Hour, sessionDir)
	off.SetMaintenancePolicy(agentruntime.MaintenancePolicy{})
	off.Start()
	if _, err := store.Get(agentruntime.MaintenanceStorageReconcileJobID()); err == nil {
		t.Fatal("a disabled policy still projected the maintenance job")
	}
	off.Stop()

	on := NewSchedulerWithSessionDir(store, nil, time.Hour, sessionDir)
	on.SetMaintenancePolicy(agentruntime.DefaultMaintenancePolicy())
	on.Start()
	if _, err := store.Get(agentruntime.MaintenanceStorageReconcileJobID()); err != nil {
		t.Fatalf("enabled policy did not project the maintenance job: %v", err)
	}
	on.Stop()

	restarted := NewSchedulerWithSessionDir(store, nil, time.Hour, sessionDir)
	restarted.SetMaintenancePolicy(agentruntime.MaintenancePolicy{})
	restarted.Start()
	if _, err := store.Get(agentruntime.MaintenanceStorageReconcileJobID()); err == nil {
		t.Fatal("turning maintenance off left the scheduled job behind")
	}
	if _, err := store.Get("cron-keep"); err != nil {
		t.Fatalf("removing maintenance disturbed an unrelated job: %v", err)
	}
	restarted.Stop()
	_ = session.CloseDatabases()
}

// TestMaintenanceScheduleOverrideKeepsRunHistory makes settings authoritative for
// cadence without pretending the job never ran: only the schedule and its next run
// move, so counters and status survive a settings edit.
func TestMaintenanceScheduleOverrideKeepsRunHistory(t *testing.T) {
	sessionDir := t.TempDir()
	store := NewSQLiteCronStore(sessionDir)
	runAt := time.Now().UTC().Add(-2 * time.Hour)
	stored := CronJob{
		ID:       agentruntime.MaintenanceStorageReconcileJobID(),
		Name:     agentruntime.MaintenanceStorageReconcileJobName,
		Prompt:   "Runtime-owned maintenance; never executed as an agent prompt.",
		Schedule: "@daily", Mode: "yolo", Enabled: true,
		CreatedAt: runAt, LastRun: runAt, RunCount: 3, LastStatus: "success",
	}
	if err := NormalizeJobSchedule(&stored); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(stored); err != nil {
		t.Fatal(err)
	}

	scheduler := NewSchedulerWithSessionDir(store, nil, time.Hour, sessionDir)
	scheduler.SetMaintenancePolicy(agentruntime.MaintenancePolicy{ReclaimAttachmentStorage: true, StorageReconcileSchedule: "@every 6h"})
	scheduler.Start()
	defer scheduler.Stop()

	job, err := store.Get(agentruntime.MaintenanceStorageReconcileJobID())
	if err != nil {
		t.Fatal(err)
	}
	if job.Schedule != "@every 6h" || job.NextRun.IsZero() {
		t.Fatalf("maintenance job after a cadence change = %#v, want the configured schedule with a next run", job)
	}
	if job.RunCount != 3 || job.LastStatus != "success" || !job.LastRun.Equal(runAt.UTC()) || !job.CreatedAt.Equal(runAt.UTC()) {
		t.Fatalf("cadence change rewrote run history: %#v", job)
	}
	_ = session.CloseDatabases()
}

// TestInvalidMaintenanceScheduleFallsBackToDefault keeps a typo in settings from
// silently disabling housekeeping: the scheduler that owns the schedule grammar
// corrects it to the Runtime default.
func TestInvalidMaintenanceScheduleFallsBackToDefault(t *testing.T) {
	sessionDir := t.TempDir()
	store := NewSQLiteCronStore(sessionDir)
	scheduler := NewSchedulerWithSessionDir(store, nil, time.Hour, sessionDir)
	scheduler.SetMaintenancePolicy(agentruntime.MaintenancePolicy{ReclaimAttachmentStorage: true, StorageReconcileSchedule: "@every soon"})
	scheduler.Start()
	defer scheduler.Stop()

	job, err := store.Get(agentruntime.MaintenanceStorageReconcileJobID())
	if err != nil {
		t.Fatalf("an invalid cadence left no maintenance job at all: %v", err)
	}
	if job.Schedule != agentruntime.MaintenanceStorageReconcileSchedule {
		t.Fatalf("maintenance schedule = %q, want the Runtime default fallback", job.Schedule)
	}
	_ = session.CloseDatabases()
}

func waitForJobStatus(t *testing.T, store *SQLiteCronStore, id, status string) CronJob {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		job, err := store.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if job.LastStatus == status {
			return *job
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s never reached status %q: %#v", id, status, job)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// writeMaintenanceArtifactDirectory plants attachment-looking storage aged by the
// given duration, using the same layout acceptArtifact writes.
func writeMaintenanceArtifactDirectory(t *testing.T, sessionDir, id string, age time.Duration, content string) string {
	t.Helper()
	dir := filepath.Join(sessionDir, "artifacts", id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "content")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-age)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(dir, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	return path
}
