package session

import (
	"path/filepath"
	"testing"
	"time"
)

func TestReadSessionExecutionFactsBatchMatchesSingleReads(t *testing.T) {
	sessionDir := t.TempDir()

	idle := New(filepath.Join(t.TempDir(), "work"), sessionDir)
	if err := idle.InitWithID("batch-facts-idle"); err != nil {
		t.Fatal(err)
	}

	running := New(filepath.Join(t.TempDir(), "work"), sessionDir)
	if err := running.InitWithID("batch-facts-run"); err != nil {
		t.Fatal(err)
	}
	admission, err := AcquireExecutionAdmission(sessionDir, "batch-facts-run")
	if err != nil {
		t.Fatalf("acquire execution admission: %v", err)
	}
	defer admission.Release()
	now := time.Now().UTC()
	if err := SaveSessionRun(sessionDir, SessionRun{
		ID: "run-batch-active", SessionID: "batch-facts-run", Status: "running", Source: "test",
		StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	leased := New(filepath.Join(t.TempDir(), "work"), sessionDir)
	if err := leased.InitWithID("batch-facts-lease"); err != nil {
		t.Fatal(err)
	}
	mutation, err := AcquireMutation(sessionDir, "batch-facts-lease")
	if err != nil {
		t.Fatalf("acquire mutation lease: %v", err)
	}
	defer mutation.Release()

	ids := []string{
		"batch-facts-idle", "batch-facts-run", "batch-facts-lease", "batch-facts-missing",
		"", "batch-facts-run",
	}
	batch, err := ReadSessionExecutionFactsBatch(t.Context(), sessionDir, ids)
	if err != nil {
		t.Fatalf("batch facts: %v", err)
	}
	if len(batch) != 4 {
		t.Fatalf("batch facts count = %d, want 4 (empty ignored, duplicates collapsed)", len(batch))
	}
	if _, ok := batch[""]; ok {
		t.Fatal("empty session ID must be ignored")
	}

	for _, id := range []string{"batch-facts-idle", "batch-facts-run", "batch-facts-lease", "batch-facts-missing"} {
		single, err := ReadSessionExecutionFacts(sessionDir, id)
		if err != nil {
			t.Fatalf("single facts %q: %v", id, err)
		}
		got, ok := batch[id]
		if !ok {
			t.Fatalf("batch facts missing ID %q", id)
		}
		if got.SessionID != single.SessionID || got.SessionExists != single.SessionExists {
			t.Fatalf("%s batch identity = %+v, single = %+v", id, got, single)
		}
		if got.DatabaseIdentity != single.DatabaseIdentity {
			t.Fatalf("%s identity = %q, want %q", id, got.DatabaseIdentity, single.DatabaseIdentity)
		}
		if got.DatabaseNow.IsZero() || single.DatabaseNow.IsZero() {
			t.Fatalf("%s database clock missing: batch=%v single=%v", id, got.DatabaseNow, single.DatabaseNow)
		}
		if delta := got.DatabaseNow.Sub(single.DatabaseNow); delta > 2*time.Second || delta < -2*time.Second {
			t.Fatalf("%s database clock diverged: batch=%v single=%v", id, got.DatabaseNow, single.DatabaseNow)
		}
		if len(got.ActiveRuns) != len(single.ActiveRuns) {
			t.Fatalf("%s active runs = %+v, want %+v", id, got.ActiveRuns, single.ActiveRuns)
		}
		for i := range got.ActiveRuns {
			if got.ActiveRuns[i].ID != single.ActiveRuns[i].ID || got.ActiveRuns[i].Status != single.ActiveRuns[i].Status || got.ActiveRuns[i].SessionID != single.ActiveRuns[i].SessionID {
				t.Fatalf("%s active run[%d] = %+v, want %+v", id, i, got.ActiveRuns[i], single.ActiveRuns[i])
			}
		}
		if (got.Lease == nil) != (single.Lease == nil) {
			t.Fatalf("%s lease presence = %v, want %v", id, got.Lease != nil, single.Lease != nil)
		}
		if got.Lease != nil {
			if got.Lease.State != single.Lease.State || got.Lease.Valid != single.Lease.Valid ||
				got.Lease.Purpose != single.Lease.Purpose || got.Lease.RunID != single.Lease.RunID ||
				got.Lease.Epoch != single.Lease.Epoch || got.Lease.OwnerInstanceID != single.Lease.OwnerInstanceID ||
				got.Lease.TokenHash != single.Lease.TokenHash || !got.Lease.ExpiresAt.Equal(single.Lease.ExpiresAt) {
				t.Fatalf("%s lease = %+v, want %+v", id, got.Lease, single.Lease)
			}
		}
		if (got.Recovery == nil) != (single.Recovery == nil) {
			t.Fatalf("%s recovery presence = %v, want %v", id, got.Recovery != nil, single.Recovery != nil)
		}
		if (got.RemoteRun == nil) != (single.RemoteRun == nil) {
			t.Fatalf("%s remote run presence = %v, want %v", id, got.RemoteRun != nil, single.RemoteRun != nil)
		}
	}

	if runFacts := batch["batch-facts-run"]; len(runFacts.ActiveRuns) != 1 || runFacts.ActiveRuns[0].ID != "run-batch-active" {
		t.Fatalf("active run batch facts = %+v", runFacts)
	}
	if leaseFacts := batch["batch-facts-lease"]; leaseFacts.Lease == nil || !leaseFacts.Lease.Valid || len(leaseFacts.ActiveRuns) != 0 {
		t.Fatalf("lease batch facts = %+v", leaseFacts)
	}
	if missing := batch["batch-facts-missing"]; missing.SessionExists || missing.Lease != nil || len(missing.ActiveRuns) != 0 {
		t.Fatalf("missing batch facts = %+v", missing)
	}
}
