package openaiapi

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

// recoveredApprovalDecision returns a durable decision made before a process
// stopped. Only a resolved decision is reusable; pending and cancelled
// requests deliberately cause the recovered agent to ask again.
func (s *Server) recoveredApprovalDecision(sessionID, runID, toolCallID, toolName string, args map[string]any) (bool, bool) {
	if s == nil || s.settings == nil || sessionID == "" || runID == "" || toolName == "" {
		return false, false
	}
	events, err := session.ListSessionRunEvents(s.settings.GetSessionDir(), sessionID)
	if err != nil {
		provider.DebugLogf("recover approval for session %q run %q: list events: %v", sessionID, runID, err)
		return false, false
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return false, false
	}
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.RunID != runID || event.EventType != "approval_resolved" {
			continue
		}
		var data struct {
			Approval   SessionApprovalRequest    `json:"approval"`
			Resolution SessionApprovalResolution `json:"resolution"`
		}
		if json.Unmarshal(event.Data, &data) != nil || data.Resolution.Status != "resolved" {
			continue
		}
		if !matchesRecoveredApproval(data.Approval, toolCallID, toolName, argsJSON) {
			continue
		}
		return data.Resolution.Action != "deny_once", true
	}
	return false, false
}

func matchesRecoveredApproval(request SessionApprovalRequest, toolCallID, toolName string, argsJSON []byte) bool {
	if request.ToolCallID != "" && request.ToolCallID != toolCallID {
		return false
	}
	storedName, _ := request.Tool["name"].(string)
	if storedName != toolName {
		return false
	}
	storedArgs, ok := request.Tool["args"]
	if !ok {
		return len(argsJSON) == 0 || string(argsJSON) == "null" || string(argsJSON) == "{}"
	}
	storedJSON, err := json.Marshal(storedArgs)
	return err == nil && string(storedJSON) == string(argsJSON)
}

func approvalCommand(args map[string]any) string {
	for _, key := range []string{"command", "cmd"} {
		if value, ok := args[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func approvalPath(args map[string]any) string {
	if value, ok := args["path"].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func suggestedApprovalCommandPrefix(command string) string {
	command = strings.TrimLeft(command, " \t\r\n")
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	count := len(fields)
	if count > 2 {
		count = 2
	}
	prefix := strings.Join(fields[:count], " ")
	if index := strings.Index(command, prefix); index >= 0 {
		prefix = command[index : index+len(prefix)]
	}
	if len(command) > len(prefix) && command[len(prefix)] == ' ' {
		return prefix + " "
	}
	return prefix
}

func approvalToolLabel(name string) string {
	// ASCII-only title case for internal tool names; avoids the deprecated
	// strings.Title which has incorrect Unicode word-boundary semantics.
	words := strings.Split(strings.ReplaceAll(name, "_", " "), " ")
	for i, w := range words {
		words[i] = approvalCapitalize(w)
	}
	return strings.Join(words, " ")
}

func approvalCapitalize(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] -= 'a' - 'A'
	}
	return string(r)
}

// questionRequestFromEvent converts a core question event into the WebUI shape.
func questionRequestFromEvent(sess *APISession, runID string, ev agent.Event) SessionQuestionRequest {
	request := SessionQuestionRequest{
		QuestionID: ev.QuestionID,
		SessionID:  sess.ID,
		RunID:      runID,
		Question:   ev.QuestionText,
		Options:    append([]string(nil), ev.QuestionOptions...),
		Context:    ev.QuestionContext,
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
	}
	return request
}

// ensureSessionDecisionLocked keeps the protocol pending map and the shared
// decision service aligned for restored/legacy sessions. The caller must hold
// sess.approvalMu; resolving through the service then supplies first-response-
// wins even when the adapter map was populated without a DecisionService.
func ensureSessionDecisionLocked(sess *APISession, request agentruntime.DecisionRequest) (*agentruntime.DecisionService, error) {
	if sess == nil {
		return nil, fmt.Errorf("session is nil")
	}
	if sess.Decisions == nil {
		sess.Decisions = &agentruntime.DecisionService{}
		if sess.Runtime != nil {
			sess.Runtime.SetDecisions(sess.Decisions)
		}
	}
	for _, pending := range sess.Decisions.Pending() {
		if pending.ID != request.ID {
			continue
		}
		if pending.RunID != request.RunID || pending.SessionID != request.SessionID || pending.Kind != request.Kind {
			return nil, fmt.Errorf("decision %q does not match the pending session request", request.ID)
		}
		return sess.Decisions, nil
	}
	if err := sess.Decisions.Register(request); err != nil {
		return nil, err
	}
	return sess.Decisions, nil
}

func (s *Server) registerSessionQuestion(sess *APISession, a *agent.Agent, runID string, ev agent.Event) *SessionQuestionRequest {
	if sess == nil || a == nil || ev.QuestionID == "" || runID == "" {
		return nil
	}
	sess.approvalMu.Lock()
	if sess.activeRunID != runID || sess.activeRunStatus != "running" {
		sess.approvalMu.Unlock()
		a.HandleQuestionResponse(ev.QuestionID, "")
		return nil
	}
	request := questionRequestFromEvent(sess, runID, ev)
	if sess.pendingQuestions == nil {
		sess.pendingQuestions = make(map[string]pendingSessionQuestion)
	}
	sess.pendingQuestions[request.QuestionID] = pendingSessionQuestion{Request: request}
	if sess.Decisions == nil {
		sess.Decisions = &agentruntime.DecisionService{}
		if sess.Runtime != nil {
			sess.Runtime.SetDecisions(sess.Decisions)
		}
	}
	if err := sess.Decisions.Register(agentruntime.DecisionRequest{ID: request.QuestionID, RunID: runID, SessionID: sess.ID, Kind: agentruntime.DecisionQuestion}); err != nil {
		provider.DebugLogf("register question decision %q: %v", request.QuestionID, err)
	}
	_ = sess.Decisions.Bind(request.QuestionID, func(answer string) error {
		if a != nil {
			a.HandleQuestionResponse(request.QuestionID, answer)
		}
		return nil
	})
	sess.approvalMu.Unlock()
	if execution := sess.executionRuntime(); execution != nil {
		_ = execution.WaitForQuestion(runID)
	}
	s.publishSessionStreamEvent(sess.ID, "question_request", request)
	_ = s.recordSessionQuestionRequest(sess, request)
	s.getEventBroker().PublishRawJSON(sess.ID, runID, "question_request", request)
	// Same rationale as approval: publish the runtime projection so a dropped
	// event stream cannot hide a run waiting on a question.
	if s != nil {
		s.publishSessionRuntime(sess)
	}
	return &request
}

func (s *Server) resolveSessionQuestion(sessionID, questionID string, response SessionQuestionResponse) (*SessionQuestionResolution, error) {
	if sessionID == "" || questionID == "" {
		return nil, ErrSessionNotFound
	}
	sess, err := s.pool.getExact(sessionID)
	if err != nil || sess == nil {
		return nil, ErrSessionNotFound
	}
	sess.approvalMu.Lock()
	pending, ok := sess.pendingQuestions[questionID]
	if !ok || pending.Request.RunID != sess.activeRunID || sess.activeRunStatus != "running" {
		sess.approvalMu.Unlock()
		return nil, fmt.Errorf("question %q is no longer pending", questionID)
	}
	resolution := &SessionQuestionResolution{QuestionID: questionID, SessionID: sessionID, RunID: pending.Request.RunID, Answer: response.Answer, Status: "resolved"}
	decisions, err := ensureSessionDecisionLocked(sess, agentruntime.DecisionRequest{ID: questionID, RunID: pending.Request.RunID, SessionID: sess.ID, Kind: agentruntime.DecisionQuestion})
	sess.approvalMu.Unlock()
	if err != nil {
		return nil, err
	}
	if decisions != nil {
		if _, err := decisions.ResolveWith(agentruntime.DecisionResolution{ID: questionID, Kind: agentruntime.DecisionQuestion, Status: "resolved", Value: response.Answer}, func(_ agentruntime.DecisionRequest) error {
			return s.recordSessionQuestionResolution(sess, pending.Request, resolution)
		}); err != nil {
			return nil, err
		}
	} else if err := s.recordSessionQuestionResolution(sess, pending.Request, resolution); err != nil {
		return nil, err
	}
	sess.approvalMu.Lock()
	delete(sess.pendingQuestions, questionID)
	sess.approvalMu.Unlock()
	if execution := sess.executionRuntime(); execution != nil {
		_ = execution.Resume(pending.Request.RunID)
	}
	s.publishSessionStreamEvent(sessionID, "question_resolved", resolution)
	s.getEventBroker().PublishRawJSON(sessionID, pending.Request.RunID, "question_resolved", resolution)
	return resolution, nil
}

func (s *Server) recordSessionQuestionRequest(sess *APISession, request SessionQuestionRequest) error {
	decision := agentruntime.DecisionRequest{ID: request.QuestionID, SessionID: request.SessionID, RunID: request.RunID, Kind: agentruntime.DecisionQuestion}
	if err := s.recordDecisionEventWithDeadline(sess, decision, nil, "question_requested", "pending", "question", "", request, s.decisionDeadline()); err != nil {
		return err
	}
	return nil
}

func (s *Server) recordSessionQuestionResolution(sess *APISession, request SessionQuestionRequest, resolution *SessionQuestionResolution) error {
	if sess == nil || resolution == nil {
		return nil
	}
	decision := agentruntime.DecisionRequest{ID: request.QuestionID, SessionID: request.SessionID, RunID: request.RunID, Kind: agentruntime.DecisionQuestion}
	result := agentruntime.DecisionResolution{ID: request.QuestionID, Kind: agentruntime.DecisionQuestion, Status: resolution.Status, Value: resolution.Answer}
	return s.recordDecisionEvent(sess, decision, &result, "question_resolved", resolution.Status, "question", "", map[string]any{
		"question":   request,
		"resolution": resolution,
	})
}

func (s *Server) ResolveSessionQuestion(sessionID, questionID string, response SessionQuestionResponse) (*SessionQuestionResolution, error) {
	return s.resolveSessionQuestion(sessionID, questionID, response)
}

func (s *Server) approvalRequestFromEvent(sess *APISession, runID string, ev agent.Event) SessionApprovalRequest {
	toolName := ev.ApprovalTool
	args := ev.ApprovalArgs
	summary := "Run " + toolName
	risk := "medium"
	reason := "requires confirmation in agent mode"
	details := map[string]any{}
	switch toolName {
	case "bash":
		command := approvalCommand(args)
		summary = "Run bash: " + command
		risk = "high"
		details["command"] = command
		details["workDir"] = sess.WorkDir
	case "write", "edit", "delete":
		path := approvalPath(args)
		summary = approvalCapitalize(toolName) + " " + path
		risk = "high"
		details["path"] = path
		details["operation"] = toolName
	case "git_access":
		summary = "Allow git metadata access"
		risk = "low"
	}
	actions := []string{"approve_once", "deny_once"}
	if toolName == "bash" && approvalCommand(args) != "" {
		actions = append(actions, "remember_command", "remember_prefix")
	}
	if (toolName == "write" || toolName == "edit") && approvalPath(args) != "" {
		actions = append(actions, "allow_edit_path")
	}
	mode := sess.Mode
	if resolved, err := s.resolveSessionMode(sess, ""); err == nil {
		mode = resolved
	}
	return SessionApprovalRequest{
		ApprovalID: ev.ApprovalID, ToolCallID: ev.ToolCallID, SessionID: sess.ID, RunID: runID, Timestamp: time.Now().UTC().Format(time.RFC3339Nano), AgentID: string(ev.AgentID), Mode: mode,
		Risk: risk, Summary: summary, Reason: reason,
		Tool:    map[string]any{"name": toolName, "label": approvalToolLabel(toolName), "args": args, "details": details},
		Context: map[string]any{"workDir": sess.WorkDir}, Actions: actions,
	}
}

func (s *Server) registerSessionApproval(sess *APISession, a *agent.Agent, ev agent.Event) *SessionApprovalRequest {
	if sess == nil || a == nil || ev.ApprovalID == "" {
		return nil
	}

	// Approval registration and run cancellation share approvalMu. Once a run is
	// cancelling, a late approval event is denied and recorded as cancelled rather
	// than exposed as a new WebUI decision.
	sess.approvalMu.Lock()
	runID := sess.activeRunID
	runStatus := sess.activeRunStatus
	if runID == "" || runStatus != "running" || (sess.activeRunAgent != nil && sess.activeRunAgent != a) {
		sess.approvalMu.Unlock()
		a.HandleApprovalResponse(ev.ApprovalID, false)
		if runID != "" {
			request := s.approvalRequestFromEvent(sess, runID, ev)
			resolution := &SessionApprovalResolution{ApprovalID: ev.ApprovalID, SessionID: sess.ID, Action: "deny_once", Status: "cancelled", Message: "run ended before approval was resolved"}
			if err := s.recordSessionApprovalRequest(sess, request); err != nil {
				provider.DebugLogf("record cancelled approval request %q for session %q: %v", request.ApprovalID, sess.ID, err)
			}
			if err := s.recordSessionApprovalResolution(sess, request, resolution); err != nil {
				provider.DebugLogf("record cancelled approval resolution %q for session %q: %v", request.ApprovalID, sess.ID, err)
			}
			s.publishSessionStreamEvent(sess.ID, "approval_resolved", resolution)
			s.getEventBroker().PublishApprovalEvent(sess.ID, runID, "approval_resolved", resolution)
		}
		return nil
	}
	request := s.approvalRequestFromEvent(sess, runID, ev)
	// Do not persist while holding approvalMu. The run-event persistence path
	// checks durable-run bookkeeping through the same mutex, so doing this under
	// the lock self-deadlocks the Agent exactly when it emits an approval request.
	sess.approvalMu.Unlock()
	if err := s.recordSessionApprovalRequest(sess, request); err != nil {
		a.HandleApprovalResponse(request.ApprovalID, false)
		return nil
	}

	// A cancellation can win while the request is being persisted. Re-check the
	// admission state before exposing the request to the WebUI; if the run ended,
	// append the cancellation resolution after the request and unblock the Agent.
	sess.approvalMu.Lock()
	runStillActive := sess.activeRunID == runID && sess.activeRunStatus == "running" && (sess.activeRunAgent == nil || sess.activeRunAgent == a)
	if !runStillActive {
		sess.approvalMu.Unlock()
		a.HandleApprovalResponse(request.ApprovalID, false)
		resolution := &SessionApprovalResolution{ApprovalID: request.ApprovalID, SessionID: sess.ID, Action: "deny_once", Status: "cancelled", Message: "run ended before approval was resolved"}
		if err := s.recordSessionApprovalResolution(sess, request, resolution); err != nil {
			provider.DebugLogf("record cancelled approval resolution %q for session %q: %v", request.ApprovalID, sess.ID, err)
		}
		s.publishSessionStreamEvent(sess.ID, "approval_resolved", resolution)
		s.getEventBroker().PublishApprovalEvent(sess.ID, runID, "approval_resolved", resolution)
		return nil
	}
	if sess.pendingApprovals == nil {
		sess.pendingApprovals = make(map[string]pendingSessionApproval)
	}
	sess.pendingApprovals[request.ApprovalID] = pendingSessionApproval{Request: request}
	if sess.Decisions == nil {
		sess.Decisions = &agentruntime.DecisionService{}
		if sess.Runtime != nil {
			sess.Runtime.SetDecisions(sess.Decisions)
		}
	}
	if err := sess.Decisions.Register(agentruntime.DecisionRequest{ID: request.ApprovalID, RunID: runID, SessionID: sess.ID, Kind: agentruntime.DecisionApproval}); err != nil {
		provider.DebugLogf("register approval decision %q: %v", request.ApprovalID, err)
	}
	_ = sess.Decisions.Bind(request.ApprovalID, func(action string) error {
		if a != nil {
			a.HandleApprovalResponse(request.ApprovalID, action != "deny_once")
		}
		return nil
	})
	sess.approvalMu.Unlock()

	s.publishSessionStreamEvent(sess.ID, "approval_request", request)
	s.getEventBroker().PublishApprovalEvent(sess.ID, runID, "approval_request", request)
	// Publish a runtime snapshot alongside the approval event so clients that
	// missed the approval frame (e.g. a dropped WebSocket that replays only
	// transcript/run/capability streams) still learn about the blocking run
	// and its pending decision through the runtime projection.
	if s != nil {
		s.publishSessionRuntime(sess)
	}
	return &request
}

func (s *Server) resolveSessionApproval(id, approvalID string, response SessionApprovalResponse) (*SessionApprovalResolution, error) {
	if id == "" || approvalID == "" {
		return nil, ErrSessionNotFound
	}
	if response.Action != "approve_once" && response.Action != "deny_once" && response.Action != "remember_command" && response.Action != "remember_prefix" && response.Action != "allow_edit_path" {
		return nil, fmt.Errorf("%w: unsupported approval action", ErrInvalidCapability)
	}
	sess, err := s.pool.getExact(id)
	if err != nil || sess == nil {
		return nil, ErrSessionNotFound
	}
	sess.approvalMu.Lock()
	pending, ok := sess.pendingApprovals[approvalID]
	if !ok || pending.Request.RunID != sess.activeRunID || sess.activeRunStatus != "running" {
		sess.approvalMu.Unlock()
		return nil, fmt.Errorf("approval %q is no longer pending", approvalID)
	}
	approved := response.Action != "deny_once"
	if approved {
		if err := s.rememberApprovalRule(pending.Request, response.Action); err != nil {
			sess.approvalMu.Unlock()
			return nil, err
		}
	}
	resolution := &SessionApprovalResolution{ApprovalID: approvalID, SessionID: id, Action: response.Action, Status: "resolved"}
	if approved {
		resolution.Message = "approval accepted"
	} else {
		resolution.Message = "approval denied"
	}
	decisions, err := ensureSessionDecisionLocked(sess, agentruntime.DecisionRequest{ID: approvalID, RunID: pending.Request.RunID, SessionID: sess.ID, Kind: agentruntime.DecisionApproval})
	sess.approvalMu.Unlock()
	if err != nil {
		return nil, err
	}
	if decisions != nil {
		if _, err := decisions.ResolveWith(agentruntime.DecisionResolution{ID: approvalID, Kind: agentruntime.DecisionApproval, Status: "resolved", Value: response.Action}, func(_ agentruntime.DecisionRequest) error {
			return s.recordSessionApprovalResolution(sess, pending.Request, resolution)
		}); err != nil {
			return nil, err
		}
	} else if err := s.recordSessionApprovalResolution(sess, pending.Request, resolution); err != nil {
		return nil, err
	}
	sess.approvalMu.Lock()
	delete(sess.pendingApprovals, approvalID)
	sess.approvalMu.Unlock()
	if execution := sess.executionRuntime(); execution != nil {
		_ = execution.Resume(pending.Request.RunID)
	}
	s.publishSessionStreamEvent(id, "approval_response", resolution)
	s.publishSessionStreamEvent(id, "approval_resolved", resolution)
	s.getEventBroker().PublishApprovalEvent(id, sess.activeRunID, "approval_response", resolution)
	s.getEventBroker().PublishApprovalEvent(id, sess.activeRunID, "approval_resolved", resolution)
	return resolution, nil
}

func (s *Server) rememberApprovalRule(request SessionApprovalRequest, action string) error {
	if action == "approve_once" {
		return nil
	}
	args, _ := request.Tool["args"].(map[string]any)
	allow := s.getAllow()
	var changed bool
	switch action {
	case "remember_command":
		changed = allow.AddBashCommand(approvalCommand(args))
	case "remember_prefix":
		changed = allow.AddBashPrefix(suggestedApprovalCommandPrefix(approvalCommand(args)))
	case "allow_edit_path":
		changed = allow.AddEditPath(filepath.Clean(approvalPath(args)))
	}
	if !changed {
		return nil
	}
	if s.saveProjectAllow != nil {
		if err := s.saveProjectAllow(allow); err != nil {
			s.rollbackApprovalRule(allow, args, action)
			return fmt.Errorf("save project allow rule: %w", err)
		}
		return nil
	}
	if err := allow.SaveProject(); err != nil {
		s.rollbackApprovalRule(allow, args, action)
		return fmt.Errorf("save project allow rule: %w", err)
	}
	return nil
}

func (s *Server) rollbackApprovalRule(allow *config.AllowConfig, args map[string]any, action string) {
	switch action {
	case "remember_command":
		allow.RemoveBashCommand(approvalCommand(args))
	case "remember_prefix":
		allow.RemoveBashPrefix(suggestedApprovalCommandPrefix(approvalCommand(args)))
	case "allow_edit_path":
		allow.RemoveEditPath(filepath.Clean(approvalPath(args)))
	}
}

func (s *Server) clearSessionApprovals(sess *APISession, status, message string) {
	if sess == nil {
		return
	}
	sess.approvalMu.Lock()
	runID := sess.activeRunID
	sess.approvalMu.Unlock()
	s.clearSessionApprovalsForRun(sess, runID, status, message)
}

func (s *Server) clearSessionApprovalsForRun(sess *APISession, runID, status, message string) {
	if sess == nil || runID == "" {
		return
	}
	sess.approvalMu.Lock()
	pending := make(map[string]pendingSessionApproval)
	questions := make(map[string]pendingSessionQuestion)
	for approvalID, item := range sess.pendingApprovals {
		if item.Request.RunID != runID {
			continue
		}
		pending[approvalID] = item
		delete(sess.pendingApprovals, approvalID)
	}
	for questionID, item := range sess.pendingQuestions {
		if item.Request.RunID != runID {
			continue
		}
		questions[questionID] = item
		delete(sess.pendingQuestions, questionID)
	}
	if sess.Decisions != nil {
		sess.Decisions.ClearRunWithValue(runID, "")
	}
	sess.approvalMu.Unlock()

	for approvalID, item := range pending {
		resolution := &SessionApprovalResolution{ApprovalID: approvalID, SessionID: sess.ID, Action: "deny_once", Status: status, Message: message}
		if err := s.recordSessionApprovalResolution(sess, item.Request, resolution); err != nil {
			provider.DebugLogf("record cleared approval resolution %q for session %q: %v", approvalID, sess.ID, err)
		}
		s.publishSessionStreamEvent(sess.ID, "approval_resolved", resolution)
		s.getEventBroker().PublishApprovalEvent(sess.ID, runID, "approval_resolved", resolution)
	}
	for questionID, item := range questions {
		resolution := &SessionQuestionResolution{QuestionID: questionID, SessionID: sess.ID, RunID: runID, Status: status, Message: message}
		s.publishSessionStreamEvent(sess.ID, "question_resolved", resolution)
		_ = s.recordSessionQuestionResolution(sess, item.Request, resolution)
		s.getEventBroker().PublishRawJSON(sess.ID, runID, "question_resolved", resolution)
	}
}

func (s *Server) recordSessionApprovalRequest(sess *APISession, request SessionApprovalRequest) error {
	decision := agentruntime.DecisionRequest{ID: request.ApprovalID, SessionID: request.SessionID, RunID: request.RunID, Kind: agentruntime.DecisionApproval}
	return s.recordDecisionEventWithDeadline(sess, decision, nil, "approval_requested", "pending", "approval", request.Mode, request, s.decisionDeadline())
}

func (s *Server) recordSessionApprovalResolution(sess *APISession, request SessionApprovalRequest, resolution *SessionApprovalResolution) error {
	if sess == nil || resolution == nil {
		return nil
	}
	decision := agentruntime.DecisionRequest{ID: request.ApprovalID, SessionID: request.SessionID, RunID: request.RunID, Kind: agentruntime.DecisionApproval}
	result := agentruntime.DecisionResolution{ID: request.ApprovalID, Kind: agentruntime.DecisionApproval, Status: resolution.Status, Value: resolution.Action}
	return s.recordDecisionEvent(sess, decision, &result, "approval_resolved", resolution.Status, "approval", request.Mode, map[string]any{
		"approval":   request,
		"resolution": resolution,
	})
}

// ResolveSessionApproval applies the first accepted WebUI decision and resumes its agent.
func (s *Server) ResolveSessionApproval(sessionID, approvalID string, response SessionApprovalResponse) (*SessionApprovalResolution, error) {
	return s.resolveSessionApproval(sessionID, approvalID, response)
}
