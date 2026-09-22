package agentruntime

import (
	"context"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/session"
)

func TestRecoveryCoordinatorWakeConvergesExpiredExternalOwner(t *testing.T) {
	sessionDir := t.TempDir()
	initRecoveryTestSession(t, sessionDir, "coordinator-session")
	now := time.Now()
	if err := (RunStore{SessionDir: sessionDir}).Create(DurableRun{
		ID: "coordinator-run", SessionID: "coordinator-session", Source: "tui", Status: "running", StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	db, err := session.OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Bun().Exec(`INSERT INTO session_runtime_leases
		(session_id, owner_instance_id, owner_pid, owner_kind, lease_token_hash, epoch, run_id, purpose, state, acquired_at, heartbeat_at, expires_at, updated_at)
		VALUES (?, 'other-process', 42, 'process', 'other-token', 1, ?, 'execution', 'active',
		CAST(strftime('%s','now') AS INTEGER), CAST(strftime('%s','now') AS INTEGER), CAST(strftime('%s','now') AS INTEGER) + 60, CAST(strftime('%s','now') AS INTEGER))`,
		"coordinator-session", "coordinator-run"); err != nil {
		t.Fatal(err)
	}

	coordinator := NewRecoveryCoordinator(sessionDir, RecoveryCoordinatorOptions{ScanInterval: time.Hour})
	if err := coordinator.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := coordinator.Stop(ctx); err != nil {
			t.Errorf("stop coordinator: %v", err)
		}
	}()
	active, err := session.GetSessionRun(sessionDir, "coordinator-run")
	if err != nil || active == nil || active.Status != "running" {
		t.Fatalf("live external run changed during startup scan: %#v, err=%v", active, err)
	}
	if _, err := db.Bun().Exec(`UPDATE session_runtime_leases SET expires_at = CAST(strftime('%s','now') AS INTEGER) - 1 WHERE session_id = ?`, "coordinator-session"); err != nil {
		t.Fatal(err)
	}
	coordinator.Wake()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		run, getErr := session.GetSessionRun(sessionDir, "coordinator-run")
		if getErr != nil {
			t.Fatal(getErr)
		}
		if run != nil && run.Status == "failed" {
			recovery, getErr := session.GetSessionRunRecovery(sessionDir, run.ID)
			if getErr != nil || recovery == nil || recovery.State != session.SessionRunRecoveryComplete {
				t.Fatalf("completed recovery = %#v, err=%v", recovery, getErr)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expired external owner did not converge after coordinator wake")
}
