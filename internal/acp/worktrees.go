package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/mcp"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/worktree"
)

// Worktree management is an additive ACP projection of the shared
// agentruntime.WorktreeManager. It owns protocol mapping only: the manager owns
// git mechanics, the registry, authorization and canonical events.

type worktreeListRequest struct {
	RepositoryRoot string `json:"repositoryRoot,omitempty"`
	ProjectID      string `json:"projectId,omitempty"`
}

type worktreeCreateRequest struct {
	BaseCwd      string `json:"baseCwd,omitempty"`
	Name         string `json:"name,omitempty"`
	Detached     bool   `json:"detached,omitempty"`
	StartCommand string `json:"startCommand,omitempty"`
	ProjectID    string `json:"projectId,omitempty"`
}

type worktreeTargetRequest struct {
	ID        string `json:"id,omitempty"`
	Directory string `json:"directory,omitempty"`
	Force     bool   `json:"force,omitempty"`
}

// handleWorktreeList serves mothx/worktree/list for a repository inside the
// negotiated workspace window.
func (s *server) handleWorktreeList(req rpcRequest) {
	var in worktreeListRequest
	if len(req.Params) > 0 && json.Unmarshal(req.Params, &in) != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "invalid params", nil))
		return
	}
	if s.worktrees == nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "worktrees_unavailable", "worktrees are unavailable", nil))
		return
	}
	repoRoot := strings.TrimSpace(in.RepositoryRoot)
	if repoRoot == "" {
		repoRoot = s.manageWorkDir()
	}
	if _, _, err := s.resolveWorkspace(requestMeta{}, repoRoot); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "unauthorized", err.Error(), nil))
		return
	}
	worktrees, err := s.worktrees.List(context.Background(), repoRoot, in.ProjectID)
	if err != nil {
		s.writeResponse(req.ID, nil, acpWorktreeRPCError(err))
		return
	}
	s.writeResponse(req.ID, map[string]any{"worktrees": worktrees}, nil)
}

// handleWorktreeCreate serves mothx/worktree/create. The base directory must be
// inside the negotiated window; the created worktree directory is authorized by
// the manager synchronously, so a session can be opened in it immediately.
func (s *server) handleWorktreeCreate(req rpcRequest) {
	var in worktreeCreateRequest
	if len(req.Params) > 0 && json.Unmarshal(req.Params, &in) != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "invalid params", nil))
		return
	}
	if s.worktrees == nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "worktrees_unavailable", "worktrees are unavailable", nil))
		return
	}
	if s.settings != nil && !s.settings.IsWorktreeEnabled() {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "worktrees_disabled", "worktree creation is disabled", nil))
		return
	}
	baseCwd := strings.TrimSpace(in.BaseCwd)
	if baseCwd == "" {
		baseCwd = s.manageWorkDir()
	}
	if _, _, err := s.resolveWorkspace(requestMeta{}, baseCwd); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "unauthorized", err.Error(), nil))
		return
	}
	created, err := s.worktrees.Create(context.Background(), agentruntime.CreateWorktreeRequest{
		BaseCwd:      baseCwd,
		Name:         in.Name,
		Detached:     in.Detached,
		StartCommand: in.StartCommand,
		ProjectID:    in.ProjectID,
	})
	if err != nil {
		s.writeResponse(req.ID, nil, acpWorktreeRPCError(err))
		return
	}
	s.writeResponse(req.ID, map[string]any{"worktree": created}, nil)
}

// handleWorktreeRemove serves mothx/worktree/remove. A worktree with an active
// run is rejected unless force is set, in which case the run is cancelled first.
func (s *server) handleWorktreeRemove(req rpcRequest) {
	var in worktreeTargetRequest
	if err := json.Unmarshal(req.Params, &in); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "invalid params", nil))
		return
	}
	if s.worktrees == nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "worktrees_unavailable", "worktrees are unavailable", nil))
		return
	}
	dir, rpcErr := s.authorizeWorktreeTarget(in)
	if rpcErr != nil {
		s.writeResponse(req.ID, nil, rpcErr)
		return
	}
	if !in.Force {
		if sessionID := s.activeWorktreeSession(dir); sessionID != "" {
			s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "worktree_in_use",
				fmt.Sprintf("worktree is in use by running session %s", sessionID), nil))
			return
		}
	} else {
		s.cancelWorktreeSessions(dir)
	}
	if err := s.worktrees.Remove(context.Background(), strings.TrimSpace(in.ID), strings.TrimSpace(in.Directory)); err != nil {
		s.writeResponse(req.ID, nil, acpWorktreeRPCError(err))
		return
	}
	s.writeResponse(req.ID, map[string]any{"removed": true}, nil)
}

// handleWorktreeReset serves mothx/worktree/reset.
func (s *server) handleWorktreeReset(req rpcRequest) {
	var in worktreeTargetRequest
	if err := json.Unmarshal(req.Params, &in); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "invalid params", nil))
		return
	}
	if s.worktrees == nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "worktrees_unavailable", "worktrees are unavailable", nil))
		return
	}
	if _, rpcErr := s.authorizeWorktreeTarget(in); rpcErr != nil {
		s.writeResponse(req.ID, nil, rpcErr)
		return
	}
	if err := s.worktrees.Reset(context.Background(), strings.TrimSpace(in.ID), strings.TrimSpace(in.Directory)); err != nil {
		s.writeResponse(req.ID, nil, acpWorktreeRPCError(err))
		return
	}
	s.writeResponse(req.ID, map[string]any{"reset": true}, nil)
}

// authorizeWorktreeTarget resolves the request target to a directory and checks
// it is inside the negotiated workspace window or a Runtime-granted worktree.
func (s *server) authorizeWorktreeTarget(in worktreeTargetRequest) (string, *mcp.RPCError) {
	dir := strings.TrimSpace(in.Directory)
	if dir == "" {
		id := strings.TrimSpace(in.ID)
		if id == "" {
			return "", acpStructuredRPCError(-32602, "invalid_params", "id or directory is required", nil)
		}
		wt, err := s.worktrees.Get(context.Background(), id)
		if err != nil {
			return "", acpWorktreeRPCError(err)
		}
		if wt.ID == "" {
			return "", acpStructuredRPCError(-32000, "worktree_not_found", fmt.Sprintf("worktree %q not found", id), nil)
		}
		dir = wt.Directory
	}
	if _, _, err := s.resolveWorkspace(requestMeta{}, dir); err != nil {
		return "", acpStructuredRPCError(-32000, "unauthorized", err.Error(), nil)
	}
	return dir, nil
}

// activeWorktreeSession returns the id of an open session with a running run
// whose working directory is dir, or "".
func (s *server) activeWorktreeSession(dir string) string {
	target := filepath.Clean(dir)
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, rt := range s.sessions {
		if rt == nil || rt.runtime == nil || rt.execution == nil {
			continue
		}
		if filepath.Clean(rt.runtime.WorkDir) != target {
			continue
		}
		if _, active := rt.execution.Active(); active {
			return id
		}
	}
	return ""
}

// cancelWorktreeSessions cancels the runs of every open session whose working
// directory is dir. It is used by force removal.
func (s *server) cancelWorktreeSessions(dir string) {
	target := filepath.Clean(dir)
	s.mu.Lock()
	runtimes := make([]*sessionRuntime, 0, len(s.sessions))
	for _, rt := range s.sessions {
		if rt != nil && rt.runtime != nil && filepath.Clean(rt.runtime.WorkDir) == target {
			runtimes = append(runtimes, rt)
		}
	}
	s.mu.Unlock()
	for _, rt := range runtimes {
		if rt.execution != nil {
			_, _ = rt.execution.CancelDurable("worktree removed")
		}
	}
}

// reauthorizeWorktrees grants the workspace window every registered worktree
// whose repository root (or directory) is already inside it. This is how a
// session whose persisted cwd is a worktree can be loaded after a restart
// without weakening the negotiated window.
func (s *server) reauthorizeWorktrees() {
	if s.worktrees == nil || s.settings == nil {
		return
	}
	s.mu.Lock()
	window := make([]string, 0, len(s.workspaceAdditionalDirectories)+1)
	if s.workspaceCwd != "" {
		window = append(window, filepath.Clean(s.workspaceCwd))
	}
	for _, dir := range s.workspaceAdditionalDirectories {
		window = append(window, filepath.Clean(dir))
	}
	s.mu.Unlock()
	if len(window) == 0 {
		return
	}
	worktrees, err := session.ListWorktrees(s.settings.GetSessionDir(), "", "")
	if err != nil {
		return
	}
	inWindow := func(dir string) bool {
		clean := filepath.Clean(dir)
		for _, root := range window {
			if clean == root {
				return true
			}
		}
		return false
	}
	for _, wt := range worktrees {
		if wt.Status == session.WorktreeStatusRemoved {
			continue
		}
		if inWindow(wt.RepositoryRoot) || inWindow(wt.Directory) {
			s.worktrees.AuthorizeDirectory(wt.Directory)
		}
	}
}

// notifyWorktreeStatus projects a canonical worktree event as the additive
// mothx/worktree/status notification. It is safe to call from the manager's
// asynchronous population goroutine.
func (s *server) notifyWorktreeStatus(event agentruntime.WorktreeEvent) {
	worktree := map[string]any{
		"id":        event.ID,
		"name":      event.Name,
		"branch":    event.Branch,
		"directory": event.Directory,
		"status":    event.Type,
	}
	if event.Message != "" {
		worktree["error"] = event.Message
	}
	_ = s.notifyExtension("mothx/worktree/status", map[string]any{"worktree": worktree})
}

// acpWorktreeRPCError maps a worktree mechanism error onto a structured ACP
// error, preserving its stable code.
func acpWorktreeRPCError(err error) *mcp.RPCError {
	var wtErr *worktree.Error
	if errors.As(err, &wtErr) {
		return acpStructuredRPCError(-32000, wtErr.Code, wtErr.Message, nil)
	}
	return acpStructuredRPCError(-32000, "worktree_failed", err.Error(), nil)
}
