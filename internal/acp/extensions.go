package acp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/mcp"
	"github.com/oschina/mothx/internal/session"
)

// This file hosts the Phase 1 additive extension methods and projections of
// the desktop ACP gap plan (docs/proposal/desktop-acp-frontend-gap-proposal.md
// §4.1–§4.8). Every handler is a thin projection of existing Runtime/session
// services: no business logic is duplicated here and no adapter-local storage
// is created. ACP v1 and all pre-existing field semantics stay unchanged.

// acpStructuredRPCError builds an RPC error carrying a stable machine code in
// error.data.code plus a human-readable message, following the established
// attachment_not_found pattern.
func acpStructuredRPCError(rpcCode int, code, message string, extra map[string]any) *mcp.RPCError {
	data := map[string]any{"code": code}
	for key, value := range extra {
		data[key] = value
	}
	return &mcp.RPCError{Code: rpcCode, Message: message, Data: data}
}

// --- §4.1 session run status -------------------------------------------------

// acpRunStatus maps a canonical durable Run status (or RunState) onto the
// projected ACP vocabulary running|completed|failed|cancelled|incomplete.
// Non-terminal durable statuses all project as running; unknown terminal-ish
// values degrade to failed instead of inventing new vocabulary.
func acpRunStatus(status string) string {
	status = strings.TrimSpace(status)
	switch status {
	case "completed":
		return "completed"
	case "incomplete":
		return "incomplete"
	case "failed", "timed_out", "expired":
		return "failed"
	case "cancelled", "canceled":
		return "cancelled"
	}
	if session.IsNonTerminalSessionRunStatus(status) {
		return "running"
	}
	return "failed"
}

// notifyRunStatus projects the additive run_status session event at durable
// run begin/finish. It complements (never replaces) the terminal event and
// the prompt response.
func (s *server) notifyRunStatus(sessionID, runID, status string) {
	_ = s.notifyExtension("_mothx/session_event", map[string]any{
		"sessionId": sessionID,
		"event":     "run_status",
		"runId":     runID,
		"status":    status,
	})
}

// notifyExternalRunStatus re-reads the canonical durable Run after an
// advisory cross-process lease-bus wake-up. UDP data never becomes projected
// state directly: it merely tells this ACP host to refresh SQLite-backed Run
// state, matching the WebUI external-session synchronization contract.
func (s *server) notifyExternalRunStatus(sessionID string) {
	if s == nil || s.settings == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	sessionDir := s.settings.GetSessionDir()
	runs, err := agentruntime.ListLatestDurableRunsBySessions(context.Background(), sessionDir, []string{sessionID})
	if err != nil {
		log.Printf("[acp] refresh external run %q: %v", sessionID, err)
		return
	}
	run, ok := runs[sessionID]
	if !ok || run.ID == "" {
		return
	}
	status := acpRunStatus(run.Status)
	if active, activeErr := agentruntime.GetActiveDurableRun(context.Background(), sessionDir, sessionID); activeErr == nil && active != nil {
		run.ID = active.ID
		status = "running"
	}
	s.notifyRunStatus(sessionID, run.ID, status)
}

// sessionListLastRun assembles the additive listedSession._meta.lastRun
// projection for one page of sessions: the most recent durable Run per
// session plus the cross-process active marker from GetActiveDurableRun.
// Sessions without any Run simply receive no lastRun key. Lookup failures
// degrade to an empty projection instead of failing session/list.
func (s *server) sessionListLastRun(sessionIDs []string) map[string]map[string]any {
	result := make(map[string]map[string]any)
	if s == nil || s.settings == nil || len(sessionIDs) == 0 {
		return result
	}
	sessionDir := s.settings.GetSessionDir()
	latestRuns, err := agentruntime.ListLatestDurableRunsBySessions(context.Background(), sessionDir, sessionIDs)
	if err != nil {
		log.Printf("[acp] list latest runs: %v", err)
		return result
	}
	for _, sessionID := range sessionIDs {
		run, ok := latestRuns[sessionID]
		if !ok || run.ID == "" {
			continue
		}
		active := false
		if activeRun, activeErr := agentruntime.GetActiveDurableRun(context.Background(), sessionDir, sessionID); activeErr == nil && activeRun != nil {
			active = true
		}
		entry := map[string]any{
			"runId":     run.ID,
			"status":    acpRunStatus(run.Status),
			"startedAt": run.StartedAt.UTC().Format(time.RFC3339),
			"active":    active,
		}
		if run.FinishedAt != nil {
			entry["finishedAt"] = run.FinishedAt.UTC().Format(time.RFC3339)
		} else {
			entry["finishedAt"] = nil
		}
		result[sessionID] = entry
	}
	return result
}

// --- §4.2 session metadata (pinned/project) and projects ---------------------

type sessionSetMetaRequest struct {
	SessionID string `json:"sessionId"`
	Pinned    *bool  `json:"pinned,omitempty"`
	// ProjectID accepts three shapes: absent (keep the current assignment),
	// null (clear the assignment), and a string (assign that project).
	ProjectID json.RawMessage `json:"projectId,omitempty"`
	Meta      requestMeta     `json:"_meta,omitempty"`
}

type sessionSetMetaResult struct {
	Pinned    bool    `json:"pinned"`
	ProjectID *string `json:"projectId"`
	UpdatedAt string  `json:"updatedAt"`
}

// acpOptionalProjectID distinguishes an absent projectId (keep current), an
// explicit null (clear), and a string value (assign).
func acpOptionalProjectID(raw json.RawMessage) (present bool, value string, err error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return false, "", nil
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return true, "", nil
	}
	var text string
	if jsonErr := json.Unmarshal(trimmed, &text); jsonErr != nil {
		return false, "", fmt.Errorf("projectId must be a string or null")
	}
	return true, strings.TrimSpace(text), nil
}

// handleSetSessionMeta serves mothx/session/setMeta: a thin projection of
// session.SetSessionMetadata. Absent fields keep their persisted values, an
// explicit null projectId clears the assignment, and the change is broadcast
// as a session_info_update carrying _meta.pinned/_meta.projectId.
func (s *server) handleSetSessionMeta(req rpcRequest) {
	var in sessionSetMetaRequest
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.SessionID) == "" {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "sessionId is required", nil))
		return
	}
	if in.Pinned == nil && len(bytes.TrimSpace(in.ProjectID)) == 0 {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "pinned or projectId is required", nil))
		return
	}
	projectPresent, projectValue, err := acpOptionalProjectID(in.ProjectID)
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", err.Error(), nil))
		return
	}
	if s.settings == nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "session_meta_unavailable", "ACP settings are unavailable", nil))
		return
	}
	sessionID := strings.TrimSpace(in.SessionID)
	sessionDir := s.settings.GetSessionDir()
	mgr, err := session.OpenByIDExact(sessionDir, sessionID)
	if err != nil || mgr.GetHeader() == nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "session_not_found", fmt.Sprintf("session %s is not available", sessionID), nil))
		return
	}
	if _, _, err := s.resolveWorkspace(in.Meta, mgr.GetHeader().Cwd); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "workspace_forbidden", err.Error(), nil))
		return
	}
	existing, err := session.GetSessionMetadata(sessionDir, sessionID)
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "session_meta_unavailable", fmt.Sprintf("load session metadata: %v", err), nil))
		return
	}
	merged := session.SessionMetadata{ProjectID: existing.ProjectID, Pinned: existing.Pinned}
	if in.Pinned != nil {
		merged.Pinned = *in.Pinned
	}
	if projectPresent {
		merged.ProjectID = projectValue
	}
	if err := session.SetSessionMetadata(sessionDir, sessionID, merged); err != nil {
		code := "session_meta_unavailable"
		if strings.Contains(strings.ToLower(err.Error()), "project not found") {
			code = "project_not_found"
		}
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, code, err.Error(), nil))
		return
	}
	stored, err := session.GetSessionMetadata(sessionDir, sessionID)
	if err != nil || stored.UpdatedAt.IsZero() {
		stored = merged
		stored.UpdatedAt = time.Now().UTC()
	}
	s.notifySessionMetaInfo(sessionID, stored)
	var projectID *string
	if stored.ProjectID != "" {
		value := stored.ProjectID
		projectID = &value
	}
	s.writeResponse(req.ID, sessionSetMetaResult{
		Pinned:    stored.Pinned,
		ProjectID: projectID,
		UpdatedAt: stored.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}, nil)
}

// notifySessionMetaInfo projects the standard session_info_update carrying
// the additive _meta.pinned/_meta.projectId keys after a persisted metadata
// change so open clients refresh without a relist.
func (s *server) notifySessionMetaInfo(sessionID string, metadata session.SessionMetadata) {
	title := ""
	if s != nil && s.settings != nil {
		if name, _, err := session.LatestSessionTitle(s.settings.GetSessionDir(), sessionID); err == nil {
			title = name
		}
	}
	meta := map[string]any{"pinned": metadata.Pinned}
	if metadata.ProjectID != "" {
		meta["projectId"] = metadata.ProjectID
	} else {
		meta["projectId"] = nil
	}
	updatedAt := metadata.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	_ = s.notify(sessionID, sessionUpdate{
		SessionUpdate: "session_info_update",
		Title:         title,
		UpdatedAt:     updatedAt.UTC().Format(time.RFC3339Nano),
		Meta:          meta,
	})
}

// sessionListMetadata assembles the additive pinned/projectId keys of
// listedSession._meta for one page of sessions in a single read-only query.
// Lookup failures degrade to default values instead of failing session/list.
func (s *server) sessionListMetadata(sessionIDs []string) map[string]session.SessionMetadata {
	result := make(map[string]session.SessionMetadata)
	if s == nil || s.settings == nil || len(sessionIDs) == 0 {
		return result
	}
	metadata, err := session.ListSessionMetadata(s.settings.GetSessionDir(), sessionIDs)
	if err != nil {
		log.Printf("[acp] list session metadata: %v", err)
		return result
	}
	return metadata
}

type projectRequest struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type projectResult struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	CreatedAt    string `json:"createdAt,omitempty"`
	UpdatedAt    string `json:"updatedAt,omitempty"`
	SessionCount *int   `json:"sessionCount,omitempty"`
}

func acpProjectResult(project session.Project, sessionCount *int) projectResult {
	result := projectResult{ID: project.ID, Name: project.Name, SessionCount: sessionCount}
	if !project.CreatedAt.IsZero() {
		result.CreatedAt = project.CreatedAt.UTC().Format(time.RFC3339)
	}
	if !project.UpdatedAt.IsZero() {
		result.UpdatedAt = project.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return result
}

// handleProjectsList serves mothx/projects/list as a projection of
// session.ListProjects plus the optional per-project session count.
func (s *server) handleProjectsList(req rpcRequest) {
	if s.settings == nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "projects_unavailable", "ACP settings are unavailable", nil))
		return
	}
	sessionDir := s.settings.GetSessionDir()
	projects, err := session.ListProjects(sessionDir)
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "projects_unavailable", fmt.Sprintf("list projects: %v", err), nil))
		return
	}
	counts, err := session.ProjectSessionCounts(sessionDir)
	if err != nil {
		log.Printf("[acp] project session counts: %v", err)
		counts = map[string]int{}
	}
	items := make([]projectResult, 0, len(projects))
	for _, project := range projects {
		count := counts[project.ID]
		items = append(items, acpProjectResult(project, &count))
	}
	s.writeResponse(req.ID, map[string]any{"projects": items}, nil)
}

// handleProjectsCreate serves mothx/projects/create as a projection of
// session.CreateProject.
func (s *server) handleProjectsCreate(req rpcRequest) {
	var in projectRequest
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "name is required", nil))
		return
	}
	if s.settings == nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "projects_unavailable", "ACP settings are unavailable", nil))
		return
	}
	project, err := session.CreateProject(s.settings.GetSessionDir(), in.Name)
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "projects_unavailable", fmt.Sprintf("create project: %v", err), nil))
		return
	}
	s.writeResponse(req.ID, acpProjectResult(project, nil), nil)
}

// handleProjectsRename serves mothx/projects/rename as a projection of
// session.RenameProject.
func (s *server) handleProjectsRename(req rpcRequest) {
	var in projectRequest
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.ID) == "" || strings.TrimSpace(in.Name) == "" {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "id and name are required", nil))
		return
	}
	if s.settings == nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "projects_unavailable", "ACP settings are unavailable", nil))
		return
	}
	project, err := session.RenameProject(s.settings.GetSessionDir(), in.ID, in.Name)
	if err != nil {
		code := "projects_unavailable"
		if strings.Contains(strings.ToLower(err.Error()), "project not found") {
			code = "project_not_found"
		}
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, code, fmt.Sprintf("rename project: %v", err), nil))
		return
	}
	s.writeResponse(req.ID, acpProjectResult(project, nil), nil)
}

// handleProjectsDelete serves mothx/projects/delete as a projection of
// session.DeleteProject. Session assignments of the deleted project are
// cleared by the session layer (declared ON DELETE SET NULL semantics).
func (s *server) handleProjectsDelete(req rpcRequest) {
	var in projectRequest
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.ID) == "" {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "id is required", nil))
		return
	}
	if s.settings == nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "projects_unavailable", "ACP settings are unavailable", nil))
		return
	}
	if err := session.DeleteProject(s.settings.GetSessionDir(), strings.TrimSpace(in.ID)); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "projects_unavailable", fmt.Sprintf("delete project: %v", err), nil))
		return
	}
	s.writeResponse(req.ID, map[string]any{}, nil)
}

// --- §4.3 dynamic workspace extension ----------------------------------------

type workspaceExtendRequest struct {
	AdditionalDirectories []string `json:"additionalDirectories"`
}

// workspaceAdditionalDirectoryLimit caps the negotiated workspace window so a
// buggy or hostile client cannot grow the granted roots without bound.
const workspaceAdditionalDirectoryLimit = 16

// handleWorkspaceExtend serves mothx/workspace/extend: grow-only expansion of
// the workspace window negotiated at initialize. Requested directories must
// be existing absolute paths, are normalized through EvalSymlinks, deduped,
// merged into the window (capped at workspaceAdditionalDirectoryLimit), and
// synchronized into every open session runtime and tool registry. The cwd is
// immutable and the window never shrinks.
func (s *server) handleWorkspaceExtend(req rpcRequest) {
	var in workspaceExtendRequest
	if err := json.Unmarshal(req.Params, &in); err != nil || len(in.AdditionalDirectories) == 0 {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "additionalDirectories is required", nil))
		return
	}
	requested := make([]string, 0, len(in.AdditionalDirectories))
	for _, raw := range in.AdditionalDirectories {
		value := strings.TrimSpace(raw)
		if value == "" || !filepath.IsAbs(value) {
			s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "workspace_directory_invalid", fmt.Sprintf("additional directory must be an absolute path: %q", raw), nil))
			return
		}
		resolved, err := filepath.EvalSymlinks(filepath.Clean(value))
		if err != nil {
			s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "workspace_directory_unavailable", fmt.Sprintf("additional directory %q cannot be resolved", value), nil))
			return
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "workspace_directory_unavailable", fmt.Sprintf("additional directory %q is not an existing directory", value), nil))
			return
		}
		requested = append(requested, resolved)
	}
	s.mu.Lock()
	current := append([]string(nil), s.workspaceAdditionalDirectories...)
	cwd := s.workspaceCwd
	if cwd == "" {
		cwd = s.cwd
	}
	runtimes := make([]*sessionRuntime, 0, len(s.sessions))
	for _, rt := range s.sessions {
		runtimes = append(runtimes, rt)
	}
	s.mu.Unlock()
	// Grow-only merge: the requested roots join the negotiated window.
	merged, err := agentruntime.NormalizeAdditionalDirectories(append(current, requested...))
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "workspace_directory_invalid", err.Error(), nil))
		return
	}
	if len(merged) > workspaceAdditionalDirectoryLimit {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "workspace_limit_exceeded",
			fmt.Sprintf("the workspace window accepts at most %d additional directories", workspaceAdditionalDirectoryLimit),
			map[string]any{"max": workspaceAdditionalDirectoryLimit, "merged": len(merged)}))
		return
	}
	s.mu.Lock()
	s.workspaceAdditionalDirectories = merged
	s.mu.Unlock()
	// Synchronize open sessions so their tools and prompt resource resolution
	// share the grown window. Per-session failures are logged and skipped; the
	// window expansion itself stays effective for subsequent requests.
	for _, rt := range runtimes {
		if rt == nil || rt.runtime == nil {
			continue
		}
		union := append(rt.runtime.AdditionalDirectoriesSnapshot(), merged...)
		if err := s.setSessionAdditionalDirectories(rt, union); err != nil {
			log.Printf("[acp] extend workspace for session %s: %v", rt.id, err)
			continue
		}
		if rt.registry != nil {
			rt.registry.SetAdditionalDirectories(rt.runtime.AdditionalDirectoriesSnapshot())
		}
	}
	_ = s.notifyExtension("_mothx/session_event", map[string]any{
		"event":                 "workspace",
		"cwd":                   cwd,
		"additionalDirectories": merged,
	})
	s.writeResponse(req.ID, map[string]any{"cwd": cwd, "additionalDirectories": merged}, nil)
}

// --- §4.4 decision deadline reminders ----------------------------------------

// Decision deadline reminder marks (§4.4): the first reminder fires at
// min(decisionDeadlineFirstNoticeCap, timeout/2) and the final notice at
// timeout-decisionDeadlineFinalNotice when that mark is later than the first.
// They are variables (not constants) only so tests can exercise both marks
// without minute-scale sleeps; production code must not mutate them.
var (
	decisionDeadlineFirstNoticeCap = time.Minute
	decisionDeadlineFinalNotice    = time.Minute
)

// scheduleDecisionDeadline emits the additive decision_deadline reminders for
// a projected approval/question request: the first at min(60s, timeout/2) and
// the second at timeout-60s when that mark is later than the first. The
// returned stop function must be called when the decision resolves, is
// cancelled, or times out; it terminates the timer goroutine so no reminder
// fires after resolution and no goroutine leaks.
func (s *server) scheduleDecisionDeadline(sessionID, requestID string, kind agentruntime.DecisionKind, timeout time.Duration, deadline time.Time) func() {
	noop := func() {}
	if s == nil || timeout <= 0 {
		return noop
	}
	first := timeout / 2
	if first > decisionDeadlineFirstNoticeCap {
		first = decisionDeadlineFirstNoticeCap
	}
	if first <= 0 {
		return noop
	}
	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done) }) }
	go func() {
		timer := time.NewTimer(first)
		defer timer.Stop()
		select {
		case <-done:
			return
		case <-timer.C:
		}
		s.emitDecisionDeadline(sessionID, requestID, kind, deadline)
		second := timeout - decisionDeadlineFinalNotice
		if second <= first {
			return
		}
		timer.Reset(second - first)
		select {
		case <-done:
			return
		case <-timer.C:
		}
		s.emitDecisionDeadline(sessionID, requestID, kind, deadline)
	}()
	return stop
}

func (s *server) emitDecisionDeadline(sessionID, requestID string, kind agentruntime.DecisionKind, deadline time.Time) {
	remaining := time.Until(deadline)
	if remaining < 0 {
		remaining = 0
	}
	_ = s.notifyExtension("_mothx/session_event", map[string]any{
		"sessionId":   sessionID,
		"event":       "decision_deadline",
		"requestId":   requestID,
		"kind":        string(kind),
		"deadline":    deadline.UTC().Format(time.RFC3339Nano),
		"remainingMs": remaining.Milliseconds(),
	})
}

// --- §4.6 sub-agent lifecycle events -----------------------------------------

// subagentProjection tracks which sub-agent lifecycle events were already
// projected for one session so exactly one started and one terminal event are
// emitted per child agent.
type subagentProjection struct {
	started           bool
	terminal          bool
	memberID          string
	expertID          string
	memberDisplayName string
	memberEmoji       string
	memberRole        string
}

type subagentEventMeta struct {
	memberID          string
	expertID          string
	memberDisplayName string
	memberEmoji       string
	memberRole        string
}

// observeSubagentEvent projects the additive subagent session event for child
// agent activity: "started" on the first observed event of an agent ID and
// exactly one terminal "completed"/"failed" projection. Child text/tool
// events remain projected on the parent session stream as before; this only
// adds lifecycle visibility and never mutates parent run facts.
func (s *server) observeSubagentEvent(sessionID string, ev agentpkg.Event) {
	if s == nil || ev.AgentID == "" {
		return
	}
	agentID := string(ev.AgentID)
	key := sessionID + "\x00" + agentID
	s.mu.Lock()
	if s.subagents == nil {
		s.subagents = make(map[string]*subagentProjection)
	}
	state := s.subagents[key]
	if state == nil {
		state = &subagentProjection{}
		s.subagents[key] = state
	}
	first := !state.started
	state.started = true
	if ev.MemberID != "" {
		state.memberID = ev.MemberID
	}
	if ev.ExpertID != "" {
		state.expertID = ev.ExpertID
	}
	if ev.MemberDisplayName != "" {
		state.memberDisplayName = ev.MemberDisplayName
	}
	if ev.MemberEmoji != "" {
		state.memberEmoji = ev.MemberEmoji
	}
	if ev.MemberRole != "" {
		state.memberRole = ev.MemberRole
	}
	terminalStatus := ""
	if !state.terminal {
		switch ev.Type {
		case agentpkg.EventRunFinished:
			state.terminal = true
			switch ev.Status {
			case agentpkg.TaskFailed, agentpkg.TaskCanceled:
				terminalStatus = "failed"
			default:
				// success and incomplete both terminated without error.
				terminalStatus = "completed"
			}
		case agentpkg.EventError:
			state.terminal = true
			terminalStatus = "failed"
		case agentpkg.EventDone:
			state.terminal = true
			terminalStatus = "completed"
		}
	}
	meta := subagentEventMeta{
		memberID:          state.memberID,
		expertID:          state.expertID,
		memberDisplayName: state.memberDisplayName,
		memberEmoji:       state.memberEmoji,
		memberRole:        state.memberRole,
	}
	s.mu.Unlock()
	if !first && terminalStatus == "" {
		return
	}
	parentID := ""
	if s.agentMgr != nil {
		if parent, ok := s.agentMgr.Parent(ev.AgentID); ok {
			parentID = string(parent)
		}
	}
	if first {
		s.emitSubagentEvent(sessionID, agentID, parentID, "started", meta)
	}
	if terminalStatus != "" {
		s.emitSubagentEvent(sessionID, agentID, parentID, terminalStatus, meta)
	}
}

func (s *server) emitSubagentEvent(sessionID, agentID, parentID, status string, meta subagentEventMeta) {
	params := map[string]any{
		"sessionId": sessionID,
		"event":     "subagent",
		"agentId":   agentID,
		"status":    status,
	}
	if parentID != "" {
		params["parentAgentId"] = parentID
	}
	if meta.memberID != "" {
		params["memberId"] = meta.memberID
	}
	if meta.expertID != "" {
		params["expertId"] = meta.expertID
	}
	if meta.memberDisplayName != "" {
		params["memberDisplayName"] = meta.memberDisplayName
	}
	if meta.memberEmoji != "" {
		params["memberEmoji"] = meta.memberEmoji
	}
	if meta.memberRole != "" {
		params["memberRole"] = meta.memberRole
	}
	_ = s.notifyExtension("_mothx/session_event", params)
}

// clearSubagentProjections forgets the projected sub-agent lifecycle states
// of one session when its runtime shuts down or the session is deleted.
func (s *server) clearSubagentProjections(sessionID string) {
	if s == nil {
		return
	}
	prefix := sessionID + "\x00"
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.subagents {
		if strings.HasPrefix(key, prefix) {
			delete(s.subagents, key)
		}
	}
}

// --- §4.7 tool result images -------------------------------------------------

const (
	// acpToolImageMaxBytes caps one projected tool result image by decoded
	// size and acpToolImageMaxCount caps the images of a single
	// tool_call_update. Oversized or excess images degrade to a textual note
	// instead of being silently dropped.
	acpToolImageMaxBytes = int64(2 << 20)
	acpToolImageMaxCount = 4
)

// acpToolImageContents projects tool result images as additive image content
// blocks of a tool_call_update. The base64 payload is passed through exactly
// as the tool produced it; images beyond the per-update count or the
// per-image size limit degrade to a text note carrying mime type and size.
func acpToolImageContents(images []agentpkg.ToolImage) []toolCallContent {
	if len(images) == 0 {
		return nil
	}
	contents := make([]toolCallContent, 0, len(images))
	var notes []string
	included := 0
	for index, image := range images {
		if strings.TrimSpace(image.Data) == "" {
			continue
		}
		mimeType := strings.TrimSpace(image.MimeType)
		if mimeType == "" {
			mimeType = "image/png"
		}
		decodedBytes := int64(base64.StdEncoding.DecodedLen(len(image.Data)))
		if decodedBytes > acpToolImageMaxBytes {
			notes = append(notes, fmt.Sprintf("image %d not projected: %s (%s) exceeds the %s per-image limit",
				index+1, mimeType, acpByteSize(decodedBytes), acpByteSize(acpToolImageMaxBytes)))
			continue
		}
		if included >= acpToolImageMaxCount {
			notes = append(notes, fmt.Sprintf("image %d not projected: a single tool_call_update carries at most %d images",
				index+1, acpToolImageMaxCount))
			continue
		}
		included++
		contents = append(contents, toolCallContent{Type: "content", Content: &contentBlock{Type: "image", MimeType: mimeType, Data: image.Data}})
	}
	for _, note := range notes {
		contents = append(contents, toolCallContent{Type: "content", Content: &contentBlock{Type: "text", Text: note}})
	}
	return contents
}

func acpByteSize(value int64) string {
	const unit = 1024
	switch {
	case value < unit:
		return fmt.Sprintf("%dB", value)
	case value < unit*unit:
		return fmt.Sprintf("%.1fKB", float64(value)/float64(unit))
	default:
		return fmt.Sprintf("%.1fMB", float64(value)/float64(unit*unit))
	}
}

// --- §4.8 attachment metadata listing ----------------------------------------

type attachmentListRequest struct {
	SessionID string `json:"sessionId"`
	Status    string `json:"status,omitempty"`
}

type attachmentListEntry struct {
	AttachmentID string `json:"attachmentId"`
	Filename     string `json:"filename"`
	Kind         string `json:"kind"`
	MediaType    string `json:"mediaType"`
	Size         int64  `json:"size"`
	Status       string `json:"status"`
	RunID        string `json:"runId,omitempty"`
	CreatedAt    string `json:"createdAt"`
}

// handleAttachmentList serves the mothx/attachment/list extension: a
// metadata-only listing of the Runtime-owned attachment rows of one session,
// optionally filtered by protocol status ("generated" for run artifacts,
// "input" for materialized prompt attachments). Rows of other sessions are
// never returned and content bytes stay behind mothx/attachment/fetch.
func (s *server) handleAttachmentList(req rpcRequest) {
	var in attachmentListRequest
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.SessionID) == "" {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "sessionId is required", nil))
		return
	}
	status := strings.TrimSpace(in.Status)
	filter := ""
	switch status {
	case "":
	case "generated":
		filter = "generated"
	case "input":
		// Input attachments persist with the canonical "accepted" status.
		filter = "accepted"
	default:
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "attachment_list_invalid_status",
			fmt.Sprintf("status %q is not supported; use generated or input", status), nil))
		return
	}
	if s.settings == nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "attachment_unavailable", "ACP settings are unavailable", nil))
		return
	}
	records, err := session.ListSessionAttachments(context.Background(), s.settings.GetSessionDir(), strings.TrimSpace(in.SessionID), filter)
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "attachment_unavailable", fmt.Sprintf("list attachments: %v", err), nil))
		return
	}
	items := make([]attachmentListEntry, 0, len(records))
	for _, record := range records {
		entry := attachmentListEntry{
			AttachmentID: record.ID,
			Filename:     record.Filename,
			Kind:         record.Kind,
			MediaType:    record.MediaType,
			Size:         record.Bytes,
			Status:       record.Status,
			RunID:        record.RunID,
		}
		if !record.CreatedAt.IsZero() {
			entry.CreatedAt = record.CreatedAt.UTC().Format(time.RFC3339Nano)
		}
		items = append(items, entry)
	}
	s.writeResponse(req.ID, map[string]any{"attachments": items}, nil)
}
