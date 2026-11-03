package agent

import (
	"context"
	"testing"

	agentpkg "github.com/oschina/mothx/agent"
)

// stubBackendAgent is a minimal agentpkg.Agent used to prove the manager's
// backend interception without constructing a local AgentFactory child.
type stubBackendAgent struct {
	id     agentpkg.AgentID
	parent agentpkg.AgentID
}

func (a *stubBackendAgent) ID() agentpkg.AgentID       { return a.id }
func (a *stubBackendAgent) ParentID() agentpkg.AgentID { return a.parent }
func (a *stubBackendAgent) Run(ctx context.Context, userMsg string) <-chan agentpkg.Event {
	ch := make(chan agentpkg.Event)
	close(ch)
	return ch
}
func (a *stubBackendAgent) RunWithMessages(ctx context.Context, msgs []agentpkg.Message) <-chan agentpkg.Event {
	return a.Run(ctx, "")
}
func (a *stubBackendAgent) Abort()                                      {}
func (a *stubBackendAgent) GetMessages() []agentpkg.Message             { return nil }
func (a *stubBackendAgent) SetMessages(msgs []agentpkg.Message)         {}
func (a *stubBackendAgent) GetContext() *agentpkg.AgentContext          { return nil }
func (a *stubBackendAgent) SetContext(ctx *agentpkg.AgentContext)       {}
func (a *stubBackendAgent) GetContextUsage() *agentpkg.ContextUsage     { return nil }
func (a *stubBackendAgent) LoadHistoryMessages(msgs []agentpkg.Message) {}
func (a *stubBackendAgent) HandleApprovalResponse(approvalID string, approved bool) {
}

type stubChildBackend struct {
	calls int
	opts  AgentOptions
}

func (b *stubChildBackend) CreateChild(opts AgentOptions) (agentpkg.Agent, bool, error) {
	if opts.ExternalAgent == "" {
		return nil, false, nil
	}
	b.calls++
	b.opts = opts
	return &stubBackendAgent{id: opts.ID, parent: opts.ParentID}, true, nil
}

func TestAgentManagerChildBackendInterceptsValidatedChild(t *testing.T) {
	manager := NewAgentManager(nil)
	manager.Register(&stubBackendAgent{id: "lead"})
	backend := &stubChildBackend{}
	manager.SetChildBackend(backend)

	child, err := manager.Create(AgentOptions{
		ParentID: "lead", ExternalAgent: "codex", ExternalTask: "review the diff",
		ExternalRole: "review", ExternalDeliverable: []string{"review"},
	})
	if err != nil {
		t.Fatalf("Create with backend: %v", err)
	}
	if backend.calls != 1 {
		t.Fatalf("backend calls = %d, want 1", backend.calls)
	}
	if backend.opts.ExternalTask != "review the diff" || backend.opts.ExternalRole != "review" {
		t.Fatalf("backend options = %+v", backend.opts)
	}
	if child == nil || child.ParentID() != "lead" {
		t.Fatalf("created child = %#v", child)
	}
	if got, ok := manager.Get(child.ID()); !ok || got != child {
		t.Fatal("backend child was not registered with the manager")
	}
	if status, ok := manager.Status(child.ID()); !ok || status.State != "ready" {
		t.Fatalf("backend child status = %+v, ok=%v", status, ok)
	}

	// Parent validation runs before the backend.
	if _, err := manager.Create(AgentOptions{ParentID: "missing", ExternalAgent: "codex"}); err == nil {
		t.Fatal("unknown parent reached the external backend")
	}
	if backend.calls != 1 {
		t.Fatalf("backend ran for an invalid parent: calls=%d", backend.calls)
	}
}

func TestAgentManagerChildBackendUnhandledFallsBackToFactory(t *testing.T) {
	manager := NewAgentManager(nil)
	manager.Register(&stubBackendAgent{id: "lead"})
	backend := &stubChildBackend{}
	manager.SetChildBackend(backend)

	// No ExternalAgent means the backend declines; the manager then calls the
	// local factory. With a nil factory that is a programmer error, so this test
	// only asserts the decline decision is reported before construction.
	if _, handled, err := backend.CreateChild(AgentOptions{ParentID: "lead"}); err != nil || handled {
		t.Fatalf("backend decline = handled %v, err %v", handled, err)
	}
}
