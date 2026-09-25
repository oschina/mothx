package worktree

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileLockExclusiveAndRelease(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root, "mothx")

	release, err := m.LockDir(context.Background(), "/repo/wt")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	// A second acquire with a short timeout must fail while the lock is held.
	blocked := newFileLock(root, "/repo/wt")
	blocked.staleTTL = time.Hour
	blocked.interval = 10 * time.Millisecond
	blocked.timeout = 100 * time.Millisecond
	if _, err := blocked.acquire(context.Background()); err == nil {
		t.Fatal("expected the second acquire to time out")
	}
	release()

	release2, err := m.LockDir(context.Background(), "/repo/wt")
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	release2()
}

func TestFileLockReclaimsStaleLock(t *testing.T) {
	root := t.TempDir()
	lock := newFileLock(root, "/repo/stale")
	if err := os.MkdirAll(filepath.Dir(lock.path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock.path, []byte("999999 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(lock.path, old, old); err != nil {
		t.Fatal(err)
	}
	lock.staleTTL = time.Minute
	lock.timeout = time.Second
	lock.interval = 5 * time.Millisecond

	release, err := lock.acquire(context.Background())
	if err != nil {
		t.Fatalf("stale lock was not reclaimed: %v", err)
	}
	release()
}
