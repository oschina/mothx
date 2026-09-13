package session

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/provider"
)

func TestForkSessionAndMessageBoundary(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := New(t.TempDir(), sessionDir)
	if err := mgr.InitWithID("source"); err != nil {
		t.Fatal(err)
	}
	turnID := "turn-1"
	if err := StartConversationTurn(sessionDir, ConversationTurn{ID: turnID, SessionID: "source", IntentID: "intent-1", RunID: "run-1"}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.AppendMessage(provider.NewUserMessage("hello")); err != nil {
		t.Fatal(err)
	}
	assistantID, err := mgr.AppendMessage(provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "world"}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := EndConversationTurn(sessionDir, "source", turnID, "completed", "stop", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Reload(); err != nil {
		t.Fatal(err)
	}

	rowFork, err := ForkSession(context.Background(), sessionDir, ForkOptions{SourceSessionID: "source", RequestID: "row-key"})
	if err != nil {
		t.Fatal(err)
	}
	if rowFork.ForkKind != ForkKindSession || rowFork.ParentSessionID != "source" {
		t.Fatalf("unexpected row fork: %+v", rowFork)
	}
	child, err := OpenByIDExact(sessionDir, rowFork.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(child.GetMessages()); got != 2 {
		t.Fatalf("child messages = %d, want 2", got)
	}

	seq := messageSeq(t, sessionDir, "source", assistantID)
	messageFork, err := ForkSession(context.Background(), sessionDir, ForkOptions{SourceSessionID: "source", AtSeq: &seq, RequestID: "message-key"})
	if err != nil {
		t.Fatal(err)
	}
	if messageFork.ForkKind != ForkKindMessage {
		t.Fatalf("message fork kind = %q", messageFork.ForkKind)
	}
	if _, err := ForkSession(context.Background(), sessionDir, ForkOptions{SourceSessionID: "source", AtSeq: &seq, RequestID: "message-key"}); err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	if got, err := OpenByIDExact(sessionDir, messageFork.SessionID); err != nil {
		t.Fatal(err)
	} else if len(got.GetMessages()) != 2 {
		t.Fatalf("message child messages = %d, want 2", len(got.GetMessages()))
	}
}

func TestForkRejectsOpenTurnAndDifferentSessionsRemainConcurrent(t *testing.T) {
	sessionDir := t.TempDir()
	for _, id := range []string{"a", "b"} {
		mgr := New(filepath.Join(t.TempDir(), id), sessionDir)
		if err := mgr.InitWithID(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := StartConversationTurn(sessionDir, ConversationTurn{ID: "open", SessionID: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ForkSession(context.Background(), sessionDir, ForkOptions{SourceSessionID: "a", RequestID: "open-key"}); !errors.Is(err, ErrForkSessionActive) {
		t.Fatalf("open turn error = %v, want active", err)
	}
	leaseA, err := acquireRuntimeLease(sessionDir, "a", "run")
	if err != nil {
		t.Fatal(err)
	}
	defer leaseA.release()
	_, ok := TryLockRuntime(sessionDir, "a")
	if ok {
		t.Fatal("expected active lease to block runtime acquisition")
	}
	releaseB, ok := TryLockRuntime(sessionDir, "b")
	if !ok {
		t.Fatal("session b should acquire independently")
	}
	releaseB()
}

func TestForkRejectsOrphanedPendingDecision(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := New(t.TempDir(), sessionDir)
	if err := mgr.InitWithID("pending-decision"); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"decision": map[string]any{"id": "approval-1", "status": "pending"}})
	if _, err := SaveSessionRunEvent(sessionDir, SessionRunEvent{SessionID: "pending-decision", RunID: "old-run", EventType: "decision_pending", Status: "pending", Data: data}); err != nil {
		t.Fatal(err)
	}
	if _, err := ForkSession(context.Background(), sessionDir, ForkOptions{SourceSessionID: "pending-decision", RequestID: "pending-key"}); !errors.Is(err, ErrForkSessionActive) {
		t.Fatalf("pending decision error = %v, want active", err)
	}
}

func TestForkAllowsOrphanedCancelledDecision(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := New(t.TempDir(), sessionDir)
	if err := mgr.InitWithID("cancelled-decision"); err != nil {
		t.Fatal(err)
	}
	pending, _ := json.Marshal(map[string]any{"decision": map[string]any{"id": "approval-1", "status": "pending"}})
	if _, err := SaveSessionRunEvent(sessionDir, SessionRunEvent{SessionID: "cancelled-decision", RunID: "old-run", EventType: "decision_pending", Status: "pending", Data: pending}); err != nil {
		t.Fatal(err)
	}
	cancelled, _ := json.Marshal(map[string]any{"decision": map[string]any{"id": "approval-1", "status": "cancelled"}})
	if _, err := SaveSessionRunEvent(sessionDir, SessionRunEvent{SessionID: "cancelled-decision", RunID: "old-run", EventType: "decision_cancelled", Status: "cancelled", Data: cancelled}); err != nil {
		t.Fatal(err)
	}
	// The cancelled decision is no longer pending, so it must not block the fork
	// the way an orphaned pending decision does.
	if _, err := ForkSession(context.Background(), sessionDir, ForkOptions{SourceSessionID: "cancelled-decision", RequestID: "cancelled-key"}); errors.Is(err, ErrForkSessionActive) {
		t.Fatalf("cancelled decision still blocked the fork: %v", err)
	}
}

func TestForkLegacyCompletedRunBoundary(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := New(t.TempDir(), sessionDir)
	if err := mgr.InitWithID("legacy-fork"); err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-time.Second)
	if err := CreateSessionRun(sessionDir, SessionRun{ID: "legacy-run", SessionID: "legacy-fork", Status: "running", StartedAt: started}); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.AppendMessage(provider.NewUserMessage("legacy question")); err != nil {
		t.Fatal(err)
	}
	assistantID, err := mgr.AppendMessage(provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "legacy answer"}}))
	if err != nil {
		t.Fatal(err)
	}
	finished := time.Now().Add(time.Second)
	if err := UpdateSessionRunStatus(sessionDir, "legacy-run", "completed", "", &finished); err != nil {
		t.Fatal(err)
	}
	result, err := ForkSession(context.Background(), sessionDir, ForkOptions{SourceSessionID: "legacy-fork", RequestID: "legacy-row"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ForkKind != ForkKindSession || result.BoundarySeq <= 0 {
		t.Fatalf("legacy row fork = %#v", result)
	}
	seq := messageSeq(t, sessionDir, "legacy-fork", assistantID)
	messageResult, err := ForkSession(context.Background(), sessionDir, ForkOptions{SourceSessionID: "legacy-fork", AtSeq: &seq, RequestID: "legacy-message"})
	if err != nil {
		t.Fatal(err)
	}
	if messageResult.ForkKind != ForkKindMessage {
		t.Fatalf("legacy message fork = %#v", messageResult)
	}
}

func TestExecutionAdmissionAtomicallyStartsConversationTurn(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := New(t.TempDir(), sessionDir)
	if err := mgr.InitWithID("atomic-turn"); err != nil {
		t.Fatal(err)
	}
	release, ok := TryLockRuntime(sessionDir, "atomic-turn")
	if !ok {
		t.Fatal("acquire runtime lease")
	}
	defer release()
	started := time.Now()
	if _, err := CreateExecutionIntentAndSessionRunEventWithTurn(sessionDir,
		ExecutionIntent{ID: "intent-atomic", SessionID: "atomic-turn", CreatedAt: started},
		SessionRun{ID: "run-atomic", SessionID: "atomic-turn", IntentID: "intent-atomic", Status: "running", StartedAt: started},
		SessionRunEvent{EventType: "started", SessionID: "atomic-turn", RunID: "run-atomic", Status: "running", Timestamp: started},
		ConversationTurn{ID: "turn-atomic", SessionID: "atomic-turn", IntentID: "intent-atomic", RunID: "run-atomic", StartedAt: started}); err != nil {
		t.Fatal(err)
	}
	var runCount, eventCount, turnCount, startEntryCount int
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Bun().QueryRow(`SELECT COUNT(*) FROM session_runs WHERE id = 'run-atomic'`).Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if err := db.Bun().QueryRow(`SELECT COUNT(*) FROM session_run_events WHERE run_id = 'run-atomic'`).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err := db.Bun().QueryRow(`SELECT COUNT(*) FROM conversation_turns WHERE id = 'turn-atomic' AND status = 'open'`).Scan(&turnCount); err != nil {
		t.Fatal(err)
	}
	if err := db.Bun().QueryRow(`SELECT COUNT(*) FROM entries WHERE session_id = 'atomic-turn' AND type = 'turn_start'`).Scan(&startEntryCount); err != nil {
		t.Fatal(err)
	}
	if runCount != 1 || eventCount != 1 || turnCount != 1 || startEntryCount != 1 {
		t.Fatalf("atomic admission counts: run=%d event=%d turn=%d start=%d", runCount, eventCount, turnCount, startEntryCount)
	}
	if _, err := FinishSessionRunAndConversationTurn(sessionDir,
		SessionRun{ID: "run-atomic", SessionID: "atomic-turn", Status: "completed", FinishedAt: timePtrForTest(time.Now())},
		SessionRunEvent{EventType: "finished", Status: "completed", Timestamp: time.Now()},
		"turn-atomic", "completed", "stop"); err != nil {
		t.Fatal(err)
	}
	var finalStatus, turnStatus string
	var terminalEvents, endEntries int
	if err := db.Bun().QueryRow(`SELECT status FROM session_runs WHERE id = 'run-atomic'`).Scan(&finalStatus); err != nil {
		t.Fatal(err)
	}
	if err := db.Bun().QueryRow(`SELECT status FROM conversation_turns WHERE id = 'turn-atomic'`).Scan(&turnStatus); err != nil {
		t.Fatal(err)
	}
	if err := db.Bun().QueryRow(`SELECT COUNT(*) FROM session_run_events WHERE run_id = 'run-atomic'`).Scan(&terminalEvents); err != nil {
		t.Fatal(err)
	}
	if err := db.Bun().QueryRow(`SELECT COUNT(*) FROM entries WHERE session_id = 'atomic-turn' AND type = 'turn_end'`).Scan(&endEntries); err != nil {
		t.Fatal(err)
	}
	if finalStatus != "completed" || turnStatus != "completed" || terminalEvents != 2 || endEntries != 1 {
		t.Fatalf("atomic finish state: run=%q turn=%q events=%d ends=%d", finalStatus, turnStatus, terminalEvents, endEntries)
	}
}

func timePtrForTest(value time.Time) *time.Time { return &value }

func messageSeq(t *testing.T, sessionDir, sessionID, entryID string) int64 {
	t.Helper()
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	var seq int64
	if err := db.Bun().QueryRow(`SELECT seq FROM entries WHERE session_id = ? AND id = ?`, sessionID, entryID).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	return seq
}
