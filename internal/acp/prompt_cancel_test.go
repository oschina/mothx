package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func promptInflightLen(s *server) int {
	s.inflightMu.Lock()
	defer s.inflightMu.Unlock()
	return len(s.inflight)
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
