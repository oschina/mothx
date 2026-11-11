package openaiapi

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/provider"
)

type serveWakeProvider struct {
	mu    sync.Mutex
	calls []provider.ChatParams
	model *provider.Model
}

func (p *serveWakeProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	p.mu.Lock()
	p.calls = append(p.calls, params)
	p.mu.Unlock()
	ch := make(chan provider.StreamEvent, 4)
	go func() {
		defer close(ch)
		for _, event := range []provider.StreamEvent{
			{Type: provider.StreamStart},
			{Type: provider.StreamTextDelta, TextDelta: "worker finding acknowledged"},
			{Type: provider.StreamDone, StopReason: "stop"},
		} {
			select {
			case <-ctx.Done():
				return
			case ch <- event:
			}
		}
	}()
	return ch
}

func (p *serveWakeProvider) Name() string { return "serve-wake-test" }
func (p *serveWakeProvider) API() string  { return "mock" }
func (p *serveWakeProvider) Models() []*provider.Model {
	return []*provider.Model{p.model}
}
func (p *serveWakeProvider) GetModel(id string) *provider.Model {
	if p.model != nil && id == p.model.ID {
		return p.model
	}
	return nil
}
func (p *serveWakeProvider) snapshot() []provider.ChatParams {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provider.ChatParams(nil), p.calls...)
}

// A Router worker finding must wake an idle Serve/WebUI lead into a canonical
// Run and project the Runtime's started/finished events plus the stream done.
// This is the cross-entry counterpart of the TUI/ACP wake schedulers.
func TestServeMailboxWakeStartsCanonicalRunForFinding(t *testing.T) {
	assertServeMailboxWake(t, "serve-wake-finding", agent.MemberCompletion{
		Kind:        agent.MemberItemFinding,
		MemberID:    "worker-a",
		DisplayName: "ACP worker",
		Status:      agent.MemberStatusFinding,
		Payload:     "[WORKER_FINDING] task complete",
		External:    true,
		Wake:        true,
	}, "task complete")
}

// A worker question uses the same Wake=true mailbox path and must therefore
// also start a canonical lead Run on an idle Serve/WebUI session.
func TestServeMailboxWakeStartsCanonicalRunForQuestion(t *testing.T) {
	assertServeMailboxWake(t, "serve-wake-question", agent.MemberCompletion{
		Kind:        agent.MemberItemQuestion,
		MemberID:    "worker-a",
		DisplayName: "ACP worker",
		Status:      agent.MemberStatusQuestion,
		Payload:     "[WORKER_QUESTION] which file should I edit?",
		QuestionID:  "question-1",
		Options:     []string{"a.go", "b.go"},
		External:    true,
		Wake:        true,
	}, "which file should I edit?")
}

func assertServeMailboxWake(t *testing.T, sessionID string, completion agent.MemberCompletion, seedSubstring string) {
	t.Helper()
	srv := newTestServer(t)
	defer srv.pool.Stop()

	testProvider := &serveWakeProvider{model: &provider.Model{ID: "serve-wake-model", Name: "Serve Wake Model", ContextWindow: 128000}}
	srv.provider = testProvider
	srv.model = testProvider.model
	srv.providerName = testProvider.Name()

	sess, err := srv.getOrCreateSession(sessionID, srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("getOrCreateSession: %v", err)
	}
	if sess.Runtime == nil || sess.Runtime.Mailbox == nil {
		t.Fatal("session runtime mailbox was not initialized")
	}
	if got, _, _, _, _ := sess.Runtime.ConfigSnapshot(); got == nil {
		t.Fatal("session runtime provider was not configured for Runtime-owned continuation")
	}

	events, cancel := srv.getEventBroker().Subscribe(sess.ID)
	defer cancel()

	sess.Runtime.Mailbox.Enqueue(completion)

	var (
		startedRun  string
		sawStarted  bool
		sawFinished bool
		sawDone     bool
	)
	deadline := time.After(10 * time.Second)
	for !(sawStarted && sawFinished && sawDone) {
		select {
		case event := <-events:
			switch {
			case event.Stream == "run":
				entry, ok := event.Data.(SessionRunEventEntry)
				if !ok {
					continue
				}
				switch entry.EventType {
				case "started":
					startedRun = entry.RunID
					sawStarted = entry.RunID != ""
				case "finished":
					if startedRun == "" || entry.RunID == startedRun {
						sawFinished = entry.RunID != "" && entry.Status == "completed"
					}
				}
			case event.Stream == "control" && event.Event == "done":
				sawDone = startedRun != "" && event.RunID == startedRun
			}
		case <-deadline:
			t.Fatalf("wake did not emit a complete canonical Run projection: started=%t finished=%t done=%t run=%q", sawStarted, sawFinished, sawDone, startedRun)
		}
	}

	calls := testProvider.snapshot()
	if len(calls) != 1 {
		t.Fatalf("provider calls = %d, want exactly one wake Run", len(calls))
	}
	seedFound := false
	for _, message := range calls[0].Messages {
		if message.SystemInjected && strings.Contains(message.Content, seedSubstring) {
			seedFound = true
			break
		}
	}
	if !seedFound {
		t.Fatalf("wake Run did not receive the mailbox payload as the continuation seed: %#v", calls[0].Messages)
	}
	if sess.Runtime.Mailbox.HasPending() {
		t.Fatal("mailbox still has pending items after the wake Run")
	}
}
