package tui

import (
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
)

func TestMailboxWakeProjectionUsesCanonicalRunWithoutForegroundTerminal(t *testing.T) {
	app := NewApp(nil, &provider.Model{ID: "mailbox-model"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "yolo", false, false, nil, nil, nil)
	execution := &agentruntime.ExecutionRuntime{}
	app.runtime = &agentruntime.SessionRuntime{ID: "session-1", Execution: execution}
	run := agentruntime.MailboxWakeRun{
		RunID: "wake_run-1", SessionID: "session-1", Model: "mailbox-model", Mode: "yolo", StartedAt: time.Now(),
	}

	if _, _ = app.Update(mailboxWakeStartedMsg{run: run}); app.run == nil {
		t.Fatal("mailbox wake start did not install a projection run")
	}
	if app.run.id != run.RunID || !app.run.runtimeOwnedTerminal {
		t.Fatalf("projection run = %#v, want runtime-owned canonical run %s", app.run, run.RunID)
	}

	before := len(app.messages)
	app.mailboxWakeProjection = true
	_ = app.handleAgentEvent(agent.Event{
		Type:    agent.EventMessageStart,
		Message: provider.Message{Role: "user", Content: "member completion", SystemInjected: true},
	})
	if len(app.messages) != before {
		t.Fatalf("system-injected continuation seed rendered as a user message: %#v", app.messages[before:])
	}

	app.mailboxWakeProjection = true
	_ = app.handleAgentEvent(agent.Event{Type: agent.EventRunFinished, Status: agent.TaskSuccess})
	if app.run != nil {
		t.Fatal("canonical terminal event did not detach the mailbox wake projection")
	}
	if app.mailboxWakeProjection {
		t.Fatal("mailbox wake projection flag was not cleared")
	}
}

func TestMailboxWakeFinishReportsBuildFailureWithoutTouchingForegroundRun(t *testing.T) {
	app := NewApp(nil, &provider.Model{ID: "mailbox-model"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "yolo", false, false, nil, nil, nil)
	app.runtime = &agentruntime.SessionRuntime{ID: "session-1", Execution: &agentruntime.ExecutionRuntime{}}
	run := agentruntime.MailboxWakeRun{RunID: "wake_run-2", SessionID: "session-1", Model: "mailbox-model", Mode: "yolo", StartedAt: time.Now()}

	app.startMailboxWakeRun(mailboxWakeStartedMsg{run: run})
	app.finishMailboxWakeRun(mailboxWakeFinishedMsg{run: run, state: agentruntime.RunStateFailed, err: assertError("build failed")})
	if app.run != nil {
		t.Fatal("failed mailbox wake did not detach its projection")
	}
	if app.isThinking {
		t.Fatal("failed mailbox wake left the TUI thinking")
	}
}

func TestMailboxWakeTerminalEventClearsPendingDecisions(t *testing.T) {
	app := NewApp(nil, &provider.Model{ID: "mailbox-model"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "yolo", false, false, nil, nil, nil)
	decisions := &agentruntime.DecisionService{}
	app.runtime = &agentruntime.SessionRuntime{ID: "session-1", Execution: &agentruntime.ExecutionRuntime{}, Decisions: decisions}
	run := agentruntime.MailboxWakeRun{
		RunID: "wake_run-decisions", SessionID: "session-1", Model: "mailbox-model", Mode: "yolo", StartedAt: time.Now(),
	}
	app.startMailboxWakeRun(mailboxWakeStartedMsg{run: run})
	if err := app.run.registerDecision("question-1", agentruntime.DecisionQuestion); err != nil {
		t.Fatalf("register decision: %v", err)
	}
	if pending := decisions.Pending(); len(pending) != 1 {
		t.Fatalf("pending decisions = %#v, want one", pending)
	}
	app.mailboxWakeProjection = true
	_ = app.handleAgentEvent(agent.Event{Type: agent.EventRunFinished, Status: agent.TaskCanceled})
	if pending := decisions.Pending(); len(pending) != 0 {
		t.Fatalf("wake terminal left pending decisions: %#v", pending)
	}
}

type assertError string

func (e assertError) Error() string { return string(e) }
