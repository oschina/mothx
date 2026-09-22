package openaiapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

// historyRecordingProvider captures Chat params so tests can assert that the
// submit-run path replays persisted session history into the agent.
type historyRecordingProvider struct {
	mu     sync.Mutex
	models []*provider.Model
	calls  []provider.ChatParams
}

// approvalBlockingProvider emits one side-effecting bash call and then waits
// for the WebUI approval path to resume the Agent.
type approvalBlockingProvider struct {
	models []*provider.Model
}

func (p *approvalBlockingProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent, 4)
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: provider.StreamStart}
		args, _ := json.Marshal(map[string]any{"command": "printf approval-required"})
		ch <- provider.StreamEvent{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{ID: "approval-call", Name: "bash", Arguments: args}}
		ch <- provider.StreamEvent{Type: provider.StreamDone, StopReason: "tool_calls"}
	}()
	return ch
}

func (p *approvalBlockingProvider) Name() string              { return "approval-blocking" }
func (p *approvalBlockingProvider) API() string               { return "openai-chat" }
func (p *approvalBlockingProvider) Models() []*provider.Model { return p.models }
func (p *approvalBlockingProvider) GetModel(id string) *provider.Model {
	for _, m := range p.models {
		if m.ID == id {
			return m
		}
	}
	return nil
}

func newHistoryRecordingProvider() *historyRecordingProvider {
	return &historyRecordingProvider{
		models: []*provider.Model{{ID: "m1", Name: "Model 1", ContextWindow: 32768, MaxTokens: 2048}},
	}
}

func TestExecutionAdmissionErrorUsesCanonicalSnapshot(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := session.New(sessionDir, sessionDir)
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	settings := config.DefaultSettings()
	settings.SessionDir = sessionDir
	srv := &Server{settings: settings}
	sessionID := mgr.GetHeader().ID

	if err := session.CreateSessionRun(sessionDir, session.SessionRun{
		ID: "orphan-run", SessionID: sessionID, Status: "running", StartedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	status, info := srv.executionAdmissionError(sessionID, session.ErrSessionRecoveryRequired)
	if status != http.StatusConflict || info.Code != "session_recovery_in_progress" || info.RunID != "orphan-run" {
		t.Fatalf("orphan admission error = status %d info %#v", status, info)
	}
}

func TestRunAPICancelExternalOwnerReturnsStructuredConflict(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := session.New(t.TempDir(), sessionDir)
	if err := mgr.InitWithID("external-cancel-session"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := session.CreateSessionRun(sessionDir, session.SessionRun{
		ID: "external-cancel-run", SessionID: mgr.GetHeader().ID, Status: "running", StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create active run: %v", err)
	}
	db, err := session.OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Bun().Exec(`INSERT INTO session_runtime_leases
		(session_id, owner_instance_id, owner_pid, owner_kind, lease_token_hash, epoch, run_id, purpose, state, acquired_at, heartbeat_at, expires_at, updated_at)
		VALUES (?, 'external-owner', 4242, 'process', 'external-token', 9, ?, 'execution', 'active',
		CAST(strftime('%s','now') AS INTEGER), CAST(strftime('%s','now') AS INTEGER), CAST(strftime('%s','now') AS INTEGER) + 60, CAST(strftime('%s','now') AS INTEGER))`,
		mgr.GetHeader().ID, "external-cancel-run"); err != nil {
		t.Fatalf("insert external lease: %v", err)
	}

	settings := config.DefaultSettings()
	settings.SessionDir = sessionDir
	srv := &Server{settings: settings}
	req := httptest.NewRequest(http.MethodPost, "/api/runs/external-cancel-run/cancel", nil)
	w := httptest.NewRecorder()
	srv.HandleRunAPI(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("cancel status = %d, body = %s", w.Code, w.Body.String())
	}
	var response ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode cancel error: %v", err)
	}
	if response.Error.Code != string(agentruntime.SessionStopOwnedElsewhere) || response.Error.RunID != "external-cancel-run" {
		t.Fatalf("cancel error = %#v", response.Error)
	}
	run, err := agentruntime.GetDurableRun(t.Context(), sessionDir, "external-cancel-run")
	if err != nil || run == nil || run.Status != "running" {
		t.Fatalf("external run changed: run=%+v err=%v", run, err)
	}
}

func (p *historyRecordingProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	p.mu.Lock()
	p.calls = append(p.calls, provider.ChatParams{
		Messages:     append([]provider.Message(nil), params.Messages...),
		SystemPrompt: params.SystemPrompt,
		Tools:        append([]provider.ToolDefinition(nil), params.Tools...),
	})
	p.mu.Unlock()
	ch := make(chan provider.StreamEvent, 3)
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: provider.StreamStart}
		ch <- provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: "ok"}
		ch <- provider.StreamEvent{Type: provider.StreamDone}
	}()
	return ch
}

func (p *historyRecordingProvider) Name() string              { return "history-recording" }
func (p *historyRecordingProvider) API() string               { return "openai-chat" }
func (p *historyRecordingProvider) Models() []*provider.Model { return p.models }
func (p *historyRecordingProvider) GetModel(id string) *provider.Model {
	for _, m := range p.models {
		if m.ID == id {
			return m
		}
	}
	return nil
}

func (p *historyRecordingProvider) recordedCalls() []provider.ChatParams {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provider.ChatParams(nil), p.calls...)
}

// TestSubmitRunReplaysSessionHistory verifies that a background run submitted
// through POST /api/sessions/{id}/runs (the WebUI chat path) loads the
// persisted session history into the fresh agent — including after a runtime
// mode switch — so the model does not treat the turn as a new conversation.
func TestSubmitRunReplaysSessionHistory(t *testing.T) {
	srv := newTestServer(t)
	srv.cfg.DefaultMode = "yolo"
	p := newHistoryRecordingProvider()
	srv.provider = p
	srv.model = p.models[0]

	sessionID := "run-history-session"
	sess, err := srv.getOrCreateSession(sessionID, srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("getOrCreateSession: %v", err)
	}
	if _, err := sess.Manager.AppendMessage(provider.NewUserMessage("之前的问题")); err != nil {
		t.Fatalf("append user history: %v", err)
	}
	assistant := provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "之前的回答"}})
	if _, err := sess.Manager.AppendMessage(assistant); err != nil {
		t.Fatalf("append assistant history: %v", err)
	}

	// Simulate the WebUI runtime mode switch before the next turn.
	mode := "agent"
	if _, err := srv.PatchSessionRuntime(sessionID, SessionRuntimePatch{Mode: &mode}); err != nil {
		t.Fatalf("PatchSessionRuntime: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/runs", strings.NewReader(`{"message":"接着聊","transcript":true}`))
	w := httptest.NewRecorder()
	srv.HandleSubmitRun(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, body = %s", w.Code, w.Body.String())
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		if calls := p.recordedCalls(); len(calls) > 0 {
			msgs := calls[0].Messages
			// The agent prepends a system-injected session context message, so
			// expect: [session context, user history, assistant history, new user].
			if len(msgs) != 4 {
				t.Fatalf("provider received %d messages, want 4 (context + 2 history + 1 new): %#v", len(msgs), msgs)
			}
			if !msgs[0].SystemInjected {
				t.Fatalf("msgs[0] should be the system-injected session context: %#v", msgs[0])
			}
			if msgs[1].Role != "user" || messageText(msgs[1]) != "之前的问题" {
				t.Fatalf("msgs[1] = %s/%q, want user history", msgs[1].Role, messageText(msgs[1]))
			}
			if msgs[2].Role != "assistant" || messageText(msgs[2]) != "之前的回答" {
				t.Fatalf("msgs[2] = %s/%q, want assistant history", msgs[2].Role, messageText(msgs[2]))
			}
			if msgs[3].Role != "user" || messageText(msgs[3]) != "接着聊" {
				t.Fatalf("msgs[3] = %s/%q, want new user message", msgs[3].Role, messageText(msgs[3]))
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("provider was not called within deadline")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSubmitRunRegistersApprovalInRuntime(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	p := &approvalBlockingProvider{models: []*provider.Model{{ID: "m1", Name: "Model 1", ContextWindow: 32768, MaxTokens: 2048}}}
	srv.provider = p
	srv.model = p.models[0]

	sessionID := "submit-approval-runtime"
	w := submitRun(t, srv, sessionID, `{"message":"run the command","mode":"agent","transcript":true}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, body = %s", w.Code, w.Body.String())
	}
	defer func() { _ = srv.CancelSessionRun(sessionID) }()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := srv.GetSessionRuntime(sessionID)
		if err == nil && snapshot != nil && len(snapshot.PendingApprovals) == 1 {
			approval := snapshot.PendingApprovals[0]
			if approval.Tool["name"] != "bash" {
				t.Fatalf("approval tool = %#v, want bash", approval.Tool)
			}
			if snapshot.ActiveRun == nil || snapshot.ActiveRun.RunID == "" {
				t.Fatalf("active run missing from approval snapshot: %#v", snapshot)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	snapshot, _ := srv.GetSessionRuntime(sessionID)
	t.Fatalf("approval was not registered in runtime: %#v", snapshot)
}

func waitForProviderCall(t *testing.T, p *historyRecordingProvider) provider.ChatParams {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if calls := p.recordedCalls(); len(calls) > 0 {
			return calls[0]
		}
		if time.Now().After(deadline) {
			t.Fatal("provider was not called within deadline")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func newHistoryRecordingServer(t *testing.T) (*Server, *historyRecordingProvider) {
	t.Helper()
	srv := newTestServer(t)
	srv.cfg.DefaultMode = "yolo"
	p := newHistoryRecordingProvider()
	srv.provider = p
	srv.model = p.models[0]
	return srv, p
}

func TestSubmitRunBindsRequestedTeamForNewSession(t *testing.T) {
	srv, provider := newHistoryRecordingServer(t)
	defer srv.pool.Stop()

	const sessionID = "new-session-team-binding"
	w := submitRun(t, srv, sessionID, `{"message":"start with a team","expertId":"software-company"}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, body = %s", w.Code, w.Body.String())
	}
	waitForProviderCall(t, provider)

	persisted, err := session.OpenByIDExact(srv.settings.GetSessionDir(), sessionID)
	if err != nil {
		t.Fatalf("open persisted session: %v", err)
	}
	if got := persisted.GetExpertID(); got != "software-company" {
		t.Fatalf("persisted expert = %q, want software-company", got)
	}
}

func TestSubmitRunPolicySnapshotIncludesProviderSelection(t *testing.T) {
	snapshot, err := marshalRunPolicySnapshot(nil, nil, submitRunRequest{
		Message:  "hello",
		Provider: "anthropic",
		Model:    "claude-sonnet",
	}, "webui", "yolo")
	if err != nil {
		t.Fatalf("marshalRunPolicySnapshot: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(snapshot, &decoded); err != nil {
		t.Fatalf("decode policy snapshot: %v", err)
	}
	if got, _ := decoded["provider"].(string); got != "anthropic" {
		t.Fatalf("provider = %q, want anthropic", got)
	}
}

func submitRun(t *testing.T, srv *Server, sessionID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/runs", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.HandleSubmitRun(w, req)
	return w
}

func TestSubmitRunRejectsWhenSharedRuntimeLockIsHeld(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	sess, err := srv.getOrCreateSession("runtime-lock-submit", srv.cfg.GetWorkDir())
	if err != nil || sess == nil {
		t.Fatalf("create session: %v", err)
	}
	release := session.LockRuntime(srv.settings.GetSessionDir(), sess.ID)
	defer release()
	w := submitRun(t, srv, sess.ID, `{"message":"must conflict"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("submit status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestSubmitRunWaitsForShortSessionMutation(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	sess, err := srv.getOrCreateSession("session-mutation-submit", srv.cfg.GetWorkDir())
	if err != nil || sess == nil {
		t.Fatalf("create session: %v", err)
	}

	// SkillHub inspection and other short-lived session mutations use the
	// session mutex but do not own the runtime admission lock. The submit path
	// must wait for that mutation rather than report a false 409 conflict.
	sess.Lock()
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		result <- submitRun(t, srv, sess.ID, `{"message":"after mutation"}`)
	}()

	select {
	case response := <-result:
		sess.Unlock()
		t.Fatalf("submit returned while session mutation was held: status=%d body=%s", response.Code, response.Body.String())
	case <-time.After(50 * time.Millisecond):
	}
	sess.Unlock()

	select {
	case response := <-result:
		if response.Code != http.StatusAccepted {
			t.Fatalf("submit status = %d, body = %s", response.Code, response.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("submit did not proceed after session mutation was released")
	}
}

func TestSubmitRunUsesSafeErrorInfoForMalformedJSON(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/safe-error-submit/runs", strings.NewReader(`{"message":"unterminated"`))
	w := httptest.NewRecorder()
	srv.HandleSubmitRun(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("submit status = %d, body = %s", w.Code, w.Body.String())
	}
	var response ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if response.Error.Code != "invalid_json" || response.Error.Type != "invalid_request_error" || response.Error.MessageKey != "run.error.invalidJSON" {
		t.Fatalf("malformed JSON error = %#v", response.Error)
	}
	if response.Error.Message != "The request body is not valid JSON." || strings.Contains(response.Error.Message, "unexpected") {
		t.Fatalf("malformed JSON message was not sanitized: %q", response.Error.Message)
	}
}

// TestSubmitRunHonorsClientChosenWorkDir verifies that a brand-new,
// client-created session (not yet persisted server-side) is created with the
// workDir sent in the submit body rather than the configured default. This
// keeps the WebUI's user choice from being silently overwritten by the
// default on background run submission.
func TestSubmitRunHonorsClientChosenWorkDir(t *testing.T) {
	srv, p := newHistoryRecordingServer(t)
	defer srv.pool.Stop()

	chosen := filepath.Join(t.TempDir(), "chosen")
	if err := os.MkdirAll(chosen, 0o755); err != nil {
		t.Fatalf("mkdir chosen: %v", err)
	}
	const sessionID = "client-created-with-workdir"

	w := submitRun(t, srv, sessionID, `{"message":"workdir test","workDir":"`+chosen+`"}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, body = %s", w.Code, w.Body.String())
	}
	waitForProviderCall(t, p)

	sess := srv.pool.Get(sessionID)
	if sess == nil {
		t.Fatalf("session %q was not created", sessionID)
	}
	if !sameWorkDir(sess.WorkDir, chosen) {
		t.Fatalf("session workDir = %q, want chosen %q", sess.WorkDir, chosen)
	}
}

// TestSubmitRunRejectsDisallowedClientChosenWorkDir ensures that a client-
// chosen workDir outside the allowed set is rejected when workdir overrides
// are restricted.
func TestSubmitRunAfterSkillHubPreflightHonorsClientChosenWorkDir(t *testing.T) {
	srv, p := newHistoryRecordingServer(t)
	defer srv.pool.Stop()

	chosen := filepath.Join(t.TempDir(), "chosen-after-preflight")
	if err := os.MkdirAll(chosen, 0o755); err != nil {
		t.Fatalf("mkdir chosen: %v", err)
	}
	const sessionID = "client-created-after-skillhub-preflight"

	// Older WebUI clients queried SkillHub with only the optimistic session ID.
	// That read must not create and bind the session to the serve default.
	state, err := srv.InspectSkillHubSession(sessionID, "")
	if err != nil {
		t.Fatalf("inspect SkillHub session: %v", err)
	}
	if state.SessionID != sessionID {
		t.Fatalf("SkillHub state session ID = %q, want %q", state.SessionID, sessionID)
	}
	if sess := srv.pool.Get(sessionID); sess != nil {
		t.Fatalf("SkillHub preflight materialized session in %q", sess.WorkDir)
	}

	w := submitRun(t, srv, sessionID, `{"message":"workdir after preflight","workDir":"`+chosen+`"}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, body = %s", w.Code, w.Body.String())
	}
	waitForProviderCall(t, p)

	sess := srv.pool.Get(sessionID)
	if sess == nil {
		t.Fatalf("session %q was not created", sessionID)
	}
	if !sameWorkDir(sess.WorkDir, chosen) {
		t.Fatalf("session workDir = %q, want chosen %q", sess.WorkDir, chosen)
	}
}

func TestSubmitRunRejectsDisallowedClientChosenWorkDir(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	allowed := []string{t.TempDir()}
	srv.cfg.AllowedWorkDirs = &allowed
	const sessionID = "client-created-disallowed-workdir"
	outside := t.TempDir() // sibling temp dir, outside the allowed root

	w := submitRun(t, srv, sessionID, `{"message":"workdir test","workDir":"`+outside+`"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("submit status = %d, body = %s, want 403", w.Code, w.Body.String())
	}
}

func TestSubmitRunIdempotencyKeyReturnsExistingRun(t *testing.T) {
	srv, p := newHistoryRecordingServer(t)
	defer srv.pool.Stop()
	const sessionID = "idempotent-submit-session"
	const key = "retry-key-1"
	firstReq := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/runs", strings.NewReader(`{"message":"run once"}`))
	firstReq.Header.Set("Idempotency-Key", key)
	first := httptest.NewRecorder()
	srv.HandleSubmitRun(first, firstReq)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first submit status = %d, body = %s", first.Code, first.Body.String())
	}
	var firstBody map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &firstBody); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	secondReq := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/runs", strings.NewReader(`{"message":"run once"}`))
	secondReq.Header.Set("Idempotency-Key", key)
	second := httptest.NewRecorder()
	srv.HandleSubmitRun(second, secondReq)
	if second.Code != http.StatusAccepted {
		t.Fatalf("second submit status = %d, body = %s", second.Code, second.Body.String())
	}
	var secondBody map[string]any
	if err := json.Unmarshal(second.Body.Bytes(), &secondBody); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	if secondBody["idempotent"] != true || secondBody["runId"] != firstBody["runId"] {
		t.Fatalf("second response = %#v, first = %#v", secondBody, firstBody)
	}
	if calls := p.recordedCalls(); len(calls) > 1 {
		t.Fatalf("provider calls = %d, want at most one", len(calls))
	}
}

func TestSetSubmitIngressEventIDUsesStableRequestKey(t *testing.T) {
	ingresses := []agentruntime.InputIngress{{Origin: "webui", ItemIndex: 9}, {Origin: "webui", ItemIndex: 8}}
	setSubmitIngressEventID(ingresses, "client-key")
	for index, ingress := range ingresses {
		if ingress.EventID != "webui-submit:client-key" || ingress.ItemIndex != index {
			t.Fatalf("ingress %d = %#v, want stable request event and index", index, ingress)
		}
	}
	setSubmitIngressEventID(ingresses, "")
	if ingresses[0].EventID != "webui-submit:client-key" {
		t.Fatal("empty idempotency key should not erase an existing event ID")
	}
}

func TestSubmitRunIdempotencyKeyRejectsDifferentRequest(t *testing.T) {
	srv, _ := newHistoryRecordingServer(t)
	defer srv.pool.Stop()
	const sessionID = "idempotent-submit-conflict-session"
	const key = "retry-key-conflict"
	firstReq := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/runs", strings.NewReader(`{"message":"run once"}`))
	firstReq.Header.Set("Idempotency-Key", key)
	first := httptest.NewRecorder()
	srv.HandleSubmitRun(first, firstReq)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first submit status = %d, body = %s", first.Code, first.Body.String())
	}
	secondReq := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/runs", strings.NewReader(`{"message":"different request"}`))
	secondReq.Header.Set("Idempotency-Key", key)
	second := httptest.NewRecorder()
	srv.HandleSubmitRun(second, secondReq)
	if second.Code != http.StatusConflict || !strings.Contains(second.Body.String(), "idempotency") {
		t.Fatalf("conflicting submit status = %d, body = %s", second.Code, second.Body.String())
	}
}

func TestRetryRunCreatesLinkedAttemptWithoutDuplicatingUserMessage(t *testing.T) {
	srv, p := newHistoryRecordingServer(t)
	defer srv.pool.Stop()
	const sessionID = "linked-retry-session"
	const oldRunID = "run-linked-old"
	sess, err := srv.getOrCreateSession(sessionID, srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if _, err := sess.Manager.AppendMessage(provider.NewUserMessage("retry this request")); err != nil {
		t.Fatalf("append persisted user message: %v", err)
	}
	request, err := json.Marshal(submitRunRequest{Message: "retry this request", Transcript: true})
	if err != nil {
		t.Fatalf("encode intent request: %v", err)
	}
	intent := session.ExecutionIntent{
		ID: "intent-linked", SessionID: sessionID, Source: "webui", Model: "m1", Mode: "yolo", WorkDir: sess.WorkDir,
		Request: request, Policy: json.RawMessage(`{"source":"webui","mode":"yolo"}`), CreatedAt: time.Now(),
	}
	if err := session.SaveExecutionIntent(srv.settings.GetSessionDir(), intent); err != nil {
		t.Fatalf("save intent: %v", err)
	}
	info := agentruntime.ErrorInfo{
		Code: "provider_unavailable", Type: "provider_error", FailureClass: agentruntime.FailureTransient,
		Phase: agentruntime.PhaseModel, MessageKey: "run.error.providerUnavailable", Message: "The service is temporarily unavailable.",
		RetryMode: agentruntime.RetryUser, Retryable: true, RunID: oldRunID, IntentID: intent.ID,
	}
	errorInfo, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("encode error info: %v", err)
	}
	now := time.Now()
	if err := session.CreateSessionRun(srv.settings.GetSessionDir(), session.SessionRun{
		ID: oldRunID, SessionID: sessionID, IntentID: intent.ID, Attempt: 1, WorkDir: sess.WorkDir,
		Source: "webui", Model: "m1", Mode: "yolo", Status: "failed", StartedAt: now, UpdatedAt: now,
		FinishedAt: &now, Error: info.Message, ErrorInfo: errorInfo,
	}); err != nil {
		t.Fatalf("create failed run: %v", err)
	}

	retryReq := httptest.NewRequest(http.MethodPost, "/api/runs/"+oldRunID+"/retry", strings.NewReader(`{}`))
	retryReq.Header.Set("Idempotency-Key", "linked-retry-key")
	first := httptest.NewRecorder()
	srv.HandleRunAPI(first, retryReq)
	if first.Code != http.StatusAccepted {
		t.Fatalf("retry status = %d, body = %s", first.Code, first.Body.String())
	}
	var response struct {
		RunID    string `json:"runId"`
		IntentID string `json:"intentId"`
		Attempt  int    `json:"attempt"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode retry response: %v", err)
	}
	if response.RunID == "" || response.RunID == oldRunID || response.IntentID != intent.ID || response.Attempt != 2 {
		t.Fatalf("retry response = %#v", response)
	}

	call := waitForProviderCall(t, p)
	userCount := 0
	for _, message := range call.Messages {
		if message.Role == "user" && messageText(message) == "retry this request" {
			userCount++
		}
	}
	if userCount != 1 {
		t.Fatalf("provider messages duplicated original user request: %#v", call.Messages)
	}
	old, err := session.GetSessionRun(srv.settings.GetSessionDir(), oldRunID)
	if err != nil || old == nil || old.Status != "failed" {
		t.Fatalf("old run changed during retry: %#v, %v", old, err)
	}
	linked, err := session.GetSessionRun(srv.settings.GetSessionDir(), response.RunID)
	if err != nil || linked == nil || linked.RetryOf != oldRunID || linked.IntentID != intent.ID || linked.Attempt != 2 {
		t.Fatalf("linked retry run = %#v, %v", linked, err)
	}

	duplicateReq := httptest.NewRequest(http.MethodPost, "/api/runs/"+oldRunID+"/retry", strings.NewReader(`{}`))
	duplicateReq.Header.Set("Idempotency-Key", "linked-retry-key")
	second := httptest.NewRecorder()
	srv.HandleRunAPI(second, duplicateReq)
	if second.Code != http.StatusAccepted {
		t.Fatalf("idempotent retry status = %d, body = %s", second.Code, second.Body.String())
	}
	var duplicate struct {
		RunID string `json:"runId"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &duplicate); err != nil || duplicate.RunID != response.RunID {
		t.Fatalf("idempotent retry response = %#v, err=%v", duplicate, err)
	}

	events, err := session.ListSessionRunEvents(srv.settings.GetSessionDir(), sessionID)
	if err != nil {
		t.Fatalf("list retry events: %v", err)
	}
	for _, event := range events {
		if event.RunID != response.RunID || event.EventType != "started" {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal(event.Data, &data); err != nil {
			t.Fatalf("decode retry start event: %v", err)
		}
		if _, found := data["idempotencyKey"]; found {
			t.Fatalf("retry start event persisted plaintext idempotency key: %s", event.Data)
		}
		if got, want := data["idempotencyKeyHash"], idempotencyKeyFingerprint("linked-retry-key"); got != want {
			t.Fatalf("retry key hash = %#v, want %q", got, want)
		}
		if got, want := data["idempotencyScope"], retryIdempotencyScope(intent.ID, oldRunID); got != want {
			t.Fatalf("retry idempotency scope = %#v, want %q", got, want)
		}
		return
	}
	t.Fatalf("missing retry started event for %s", response.RunID)
}

func TestRetryRunRejectsIdempotencyKeyScopedToAnotherRun(t *testing.T) {
	srv, _ := newHistoryRecordingServer(t)
	defer srv.pool.Stop()
	const sessionID = "retry-idempotency-scope-session"
	const firstRunID = "run-retry-first"
	const secondRunID = "run-retry-second"
	const existingAttemptID = "run-retry-existing"
	const key = "retry-key-scoped"

	sess, err := srv.getOrCreateSession(sessionID, srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	request, err := json.Marshal(submitRunRequest{Message: "retry exactly once"})
	if err != nil {
		t.Fatalf("encode intent request: %v", err)
	}
	intent := session.ExecutionIntent{
		ID: "intent-retry-idempotency", SessionID: sessionID, Source: "webui", Model: "m1", Mode: "yolo", WorkDir: sess.WorkDir,
		Request: request, Policy: json.RawMessage(`{"source":"webui","mode":"yolo"}`), CreatedAt: time.Now(),
	}
	if err := session.SaveExecutionIntent(srv.settings.GetSessionDir(), intent); err != nil {
		t.Fatalf("save intent: %v", err)
	}
	info := agentruntime.ErrorInfo{
		Code: "provider_unavailable", Type: "provider_error", FailureClass: agentruntime.FailureTransient,
		Phase: agentruntime.PhaseModel, Message: "The service is temporarily unavailable.",
		RetryMode: agentruntime.RetryUser, Retryable: true, IntentID: intent.ID,
	}
	infoRaw, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("encode error info: %v", err)
	}
	now := time.Now()
	for _, runID := range []string{firstRunID, secondRunID, existingAttemptID} {
		retryOf := ""
		attempt := 1
		if runID == existingAttemptID {
			retryOf = firstRunID
			attempt = 2
		}
		if err := session.CreateSessionRun(srv.settings.GetSessionDir(), session.SessionRun{
			ID: runID, SessionID: sessionID, IntentID: intent.ID, RetryOf: retryOf, Attempt: attempt, WorkDir: sess.WorkDir,
			Source: "webui", Model: "m1", Mode: "yolo", Status: "failed", StartedAt: now, UpdatedAt: now,
			FinishedAt: &now, Error: info.Message, ErrorInfo: infoRaw,
		}); err != nil {
			t.Fatalf("create run %s: %v", runID, err)
		}
	}
	if _, err := session.SaveSessionRunEvent(srv.settings.GetSessionDir(), session.SessionRunEvent{
		SessionID: sessionID, RunID: existingAttemptID, EventType: "started", Source: "webui", Status: "queued", Model: "m1", Mode: "yolo", Timestamp: now,
		Data: rawEventData(map[string]any{
			"idempotencyKeyHash": idempotencyKeyFingerprint(key),
			"idempotencyScope":   retryIdempotencyScope(intent.ID, firstRunID),
			"requestFingerprint": requestFingerprint(submitRunRequest{Message: "retry exactly once"}),
		}),
	}); err != nil {
		t.Fatalf("save existing retry start event: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/runs/"+secondRunID+"/retry", strings.NewReader(`{}`))
	req.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	srv.HandleRunAPI(w, req)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "idempotency") {
		t.Fatalf("cross-run retry key reuse = %d %s, want idempotency conflict", w.Code, w.Body.String())
	}
}

func TestGetRunReturnsLastEventSeq(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	const sessionID = "run-last-event-seq-session"
	const runID = "run-last-event-seq"
	sess, err := srv.getOrCreateSession(sessionID, srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	now := time.Now()
	if err := session.CreateSessionRun(srv.settings.GetSessionDir(), session.SessionRun{
		ID: runID, SessionID: sessionID, IntentID: "intent-last-event-seq", Attempt: 1, WorkDir: sess.WorkDir,
		Source: "webui", Model: "m1", Mode: "yolo", Status: "running", StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	for _, eventType := range []string{"started", "run_retrying"} {
		if _, err := session.SaveSessionRunEvent(srv.settings.GetSessionDir(), session.SessionRunEvent{
			SessionID: sessionID, RunID: runID, EventType: eventType, Source: "webui", Status: "running", Model: "m1", Mode: "yolo", Timestamp: time.Now(),
		}); err != nil {
			t.Fatalf("save %s event: %v", eventType, err)
		}
	}
	sequenced, err := session.ListSessionRunEventsWithSeq(srv.settings.GetSessionDir(), sessionID)
	if err != nil || len(sequenced) == 0 {
		t.Fatalf("list sequenced events = %#v, %v", sequenced, err)
	}
	wantSeq := sequenced[len(sequenced)-1].Seq

	req := httptest.NewRequest(http.MethodGet, "/api/runs/"+runID, nil)
	w := httptest.NewRecorder()
	srv.HandleRunAPI(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get run = %d %s", w.Code, w.Body.String())
	}
	var response runAPIView
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode get run response: %v", err)
	}
	if response.LastEventSeq != wantSeq {
		t.Fatalf("last event sequence = %d, want %d", response.LastEventSeq, wantSeq)
	}
}

func TestGetRunPreservesStorageFailureAndReturnsSafeAPIError(t *testing.T) {
	sessionDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(sessionDir, "sessions.db"), 0o755); err != nil {
		t.Fatalf("create invalid database path: %v", err)
	}
	settings := config.DefaultSettings()
	settings.SessionDir = sessionDir
	srv := &Server{settings: settings}

	if _, err := srv.GetRun("run-storage-error"); err == nil || errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("GetRun error = %v, want underlying storage error", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/runs/run-storage-error", nil)
	w := httptest.NewRecorder()
	srv.HandleRunAPI(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("get run status = %d, body = %s", w.Code, w.Body.String())
	}
	var response ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if response.Error.Code != "run_lookup_failed" || response.Error.FailureClass != string(agentruntime.FailurePersistence) || response.Error.RetryMode != string(agentruntime.RetryReconcile) {
		t.Fatalf("safe lookup error = %#v", response.Error)
	}
	if strings.Contains(response.Error.Message, sessionDir) || strings.Contains(response.Error.Message, "sessions.db") {
		t.Fatalf("safe lookup message leaked storage path: %q", response.Error.Message)
	}
}

func TestGetRunReadsCanonicalStoreWithoutRunManager(t *testing.T) {
	sessionDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.SessionDir = sessionDir
	startedAt := time.Now().Add(-time.Minute)
	finishedAt := time.Now()
	if err := session.SaveSessionRun(sessionDir, session.SessionRun{
		ID: "cross-process-run", SessionID: "cross-process-session", WorkDir: t.TempDir(),
		Status: "completed", StartedAt: startedAt, UpdatedAt: finishedAt, FinishedAt: &finishedAt,
	}); err != nil {
		t.Fatalf("save canonical run: %v", err)
	}

	// A fresh Serve process has no in-memory RunManager entry for a Run created
	// by another process. GetRun must still read the canonical Runtime store.
	srv := &Server{settings: settings}
	run, err := srv.GetRun("cross-process-run")
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if run == nil || run.SessionID != "cross-process-session" || run.Status != "completed" {
		t.Fatalf("canonical run = %+v", run)
	}
}

func TestRunAPIResponseReturnsCursorReadFailure(t *testing.T) {
	sessionDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(sessionDir, "sessions.db"), 0o755); err != nil {
		t.Fatalf("create invalid database path: %v", err)
	}
	_, err := runAPIResponse(sessionDir, &session.SessionRun{ID: "run-cursor-error", SessionID: "session-cursor-error", StartedAt: time.Now(), UpdatedAt: time.Now()})
	if err == nil {
		t.Fatal("runAPIResponse unexpectedly accepted an unreadable event store")
	}
}

func TestRetryRunUnknownRunReturnsStructuredSafeError(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	req := httptest.NewRequest(http.MethodPost, "/api/runs/run-does-not-exist/retry", strings.NewReader(`{}`))
	req.Header.Set("Idempotency-Key", "unknown-run-retry-key")
	w := httptest.NewRecorder()
	srv.HandleRunAPI(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("retry unknown run status = %d, body = %s", w.Code, w.Body.String())
	}
	var response ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode retry error response: %v", err)
	}
	if response.Error.Code != "run_not_found" || response.Error.MessageKey != "run.error.notFound" || response.Error.RunID != "run-does-not-exist" {
		t.Fatalf("retry unknown run error = %#v", response.Error)
	}
	if strings.Contains(strings.ToLower(response.Error.Message), "session not found") {
		t.Fatalf("retry error leaked internal lookup message: %q", response.Error.Message)
	}
}

func TestFindIdempotentRunRejectsLegacySubmitKeyForRetry(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	const sessionID = "legacy-idempotency-scope-session"
	const key = "legacy-submit-key"
	sess, err := srv.getOrCreateSession(sessionID, srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if _, err := session.SaveSessionRunEvent(srv.settings.GetSessionDir(), session.SessionRunEvent{
		SessionID: sess.ID, RunID: "legacy-submit-run", EventType: "started", Source: "webui", Status: "queued", Timestamp: time.Now(),
		Data: rawEventData(map[string]any{"idempotencyKey": key, "requestFingerprint": "legacy-request"}),
	}); err != nil {
		t.Fatalf("save legacy started event: %v", err)
	}
	_, err = findIdempotentRun(srv.settings.GetSessionDir(), sess.ID, key, "legacy-request", retryIdempotencyScope("intent-legacy", "legacy-submit-run"))
	if !errors.Is(err, ErrIdempotencyKeyConflict) {
		t.Fatalf("legacy submit key used for retry error = %v, want idempotency conflict", err)
	}
}

func TestRetryRunRequiresConfirmationForUnknownSideEffects(t *testing.T) {
	srv, _ := newHistoryRecordingServer(t)
	defer srv.pool.Stop()
	const sessionID = "retry-confirm-session"
	const runID = "run-confirm"
	sess, err := srv.getOrCreateSession(sessionID, srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	request, _ := json.Marshal(submitRunRequest{Message: "change something"})
	intent := session.ExecutionIntent{ID: "intent-confirm", SessionID: sessionID, Source: "webui", Model: "m1", Mode: "yolo", WorkDir: sess.WorkDir, Request: request, Policy: json.RawMessage(`{}`), CreatedAt: time.Now()}
	if err := session.SaveExecutionIntent(srv.settings.GetSessionDir(), intent); err != nil {
		t.Fatalf("save intent: %v", err)
	}
	infoRaw, _ := json.Marshal(agentruntime.ErrorInfo{
		Code: "provider_unavailable", Type: "provider_error", FailureClass: agentruntime.FailureTransient,
		Message: "The service is temporarily unavailable.", RetryMode: agentruntime.RetryDecisionRequired,
		Retryable: true, SideEffectState: agentruntime.SideEffectUnknown, RunID: runID, IntentID: intent.ID,
	})
	now := time.Now()
	if err := session.CreateSessionRun(srv.settings.GetSessionDir(), session.SessionRun{
		ID: runID, SessionID: sessionID, IntentID: intent.ID, Attempt: 1, WorkDir: sess.WorkDir, Source: "webui", Model: "m1", Mode: "yolo",
		Status: "failed", StartedAt: now, UpdatedAt: now, FinishedAt: &now, ErrorInfo: infoRaw,
	}); err != nil {
		t.Fatalf("create failed run: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/runs/"+runID+"/retry", strings.NewReader(`{}`))
	req.Header.Set("Idempotency-Key", "confirm-key")
	w := httptest.NewRecorder()
	srv.HandleRunAPI(w, req)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "retry_confirmation_required") {
		t.Fatalf("unconfirmed retry = %d %s", w.Code, w.Body.String())
	}
}

const testPNGDataURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl8P6sAAAAASUVORK5CYII="

// TestSubmitRunMaterializesImages verifies that WebUI data URLs become
// project-relative Runtime resources rather than first-turn image blocks.
func TestSubmitRunMaterializesImages(t *testing.T) {
	srv, p := newHistoryRecordingServer(t)
	p.models[0].Input = []string{"text", "image"}

	w := submitRun(t, srv, "run-images-session", fmt.Sprintf(`{"message":"看图","images":[%q],"transcript":true}`, testPNGDataURL))
	if w.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, body = %s", w.Code, w.Body.String())
	}

	call := waitForProviderCall(t, p)
	last := call.Messages[len(call.Messages)-1]
	if last.Role != "user" {
		t.Fatalf("last message role = %q, want user", last.Role)
	}
	if text := messageText(last); !strings.Contains(text, "看图") || !strings.Contains(text, ".mothx/tmp/inputs/") {
		t.Fatalf("path manifest missing from submitted message: %#v", last)
	}
	for _, block := range last.Contents {
		if block.Image != nil {
			t.Fatalf("first-turn image content leaked to provider: %#v", last.Contents)
		}
	}
}

func TestSubmitRunAcceptsImageResourceForTextOnlyModel(t *testing.T) {
	srv, p := newHistoryRecordingServer(t)
	w := submitRun(t, srv, "run-images-text-only", fmt.Sprintf(`{"message":"看图","images":[%q]}`, testPNGDataURL))
	if w.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, want 202, body = %s", w.Code, w.Body.String())
	}
	call := waitForProviderCall(t, p)
	last := call.Messages[len(call.Messages)-1]
	if !strings.Contains(messageText(last), ".mothx/tmp/inputs/") {
		t.Fatalf("text-only input was not path-only: %#v", last)
	}
	for _, block := range last.Contents {
		if block.Image != nil {
			t.Fatalf("text-only first turn contained image: %#v", last.Contents)
		}
	}
}

func TestSubmitRunArtifactToolDisabledByDefault(t *testing.T) {
	srv, p := newHistoryRecordingServer(t)
	defer srv.pool.Stop()

	w := submitRun(t, srv, "run-artifact-disabled", `{"message":"create a report","transcript":true}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, body = %s", w.Code, w.Body.String())
	}
	call := waitForProviderCall(t, p)
	for _, tool := range call.Tools {
		if tool.Name == "publish_artifact" {
			t.Fatalf("publish_artifact was offered while WebUI artifacts were disabled: %#v", call.Tools)
		}
	}
}

func TestSubmitRunNormalizesFileAttachmentThroughRuntime(t *testing.T) {
	srv, p := newHistoryRecordingServer(t)
	srv.cfg.EnableArtifact = true
	sessionID := "run-file-attachment-session"
	w := submitRun(t, srv, sessionID, `{"message":"inspect the attached file","attachments":[{"kind":"file","filename":"notes.txt","mediaType":"text/plain","dataUrl":"data:text/plain;base64,aGVsbG8gYXR0YWNobWVudA==","size":16}],"transcript":true}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, body = %s", w.Code, w.Body.String())
	}
	var accepted struct {
		RunID    string `json:"runId"`
		IntentID string `json:"intentId"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode submit response: %v", err)
	}
	call := waitForProviderCall(t, p)
	last := call.Messages[len(call.Messages)-1]
	lastText := messageText(last)
	if !strings.Contains(lastText, ".mothx/tmp/inputs/") || strings.Contains(lastText, "hello attachment") || strings.Contains(lastText, "read_attachment") {
		t.Fatalf("file prompt = %q, want Runtime manifest without raw content", lastText)
	}
	foundPublishTool := false
	for _, tool := range call.Tools {
		if tool.Name == "read_attachment" {
			t.Fatalf("legacy read_attachment tool remained registered: %#v", call.Tools)
		}
		if tool.Name == "publish_artifact" {
			foundPublishTool = true
		}
	}
	if !foundPublishTool {
		t.Fatalf("Runtime publish_artifact tool missing from provider definitions: %#v", call.Tools)
	}
	intent, err := (agentruntime.RunStore{SessionDir: srv.settings.GetSessionDir()}).GetIntent(accepted.IntentID)
	if err != nil || intent == nil {
		t.Fatalf("get intent: %v, %#v", err, intent)
	}
	if strings.Contains(string(intent.Request), "aGVsbG8gYXR0YWNobWVudA==") || !strings.Contains(string(intent.Request), "attachmentId") {
		t.Fatalf("durable request leaked data URL or missed canonical attachment ID: %s", intent.Request)
	}
	var stored submitRunRequest
	if err := json.Unmarshal(intent.Request, &stored); err != nil || len(stored.Attachments) != 1 {
		t.Fatalf("decode normalized intent request: %v, %#v", err, stored)
	}
	materializer, err := agentruntime.NewInputMaterializer(srv.settings.GetSessionDir(), srv.cfg.GetWorkDir(), agentruntime.DefaultInputPolicy())
	if err != nil {
		t.Fatalf("new input materializer: %v", err)
	}
	record, err := materializer.Get(context.Background(), sessionID, stored.Attachments[0].AttachmentID)
	if err != nil {
		t.Fatalf("get persisted input resource: %v", err)
	}
	data, readErr := os.ReadFile(filepath.Join(srv.cfg.GetWorkDir(), filepath.FromSlash(record.RelativePath)))
	if readErr != nil || string(data) != "hello attachment" || record.RunID != accepted.RunID {
		t.Fatalf("persisted input = %#v, data=%q, read=%v", record, data, readErr)
	}
}

// TestSubmitRunAppliesToolOptionsAndMode verifies the submit body `tools`
// array controls local capabilities and an explicit `mode` is persisted for
// subsequent runs. Hosted configuration is preserved independently.
func TestSubmitRunAppliesToolOptionsAndMode(t *testing.T) {
	srv, p := newHistoryRecordingServer(t)
	srv.settings.WebSearch.Enabled = config.BoolPtr(true)

	sessionID := "run-tools-session"
	w := submitRun(t, srv, sessionID, `{"message":"hi","mode":"plan","tools":["webSearch"],"transcript":true}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, body = %s", w.Code, w.Body.String())
	}
	waitForProviderCall(t, p)

	caps, err := srv.GetSessionCapabilities(sessionID)
	if err != nil {
		t.Fatalf("GetSessionCapabilities: %v", err)
	}
	if caps.Mode != "plan" {
		t.Fatalf("mode = %q, want plan (submit mode must persist)", caps.Mode)
	}
	if !caps.WebSearch {
		t.Fatal("hosted webSearch setting should not be disabled by local tools array")
	}
	if caps.Browser || caps.MultiAgent || caps.Workflows || caps.DelegateMode || caps.A2AMaster {
		t.Fatalf("unlisted capabilities must be disabled: %#v", caps)
	}

	// Unknown tool names are rejected.
	w = submitRun(t, srv, "run-tools-bogus", `{"message":"hi","tools":["bogusTool"]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("submit status = %d, want 400, body = %s", w.Code, w.Body.String())
	}
}

// TestSubmitRunAppliesSkills verifies the submit body `skills` array activates
// session skills, and unknown skills are rejected.
func TestSubmitRunAppliesSkills(t *testing.T) {
	srv, p := newHistoryRecordingServer(t)
	defer srv.pool.Stop()

	skillDir := filepath.Join(srv.cfg.GetWorkDir(), ".skills", "demo-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Demo Skill\n"), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	sessionID := "run-skills-session"
	w := submitRun(t, srv, sessionID, `{"message":"hi","skills":["demo-skill"],"transcript":true}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, body = %s", w.Code, w.Body.String())
	}
	waitForProviderCall(t, p)

	sess := srv.pool.Get(sessionID)
	if sess == nil {
		t.Fatal("session not found in pool")
	}
	if !sess.ActiveSkills["demo-skill"] {
		t.Fatalf("demo-skill should be active: %#v", sess.ActiveSkills)
	}

	// Unknown skills are rejected.
	w = submitRun(t, srv, "run-skills-bogus", `{"message":"hi","skills":["missing-skill"]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("submit status = %d, want 400, body = %s", w.Code, w.Body.String())
	}
}

func TestSessionToolOptionsFromNamesDoesNotOverrideHostedTools(t *testing.T) {
	options, err := sessionToolOptionsFromNames([]string{"webSearch", "browser"})
	if err != nil {
		t.Fatalf("sessionToolOptionsFromNames: %v", err)
	}
	if options == nil || options.WebSearch != nil {
		t.Fatalf("hosted webSearch option = %#v, want nil", options)
	}
	if options.Browser == nil || !*options.Browser {
		t.Fatalf("browser option = %#v, want enabled", options.Browser)
	}
}
