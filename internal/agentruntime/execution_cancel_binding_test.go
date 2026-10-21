package agentruntime

import (
	"context"
	"sync"
	"testing"
	"time"
)

// abortRecorderAgent records Abort calls without depending on the Agent loop.
type abortRecorderAgent struct {
	once    sync.Once
	aborted chan struct{}
}

func newAbortRecorderAgent() *abortRecorderAgent {
	return &abortRecorderAgent{aborted: make(chan struct{})}
}

func (a *abortRecorderAgent) Abort() {
	a.once.Do(func() { close(a.aborted) })
}

func (a *abortRecorderAgent) wait(t *testing.T) {
	t.Helper()
	select {
	case <-a.aborted:
	case <-time.After(time.Second):
		t.Fatal("agent was not aborted")
	}
}

func (a *abortRecorderAgent) assertNotAborted(t *testing.T) {
	t.Helper()
	select {
	case <-a.aborted:
		t.Fatal("agent was aborted unexpectedly")
	default:
	}
}

// A stop request can arrive before the adapter binds the Agent to the
// ExecutionRuntime (queued Run, slow runtime assembly). Cancel cannot abort an
// agent that is not attached yet, so SetAgent must honor the pending
// cancellation instead of silently dropping it and letting the Run continue.
func TestExecutionRuntimeSetAgentHonorsCancellationRequestedBeforeBinding(t *testing.T) {
	store := &recordingDurableRunStore{}
	runtime := &ExecutionRuntime{}
	runtime.SetRunStore(store)
	if _, err := runtime.BeginDurable(context.Background(), DurableRun{ID: "run-early-cancel", SessionID: "session-early-cancel"}, RunEvent{EventType: "started"}); err != nil {
		t.Fatal(err)
	}

	cancelled, err := runtime.CancelDurable("run cancellation requested by user")
	if err != nil {
		t.Fatalf("CancelDurable: %v", err)
	}
	if !cancelled {
		t.Fatal("CancelDurable = false, want accepted")
	}

	agent := newAbortRecorderAgent()
	runtime.SetAgent(agent)
	agent.wait(t)
}

// The normal ordering must stay unchanged: binding a running Agent does not
// abort it, and a later cancel still reaches it.
func TestExecutionRuntimeSetAgentThenCancelAbortsAgent(t *testing.T) {
	store := &recordingDurableRunStore{}
	runtime := &ExecutionRuntime{}
	runtime.SetRunStore(store)
	if _, err := runtime.BeginDurable(context.Background(), DurableRun{ID: "run-late-cancel", SessionID: "session-late-cancel"}, RunEvent{EventType: "started"}); err != nil {
		t.Fatal(err)
	}

	agent := newAbortRecorderAgent()
	runtime.SetAgent(agent)
	agent.assertNotAborted(t)

	if !runtime.Cancel() {
		t.Fatal("Cancel = false, want accepted")
	}
	agent.wait(t)
}
