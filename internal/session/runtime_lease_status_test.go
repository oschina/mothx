package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/dao"
)

// TestActiveRuntimeLeasesReportsOnlyLiveHolders pins the preflight that guards
// destructive maintenance: a lease another process is still renewing must be
// reported, a released or expired one must not, and a directory without a
// database must be answered without creating one.
func TestActiveRuntimeLeasesReportsOnlyLiveHolders(t *testing.T) {
	sessionDir := t.TempDir()

	// No database yet: nothing is held, and a read-only preflight must not
	// initialize the file it is only inspecting.
	leases, err := ActiveRuntimeLeases(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 0 {
		t.Fatalf("leases before the first database = %#v, want none", leases)
	}
	if _, err := os.Stat(RootDatabasePath(sessionDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ActiveRuntimeLeases created the database it inspected: %v", err)
	}

	manager := New(filepath.Join(t.TempDir(), "work"), sessionDir)
	if err := manager.InitWithID("lease-holder"); err != nil {
		t.Fatal(err)
	}
	expired := New(filepath.Join(t.TempDir(), "work"), sessionDir)
	if err := expired.InitWithID("lease-expired"); err != nil {
		t.Fatal(err)
	}
	lease, err := acquireRuntimeLease(sessionDir, "lease-holder", "run")
	if err != nil || lease == nil {
		t.Fatalf("acquire live lease = %v, lease=%v", err, lease)
	}
	t.Cleanup(lease.release)

	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	// A row that lapsed without a takeover is still owned by its process, but it
	// is no longer evidence of concurrent writing, so the preflight must not
	// refuse on its behalf.
	past := time.Now().Add(-time.Minute).Unix()
	if err := dao.NewRuntimeLeaseDAO(db.Bun()).Insert(context.Background(), db.Bun(), &dao.RuntimeLeaseRecord{
		SessionID: "lease-expired", OwnerID: "owner-gone", OwnerPID: 1, OwnerKind: "process",
		TokenHash: "token-gone", Epoch: 1, Purpose: "run", State: "active",
		AcquiredAt: past, HeartbeatAt: past, ExpiresAt: past, UpdatedAt: past,
	}); err != nil {
		t.Fatal(err)
	}

	leases, err = ActiveRuntimeLeases(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 1 {
		t.Fatalf("active leases = %#v, want only the live holder", leases)
	}
	holder := leases[0]
	if holder.SessionID != "lease-holder" || holder.OwnerPID != os.Getpid() || holder.Purpose != "run" {
		t.Fatalf("holder = %#v, want this process' run lease for lease-holder", holder)
	}
	if description := holder.Describe(); !strings.Contains(description, "lease-holder") || !strings.Contains(description, "run") {
		t.Fatalf("Describe() = %q, want the session and run it belongs to", description)
	}

	lease.release()
	leases, err = ActiveRuntimeLeases(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 0 {
		t.Fatalf("active leases after release = %#v, want none", leases)
	}
}
