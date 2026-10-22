package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/tools"
)

// A reasoning model may work for many turns without emitting user-facing text.
// Reasoning output is progress and must not trip the silent tool-call guard.
func TestThinkingToolTurnsAreProgressNotStuck(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
	registry.Register(noopTool{})

	batches := make([][]provider.StreamEvent, 0, 7)
	for i := 0; i < 6; i++ {
		batches = append(batches, []provider.StreamEvent{
			{Type: provider.StreamStart},
			{Type: provider.StreamThinkDelta, ThinkDelta: fmt.Sprintf("still working on step %d", i)},
			{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{
				ID: fmt.Sprintf("call-%d", i), Name: "noop", Arguments: json.RawMessage(`{}`),
			}},
			{Type: provider.StreamDone, StopReason: "tool_use"},
		})
	}
	batches = append(batches, []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamTextDelta, TextDelta: "done"},
		{Type: provider.StreamDone, StopReason: "stop"},
	})
	scripted := newScriptedProvider(batches...)
	a := NewWithLoopConfig(AgentLoopConfig{
		Config:               Config{ID: "thinking", Provider: scripted, Model: scripted.models[0], Mode: "yolo"},
		MaxIterations:        20,
		MaxConsecutiveNoText: 2,
	}, registry)

	status, reason, errorEvent := collectTerminalEvents(t, a.Run(context.Background(), "finish the refactor"))
	if status != TaskSuccess || reason != "stop" || errorEvent {
		t.Fatalf("terminal = %q/%q errorEvent=%v, want success/stop", status, reason, errorEvent)
	}
	if scripted.calls != 7 {
		t.Fatalf("provider calls = %d, want 7", scripted.calls)
	}
}

// The guard must still stop a genuinely silent tool-call loop, and the terminal
// event must carry ErrAgentStuck so Runtime error classification can preserve
// the real stop reason.
func TestSilentToolTurnsStopWithStuckReason(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
	registry.Register(noopTool{})

	batches := make([][]provider.StreamEvent, 0, 8)
	for i := 0; i < 8; i++ {
		batches = append(batches, []provider.StreamEvent{
			{Type: provider.StreamStart},
			{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{
				ID: fmt.Sprintf("call-%d", i), Name: "noop", Arguments: json.RawMessage(`{}`),
			}},
			{Type: provider.StreamDone, StopReason: "tool_use"},
		})
	}
	scripted := newScriptedProvider(batches...)
	a := NewWithLoopConfig(AgentLoopConfig{
		Config:               Config{ID: "silent", Provider: scripted, Model: scripted.models[0], Mode: "yolo"},
		MaxIterations:        20,
		MaxConsecutiveNoText: 2,
	}, registry)

	terminal, ok := collectTerminalEvent(t, a.Run(context.Background(), "loop"), 10*time.Second)
	if !ok {
		t.Fatal("run did not reach a terminal event")
	}
	if terminal.Status != TaskIncomplete || terminal.StopReason != "stuck" {
		t.Fatalf("terminal = %q/%q, want incomplete/stuck", terminal.Status, terminal.StopReason)
	}
	if terminal.Error == nil || !errors.Is(terminal.Error, ErrAgentStuck) {
		t.Fatalf("terminal error = %v, want ErrAgentStuck", terminal.Error)
	}
	if !strings.Contains(terminal.Error.Error(), "without text or reasoning output") {
		t.Fatalf("terminal error = %q, want the silent-loop reason", terminal.Error)
	}
}

func collectTerminalEvent(t *testing.T, events <-chan Event, within time.Duration) (Event, bool) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return Event{}, false
			}
			if event.Type == EventRunFinished {
				return event, true
			}
		case <-deadline:
			t.Fatal("agent run did not reach a terminal event in time")
			return Event{}, false
		}
	}
}
