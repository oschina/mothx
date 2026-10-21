package agentruntime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/session"
)

// A canonical Run terminalized by another owner must be adopted even when the
// local terminal state differs. Otherwise MarkTerminalizing keeps failing, the
// local execution registration stays active, and the session looks permanently
// busy.
func TestFinishDurableAdoptsCanonicalTerminalState(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "sessions")
	mgr := session.New(t.TempDir(), sessionDir)
	if err := mgr.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	sessionID := mgr.GetHeader().ID

	execution := &ExecutionRuntime{}
	execution.SetRunStore(RunStore{SessionDir: sessionDir})
	execution.SetEventSink(SessionRunEventSink{SessionDir: sessionDir})
	observer := make(chan RunState, 1)
	execution.SetTerminalObserver(func(runID string, state RunState) {
		if runID == "adopt-terminal-run" {
			observer <- state
		}
	})

	guard, err := session.AcquireExecutionAdmission(sessionDir, sessionID)
	if err != nil {
		t.Fatalf("acquire admission: %v", err)
	}
	runID := "adopt-terminal-run"
	intent := ExecutionIntent{ID: "intent-adopt-terminal", SessionID: sessionID, Source: "test", CreatedAt: time.Now()}
	if _, err := execution.BeginIntentDurable(context.Background(), intent, DurableRun{
		ID: runID, SessionID: sessionID, IntentID: intent.ID, Source: "test", Status: "running", StartedAt: time.Now(),
	}, RunEvent{SessionID: sessionID, RunID: runID, EventType: "started", Source: "test", Status: "running", Timestamp: time.Now()}); err != nil {
		guard.Release()
		t.Fatalf("begin durable run: %v", err)
	}
	guard.Release()

	// Another owner (for example user-triggered orphan recovery) terminalizes
	// the row first, as cancelled. The local loop later reports failed.
	finishedAt := time.Now()
	if err := session.UpdateSessionRunStatus(sessionDir, runID, "cancelled", "external terminalization", &finishedAt); err != nil {
		t.Fatalf("terminalize canonical row: %v", err)
	}

	if err := execution.FinishDurable(runID, RunStateFailed, "", RunEvent{
		SessionID: sessionID, RunID: runID, EventType: "failed", Source: "test", Status: "failed", Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("adopt canonical terminal row: %v", err)
	}
	if _, active := execution.Active(); active {
		t.Fatal("execution stayed active after adopting the canonical terminal row")
	}
	select {
	case state := <-observer:
		if state != RunStateCancelled {
			t.Fatalf("terminal observer state = %q, want canonical cancelled", state)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("terminal observer was not notified of the adopted terminal state")
	}
	stored, err := GetDurableRun(context.Background(), sessionDir, runID)
	if err != nil {
		t.Fatalf("load canonical run: %v", err)
	}
	if stored == nil || stored.Status != "cancelled" {
		t.Fatalf("canonical run = %#v, want cancelled", stored)
	}
	snapshot, err := InspectSessionExecution(sessionDir, sessionID)
	if err != nil {
		t.Fatalf("inspect execution: %v", err)
	}
	if snapshot.Busy {
		t.Fatalf("session stayed busy after adoption: %#v", snapshot)
	}
}

func TestRunStateFromDurableStatus(t *testing.T) {
	cases := map[string]RunState{
		"completed":  RunStateCompleted,
		"incomplete": RunStateIncomplete,
		"failed":     RunStateFailed,
		"expired":    RunStateFailed,
		"cancelled":  RunStateCancelled,
		"canceled":   RunStateCancelled,
		"timed_out":  RunStateTimedOut,
	}
	for status, want := range cases {
		got, ok := runStateFromDurableStatus(status)
		if !ok || got != want {
			t.Fatalf("runStateFromDurableStatus(%q) = %q, %v; want %q, true", status, got, ok, want)
		}
	}
	for _, status := range []string{"running", "cancelling", "terminalizing", "unknown"} {
		if state, ok := runStateFromDurableStatus(status); ok {
			t.Fatalf("runStateFromDurableStatus(%q) = %q, true; want false", status, state)
		}
	}
}
