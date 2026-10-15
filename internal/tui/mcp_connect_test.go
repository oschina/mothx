package tui

import (
	"testing"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/tui/i18n"
)

// TestDeferredMCPCmdOnlyRunsWhenDeferred keeps the interactive MCP connect on
// the deferred path only: a runtime that already connected (or never deferred)
// must not connect twice.
func TestDeferredMCPCmdOnlyRunsWhenDeferred(t *testing.T) {
	if cmd := (&App{}).deferredMCPCmd(); cmd != nil {
		t.Fatal("a nil runtime must not schedule a deferred MCP connect")
	}
	app := &App{runtime: &agentruntime.SessionRuntime{}}
	if cmd := app.deferredMCPCmd(); cmd != nil {
		t.Fatal("a non-deferred runtime must not schedule a deferred MCP connect")
	}
}

// TestHandleMCPConnectReadyDefersRebuildDuringRun guards that finishing the MCP
// connect never cancels an in-flight run just to pick up the new tools: the
// cached Agent is kept and rebuilt on the next idle submission.
func TestHandleMCPConnectReadyDefersRebuildDuringRun(t *testing.T) {
	app := &App{translator: i18n.New(i18n.LanguageEN)}
	app.agent = &agent.Agent{}
	app.run = newTUIRun()

	app.handleMCPConnectReady(mcpConnectReadyMsg{})

	if !app.mcpRebuildPending {
		t.Fatal("an active run must defer the agent rebuild")
	}
	if app.agent == nil {
		t.Fatal("an active run must not discard its Agent")
	}
}

// TestHandleMCPConnectReadyDiscardsIdleAgent guards the idle path: with no run
// in flight the cached Agent is dropped immediately so the next build freezes
// the newly registered MCP tools into its system prompt.
func TestHandleMCPConnectReadyDiscardsIdleAgent(t *testing.T) {
	app := &App{translator: i18n.New(i18n.LanguageEN)}
	app.agent = &agent.Agent{}

	app.handleMCPConnectReady(mcpConnectReadyMsg{})

	if app.mcpRebuildPending {
		t.Fatal("an idle handler must rebuild immediately, not defer")
	}
	if app.agent != nil {
		t.Fatal("an idle handler must discard the cached Agent")
	}
}
