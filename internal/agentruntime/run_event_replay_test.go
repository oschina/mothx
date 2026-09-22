package agentruntime

import (
	"testing"

	"github.com/oschina/mothx/internal/session"
)

func TestSessionRunEventSinkPreservesInsertionOrder(t *testing.T) {
	sessionDir := t.TempDir()
	sink := SessionRunEventSink{SessionDir: sessionDir}
	for _, event := range []RunEvent{
		{SessionID: "session-1", RunID: "run-1", EventType: "started", Source: "runtime", Status: "running"},
		{SessionID: "session-1", RunID: "run-1", EventType: "decision_pending", Source: "runtime", Status: "pending"},
		{SessionID: "session-1", RunID: "run-1", EventType: "finished", Source: "runtime", Status: "completed"},
	} {
		if _, err := sink.Record(event); err != nil {
			t.Fatal(err)
		}
	}
	events, err := session.ListSessionRunEvents(sessionDir, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %#v", events)
	}
	for i, want := range []string{"started", "decision_pending", "finished"} {
		if events[i].EventType != want || events[i].RunID != "run-1" {
			t.Fatalf("event[%d] = %#v, want %s/run-1", i, events[i], want)
		}
	}
}
