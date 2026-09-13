package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/session"
)

func TestRecoverOrphanedRunsFailsLocalAndKeepsRemote(t *testing.T) {
	sessionDir := t.TempDir()
	store := RunStore{SessionDir: sessionDir}
	now := time.Now()
	initRecoveryTestSession(t, sessionDir, "session-1")
	initRecoveryTestSession(t, sessionDir, "session-2")
	for _, run := range []DurableRun{
		{ID: "local", SessionID: "session-1", Source: "acp", Status: "running", StartedAt: now},
		{ID: "remote", SessionID: "session-2", Source: "responses_background", Status: "running", StartedAt: now},
	} {
		if err := store.Create(run); err != nil {
			t.Fatal(err)
		}
	}
	var cleaned []string
	result, err := RecoverOrphanedRuns(sessionDir, func(run session.SessionRun) RecoveryAction {
		if run.ID == "remote" {
			return RecoveryKeepRemote
		}
		return RecoveryFailLocal
	}, func(run session.SessionRun) error {
		cleaned = append(cleaned, run.ID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Failed) != 1 || result.Failed[0].ID != "local" || len(result.Kept) != 1 || result.Kept[0].ID != "remote" {
		t.Fatalf("result = %#v", result)
	}
	if len(cleaned) != 1 || cleaned[0] != "local" {
		t.Fatalf("cleaned = %#v", cleaned)
	}
	local, _ := session.GetSessionRun(sessionDir, "local")
	remote, _ := session.GetSessionRun(sessionDir, "remote")
	if local.Status != "failed" || remote.Status != "running" {
		t.Fatalf("local=%#v remote=%#v", local, remote)
	}
}

func TestRecoverOrphanedRunsParallelizesAndPreservesScanOrder(t *testing.T) {
	sessionDir := t.TempDir()
	store := RunStore{SessionDir: sessionDir}
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("parallel-%02d", i)
		initRecoveryTestSession(t, sessionDir, id)
		started := time.Unix(int64(100+i), 0)
		if err := store.Create(DurableRun{ID: "run-" + id, SessionID: id, Status: "running", StartedAt: started}); err != nil {
			t.Fatal(err)
		}
	}
	var active, maxActive atomic.Int32
	// Rendezvous: the first worker stays inside the callback until a second one
	// arrives, so the overlap assertion is deterministic instead of depending on
	// how the scheduler and the single-connection SQLite interleave. The barrier
	// normally closes within milliseconds; the generous 30s budget only bounds the
	// serialized case, where the first worker times out and the assertion below
	// fails with a clear message (later workers find the barrier open).
	var arrived atomic.Int32
	release := make(chan struct{})
	result, err := RecoverOrphanedRuns(sessionDir, nil, func(session.SessionRun) error {
		current := active.Add(1)
		for {
			previous := maxActive.Load()
			if current <= previous || maxActive.CompareAndSwap(previous, current) {
				break
			}
		}
		if arrived.Add(1) == 2 {
			close(release)
		}
		select {
		case <-release:
		case <-time.After(30 * time.Second):
		}
		active.Add(-1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if maxActive.Load() < 2 {
		t.Fatalf("maximum recovery concurrency = %d, want parallel workers (callbacks never overlapped)", maxActive.Load())
	}
	if len(result.Failed) != 10 {
		t.Fatalf("failed recovery count = %d, want 10", len(result.Failed))
	}
	for i, run := range result.Failed {
		want := fmt.Sprintf("run-parallel-%02d", i)
		if run.ID != want {
			t.Fatalf("failed recovery order[%d] = %q, want %q", i, run.ID, want)
		}
	}
}

func TestRecoverOrphanedRunsSlowAttemptDoesNotBlockOtherSessions(t *testing.T) {
	sessionDir := t.TempDir()
	store := RunStore{SessionDir: sessionDir}
	initRecoveryTestSession(t, sessionDir, "slow-session")
	initRecoveryTestSession(t, sessionDir, "fast-session")
	if err := store.Create(DurableRun{ID: "slow-run", SessionID: "slow-session", Source: "tui", Status: "running", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(DurableRun{ID: "fast-run", SessionID: "fast-session", Source: "tui", Status: "running", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	fastAttemptStarted := make(chan struct{})
	resultCh := make(chan error, 1)
	// Timing margins keep the serial-vs-parallel discrimination under -race:
	// the slow callback must stay asleep longer than every fast-path window,
	// while each fast window must stay wide enough for slowed DB operations.
	const slowAttemptSleep = 4 * time.Second
	go func() {
		_, err := recoverOrphanedRunsWithTrigger(context.Background(), sessionDir, "periodic", time.Second, nil, func(run session.SessionRun) error {
			if run.ID == "slow-run" {
				// Simulate an adapter/provider callback that ignores context. The
				// other worker must still make progress for its own Session.
				time.Sleep(slowAttemptSleep)
			} else if run.ID == "fast-run" {
				close(fastAttemptStarted)
			}
			return nil
		})
		resultCh <- err
	}()
	select {
	case <-fastAttemptStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("fast recovery attempt did not start while slow attempt was running")
	}
	deadline := time.Now().Add(3 * time.Second)
	fastDone := false
	for time.Now().Before(deadline) {
		run, err := session.GetSessionRun(sessionDir, "fast-run")
		if err != nil {
			t.Fatal(err)
		}
		if run != nil && run.Status == "failed" {
			fastDone = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !fastDone {
		fastRun, _ := session.GetSessionRun(sessionDir, "fast-run")
		t.Fatalf("fast run remained blocked by slow attempt: %#v", fastRun)
	}
	select {
	case err := <-resultCh:
		if err == nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("recovery scan error = %v, want slow-attempt deadline", err)
		}
	case <-time.After(2 * slowAttemptSleep):
		t.Fatal("recovery scan did not finish")
	}
}

func TestRecoverOrphanedSessionRunForAdmissionFailsOnlyLocalRun(t *testing.T) {
	sessionDir := t.TempDir()
	store := RunStore{SessionDir: sessionDir}
	now := time.Now()
	initRecoveryTestSession(t, sessionDir, "session-local")
	initRecoveryTestSession(t, sessionDir, "session-remote")
	if err := store.Create(DurableRun{ID: "stale-local", SessionID: "session-local", Source: "wechat", Status: "running", StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(DurableRun{ID: "remote", SessionID: "session-remote", Source: "responses_background", Status: "running", StartedAt: now}); err != nil {
		t.Fatal(err)
	}

	local, err := RecoverOrphanedSessionRun(sessionDir, "session-local", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(local.Failed) != 1 || local.Failed[0].ID != "stale-local" || len(local.Kept) != 0 {
		t.Fatalf("local recovery = %#v", local)
	}
	recovered, err := session.GetSessionRun(sessionDir, "stale-local")
	if err != nil || recovered == nil || recovered.Status != "failed" {
		t.Fatalf("recovered local run = %#v, err=%v", recovered, err)
	}

	remote, err := RecoverOrphanedSessionRun(sessionDir, "session-remote", func(run session.SessionRun) RecoveryAction {
		return RecoveryKeepRemote
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(remote.Kept) != 1 || remote.Kept[0].ID != "remote" || len(remote.Failed) != 0 {
		t.Fatalf("remote recovery = %#v", remote)
	}
	stillActive, err := session.GetSessionRun(sessionDir, "remote")
	if err != nil || stillActive == nil || stillActive.Status != "running" {
		t.Fatalf("remote run = %#v, err=%v", stillActive, err)
	}
}

func TestRecoverOrphanedRunsSkipsValidExecutionLease(t *testing.T) {
	sessionDir := t.TempDir()
	initRecoveryTestSession(t, sessionDir, "session-owned")
	guard, err := session.AcquireExecutionAdmission(sessionDir, "session-owned")
	if err != nil {
		t.Fatal(err)
	}
	store := RunStore{SessionDir: sessionDir}
	if err := store.Create(DurableRun{ID: "owned", SessionID: "session-owned", Source: "acp", Status: "running", StartedAt: time.Now()}); err != nil {
		guard.Release()
		t.Fatal(err)
	}

	result, err := RecoverOrphanedRuns(sessionDir, nil, nil)
	if err != nil {
		guard.Release()
		t.Fatal(err)
	}
	if len(result.Skipped) != 1 || result.Skipped[0].ID != "owned" || len(result.Failed) != 0 {
		guard.Release()
		t.Fatalf("recovery with live owner = %#v", result)
	}
	active, err := session.GetSessionRun(sessionDir, "owned")
	if err != nil || active == nil || active.Status != "running" {
		guard.Release()
		t.Fatalf("owned run = %#v, err=%v", active, err)
	}

	guard.Release()
	result, err = RecoverOrphanedRuns(sessionDir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Failed) != 1 || result.Failed[0].ID != "owned" {
		t.Fatalf("recovery after release = %#v", result)
	}
}

func TestDefaultRunRecoveryPolicyDoesNotTrustSourceAlone(t *testing.T) {
	if got := DefaultRunRecoveryPolicy(session.SessionRun{Source: "responses_background"}); got != RecoveryFailLocal {
		t.Fatalf("default policy = %q, want %q", got, RecoveryFailLocal)
	}
}

func TestRecoverOrphanedRunsKeepsVerifiedRemoteRecord(t *testing.T) {
	sessionDir := t.TempDir()
	initRecoveryTestSession(t, sessionDir, "session-remote-record")
	now := time.Now()
	if err := (RunStore{SessionDir: sessionDir}).Create(DurableRun{
		ID: "remote-parent", SessionID: "session-remote-record", Source: "responses_background", Status: "running", StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.SaveResponseRun(sessionDir, session.ResponseRun{
		SessionID: "session-remote-record", LocalRunID: "remote-provider-run", LocalTurnID: "remote-parent",
		ResponseID: "resp-remote", Provider: "openai", API: "openai-responses", State: "queued",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	result, err := RecoverOrphanedRuns(sessionDir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Kept) != 1 || result.Kept[0].ID != "remote-parent" || len(result.Failed) != 0 {
		t.Fatalf("verified remote recovery = %#v", result)
	}
	recovery, err := session.GetSessionRunRecovery(sessionDir, "remote-parent")
	if err != nil || recovery == nil || recovery.State != session.SessionRunRecoveryDetached {
		t.Fatalf("remote recovery record = %#v, err=%v", recovery, err)
	}
}

func TestRecoveryFailureIsDurableAndRetryable(t *testing.T) {
	sessionDir := t.TempDir()
	initRecoveryTestSession(t, sessionDir, "session-retry")
	store := RunStore{SessionDir: sessionDir}
	if err := store.Create(DurableRun{ID: "retry", SessionID: "session-retry", Source: "tui", Status: "running", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("decision cleanup unavailable")
	if _, err := RecoverOrphanedRuns(sessionDir, nil, func(session.SessionRun) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("recovery error = %v, want %v", err, wantErr)
	}
	recovery, err := session.GetSessionRunRecovery(sessionDir, "retry")
	if err != nil {
		t.Fatal(err)
	}
	if recovery == nil || recovery.State != session.SessionRunRecoveryFailed || recovery.Attempt != 1 || recovery.LastError != wantErr.Error() || recovery.NextRetryAt == nil {
		t.Fatalf("failed recovery record = %#v", recovery)
	}
	snapshot, err := InspectSessionExecution(sessionDir, "session-retry")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != SessionExecutionRecoveryFailed || snapshot.Running || !snapshot.Busy || snapshot.RecoveryAttempt != 1 {
		t.Fatalf("failed recovery snapshot = %#v", snapshot)
	}

	result, err := RecoverOrphanedSessionRun(sessionDir, "session-retry", nil, nil)
	if err != nil || len(result.Failed) != 1 {
		t.Fatalf("retry result = %#v, err=%v", result, err)
	}
	recovery, err = session.GetSessionRunRecovery(sessionDir, "retry")
	if err != nil || recovery == nil || recovery.State != session.SessionRunRecoveryComplete || recovery.Attempt != 2 || recovery.CompletedAt == nil {
		t.Fatalf("completed recovery record = %#v, err=%v", recovery, err)
	}
}

func TestRecoveryFailurePersistsAfterAttemptContextCancellation(t *testing.T) {
	sessionDir := t.TempDir()
	initRecoveryTestSession(t, sessionDir, "session-cancelled-attempt")
	run := session.SessionRun{ID: "cancelled-attempt", SessionID: "session-cancelled-attempt", Status: "running", StartedAt: time.Now()}
	if err := (RunStore{SessionDir: sessionDir}).Create(DurableRun{ID: run.ID, SessionID: run.SessionID, Status: "running", StartedAt: run.StartedAt}); err != nil {
		t.Fatal(err)
	}
	guard, err := session.AcquireRecovery(sessionDir, run.SessionID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()
	recovery, err := session.BeginSessionRunRecovery(sessionDir, run.SessionID, run.ID, "startup", "owner_lost", 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	wantErr := errors.New("attempt deadline exceeded")
	if err := failRunRecoveryAttemptContext(ctx, sessionDir, run, recovery.Attempt, wantErr); !errors.Is(err, wantErr) {
		t.Fatalf("failure result = %v, want %v", err, wantErr)
	}
	stored, err := session.GetSessionRunRecovery(sessionDir, run.ID)
	if err != nil || stored == nil || stored.State != session.SessionRunRecoveryFailed {
		t.Fatalf("stored recovery = %#v, err=%v", stored, err)
	}
}

func TestRecoverySQLiteTerminalWriteFailurePersistsRecoveryFailed(t *testing.T) {
	sessionDir := t.TempDir()
	initRecoveryTestSession(t, sessionDir, "session-sqlite-failure")
	store := RunStore{SessionDir: sessionDir}
	if err := store.Create(DurableRun{ID: "sqlite-failure", SessionID: "session-sqlite-failure", Source: "tui", Status: "running", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	db, err := session.OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Bun().Exec(`CREATE TRIGGER reject_recovery_terminal_event BEFORE INSERT ON session_run_events
		WHEN NEW.event_type = 'recovered' BEGIN SELECT RAISE(ABORT, 'injected recovery terminal write failure'); END`); err != nil {
		t.Fatal(err)
	}

	if _, err := RecoverOrphanedRuns(sessionDir, nil, nil); err == nil || !strings.Contains(err.Error(), "injected recovery terminal write failure") {
		t.Fatalf("recovery error = %v, want injected terminal write failure", err)
	}
	run, err := session.GetSessionRun(sessionDir, "sqlite-failure")
	if err != nil {
		t.Fatal(err)
	}
	if run == nil || run.Status != "running" {
		t.Fatalf("run after failed terminal write = %#v, want active", run)
	}
	recovery, err := session.GetSessionRunRecovery(sessionDir, "sqlite-failure")
	if err != nil || recovery == nil || recovery.State != session.SessionRunRecoveryFailed || recovery.NextRetryAt == nil {
		t.Fatalf("recovery after failed terminal write = %#v, err=%v", recovery, err)
	}
	snapshot, err := InspectSessionExecution(sessionDir, "session-sqlite-failure")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != SessionExecutionRecoveryFailed || !snapshot.Busy || snapshot.Running {
		t.Fatalf("snapshot after failed terminal write = %+v", snapshot)
	}

	if _, err := db.Bun().Exec(`DROP TRIGGER reject_recovery_terminal_event`); err != nil {
		t.Fatal(err)
	}
	result, err := RecoverOrphanedSessionRun(sessionDir, "session-sqlite-failure", nil, nil)
	if err != nil || len(result.Failed) != 1 {
		t.Fatalf("recovery retry = %#v, err=%v", result, err)
	}
	run, err = session.GetSessionRun(sessionDir, "sqlite-failure")
	if err != nil || run == nil || run.Status != "failed" {
		t.Fatalf("run after recovery retry = %#v, err=%v", run, err)
	}
	recovery, err = session.GetSessionRunRecovery(sessionDir, "sqlite-failure")
	if err != nil || recovery == nil || recovery.State != session.SessionRunRecoveryComplete || recovery.Attempt != 2 {
		t.Fatalf("recovery after retry = %#v, err=%v", recovery, err)
	}
}

func initRecoveryTestSession(t *testing.T, sessionDir, id string) {
	t.Helper()
	manager := session.New(t.TempDir(), sessionDir)
	if err := manager.InitWithID(id); err != nil {
		t.Fatalf("create recovery test session %s: %v", id, err)
	}
}
