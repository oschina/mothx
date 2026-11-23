package acp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Prompt turns are intentionally unbounded; every other request has a bounded
// default so a wedged child cannot leave a management panel loading forever.
func TestRequestTimeoutPolicy(t *testing.T) {
	if timeout, bounded := requestTimeout("session/prompt"); bounded || timeout != 0 {
		t.Fatalf("session/prompt timeout = (%v, %v), want unbounded", timeout, bounded)
	}
	for _, method := range []string{"initialize", "session/load", "mothx/manage/providers/list", "mothx/worktree/create"} {
		timeout, bounded := requestTimeout(method)
		if !bounded || timeout != defaultRequestTimeout {
			t.Fatalf("%s timeout = (%v, %v), want (%v, true)", method, timeout, bounded, defaultRequestTimeout)
		}
	}
}

// caller's context instead of blocking a worker forever. The prompt path keeps
// using the unbounded Call, so this only covers the handshake-style timeout.
func TestCallContextReturnsOnDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell helper")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-acp.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat >/dev/null\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	client := NewClient(nil, nil, nil)
	if err := client.Start(script, dir, os.Environ()); err != nil {
		t.Fatalf("start fake ACP child: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := client.CallContext(ctx, "initialize", map[string]any{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("CallContext error = %v, want context deadline", err)
	}
}
