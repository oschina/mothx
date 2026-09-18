package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/session"
)

// TestReconcileArtifactStorageReclaimsOnlyAgedUnreferenced is the core safety
// contract of the directory-driven pass: a live attachment's bytes, a young
// orphan that could still belong to an in-flight intake or a restorable archive,
// and anything whose layout is not the Runtime's own are all left alone, while
// an unreferenced directory already past retention plus the grace window is
// reclaimed with its size accounted.
func TestReconcileArtifactStorageReclaimsOnlyAgedUnreferenced(t *testing.T) {
	root, _, mgr := inputTestSession(t)
	service, err := NewAttachmentService(root, DefaultAttachmentPolicy())
	if err != nil {
		t.Fatal(err)
	}
	live := publishTestArtifact(t, service, mgr.GetHeader().ID, "run-1", "keep.txt", "keep me")

	policy := DefaultAttachmentPolicy()
	now := time.Now().UTC()
	stale := writeArtifactDirectory(t, root, "0123456789abcdef", policy.Retention+artifactReconcileGrace+time.Hour, "gone")
	young := writeArtifactDirectory(t, root, "1123456789abcdef", time.Minute, "in flight")
	foreign := writeArtifactDirectory(t, root, "someone-elses", policy.Retention+artifactReconcileGrace+time.Hour, "not mine")
	nested := filepath.Join(root, artifactStorageDirectoryName, "2123456789abcdef")
	if err := os.MkdirAll(filepath.Join(nested, "subdir"), 0700); err != nil {
		t.Fatal(err)
	}

	report, err := ReconcileArtifactStorage(context.Background(), root, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Removed != 1 || report.Freed != int64(len("gone")) {
		t.Fatalf("report = %+v, want exactly the aged orphan reclaimed", report)
	}
	if report.SkippedReferenced != 1 || report.SkippedYoung != 1 || report.SkippedUnrecognized != 2 {
		t.Fatalf("report = %+v, want the referenced, young, and two unrecognized entries accounted", report)
	}
	if _, err := os.Stat(filepath.Join(root, artifactStorageDirectoryName, live.ID)); err != nil {
		t.Fatalf("the live attachment was reclaimed: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("the aged orphan survived the reconciliation: %v", err)
	}
	for _, kept := range []string{young, foreign, nested} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("%s must not be touched: %v", kept, err)
		}
	}
}

// TestReconcileArtifactStorageFailsClosedWithoutKnownReferences proves the
// difference between "nothing references this" and "we cannot ask": an absent or
// unreadable sessions database must never be read as an empty reference set.
func TestReconcileArtifactStorageFailsClosedWithoutKnownReferences(t *testing.T) {
	policy := DefaultAttachmentPolicy()
	now := time.Now().UTC()

	missingDB := t.TempDir()
	orphan := writeArtifactDirectory(t, missingDB, "0123456789abcdef", policy.Retention+artifactReconcileGrace+time.Hour, "bytes")
	if _, err := ReconcileArtifactStorage(context.Background(), missingDB, policy, now); err == nil {
		t.Fatal("reconciliation ran against a directory with no sessions database")
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("content was removed although the reference set was unknown: %v", err)
	}

	corruptDB := t.TempDir()
	orphan = writeArtifactDirectory(t, corruptDB, "0123456789abcdef", policy.Retention+artifactReconcileGrace+time.Hour, "bytes")
	if err := os.WriteFile(session.RootDatabasePath(corruptDB), []byte("not a sqlite database"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileArtifactStorage(context.Background(), corruptDB, policy, now); err == nil {
		t.Fatal("reconciliation ran against an unreadable sessions database")
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("content was removed although the reference set could not be read: %v", err)
	}
	t.Cleanup(func() { _ = session.CloseDatabases() })
}

// TestReconcileArtifactStorageNeverFollowsSymlinks keeps a planted link from
// turning the sweep into a deletion tool outside the private store.
func TestReconcileArtifactStorageNeverFollowsSymlinks(t *testing.T) {
	root, _, _ := inputTestSession(t)
	policy := DefaultAttachmentPolicy()
	victim := t.TempDir()
	victimFile := filepath.Join(victim, "precious")
	if err := os.WriteFile(victimFile, []byte("do not delete"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, artifactStorageDirectoryName), 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, artifactStorageDirectoryName, "3123456789abcdef")
	if err := os.Symlink(victim, link); err != nil {
		t.Skipf("this platform cannot create a directory symlink: %v", err)
	}
	if err := os.Chtimes(victimFile, time.Now().Add(-48*time.Hour), time.Now().Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}

	report, err := ReconcileArtifactStorage(context.Background(), root, policy, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if report.Removed != 0 {
		t.Fatalf("report = %+v, want a symlink left unrecognized rather than reclaimed", report)
	}
	if _, err := os.Stat(victimFile); err != nil {
		t.Fatalf("the reconciliation deleted through a symlink: %v", err)
	}
}

// TestReconcileArtifactStorageOpportunisticRunsOncePerInterval pins the intake
// hook: it reclaims without being asked, and a burst of intakes does not turn
// into a directory walk per attachment.
func TestReconcileArtifactStorageOpportunisticRunsOncePerInterval(t *testing.T) {
	root, _, _ := inputTestSession(t)
	policy := DefaultAttachmentPolicy()
	old := policy.Retention + artifactReconcileGrace + time.Hour

	lastArtifactReconcile.Store(0)
	first := writeArtifactDirectory(t, root, "4123456789abcdef", old, "one")
	reconcileArtifactStorageOpportunistic(root, policy)
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("the first opportunistic pass did not reclaim %s: %v", first, err)
	}

	second := writeArtifactDirectory(t, root, "5123456789abcdef", old, "two")
	reconcileArtifactStorageOpportunistic(root, policy)
	if _, err := os.Stat(second); err != nil {
		t.Fatalf("a second pass ran inside the throttle window and removed %s: %v", second, err)
	}

	lastArtifactReconcile.Store(time.Now().Add(-2 * artifactReconcileInterval).UnixNano())
	reconcileArtifactStorageOpportunistic(root, policy)
	if _, err := os.Stat(second); !os.IsNotExist(err) {
		t.Fatalf("the sweep did not resume after the interval elapsed: %v", err)
	}
	lastArtifactReconcile.Store(0)
}

func TestArtifactReclaimFloorIsRetentionPlusGrace(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	floor := ArtifactReclaimFloor(DefaultAttachmentPolicy(), now)
	want := now.Add(-(7*24*time.Hour + artifactReconcileGrace))
	if !floor.Equal(want) {
		t.Fatalf("floor = %s, want %s", floor, want)
	}
}

// writeArtifactDirectory creates a committed-looking attachment directory whose
// content is aged by the given amount below the current time.
func writeArtifactDirectory(t *testing.T, sessionDir, id string, age time.Duration, content string) string {
	t.Helper()
	dir := filepath.Join(sessionDir, artifactStorageDirectoryName, id)
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
