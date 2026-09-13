package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/sandbox"
	"github.com/startvibecoding/mothx/internal/tools"
)

// TestParallelTenEchoToolCalls drives ten real bash `echo N` calls through one
// parallel batch. The rendezvous in BeforeToolExecute only closes when all ten
// workers are live at the same time, so the batch is proven concurrent, while
// the start events must still keep the model's declared order.
func TestParallelTenEchoToolCalls(t *testing.T) {
	const calls = 10

	registry := tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
	registry.RegisterDefaults()

	declared := make([]string, 0, calls)
	stream := []provider.StreamEvent{{Type: provider.StreamStart}}
	for i := 1; i <= calls; i++ {
		args, err := json.Marshal(map[string]any{"command": fmt.Sprintf("echo %d", i)})
		if err != nil {
			t.Fatalf("marshal echo %d: %v", i, err)
		}
		call := provider.ToolCallBlock{ID: fmt.Sprintf("call-%d", i), Name: "bash", Arguments: args}
		declared = append(declared, call.ID)
		stream = append(stream, provider.StreamEvent{Type: provider.StreamToolCall, ToolCall: &call})
	}
	stream = append(stream, provider.StreamEvent{Type: provider.StreamDone, StopReason: "tool_use"})

	mock := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1", ContextWindow: 50000, MaxTokens: 512},
	}, stream)

	var (
		arrived    atomic.Int32
		serialized atomic.Bool
		releaseOne sync.Once
	)
	allArrived := make(chan struct{})
	release := func() { releaseOne.Do(func() { close(allArrived) }) }
	a := NewWithLoopConfig(AgentLoopConfig{
		Config: Config{Provider: mock, Model: mock.Models()[0], Mode: "yolo", MaxTokens: 512},
		BeforeToolExecute: func(BeforeToolExecuteContext) *ToolCallBlockResult {
			if arrived.Add(1) == calls {
				release()
			}
			select {
			case <-allArrived:
			case <-time.After(5 * time.Second):
				// A serialized batch: report it, and let everyone through so the
				// assertion fails with a clear message instead of hanging.
				serialized.Store(true)
				release()
			}
			return nil
		},
		ToolExecutionMode:  "parallel",
		MaxToolConcurrency: calls,
		MaxIterations:      1,
	}, registry)

	var (
		started []string
		ended   []string
		results = map[string]string{}
		failed  = map[string]error{}
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range a.Run(context.Background(), "run ten echo calls in parallel") {
			switch ev.Type {
			case EventToolExecutionStart:
				started = append(started, ev.ToolCallID)
			case EventToolExecutionEnd:
				ended = append(ended, ev.ToolCallID)
				results[ev.ToolCallID] = strings.TrimSpace(ev.ToolResult)
				failed[ev.ToolCallID] = ev.ToolError
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("the ten-call batch never finished")
	}

	t.Logf("declared start order: %v", declared)
	t.Logf("observed start order: %v", started)
	t.Logf("observed end order:   %v", ended)
	for _, id := range declared {
		t.Logf("  %s -> %q (err=%v)", id, results[id], failed[id])
	}

	if len(started) != calls {
		t.Fatalf("start events = %d, want %d", len(started), calls)
	}
	for i, id := range declared {
		if started[i] != id {
			t.Fatalf("start order = %v, want declared order %v", started, declared)
		}
	}
	if serialized.Load() || arrived.Load() != calls {
		t.Fatalf("only %d of %d calls reached the pre-execute hook together; the batch was not parallel", arrived.Load(), calls)
	}
	for i, id := range declared {
		want := strconv.Itoa(i + 1)
		if failed[id] != nil || !strings.Contains(results[id], "\n[stdout]\n"+want+"\n") {
			t.Fatalf("%s result = %q (err=%v), want stdout %q", id, results[id], failed[id], want)
		}
	}
	if len(ended) != calls {
		t.Fatalf("end events = %d, want %d", len(ended), calls)
	}
}
