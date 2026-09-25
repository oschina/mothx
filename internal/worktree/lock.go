package worktree

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// fileLock is a best-effort cross-process lock for one worktree directory. It
// uses an O_CREATE|O_EXCL lock file beside the managed worktrees: portable
// across platforms, dependency-free, and self-healing (a lock older than
// staleTTL is treated as abandoned and reclaimed). It complements, but never
// replaces, the Runtime's in-process per-directory lock.
type fileLock struct {
	path     string
	staleTTL time.Duration
	interval time.Duration
	timeout  time.Duration
}

func newFileLock(root, key string) *fileLock {
	sum := sha1.Sum([]byte(filepath.Clean(key)))
	return &fileLock{
		path:     filepath.Join(root, ".locks", hex.EncodeToString(sum[:])[:16]+".lock"),
		staleTTL: 5 * time.Minute,
		interval: 50 * time.Millisecond,
		timeout:  30 * time.Second,
	}
}

// acquire blocks until the lock is held or ctx/timeout expires, returning a
// release function.
func (l *fileLock) acquire(ctx context.Context) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(l.timeout)
	for {
		file, err := os.OpenFile(l.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = fmt.Fprintf(file, "%d %d\n", os.Getpid(), time.Now().UnixNano())
			_ = file.Close()
			return func() { _ = os.Remove(l.path) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if info, statErr := os.Stat(l.path); statErr == nil && time.Since(info.ModTime()) > l.staleTTL {
			_ = os.Remove(l.path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, &Error{Code: CodeResetFailed, Message: "timed out acquiring the worktree lock"}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(l.interval):
		}
	}
}

// LockDir acquires a cross-process lock for one worktree directory under the
// manager root. The returned release function is always non-nil on success.
func (m *Manager) LockDir(ctx context.Context, directory string) (func(), error) {
	return newFileLock(m.root, directory).acquire(ctx)
}
