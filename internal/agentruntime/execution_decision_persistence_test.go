package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/session"
)

type failOnceEventTypeSink struct {
	delegate  RunEventSink
	eventType string
	failed    bool
}

func (s *failOnceEventTypeSink) Record(event RunEvent) (string, error) {
	if event.EventType == s.eventType && !s.failed {
		s.failed = true
		return "", errors.New("event store temporarily unavailable")
	}
	return s.delegate.Record(event)
}

func TestExecutionRuntimeDurableDecisionWaitRollsBackAfterEventFailure(t *testing.T) {
	sessionDir := t.TempDir()
	manager, err := CreateSession(CreateSessionOptions{
		WorkDir: t.TempDir(), SessionDir: sessionDir, ID: "decision-rollback-session",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	run := DurableRun{
		ID: "decision-rollback-run", SessionID: manager.GetHeader().ID,
		WorkDir: manager.GetHeader().Cwd, Source: "webui", Model: "test-model",
		Mode: "agent", Status: string(RunStateRunning), StartedAt: time.Now(),
	}
	sink := &failOnceEventTypeSink{
		delegate:  SessionRunEventSink{SessionDir: sessionDir},
		eventType: string(RunStateWaitingApproval),
	}
	var runtime ExecutionRuntime
	runtime.SetRunStore(RunStore{SessionDir: sessionDir})
	runtime.SetEventSink(sink)
	if _, err := runtime.BeginDurable(context.Background(), run, RunEvent{
		EventType: "started", Source: run.Source, Model: run.Model, Mode: run.Mode,
	}); err != nil {
		t.Fatalf("BeginDurable: %v", err)
	}

	if err := runtime.WaitForApproval(run.ID); err == nil {
		t.Fatal("WaitForApproval succeeded while its durable event failed")
	}
	assertPersistedRunState(t, sessionDir, run.ID, RunStateRunning)
	if got := runtime.State(); got != RunStateRunning {
		t.Fatalf("runtime state after failed wait = %q, want %q", got, RunStateRunning)
	}
	events, err := session.ListSessionRunEvents(sessionDir, run.SessionID)
	if err != nil {
		t.Fatalf("ListSessionRunEvents after failed wait: %v", err)
	}
	if len(events) != 1 || events[0].EventType != "started" {
		t.Fatalf("events after failed wait = %#v, want only started", events)
	}

	if err := runtime.WaitForApproval(run.ID); err != nil {
		t.Fatalf("retry WaitForApproval: %v", err)
	}
	assertPersistedRunState(t, sessionDir, run.ID, RunStateWaitingApproval)
	if err := runtime.Resume(run.ID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	assertPersistedRunState(t, sessionDir, run.ID, RunStateRunning)
	if err := runtime.FinishWithState(run.ID, RunStateCompleted); err != nil {
		t.Fatalf("FinishWithState: %v", err)
	}
	assertPersistedRunState(t, sessionDir, run.ID, RunStateCompleted)

	events, err = session.ListSessionRunEvents(sessionDir, run.SessionID)
	if err != nil {
		t.Fatalf("ListSessionRunEvents: %v", err)
	}
	wantTypes := []string{"started", string(RunStateWaitingApproval), "resumed", "finished"}
	if len(events) != len(wantTypes) {
		t.Fatalf("event count = %d, want %d: %#v", len(events), len(wantTypes), events)
	}
	for i, want := range wantTypes {
		if events[i].EventType != want || events[i].RunID != run.ID {
			t.Fatalf("event[%d] = %#v, want type %q for run %q", i, events[i], want, run.ID)
		}
	}
}

func TestExecutionRuntimeReattachDurableQuestionPersistsUsageAndCompletion(t *testing.T) {
	sessionDir := t.TempDir()
	manager, err := CreateSession(CreateSessionOptions{
		WorkDir: t.TempDir(), SessionDir: sessionDir, ID: "reattach-question-session",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	run := DurableRun{
		ID: "reattach-question-run", SessionID: manager.GetHeader().ID,
		WorkDir: manager.GetHeader().Cwd, Source: "responses_background", Model: "test-model",
		Mode: "yolo", Status: string(RunStateWaitingQuestion), StartedAt: time.Now(),
	}
	store := RunStore{SessionDir: sessionDir}
	if err := store.Create(run); err != nil {
		t.Fatalf("create persisted run: %v", err)
	}
	sink := SessionRunEventSink{SessionDir: sessionDir}
	if _, err := sink.Record(RunEvent{
		SessionID: run.SessionID, RunID: run.ID, EventType: string(RunStateWaitingQuestion),
		Source: run.Source, Status: run.Status, Model: run.Model, Mode: run.Mode,
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("record persisted wait event: %v", err)
	}

	var runtime ExecutionRuntime
	runtime.SetRunStore(store)
	runtime.SetEventSink(sink)
	runCtx, err := runtime.ReattachDurableRun(context.Background(), run, RunStateWaitingQuestion, RunEvent{
		EventType: "reattached", Source: run.Source, Model: run.Model, Mode: run.Mode,
	})
	if err != nil {
		t.Fatalf("ReattachDurableRun: %v", err)
	}
	usage := json.RawMessage(`{"inputTokens":21,"outputTokens":8}`)
	contextUsage := json.RawMessage(`{"used":29,"limit":128000}`)
	if err := runtime.RecordUsage(run.ID, usage, contextUsage); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}
	if err := runtime.Resume(run.ID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if err := runtime.FinishWithState(run.ID, RunStateCompleted); err != nil {
		t.Fatalf("FinishWithState: %v", err)
	}
	select {
	case <-runCtx.Done():
	default:
		t.Fatal("reattached run context remained active after completion")
	}

	persisted, err := session.GetSessionRun(sessionDir, run.ID)
	if err != nil {
		t.Fatalf("GetSessionRun: %v", err)
	}
	if persisted == nil || persisted.Status != string(RunStateCompleted) || persisted.FinishedAt == nil {
		t.Fatalf("persisted completed run = %#v", persisted)
	}
	if string(persisted.Usage) != string(usage) || string(persisted.ContextUsage) != string(contextUsage) {
		t.Fatalf("persisted usage = %s / %s, want %s / %s", persisted.Usage, persisted.ContextUsage, usage, contextUsage)
	}
	events, err := session.ListSessionRunEvents(sessionDir, run.SessionID)
	if err != nil {
		t.Fatalf("ListSessionRunEvents: %v", err)
	}
	wantTypes := []string{string(RunStateWaitingQuestion), "resumed", "finished"}
	if len(events) != len(wantTypes) {
		t.Fatalf("event count = %d, want %d: %#v", len(events), len(wantTypes), events)
	}
	for i, want := range wantTypes {
		if events[i].EventType != want || events[i].SessionID != run.SessionID || events[i].RunID != run.ID ||
			events[i].Source != run.Source || events[i].Model != run.Model || events[i].Mode != run.Mode {
			t.Fatalf("event[%d] = %#v, want reattached run identity and type %q", i, events[i], want)
		}
	}
}

func assertPersistedRunState(t *testing.T, sessionDir, runID string, want RunState) {
	t.Helper()
	run, err := session.GetSessionRun(sessionDir, runID)
	if err != nil {
		t.Fatalf("GetSessionRun(%q): %v", runID, err)
	}
	if run == nil || run.Status != string(want) {
		t.Fatalf("persisted run = %#v, want state %q", run, want)
	}
}
