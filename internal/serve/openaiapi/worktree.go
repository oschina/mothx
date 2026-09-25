package openaiapi

import (
	"fmt"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/esm"
)

// worktreeManagerForSession builds the shared Runtime worktree manager for a
// WebUI session. Worktree mechanics, authorization and events are unchanged; it
// is installed so ESM roles and sub-agents can request an isolated workspace.
func (s *Server) worktreeManagerForSession(settings *config.Settings) *agentruntime.WorktreeManager {
	if s == nil || settings == nil || !settings.IsWorktreeEnabled() {
		return nil
	}
	level := settings.Sandbox.EffectiveLevel()
	mgr, err := agentruntime.NewWorktreeManager(agentruntime.WorktreeManagerOptions{
		SessionDir:          settings.GetSessionDir(),
		BranchPrefix:        settings.WorktreeBranchPrefix(),
		DefaultStartCommand: settings.WorktreeStartCommand(),
		SandboxOptions:      settings.Sandbox.Options(),
		SandboxLevel:        &level,
	})
	if err != nil {
		return nil
	}
	return mgr
}

// esmWorktreeSpec is the shared per-objective ESM worktree request: one worktree
// per session objective, reused across every role. Optional so a non-git
// workspace degrades to the session workspace instead of failing the role.
func (s *Server) esmWorktreeSpec(req esm.RoleRequest) *agent.WorktreeSpec {
	if s == nil || s.settings == nil || !s.settings.WorktreeESMEnabled() {
		return nil
	}
	if req.SessionID == "" {
		return nil
	}
	return &agent.WorktreeSpec{
		Name:     fmt.Sprintf("esm-%s", req.SessionID),
		Reuse:    true,
		Optional: true,
	}
}
