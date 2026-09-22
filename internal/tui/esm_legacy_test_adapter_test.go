package tui

import (
	"context"
	"strings"

	internalagent "github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/esm"
)

// These compatibility helpers keep the historical TUI tests focused on the
// same shared runtime while the old production orchestration is removed.
func (a *App) runESMWorker(ctx context.Context, eventCh chan<- internalagent.Event, manager *internalagent.AgentManager, store *esm.Store, sessionID, runID, workDir, mode string, obj *esm.Objective) bool {
	adapter := &tuiTestESMAdapter{app: a, eventCh: eventCh, manager: manager}
	_, err := (&esm.Supervisor{Store: store, Adapter: adapter, Events: adapter}).Run(ctx, sessionID, strings.TrimSuffix(runID, "-worker"), workDir, mode)
	return err == nil
}

func (a *App) runESMCritic(ctx context.Context, eventCh chan<- internalagent.Event, manager *internalagent.AgentManager, store *esm.Store, sessionID, runID, workDir, mode string, obj *esm.Objective) bool {
	adapter := &tuiTestESMAdapter{app: a, eventCh: eventCh, manager: manager}
	_, err := (&esm.Supervisor{Store: store, Adapter: adapter, Events: adapter}).Run(ctx, sessionID, strings.TrimSuffix(runID, "-critic"), workDir, mode)
	return err == nil
}

func (a *App) runESMAudit(ctx context.Context, eventCh chan<- internalagent.Event, manager *internalagent.AgentManager, store *esm.Store, sessionID, runID, workDir, mode string, obj *esm.Objective) bool {
	adapter := &tuiTestESMAdapter{app: a, eventCh: eventCh, manager: manager}
	_, err := (&esm.Supervisor{Store: store, Adapter: adapter, Events: adapter}).Run(ctx, sessionID, strings.TrimSuffix(runID, "-audit"), workDir, mode)
	return err == nil
}

type tuiTestESMAdapter struct {
	app     *App
	eventCh chan<- internalagent.Event
	manager *internalagent.AgentManager
}

func (a *tuiTestESMAdapter) RunRole(ctx context.Context, req esm.RoleRequest) (esm.RoleResult, error) {
	result, err := a.app.runESMRoleAgentWithTimeoutForRole(ctx, a.eventCh, a.manager, req.Role, req.RunID, req.WorkDir, req.Mode, req.Tools, req.MaxIterations, req.Prompt, esmRoleTimeout)
	return esm.RoleResult{Response: result.Response, Tokens: result.Tokens, ToolCalls: result.ToolCalls, ToolNames: result.ToolNames, ToolError: result.ToolError}, err
}

func (a *tuiTestESMAdapter) PublishESMEvent(_ context.Context, event esm.RuntimeEvent) error {
	if a.eventCh != nil && event.Message != "" {
		sendESMEvent(context.Background(), a.eventCh, internalagent.Event{Type: internalagent.EventStatus, StatusMessage: event.Message})
	}
	return nil
}

func (a *tuiTestESMAdapter) RunRecoveryObserver(ctx context.Context, req esm.RoleRequest, _ error) (esm.RoleResult, error) {
	result, err := a.app.runESMRoleAgentWithTimeout(ctx, a.eventCh, a.manager, req.RunID, req.WorkDir, req.Mode, req.Tools, req.MaxIterations, req.Prompt, esm.RecoveryObserverTimeout)
	return esm.RoleResult{Response: result.Response, Tokens: result.Tokens, ToolCalls: result.ToolCalls, ToolNames: result.ToolNames, ToolError: result.ToolError}, err
}
