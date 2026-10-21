package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/session"
)

// TestHandleCancelUsesSharedRuntimeStop pins that a durable ACP prompt is
// stopped through the canonical Runtime stop matrix (persisting the cancelling
// state) while the adapter still owns the in-memory Agent teardown.
func TestHandleCancelUsesSharedRuntimeStop(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "sessions")
	mgr := session.New(t.TempDir(), sessionDir)
	if err := mgr.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	sessionID := mgr.GetHeader().ID
	settings := config.DefaultSettings()
	settings.SessionDir = sessionDir

	srv := &server{
		settings: settings,
		sessions: map[string]*sessionRuntime{},
		w:        &bytes.Buffer{},
	}
	runID := "acp-stop-run"
	rt := &sessionRuntime{id: sessionID, execution: &agentruntime.ExecutionRuntime{}, runID: runID}
	rt.execution.SetRunStore(agentruntime.RunStore{SessionDir: sessionDir})
	rt.execution.SetEventSink(agentruntime.SessionRunEventSink{SessionDir: sessionDir})

	guard, err := session.AcquireExecutionAdmission(sessionDir, sessionID)
	if err != nil {
		t.Fatalf("acquire admission: %v", err)
	}
	intent := agentruntime.ExecutionIntent{ID: "intent-acp-stop", SessionID: sessionID, Source: "acp", CreatedAt: time.Now()}
	if _, err := rt.execution.BeginIntentDurable(context.Background(), intent, agentruntime.DurableRun{
		ID: runID, SessionID: sessionID, IntentID: intent.ID, Source: "acp", Status: "running", StartedAt: time.Now(),
	}, agentruntime.RunEvent{SessionID: sessionID, RunID: runID, EventType: "started", Source: "acp", Status: "running", Timestamp: time.Now()}); err != nil {
		guard.Release()
		t.Fatalf("begin durable run: %v", err)
	}
	guard.Release()
	srv.sessions[sessionID] = rt

	srv.handleCancel(rpcRequest{ID: json.RawMessage("1"), Params: json.RawMessage(`{"sessionId":"` + sessionID + `"}`)})

	stored, err := agentruntime.GetDurableRun(context.Background(), sessionDir, runID)
	if err != nil {
		t.Fatalf("load cancelled run: %v", err)
	}
	if stored == nil || stored.Status != "cancelling" {
		t.Fatalf("run after session/cancel = %#v, want cancelling", stored)
	}
	if err := rt.execution.FinishDurableWithRetry(context.Background(), runID, agentruntime.RunStateCancelled, "", agentruntime.RunEvent{
		SessionID: sessionID, RunID: runID, EventType: "finished", Source: "acp", Status: "cancelled", Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("finish cancelled run: %v", err)
	}
	if _, active := rt.execution.Active(); active {
		t.Fatal("ACP execution stayed active after cancellation terminalized")
	}
	snapshot, err := agentruntime.InspectSessionExecution(sessionDir, sessionID)
	if err != nil {
		t.Fatalf("inspect execution: %v", err)
	}
	if snapshot.Busy {
		t.Fatalf("session stayed busy after ACP cancel: %#v", snapshot)
	}
}

func promptInflightLen(s *server) int {
	s.inflightMu.Lock()
	defer s.inflightMu.Unlock()
	return len(s.inflight)
}

// TestHandleCancelRequestUsesSharedRuntimeStop drives the $/cancel_request wire
// path through the same Runtime stop matrix as session/cancel. The request key
// resolves rt.promptID -> rt.runID under cancelMu before stopping.
func TestHandleCancelRequestUsesSharedRuntimeStop(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "sessions")
	mgr := session.New(t.TempDir(), sessionDir)
	if err := mgr.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	sessionID := mgr.GetHeader().ID
	settings := config.DefaultSettings()
	settings.SessionDir = sessionDir

	srv := &server{
		settings: settings,
		sessions: map[string]*sessionRuntime{},
		pending:  map[string]chan json.RawMessage{},
		w:        &bytes.Buffer{},
	}
	runID := "acp-cancel-request-run"
	rt := &sessionRuntime{id: sessionID, execution: &agentruntime.ExecutionRuntime{}, runID: runID, promptID: "7"}
	rt.execution.SetRunStore(agentruntime.RunStore{SessionDir: sessionDir})
	rt.execution.SetEventSink(agentruntime.SessionRunEventSink{SessionDir: sessionDir})

	guard, err := session.AcquireExecutionAdmission(sessionDir, sessionID)
	if err != nil {
		t.Fatalf("acquire admission: %v", err)
	}
	intent := agentruntime.ExecutionIntent{ID: "intent-acp-cancel-request", SessionID: sessionID, Source: "acp", CreatedAt: time.Now()}
	if _, err := rt.execution.BeginIntentDurable(context.Background(), intent, agentruntime.DurableRun{
		ID: runID, SessionID: sessionID, IntentID: intent.ID, Source: "acp", Status: "running", StartedAt: time.Now(),
	}, agentruntime.RunEvent{SessionID: sessionID, RunID: runID, EventType: "started", Source: "acp", Status: "running", Timestamp: time.Now()}); err != nil {
		guard.Release()
		t.Fatalf("begin durable run: %v", err)
	}
	guard.Release()
	srv.sessions[sessionID] = rt

	srv.handleCancelRequest(rpcRequest{Params: json.RawMessage(`{"requestId":"7"}`)})

	stored, err := agentruntime.GetDurableRun(context.Background(), sessionDir, runID)
	if err != nil {
		t.Fatalf("load cancelled run: %v", err)
	}
	if stored == nil || stored.Status != "cancelling" {
		t.Fatalf("run after $/cancel_request = %#v, want cancelling", stored)
	}
	if err := rt.execution.FinishDurableWithRetry(context.Background(), runID, agentruntime.RunStateCancelled, "", agentruntime.RunEvent{
		SessionID: sessionID, RunID: runID, EventType: "finished", Source: "acp", Status: "cancelled", Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("finish cancelled run: %v", err)
	}
	if _, active := rt.execution.Active(); active {
		t.Fatal("ACP execution stayed active after $/cancel_request terminalized")
	}
}

// TestCancelRequestAbortsPromptAdmissionContext pins the F2 fix at the helper
// level: the request-keyed admission context of an in-flight prompt is
// cancellable, and clearing it makes further cancels a no-op.
func TestCancelRequestAbortsPromptAdmissionContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := &server{inflight: make(map[string]*promptInflight)}
	srv.trackPromptInflight("7", "session-a", cancel)
	if !srv.cancelPromptInflight("7") {
		t.Fatal("cancelPromptInflight reported no entry for a tracked prompt")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("prompt admission context was not cancelled")
	}
	srv.clearPromptInflight("7")
	if srv.cancelPromptInflight("7") {
		t.Fatal("cancelPromptInflight matched an entry after it was cleared")
	}
}

// TestHandleCancelRequestCancelsInflightPrompt drives the wire path: a
// $/cancel_request for a prompt that has not registered rt.cancel yet ("still
// in admission") still aborts it through the request context.
func TestHandleCancelRequestCancelsInflightPrompt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := &server{
		inflight: make(map[string]*promptInflight),
		pending:  make(map[string]chan json.RawMessage),
		w:        &bytes.Buffer{},
	}
	srv.trackPromptInflight("5", "session-a", cancel)
	srv.handleCancelRequest(rpcRequest{Params: json.RawMessage(`{"requestId":"5"}`)})
	select {
	case <-ctx.Done():
	default:
		t.Fatal("handleCancelRequest did not cancel the in-flight prompt admission")
	}
}

// TestHandleCancelAbortsPromptAdmissionForSession pins that session/cancel
// aborts a prompt still stuck in admission even though rt.cancel is not set yet.
func TestHandleCancelAbortsPromptAdmissionForSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := &server{
		sessions: map[string]*sessionRuntime{"session-a": {id: "session-a"}},
		inflight: make(map[string]*promptInflight),
		w:        &bytes.Buffer{},
	}
	srv.trackPromptInflight("5", "session-a", cancel)
	srv.handleCancel(rpcRequest{ID: json.RawMessage("1"), Params: json.RawMessage(`{"sessionId":"session-a"}`)})
	select {
	case <-ctx.Done():
	default:
		t.Fatal("handleCancel did not cancel the in-flight prompt admission")
	}
}

// TestHandleCancelAbortsQueuedPromptBeforeSessionRuntimeIsInstalled covers a
// prompt queued behind session/load. Its admission context is already known,
// but the loader has not installed a runtime yet, so session/cancel must not
// reject the request as an unknown session and let the prompt start later.
func TestHandleCancelAbortsQueuedPromptBeforeSessionRuntimeIsInstalled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := &server{
		inflight: make(map[string]*promptInflight),
		w:        &bytes.Buffer{},
	}
	srv.trackPromptInflight("5", "session-loading", cancel)
	srv.handleCancel(rpcRequest{ID: json.RawMessage("1"), Params: json.RawMessage(`{"sessionId":"session-loading"}`)})
	select {
	case <-ctx.Done():
	default:
		t.Fatal("handleCancel did not cancel the prompt queued behind session/load")
	}
	responses := jsonLines(t, srv.w.(*bytes.Buffer))
	if len(responses) != 1 || responses[0]["error"] != nil {
		t.Fatalf("cancel response = %#v, want successful cancellation", responses)
	}
}

// TestCancelPromptInflightForSessionScopesBySession verifies the session-scoped
// cancel only touches the matching session's admission context.
func TestCancelPromptInflightForSessionScopesBySession(t *testing.T) {
	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	srv := &server{inflight: make(map[string]*promptInflight)}
	srv.trackPromptInflight("1", "session-a", cancelA)
	srv.trackPromptInflight("2", "session-b", cancelB)
	if !srv.cancelPromptInflightForSession("session-a") {
		t.Fatal("cancelPromptInflightForSession matched no entry for session-a")
	}
	select {
	case <-ctxA.Done():
	default:
		t.Fatal("session-a admission was not cancelled")
	}
	select {
	case <-ctxB.Done():
		t.Fatal("session-b admission was cancelled by session-a's cancel")
	default:
	}
}

// TestDispatchRequestPromptRunsOffReadLoop pins the reactor routing: a
// session/prompt is dispatched asynchronously (dispatchRequest returns before the
// handler runs) and the admission tracking entry is cleaned up when the handler
// finishes.
func TestDispatchRequestPromptRunsOffReadLoop(t *testing.T) {
	srv := &server{
		ops:      newSessionOpLanes(),
		inflight: make(map[string]*promptInflight),
		pending:  make(map[string]chan json.RawMessage),
		w:        &bytes.Buffer{},
	}
	srv.dispatchRequest(rpcRequest{
		ID:     json.RawMessage("9"),
		Method: "session/prompt",
		Params: json.RawMessage(`{"sessionId":"missing","prompt":[{"type":"text","text":"hi"}]}`),
	})
	// The handler runs on the lane, not inline: it answers "unknown session"
	// (no fixture session) and then clears its inflight entry.
	waitForCondition(t, "prompt handler to finish off the read loop", func() bool {
		return promptInflightLen(srv) == 0
	})
	if busy := srv.ops.busy("missing"); busy {
		t.Fatal("lane still busy after the prompt handler finished")
	}
}
