package agentruntime

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

// RunEvent is the front-end-neutral durable representation of a run event.
// Adapters may keep their protocol payload in Data, but persistence is owned by
// this runtime boundary.
type RunEvent struct {
	ID        string
	SessionID string
	RunID     string
	EventType string
	Source    string
	Status    string
	Model     string
	Mode      string
	Timestamp time.Time
	Data      json.RawMessage
	// Assistant fields are Runtime-only terminal transaction inputs. They are
	// not serialized into the generic event envelope or exposed to prompts.
	AssistantEntryID string
	AssistantMessage provider.Message
}

// RunEventSinkFunc adapts a function to the adapter-neutral event sink.
type RunEventSinkFunc func(RunEvent) (string, error)

func (f RunEventSinkFunc) Record(event RunEvent) (string, error) {
	if f == nil {
		return "", nil
	}
	return f(event)
}

// RunEventSink persists run lifecycle events without exposing the adapter
// implementation to the Runtime.
type RunEventSink interface {
	Record(RunEvent) (string, error)
}

// RunEventProjector is an optional live-transport hook used when an event was
// persisted as part of an atomic admission transaction. It must not write a
// second durable row; it only fans the already committed event out to clients.
type RunEventProjector interface {
	Project(RunEvent, string) error
}

func withRunAttemptData(raw json.RawMessage, run DurableRun) json.RawMessage {
	data := make(map[string]any)
	if len(raw) > 0 && json.Unmarshal(raw, &data) != nil {
		return raw
	}
	if data == nil {
		data = make(map[string]any)
	}
	if run.IntentID != "" {
		if _, ok := data["intentId"]; !ok {
			data["intentId"] = run.IntentID
		}
	}
	if run.RetryOf != "" {
		if _, ok := data["retryOf"]; !ok {
			data["retryOf"] = run.RetryOf
		}
	}
	if _, ok := data["attempt"]; !ok && (run.IntentID != "" || run.RetryOf != "" || run.Attempt > 0) {
		attempt := run.Attempt
		if attempt <= 0 {
			attempt = 1
		}
		data["attempt"] = attempt
	}
	if len(data) == 0 {
		return raw
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return raw
	}
	return encoded
}

func withAssistantEntryData(raw json.RawMessage, entryID string) json.RawMessage {
	if entryID == "" {
		return raw
	}
	data := make(map[string]any)
	if len(raw) > 0 && json.Unmarshal(raw, &data) != nil {
		return raw
	}
	if data == nil {
		data = make(map[string]any)
	}
	if _, exists := data["assistantEntryId"]; !exists {
		data["assistantEntryId"] = entryID
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return raw
	}
	return encoded
}

// withTerminalErrorInfo ensures every unsuccessful durable terminal event
// carries the same safe ErrorInfo persisted on the Run row. Malformed adapter
// data is discarded instead of retaining a possible raw provider diagnostic.
func withTerminalErrorInfo(raw json.RawMessage, info ErrorInfo) json.RawMessage {
	data := make(map[string]any)
	if len(raw) > 0 && json.Unmarshal(raw, &data) != nil {
		data = make(map[string]any)
	}
	if data == nil {
		data = make(map[string]any)
	}
	data["error"] = info
	data["errorInfo"] = info
	data["errorMessage"] = DisplayErrorMessage(info)
	encoded, err := json.Marshal(data)
	if err != nil {
		return raw
	}
	return encoded
}

// SessionRunEventSink stores events in the existing session_run_events table.
// It intentionally reuses the existing session persistence API and schema.
type SessionRunEventSink struct {
	SessionDir string
}

func (s SessionRunEventSink) Record(ev RunEvent) (string, error) {
	if ev.SessionID == "" || ev.RunID == "" || ev.EventType == "" {
		return "", fmt.Errorf("run event requires session ID, run ID, and event type")
	}
	return session.SaveSessionRunEvent(s.SessionDir, session.SessionRunEvent{
		ID: ev.ID, SessionID: ev.SessionID, RunID: ev.RunID,
		EventType: ev.EventType, Source: ev.Source, Status: ev.Status,
		Model: ev.Model, Mode: ev.Mode, Timestamp: ev.Timestamp, Data: ev.Data,
	})
}

func (s SessionRunEventSink) RecordJSON(sessionID, runID, eventType, source, status, model, mode string, data any) (string, error) {
	var raw json.RawMessage
	if data != nil {
		encoded, err := json.Marshal(data)
		if err != nil {
			return "", err
		}
		raw = encoded
	}
	return s.Record(RunEvent{SessionID: sessionID, RunID: runID, EventType: eventType, Source: source, Status: status, Model: model, Mode: mode, Timestamp: time.Now(), Data: raw})
}
