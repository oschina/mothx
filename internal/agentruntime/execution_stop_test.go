package agentruntime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/session"
)

func newExecutionStopTestSession(t *testing.T, sessionDir, sessionID string) *session.Manager {
	t.Helper()
	mgr := session.New(filepath.Join(t.TempDir(), "work"), sessionDir)
	if err := mgr.InitWithID(sessionID); err != nil {
		t.Fatalf("init session: %v", err)
	}
	return mgr
}

func TestRequestSessionStopCancelsOnlyRegisteredLocalExecution(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := newExecutionStopTestSession(t, sessionDir, "stop-local")
	guard, err := session.AcquireExecutionAdmission(sessionDir, mgr.GetHeader().ID)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()

	execution := &ExecutionRuntime{}
	execution.SetRunStore(RunStore{SessionDir: sessionDir})
	startedAt := time.Now()
	runCtx, err := execution.BeginDurable(t.Context(), DurableRun{
		ID: "run-local", SessionID: mgr.GetHeader().ID, Status: string(RunStateRunning), StartedAt: startedAt,
	}, RunEvent{EventType: "started", Timestamp: startedAt})
	if err != nil {
		t.Fatal(err)
	}

	result, err := RequestSessionStop(t.Context(), sessionDir, mgr.GetHeader().ID, SessionStopOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != SessionStopAccepted || result.Execution.State != SessionExecutionLocal || result.Execution.ActiveRun == nil || result.Execution.ActiveRun.Status != string(RunStateCancelling) {
		t.Fatalf("stop result = %+v", result)
	}
	select {
	case <-runCtx.Done():
	default:
		t.Fatal("local execution context was not cancelled")
	}
	if err := execution.FinishDurable("run-local", RunStateCancelled, "cancelled", RunEvent{EventType: "finished"}); err != nil {
		t.Fatal(err)
	}
}

func TestRequestSessionStopDoesNotCancelExternalExecution(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := newExecutionStopTestSession(t, sessionDir, "stop-external")
	now := time.Now()
	if err := session.SaveSessionRun(sessionDir, session.SessionRun{
		ID: "run-external", SessionID: mgr.GetHeader().ID, Status: "running", StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	db, err := session.OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Bun().Exec(`INSERT INTO session_runtime_leases
		(session_id, owner_instance_id, owner_pid, owner_kind, lease_token_hash, epoch, run_id, purpose, state, acquired_at, heartbeat_at, expires_at, updated_at)
		VALUES (?, 'external-owner', 4242, 'process', 'external-token', 9, ?, 'execution', 'active',
		CAST(strftime('%s','now') AS INTEGER), CAST(strftime('%s','now') AS INTEGER), CAST(strftime('%s','now') AS INTEGER) + 60, CAST(strftime('%s','now') AS INTEGER))`,
		mgr.GetHeader().ID, "run-external"); err != nil {
		t.Fatal(err)
	}

	result, err := RequestSessionStop(t.Context(), sessionDir, mgr.GetHeader().ID, SessionStopOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != SessionStopOwnedElsewhere || result.Execution.State != SessionExecutionExternal {
		t.Fatalf("stop result = %+v", result)
	}
	run, err := session.GetSessionRun(sessionDir, "run-external")
	if err != nil || run == nil || run.Status != "running" {
		t.Fatalf("external run changed: run=%+v err=%v", run, err)
	}
}

func TestRequestSessionStopRejectsStaleExpectedRunID(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := newExecutionStopTestSession(t, sessionDir, "stop-target-changed")
	now := time.Now()
	if err := session.CreateSessionRun(sessionDir, session.SessionRun{
		ID: "old-run", SessionID: mgr.GetHeader().ID, Status: "completed", StartedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.CreateSessionRun(sessionDir, session.SessionRun{
		ID: "new-run", SessionID: mgr.GetHeader().ID, Status: "running", StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := RequestSessionStop(t.Context(), sessionDir, mgr.GetHeader().ID, SessionStopOptions{ExpectedRunID: "old-run"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != SessionStopTargetChanged || result.Execution.ActiveRun == nil || result.Execution.ActiveRun.ID != "new-run" {
		t.Fatalf("stale stop result = %+v", result)
	}
	run, err := session.GetSessionRun(sessionDir, "new-run")
	if err != nil || run == nil || run.Status != "running" {
		t.Fatalf("new run changed: run=%+v err=%v", run, err)
	}
}

func TestRequestSessionStopTerminalizesOrphanAsCancelled(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := newExecutionStopTestSession(t, sessionDir, "stop-orphan")
	now := time.Now()
	if err := session.SaveSessionRun(sessionDir, session.SessionRun{
		ID: "run-orphan", SessionID: mgr.GetHeader().ID, Status: "running", StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := RequestSessionStop(t.Context(), sessionDir, mgr.GetHeader().ID, SessionStopOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != SessionStopRecoveryStarted || result.Execution.State != SessionExecutionIdle {
		t.Fatalf("stop result = %+v", result)
	}
	run, err := session.GetSessionRun(sessionDir, "run-orphan")
	if err != nil || run == nil || run.Status != "cancelled" || run.Error != "run cancelled by user after its execution owner was lost" {
		t.Fatalf("orphan terminal state: run=%+v err=%v", run, err)
	}
	recovery, err := session.GetSessionRunRecovery(sessionDir, run.ID)
	if err != nil || recovery == nil || recovery.State != session.SessionRunRecoveryComplete || recovery.ReasonCode != "cancelled_by_user_after_owner_loss" {
		t.Fatalf("recovery record: recovery=%+v err=%v", recovery, err)
	}
}

func TestRequestSessionStopProjectsReservedLease(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := newExecutionStopTestSession(t, sessionDir, "stop-reserved")
	guard, err := session.AcquireMutation(sessionDir, mgr.GetHeader().ID)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()

	result, err := RequestSessionStop(t.Context(), sessionDir, mgr.GetHeader().ID, SessionStopOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != SessionStopReserved || result.Execution.State != SessionExecutionReserved {
		t.Fatalf("stop result = %+v", result)
	}
}

func TestRequestSessionStopDetachedRemoteUsesProviderHook(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := newExecutionStopTestSession(t, sessionDir, "stop-remote")
	now := time.Now()
	if err := session.SaveSessionRun(sessionDir, session.SessionRun{
		ID: "run-remote", SessionID: mgr.GetHeader().ID, Source: "responses_background", Status: "running", StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.SaveResponseRun(sessionDir, session.ResponseRun{
		SessionID: mgr.GetHeader().ID, LocalRunID: "remote-local", LocalTurnID: "run-remote",
		ResponseID: "resp-1", Provider: "openai", API: "openai-responses", State: "in_progress",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	unsupported, err := RequestSessionStop(t.Context(), sessionDir, mgr.GetHeader().ID, SessionStopOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if unsupported.Code != SessionStopRemoteUnsupported {
		t.Fatalf("unsupported result = %+v", unsupported)
	}
	called := false
	accepted, err := RequestSessionStop(context.Background(), sessionDir, mgr.GetHeader().ID, SessionStopOptions{
		RemoteCancel: func(_ context.Context, request RemoteStopRequest) error {
			called = true
			if request.SessionID != mgr.GetHeader().ID || request.RunID != "run-remote" || request.RemoteRunID != "remote-local" || request.Provider != "openai" {
				t.Fatalf("remote request = %+v", request)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called || accepted.Code != SessionStopRemoteAccepted || accepted.Execution.State != SessionExecutionDetached {
		t.Fatalf("accepted result = %+v called=%t", accepted, called)
	}
}
