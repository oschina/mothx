package tui

import (
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/agent"
)

// TestRetireEventStreamKeepsDrainingProducer guards the abort path: the Agent
// loop sends events without selecting on a context, so a stream abandoned with
// a full buffer would block the aborted run forever. retireEventStream must
// detach the UI channel and keep consuming it until the producer closes it.
func TestRetireEventStreamKeepsDrainingProducer(t *testing.T) {
	a := &App{}
	ch := make(chan agent.Event) // unbuffered: any unread send blocks
	a.eventCh = ch

	a.retireEventStream()
	if a.eventCh != nil {
		t.Fatal("retireEventStream did not detach the UI event channel")
	}

	done := make(chan struct{})
	go func() {
		ch <- agent.Event{Type: agent.EventTextDelta, TextDelta: "late delta"}
		ch <- agent.Event{Type: agent.EventRunFinished}
		close(ch)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("producer blocked: the retired stream is not being drained")
	}
}
