package terminal

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRingBufferKeepsTail(t *testing.T) {
	r := newRing(5)
	r.write([]byte("abc"))
	r.write([]byte("defg"))
	if got := string(r.snapshot()); got != "cdefg" {
		t.Fatalf("snapshot = %q, want cdefg", got)
	}
	r.resize(3)
	if got := string(r.snapshot()); got != "efg" {
		t.Fatalf("resized snapshot = %q, want efg", got)
	}
}

func TestOutputLimiterReportsThrottle(t *testing.T) {
	originalNow, originalSleep := nowNanos, sleepSeconds
	defer func() { nowNanos, sleepSeconds = originalNow, originalSleep }()
	clock := int64(0)
	nowNanos = func() int64 { return clock }
	sleepSeconds = func(seconds float64) { clock += int64(seconds * float64(time.Second)) }

	limiter := newOutputLimiter(1000)
	limiter.tokens = 0
	limiter.last = 0
	if !limiter.wait(1000) {
		t.Fatal("wait should report throttling when the bucket is empty")
	}
	limiter.tokens = 1000
	if limiter.wait(1) {
		t.Fatal("wait should not report throttling when tokens are available")
	}
}

func TestShellEnvKeepsUserVariables(t *testing.T) {
	t.Setenv("MOTHX_TERMINAL_TEST_SECRET", "kept")
	env := strings.Join(shellEnv(), "\n")
	for _, want := range []string{"MOTHX_TERMINAL_TEST_SECRET=kept", "PATH="} {
		if !strings.Contains(env, want) {
			t.Fatalf("env missing %q", want)
		}
	}
}

func TestManagerValidatesLimits(t *testing.T) {
	m := New(Config{MaxSessions: 1, MaxPerSession: 1, Shell: "/bin/sh"})
	defer m.Shutdown()
	if _, err := m.Create("", "/tmp", 80, 24); err == nil {
		t.Fatal("expected error for empty session")
	}
	if _, err := m.Create("s", "/tmp", 0, 24); err != ErrInvalidSize {
		t.Fatalf("err = %v, want ErrInvalidSize", err)
	}
}

func TestTerminalEcho(t *testing.T) {
	if runtime.GOOS == "windows" || !Supported() {
		t.Skip("PTY not supported")
	}
	m := New(Config{Shell: "/bin/sh", ScrollbackBytes: 1 << 12})
	defer m.Shutdown()
	term, err := m.Create("session", t.TempDir(), 80, 24)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sub, err := term.Attach()
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer term.Detach(sub)
	if err := term.Write([]byte("printf 'mothx-terminal-ok\\n'\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	deadline := time.After(5 * time.Second)
	var out strings.Builder
	for {
		select {
		case chunk := <-sub.Output():
			out.Write(chunk)
			if strings.Contains(out.String(), "mothx-terminal-ok") {
				return
			}
		case <-sub.Stop():
			t.Fatalf("terminal stopped before output: %q (%s)", out.String(), sub.Reason())
		case <-deadline:
			t.Fatalf("timed out waiting for output: %q", out.String())
		}
	}
}

func TestManagerPerSessionLimitDoesNotEvict(t *testing.T) {
	if runtime.GOOS == "windows" || !Supported() {
		t.Skip("PTY not supported")
	}
	m := New(Config{Shell: "/bin/sh", MaxSessions: 4, MaxPerSession: 1, ScrollbackBytes: 1 << 12})
	defer m.Shutdown()
	first, err := m.Create("session-a", t.TempDir(), 80, 24)
	if err != nil {
		t.Fatalf("Create first: %v", err)
	}
	if _, err := m.Create("session-a", t.TempDir(), 80, 24); err != ErrLimit {
		t.Fatalf("second create err = %v, want ErrLimit", err)
	}
	if _, err := m.Get("session-a", first.ID); err != nil {
		t.Fatalf("first terminal was evicted: %v", err)
	}
	if _, err := m.Create("session-b", t.TempDir(), 80, 24); err != nil {
		t.Fatalf("other session create: %v", err)
	}
}

func TestManagerShutdownIsIdempotentAndClosed(t *testing.T) {
	if runtime.GOOS == "windows" || !Supported() {
		t.Skip("PTY not supported")
	}
	m := New(Config{Shell: "/bin/sh", ScrollbackBytes: 1 << 12})
	if _, err := m.Create("session", t.TempDir(), 80, 24); err != nil {
		t.Fatalf("Create: %v", err)
	}
	m.Shutdown()
	m.Shutdown()
	if _, err := m.Create("session", t.TempDir(), 80, 24); err != ErrClosed {
		t.Fatalf("create after shutdown err = %v, want ErrClosed", err)
	}
}

func TestManagerCloseAllKeepsManagerUsable(t *testing.T) {
	if runtime.GOOS == "windows" || !Supported() {
		t.Skip("PTY not supported")
	}
	m := New(Config{Shell: "/bin/sh", ScrollbackBytes: 1 << 12})
	defer m.Shutdown()
	first, err := m.Create("session", t.TempDir(), 80, 24)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	m.CloseAll()
	if got := len(m.List("")); got != 0 {
		t.Fatalf("List after CloseAll = %d, want 0", got)
	}
	select {
	case <-first.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("CloseAll did not terminate the terminal")
	}
	second, err := m.Create("session", t.TempDir(), 80, 24)
	if err != nil {
		t.Fatalf("Create after CloseAll: %v", err)
	}
	if second.ID == first.ID {
		t.Fatal("Create after CloseAll reused the closed terminal id")
	}
}

func TestTerminalResizeRejectsInvalidSize(t *testing.T) {
	if runtime.GOOS == "windows" || !Supported() {
		t.Skip("PTY not supported")
	}
	m := New(Config{Shell: "/bin/sh", ScrollbackBytes: 1 << 12})
	defer m.Shutdown()
	term, err := m.Create("session", t.TempDir(), 80, 24)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := term.Resize(0, 24); err != ErrInvalidSize {
		t.Fatalf("Resize(0,24) err = %v, want ErrInvalidSize", err)
	}
	if err := term.Resize(120, 40); err != nil {
		t.Fatalf("Resize(120,40): %v", err)
	}
	if cols, rows := term.Size(); cols != 120 || rows != 40 {
		t.Fatalf("Size() = %dx%d, want 120x40", cols, rows)
	}
	if err := term.Resize(60000, 60000); err != nil {
		t.Fatalf("Resize(60000,60000): %v", err)
	}
	if cols, rows := term.Size(); cols != MaxCols || rows != MaxRows {
		t.Fatalf("oversized Size() = %dx%d, want %dx%d", cols, rows, MaxCols, MaxRows)
	}
}

func TestTerminalTakeoverReplaysBufferedOutput(t *testing.T) {
	if runtime.GOOS == "windows" || !Supported() {
		t.Skip("PTY not supported")
	}
	m := New(Config{Shell: "/bin/sh", ScrollbackBytes: 1 << 12})
	defer m.Shutdown()
	term, err := m.Create("session", t.TempDir(), 80, 24)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	first, err := term.Attach()
	if err != nil {
		t.Fatalf("first Attach: %v", err)
	}
	if err := term.Write([]byte("printf 'takeover-buffer-ok\\n'\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	deadline := time.After(5 * time.Second)
	var out strings.Builder
	for !strings.Contains(out.String(), "takeover-buffer-ok") {
		select {
		case chunk := <-first.Output():
			out.Write(chunk)
		case <-first.Stop():
			t.Fatalf("first subscription stopped early: %s", first.Reason())
		case <-deadline:
			t.Fatalf("timed out waiting for output: %q", out.String())
		}
	}
	second, err := term.Attach()
	if err != nil {
		t.Fatalf("second Attach: %v", err)
	}
	defer term.Detach(second)
	select {
	case <-first.Stop():
		if first.Reason() != "takeover" {
			t.Fatalf("first stop reason = %q, want takeover", first.Reason())
		}
	case <-time.After(time.Second):
		t.Fatal("first subscription was not stopped by takeover")
	}
	if !strings.Contains(string(second.Replay()), "takeover-buffer-ok") {
		t.Fatalf("replay missing buffered output: %q", string(second.Replay()))
	}
	if err := term.Write([]byte("printf 'takeover-second-ok\\n'\n")); err != nil {
		t.Fatalf("second Write: %v", err)
	}
	deadline = time.After(5 * time.Second)
	out.Reset()
	for !strings.Contains(out.String(), "takeover-second-ok") {
		select {
		case chunk := <-second.Output():
			out.Write(chunk)
		case <-second.Stop():
			t.Fatalf("second subscription stopped: %s", second.Reason())
		case <-deadline:
			t.Fatalf("timed out waiting for second output: %q", out.String())
		}
	}
}

func TestTerminalCloseIsPromptForIdleShell(t *testing.T) {
	if runtime.GOOS == "windows" || !Supported() {
		t.Skip("PTY not supported")
	}
	m := New(Config{Shell: "/bin/sh", ScrollbackBytes: 1 << 12})
	defer m.Shutdown()
	term, err := m.Create("session", t.TempDir(), 80, 24)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sub, err := term.Attach()
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer term.Detach(sub)
	// Wait until the shell has actually parsed a command before closing. Closing
	// in the startup window would test the signal disposition race rather than
	// the steady-state behavior (interactive dash/bash ignore SIGTERM).
	if err := term.Write([]byte("printf '\\122EADY\\n'\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	deadline := time.After(5 * time.Second)
	var out strings.Builder
	for !strings.Contains(out.String(), "READY") {
		select {
		case chunk := <-sub.Output():
			out.Write(chunk)
		case <-deadline:
			t.Fatalf("idle shell did not become ready: %q", out.String())
		}
	}
	start := time.Now()
	if err := term.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= closeGracePeriod {
		t.Fatalf("Close took %s for an idle shell; expected immediate termination", elapsed)
	}
	select {
	case <-term.Done():
	case <-time.After(time.Second):
		t.Fatal("terminal did not reach a terminal state after Close")
	}
}

func TestTerminalCloseEscalatesToKill(t *testing.T) {
	if runtime.GOOS == "windows" || !Supported() {
		t.Skip("PTY not supported")
	}
	dir := t.TempDir()
	shell := filepath.Join(dir, "stubborn.sh")
	script := "#!/bin/sh\ntrap '' HUP TERM\necho ready\nwhile :; do sleep 1; done\n"
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatalf("write shell: %v", err)
	}
	m := New(Config{Shell: shell, ScrollbackBytes: 1 << 12})
	defer m.Shutdown()
	term, err := m.Create("session", dir, 80, 24)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sub, err := term.Attach()
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer term.Detach(sub)
	deadline := time.After(5 * time.Second)
	var out strings.Builder
	for !strings.Contains(out.String(), "ready") {
		select {
		case chunk := <-sub.Output():
			out.Write(chunk)
		case <-deadline:
			t.Fatalf("stubborn shell did not report readiness: %q", out.String())
		}
	}
	start := time.Now()
	if err := term.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < closeGracePeriod/2 {
		t.Fatalf("Close returned after %s; expected the HUP/TERM-ignoring shell to consume the grace period", elapsed)
	}
	if elapsed > closeGracePeriod+3*time.Second {
		t.Fatalf("Close took %s; SIGKILL escalation did not take effect", elapsed)
	}
	select {
	case <-term.Done():
	case <-time.After(time.Second):
		t.Fatal("terminal did not reach a terminal state after forced close")
	}
}

func TestManagerAuditLogsLifecycleWithoutOutput(t *testing.T) {
	if runtime.GOOS == "windows" || !Supported() {
		t.Skip("PTY not supported")
	}
	var mu sync.Mutex
	var events []string
	m := New(Config{Shell: "/bin/sh", ScrollbackBytes: 1 << 12})
	m.SetLogger(func(format string, args ...any) {
		mu.Lock()
		events = append(events, fmt.Sprintf(format, args...))
		mu.Unlock()
	})
	defer m.Shutdown()
	term, err := m.Create("audit-session", t.TempDir(), 80, 24)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := term.Write([]byte("echo mothx-audit-secret\nexit\n")); err != nil && err != ErrExited {
		t.Fatalf("Write: %v", err)
	}
	select {
	case <-term.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("terminal did not exit")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		joined := strings.Join(events, "\n")
		mu.Unlock()
		if strings.Contains(joined, "terminal_created") && strings.Contains(joined, "terminal_exited") {
			if strings.Contains(joined, "mothx-audit-secret") {
				t.Fatalf("audit log leaked terminal output: %s", joined)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Fatalf("missing lifecycle audit events: %v", events)
}

func TestTerminalReportsExitCode(t *testing.T) {
	if runtime.GOOS == "windows" || !Supported() {
		t.Skip("PTY not supported")
	}
	m := New(Config{Shell: "/bin/sh", ScrollbackBytes: 1 << 12})
	defer m.Shutdown()
	term, err := m.Create("exit-code", t.TempDir(), 80, 24)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := term.Write([]byte("exit 7\n")); err != nil && err != ErrExited {
		t.Fatalf("Write: %v", err)
	}
	select {
	case <-term.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("terminal did not exit")
	}
	if code := term.ExitCode(); code != 7 {
		t.Fatalf("ExitCode = %d, want 7", code)
	}
}

func TestTerminalShortLivedExit(t *testing.T) {
	if runtime.GOOS == "windows" || !Supported() {
		t.Skip("PTY not supported")
	}
	m := New(Config{Shell: "/bin/sh", MaxSessions: 64, MaxPerSession: 64, ScrollbackBytes: 1 << 12})
	defer m.Shutdown()
	for i := 0; i < 24; i++ {
		term, err := m.Create("short", t.TempDir(), 40, 12)
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		if err := term.Write([]byte("exit\n")); err != nil && err != ErrExited {
			t.Fatalf("Write %d: %v", i, err)
		}
		select {
		case <-term.Done():
		case <-time.After(5 * time.Second):
			t.Fatalf("terminal %d did not exit", i)
		}
		m.CloseSession("short")
	}
}
