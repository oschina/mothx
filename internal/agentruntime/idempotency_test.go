package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/session"
)

func TestFindIdempotentRunUsesCanonicalStartedEvent(t *testing.T) {
	root := t.TempDir()
	mgr := session.New(t.TempDir(), root)
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.CloseDatabases() })
	started := time.Now()
	data, err := json.Marshal(map[string]any{
		"idempotencyKeyHash": IdempotencyKeyFingerprint("submission-1"),
		"idempotencyScope":   "channel",
		"requestFingerprint": "request-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.CreateExecutionIntentAndSessionRunEvent(root,
		session.ExecutionIntent{ID: "intent-idempotent", SessionID: mgr.GetHeader().ID, CreatedAt: started},
		session.SessionRun{ID: "run-idempotent", SessionID: mgr.GetHeader().ID, IntentID: "intent-idempotent", Status: "completed", StartedAt: started},
		session.SessionRunEvent{SessionID: mgr.GetHeader().ID, RunID: "run-idempotent", EventType: "started", Status: "completed", Timestamp: started, Data: data}); err != nil {
		t.Fatal(err)
	}
	run, err := FindIdempotentRun(context.Background(), root, mgr.GetHeader().ID, "submission-1", "request-1", "channel")
	if err != nil || run == nil || run.ID != "run-idempotent" {
		t.Fatalf("resolved run = %#v, err=%v", run, err)
	}
	if _, err := FindIdempotentRun(context.Background(), root, mgr.GetHeader().ID, "submission-1", "request-2", "channel"); !errors.Is(err, ErrIdempotencyKeyConflict) {
		t.Fatalf("fingerprint conflict = %v", err)
	}
	if _, err := FindIdempotentRun(context.Background(), root, mgr.GetHeader().ID, "submission-1", "request-1", "external"); !errors.Is(err, ErrIdempotencyKeyConflict) {
		t.Fatalf("scope conflict = %v", err)
	}
}

func TestRuntimeSubmissionReservationIsAtomic(t *testing.T) {
	root := t.TempDir()
	mgr := session.New(t.TempDir(), root)
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.CloseDatabases() })
	started := time.Now()
	keyHash := IdempotencyKeyFingerprint("durable-submission")
	create := func(intentID, runID, fingerprint string) error {
		_, err := session.CreateExecutionIntentAndSessionRunEvent(root,
			session.ExecutionIntent{ID: intentID, SessionID: mgr.GetHeader().ID, RequestFingerprint: fingerprint, CreatedAt: started},
			session.SessionRun{
				ID: runID, SessionID: mgr.GetHeader().ID, IntentID: intentID, Status: "completed", StartedAt: started,
				SubmissionKeyHash: keyHash, SubmissionScope: "channel", SubmissionFingerprint: fingerprint,
			},
			session.SessionRunEvent{SessionID: mgr.GetHeader().ID, RunID: runID, EventType: "started", Status: "completed", Timestamp: started})
		return err
	}
	if err := create("intent-first", "run-first", "request-one"); err != nil {
		t.Fatal(err)
	}
	run, err := FindIdempotentRun(t.Context(), root, mgr.GetHeader().ID, "durable-submission", "request-one", "channel")
	if err != nil || run == nil || run.ID != "run-first" {
		t.Fatalf("submission lookup = %#v, err=%v", run, err)
	}
	if err := create("intent-duplicate", "run-duplicate", "request-one"); !errors.Is(err, session.ErrRuntimeSubmissionExists) {
		t.Fatalf("duplicate reservation error = %v", err)
	}
	if err := create("intent-conflict", "run-conflict", "request-two"); !errors.Is(err, session.ErrRuntimeSubmissionConflict) {
		t.Fatalf("conflicting reservation error = %v", err)
	}
	var duplicateIntents, duplicateRuns, duplicateEvents int
	if err := session.QueryRootDatabase(root, func(db *dao.Database) error {
		if err := db.Bun().QueryRow(`SELECT COUNT(*) FROM session_execution_intents WHERE id IN ('intent-duplicate', 'intent-conflict')`).Scan(&duplicateIntents); err != nil {
			return err
		}
		if err := db.Bun().QueryRow(`SELECT COUNT(*) FROM session_runs WHERE id IN ('run-duplicate', 'run-conflict')`).Scan(&duplicateRuns); err != nil {
			return err
		}
		return db.Bun().QueryRow(`SELECT COUNT(*) FROM session_run_events WHERE run_id IN ('run-duplicate', 'run-conflict')`).Scan(&duplicateEvents)
	}); err != nil {
		t.Fatal(err)
	}
	if duplicateIntents != 0 || duplicateRuns != 0 || duplicateEvents != 0 {
		t.Fatalf("failed reservations left rows: intents=%d runs=%d events=%d", duplicateIntents, duplicateRuns, duplicateEvents)
	}
}
