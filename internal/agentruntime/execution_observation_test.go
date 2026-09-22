package agentruntime

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/agent"
)

type observationRunStore struct {
	recordingDurableRunStore
	progress []RetryInfo
	errors   []ErrorInfo
}

func (s *observationRunStore) UpdateProgress(_ string, value RetryInfo) error {
	s.progress = append(s.progress, value)
	return nil
}

func (s *observationRunStore) UpdateErrorInfo(_ string, value ErrorInfo) error {
	s.errors = append(s.errors, value)
	return nil
}

func TestExecutionRuntimeObserveAgentEventPersistsRetryAndSafeTerminalError(t *testing.T) {
	store := &observationRunStore{}
	sink := &recordingRunEventSink{}
	var runtime ExecutionRuntime
	runtime.SetRunStore(store)
	runtime.SetEventSink(sink)
	run := DurableRun{ID: "run-observe", SessionID: "session-observe", IntentID: "intent-observe", Source: "webui", Model: "model", Mode: "agent"}
	if _, err := runtime.BeginDurable(context.Background(), run, RunEvent{EventType: "started"}); err != nil {
		t.Fatalf("begin durable: %v", err)
	}

	observed, err := runtime.ObserveAgentEvent(agent.Event{
		Type:             agent.EventRetry,
		RetryAttempt:     2,
		RetryMaxAttempts: 3,
		RetryAfterMS:     1200,
		RetryReason:      "provider timeout",
		StatusMessage:    `"auto" tool choice requires --enable-auto-tool-choice (api_key=sk-secret-123)`,
	})
	if err != nil {
		t.Fatalf("observe retry: %v", err)
	}
	if observed.Retry == nil || observed.Retry.Attempt != 2 || observed.Retry.MaxAttempts != 3 || observed.Retry.RetryAfterMS != 1200 {
		t.Fatalf("retry observation = %#v", observed)
	}
	if len(store.progress) != 1 || store.progress[0].ReasonCode != "timeout" {
		t.Fatalf("stored retry progress = %#v", store.progress)
	}
	// The provider diagnostic is redacted and bounded before it enters the
	// durable record, keeping the actionable detail renderable by adapters.
	if !strings.Contains(store.progress[0].Message, `"auto" tool choice requires --enable-auto-tool-choice`) {
		t.Fatalf("stored retry message = %q, want sanitized provider detail", store.progress[0].Message)
	}
	if strings.Contains(store.progress[0].Message, "sk-secret-123") || !strings.Contains(store.progress[0].Message, "[redacted]") {
		t.Fatalf("stored retry message = %q, want credential redaction", store.progress[0].Message)
	}
	if len(sink.events) != 2 || sink.events[1].EventType != "run_retrying" || sink.events[1].Status != string(RunStateRunning) {
		t.Fatalf("durable events = %#v", sink.events)
	}
	if !bytes.Contains(sink.events[1].Data, []byte(`"message":`)) || bytes.Contains(sink.events[1].Data, []byte("sk-secret-123")) {
		t.Fatalf("run_retrying data = %s, want redacted message field", sink.events[1].Data)
	}

	if _, err := runtime.ObserveAgentEvent(agent.Event{Type: agent.EventTextDelta, TextDelta: "partial answer"}); err != nil {
		t.Fatalf("observe text: %v", err)
	}
	if _, err := runtime.ObserveAgentEvent(agent.Event{Type: agent.EventToolExecutionStart, ToolName: "bash"}); err != nil {
		t.Fatalf("observe tool start: %v", err)
	}
	observed, err = runtime.ObserveAgentEvent(agent.Event{
		Type:   agent.EventRunFinished,
		Status: agent.TaskError,
		Error:  fmt.Errorf("HTTP 503 provider returned secret diagnostic"),
	})
	if err != nil {
		t.Fatalf("observe terminal: %v", err)
	}
	if observed.Error == nil || observed.Error.RetryMode != RetryDecisionRequired || observed.Error.SideEffectState != SideEffectUnknown || !observed.Error.PartialOutput {
		t.Fatalf("terminal observation = %#v", observed)
	}
	if observed.Error.Message != "HTTP 503 provider returned secret diagnostic" || observed.Error.Detail != observed.Error.Message {
		t.Fatalf("provider diagnostic was not preserved: %#v", observed.Error)
	}
	if len(store.errors) != 1 || store.errors[0].IntentID != run.IntentID {
		t.Fatalf("stored errors = %#v", store.errors)
	}
	if len(store.progress) != 2 || store.progress[1] != (RetryInfo{}) {
		t.Fatalf("terminal retry progress was not cleared: %#v", store.progress)
	}
}

type intentRunStore struct {
	recordingDurableRunStore
	intents map[string]ExecutionIntent
}

func (s *intentRunStore) CreateIntentAndRun(intent ExecutionIntent, run DurableRun) error {
	if s.intents == nil {
		s.intents = make(map[string]ExecutionIntent)
	}
	s.intents[intent.ID] = intent
	s.created = append(s.created, run)
	return nil
}

func (s *intentRunStore) GetIntent(id string) (*ExecutionIntent, error) {
	intent, ok := s.intents[id]
	if !ok {
		return nil, nil
	}
	return &intent, nil
}

func TestExecutionRuntimeIntentAdmissionAndLinkedRetry(t *testing.T) {
	store := &intentRunStore{}
	var runtime ExecutionRuntime
	runtime.SetRunStore(store)
	intent := ExecutionIntent{ID: "intent-1", SessionID: "session-1"}
	first := DurableRun{ID: "run-1", SessionID: "session-1", IntentID: intent.ID, Attempt: 1, Status: "queued"}
	if _, err := runtime.BeginIntentDurable(context.Background(), intent, first, RunEvent{EventType: "started"}); err != nil {
		t.Fatalf("begin intent durable: %v", err)
	}
	if err := runtime.FinishDurable(first.ID, RunStateFailed, "failed", RunEvent{EventType: "failed"}); err != nil {
		t.Fatalf("finish first attempt: %v", err)
	}
	retry := DurableRun{ID: "run-2", SessionID: "session-1", IntentID: intent.ID, RetryOf: first.ID, Attempt: 2, Status: "queued"}
	loaded, _, err := runtime.BeginRetryDurable(context.Background(), retry, RunEvent{EventType: "started"})
	if err != nil {
		t.Fatalf("begin linked retry: %v", err)
	}
	if loaded == nil || loaded.ID != intent.ID {
		t.Fatalf("loaded intent = %#v", loaded)
	}
	if len(store.created) != 2 || store.created[0].ID != first.ID || store.created[1].ID != retry.ID || store.created[1].RetryOf != first.ID {
		t.Fatalf("created attempt chain = %#v", store.created)
	}
}
