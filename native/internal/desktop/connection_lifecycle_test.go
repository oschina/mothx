package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oschina/mothx/native/internal/feature/hosttest"
)

// dispatchQueue adapts the shared UI-thread stand-in to the shell's unexported
// post hook. Work runs only when the test drains the queue on its goroutine.
type dispatchQueue struct {
	*hosttest.Queue
}

func (q *dispatchQueue) post(fn func()) { q.Queue.Post(fn) }
func (q *dispatchQueue) drain()         { q.Queue.Drain() }
func (q *dispatchQueue) runUntil(t *testing.T, timeout time.Duration, done func() bool) bool {
	return q.Queue.RunUntil(t, timeout, done)
}

func installDispatchQueue(a *nativeApp) *dispatchQueue {
	q := &dispatchQueue{Queue: hosttest.NewQueue()}
	a.post = q.post
	return q
}

// TestACPDesktopChild is the child side of the connection and prompt
// lifecycle tests; hosttest.ServeChild documents the scenarios.
func TestACPDesktopChild(t *testing.T) {
	hosttest.ServeChild(os.Getenv(hosttest.EnvChild))
}

func acpChildShim(t *testing.T, scenario string) string {
	return hosttest.ChildShim(t, "TestACPDesktopChild", scenario)
}

// connectTestApp wires the dispatch queue, starts from a not-yet-connected
// shell (testApp begins already connected, which would make "wait until
// ready" vacuous), and makes sure every child the test started is stopped
// when it ends.
func connectTestApp(t *testing.T) (*nativeApp, *dispatchQueue) {
	t.Helper()
	a := testApp()
	a.conn = connIdle
	queue := installDispatchQueue(a)
	previous := launchBinaryOverride
	t.Cleanup(func() {
		launchBinaryOverride = previous
		a.closing.Store(true)
		queue.drain()
		if client := a.client(); client != nil {
			client.Close()
		}
	})
	return a, queue
}

func clientClosed(client *acpClient) bool {
	return client != nil && client.Closed()
}

func connectAttemptsIdle(a *nativeApp) bool {
	a.connectMu.Lock()
	defer a.connectMu.Unlock()
	return a.activeConnect == nil
}

// A person switches the runtime binary while the old one is still stuck in its
// handshake. The old attempt fails later, after the new binary connected. Its
// failure must not turn the healthy connection into an error card, schedule a
// restart, or leave the healthy child running unseen.
func TestSupersededConnectFailureLeavesTheHealthyConnectionAlone(t *testing.T) {
	a, queue := connectTestApp(t)
	release := filepath.Join(t.TempDir(), "release")
	t.Setenv("MOTHX_NATIVE_ACP_TEST_RELEASE", release)

	setLaunchBinaryOverride(acpChildShim(t, "hang"))
	go a.connect()
	if !queue.runUntil(t, 20*time.Second, func() bool { return a.client() != nil }) {
		t.Fatal("the first attempt never started its child")
	}
	hung := a.client()

	setLaunchBinaryOverride(acpChildShim(t, "serve"))
	go a.connect()
	if !queue.runUntil(t, 20*time.Second, func() bool { return a.conn == connReady }) {
		t.Fatalf("the second attempt never connected: conn=%d err=%q", a.conn, a.connErr)
	}
	healthy := a.client()
	if healthy == nil || healthy == hung {
		t.Fatal("the second attempt did not install its own child")
	}

	// Only now does the stuck binary fail.
	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	settled := queue.runUntil(t, 20*time.Second, func() bool {
		return clientClosed(hung) && connectAttemptsIdle(a)
	})
	for attempt := 0; attempt < 20 && settled; attempt++ {
		// A restart would be posted by a timer; give it room to show up.
		time.Sleep(50 * time.Millisecond)
		queue.drain()
	}

	if a.conn != connReady {
		t.Fatalf("a superseded failure changed the connection state: conn=%d err=%q", a.conn, a.connErr)
	}
	if a.client() != healthy {
		t.Fatal("a superseded failure replaced the healthy child")
	}
	if clientClosed(healthy) {
		t.Fatal("the healthy child was closed")
	}
	if !clientClosed(hung) {
		t.Fatal("the superseded child was left running")
	}
	if a.autoRestartPending || a.autoRestartAttempts != 0 {
		t.Fatalf("a superseded failure scheduled a restart: pending=%v attempts=%d",
			a.autoRestartPending, a.autoRestartAttempts)
	}
}

// connect() is the only place a child is started, so it is also the place that
// stops the child it replaces. A replaced child used to be left running for
// the rest of the app's life.
func TestConnectClosesTheChildItReplaces(t *testing.T) {
	a, queue := connectTestApp(t)

	setLaunchBinaryOverride(acpChildShim(t, "serve"))
	go a.connect()
	if !queue.runUntil(t, 20*time.Second, func() bool {
		return a.conn == connReady && connectAttemptsIdle(a) && a.client() != nil
	}) {
		t.Fatalf("the first attempt never connected: conn=%d err=%q", a.conn, a.connErr)
	}
	first := a.client()
	if first == nil || first.Closed() {
		t.Fatal("the first child must be installed and connected")
	}

	go a.connect()
	if !queue.runUntil(t, 20*time.Second, func() bool {
		return a.conn == connReady && connectAttemptsIdle(a) && a.client() != nil && a.client() != first
	}) {
		t.Fatalf("the second attempt never connected: conn=%d err=%q", a.conn, a.connErr)
	}
	second := a.client()
	if !queue.runUntil(t, 20*time.Second, func() bool { return first.Closed() }) {
		t.Fatal("the replaced child was left running")
	}
	if clientClosed(second) {
		t.Fatal("the new child must stay connected")
	}
	if seq := atomic.LoadInt64(&a.reloadSeq); seq != 2 {
		t.Fatalf("reloadSeq = %d, want 2", seq)
	}
}

// Two overlapping requests are ordered, not raced: the request that arrives
// second owns the connection, whichever child finishes its handshake first.
func TestLatestConnectRequestOwnsTheConnection(t *testing.T) {
	a, queue := connectTestApp(t)
	setLaunchBinaryOverride(acpChildShim(t, "serve"))

	go a.connect()
	go a.connect()
	if !queue.runUntil(t, 20*time.Second, func() bool {
		return a.conn == connReady && connectAttemptsIdle(a) && a.client() != nil
	}) {
		t.Fatalf("the connection never became ready: conn=%d err=%q", a.conn, a.connErr)
	}
	current := a.client()
	if clientClosed(current) {
		t.Fatal("the owning child is closed")
	}
	if seq := atomic.LoadInt64(&a.reloadSeq); seq != 2 {
		t.Fatalf("reloadSeq = %d, want 2", seq)
	}
}

// The diagnostic log is rendered as it is in every locale, so what connect()
// writes into it must carry identifiers, not Chinese display text.
func TestConnectDiagnosticsCarryNoDisplayText(t *testing.T) {
	a, queue := connectTestApp(t)
	setLaunchBinaryOverride(acpChildShim(t, "serve"))
	go a.connect()
	if !queue.runUntil(t, 20*time.Second, func() bool {
		return a.conn == connReady && connectAttemptsIdle(a)
	}) {
		t.Fatalf("the runtime never connected: conn=%d err=%q", a.conn, a.connErr)
	}
	for _, entry := range diagnosticSnapshot() {
		if !strings.HasPrefix(entry.Text, "connect:") {
			continue
		}
		if hasHan(entry.Text) {
			t.Fatalf("connect() wrote display text into the diagnostics: %q", entry.Text)
		}
		if !strings.Contains(entry.Text, "source=launch-arg") {
			t.Fatalf("connect() must still record the runtime source: %q", entry.Text)
		}
	}
}

func hasHan(text string) bool {
	for _, r := range text {
		if r >= 0x4e00 && r <= 0x9fff {
			return true
		}
	}
	return false
}
