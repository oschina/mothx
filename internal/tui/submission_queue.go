package tui

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oschina/mothx/internal/agentruntime"
)

// tuiSubmission is the TUI projection of one Runtime-owned submission. It is
// deliberately not a scheduler: the shared SubmissionCoordinator owns FIFO
// ordering and admission, and this value only carries the text/resources the
// adapter needs to start the admitted execution plus the row it renders while
// the submission waits.
type tuiSubmission struct {
	runID     string
	sessionID string
	text      string
	resources []agentruntime.PreparedInput
	runtime   *agentruntime.SessionRuntime

	// handle is published by the coordinator runner before the admission
	// hand-off message is sent.
	handle *agentruntime.ExecutionHandle

	started      atomic.Bool
	resourceOnce sync.Once
}

// discardPreparedResources releases staged resources that were never bound to a
// Run. Once the TUI has accepted the input for a started run, the run owns them
// and cleanup goes through the Runtime input/DiscardInput path instead.
func (s *tuiSubmission) discardPreparedResources() {
	if s == nil || len(s.resources) == 0 || s.runtime == nil {
		return
	}
	s.resourceOnce.Do(func() {
		s.runtime.DiscardPreparedResources(context.Background(), s.sessionID, s.resources)
	})
}

func (s *tuiSubmission) markStarted() {
	if s != nil {
		s.started.Store(true)
	}
}

// tuiSubmissionCompletedMsg is the single terminal projection for a submission.
// A zero-error completion means the Run finished normally; the row was already
// removed when the run started.
type tuiSubmissionCompletedMsg struct {
	submission *tuiSubmission
	completion agentruntime.ExecutionCompletion
}

func (a *App) addPendingSubmission(sub *tuiSubmission) {
	if a == nil || sub == nil {
		return
	}
	a.pendingSubmissions = append(a.pendingSubmissions, sub)
}

func (a *App) hasPendingSubmissions() bool {
	return a != nil && len(a.pendingSubmissions) > 0
}

func (a *App) removePendingSubmission(target *tuiSubmission) bool {
	if a == nil || target == nil {
		return false
	}
	for i, sub := range a.pendingSubmissions {
		if sub == target {
			a.pendingSubmissions = append(a.pendingSubmissions[:i], a.pendingSubmissions[i+1:]...)
			return true
		}
	}
	return false
}

// cancelPendingSubmissions asks the Runtime coordinator to cancel every queued
// submission and clears the adapter projection. It is used when a session is
// replaced; the completion watchers still release any unbound resources.
func (a *App) cancelPendingSubmissions() {
	if a == nil {
		return
	}
	for _, sub := range a.pendingSubmissions {
		if sub == nil || sub.handle == nil {
			continue
		}
		sub.handle.Cancel()
	}
	a.pendingSubmissions = nil
}

// dropAllQueuedInput clears queues that belong to the current session. Unlike
// abortPendingRequest, it must not carry staged prompts into a different
// session.
func (a *App) dropAllQueuedInput() {
	if a == nil {
		return
	}
	for _, prompt := range a.queuedPrompts {
		if prompt.resources != nil && a.runtime != nil {
			a.runtime.DiscardPreparedResources(context.Background(), a.currentSessionID(), prompt.resources)
		}
	}
	a.queuedPrompts = nil
	if a.preparingInputResources != nil && a.runtime != nil {
		a.runtime.DiscardPreparedResources(context.Background(), a.currentSessionID(), a.preparingInputResources)
	}
	a.preparingInputResources = nil
	if a.pendingInputResources != nil && a.runtime != nil {
		a.runtime.DiscardPreparedResources(context.Background(), a.currentSessionID(), a.pendingInputResources)
		a.pendingInputResources = nil
	}
	a.cancelPendingSubmissions()
}

// watchSubmission projects every terminal submission outcome back to the UI.
// The TUI must not infer queue completion from EventRunFinished: a run terminal
// event is not the Runtime admission/ownership barrier.
func (a *App) watchSubmission(sub *tuiSubmission) {
	if a == nil || sub == nil || sub.handle == nil {
		return
	}
	completion, ok := <-sub.handle.Completed
	if !ok {
		completion = agentruntime.ExecutionCompletion{
			RunID: sub.runID,
			Error: agentruntime.ErrSubmissionClosed,
		}
	}
	if !sub.started.Load() {
		sub.discardPreparedResources()
	}
	a.notifyAsync(tuiSubmissionCompletedMsg{submission: sub, completion: completion})
}

// notifyAsync is the adapter's single hand-off to the Bubble Tea loop. Tests can
// install sendHook instead of running a real Program.
func (a *App) notifyAsync(msg tea.Msg) {
	if a == nil || msg == nil {
		return
	}
	if a.sendHook != nil {
		a.sendHook(msg)
		return
	}
	if a.program != nil {
		a.program.Send(msg)
	}
}

func (a *App) handleSubmissionCompleted(msg tuiSubmissionCompletedMsg) tea.Cmd {
	if a == nil || msg.submission == nil {
		return nil
	}
	a.removePendingSubmission(msg.submission)
	err := msg.completion.Error
	if err != nil && !isSubmissionCancelError(err) && !msg.submission.started.Load() {
		a.addCommandError(fmt.Sprintf("Queued input failed: %v", err))
	}
	a.refreshIdleStateAfterSubmission()
	a.scheduleRender()
	return nil
}

func isSubmissionCancelError(err error) bool {
	return err == nil ||
		errors.Is(err, agentruntime.ErrSubmissionCancelled) ||
		errors.Is(err, context.Canceled)
}

// refreshIdleStateAfterSubmission closes the waiting state when no run, no
// pending submission, and no session preparation remain. A failed admission
// never produces an Agent terminal event, so without this the TUI would stay in
// the "working" state forever.
func (a *App) refreshIdleStateAfterSubmission() {
	if a == nil || a.run != nil || a.preparingSession || a.hasPendingSubmissions() {
		return
	}
	a.isThinking = false
	a.manualCompactionActive = false
	a.finishRequestTimer()
}
