package tui

import (
	"context"
	"errors"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/tui/i18n"
)

// errMCPToolsConnected is the synthetic cause used when the cached Agent must be
// rebuilt to freeze newly connected MCP tools into its next system prompt. It is
// an internal rebuild signal, not a user-facing failure.
var errMCPToolsConnected = errors.New("MCP tools connected")

// deferredMCPCmd connects the configured MCP servers a deferred build skipped.
// It runs on a Bubble Tea command goroutine, never the Update loop, so a slow
// or unhealthy server cannot freeze the UI. Tolerated failures are reported
// back through mcpConnectReadyMsg for a warning.
func (a *App) deferredMCPCmd() tea.Cmd {
	if a == nil || a.runtime == nil || !a.runtime.MCPDeferred() {
		return nil
	}
	runtime := a.runtime
	return func() tea.Msg {
		var failures []string
		err := runtime.ConnectDeferredMCP(context.Background(), agentruntime.MCPPolicy{
			Optional: true,
			OnError: func(cause error) {
				if cause != nil {
					failures = append(failures, cause.Error())
				}
			},
		})
		if err != nil {
			return mcpConnectReadyMsg{Err: err}
		}
		// The OnError collector captures both a malformed mcp.json and individual
		// server failures, so it is the complete warning set for this connect.
		return mcpConnectReadyMsg{Failures: failures}
	}
}

// mcpConnectReadyMsg reports the outcome of the deferred configured-MCP connect.
type mcpConnectReadyMsg struct {
	Failures []string
	Err      error
}

// handleMCPConnectReady folds an MCP connect outcome into the UI. MCP tools are
// registered directly on the shared registry, so the cached Agent must be
// rebuilt to freeze them into its next system prompt. An active run is never
// cancelled for this: the rebuild is deferred to the next idle submission.
func (a *App) handleMCPConnectReady(msg mcpConnectReadyMsg) tea.Cmd {
	if msg.Err != nil {
		a.addCommandError(a.translator.Text(i18n.MsgMCPConnectFailed, msg.Err))
	}
	for _, failure := range msg.Failures {
		a.addCommandError(a.translator.Text(i18n.MsgMCPConnectWarning, failure))
	}
	if a.agent != nil {
		if a.run == nil && !a.isThinking {
			a.discardAgent(errMCPToolsConnected)
		} else {
			a.mcpRebuildPending = true
		}
	}
	a.scheduleRender()
	return nil
}
