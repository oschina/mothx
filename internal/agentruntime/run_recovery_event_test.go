package agentruntime

import (
	"testing"
	"time"

	"github.com/oschina/mothx/internal/session"
)

func TestRecoverOrphanedRunsRecordsRecoveryEvent(t *testing.T) {
	sessionDir := t.TempDir()
	initRecoveryTestSession(t, sessionDir, "session-1")
	store := RunStore{SessionDir: sessionDir}
	if err := store.Create(DurableRun{ID: "run-1", SessionID: "session-1", Source: "acp", Status: "running", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := RecoverOrphanedRuns(sessionDir, nil, nil); err != nil {
		t.Fatal(err)
	}
	events, err := session.ListSessionRunEvents(sessionDir, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].EventType != "recovered" || events[0].Status != "failed" {
		t.Fatalf("events = %#v", events)
	}
}
