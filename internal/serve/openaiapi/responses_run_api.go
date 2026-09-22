package openaiapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

var errResponseRunWorkDirNotAllowed = errors.New("response run work directory is not allowed")

// HandleResponsesRunAPI exposes durable OpenAI Responses background runs.
// Paths:
//
//	GET  /api/responses/runs/{localRunID}?session_id={sessionID}
//	POST /api/responses/runs/{localRunID}/cancel?session_id={sessionID}
//	POST /api/responses/runs/{localRunID}/reconnect?session_id={sessionID}
//	POST /api/responses/runs/{localRunID}/abandon?session_id={sessionID}
//	POST /api/responses/runs/{localRunID}/recover?session_id={sessionID}
func (s *Server) HandleResponsesRunAPI(w http.ResponseWriter, r *http.Request) {
	if s == nil {
		writeError(w, http.StatusServiceUnavailable, "API server not ready", "server_error")
		return
	}
	s.mu.RLock()
	manager := s.responsesRuns
	s.mu.RUnlock()
	if manager == nil {
		writeError(w, http.StatusNotImplemented, "Responses background runs are unavailable for the active provider", "capability_error")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/responses/runs/")
	path = strings.TrimSuffix(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusBadRequest, "response run ID required", "invalid_request_error")
		return
	}
	localRunID := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	if len(parts) > 2 || (action != "" && action != "cancel" && action != "reconnect" && action != "abandon" && action != "recover") {
		writeError(w, http.StatusBadRequest, "invalid response run path", "invalid_request_error")
		return
	}

	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session_id is required", "invalid_request_error")
		return
	}
	if err := s.authorizeResponseRunSession(sessionID); err != nil {
		status := http.StatusInternalServerError
		errType := "server_error"
		if errors.Is(err, ErrSessionNotFound) {
			status = http.StatusNotFound
			errType = "not_found"
		} else if errors.Is(err, errResponseRunWorkDirNotAllowed) {
			status = http.StatusForbidden
			errType = "permission_error"
		}
		writeError(w, status, err.Error(), errType)
		return
	}

	switch {
	case r.Method == http.MethodGet && action == "":
		run, err := manager.Get(r.Context(), sessionID, localRunID)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error(), "upstream_error")
			return
		}
		if run == nil {
			writeError(w, http.StatusNotFound, "response run not found", "not_found")
			return
		}
		writeJSON(w, http.StatusOK, run)
	case r.Method == http.MethodPost && action == "cancel":
		// A durable remote cancel mutates response lineage and must serialize
		// with lifecycle deletion/transfer. A live local monitor owns this lock;
		// callers should use the session stop endpoint first in that window.
		guard, err := session.AcquireMutation(s.settings.GetSessionDir(), sessionID)
		if err != nil {
			status, info := s.executionAdmissionError(sessionID, err)
			writeErrorInfo(w, status, info)
			return
		}
		defer guard.Release()
		if err := manager.Cancel(r.Context(), sessionID, localRunID); err != nil {
			writeError(w, http.StatusBadGateway, err.Error(), "upstream_error")
			return
		}
		run, err := manager.Get(r.Context(), sessionID, localRunID)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error(), "upstream_error")
			return
		}
		writeJSON(w, http.StatusAccepted, run)
	case r.Method == http.MethodPost && action == "reconnect":
		run, err := manager.Get(r.Context(), sessionID, localRunID)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error(), "upstream_error")
			return
		}
		if run == nil {
			writeError(w, http.StatusNotFound, "response run not found", "not_found")
			return
		}
		parent, err := s.responsesBackgroundParentRun(sessionID, run.LocalTurnID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "server_error")
			return
		}
		if parent == nil {
			writeError(w, http.StatusConflict, "response run has no recoverable local background run", "conflict_error")
			return
		}
		reattached, err := s.reattachResponsesBackgroundRun(*parent, run)
		if err != nil {
			if errors.Is(err, ErrResponsesRuntimeBusy) {
				status, info := s.executionAdmissionError(sessionID, err)
				writeErrorInfo(w, status, info)
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error(), "server_error")
			return
		}
		status := http.StatusOK
		if reattached {
			status = http.StatusAccepted
		}
		writeJSON(w, status, map[string]any{"run": run, "reattached": reattached})
	case r.Method == http.MethodPost && action == "abandon":
		s.abandonResponsesRun(w, r, manager, sessionID, localRunID)
	case r.Method == http.MethodPost && action == "recover":
		s.recoverResponsesRun(w, r, sessionID, localRunID)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) recoverResponsesRun(w http.ResponseWriter, r *http.Request, sessionID, localRunID string) {
	var request struct {
		Confirm     bool     `json:"confirm"`
		ToolCallIDs []string `json:"toolCallIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error(), "invalid_request_error")
		return
	}
	if !request.Confirm || len(request.ToolCallIDs) == 0 || len(request.ToolCallIDs) > 32 {
		writeError(w, http.StatusBadRequest, "confirm=true and one to 32 toolCallIds are required", "invalid_request_error")
		return
	}
	run, err := session.GetResponseRun(s.settings.GetSessionDir(), sessionID, localRunID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "server_error")
		return
	}
	if run == nil {
		writeError(w, http.StatusNotFound, "response run not found", "not_found")
		return
	}
	if !isTerminalResponsesRunState(run.State) {
		writeError(w, http.StatusConflict, "response run must be terminal before tool recovery", "conflict_error")
		return
	}
	guard, err := session.AcquireMutation(s.settings.GetSessionDir(), sessionID)
	if err != nil {
		writeError(w, http.StatusConflict, "response run is still active", "conflict_error")
		return
	}
	released := false
	defer func() {
		if !released {
			guard.Release()
		}
	}()
	parentRun, err := s.responsesBackgroundParentRun(sessionID, run.LocalTurnID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "server_error")
		return
	}
	workDir, _, err := s.findSessionWorkDir(sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "server_error")
		return
	}
	sess, err := s.getOrCreateSession(sessionID, workDir)
	if err != nil || sess == nil {
		if err == nil {
			err = fmt.Errorf("session is unavailable")
		}
		writeError(w, http.StatusInternalServerError, err.Error(), "server_error")
		return
	}
	if parentRun != nil && sess.ActiveRunID() == parentRun.ID {
		writeError(w, http.StatusConflict, "response run is still active", "conflict_error")
		return
	}
	if parentRun == nil {
		writeError(w, http.StatusConflict, "parent session run is unavailable", "conflict_error")
		return
	}
	if !session.IsTerminalSessionRunStatus(parentRun.Status) {
		writeError(w, http.StatusConflict, "parent session run must be terminal before tool recovery", "conflict_error")
		return
	}
	archivedCalls, err := responsesBackgroundFunctionCallsForRun(s.settings.GetSessionDir(), sessionID, run.LocalTurnID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "server_error")
		return
	}
	recoveryRecords, newlyRequested, err := session.RequestToolExecutionRecoveryRecords(s.settings.GetSessionDir(), sessionID, run.LocalTurnID, request.ToolCallIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "server_error")
		return
	}
	if len(recoveryRecords) == 0 {
		writeError(w, http.StatusConflict, "no interrupted tool calls matched the requested recovery", "conflict_error")
		return
	}
	recoveryMessage := responsesRecoveryAgentMessage(*parentRun, *run, recoveryRecords, archivedCalls)
	recoveryKey := responsesRecoverySubmissionKey(sessionID, localRunID, recoveryRecords)
	released = true
	guard.Release()

	// A terminal Run is immutable. Recovery is represented by a new user
	// message and a fresh durable Run through the normal submit path, with the
	// local AgentLoop forced for this turn instead of reattaching the completed
	// remote Responses task.
	payload, err := json.Marshal(submitRunRequest{
		Message: recoveryMessage, Provider: run.Provider, Model: parentRun.Model, Mode: parentRun.Mode,
		WorkDir: parentRun.WorkDir, Transcript: true,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "server_error")
		return
	}
	recoveryRequest := r.Clone(context.WithValue(r.Context(), forceAgentLoopContextKey{}, true))
	urlCopy := *recoveryRequest.URL
	urlCopy.Path = "/api/sessions/" + sessionID + "/runs"
	urlCopy.RawQuery = ""
	recoveryRequest.URL = &urlCopy
	recoveryRequest.RequestURI = urlCopy.RequestURI()
	recoveryRequest.Method = http.MethodPost
	recoveryRequest.Body = io.NopCloser(bytes.NewReader(payload))
	recoveryRequest.ContentLength = int64(len(payload))
	recoveryRequest.Header = r.Header.Clone()
	recoveryRequest.Header.Set("Content-Type", "application/json")
	recoveryRequest.Header.Set("Idempotency-Key", recoveryKey)

	captured := newBufferedHTTPResponse()
	s.HandleSubmitRun(captured, recoveryRequest)
	if captured.statusCode < http.StatusOK || captured.statusCode >= http.StatusMultipleChoices {
		captured.copyTo(w)
		return
	}
	var response map[string]any
	if err := json.Unmarshal(captured.body.Bytes(), &response); err != nil {
		writeError(w, http.StatusInternalServerError, "recovery run response was invalid", "server_error")
		return
	}
	response["run"] = run
	response["reattached"] = false
	response["recoveryRequested"] = len(recoveryRecords)
	response["newlyRequested"] = newlyRequested
	writeJSON(w, captured.statusCode, response)
}

func responsesRecoveryAgentMessage(parent session.SessionRun, remote session.ResponseRun, records []session.ToolExecutionRecord, calls []provider.ToolCallBlock) string {
	callByID := make(map[string]provider.ToolCallBlock, len(calls))
	for _, call := range calls {
		callByID[call.ID] = call
	}
	var b strings.Builder
	b.WriteString("Continue the previous task in a new agent run. The earlier durable run is terminal and must not be resumed.\n\n")
	fmt.Fprintf(&b, "Previous local run: %s (%s)\nPrevious remote response: %s (%s)\n\n", parent.ID, parent.Status, remote.LocalRunID, remote.State)
	b.WriteString("The user explicitly confirmed recovery of these interrupted tool calls:\n")
	for _, record := range records {
		fmt.Fprintf(&b, "- %s (call_id: %s", record.ToolName, record.ProviderCallID)
		if call, ok := callByID[record.ProviderCallID]; ok {
			if args := compactRecoveryArguments(call.Arguments); args != "" {
				fmt.Fprintf(&b, ", arguments: %s", args)
			}
		}
		b.WriteString(")\n")
	}
	b.WriteString("\nInspect the current workspace and any relevant external state before repeating side effects. Retry only the confirmed operations that are still necessary, then continue the original task to a normal terminal result.")
	return b.String()
}

func compactRecoveryArguments(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil {
		return ""
	}
	const maxArguments = 16 << 10
	if compact.Len() <= maxArguments {
		return compact.String()
	}
	return compact.String()[:maxArguments] + "..."
}

func responsesRecoverySubmissionKey(sessionID, localRunID string, records []session.ToolExecutionRecord) string {
	callIDs := make([]string, 0, len(records))
	for _, record := range records {
		callIDs = append(callIDs, record.ProviderCallID)
	}
	sort.Strings(callIDs)
	digest := strings.TrimPrefix(idempotencyKeyFingerprint(sessionID+"\x00"+localRunID+"\x00"+strings.Join(callIDs, "\x00")), "sha256:")
	return "responses-recover-" + digest
}

type bufferedHTTPResponse struct {
	header     http.Header
	body       bytes.Buffer
	statusCode int
}

func newBufferedHTTPResponse() *bufferedHTTPResponse {
	return &bufferedHTTPResponse{header: make(http.Header)}
}

func (w *bufferedHTTPResponse) Header() http.Header { return w.header }

func (w *bufferedHTTPResponse) WriteHeader(statusCode int) {
	if w.statusCode == 0 {
		w.statusCode = statusCode
	}
}

func (w *bufferedHTTPResponse) Write(data []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	return w.body.Write(data)
}

func (w *bufferedHTTPResponse) copyTo(dst http.ResponseWriter) {
	for key, values := range w.header {
		dst.Header()[key] = append([]string(nil), values...)
	}
	status := w.statusCode
	if status == 0 {
		status = http.StatusOK
	}
	dst.WriteHeader(status)
	_, _ = dst.Write(w.body.Bytes())
}

func (s *Server) abandonResponsesRun(w http.ResponseWriter, r *http.Request, manager interface {
	Get(context.Context, string, string) (*session.ResponseRun, error)
	Cancel(context.Context, string, string) error
}, sessionID, localRunID string) {
	run, err := session.GetResponseRun(s.settings.GetSessionDir(), sessionID, localRunID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "server_error")
		return
	}
	if run == nil {
		writeError(w, http.StatusNotFound, "response run not found", "not_found")
		return
	}

	// Serializing with the background coordinator prevents abandoning a tool
	// while a live execution can still write a successful result.
	guard, err := session.AcquireMutation(s.settings.GetSessionDir(), sessionID)
	if err != nil {
		writeError(w, http.StatusConflict, "response run is still active; cancel it before abandoning interrupted tools", "conflict_error")
		return
	}
	defer guard.Release()

	parentRun, err := s.responsesBackgroundParentRun(sessionID, run.LocalTurnID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "server_error")
		return
	}
	workDir, _, err := s.findSessionWorkDir(sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "server_error")
		return
	}
	sess, err := s.getOrCreateSession(sessionID, workDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "server_error")
		return
	}
	if parentRun != nil && sess.ActiveRunID() == parentRun.ID {
		writeError(w, http.StatusConflict, "response run is still active; cancel it before abandoning interrupted tools", "conflict_error")
		return
	}

	if !isTerminalResponsesRunState(run.State) {
		if err := manager.Cancel(r.Context(), sessionID, localRunID); err != nil {
			writeError(w, http.StatusBadGateway, err.Error(), "upstream_error")
			return
		}
	}
	abandoned, err := session.AbandonInterruptedToolExecutionRecords(s.settings.GetSessionDir(), sessionID, run.LocalTurnID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "server_error")
		return
	}
	if parentRun != nil {
		const abandonReason = "abandoned after interrupted tool execution"
		// Durable Run rows are finalized by the canonical ExecutionRuntime.
		// The parent run is already terminal here, so persist the abandon
		// reason through the Runtime annotation boundary instead of relying
		// on the legacy RunManager finalizer, which skips durable-owned rows.
		if _, err := agentruntime.AnnotateDurableRunError(r.Context(), s.settings.GetSessionDir(), parentRun.ID, abandonReason); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "server_error")
			return
		}
		s.FinalizeRun(sess, parentRun.ID, "failed", abandonReason)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"run":                     run,
		"abandonedToolExecutions": abandoned,
		"abandonedAt":             time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func (s *Server) responsesBackgroundParentRun(sessionID, localTurnID string) (*session.SessionRun, error) {
	if sessionID == "" || localTurnID == "" {
		return nil, nil
	}
	runs, err := session.ListSessionRuns(s.settings.GetSessionDir(), sessionID, 500)
	if err != nil {
		return nil, err
	}
	var parent *session.SessionRun
	for i := range runs {
		candidate := runs[i]
		if candidate.Source != "responses_background" || (candidate.ID != localTurnID && !strings.HasPrefix(localTurnID, candidate.ID+":")) {
			continue
		}
		if parent == nil || len(candidate.ID) > len(parent.ID) {
			parent = &candidate
		}
	}
	return parent, nil
}

func (s *Server) authorizeResponseRunSession(sessionID string) error {
	workDir, found, err := s.findSessionWorkDir(sessionID)
	if err != nil {
		return err
	}
	if !found {
		return ErrSessionNotFound
	}
	if workDir == "" || sameWorkDir(workDir, s.cfg.GetWorkDir()) {
		return nil
	}
	if err := s.cfg.ValidateWorkDir(workDir); err != nil {
		return errResponseRunWorkDirNotAllowed
	}
	return nil
}
