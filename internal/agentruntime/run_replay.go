package agentruntime

import (
	"encoding/json"
	"sort"

	"github.com/oschina/mothx/internal/session"
)

// RunReplay is the adapter-neutral projection of persisted run events. It is
// intentionally read-only: adapters decide how to render or recover protocol
// state from the event data.
type RunReplay struct {
	SessionID string
	RunID     string
	Events    []RunEvent
	Status    RunState
	Terminal  bool
}

// ReplayRunEvents reconstructs one run's latest lifecycle state from durable
// SessionRunEvents. Unknown event types remain in Events for adapter replay.
func ReplayRunEvents(events []session.SessionRunEvent, runID string) RunReplay {
	replay := RunReplay{RunID: runID}
	for _, event := range events {
		if runID != "" && event.RunID != runID {
			continue
		}
		if replay.SessionID == "" {
			replay.SessionID = event.SessionID
		}
		replay.Events = append(replay.Events, RunEvent{
			ID: event.ID, SessionID: event.SessionID, RunID: event.RunID,
			EventType: event.EventType, Source: event.Source, Status: event.Status,
			Model: event.Model, Mode: event.Mode, Timestamp: event.Timestamp, Data: event.Data,
		})
		if state, ok := runStateFromEvent(event); ok {
			replay.Status = state
			replay.Terminal = isTerminalRunState(state)
		}
	}
	return replay
}

func runStateFromEvent(event session.SessionRunEvent) (RunState, bool) {
	switch event.EventType {
	case "started", "remote_started":
		return RunStateRunning, true
	case "waiting_for_approval", "approval_requested":
		return RunStateWaitingApproval, true
	case "waiting_for_question", "question_requested":
		return RunStateWaitingQuestion, true
	case "cancelling", "cancel_requested":
		return RunStateCancelling, true
	case "finished", "completed":
		return RunStateCompleted, true
	case "failed":
		return RunStateFailed, true
	case "canceled", "cancelled":
		return RunStateCancelled, true
	case "timed_out", "timeout":
		return RunStateTimedOut, true
	case "incomplete":
		return RunStateIncomplete, true
	}
	switch event.Status {
	case "created":
		return RunStateCreated, true
	case "queued":
		return RunStateQueued, true
	case "running":
		return RunStateRunning, true
	case "waiting_for_approval":
		return RunStateWaitingApproval, true
	case "waiting_for_question":
		return RunStateWaitingQuestion, true
	case "cancelling", "terminalizing":
		return RunStateCancelling, true
	case "completed":
		return RunStateCompleted, true
	case "failed":
		return RunStateFailed, true
	case "cancelled", "canceled":
		return RunStateCancelled, true
	case "timed_out":
		return RunStateTimedOut, true
	case "incomplete":
		return RunStateIncomplete, true
	default:
		return "", false
	}
}

// ReplayRunEventsJSON provides a stable JSON projection for adapters that need
// to pass durable replay data across an API boundary.
func ReplayRunEventsJSON(events []session.SessionRunEvent, runID string) ([]byte, error) {
	replay := ReplayRunEvents(events, runID)
	sort.SliceStable(replay.Events, func(i, j int) bool {
		return replay.Events[i].Timestamp.Before(replay.Events[j].Timestamp)
	})
	return json.Marshal(replay)
}
