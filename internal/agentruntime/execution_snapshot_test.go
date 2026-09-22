package agentruntime

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/session"
)

func TestInspectSessionExecutionTracksLocalLifecycle(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := session.New(filepath.Join(t.TempDir(), "work"), sessionDir)
	if err := mgr.InitWithID("snapshot-local"); err != nil {
		t.Fatal(err)
	}
	lease, err := session.AcquireExecutionAdmission(sessionDir, "snapshot-local")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()

	execution := &ExecutionRuntime{}
	execution.SetRunStore(RunStore{SessionDir: sessionDir})
	now := time.Now()
	if _, err := execution.BeginDurable(t.Context(), DurableRun{
		ID: "run-local", SessionID: "snapshot-local", Status: string(RunStateRunning), StartedAt: now,
	}, RunEvent{EventType: "started", Timestamp: now}); err != nil {
		t.Fatal(err)
	}

	snapshot, err := InspectSessionExecution(sessionDir, "snapshot-local")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != SessionExecutionLocal || !snapshot.Running || snapshot.CanSubmit || !snapshot.CanCancelLocal {
		t.Fatalf("local snapshot = %+v", snapshot)
	}
	if snapshot.ActiveRun == nil || snapshot.ActiveRun.ID != "run-local" || snapshot.LinkageState != "bound" {
		t.Fatalf("local run projection = %+v", snapshot)
	}
	if snapshot.LeasePurpose != string(session.RuntimeLeasePurposeExecution) || snapshot.LeaseEpoch == 0 || snapshot.LeaseTokenIdentity == "" {
		t.Fatalf("local lease diagnostics = %+v", snapshot)
	}

	if err := execution.FinishDurable("run-local", RunStateCompleted, "", RunEvent{EventType: "finished"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = InspectSessionExecution(sessionDir, "snapshot-local")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != SessionExecutionReserved || snapshot.Phase != "releasing" || snapshot.CanSubmit {
		t.Fatalf("post-terminal pre-release snapshot = %+v", snapshot)
	}
	lease.Release()
	snapshot, err = InspectSessionExecution(sessionDir, "snapshot-local")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != SessionExecutionIdle || snapshot.Busy || !snapshot.CanSubmit {
		t.Fatalf("idle snapshot = %+v", snapshot)
	}
}

func TestInspectSessionExecutionDistinguishesExternalLegacyAndOrphaned(t *testing.T) {
	tests := []struct {
		name         string
		leasePurpose string
		leaseRunID   string
		wantState    SessionExecutionState
		wantLinkage  string
		wantRunning  bool
	}{
		{name: "external", leasePurpose: "execution", leaseRunID: "run-active", wantState: SessionExecutionExternal, wantLinkage: "bound", wantRunning: true},
		{name: "legacy unbound", leasePurpose: "run", wantState: SessionExecutionExternal, wantLinkage: "legacy_unbound", wantRunning: true},
		{name: "mismatched", leasePurpose: "execution", leaseRunID: "other-run", wantState: SessionExecutionInconsistent, wantLinkage: "mismatched"},
		{name: "orphaned", wantState: SessionExecutionOrphaned, wantLinkage: "none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionDir := t.TempDir()
			mgr := session.New(filepath.Join(t.TempDir(), "work"), sessionDir)
			if err := mgr.InitWithID("snapshot-state"); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			if err := session.SaveSessionRun(sessionDir, session.SessionRun{
				ID: "run-active", SessionID: "snapshot-state", Status: "running", StartedAt: now, UpdatedAt: now,
			}); err != nil {
				t.Fatal(err)
			}
			if tt.leasePurpose != "" {
				db, err := session.OpenRootDB(sessionDir)
				if err != nil {
					t.Fatal(err)
				}
				_, err = db.Bun().Exec(`INSERT INTO session_runtime_leases
					(session_id, owner_instance_id, owner_pid, owner_kind, lease_token_hash, epoch, run_id, purpose, state, acquired_at, heartbeat_at, expires_at, updated_at)
					VALUES (?, 'external-owner', 4242, 'process', 'external-token', 7, ?, ?, 'active',
					CAST(strftime('%s','now') AS INTEGER), CAST(strftime('%s','now') AS INTEGER), CAST(strftime('%s','now') AS INTEGER) + 60, CAST(strftime('%s','now') AS INTEGER))`,
					"snapshot-state", tt.leaseRunID, tt.leasePurpose)
				if err != nil {
					t.Fatal(err)
				}
			}

			snapshot, err := InspectSessionExecution(sessionDir, "snapshot-state")
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.State != tt.wantState || snapshot.LinkageState != tt.wantLinkage || snapshot.Running != tt.wantRunning {
				t.Fatalf("snapshot = %+v, want state=%s linkage=%s running=%t", snapshot, tt.wantState, tt.wantLinkage, tt.wantRunning)
			}
			if snapshot.CanSubmit || snapshot.CanCancelLocal {
				t.Fatalf("busy non-local snapshot exposes controls: %+v", snapshot)
			}
		})
	}
}

func TestInspectSessionExecutionProjectsMutationAsReserved(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := session.New(filepath.Join(t.TempDir(), "work"), sessionDir)
	if err := mgr.InitWithID("snapshot-reserved"); err != nil {
		t.Fatal(err)
	}
	lease, err := session.AcquireMutation(sessionDir, "snapshot-reserved")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()

	snapshot, err := InspectSessionExecution(sessionDir, "snapshot-reserved")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != SessionExecutionReserved || snapshot.Running || !snapshot.Busy || snapshot.CanSubmit || snapshot.DisplayOwnerScope != "local" {
		t.Fatalf("reserved snapshot = %+v", snapshot)
	}
}

func TestInspectSessionExecutionRequiresCanonicalRemoteRecordForDetachedState(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := session.New(filepath.Join(t.TempDir(), "work"), sessionDir)
	if err := mgr.InitWithID("snapshot-remote"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := session.SaveSessionRun(sessionDir, session.SessionRun{
		ID: "run-remote", SessionID: "snapshot-remote", Source: "responses_background", Status: "running", StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	snapshot, err := InspectSessionExecution(sessionDir, "snapshot-remote")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != SessionExecutionOrphaned {
		t.Fatalf("source-only snapshot = %+v, want orphaned", snapshot)
	}
	if err := session.SaveResponseRun(sessionDir, session.ResponseRun{
		SessionID: "snapshot-remote", LocalRunID: "provider-run", LocalTurnID: "run-remote",
		ResponseID: "resp-1", Provider: "openai", API: "openai-responses", State: "in_progress",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = InspectSessionExecution(sessionDir, "snapshot-remote")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != SessionExecutionDetached || !snapshot.Running || !snapshot.CanCancelRemote || snapshot.CanSubmit || snapshot.RemoteRunID != "provider-run" {
		t.Fatalf("detached snapshot = %+v", snapshot)
	}
}

func TestReattachDurableRunPromotesRecoveryLeaseAndRegistersLocal(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := session.New(filepath.Join(t.TempDir(), "work"), sessionDir)
	if err := mgr.InitWithID("snapshot-reattach"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := session.SaveSessionRun(sessionDir, session.SessionRun{
		ID: "run-reattach", SessionID: "snapshot-reattach", Status: "running", StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	lease, err := session.AcquireRecovery(sessionDir, "snapshot-reattach", "run-reattach")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()

	execution := &ExecutionRuntime{}
	execution.SetRunStore(RunStore{SessionDir: sessionDir})
	if _, err := execution.ReattachDurableRun(t.Context(), DurableRun{
		ID: "run-reattach", SessionID: "snapshot-reattach", Status: string(RunStateRunning), StartedAt: now,
	}, RunStateRunning, RunEvent{EventType: "started", Timestamp: now}); err != nil {
		t.Fatal(err)
	}
	binding := lease.Binding()
	if binding.Purpose != session.RuntimeLeasePurposeExecution || binding.RunID != "run-reattach" {
		t.Fatalf("reattach binding = %+v", binding)
	}
	snapshot, err := InspectSessionExecution(sessionDir, "snapshot-reattach")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != SessionExecutionLocal || !snapshot.CanCancelLocal {
		t.Fatalf("reattached snapshot = %+v", snapshot)
	}
	if err := execution.FinishDurable("run-reattach", RunStateFailed, "test cleanup", RunEvent{EventType: "failed"}); err != nil {
		t.Fatal(err)
	}
}
