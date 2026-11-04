package agentruntime

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/tools"
)

type mailboxWakeTestProvider struct {
	mu    sync.Mutex
	calls []provider.ChatParams
	model *provider.Model
}

func (p *mailboxWakeTestProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	p.mu.Lock()
	p.calls = append(p.calls, params)
	p.mu.Unlock()
	ch := make(chan provider.StreamEvent, 4)
	go func() {
		defer close(ch)
		for _, event := range []provider.StreamEvent{
			{Type: provider.StreamStart},
			{Type: provider.StreamTextDelta, TextDelta: "wake answer"},
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

func (p *mailboxWakeTestProvider) Name() string { return "mailbox-test" }
func (p *mailboxWakeTestProvider) API() string  { return "mock" }
func (p *mailboxWakeTestProvider) Models() []*provider.Model {
	return []*provider.Model{p.model}
}
func (p *mailboxWakeTestProvider) GetModel(id string) *provider.Model {
	if id == p.model.ID {
		return p.model
	}
	return nil
}

func (p *mailboxWakeTestProvider) snapshot() []provider.ChatParams {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provider.ChatParams(nil), p.calls...)
}

func newMailboxWakeFixture(t *testing.T) (*SessionRuntime, *mailboxWakeTestProvider, string) {
	t.Helper()
	sessionDir := t.TempDir()
	workDir := t.TempDir()
	manager := session.New(workDir, sessionDir)
	if err := manager.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	registry := tools.NewRegistry(workDir, sandbox.NewNoneSandbox())
	runtime, err := AttachSessionResources(AttachedResources{
		ID: manager.GetHeader().ID, Source: SourceTUI, WorkDir: workDir,
		Manager: manager, Registry: registry, Settings: config.DefaultSettings(),
	})
	if err != nil {
		t.Fatalf("attach session resources: %v", err)
	}
	t.Cleanup(runtime.Close)
	testProvider := &mailboxWakeTestProvider{model: &provider.Model{ID: "mailbox-model", Name: "Mailbox Model"}}
	runtime.Provider = testProvider
	runtime.ProviderName = testProvider.Name()
	runtime.Model = testProvider.model
	runtime.Mode = ModeYolo
	execution := &ExecutionRuntime{}
	execution.SetRunStore(RunStore{SessionDir: sessionDir})
	execution.SetEventSink(SessionRunEventSink{SessionDir: sessionDir})
	runtime.SetExecution(execution)
	return runtime, testProvider, manager.GetHeader().ID
}

func TestMailboxWakeCoalescesCompletionsIntoOneCanonicalRun(t *testing.T) {
	runtime, testProvider, sessionID := newMailboxWakeFixture(t)
	started := make(chan MailboxWakeRun, 4)
	finished := make(chan struct {
		run   MailboxWakeRun
		state RunState
		err   error
	}, 4)
	if err := runtime.EnableMailboxWake(context.Background(), MailboxWakeOptions{
		Debounce: 5 * time.Millisecond,
		OnStarted: func(run MailboxWakeRun) {
			started <- run
		},
		OnFinished: func(run MailboxWakeRun, state RunState, err error) {
			finished <- struct {
				run   MailboxWakeRun
				state RunState
				err   error
			}{run: run, state: state, err: err}
		},
	}); err != nil {
		t.Fatalf("enable mailbox wake: %v", err)
	}
	t.Cleanup(runtime.DisableMailboxWake)

	runtime.Mailbox.Enqueue(agent.MemberCompletion{MemberID: "worker-a", Status: agent.MemberStatusDone, Payload: "first result", External: true})
	runtime.Mailbox.Enqueue(agent.MemberCompletion{MemberID: "worker-b", Status: agent.MemberStatusError, Payload: "second result", External: true})

	var runInfo MailboxWakeRun
	select {
	case runInfo = <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("mailbox wake did not start")
	}
	if runInfo.SessionID != sessionID || runInfo.RunID == "" || runInfo.Source != string(SourceTUI) {
		t.Fatalf("wake identity = %#v", runInfo)
	}
	select {
	case result := <-finished:
		if result.err != nil || result.state != RunStateCompleted {
			t.Fatalf("wake result = state %s err %v", result.state, result.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("mailbox wake did not finish")
	}

	select {
	case extra := <-started:
		t.Fatalf("coalesced completions started another wake: %#v", extra)
	default:
	}
	if runtime.Mailbox.HasPending() {
		t.Fatal("mailbox still has pending completions after the wake")
	}
	calls := testProvider.snapshot()
	if len(calls) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(calls))
	}
	seedFound := false
	for _, message := range calls[0].Messages {
		if message.Role == "user" && message.SystemInjected &&
			strings.Contains(message.Content, "first result") && strings.Contains(message.Content, "second result") {
			seedFound = true
		}
	}
	if !seedFound {
		t.Fatalf("wake did not pass one system-injected continuation with both completions: %#v", calls[0].Messages)
	}

	manager := runtime.Manager
	if manager == nil {
		t.Fatal("runtime manager is nil")
	}
	if err := manager.Reload(); err != nil {
		t.Fatalf("reload session: %v", err)
	}
	state := manager.GetReplayState()
	assistantText := ""
	if len(state.Messages) == 1 && len(state.Messages[0].Contents) == 1 {
		assistantText = state.Messages[0].Contents[0].Text
	}
	if len(state.Messages) != 1 || state.Messages[0].Role != "assistant" || assistantText != "wake answer" {
		t.Fatalf("wake transcript = %#v, want one persisted assistant answer and no fake user entry", state.Messages)
	}
}

func TestMailboxWakeDoesNotStartWithoutPendingCompletions(t *testing.T) {
	runtime, testProvider, _ := newMailboxWakeFixture(t)
	started := make(chan MailboxWakeRun, 1)
	if err := runtime.EnableMailboxWake(context.Background(), MailboxWakeOptions{
		Debounce: time.Millisecond,
		OnStarted: func(run MailboxWakeRun) {
			started <- run
		},
	}); err != nil {
		t.Fatalf("enable mailbox wake: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	if calls := testProvider.snapshot(); len(calls) != 0 {
		t.Fatalf("provider calls without a completion = %d, want 0", len(calls))
	}
	select {
	case run := <-started:
		t.Fatalf("wake started without a pending completion: %#v", run)
	default:
	}
}
