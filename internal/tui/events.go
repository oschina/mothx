package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oschina/mothx/internal/agent"
)

type agentEventMsg struct {
	event   agent.Event
	eventCh <-chan agent.Event
}
type agentDoneMsg struct {
	err        error
	stopReason string
	eventCh    <-chan agent.Event
}
type updateNoticeMsg string

// noticeMsg carries a process-level notice (for example a database that another
// mothx process rebuilt) into the view.
type noticeMsg string

// ShowUpdateNotice displays an update notification from a background check.
// ShowNotice displays a process-level notice raised by a background watcher.
func (a *App) ShowNotice(notice string) {
	if notice == "" || a.program == nil {
		return
	}
	a.program.Send(noticeMsg(notice))
}

func (a *App) ShowUpdateNotice(notice string) {
	if notice == "" || a.program == nil {
		return
	}
	a.program.Send(updateNoticeMsg(notice))
}

func (a *App) listenAgentEvents() tea.Cmd {
	eventCh := a.eventCh
	return func() tea.Msg {
		var next agent.Event
		var lastDone agent.Event
		err := agent.ConsumeEvents(context.Background(), eventCh, agent.EventHandlerFunc(func(_ context.Context, event agent.Event) error {
			next = event
			// Capture the last terminal event for stop reason
			if event.Type == agent.EventDone || event.Type == agent.EventError || event.Type == agent.EventRunFinished {
				lastDone = event
			}
			return context.Canceled
		}))
		if next.Type != 0 || err == context.Canceled {
			return agentEventMsg{event: next, eventCh: eventCh}
		}
		return agentDoneMsg{err: err, stopReason: lastDone.StopReason, eventCh: eventCh}
	}
}
