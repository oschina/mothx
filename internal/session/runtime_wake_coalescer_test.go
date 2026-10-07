package session

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSessionWakeCoalescerCollapsesBursts pins the receive-side bound: a burst
// of advisory wakes for one session runs the handler exactly once in flight
// plus one trailing re-run, never one goroutine per datagram.
func TestSessionWakeCoalescerCollapsesBursts(t *testing.T) {
	started := make(chan string, 16)
	release := make(chan struct{})
	var calls atomic.Int64
	coalescer := NewSessionWakeCoalescer(func(sessionID string) {
		calls.Add(1)
		started <- sessionID
		<-release
	})

	for i := 0; i < 50; i++ {
		coalescer.Wake("burst-session")
	}
	select {
	case id := <-started:
		if id != "burst-session" {
			t.Fatalf("handler session = %q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	// While the first invocation is in flight, the queued wakes must not start
	// a second concurrent handler for the same session.
	select {
	case <-started:
		t.Fatal("coalescer started a concurrent handler for the same session")
	case <-time.After(50 * time.Millisecond):
	}

	release <- struct{}{}
	// Exactly one trailing re-run drains the burst so the final durable state
	// is still re-read after the last wake.
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("trailing re-run did not start")
	}
	release <- struct{}{}

	select {
	case <-started:
		t.Fatal("coalescer re-ran more than once after the burst")
	case <-time.After(100 * time.Millisecond):
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("handler calls = %d, want exactly 2 (in-flight + trailing)", got)
	}
}

// TestSessionWakeCoalescerKeepsSessionsIndependent proves coalescing is
// per-session: distinct sessions never wait on each other.
func TestSessionWakeCoalescerKeepsSessionsIndependent(t *testing.T) {
	done := make(chan string, 4)
	coalescer := NewSessionWakeCoalescer(func(sessionID string) {
		done <- sessionID
	})
	coalescer.Wake("session-a")
	coalescer.Wake("session-b")
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case id := <-done:
			seen[id] = true
		case <-time.After(time.Second):
			t.Fatal("wake did not run for both sessions")
		}
	}
	if !seen["session-a"] || !seen["session-b"] {
		t.Fatalf("seen = %#v, want both sessions", seen)
	}
}

// TestSessionWakeCoalescerIgnoresDegenerateInputs keeps nil receivers, nil
// handlers, and blank session IDs inert instead of panicking.
func TestSessionWakeCoalescerIgnoresDegenerateInputs(t *testing.T) {
	calls := atomic.Int64{}
	coalescer := NewSessionWakeCoalescer(func(string) { calls.Add(1) })
	coalescer.Wake("")
	coalescer.Wake("   ")

	var nilCoalescer *SessionWakeCoalescer
	nilCoalescer.Wake("session")

	noHandler := NewSessionWakeCoalescer(nil)
	noHandler.Wake("session")

	time.Sleep(20 * time.Millisecond)
	if got := calls.Load(); got != 0 {
		t.Fatalf("degenerate wakes ran the handler %d times", got)
	}
}

// TestSessionWakeCoalescerConcurrentWakes exercises the locking under -race
// with many sessions and many concurrent wakers.
func TestSessionWakeCoalescerConcurrentWakes(t *testing.T) {
	var calls atomic.Int64
	var wg sync.WaitGroup
	coalescer := NewSessionWakeCoalescer(func(string) { calls.Add(1) })
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				coalescer.Wake("session-" + string(rune('a'+i%4)))
			}
		}(worker)
	}
	wg.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		coalescer.mu.Lock()
		idle := len(coalescer.running) == 0 && len(coalescer.dirty) == 0
		coalescer.mu.Unlock()
		if idle {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	coalescer.mu.Lock()
	defer coalescer.mu.Unlock()
	if len(coalescer.running) != 0 || len(coalescer.dirty) != 0 {
		t.Fatalf("coalescer did not drain: running=%v dirty=%v", coalescer.running, coalescer.dirty)
	}
	if got := calls.Load(); got < 4 {
		t.Fatalf("handler calls = %d, want at least one per session", got)
	}
}
