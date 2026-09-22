package channels

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/messaging"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/serve/hooks"
	"github.com/oschina/mothx/internal/session"
)

// blockingChildChannelProvider makes the child agent's provider call block until
// its run context is canceled (which happens when the parent run finishes), so
// the parent run — and its event stream — always ends first. Requests are
// routed by content: the parent follow-up call and the child's first call race
// with each other, so a shared call counter could hand the blocking branch to
// the parent and deadlock the test.
type blockingChildChannelProvider struct {
	model        *provider.Model
	childStarted chan struct{}
	childOnce    sync.Once
}

func (p *blockingChildChannelProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	payload, _ := json.Marshal(params.Messages)
	ch := make(chan provider.StreamEvent, 8)
	go func() {
		defer close(ch)
		send := func(event provider.StreamEvent) bool {
			select {
			case ch <- event:
				return true
			case <-ctx.Done():
				return false
			}
		}
		switch {
		case strings.Contains(string(payload), "spawn-1"):
			// Parent follow-up call after the spawn tool result.
			// Wait until the child has entered its blocking provider call. Without
			// this ordering barrier, a fast parent can complete before the async
			// child is registered, which makes this regression test race rather
			// than exercise the late-terminal delivery path.
			select {
			case <-p.childStarted:
			case <-ctx.Done():
				return
			}
			send(provider.StreamEvent{Type: provider.StreamStart})
			send(provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: "parent result"})
			send(provider.StreamEvent{Type: provider.StreamDone, StopReason: "stop"})
		case strings.Contains(string(payload), "inspect the project"):
			// The spawned child agent's provider call: only unblocks when its run
			// context is canceled at the end of the parent run.
			p.childOnce.Do(func() { close(p.childStarted) })
			<-ctx.Done()
		default:
			send(provider.StreamEvent{Type: provider.StreamStart})
			send(provider.StreamEvent{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{
				ID: "spawn-1", Name: "subagent_spawn", Arguments: []byte(`{"task":"inspect the project"}`),
			}})
			send(provider.StreamEvent{Type: provider.StreamDone, StopReason: "tool_calls"})
		}
	}()
	return ch
}

func (p *blockingChildChannelProvider) GetModel(id string) *provider.Model {
	if p.model != nil && p.model.ID == id {
		return p.model
	}
	return p.model
}

func (p *blockingChildChannelProvider) Models() []*provider.Model { return []*provider.Model{p.model} }
func (p *blockingChildChannelProvider) Name() string              { return "blocking-child" }
func (p *blockingChildChannelProvider) API() string               { return "openai-chat" }

// TestSubAgentTerminalEventDeliveredAfterParentStreamClose is the regression
// test for the lost-terminal-event race: the child agent's terminal transition
// happens after the parent run closed its event stream (here: the child is
// canceled when the run ends). The stream-forwarded copy is dropped in that
// case, so the terminal state must arrive through the AgentManager status
// listener instead of leaving observers stuck on "running" forever.
func TestSubAgentTerminalEventDeliveredAfterParentStreamClose(t *testing.T) {
	workDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	cfg := DefaultConfig()
	cfg.WorkDir = workDir
	cfg.MultiAgent = true
	model := &provider.Model{ID: "m1", ContextWindow: 32768, MaxTokens: 1024}
	p := &blockingChildChannelProvider{model: model, childStarted: make(chan struct{})}
	d := &Dispatcher{
		cfg: cfg, settings: settings, allow: &config.AllowConfig{}, sessionDir: settings.SessionDir,
		security: NewSecurity(cfg), hooksMgr: hooks.NewManager("", ""), provider: p, model: model, multiAgent: true,
		sessions: make(map[string]*ChannelSession), identityLocks: session.NewIdentityLocks(),
		agentSessions: make(map[string]string),
	}
	type observed struct {
		sessionID string
		event     agent.Event
	}
	observedCh := make(chan observed, 32)
	d.SetSubAgentObserver(func(sessionID string, ev agent.Event) {
		observedCh <- observed{sessionID: sessionID, event: ev}
	})

	if _, err := d.HandleMessage(context.Background(), messaging.InboundMessage{
		Platform: "wechat", UserID: "sender", ChatID: "channel-chat", Text: "delegate this",
	}); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	sess, err := d.resolveSession("wechat", "channel-chat")
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	sessionID := sess.Manager.GetHeader().ID

	deadline := time.After(3 * time.Second)
	for {
		select {
		case item := <-observedCh:
			// The canonical terminal event is EventRunFinished; legacy observers may
			// still receive EventDone/EventError.
			if item.event.Type != agent.EventDone && item.event.Type != agent.EventError && item.event.Type != agent.EventRunFinished {
				continue
			}
			if item.event.Type == agent.EventRunFinished && !item.event.Status.IsTerminal() {
				t.Fatalf("terminal event with non-terminal status %q", item.event.Status)
			}
			if item.sessionID != sessionID {
				t.Fatalf("observer session ID = %q, want %q", item.sessionID, sessionID)
			}
			if item.event.AgentID == "" {
				t.Fatal("terminal event without child agent ID")
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for child terminal event delivered after parent stream close")
		}
	}
}
