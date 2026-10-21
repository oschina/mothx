package tui

import (
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/tools"
)

// TestIncompleteRunTerminalizesAndAcceptsNextInput reproduces the run that ends
// on a tool-call turn because it exhausted its iteration budget. The Agent has
// already persisted that assistant message and its tool results, so the Runtime
// terminal transaction must append the turn-end at the current leaf instead of
// re-parenting it under the earlier assistant entry. Otherwise the durable Run
// stays terminalizing, keeps the execution lease, and every later submission
// waits forever behind an admission that can never succeed.
func TestIncompleteRunTerminalizesAndAcceptsNextInput(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := filepath.Join(workDir, "sessions")
	sess := session.New(workDir, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatal(err)
	}
	// Always request a tool so every turn ends with tool calls; the run can then
	// only finish through the iteration limit, exactly like the reported case.
	responses := []provider.StreamEvent{
		{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{ID: "call-1", Name: "nonexistent_tool", Arguments: []byte(`{}`)}},
		{Type: provider.StreamDone, StopReason: "toolUse"},
	}
	mock := provider.NewMockProvider("mock", []*provider.Model{{ID: "test", Name: "Test"}}, responses)
	settings := config.DefaultSettings()
	settings.DefaultThinkingLevel = "off"
	app := NewApp(mock, mock.Models()[0], settings, sess, tools.NewRegistry(workDir, nil), "", "", "", nil, "agent", false, false, nil, nil, nil)
	if err := app.ensureRuntime(); err != nil {
		t.Fatal(err)
	}
	// The shared policy owns budget defaults; narrow it for a fast deterministic
	// iteration-limit run instead of driving hundreds of turns.
	app.runtime.Policy.IterationBudget = agent.IterationBudgetPolicy{Soft: 2, Hard: 3}

	startFirst := app.processInput("loop until incomplete")
	if startFirst == nil {
		t.Fatal("first processInput returned nil")
	}
	first, ok := startFirst().(agentStreamStartMsg)
	if !ok || first.err != nil || first.eventCh == nil {
		t.Fatalf("first stream start = %#v", first)
	}
	app.Update(first)
	runID := ""
	if app.run != nil {
		runID = app.run.id
	}
	if runID == "" {
		t.Fatal("TUI did not attach the accepted run")
	}

	deadline := time.After(20 * time.Second)
	var terminal *agent.Event
	for app.isThinking {
		select {
		case event, open := <-first.eventCh:
			if !open {
				app.Update(agentDoneMsg{eventCh: first.eventCh})
				continue
			}
			copied := event
			app.Update(agentEventMsg{event: event, eventCh: first.eventCh})
			if event.Type == agent.EventRunFinished {
				terminal = &copied
			}
		case <-deadline:
			t.Fatal("incomplete run never terminalized")
		}
	}
	if terminal == nil || terminal.Status != agent.TaskIncomplete {
		t.Fatalf("terminal event = %#v, want incomplete", terminal)
	}
	if app.run != nil {
		t.Fatal("TUI retained the terminal run")
	}

	// The durable outcome must match the canonical event, not default to success.
	sessionID := sess.GetHeader().ID
	run, err := agentruntime.GetDurableRun(t.Context(), sessionDir, runID)
	if err != nil {
		t.Fatalf("load durable run: %v", err)
	}
	if run == nil {
		t.Fatal("durable run was not recorded")
	}
	if run.Status != string(agentruntime.RunStateIncomplete) {
		t.Fatalf("durable run status = %q, want %q", run.Status, agentruntime.RunStateIncomplete)
	}
	if active, err := agentruntime.GetActiveDurableRun(t.Context(), sessionDir, sessionID); err != nil || active != nil {
		t.Fatalf("active durable run after incomplete = %#v (err %v), want none", active, err)
	}

	// The execution lease must be released so the next submission can start. A
	// regression leaves it draining and this wait never returns.
	startSecond := app.processInput("continue")
	if startSecond == nil {
		t.Fatal("second processInput returned nil")
	}
	secondResult := make(chan any, 1)
	go func() { secondResult <- startSecond() }()
	select {
	case result := <-secondResult:
		second, ok := result.(agentStreamStartMsg)
		if !ok || second.err != nil || second.eventCh == nil {
			t.Fatalf("second stream start = %#v", result)
		}
		app.Update(second)
		// Abort the follow-up run so the test does not leave an active provider
		// loop behind.
		app.Update(tea.KeyMsg{Type: tea.KeyEsc})
		app.waitForRunFinalization()
		app.retireEventStream()
	case <-time.After(15 * time.Second):
		t.Fatal("second input never started; session lease was not released")
	}
}
