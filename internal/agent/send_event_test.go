package agent

import (
	"context"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/tools"
)

// TestSendEventStopsWhenRunContextIsDone is the mechanism behind the
// "aborted run parks on a full channel" fix: once the run context is done, an
// event send must fail fast instead of blocking on a channel whose consumer
// stopped reading; while the run is live it must still block until delivery.
func TestSendEventStopsWhenRunContextIsDone(t *testing.T) {
	a := New(Config{ID: "send-event", Mode: "yolo"}, tools.NewRegistry(t.TempDir(), nil))
	ch := make(chan Event) // unbuffered: a bare send would block forever

	ctx, cancel := context.WithCancel(context.Background())
	a.setRunContext(ctx)
	cancel()

	done := make(chan bool, 1)
	go func() { done <- a.sendEvent(ch, Event{Type: EventTextDelta, TextDelta: "late"}) }()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("sendEvent reported success for a cancelled run")
		}
	case <-time.After(time.Second):
		t.Fatal("sendEvent parked on a cancelled run with no consumer")
	}

	a.setRunContext(context.Background())
	go func() { done <- a.sendEvent(ch, Event{Type: EventTextDelta, TextDelta: "live"}) }()
	select {
	case <-done:
		t.Fatal("sendEvent delivered without a reader")
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("live run event was never delivered")
	}
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("sendEvent failed for a live run")
		}
	case <-time.After(time.Second):
		t.Fatal("sendEvent did not return once the consumer read")
	}
}

// TestSendEventDoesNotTakeTheMessageLock guards the steering-injection path:
// the loop holds a.mu while it injects steering messages and sends their
// events, so sendEvent must not acquire a.mu itself (a non-reentrant RWMutex
// would deadlock the whole run).
func TestSendEventDoesNotTakeTheMessageLock(t *testing.T) {
	a := New(Config{ID: "send-lock", Mode: "yolo"}, tools.NewRegistry(t.TempDir(), nil))
	ch := make(chan Event, 1)
	a.setRunContext(context.Background())

	a.mu.Lock()
	done := make(chan bool, 1)
	go func() { done <- a.sendEvent(ch, Event{Type: EventMessageStart}) }()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("sendEvent failed for a live run")
		}
	case <-time.After(time.Second):
		a.mu.Unlock()
		t.Fatal("sendEvent blocked while the caller held the message lock")
	}
	a.mu.Unlock()
}

// TestSendEventCountsDroppedEvents keeps the cancellation drop observable: a
// consumer that stopped reading must not silently swallow the fact that events
// were abandoned.
func TestSendEventCountsDroppedEvents(t *testing.T) {
	a := New(Config{ID: "send-drop", Mode: "yolo"}, tools.NewRegistry(t.TempDir(), nil))
	ch := make(chan Event)
	ctx, cancel := context.WithCancel(context.Background())
	a.setRunContext(ctx)
	cancel()

	for i := 0; i < 3; i++ {
		if a.sendEvent(ch, Event{Type: EventTextDelta}) {
			t.Fatal("sendEvent reported success for a cancelled run")
		}
	}
	if got := a.droppedEvents.Load(); got != 3 {
		t.Fatalf("dropped events = %d, want 3", got)
	}

	// A new run resets the counter.
	a.setRunContext(context.Background())
	if got := a.droppedEvents.Load(); got != 0 {
		t.Fatalf("dropped events after a new run = %d, want 0", got)
	}
}
