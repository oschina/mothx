package tui

import (
	"context"

	internalagent "github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/esm"
)

// esmRuntimeAdapter is the TUI host adapter for the ESM core. It owns only
// AgentManager execution and Bubble Tea event projection; ESM policy lives in
// internal/esm.
type esmRuntimeAdapter struct {
	app     *App
	eventCh chan<- internalagent.Event
	manager *internalagent.AgentManager
}

func (a *App) newESMRuntimeAdapter(eventCh chan<- internalagent.Event, manager *internalagent.AgentManager) *esmRuntimeAdapter {
	return &esmRuntimeAdapter{app: a, eventCh: eventCh, manager: manager}
}

func (r *esmRuntimeAdapter) RunRole(ctx context.Context, req esm.RoleRequest) (esm.RoleResult, error) {
	if r == nil || r.app == nil {
		return esm.RoleResult{}, context.Canceled
	}
	result, err := r.app.runESMRoleAgentWithTimeoutForRole(ctx, r.eventCh, r.manager, req.Role, req.RunID, req.WorkDir, req.Mode, req.Tools, req.MaxIterations, req.Prompt, esmRoleTimeout)
	return esm.RoleResult{
		Response: result.Response, Tokens: result.Tokens, ToolCalls: result.ToolCalls,
		ToolNames: result.ToolNames, ToolError: result.ToolError,
	}, err
}

func (r *esmRuntimeAdapter) PublishESMEvent(_ context.Context, event esm.RuntimeEvent) error {
	if r != nil && r.eventCh != nil && event.Message != "" {
		sendESMEvent(context.Background(), r.eventCh, internalagent.Event{Type: internalagent.EventStatus, StatusMessage: event.Message})
	}
	return nil
}

func (r *esmRuntimeAdapter) RunRecoveryObserver(ctx context.Context, req esm.RoleRequest, interruption error) (esm.RoleResult, error) {
	if r == nil || r.app == nil {
		return esm.RoleResult{}, context.Canceled
	}
	result, err := r.app.runESMRoleAgentWithTimeout(ctx, r.eventCh, r.manager, req.RunID, req.WorkDir, req.Mode, req.Tools, req.MaxIterations, req.Prompt, esm.RecoveryObserverTimeout)
	return esm.RoleResult{
		Response: result.Response, Tokens: result.Tokens, ToolCalls: result.ToolCalls,
		ToolNames: result.ToolNames, ToolError: result.ToolError,
	}, err
}
