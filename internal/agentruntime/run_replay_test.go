package agentruntime

import (
	"testing"
	"time"

	"github.com/oschina/mothx/internal/session"
)

func TestReplayRunEventsReconstructsTerminalState(t *testing.T) {
	now := time.Now()
	events := []session.SessionRunEvent{
		{SessionID: "session-1", RunID: "run-1", EventType: "started", Status: "running", Timestamp: now},
		{SessionID: "session-1", RunID: "run-1", EventType: "decision_pending", Status: "pending", Timestamp: now.Add(time.Millisecond)},
		{SessionID: "session-1", RunID: "run-1", EventType: "finished", Status: "completed", Timestamp: now.Add(2 * time.Millisecond)},
		{SessionID: "session-1", RunID: "run-2", EventType: "failed", Status: "failed", Timestamp: now.Add(3 * time.Millisecond)},
	}
	replay := ReplayRunEvents(events, "run-1")
	if replay.SessionID != "session-1" || replay.RunID != "run-1" || len(replay.Events) != 3 {
		t.Fatalf("replay = %#v", replay)
	}
	if replay.Status != RunStateCompleted || !replay.Terminal {
		t.Fatalf("terminal state = %q/%v", replay.Status, replay.Terminal)
	}
}

func TestReplayRunEventsKeepsPendingRunNonTerminal(t *testing.T) {
	replay := ReplayRunEvents([]session.SessionRunEvent{{SessionID: "session-1", RunID: "run-1", EventType: "started", Status: "running"}}, "run-1")
	if replay.Status != RunStateRunning || replay.Terminal {
		t.Fatalf("replay = %#v", replay)
	}
}
