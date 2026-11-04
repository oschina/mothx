package tui

import (
	"context"
	"log"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/tui/i18n"
)

type mailboxWakeStartedMsg struct {
	run agentruntime.MailboxWakeRun
}

type mailboxWakeEventMsg struct {
	run   agentruntime.MailboxWakeRun
	event agent.Event
}

type mailboxWakeAgentMsg struct {
	run   agentruntime.MailboxWakeRun
	agent *agent.Agent
}

type mailboxWakeFinishedMsg struct {
	run   agentruntime.MailboxWakeRun
	state agentruntime.RunState
	err   error
}

// enableMailboxWake attaches the TUI's renderer to the Runtime-owned
// continuation path. The scheduler and the Run both stay in the shared
// Runtime; the TUI only receives canonical event projections.
func (a *App) enableMailboxWake() {
	if a == nil || a.runtime == nil || a.program == nil {
		return
	}
	if a.runtime.Mailbox == nil {
		return
	}
	build := agentruntime.AgentBuildOptions{
		Allow:        a.allow,
		MultiAgent:   a.multiAgent,
		DelegateMode: a.delegateMode,
		Workflows:    a.workflows,
	}
	if a.settings != nil {
		build.ThinkingLevel = provider.ThinkingLevel(a.settings.DefaultThinkingLevel)
	}
	err := a.runtime.EnableMailboxWake(context.Background(), agentruntime.MailboxWakeOptions{
		Build: build,
		OnStarted: func(run agentruntime.MailboxWakeRun) {
			a.sendMailboxWake(mailboxWakeStartedMsg{run: run})
		},
		OnEvent: func(run agentruntime.MailboxWakeRun, event agent.Event) {
			a.sendMailboxWake(mailboxWakeEventMsg{run: run, event: event})
		},
		OnAgent: func(run agentruntime.MailboxWakeRun, wakeAgent *agent.Agent) {
			a.sendMailboxWake(mailboxWakeAgentMsg{run: run, agent: wakeAgent})
		},
		OnFinished: func(run agentruntime.MailboxWakeRun, state agentruntime.RunState, err error) {
			a.sendMailboxWake(mailboxWakeFinishedMsg{run: run, state: state, err: err})
		},
	})
	if err != nil {
		log.Printf("[tui] enable mailbox wake: %v", err)
	}
}

func (a *App) sendMailboxWake(msg tea.Msg) {
	if a == nil || a.program == nil {
		return
	}
	a.program.Send(msg)
}

// restartMailboxWake refreshes the adapter policy captured when the scheduler
// was installed. It is safe only while the session is idle; callers already
// reject capability changes during a run.
func (a *App) restartMailboxWake() {
	if a == nil || a.runtime == nil {
		return
	}
	a.runtime.DisableMailboxWake()
	a.enableMailboxWake()
}

func (a *App) startMailboxWakeRun(msg mailboxWakeStartedMsg) (tea.Model, tea.Cmd) {
	if a == nil || a.runtime == nil || a.runtime.Execution == nil {
		return a, nil
	}
	if a.run != nil && a.run.id != msg.run.RunID {
		// A foreground run still owns the live view. Its EventRunFinished will
		// detach before the coordinator can admit this continuation; dropping a
		// racing start projection is safer than replacing a run mid-stream.
		return a, nil
	}
	sessionDir := ""
	if a.runtime.Manager != nil {
		sessionDir = a.runtime.Manager.GetSessionDir()
	}
	run := &tuiRun{
		execution:            a.runtime.Execution,
		decisions:            a.runtime.Decisions,
		sessionID:            msg.run.SessionID,
		id:                   msg.run.RunID,
		sessionDir:           sessionDir,
		model:                msg.run.Model,
		mode:                 msg.run.Mode,
		runtimeOwnedTerminal: true,
	}
	a.eventCh = nil
	a.run = run
	a.isThinking = true
	a.manualCompactionActive = false
	a.pendingAbortReason = ""
	a.runTerminalHandled = false
	a.finalizeInterruptedTools()
	a.spinnerIndex = 0
	a.requestStart = msg.run.StartedAt
	if a.requestStart.IsZero() {
		a.requestStart = time.Now()
	}
	a.lastDuration = 0
	a.streamRate.reset(a.requestStart)
	return a, tea.Batch(a.tickSpinner(), a.timer.Reset(), a.timer.Start())
}

func (a *App) finishMailboxWakeRun(msg mailboxWakeFinishedMsg) (tea.Model, tea.Cmd) {
	if a == nil || a.run == nil || a.run.id != msg.run.RunID {
		return a, nil
	}
	a.run = nil
	a.isThinking = false
	a.finalizeInterruptedTools()
	a.finishRequestTimer()
	if msg.err != nil {
		a.addMessage(errorStyle.Render(a.translator.Text(i18n.MsgErrorPrefix)) + msg.err.Error())
	} else if msg.state != agentruntime.RunStateCompleted && msg.state != agentruntime.RunStateCancelled {
		a.addMessage(warningStyle.Render(a.translator.Text(i18n.MsgSessionEndedPrefix)) + string(msg.state))
	}
	a.updateViewportContent()
	return a, tea.Batch(a.timer.Stop(), a.scheduleNextQueuedPrompt())
}

func (a *App) attachMailboxWakeAgent(msg mailboxWakeAgentMsg) (tea.Model, tea.Cmd) {
	if a == nil || a.run == nil || a.run.id != msg.run.RunID {
		return a, nil
	}
	a.run.agent = msg.agent
	return a, nil
}

// activeRunAgent is the Agent that currently owns the displayed run. A
// Runtime-owned continuation builds a fresh Agent, so routing decisions to the
// cached foreground lead would leave it blocked.
func (a *App) activeRunAgent() *agent.Agent {
	if a != nil && a.run != nil && a.run.agent != nil {
		return a.run.agent
	}
	if a == nil {
		return nil
	}
	return a.agent
}

// managesTUILeadAgent is true only for the foreground TUI run that owns the
// cached lead Agent. Runtime-owned continuations build their own Agent and must
// not mutate the foreground lead's manager state.
func (a *App) managesTUILeadAgent() bool {
	return a != nil && !a.mailboxWakeProjection &&
		a.agentManagementEnabled() && a.agentMgr != nil && a.agent != nil
}
