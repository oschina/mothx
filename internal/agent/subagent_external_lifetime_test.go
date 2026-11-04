package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	agentpkg "github.com/oschina/mothx/agent"
)

// ctxAwareChildBackend returns a child that honours its run context, so the
// test can observe whether the spawned worker was cancelled with its parent.
type ctxAwareChildBackend struct {
	mu    sync.Mutex
	calls int
	opts  AgentOptions
	child *ctxAwareChild
}

func (b *ctxAwareChildBackend) CreateChild(opts AgentOptions) (agentpkg.Agent, bool, error) {
	if opts.ExternalAgent == "" {
		return nil, false, nil
	}
	b.mu.Lock()
	b.calls++
	b.opts = opts
	child := newCtxAwareChild(opts.ID, opts.ParentID, b)
	b.child = child
	b.mu.Unlock()
	return child, true, nil
}

// startedChild returns the created child once it exists. The backend may be
// called from the tool's own goroutine, so the test must not read the field
// before CreateChild ran.
func (b *ctxAwareChildBackend) startedChild(t *testing.T) *ctxAwareChild {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b.mu.Lock()
		child := b.child
		b.mu.Unlock()
		if child != nil {
			return child
		}
		if time.Now().After(deadline) {
			t.Fatal("child backend was never called")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ctxAwareChild runs until its context is cancelled. It reports the
// cancellation cause so the test can tell "cancelled by the parent run" from
// "still running when the parent finished".
type ctxAwareChild struct {
	id      agentpkg.AgentID
	parent  agentpkg.AgentID
	backend *ctxAwareChildBackend

	started   chan struct{}
	cancelled chan error
}

func newCtxAwareChild(id agentpkg.AgentID, parent agentpkg.AgentID, backend *ctxAwareChildBackend) *ctxAwareChild {
	return &ctxAwareChild{
		id:        id,
		parent:    parent,
		backend:   backend,
		started:   make(chan struct{}),
		cancelled: make(chan error, 1),
	}
}

func (a *ctxAwareChild) ID() agentpkg.AgentID       { return a.id }
func (a *ctxAwareChild) ParentID() agentpkg.AgentID { return a.parent }
func (a *ctxAwareChild) Abort()                     {}
func (a *ctxAwareChild) GetMessages() []agentpkg.Message {
	return nil
}
func (a *ctxAwareChild) SetMessages([]agentpkg.Message)          {}
func (a *ctxAwareChild) GetContext() *agentpkg.AgentContext      { return nil }
func (a *ctxAwareChild) SetContext(*agentpkg.AgentContext)       {}
func (a *ctxAwareChild) GetContextUsage() *agentpkg.ContextUsage { return nil }
func (a *ctxAwareChild) LoadHistoryMessages([]agentpkg.Message)  {}
func (a *ctxAwareChild) HandleApprovalResponse(string, bool)     {}

func (a *ctxAwareChild) Run(ctx context.Context, _ string) <-chan agentpkg.Event {
	events := make(chan agentpkg.Event, 4)
	go func() {
		defer close(events)
		close(a.started)
		<-ctx.Done()
		err := ctx.Err()
		a.cancelled <- err
		events <- agentpkg.Event{Type: agentpkg.EventRunFinished, Status: agentpkg.TaskCanceled, Error: err}
	}()
	return events
}

func (a *ctxAwareChild) RunWithMessages(ctx context.Context, _ []agentpkg.Message) <-chan agentpkg.Event {
	return a.Run(ctx, "")
}

// TestRouterSpawnedExternalWorkerSurvivesParentRun proves an asynchronously
// spawned external worker outlives the lead turn that created it.
//
// The spawned worker's context used to be derived from the lead's run context,
// and that context is cancelled when the lead run reaches its terminal state.
// Every spawned worker was therefore cancelled the instant the lead finished
// its own turn, so a fan-out never ran to completion. The worker must instead
// keep running until it reaches its own terminal state or is explicitly
// destroyed.
func TestRouterSpawnedExternalWorkerSurvivesParentRun(t *testing.T) {
	manager := NewAgentManager(nil)
	manager.Register(&stubBackendAgent{id: "lead"})
	backend := &ctxAwareChildBackend{}
	manager.SetChildBackend(backend)
	tool := NewSubAgentSpawnTool(manager)
	tool.SetRouterTarget(&RouterTarget{ID: "opencode"})

	// The parent run context is cancelled when the lead run terminalizes.
	parentCtx, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	ctx := ContextWithParentMode(parentCtx, "router")
	ctx = ContextWithAgentID(ctx, "lead")
	ctx = ContextWithParentRunContext(ctx, parentCtx)

	result, err := tool.Execute(ctx, map[string]any{
		"task":  "inspect the retry path",
		"agent": "opencode",
		"role":  "investigate",
	})
	if err != nil {
		t.Fatalf("spawn external worker: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(result.Text), &parsed); err != nil {
		t.Fatalf("parse spawn result: %v", err)
	}
	handle, _ := parsed["handle"].(string)
	if handle == "" {
		t.Fatal("spawn result has no handle")
	}

	// Wait until the worker is actually running.
	child := backend.startedChild(t)
	select {
	case <-child.started:
	case <-time.After(5 * time.Second):
		t.Fatal("spawned worker never started")
	}

	// The lead run reaches its terminal state, which cancels the parent run
	// context exactly like ExecutionRuntime.Finish does.
	cancelParent()

	// The spawned worker must not be cancelled by that.
	select {
	case err := <-child.cancelled:
		t.Fatalf("spawned worker was cancelled with its parent: %v", err)
	case <-time.After(500 * time.Millisecond):
	}

	// Destroying the worker must still reach it, so it stays cancellable.
	if err := manager.Destroy(agentpkg.AgentID(handle)); err != nil {
		t.Fatalf("destroy spawned worker: %v", err)
	}
}

// TestRouterBlockingDelegateStillEndsWithParent proves the fix did not change
// the blocking delegation path: a blocking delegate is meant to finish with
// the lead turn that called it.
func TestRouterBlockingDelegateStillEndsWithParent(t *testing.T) {
	manager := NewAgentManager(nil)
	manager.Register(&stubBackendAgent{id: "lead"})
	backend := &ctxAwareChildBackend{}
	manager.SetChildBackend(backend)
	tool := NewDelegateSubAgentTool(manager)
	tool.SetRouterTarget(&RouterTarget{ID: "opencode"})

	parentCtx, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	ctx := ContextWithParentMode(parentCtx, "router")
	ctx = ContextWithAgentID(ctx, "lead")
	ctx = ContextWithParentRunContext(ctx, parentCtx)

	done := make(chan error, 1)
	resultCh := make(chan string, 1)
	go func() {
		result, err := tool.Execute(ctx, map[string]any{
			"task":  "inspect the retry path",
			"agent": "opencode",
			"role":  "investigate",
		})
		if err == nil {
			resultCh <- result.Text
		}
		done <- err
	}()

	select {
	case <-backend.startedChild(t).started:
	case <-time.After(5 * time.Second):
		t.Fatal("blocking delegate never started")
	}

	// The lead turn ends; the blocking delegate must be released with it.
	cancelParent()
	select {
	case err := <-done:
		if err != nil {
			if !strings.Contains(err.Error(), "context canceled") &&
				!errors.Is(err, context.Canceled) {
				t.Fatalf("blocking delegate error = %v, want a cancellation", err)
			}
			return
		}
		// A delegated failure is reported as evidence inside the tool result,
		// not as a Go error: the lead must be able to read what happened.
		var parsed map[string]any
		if err := json.Unmarshal([]byte(<-resultCh), &parsed); err != nil {
			t.Fatalf("parse delegate result: %v", err)
		}
		if parsed["status"] != "canceled" && parsed["status"] != "error" {
			t.Fatalf("blocking delegate status = %v, want canceled or error after cancellation", parsed["status"])
		}
	case <-time.After(10 * time.Second):
		t.Fatal("blocking delegate did not end with its parent turn")
	}
}
