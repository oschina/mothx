package agentruntime

import (
	"testing"
	"time"

	"github.com/oschina/mothx/internal/session"
)

func TestRunStoreCreateAndFinish(t *testing.T) {
	sessionDir := t.TempDir()
	store := RunStore{SessionDir: sessionDir}
	now := time.Now()
	if err := store.Create(DurableRun{ID: "run-1", SessionID: "session-1", WorkDir: t.TempDir(), Source: "acp", Mode: "agent", Status: "running", StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.Finish("run-1", RunStateFailed, "process restarted"); err != nil {
		t.Fatal(err)
	}
	run, err := session.GetSessionRun(sessionDir, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if run == nil || run.Status != "failed" || run.Error != "process restarted" || run.FinishedAt == nil {
		t.Fatalf("run = %#v", run)
	}
}

func TestRunStoreRejectsNonTerminalFinish(t *testing.T) {
	store := RunStore{SessionDir: t.TempDir()}
	if err := store.Finish("run-1", RunStateRunning, ""); err == nil {
		t.Fatal("Finish accepted non-terminal state")
	}
}
