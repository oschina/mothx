package session

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/dao"
	database "github.com/startvibecoding/mothx/internal/db"
)

// TestActiveRuntimeLeasesReportsHeldHolders pins the preflight that guards
// destructive maintenance: every active lease must be reported even when its
// most recent renewal lapsed, because that process retains ownership until a
// fenced takeover or release. A directory without a database is answered
// without creating one.
func TestActiveRuntimeLeasesReportsHeldHolders(t *testing.T) {
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
	// A row that lapsed without a takeover still belongs to its original owner.
	// The owner can renew it again, so destructive maintenance must refuse until
	// it is explicitly released or fenced out by a competing acquire.
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
	if len(leases) != 2 {
		t.Fatalf("active leases = %#v, want both held owners", leases)
	}
	var holder ActiveRuntimeLease
	for _, lease := range leases {
		if lease.SessionID == "lease-holder" {
			holder = lease
			break
		}
	}
	if holder.SessionID != "lease-holder" || holder.OwnerPID != os.Getpid() || holder.Purpose != "run" {
		t.Fatalf("holder = %#v, want this process' run lease for lease-holder", holder)
	}
	if description := holder.Describe(); !strings.Contains(description, "lease-holder") || !strings.Contains(description, "run") {
		t.Fatalf("Describe() = %q, want the session and run it belongs to", description)
	}

	lease.release()
	if _, err := dao.NewRuntimeLeaseDAO(db.Bun()).Release(context.Background(), &dao.RuntimeLeaseRecord{
		SessionID: "lease-expired", OwnerID: "owner-gone", Epoch: 1, TokenHash: "token-gone",
	}); err != nil {
		t.Fatal(err)
	}
	leases, err = ActiveRuntimeLeases(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 0 {
		t.Fatalf("active leases after release = %#v, want none", leases)
	}
}

// TestActiveRuntimeLeasesNeverMigratesThePreflightDatabase builds a database
// which has only the lease table. The normal session opener would apply the
// entire schema to it; the maintenance preflight must only read the table and
// leave its file byte-for-byte unchanged.
func TestActiveRuntimeLeasesNeverMigratesThePreflightDatabase(t *testing.T) {
	sessionDir := t.TempDir()
	path := RootDatabasePath(sessionDir)
	standalone, err := database.OpenStandalone(path, func(sqlDB *sql.DB) error {
		_, err := sqlDB.Exec(`CREATE TABLE session_runtime_leases (
			session_id TEXT PRIMARY KEY,
			owner_instance_id TEXT NOT NULL,
			owner_pid INTEGER NOT NULL,
			owner_kind TEXT NOT NULL,
			lease_token_hash TEXT NOT NULL,
			epoch INTEGER NOT NULL,
			run_id TEXT NOT NULL,
			purpose TEXT NOT NULL,
			state TEXT NOT NULL,
			acquired_at INTEGER NOT NULL,
			heartbeat_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := standalone.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	leases, err := ActiveRuntimeLeases(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 0 {
		t.Fatalf("leases = %#v, want none", leases)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("the lease preflight modified the database it inspected")
	}
}
