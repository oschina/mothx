package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/sandbox"
	"github.com/startvibecoding/mothx/internal/tools"
)

// orderedProbeTool records the declared index of every parallel call that
// reaches the tool and holds each call until the whole batch arrived, so tests
// can prove that ordered starts did not serialize execution.
type orderedProbeTool struct {
	calls int

	mu      sync.Mutex
	entered []int
	closed  bool
	batch   chan struct{}

	serialized atomic.Bool
}

func newOrderedProbeTool(calls int) *orderedProbeTool {
	return &orderedProbeTool{calls: calls, batch: make(chan struct{})}
}

func (t *orderedProbeTool) Name() string { return "ordered_probe" }

func (t *orderedProbeTool) Description() string { return "records parallel tool entry order" }

func (t *orderedProbeTool) PromptSnippet() string { return "ordered probe" }

func (t *orderedProbeTool) PromptGuidelines() []string { return nil }

func (t *orderedProbeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"index":{"type":"number"},"padding":{"type":"string"}}}`)
}

func (t *orderedProbeTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	index, _ := params["index"].(float64)

	t.mu.Lock()
	t.entered = append(t.entered, int(index))
	if len(t.entered) == t.calls && !t.closed {
		t.closed = true
		close(t.batch)
	}
	t.mu.Unlock()

	// Every call of the batch must be inside Execute at the same time;
	// otherwise the ordered start degraded into serial execution.
	select {
	case <-t.batch:
	case <-time.After(3 * time.Second):
		t.serialized.Store(true)
		t.mu.Lock()
		if !t.closed {
			t.closed = true
			close(t.batch)
		}
		t.mu.Unlock()
	}
	return tools.NewTextToolResult(fmt.Sprintf("probe %d", int(index))), nil
}

func (t *orderedProbeTool) enteredOrder() []int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]int(nil), t.entered...)
}

func newProbeAgent(t *testing.T, registry *tools.Registry, stream []provider.StreamEvent, concurrency int, settings *config.Settings) *Agent {
	t.Helper()
	mock := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1", ContextWindow: 50000, MaxTokens: 512},
	}, stream)
	return NewWithLoopConfig(AgentLoopConfig{
		Config: Config{
			Provider: mock, Model: mock.Models()[0], Mode: "yolo", MaxTokens: 512, Settings: settings,
		},
		ToolExecutionMode:  "parallel",
		MaxToolConcurrency: concurrency,
		MaxIterations:      1,
	}, registry)
}

func newParallelProbeAgent(t *testing.T, registry *tools.Registry, stream []provider.StreamEvent, concurrency int) *Agent {
	t.Helper()
	return newProbeAgent(t, registry, stream, concurrency, nil)
}

// probeStreamEvents returns the provider stream that declares the given calls.
func probeStreamEvents(calls ...provider.ToolCallBlock) []provider.StreamEvent {
	stream := []provider.StreamEvent{{Type: provider.StreamStart}}
	for i := range calls {
		call := calls[i]
		stream = append(stream, provider.StreamEvent{Type: provider.StreamToolCall, ToolCall: &call})
	}
	return append(stream, provider.StreamEvent{Type: provider.StreamDone, StopReason: "tool_use"})
}

// probeCall builds one declared tool call. padding keeps the argument payload
// large enough for parsing it to outlast starting the other workers, which is
// exactly what reorders an ungated batch.
func probeCall(index int, padding int) provider.ToolCallBlock {
	payload := ""
	if padding > 0 {
		payload = strings.Repeat("a", padding)
	}
	args, err := json.Marshal(map[string]any{"index": index, "padding": payload})
	if err != nil {
		panic(err)
	}
	return provider.ToolCallBlock{
		ID:        fmt.Sprintf("call-%d", index),
		Name:      "ordered_probe",
		Arguments: args,
	}
}

func collectProbeRun(t *testing.T, a *Agent, timeout time.Duration) (started, ended []string) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range a.Run(context.Background(), "probe") {
			switch ev.Type {
			case EventToolExecutionStart:
				started = append(started, ev.ToolCallID)
			case EventToolExecutionEnd:
				ended = append(ended, ev.ToolCallID)
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatal("parallel tool batch stalled: a queued call was never released")
	}
	return started, ended
}

func TestParallelToolCallsStartInDeclaredOrderAndStillOverlap(t *testing.T) {
	const calls = 4

	tool := newOrderedProbeTool(calls)
	registry := tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
	registry.Register(tool)

	declared := make([]string, 0, calls)
	batch := make([]provider.ToolCallBlock, 0, calls)
	for i := 0; i < calls; i++ {
		padding := 0
		if i == 0 {
			padding = 256 << 10
		}
		call := probeCall(i, padding)
		batch = append(batch, call)
		declared = append(declared, call.ID)
	}
	stream := probeStreamEvents(batch...)

	a := newParallelProbeAgent(t, registry, stream, calls)
	started, ended := collectProbeRun(t, a, 30*time.Second)

	if len(started) != calls {
		t.Fatalf("start events = %v, want %d", started, calls)
	}
	for i := range declared {
		if started[i] != declared[i] {
			t.Fatalf("start order = %v, want declared order %v", started, declared)
		}
	}
	if entered := tool.enteredOrder(); len(entered) != calls {
		t.Fatalf("tool entries = %v, want %d", entered, calls)
	}
	if tool.serialized.Load() {
		t.Fatal("ordered parallel calls did not overlap: execution was serialized")
	}
	if len(ended) != calls {
		t.Fatalf("end events = %v, want %d", ended, calls)
	}
}

// An earlier call that waits for a user decision must not hold back the calls
// behind it: only the reported start is ordered, never the execution.
func TestParallelToolCallsDoNotWaitForAnEarlierApproval(t *testing.T) {
	tool := newOrderedProbeTool(1)
	registry := tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
	registry.Register(tool)
	registry.Register(fixedBashTool{})

	settings := &config.Settings{Approval: config.ApprovalSettings{BashBlacklist: []string{"git push"}}}
	blockedArgs, err := json.Marshal(map[string]any{"command": "git push origin main"})
	if err != nil {
		t.Fatalf("marshal bash arguments: %v", err)
	}
	blocked := provider.ToolCallBlock{ID: "call-0", Name: "bash", Arguments: blockedArgs}
	mock := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1", ContextWindow: 50000, MaxTokens: 512},
	}, probeStreamEvents(blocked, probeCall(1, 0)))

	a := NewWithLoopConfig(AgentLoopConfig{
		Config: Config{
			Provider: mock, Model: mock.Models()[0], Mode: "agent", MaxTokens: 512, Settings: settings,
		},
		ToolExecutionMode:  "parallel",
		MaxToolConcurrency: 2,
		MaxIterations:      1,
	}, registry)

	var started []string
	approvals := make(chan string, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range a.Run(context.Background(), "probe") {
			switch ev.Type {
			case EventToolExecutionStart:
				started = append(started, ev.ToolCallID)
			case EventToolApprovalRequest:
				approvals <- ev.ApprovalID
			}
		}
	}()

	// The queued call must reach its tool while call-0 is still awaiting approval.
	select {
	case <-tool.batch:
	case <-time.After(10 * time.Second):
		t.Fatal("the queued call waited for the earlier call's approval instead of running concurrently")
	}

	select {
	case approvalID := <-approvals:
		a.HandleApprovalResponse(approvalID, true)
	case <-time.After(10 * time.Second):
		t.Fatal("no approval request arrived for the blocked call")
	}

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("run did not finish after the approval")
	}
	if len(started) != 2 || started[0] != "call-0" || started[1] != "call-1" {
		t.Fatalf("start order = %v, want [call-0 call-1]", started)
	}
}

func TestParallelToolCallsReleaseQueuedCallsWhenAnEarlierCallFails(t *testing.T) {
	cases := []struct {
		name    string
		first   provider.ToolCallBlock
		wantErr string
	}{
		{
			name:    "invalid arguments",
			first:   provider.ToolCallBlock{ID: "call-0", Name: "ordered_probe", Arguments: json.RawMessage(`{"index":`)},
			wantErr: "parse tool arguments",
		},
		{
			name:    "unknown tool",
			first:   provider.ToolCallBlock{ID: "call-0", Name: "does_not_exist", Arguments: json.RawMessage(`{}`)},
			wantErr: "unknown tool",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool := newOrderedProbeTool(1)
			registry := tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
			registry.Register(tool)

			a := newParallelProbeAgent(t, registry, probeStreamEvents(tc.first, probeCall(1, 0)), 2)
			_, ended := collectProbeRun(t, a, 15*time.Second)

			if entered := tool.enteredOrder(); len(entered) != 1 || entered[0] != 1 {
				t.Fatalf("tool entries = %v, want the queued call to run after the failure", entered)
			}
			want := map[string]bool{"call-0": false, "call-1": false}
			for _, id := range ended {
				if _, ok := want[id]; ok {
					want[id] = true
				}
			}
			for id, seen := range want {
				if !seen {
					t.Fatalf("missing tool result for %s (ended=%v)", id, ended)
				}
			}
			if message := firstToolError(a, tc.first.ID); !strings.Contains(message, tc.wantErr) {
				t.Fatalf("first call error = %q, want %q", message, tc.wantErr)
			}
		})
	}
}

// firstToolError finds the recorded failure text of one call in the run history.
func firstToolError(a *Agent, callID string) string {
	for _, msg := range a.GetMessages() {
		if msg.Role == "toolResult" && msg.ToolCallID == callID && msg.IsError {
			return msg.Content
		}
	}
	return ""
}

// The background entry point used by Responses background runs shares the same
// handle, so a call that fails before reporting its start must still release the
// call queued behind it.
func TestExecuteBackgroundToolCallOrderedReleasesQueuedCalls(t *testing.T) {
	tool := newOrderedProbeTool(1)
	registry := tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
	registry.Register(tool)
	mock := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1", ContextWindow: 50000, MaxTokens: 512},
	}, nil)
	a := NewWithLoopConfig(AgentLoopConfig{
		Config:             Config{Provider: mock, Model: mock.Models()[0], Mode: "yolo", MaxTokens: 512},
		ToolExecutionMode:  "parallel",
		MaxToolConcurrency: 2,
		MaxIterations:      1,
	}, registry)

	order := NewToolLaunchOrder(2)
	failed := provider.ToolCallBlock{ID: "call-0", Name: "ordered_probe", Arguments: json.RawMessage(`{"index":`)}
	queued := probeCall(1, 0)
	ctx := context.Background()
	first := a.ExecuteBackgroundToolCallOrdered(ctx, failed, "", false, order.Handle(0))
	second := a.ExecuteBackgroundToolCallOrdered(ctx, queued, "", false, order.Handle(1))

	var failedResult string
	queuedStarted := false
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for ev := range first {
			if ev.Type == EventToolExecutionEnd {
				failedResult = ev.ToolResult
			}
		}
		for ev := range second {
			if ev.Type == EventToolExecutionStart && ev.ToolCallID == queued.ID {
				queuedStarted = true
			}
		}
	}()

	select {
	case <-drained:
	case <-time.After(20 * time.Second):
		t.Fatal("background batch stalled: the queued call never started")
	}
	if !strings.Contains(failedResult, "parse tool arguments") {
		t.Fatalf("first call result = %q, want a parse failure", failedResult)
	}
	if !queuedStarted {
		t.Fatal("queued background call never reported its start")
	}
	if entered := tool.enteredOrder(); len(entered) != 1 || entered[0] != 1 {
		t.Fatalf("tool entries = %v, want only the queued call", entered)
	}
}

func TestToolLaunchOrderKeepsStartOrderUnderJitter(t *testing.T) {
	const calls = 8

	order := NewToolLaunchOrder(calls)
	indexes := make([]int, calls)
	for i := range indexes {
		indexes[i] = i
	}

	var mu sync.Mutex
	var starts []int
	BoundedParallel(calls, indexes, func(index int) struct{} {
		handle := order.Handle(index)
		defer handle.Release()

		// Earlier calls become ready later, so only the declared order may decide
		// who reports its start first.
		time.Sleep(time.Duration(calls-index) * time.Millisecond)
		handle.waitStart()
		mu.Lock()
		starts = append(starts, index)
		mu.Unlock()
		handle.markStarted()
		return struct{}{}
	})

	if len(starts) != calls {
		t.Fatalf("recorded starts = %v, want %d", starts, calls)
	}
	for i := 0; i < calls; i++ {
		if starts[i] != i {
			t.Fatalf("start order = %v, want declared order", starts)
		}
	}
}

func TestToolLaunchHandleReleaseUnblocksQueuedCalls(t *testing.T) {
	order := NewToolLaunchOrder(2)
	first := order.Handle(0)
	queued := order.Handle(1)

	unblocked := make(chan struct{})
	go func() {
		defer close(unblocked)
		queued.waitStart()
	}()

	select {
	case <-unblocked:
		t.Fatal("queued call proceeded before the earlier call reported its start")
	case <-time.After(50 * time.Millisecond):
	}

	first.Release()
	select {
	case <-unblocked:
	case <-time.After(5 * time.Second):
		t.Fatal("Release did not unblock the queued call")
	}
}

func TestToolLaunchHandleNilIsNoOp(t *testing.T) {
	var handle *ToolLaunchHandle
	handle.waitStart()
	handle.markStarted()
	handle.Release()
}
