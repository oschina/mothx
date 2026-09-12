package agent

import (
	"context"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/tools"
)

// scriptedProvider replays one event batch per Chat call, so a test can make a
// turn end with "length" and the next (recovered) turn end with "stop".
type scriptedProvider struct {
	models  []*provider.Model
	batches [][]provider.StreamEvent
	calls   int
}

func (p *scriptedProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent, 32)
	idx := p.calls
	if idx >= len(p.batches) {
		idx = len(p.batches) - 1
	}
	p.calls++
	events := p.batches[idx]
	go func() {
		defer close(ch)
		for _, event := range events {
			select {
			case <-ctx.Done():
				return
			case ch <- event:
			}
		}
	}()
	return ch
}

func (p *scriptedProvider) Name() string              { return "scripted" }
func (p *scriptedProvider) API() string               { return "mock" }
func (p *scriptedProvider) Models() []*provider.Model { return p.models }
func (p *scriptedProvider) GetModel(id string) *provider.Model {
	for _, m := range p.models {
		if m.ID == id {
			return m
		}
	}
	return nil
}

func newScriptedProvider(batches ...[]provider.StreamEvent) *scriptedProvider {
	return &scriptedProvider{models: []*provider.Model{{ID: "scripted-model", Name: "Scripted"}}, batches: batches}
}

func collectTerminalEvents(t *testing.T, events <-chan Event) (TaskStatus, string, bool) {
	t.Helper()
	var (
		status     TaskStatus
		reason     string
		errorEvent bool
	)
	deadline := time.After(10 * time.Second)
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return status, reason, errorEvent
			}
			switch event.Type {
			case EventRunFinished:
				status = event.Status
				reason = event.StopReason
			case EventError:
				errorEvent = true
			}
		case <-deadline:
			t.Fatal("agent run did not reach a terminal event in time")
			return status, reason, errorEvent
		}
	}
}

// TestMemberWaitCancellationTerminalizesRunAsCanceled guards the cancellation
// window inside the team-lead follow-up hook: the loop-entry cancellation check
// is only reached on the next iteration, so a run cancelled while it waits for
// its members used to finish as a success. Adapters that derive their terminal
// state from EventRunFinished (ACP/Desktop) then projected a user cancel as a
// normal completion.
func TestMemberWaitCancellationTerminalizesRunAsCanceled(t *testing.T) {
	scripted := newScriptedProvider([]provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamTextDelta, TextDelta: "lead turn"},
		{Type: provider.StreamDone, StopReason: "stop"},
	})

	mailbox := NewMemberMailbox()
	mailbox.SetRunningPredicate(func() bool { return true }) // a member is still running

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := NewWithLoopConfig(AgentLoopConfig{
		Config:              Config{ID: "lead", Provider: scripted, Model: scripted.models[0], Mode: "yolo"},
		GetFollowUpMessages: ComposeFollowUps(mailbox, nil),
	}, tools.NewRegistry(t.TempDir(), nil))

	events := a.Run(ctx, "start")
	go func() {
		time.Sleep(200 * time.Millisecond) // let the loop reach the member wait
		cancel()
	}()

	status, reason, _ := collectTerminalEvents(t, events)
	if status != TaskCanceled {
		t.Fatalf("terminal status = %q reason = %q, want %q for a run cancelled during the member wait", status, reason, TaskCanceled)
	}
	if reason != "aborted" {
		t.Fatalf("terminal reason = %q, want aborted", reason)
	}
}

// TestTruncatedTurnDoesNotMarkARecoveredTurnIncomplete guards the per-turn scope
// of the truncation flag: after a follow-up kept the run open, the next turn
// completed normally and must be reported as a success instead of inheriting
// output_limit from the previous turn.
func TestTruncatedTurnDoesNotMarkARecoveredTurnIncomplete(t *testing.T) {
	scripted := newScriptedProvider(
		[]provider.StreamEvent{
			{Type: provider.StreamStart},
			{Type: provider.StreamTextDelta, TextDelta: "cut off"},
			{Type: provider.StreamDone, StopReason: "length"},
		},
		[]provider.StreamEvent{
			{Type: provider.StreamStart},
			{Type: provider.StreamTextDelta, TextDelta: "complete answer"},
			{Type: provider.StreamDone, StopReason: "stop"},
		},
	)

	drains := 0
	a := NewWithLoopConfig(AgentLoopConfig{
		Config: Config{
			ID:               "lead",
			Provider:         scripted,
			Model:            scripted.models[0],
			Mode:             "yolo",
			MaxTokensUserSet: true, // no escalation; the truncated turn is final
		},
		GetFollowUpMessages: func(context.Context) []provider.Message {
			drains++
			if drains == 1 {
				return []provider.Message{provider.NewSystemInjectedUserMessage("[MEMBER_COMPLETION] member finished")}
			}
			return nil
		},
	}, tools.NewRegistry(t.TempDir(), nil))

	status, reason, errorEvent := collectTerminalEvents(t, a.Run(context.Background(), "start"))
	if status != TaskSuccess {
		t.Fatalf("terminal status = %q reason = %q errorEvent = %v, want the recovered turn to be a success", status, reason, errorEvent)
	}
	if errorEvent {
		t.Fatal("a recovered turn must not emit a terminal error event")
	}
	if scripted.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", scripted.calls)
	}
}
