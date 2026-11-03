//go:build !windows

package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestACPProcessLauncherReapsDescendantTree proves cancellation kills the whole
// process group, not only the ACP parent. A shell starts a background child,
// records its PID, and waits; after Terminate both must be gone.
func TestACPProcessLauncherReapsDescendantTree(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	script := "sleep 60 & echo $! > " + pidFile + "; wait"
	process, err := (acpProcessLauncher{}).Start(context.Background(),
		[]string{"/bin/sh", "-c", script}, dir, os.Environ(), nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var childPID int
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(pidFile)
		if readErr == nil {
			childPID, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			if childPID > 0 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if childPID <= 0 {
		process.Terminate()
		_ = process.Wait()
		t.Fatal("fixture did not start a descendant process")
	}
	if err := syscall.Kill(childPID, 0); err != nil {
		process.Terminate()
		_ = process.Wait()
		t.Fatalf("descendant %d is not running before terminate: %v", childPID, err)
	}

	process.Terminate()
	_ = process.Wait()

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(childPID, 0); err == syscall.ESRCH {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("descendant %d survived process-tree termination", childPID)
}
