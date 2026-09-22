package agentruntime

import (
	"testing"
	"time"

	"github.com/oschina/mothx/internal/session"
)

func TestRecoverOrphanedRunsIsIdempotentAfterFirstPass(t *testing.T) {
	sessionDir := t.TempDir()
	initRecoveryTestSession(t, sessionDir, "session-1")
	store := RunStore{SessionDir: sessionDir}
	if err := store.Create(DurableRun{ID: "run-1", SessionID: "session-1", Source: "tui", Status: "running", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	first, err := RecoverOrphanedRuns(sessionDir, nil, nil)
	if err != nil || len(first.Failed) != 1 {
		t.Fatalf("first recovery = %#v, err=%v", first, err)
	}
	second, err := RecoverOrphanedRuns(sessionDir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Failed) != 0 && len(second.Kept) != 0 {
		t.Fatalf("second recovery = %#v", second)
	}
	run, err := session.GetSessionRun(sessionDir, "run-1")
	if err != nil || run == nil || run.Status != "failed" {
		t.Fatalf("run after recovery = %#v, err=%v", run, err)
	}
}
