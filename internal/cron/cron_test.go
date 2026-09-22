package cron

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	agentimpl "github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/config"
	ctxpkg "github.com/oschina/mothx/internal/context"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

func TestSchedulerHandlerUsesCanonicalCronCompletionLifecycle(t *testing.T) {
	store := NewSQLiteCronStore(t.TempDir())
	if _, err := store.Create(CronJob{ID: "maintenance", Name: "maintenance", Prompt: "ignored", Schedule: "@hourly", Mode: "yolo", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	called := make(chan CronJob, 1)
	scheduler := NewSchedulerWithSessionDirAndHandler(store, nil, time.Hour, "", func(ctx context.Context, job CronJob) (bool, string, error) {
		called <- job
		return true, "maintained", nil
	})
	if err := scheduler.RunNow("maintenance"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-called:
		if got.ID != "maintenance" {
			t.Fatalf("handler job = %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("custom cron handler was not invoked")
	}
	deadline := time.Now().Add(time.Second)
	for {
		job, err := store.Get("maintenance")
		if err != nil {
			t.Fatal(err)
		}
		if job.LastStatus == "success" {
			if job.RunCount != 1 || job.NextRun.IsZero() {
				t.Fatalf("completed job = %#v", job)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("handler completion was not persisted: %#v", job)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSQLiteCronStoreCreate(t *testing.T) {
	tmp := t.TempDir()
	store := NewSQLiteCronStore(tmp)

	job, err := store.Create(CronJob{
		Name:     "test job",
		Prompt:   "do something",
		Schedule: "0 9 * * *",
		Mode:     "agent",
		Enabled:  true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if job.ID == "" {
		t.Error("expected non-empty ID")
	}
	if job.Name != "test job" {
		t.Errorf("expected 'test job', got %q", job.Name)
	}
	if job.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
}

func TestSQLiteCronStoreCreateDuplicate(t *testing.T) {
	tmp := t.TempDir()
	store := NewSQLiteCronStore(tmp)

	store.Create(CronJob{ID: "j1", Name: "first"})
	_, err := store.Create(CronJob{ID: "j1", Name: "duplicate"})
	if err == nil {
		t.Fatal("expected error for duplicate ID")
	}
}

func TestNewCronIDConcurrentUnique(t *testing.T) {
	const count = 500
	var wg sync.WaitGroup
	ids := make(chan string, count)

	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids <- newCronID()
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[string]bool, count)
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate id: %s", id)
		}
		seen[id] = true
	}
}

func TestSQLiteCronStoreList(t *testing.T) {
	tmp := t.TempDir()
	store := NewSQLiteCronStore(tmp)

	store.Create(CronJob{Name: "job1"})
	store.Create(CronJob{Name: "job2"})
	store.Create(CronJob{Name: "job3"})

	jobs, err := store.List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(jobs) != 3 {
		t.Errorf("expected 3 jobs, got %d", len(jobs))
	}
}

func TestSQLiteCronStoreGet(t *testing.T) {
	tmp := t.TempDir()
	store := NewSQLiteCronStore(tmp)

	created, _ := store.Create(CronJob{ID: "j1", Name: "test"})

	got, err := store.Get("j1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != created.Name {
		t.Errorf("expected %q, got %q", created.Name, got.Name)
	}
}

func TestSQLiteCronStoreGetNotFound(t *testing.T) {
	tmp := t.TempDir()
	store := NewSQLiteCronStore(tmp)

	_, err := store.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSQLiteCronStoreUpdate(t *testing.T) {
	tmp := t.TempDir()
	store := NewSQLiteCronStore(tmp)

	store.Create(CronJob{ID: "j1", Name: "original"})

	job, _ := store.Get("j1")
	job.Name = "updated"
	job.RunCount = 5
	if err := store.Update(*job); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, _ := store.Get("j1")
	if got.Name != "updated" {
		t.Errorf("expected 'updated', got %q", got.Name)
	}
	if got.RunCount != 5 {
		t.Errorf("expected RunCount=5, got %d", got.RunCount)
	}
}

func TestSQLiteCronStoreUpdateNotFound(t *testing.T) {
	tmp := t.TempDir()
	store := NewSQLiteCronStore(tmp)

	err := store.Update(CronJob{ID: "nonexistent"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSQLiteCronStoreDelete(t *testing.T) {
	tmp := t.TempDir()
	store := NewSQLiteCronStore(tmp)

	store.Create(CronJob{ID: "j1", Name: "to delete"})

	if err := store.Delete("j1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err := store.Get("j1")
	if err == nil {
		t.Fatal("expected error after deletion")
	}
}

func TestSQLiteCronStoreDeleteNotFound(t *testing.T) {
	tmp := t.TempDir()
	store := NewSQLiteCronStore(tmp)

	err := store.Delete("nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSQLiteCronStoreClaimDueIsAtomic(t *testing.T) {
	store := NewSQLiteCronStore(t.TempDir())
	if _, err := store.Create(CronJob{ID: "due", Name: "due", Enabled: true}); err != nil {
		t.Fatalf("create job: %v", err)
	}

	const contenders = 2
	var wg sync.WaitGroup
	claimed := make(chan bool, contenders)
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := store.ClaimDue("due", time.Now())
			if err != nil {
				t.Errorf("claim due: %v", err)
				return
			}
			claimed <- ok
		}()
	}
	wg.Wait()
	close(claimed)

	count := 0
	for ok := range claimed {
		if ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("claims = %d, want 1", count)
	}
}

func TestSQLiteCronStoreClaimDueHonorsFutureNextRun(t *testing.T) {
	store := NewSQLiteCronStore(t.TempDir())
	future := time.Now().Add(time.Hour)
	if _, err := store.Create(CronJob{ID: "future", Enabled: true, Schedule: "@hourly", NextRun: future}); err != nil {
		t.Fatalf("create job: %v", err)
	}
	claimed, err := store.ClaimDue("future", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("future job was claimed before NextRun")
	}
}

func TestSQLiteCronStoreClaimDueReclaimsStaleRunning(t *testing.T) {
	store := NewSQLiteCronStore(t.TempDir())
	old := time.Now().Add(-runningLeaseTimeout - time.Minute)
	if _, err := store.Create(CronJob{ID: "stale", Enabled: true, LastRun: old, NextRun: old.Add(time.Hour), LastStatus: "running"}); err != nil {
		t.Fatalf("create job: %v", err)
	}
	claimed, err := store.ClaimDue("stale", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("stale running job was not reclaimed")
	}
}

func TestSQLiteCronStorePersistence(t *testing.T) {
	tmp := t.TempDir()

	store1 := NewSQLiteCronStore(tmp)
	store1.Create(CronJob{ID: "j1", Name: "persistent", Prompt: "test"})

	// Create a new store from the same sessions.db root.
	store2 := NewSQLiteCronStore(tmp)
	got, err := store2.Get("j1")
	if err != nil {
		t.Fatalf("expected job to persist, got error: %v", err)
	}
	if got.Name != "persistent" {
		t.Errorf("expected 'persistent', got %q", got.Name)
	}
}

// --- Scheduler tests ---

func TestSchedulerStartStop(t *testing.T) {
	tmp := t.TempDir()
	store := NewSQLiteCronStore(tmp)

	// Create a mock manager (nil factory is ok for basic lifecycle tests)
	sched := NewScheduler(store, nil, 1*time.Second)

	if sched.IsRunning() {
		t.Error("expected not running initially")
	}

	sched.Start()
	if !sched.IsRunning() {
		t.Error("expected running after start")
	}

	// Double start should be no-op
	sched.Start()

	sched.Stop()
	if sched.IsRunning() {
		t.Error("expected not running after stop")
	}

	// Double stop should be no-op
	sched.Stop()
}

func TestSchedulerConcurrentStartStop(t *testing.T) {
	sched := NewScheduler(NewSQLiteCronStore(t.TempDir()), nil, time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			sched.Start()
		}()
		go func() {
			defer wg.Done()
			sched.Stop()
		}()
	}
	wg.Wait()
	sched.Stop()
	if sched.IsRunning() {
		t.Fatal("scheduler still running after concurrent start/stop")
	}
}

func TestSchedulerDefaultInterval(t *testing.T) {
	tmp := t.TempDir()
	store := NewSQLiteCronStore(tmp)
	sched := NewScheduler(store, nil, 0)

	if sched.interval != 30*time.Second {
		t.Errorf("expected 30s default interval, got %v", sched.interval)
	}
}

func TestSchedulerCompletionObserver(t *testing.T) {
	sched := NewScheduler(nil, nil, time.Second)
	called := make(chan struct {
		sessionID string
		response  string
		err       error
	}, 1)
	sched.SetCompletionObserver(func(sessionID, response string, err error) {
		called <- struct {
			sessionID string
			response  string
			err       error
		}{sessionID, response, err}
	})
	sched.notifyCompletion("session-1", "result", nil)

	select {
	case got := <-called:
		if got.sessionID != "session-1" || got.response != "result" || got.err != nil {
			t.Fatalf("unexpected completion: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("completion observer was not called")
	}
}

func TestSchedulerLocalJobWaitsForSessionRuntimeLock(t *testing.T) {
	tmp := t.TempDir()
	mgr := session.New(tmp, tmp)
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	sessionID := mgr.GetHeader().ID
	store := NewSQLiteCronStore(tmp)
	job, err := store.Create(CronJob{
		ID:        "job-lock",
		SessionID: sessionID,
		Prompt:    "run once",
		Enabled:   true,
		OneShot:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	model := &provider.Model{ID: "mock-model", Name: "Mock", ContextWindow: 100000}
	mock := provider.NewMockProvider("mock", []*provider.Model{model}, []provider.StreamEvent{
		{Type: provider.StreamTextDelta, TextDelta: "done"},
		{Type: provider.StreamDone, StopReason: "stop"},
	})
	settings := config.DefaultSettings()
	settings.SessionDir = tmp
	factory := agentimpl.NewAgentFactory(mock, model, settings, nil, "", "", nil, ctxpkg.CompactionSettings{Enabled: false}, nil)
	sched := NewSchedulerWithSessionDir(store, agentimpl.NewAgentManager(factory), time.Second, tmp)
	completed := make(chan struct{}, 1)
	sched.SetCompletionObserver(func(string, string, error) { completed <- struct{}{} })

	release := session.LockRuntime(tmp, job.SessionID)
	go sched.executeJob(*job)
	select {
	case <-completed:
		t.Fatal("cron job ran while the session runtime lock was held")
	case <-time.After(100 * time.Millisecond):
	}
	release()

	// Starting a local agent also initializes the session runtime and SQLite
	// stores. Under the race detector that setup can take longer than two
	// seconds on a busy CI runner, so keep the assertion focused on eventual
	// execution after the lock is released rather than imposing a tight limit.
	select {
	case <-completed:
	case <-time.After(10 * time.Second):
		t.Fatal("cron job did not run after the session runtime lock was released")
	}
	if got := mock.GetCallCount(); got != 1 {
		t.Fatalf("provider calls = %d, want 1", got)
	}
	events, err := session.ListSessionRunEvents(tmp, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Source != "cron" || events[0].EventType != "started" || events[1].EventType != "finished" {
		t.Fatalf("cron run events = %#v, want cron started and finished", events)
	}
	var eventData map[string]string
	if err := json.Unmarshal(events[0].Data, &eventData); err != nil {
		t.Fatalf("decode cron event data: %v", err)
	}
	if eventData["cronJobId"] != job.ID || eventData["cronJobName"] != job.Name {
		t.Fatalf("cron event data = %#v, want job metadata", eventData)
	}
}

func TestSchedulerBoundChannelJobUsesForcedRuntimePolicy(t *testing.T) {
	sessionDir := t.TempDir()
	workDir := t.TempDir()
	bound, err := session.CreateBound(workDir, sessionDir, "wechat", "cron-policy-user")
	if err != nil {
		t.Fatalf("CreateBound: %v", err)
	}
	store := NewSQLiteCronStore(sessionDir)
	job, err := store.Create(CronJob{
		ID: "job-bound-policy", SessionID: bound.GetHeader().ID, WorkDir: workDir,
		Prompt: "run once", Mode: "agent", Enabled: true, OneShot: true,
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	model := &provider.Model{ID: "mock-model", Name: "Mock", ContextWindow: 100000}
	mock := provider.NewMockProvider("mock", []*provider.Model{model}, []provider.StreamEvent{
		{Type: provider.StreamTextDelta, TextDelta: "done"},
		{Type: provider.StreamDone, StopReason: "stop"},
	})
	settings := config.DefaultSettings()
	settings.SessionDir = sessionDir
	factory := agentimpl.NewAgentFactory(mock, model, settings, nil, "", "", nil, ctxpkg.CompactionSettings{Enabled: false}, nil)
	scheduler := NewSchedulerWithSessionDir(store, agentimpl.NewAgentManager(factory), time.Second, sessionDir)
	scheduler.executeJob(*job)

	events, err := session.ListSessionRunEvents(sessionDir, bound.GetHeader().ID)
	if err != nil {
		t.Fatalf("ListSessionRunEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("run events = %#v, want start and finish", events)
	}
	for _, event := range events {
		if event.Source != "wechat" || event.Mode != "yolo" {
			t.Fatalf("event source/mode = %q/%q, want wechat/yolo", event.Source, event.Mode)
		}
	}
}

func TestSchedulerUpdateJobPreservesExistingFields(t *testing.T) {
	tmp := t.TempDir()
	store := NewSQLiteCronStore(tmp)
	store.Create(CronJob{ID: "j1", Name: "keep name", Schedule: "@daily", Enabled: true})

	sched := NewScheduler(store, nil, time.Second)
	sched.updateJob("j1", func(job *CronJob) {
		job.LastStatus = "running"
	})

	got, err := store.Get("j1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "keep name" {
		t.Fatalf("name = %q, want keep name", got.Name)
	}
	if got.LastStatus != "running" {
		t.Fatalf("last status = %q, want running", got.LastStatus)
	}
}

func TestIsDueNeverRun(t *testing.T) {
	s := &Scheduler{}
	job := CronJob{Enabled: true}
	if !s.isDue(job, time.Now()) {
		t.Error("expected due for never-run job")
	}
}

func TestIsDueNextRunPassed(t *testing.T) {
	s := &Scheduler{}
	job := CronJob{
		Enabled: true,
		LastRun: time.Now().Add(-2 * time.Hour),
		NextRun: time.Now().Add(-1 * time.Hour),
	}
	if !s.isDue(job, time.Now()) {
		t.Error("expected due when NextRun has passed")
	}
}

func TestIsDueRecentRun(t *testing.T) {
	s := &Scheduler{}
	job := CronJob{
		Enabled: true,
		LastRun: time.Now().Add(-5 * time.Minute),
		NextRun: time.Now().Add(55 * time.Minute),
	}
	if s.isDue(job, time.Now()) {
		t.Error("expected not due for recent run with future NextRun")
	}
}

func TestIsDuePeriodicFirstRunWaitsForNextRun(t *testing.T) {
	s := &Scheduler{}
	next := time.Now().Add(time.Hour)
	job := CronJob{Enabled: true, Schedule: "@hourly", NextRun: next}
	if s.isDue(job, time.Now()) {
		t.Fatal("periodic job was due before its first NextRun")
	}
}

func TestSchedulerCreateFailureFinalizesOneShot(t *testing.T) {
	store := NewSQLiteCronStore(t.TempDir())
	job, err := store.Create(CronJob{ID: "one-shot", Enabled: true, OneShot: true})
	if err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(store, nil, time.Second)
	claimed, _, err := s.claimJob(job.ID, time.Now())
	if err != nil || !claimed {
		t.Fatalf("claim = %v, err = %v", claimed, err)
	}
	claimedJob, err := store.Get(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	claimedJob.LastStatus = "running"
	if err := store.Update(*claimedJob); err != nil {
		t.Fatal(err)
	}
	s.executeJob(*claimedJob)
	got, err := store.Get(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled || got.RunCount != 1 || got.LastStatus != "failed" {
		t.Fatalf("job after failed one-shot = %#v", got)
	}
}

func TestIsDueOldRun(t *testing.T) {
	s := &Scheduler{}
	// A job with no NextRun and already run — should NOT be due (one-shot already done)
	job := CronJob{
		Enabled: true,
		LastRun: time.Now().Add(-2 * time.Hour),
	}
	if s.isDue(job, time.Now()) {
		t.Error("expected not due — no NextRun set, one-shot already completed")
	}

	// A job with NextRun in the past — should be due
	job2 := CronJob{
		Enabled: true,
		LastRun: time.Now().Add(-2 * time.Hour),
		NextRun: time.Now().Add(-30 * time.Minute),
	}
	if !s.isDue(job2, time.Now()) {
		t.Error("expected due — NextRun is in the past")
	}
}

func TestIsDueOneShotFirstRun(t *testing.T) {
	s := &Scheduler{}
	job := CronJob{
		Enabled: true,
		OneShot: true,
		LastRun: time.Time{}, // never run
	}
	if !s.isDue(job, time.Now()) {
		t.Error("expected due — one-shot never run")
	}
}

func TestIsDuePeriodicJob(t *testing.T) {
	s := &Scheduler{}
	next := time.Now().Add(-5 * time.Minute) // 5 min ago
	job := CronJob{
		Enabled:  true,
		Schedule: "@hourly",
		LastRun:  time.Now().Add(-2 * time.Hour),
		NextRun:  next,
	}
	if !s.isDue(job, time.Now()) {
		t.Error("expected due — periodic job past NextRun")
	}
}

func TestIsDueDisabled(t *testing.T) {
	s := &Scheduler{}
	// isDue only checks timing; the checkAndRun loop skips disabled jobs.
	// But isDue itself should still return true for timing.
	job := CronJob{
		Enabled: false,
		LastRun: time.Time{}, // Never run
	}
	// isDue doesn't check Enabled flag — that's checked in checkAndRun.
	if !s.isDue(job, time.Now()) {
		t.Error("isDue should return true regardless of Enabled flag")
	}
}

func TestSchedulerCheckAndRunSkipsDisabledAndRunning(t *testing.T) {
	tmp := t.TempDir()
	store := NewSQLiteCronStore(tmp)

	// Create disabled job
	store.Create(CronJob{ID: "disabled", Name: "Disabled", Enabled: false})

	// Create already running job
	runningJob := CronJob{ID: "running", Name: "Running", Enabled: true, LastStatus: "running"}
	store.Create(runningJob)

	sched := NewScheduler(store, nil, time.Second)
	// Should not panic even with nil manager (neither job should execute)
	sched.checkAndRun()

	// Verify no changes
	disabled, _ := store.Get("disabled")
	if disabled.LastStatus != "" {
		t.Errorf("disabled job status = %q, want empty", disabled.LastStatus)
	}
	running, _ := store.Get("running")
	if running.LastStatus != "running" {
		t.Errorf("running job status = %q, want 'running'", running.LastStatus)
	}
}

func TestCronJobStructFields(t *testing.T) {
	now := time.Now()
	job := CronJob{
		ID:         "j1",
		Name:       "Test Job",
		Prompt:     "Run tests",
		Schedule:   "0 9 * * *",
		Mode:       "agent",
		WorkDir:    "/home/user/project",
		Enabled:    true,
		CreatedAt:  now,
		LastRun:    now,
		NextRun:    now.Add(time.Hour),
		RunCount:   5,
		LastStatus: "success",
		LastError:  "",
	}

	if job.ID != "j1" {
		t.Errorf("ID = %q, want 'j1'", job.ID)
	}
	if job.RunCount != 5 {
		t.Errorf("RunCount = %d, want 5", job.RunCount)
	}
}
