package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/startvibecoding/mothx/agent"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/sandbox"
	"github.com/startvibecoding/mothx/internal/tools"
)

// collectRunEvents drains a run channel and returns every event in order.
func collectRunEvents(t *testing.T, ch <-chan Event) []Event {
	t.Helper()
	var events []Event
	for ev := range ch {
		events = append(events, ev)
	}
	return events
}

// requireSingleRunFinished verifies the canonical terminal contract: exactly
// one EventRunFinished per run, followed only by legacy terminal events and
// EventAgentEnd.
func requireSingleRunFinished(t *testing.T, events []Event) Event {
	t.Helper()
	var finished []Event
	finishedIdx := -1
	for i, ev := range events {
		if ev.Type == EventRunFinished {
			finished = append(finished, ev)
			if finishedIdx < 0 {
				finishedIdx = i
			}
		}
	}
	if len(finished) != 1 {
		t.Fatalf("EventRunFinished count = %d, want exactly 1", len(finished))
	}
	// Everything after the canonical terminal event must be legacy terminal
	// compatibility events or the lifecycle end marker.
	for _, ev := range events[finishedIdx+1:] {
		switch ev.Type {
		case EventDone, EventError, EventAgentEnd:
		default:
			t.Fatalf("non-terminal event %v after EventRunFinished", ev.Type)
		}
	}
	// The canonical terminal event must precede the legacy terminal events so
	// consumers can classify the outcome before compatibility handling runs.
	for i := 0; i < finishedIdx; i++ {
		if events[i].Type == EventDone || events[i].Type == EventError {
			t.Fatalf("legacy terminal event %v emitted before EventRunFinished", events[i].Type)
		}
	}
	if last := events[len(events)-1]; last.Type != EventAgentEnd {
		t.Fatalf("last event = %v, want EventAgentEnd", last.Type)
	}
	return finished[0]
}

func newTerminalContractAgent(t *testing.T, responses []provider.StreamEvent, maxIterations int) *Agent {
	t.Helper()
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1", ContextWindow: 50000, MaxTokens: 512},
	}, responses)
	cfg := AgentLoopConfig{
		Config: Config{
			Provider:  mockProvider,
			Model:     mockProvider.Models()[0],
			Mode:      "agent",
			MaxTokens: 512,
		},
		ToolExecutionMode: "sequential",
		MaxIterations:     maxIterations,
	}
	return NewWithLoopConfig(cfg, tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox()))
}

func TestRunFinishedSuccessOnNormalCompletion(t *testing.T) {
	a := newTerminalContractAgent(t, []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamTextDelta, TextDelta: "hello"},
		{Type: provider.StreamUsage, Usage: &provider.Usage{Input: 5, Output: 2}},
		{Type: provider.StreamDone, StopReason: "stop"},
	}, 3)

	events := collectRunEvents(t, a.Run(context.Background(), "hi"))
	finished := requireSingleRunFinished(t, events)
	if finished.Status != TaskSuccess {
		t.Fatalf("status = %q, want %q", finished.Status, TaskSuccess)
	}
	if finished.Error != nil {
		t.Fatalf("success run carries error %v", finished.Error)
	}
	if finished.StopReason != "stop" {
		t.Fatalf("stop reason = %q, want stop", finished.StopReason)
	}
	if !finished.Status.IsTerminal() || !finished.Status.IsSuccessful() {
		t.Fatalf("status helpers wrong for %q", finished.Status)
	}
}

func TestRunFinishedFailedOnStreamError(t *testing.T) {
	// A stream timeout is an availability failure that Agent Core keeps
	// retrying, so a terminal TaskFailed needs a permanent, non-retryable
	// stream error instead.
	a := newTerminalContractAgent(t, []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamError, Error: errors.New("provider returned a permanent failure"), StopReason: "error"},
	}, 3)

	events := collectRunEvents(t, a.Run(context.Background(), "hi"))
	finished := requireSingleRunFinished(t, events)
	if finished.Status != TaskFailed {
		t.Fatalf("status = %q, want %q", finished.Status, TaskFailed)
	}
	if finished.Error == nil {
		t.Fatal("failed run must carry an error")
	}
}

func TestRunProjectsProviderRetryMetadata(t *testing.T) {
	a := newTerminalContractAgent(t, []provider.StreamEvent{
		{Type: provider.StreamStart},
		{
			Type:             provider.StreamRetry,
			RetryAttempt:     2,
			RetryMaxAttempts: 4,
			RetryAfterMS:     1250,
			Error:            errors.New("Retrying (2/4): service unavailable"),
			RetryDetail:      "service unavailable (HTTP 503)",
		},
		{Type: provider.StreamTextDelta, TextDelta: "recovered"},
		{Type: provider.StreamDone, StopReason: "stop"},
	}, 3)

	events := collectRunEvents(t, a.Run(context.Background(), "hi"))
	statusIndex, retryIndex := -1, -1
	var status Event
	var retry Event
	for i, event := range events {
		switch event.Type {
		case EventStatus:
			if event.RetryStatus {
				statusIndex = i
				status = event
			}
		case EventRetry:
			retryIndex = i
			retry = event
		}
	}
	if statusIndex < 0 {
		t.Fatal("provider retry must preserve the compatibility EventStatus")
	}
	if retryIndex < 0 {
		t.Fatal("provider retry must emit EventRetry")
	}
	if statusIndex > retryIndex {
		t.Fatalf("EventStatus index = %d, EventRetry index = %d; compatibility status must precede retry event", statusIndex, retryIndex)
	}
	if status.StatusMessage != "Retrying (attempt 2/4); waiting 1.25s..." || strings.Contains(status.StatusMessage, "service unavailable") {
		t.Fatalf("compatibility status = %#v, want safe retry summary", status)
	}
	// Provider diagnostics ride on the marked EventRetry only, as a sanitized
	// single-line detail for adapters that opt into showing it.
	if retry.StatusMessage != "service unavailable (HTTP 503)" {
		t.Fatalf("retry detail = %#v, want sanitized provider diagnostic on EventRetry", retry)
	}
	if retry.RetryAttempt != 2 || retry.RetryMaxAttempts != 4 || retry.RetryAfterMS != 1250 {
		t.Fatalf("retry metadata = %#v, want attempt=2 max=4 delay=1250ms", retry)
	}
	if retry.RetryReason != "provider" {
		t.Fatalf("retry reason = %q", retry.RetryReason)
	}
}

func TestRunFinishedIncompleteOnMaxIterations(t *testing.T) {
	// Every turn requests a tool call, so the loop never reaches a no-tool
	// completion and must stop at MaxIterations.
	a := newTerminalContractAgent(t, []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{
			ID: "call_1", Name: "unknown_tool", Arguments: []byte(`{}`),
		}},
		{Type: provider.StreamUsage, Usage: &provider.Usage{Input: 10, Output: 3}},
		{Type: provider.StreamDone, StopReason: "tool_use"},
	}, 1)

	events := collectRunEvents(t, a.Run(context.Background(), "loop forever"))
	finished := requireSingleRunFinished(t, events)
	if finished.Status != TaskIncomplete {
		t.Fatalf("status = %q, want %q", finished.Status, TaskIncomplete)
	}
	if finished.StopReason != "max_iterations" {
		t.Fatalf("stop reason = %q, want max_iterations", finished.StopReason)
	}
}

func TestRunFinishedCanceledOnAbort(t *testing.T) {
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1", ContextWindow: 50000, MaxTokens: 512},
	}, []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{
			ID: "call_1", Name: "workflow_run", Arguments: []byte(`{}`),
		}},
		{Type: provider.StreamDone, StopReason: "tool_use"},
	})
	registry := tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
	registry.Register(blockingWorkflowRunTool{delay: time.Minute})

	cfg := AgentLoopConfig{
		Config: Config{
			Provider: mockProvider,
			Model:    mockProvider.Models()[0],
			Mode:     "yolo",
		},
		ToolExecutionMode: "sequential",
		MaxIterations:     10,
	}
	a := NewWithLoopConfig(cfg, registry)

	eventCh := a.Run(context.Background(), "test")

	// Wait until the blocking tool executes, then abort.
	deadline := time.After(10 * time.Second)
	started := false
	for !started {
		select {
		case event, ok := <-eventCh:
			if !ok {
				t.Fatal("event channel closed before tool execution started")
			}
			if event.Type == EventToolExecutionStart {
				started = true
			}
		case <-deadline:
			t.Fatal("tool execution did not start in time")
		}
	}
	a.Abort()

	var events []Event
	drainTimer := time.After(10 * time.Second)
	for {
		select {
		case event, ok := <-eventCh:
			if !ok {
				goto done
			}
			events = append(events, event)
		case <-drainTimer:
			t.Fatal("agent loop did not terminate after abort")
		}
	}
done:
	finished := requireSingleRunFinished(t, events)
	if finished.Status != TaskCanceled {
		t.Fatalf("status = %q, want %q", finished.Status, TaskCanceled)
	}
}

func TestRunFinishedBridgePreservesTerminalContract(t *testing.T) {
	// The public enum value must match the internal one so the numeric bridge
	// cast stays valid for the canonical terminal event.
	if int(EventRunFinished) != int(agentpkg.EventRunFinished) {
		t.Fatalf("EventRunFinished numeric mismatch: internal=%d public=%d", EventRunFinished, agentpkg.EventRunFinished)
	}

	pub := EventToPublic(Event{
		Type:       EventRunFinished,
		Status:     TaskCanceled,
		StopReason: "aborted",
		Done:       true,
	})
	if pub.Type != agentpkg.EventRunFinished {
		t.Fatalf("bridged type = %v, want EventRunFinished", pub.Type)
	}
	if pub.Status != agentpkg.TaskCanceled {
		t.Fatalf("bridged status = %q, want canceled", pub.Status)
	}
	if !pub.Status.IsTerminal() || pub.Status.IsSuccessful() {
		t.Fatalf("bridged status helpers wrong for %q", pub.Status)
	}
}

func TestTaskStatusHelpers(t *testing.T) {
	for status, wantTerminal := range map[TaskStatus]bool{
		TaskSuccess:           true,
		TaskIncomplete:        true,
		TaskFailed:            true,
		TaskCanceled:          true,
		TaskStatus(""):        false,
		TaskStatus("running"): false,
	} {
		if got := status.IsTerminal(); got != wantTerminal {
			t.Fatalf("IsTerminal(%q) = %v, want %v", status, got, wantTerminal)
		}
	}
	if !TaskSuccess.IsSuccessful() {
		t.Fatal("TaskSuccess must be successful")
	}
	for _, status := range []TaskStatus{TaskIncomplete, TaskFailed, TaskCanceled} {
		if status.IsSuccessful() {
			t.Fatalf("%q must not be successful", status)
		}
	}
}

// TestRunFinishedIncompleteOnWallClockBudget pins the single-terminal contract for
// the iteration-budget wall-clock exit: an exhausted time budget still emits
// exactly one EventRunFinished with the canonical incomplete/wall_clock_limit pair.
func TestRunFinishedIncompleteOnWallClockBudget(t *testing.T) {
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1", ContextWindow: 50000, MaxTokens: 512},
	}, []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamTextDelta, TextDelta: "hello"},
		{Type: provider.StreamDone, StopReason: "stop"},
	})
	cfg := AgentLoopConfig{
		Config: Config{
			Provider: mockProvider,
			Model:    mockProvider.Models()[0],
			Mode:     "agent",
		},
		ToolExecutionMode: "sequential",
		MaxIterations:     5,
		IterationBudget: IterationBudgetPolicy{
			Soft: 5, Hard: 10, RenewFactor: 0.5, MaxRenewals: 2, MinInterval: 1,
			MaxWallClock: time.Nanosecond,
		},
	}
	a := NewWithLoopConfig(cfg, tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox()))

	events := collectRunEvents(t, a.Run(context.Background(), "hi"))
	finished := requireSingleRunFinished(t, events)
	if finished.Status != TaskIncomplete {
		t.Fatalf("status = %q, want %q", finished.Status, TaskIncomplete)
	}
	if finished.StopReason != "wall_clock_limit" {
		t.Fatalf("stop reason = %q, want wall_clock_limit", finished.StopReason)
	}
}

// TestRunFinishedStaysSingleAcrossBudgetRenewal pins that a model-initiated
// renewal adds no terminal event: a run that extends its budget and then completes
// still emits exactly one EventRunFinished, and the renewal is a status event only.
func TestRunFinishedStaysSingleAcrossBudgetRenewal(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
	registry.Register(NewExtendBudgetTool())
	scripted := newScriptedProvider(
		[]provider.StreamEvent{
			{Type: provider.StreamStart},
			{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{ID: "c1", Name: IterationBudgetToolName, Arguments: json.RawMessage(`{"reason":"unfinished work"}`)}},
			{Type: provider.StreamDone, StopReason: "tool_use"},
		},
		[]provider.StreamEvent{
			{Type: provider.StreamStart},
			{Type: provider.StreamTextDelta, TextDelta: "done"},
			{Type: provider.StreamDone, StopReason: "stop"},
		},
	)
	cfg := AgentLoopConfig{
		Config:            Config{ID: "lead", Provider: scripted, Model: scripted.models[0], Mode: "yolo"},
		ToolExecutionMode: "sequential",
		MaxIterations:     1,
		IterationBudget: IterationBudgetPolicy{
			Soft: 1, Hard: 4, RenewFactor: 1.0, MaxRenewals: 2, MinInterval: 1, MaxWallClock: time.Hour,
		},
	}
	a := NewWithLoopConfig(cfg, registry)

	events := collectRunEvents(t, a.Run(context.Background(), "go"))
	finished := requireSingleRunFinished(t, events)
	if finished.Status != TaskSuccess {
		t.Fatalf("status = %q, want %q", finished.Status, TaskSuccess)
	}
	renewed := false
	for _, ev := range events {
		if ev.Type == EventStatus && strings.Contains(ev.StatusMessage, "Iteration budget renewed") {
			renewed = true
		}
	}
	if !renewed {
		t.Fatal("renewal must project exactly one status event")
	}
}
