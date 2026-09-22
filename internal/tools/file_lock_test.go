package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/sandbox"
)

func TestFileLockManagerAcquireWaitsAndCancels(t *testing.T) {
	mgr := NewFileLockManager()
	path := filepath.Join(t.TempDir(), "locked.txt")

	release, err := mgr.Acquire(context.Background(), path, "first")
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := mgr.Acquire(ctx, path, "second")
		errCh <- err
	}()

	select {
	case err := <-errCh:
		t.Fatalf("second acquire returned before release: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected canceled acquire to return an error")
		}
		if !strings.Contains(err.Error(), context.Canceled.Error()) {
			t.Fatalf("expected cancellation error, got: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for canceled acquire")
	}
}

func TestFileLockManagerCancelDoesNotPoisonLock(t *testing.T) {
	mgr := NewFileLockManager()
	path := filepath.Join(t.TempDir(), "locked.txt")

	release, err := mgr.Acquire(context.Background(), path, "first")
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := mgr.Acquire(ctx, path, "second")
		errCh <- err
	}()
	cancel()
	if err := <-errCh; err == nil {
		t.Fatal("expected canceled acquire to fail")
	}
	release()

	releaseAgain, err := mgr.Acquire(context.Background(), path, "third")
	if err != nil {
		t.Fatalf("acquire after canceled waiter: %v", err)
	}
	releaseAgain()
}

func TestRegistriesShareDefaultFileLockManager(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r1 := NewRegistry(t.TempDir(), sb)
	r2 := NewRegistry(t.TempDir(), sb)

	if r1.FileLocks() == nil {
		t.Fatal("expected registry file lock manager")
	}
	if r1.FileLocks() != r2.FileLocks() {
		t.Fatal("expected default registries to share the process-wide file lock manager")
	}
}

func TestWriteToolWaitsForFileLockAndHonorsContext(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "target.txt")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	mgr := NewFileLockManager()
	release, err := mgr.Acquire(context.Background(), path, "test")
	if err != nil {
		t.Fatalf("acquire fixture lock: %v", err)
	}
	defer release()

	r := NewRegistryWithConfig(RegistryConfig{
		WorkDir:   tmpDir,
		Sandbox:   sandbox.NewNoneSandbox(),
		FileLocks: mgr,
	})
	tool := NewWriteTool(r)

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err = tool.Execute(ctx, map[string]any{
		"path":    "target.txt",
		"content": "new",
	})
	if err == nil {
		t.Fatal("expected write to fail while waiting for file lock")
	}
	if !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("expected deadline error, got: %v", err)
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read target: %v", readErr)
	}
	if string(data) != "old" {
		t.Fatalf("content changed while lock was held: %q", string(data))
	}
}

func TestEditToolWaitsForFileLockAndHonorsContext(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "target.txt")
	if err := os.WriteFile(path, []byte("alpha beta"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	mgr := NewFileLockManager()
	release, err := mgr.Acquire(context.Background(), path, "test")
	if err != nil {
		t.Fatalf("acquire fixture lock: %v", err)
	}
	defer release()

	r := NewRegistryWithConfig(RegistryConfig{
		WorkDir:   tmpDir,
		Sandbox:   sandbox.NewNoneSandbox(),
		FileLocks: mgr,
	})
	tool := NewEditTool(r)

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err = tool.Execute(ctx, map[string]any{
		"path": "target.txt",
		"edits": []any{
			map[string]any{"oldText": "beta", "newText": "gamma"},
		},
	})
	if err == nil {
		t.Fatal("expected edit to fail while waiting for file lock")
	}
	if !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("expected deadline error, got: %v", err)
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read target: %v", readErr)
	}
	if string(data) != "alpha beta" {
		t.Fatalf("content changed while lock was held: %q", string(data))
	}
}
