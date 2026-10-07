package agentruntime

import (
	"os"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/session"
)

// TestInspectSessionExecutionUsesOwnershipCacheForRemoteExecution proves the
// synchronized ownership view is the projection authority for a fresh remote
// execution lease: no SQLite read is needed and the projected state matches the
// durable path (external owner, running run).
func TestInspectSessionExecutionUsesOwnershipCacheForRemoteExecution(t *testing.T) {
	dir := t.TempDir()
	identity := session.RuntimeDatabaseIdentity(dir)
	session.PrimeRuntimeOwnership(identity, "session-cache", &session.RuntimeLeaseSnapshot{
		SessionID: "session-cache", OwnerInstanceID: "peer-process", OwnerPID: 4242, Epoch: 3,
		Purpose: session.RuntimeLeasePurposeExecution, RunID: "run-cache", State: "active",
		ExpiresAt: time.Now().Add(time.Minute), Valid: true,
	}, "run-cache", "running")

	snapshot, err := InspectSessionExecution(dir, "session-cache")
	if err != nil {
		t.Fatalf("InspectSessionExecution: %v", err)
	}
	if snapshot.Source != snapshotSourceCache {
		t.Fatalf("projection source = %q, want %q", snapshot.Source, snapshotSourceCache)
	}
	if snapshot.State != SessionExecutionExternal || !snapshot.Running {
		t.Fatalf("state=%s running=%v, want external/running", snapshot.State, snapshot.Running)
	}
	if snapshot.ActiveRun == nil || snapshot.ActiveRun.ID != "run-cache" || snapshot.ActiveRun.Status != "running" {
		t.Fatalf("activeRun = %#v", snapshot.ActiveRun)
	}
	if snapshot.LeaseOwnerInstanceID != "peer-process" || snapshot.LeaseOwnerPID != 4242 {
		t.Fatalf("lease owner projection = %#v", snapshot)
	}
	if !snapshot.Busy || snapshot.CanSubmit {
		t.Fatalf("busy=%v canSubmit=%v, want a busy external session", snapshot.Busy, snapshot.CanSubmit)
	}
}

// TestInspectSessionExecutionDeclinesOwnLeaseFastPath proves the cache never
// substitutes for the local path: this process's own lease must go through the
// DB/local projection.
func TestInspectSessionExecutionDeclinesOwnLeaseFastPath(t *testing.T) {
	dir := t.TempDir()
	identity := session.RuntimeDatabaseIdentity(dir)
	session.PrimeRuntimeOwnership(identity, "session-own", &session.RuntimeLeaseSnapshot{
		SessionID: "session-own", OwnerInstanceID: session.RuntimeOwnerInstanceID(), OwnerPID: os.Getpid(), Epoch: 1,
		Purpose: session.RuntimeLeasePurposeExecution, RunID: "run-own", State: "active",
		ExpiresAt: time.Now().Add(time.Minute), Valid: true,
	}, "run-own", "running")

	if _, ok := inspectSessionExecutionFromOwnershipCache(dir, "session-own", identity); ok {
		t.Fatal("the cache fast path must decline this process's own lease")
	}
}

// TestInspectSessionExecutionDeclinesIncompleteCacheEntry proves an execution
// entry without a Run status falls through to SQLite rather than projecting a
// misleading external snapshot.
func TestInspectSessionExecutionDeclinesIncompleteCacheEntry(t *testing.T) {
	dir := t.TempDir()
	identity := session.RuntimeDatabaseIdentity(dir)
	session.PrimeRuntimeOwnership(identity, "session-partial", &session.RuntimeLeaseSnapshot{
		SessionID: "session-partial", OwnerInstanceID: "peer-process", OwnerPID: 11, Epoch: 1,
		Purpose: session.RuntimeLeasePurposeExecution, RunID: "run-partial", State: "active",
		ExpiresAt: time.Now().Add(time.Minute), Valid: true,
	}, "run-partial", "")

	if _, ok := inspectSessionExecutionFromOwnershipCache(dir, "session-partial", identity); ok {
		t.Fatal("an entry without a Run status must not satisfy the cache fast path")
	}
}
