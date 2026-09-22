package openaiapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	openaiprovider "github.com/oschina/mothx/internal/provider/openai"
	"github.com/oschina/mothx/internal/session"
)

type recoveryRunDriver struct {
	run *session.ResponseRun
}

func (d *recoveryRunDriver) Start(context.Context, string, string, provider.ChatParams) (*session.ResponseRun, error) {
	return d.run, nil
}
func (d *recoveryRunDriver) Continue(context.Context, string, string, *session.ResponseRun, []provider.Message, provider.ChatParams) (*session.ResponseRun, error) {
	return d.run, nil
}
func (d *recoveryRunDriver) Get(context.Context, string, string) (*session.ResponseRun, error) {
	copy := *d.run
	return &copy, nil
}
func (d *recoveryRunDriver) Cancel(context.Context, string, string) error { return nil }

func TestRegisterRoutesResponsesRunAPI(t *testing.T) {
	srv := newTestServer(t)
	mux := http.NewServeMux()
	registerRoutes(mux, srv, RunOptions{})

	req := httptest.NewRequest(http.MethodGet, "/api/responses/runs/run-1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code == http.StatusNotFound {
		t.Fatalf("Responses run route returned 404; route was not registered")
	}
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("Responses run route status = %d, want 501 for non-Responses provider", w.Code)
	}
}

func TestRegisterRoutesResponsesRunAPIDisabledWithAPI(t *testing.T) {
	srv := newTestServer(t)
	mux := http.NewServeMux()
	registerRoutes(mux, srv, RunOptions{DisableAPI: true})

	req := httptest.NewRequest(http.MethodGet, "/api/responses/runs/run-1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("disabled Responses run route status = %d, want 404", w.Code)
	}
}

func TestResponsesRunAPIAbandonMarksInterruptedToolsWithoutRetry(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	p := openaiprovider.NewProviderWithModels("test-key", "https://example.invalid/v1", []*provider.Model{srv.model})
	p.SetUseResponsesAPI(true)
	if err := p.SetResponsesConfig(config.ResponsesConfig{Background: config.BoolPtr(true)}); err != nil {
		t.Fatalf("set Responses config: %v", err)
	}
	srv.provider = p
	srv.responsesRuns = p.NewResponsesRunManager(srv.settings.GetSessionDir())
	srv.runManager = NewRunManager(srv.settings.GetSessionDir())

	sess, err := srv.getOrCreateSession("abandon-session", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now()
	if err := session.SaveSessionRun(srv.settings.GetSessionDir(), session.SessionRun{
		ID: "abandon-local", SessionID: sess.ID, WorkDir: sess.WorkDir, Source: "responses_background",
		Model: srv.model.ID, Mode: "yolo", Status: "failed", StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save local run: %v", err)
	}
	if err := session.SaveResponseRun(srv.settings.GetSessionDir(), session.ResponseRun{
		SessionID: sess.ID, LocalRunID: "abandon-remote", LocalTurnID: "abandon-local", ResponseID: "resp-abandon",
		Provider: p.Name(), API: p.API(), State: "completed", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save response run: %v", err)
	}
	record := session.ToolExecutionRecord{
		SessionID: sess.ID, LocalTurnID: "abandon-local", ExecutionKey: "abandon-tool", Provider: p.Name(), API: p.API(),
		ProviderCallID: "call-abandon", ToolKind: "function", ToolName: "write", ArgsHash: "args", ExecutionState: "running",
	}
	if _, created, err := session.ClaimToolExecutionRecord(srv.settings.GetSessionDir(), record); err != nil || !created {
		t.Fatalf("claim interrupted tool: created=%v err=%v", created, err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/responses/runs/abandon-remote/abandon?session_id="+sess.ID, nil)
	w := httptest.NewRecorder()
	srv.HandleResponsesRunAPI(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("abandon response status = %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode abandon response: %v", err)
	}
	if body["abandonedToolExecutions"] != float64(1) {
		t.Fatalf("abandon response = %#v", body)
	}
	// ClaimToolExecutionRecord is an idempotent write (INSERT ... ON CONFLICT),
	// not a read-only lookup. The abandon endpoint has released its runtime
	// lease by this point, so inspect the stored record under a fresh short
	// lease just as a recovery caller would.
	var release func()
	var locked bool
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		release, locked = session.TryLockRuntime(srv.settings.GetSessionDir(), sess.ID)
		if locked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !locked {
		t.Fatal("could not acquire runtime lease to inspect abandoned tool record")
	}
	stored, created, err := session.ClaimToolExecutionRecord(srv.settings.GetSessionDir(), record)
	release()
	if err != nil || created || stored.ExecutionState != "abandoned" {
		t.Fatalf("abandoned tool record = %#v, created=%v err=%v", stored, created, err)
	}
	local, err := session.GetSessionRun(srv.settings.GetSessionDir(), "abandon-local")
	if err != nil || local == nil || local.Status != "failed" || local.Error != "abandoned after interrupted tool execution" {
		t.Fatalf("abandoned local run = %#v, err=%v", local, err)
	}
}

func TestResponsesRunAPIRecoverStartsFreshAgentLoopAndPreservesTerminalParent(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	recording := newHistoryRecordingProvider()
	srv.provider = recording
	srv.providerName = recording.Name()
	srv.model = recording.models[0]
	srv.runManager = NewRunManager(srv.settings.GetSessionDir())
	sess, err := srv.getOrCreateSession("recover-session", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now()
	if err := session.SaveSessionRun(srv.settings.GetSessionDir(), session.SessionRun{
		ID: "recover-parent", SessionID: sess.ID, WorkDir: sess.WorkDir, Source: "responses_background",
		Model: srv.model.ID, Mode: "yolo", Status: "failed", StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save parent run: %v", err)
	}
	remote := &session.ResponseRun{SessionID: sess.ID, LocalRunID: "recover-remote", LocalTurnID: "recover-parent", ResponseID: "resp-recover", Provider: recording.Name(), API: "openai-responses", State: "completed", CreatedAt: now, UpdatedAt: now}
	if err := session.SaveResponseRun(srv.settings.GetSessionDir(), *remote); err != nil {
		t.Fatalf("save response run: %v", err)
	}
	if err := session.SaveResponseItem(srv.settings.GetSessionDir(), session.ResponseItemArchive{
		SessionID: sess.ID, LocalTurnID: "recover-parent", ResponseID: "resp-recover", ItemID: "fc-recover",
		OutputIndex: 0, ItemType: "function_call", ItemStatus: "completed", CreatedAt: now,
		SanitizedJSON: json.RawMessage(`{"id":"fc-recover","type":"function_call","call_id":"call-recover","name":"write","arguments":"{\"path\":\"recovered.txt\"}"}`),
	}); err != nil {
		t.Fatalf("save archived tool call: %v", err)
	}
	if _, created, err := session.ClaimToolExecutionRecord(srv.settings.GetSessionDir(), session.ToolExecutionRecord{
		SessionID: sess.ID, LocalTurnID: "recover-parent", ExecutionKey: "recover-tool", ProviderCallID: "call-recover",
		Provider: "test", API: "openai-responses", ToolKind: "function", ToolName: "write", ArgsHash: "hash", ExecutionState: "running", SideEffecting: true,
	}); err != nil || !created {
		t.Fatalf("save interrupted tool: created=%v err=%v", created, err)
	}
	srv.responsesRuns = &recoveryRunDriver{run: remote}
	req := httptest.NewRequest(http.MethodPost, "/api/responses/runs/recover-remote/recover?session_id="+sess.ID, strings.NewReader(`{"confirm":true,"toolCallIds":["call-recover"]}`))
	w := httptest.NewRecorder()
	srv.HandleResponsesRunAPI(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("recover status = %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode recover response: %v", err)
	}
	if body["recoveryRequested"] != float64(1) {
		t.Fatalf("recover response = %#v", body)
	}
	if body["reattached"] != false {
		t.Fatalf("terminal response run was reattached: %#v", body)
	}
	newRunID, _ := body["runId"].(string)
	if newRunID == "" || newRunID == "recover-parent" {
		t.Fatalf("fresh recovery run ID = %q, response = %#v", newRunID, body)
	}
	parent, err := session.GetSessionRun(srv.settings.GetSessionDir(), "recover-parent")
	if err != nil || parent == nil || parent.Status != "failed" {
		t.Fatalf("terminal parent was mutated: run=%#v err=%v", parent, err)
	}
	fresh, err := session.GetSessionRun(srv.settings.GetSessionDir(), newRunID)
	if err != nil || fresh == nil || fresh.ID == parent.ID || fresh.RetryOf != "" {
		t.Fatalf("fresh recovery run = %#v err=%v", fresh, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(recording.recordedCalls()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	calls := recording.recordedCalls()
	if len(calls) == 0 {
		t.Fatal("fresh recovery message did not start AgentLoop")
	}
	messages := calls[0].Messages
	if len(messages) == 0 {
		t.Fatal("AgentLoop request did not contain the recovery message")
	}
	last := messageText(messages[len(messages)-1])
	for _, want := range []string{"terminal and must not be resumed", "call-recover", `{"path":"recovered.txt"}`} {
		if !strings.Contains(last, want) {
			t.Fatalf("recovery message missing %q: %s", want, last)
		}
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fresh, err = session.GetSessionRun(srv.settings.GetSessionDir(), newRunID)
		if err == nil && fresh != nil && session.IsTerminalSessionRunStatus(fresh.Status) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if fresh == nil || fresh.Status != "completed" {
		t.Fatalf("fresh AgentLoop run did not complete: %#v", fresh)
	}

	var release func()
	var locked bool
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		release, locked = session.TryLockRuntime(srv.settings.GetSessionDir(), sess.ID)
		if locked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !locked {
		t.Fatal("could not acquire runtime lease to inspect recovery record")
	}
	stored, created, err := session.ClaimToolExecutionRecord(srv.settings.GetSessionDir(), session.ToolExecutionRecord{
		SessionID: sess.ID, LocalTurnID: "recover-parent", ExecutionKey: "recover-tool", ProviderCallID: "call-recover",
		Provider: "test", API: "openai-responses", ToolKind: "function", ToolName: "write", ArgsHash: "hash", ExecutionState: "running", SideEffecting: true,
	})
	release()
	if err != nil || created || stored.ExecutionState != "retry_requested" {
		t.Fatalf("recovery record = %#v, created=%v err=%v", stored, created, err)
	}

	// Repeating the same confirmed recovery reconciles to the new Run instead
	// of creating another attempt or touching the terminal parent.
	repeatReq := httptest.NewRequest(http.MethodPost, "/api/responses/runs/recover-remote/recover?session_id="+sess.ID, strings.NewReader(`{"confirm":true,"toolCallIds":["call-recover"]}`))
	repeat := httptest.NewRecorder()
	srv.HandleResponsesRunAPI(repeat, repeatReq)
	if repeat.Code != http.StatusAccepted {
		t.Fatalf("repeated recover status = %d: %s", repeat.Code, repeat.Body.String())
	}
	var repeatedBody map[string]any
	if err := json.Unmarshal(repeat.Body.Bytes(), &repeatedBody); err != nil {
		t.Fatalf("decode repeated recovery response: %v", err)
	}
	if repeatedBody["runId"] != newRunID || repeatedBody["idempotent"] != true {
		t.Fatalf("repeated recovery did not reconcile to %q: %#v", newRunID, repeatedBody)
	}
}

func TestResponsesRunAPIReconnectReattachesDurableBackgroundRun(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	p := openaiprovider.NewProviderWithModels("test-key", "https://example.invalid/v1", []*provider.Model{srv.model})
	p.SetUseResponsesAPI(true)
	if err := p.SetResponsesConfig(config.ResponsesConfig{Background: config.BoolPtr(true)}); err != nil {
		t.Fatalf("set Responses config: %v", err)
	}
	srv.provider = p
	srv.responsesRuns = p.NewResponsesRunManager(srv.settings.GetSessionDir())
	srv.runManager = NewRunManager(srv.settings.GetSessionDir())

	sess, err := srv.getOrCreateSession("reconnect-session", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now()
	if err := session.SaveSessionRun(srv.settings.GetSessionDir(), session.SessionRun{
		ID: "reconnect-local", SessionID: sess.ID, WorkDir: sess.WorkDir, Source: "responses_background",
		Model: srv.model.ID, Mode: "yolo", Status: "running", StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save local run: %v", err)
	}
	if err := session.SaveResponseRun(srv.settings.GetSessionDir(), session.ResponseRun{
		SessionID: sess.ID, LocalRunID: "reconnect-remote", LocalTurnID: "reconnect-local", ResponseID: "resp-reconnect",
		Provider: p.Name(), API: p.API(), State: "completed", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save response run: %v", err)
	}
	if err := session.SaveResponseTurn(srv.settings.GetSessionDir(), session.ResponseTurn{
		SessionID: sess.ID, LocalTurnID: "reconnect-local", ResponseID: "resp-reconnect", Provider: p.Name(), API: p.API(),
		Model: "background", StateMode: "replay", Status: "completed", ResponseSummary: json.RawMessage(`{"status":"completed"}`), CreatedAt: now,
	}); err != nil {
		t.Fatalf("save response archive: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/responses/runs/reconnect-remote/reconnect?session_id="+sess.ID, nil)
	w := httptest.NewRecorder()
	srv.HandleResponsesRunAPI(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("reconnect response status = %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Reattached bool `json:"reattached"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || !body.Reattached {
		t.Fatalf("reconnect body = %s, err=%v", w.Body.String(), err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		local, err := session.GetSessionRun(srv.settings.GetSessionDir(), "reconnect-local")
		if err == nil && local != nil && local.Status == "completed" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	local, err := session.GetSessionRun(srv.settings.GetSessionDir(), "reconnect-local")
	t.Fatalf("reattached local run did not complete: %#v, err=%v", local, err)
}

func TestResponsesRunAPIReconnectRejectsSharedRuntimeConflict(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/responses/resp-conflict") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"resp-conflict","status":"in_progress"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	p := openaiprovider.NewProviderWithModels("test-key", upstream.URL, []*provider.Model{srv.model})
	p.SetUseResponsesAPI(true)
	if err := p.SetResponsesConfig(config.ResponsesConfig{Background: config.BoolPtr(true)}); err != nil {
		t.Fatal(err)
	}
	srv.provider = p
	srv.responsesRuns = p.NewResponsesRunManager(srv.settings.GetSessionDir())
	srv.runManager = NewRunManager(srv.settings.GetSessionDir())
	sess, err := srv.getOrCreateSession("reconnect-conflict", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := session.SaveSessionRun(srv.settings.GetSessionDir(), session.SessionRun{
		ID: "reconnect-conflict-local", SessionID: sess.ID, WorkDir: sess.WorkDir,
		Source: "responses_background", Model: srv.model.ID, Mode: "yolo", Status: "running", StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.SaveResponseRun(srv.settings.GetSessionDir(), session.ResponseRun{
		SessionID: sess.ID, LocalRunID: "reconnect-conflict-remote", LocalTurnID: "reconnect-conflict-local",
		ResponseID: "resp-conflict", Provider: p.Name(), API: p.API(), State: "running", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	release := session.LockRuntime(srv.settings.GetSessionDir(), sess.ID)
	defer release()
	req := httptest.NewRequest(http.MethodPost, "/api/responses/runs/reconnect-conflict-remote/reconnect?session_id="+sess.ID, nil)
	w := httptest.NewRecorder()
	srv.HandleResponsesRunAPI(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("reconnect status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestResponsesRunAPICancelRejectsSharedRuntimeConflict(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	p := openaiprovider.NewProviderWithModels("test-key", "https://example.invalid/v1", []*provider.Model{srv.model})
	p.SetUseResponsesAPI(true)
	if err := p.SetResponsesConfig(config.ResponsesConfig{Background: config.BoolPtr(true)}); err != nil {
		t.Fatal(err)
	}
	srv.provider = p
	srv.responsesRuns = p.NewResponsesRunManager(srv.settings.GetSessionDir())
	sess, err := srv.getOrCreateSession("cancel-conflict", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := session.SaveResponseRun(srv.settings.GetSessionDir(), session.ResponseRun{
		SessionID: sess.ID, LocalRunID: "cancel-remote", LocalTurnID: "cancel-local", ResponseID: "resp-cancel",
		Provider: p.Name(), API: p.API(), State: "in_progress", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	release := session.LockRuntime(srv.settings.GetSessionDir(), sess.ID)
	defer release()
	req := httptest.NewRequest(http.MethodPost, "/api/responses/runs/cancel-remote/cancel?session_id="+sess.ID, nil)
	w := httptest.NewRecorder()
	srv.HandleResponsesRunAPI(w, req)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"code":"session_reserved"`) {
		t.Fatalf("cancel status = %d, body = %s", w.Code, w.Body.String())
	}
}
