package agentruntime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// terminalizingRunStore models a store whose terminal write fails once after
// the Run has already been marked terminalizing, then succeeds on the retry.
type terminalizingRunStore struct {
	mu            sync.Mutex
	updates       []RunState
	finishes      []RunState
	failNext      bool
	terminalizing bool
}

func (s *terminalizingRunStore) Create(DurableRun) error {
	return nil
}

func (s *terminalizingRunStore) Update(_ string, state RunState, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Mirror the canonical transition matrix: a run that already entered the
	// committed terminalizing window cannot be moved back to cancelling.
	if s.terminalizing && state == RunStateCancelling {
		return errors.New("invalid session run transition \"terminalizing\" -> \"cancelling\"")
	}
	s.updates = append(s.updates, state)
	return nil
}

func (s *terminalizingRunStore) Finish(_ string, state RunState, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finishes = append(s.finishes, state)
	if s.failNext {
		s.failNext = false
		return errors.New("run store unavailable")
	}
	return nil
}

// MarkTerminalizing makes this store a DurableTerminalPersistenceStore, so the
// durable row is committed to the non-terminal terminalizing window before the
// final write.
func (s *terminalizingRunStore) MarkTerminalizing(string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.terminalizing = true
	return nil
}

func (s *terminalizingRunStore) UpdateStates() []RunState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]RunState(nil), s.updates...)
}

func (s *terminalizingRunStore) FinishStates() []RunState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]RunState(nil), s.finishes...)
}

// A terminal write that fails after the Run was marked terminalizing leaves the
// durable row in the committed, non-terminal terminalizing window while the run
// stays active. Cancelling must not force the (rejected) terminalizing ->
// cancelling transition; it must accept the cancellation and let the selected
// terminal write finish.
func TestExecutionRuntimeCancelDurableAcceptsPreparedTerminalizing(t *testing.T) {
	store := &terminalizingRunStore{failNext: true}
	var runtime ExecutionRuntime
	runtime.SetRunStore(store)
	if _, err := runtime.BeginDurable(context.Background(), DurableRun{ID: "run-terminalizing", SessionID: "session-1"}, RunEvent{EventType: "started"}); err != nil {
		t.Fatal(err)
	}

	if err := runtime.FinishDurable("run-terminalizing", RunStateCompleted, "", RunEvent{EventType: "finished"}); err == nil {
		t.Fatal("first terminal write unexpectedly succeeded")
	}
	if _, active := runtime.Active(); !active {
		t.Fatal("failed terminal write released the active run")
	}

	cancelled, err := runtime.CancelDurable("run cancellation requested by user")
	if err != nil {
		t.Fatalf("CancelDurable after terminalizing = %v, want no error", err)
	}
	if !cancelled {
		t.Fatal("CancelDurable after terminalizing = false, want accepted")
	}
	for _, state := range store.UpdateStates() {
		if state == RunStateCancelling {
			t.Fatalf("cancelling transition attempted after terminalizing: %#v", store.UpdateStates())
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, active := runtime.Active(); !active {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, active := runtime.Active(); active {
		t.Fatal("run stayed active after cancellation restarted terminal persistence")
	}
	states := store.FinishStates()
	if len(states) < 2 || states[len(states)-1] != RunStateCompleted {
		t.Fatalf("store finishes = %#v, want a final completed transition", states)
	}
}
