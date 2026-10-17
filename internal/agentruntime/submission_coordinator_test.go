package agentruntime

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/oschina/mothx/internal/session"
)

func TestSubmissionCoordinatorRunsQueuedSubmissionsFIFO(t *testing.T) {
	c := NewSubmissionCoordinator()
	defer c.Close()
	started := make(chan string, 2)
	release := make(chan struct{})
	admit := func(context.Context) (func(), error) { return func() {}, nil }
	run := func(id string) SubmissionRunner {
		return func(ctx context.Context) error {
			started <- id
			if id == "a" {
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		}
	}
	a, err := c.Submit(context.Background(), "a", SubmitPolicy{QueueBehindLocal: true}, admit, run("a"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Submit(context.Background(), "b", SubmitPolicy{QueueBehindLocal: true}, admit, run("b"))
	if err != nil {
		t.Fatal(err)
	}
	if got := <-started; got != "a" {
		t.Fatalf("first started = %q, want a", got)
	}
	select {
	case got := <-started:
		t.Fatalf("second submission started early: %q", got)
	default:
	}
	close(release)
	if completion := <-a.Completed; completion.Error != nil {
		t.Fatalf("a completion error = %v", completion.Error)
	}
	if completion := <-b.Completed; completion.Error != nil {
		t.Fatalf("b completion error = %v", completion.Error)
	}
	if got := <-started; got != "b" {
		t.Fatalf("second started = %q, want b", got)
	}
}

func TestSubmissionCoordinatorRejectsNonQueuedLocalSubmission(t *testing.T) {
	c := NewSubmissionCoordinator()
	defer c.Close()
	block := make(chan struct{})
	first, err := c.Submit(context.Background(), "a", SubmitPolicy{}, func(context.Context) (func(), error) {
		return func() {}, nil
	}, func(ctx context.Context) error {
		<-block
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Submit(context.Background(), "b", SubmitPolicy{}, func(context.Context) (func(), error) {
		return func() {}, nil
	}, func(context.Context) error { return nil }); !errors.Is(err, ErrSubmissionLocalBusy) {
		t.Fatalf("second submit error = %v, want local busy", err)
	}
	close(block)
	<-first.Completed
}

func TestSubmissionCoordinatorCancelsQueuedSubmission(t *testing.T) {
	c := NewSubmissionCoordinator()
	defer c.Close()
	block := make(chan struct{})
	first, err := c.Submit(context.Background(), "a", SubmitPolicy{QueueBehindLocal: true}, func(context.Context) (func(), error) {
		return func() {}, nil
	}, func(ctx context.Context) error {
		select {
		case <-block:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Submit(context.Background(), "b", SubmitPolicy{QueueBehindLocal: true}, func(context.Context) (func(), error) {
		return func() {}, nil
	}, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	second.Cancel()
	completion := <-second.Completed
	if !errors.Is(completion.Error, ErrSubmissionCancelled) {
		t.Fatalf("queued completion error = %v, want cancelled", completion.Error)
	}
	close(block)
	<-first.Completed
}

func TestSubmissionCoordinatorCloseCancelsActiveAndQueued(t *testing.T) {
	c := NewSubmissionCoordinator()
	started := make(chan struct{})
	var once sync.Once
	first, err := c.Submit(context.Background(), "a", SubmitPolicy{QueueBehindLocal: true}, func(context.Context) (func(), error) {
		return func() {}, nil
	}, func(ctx context.Context) error {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Submit(context.Background(), "b", SubmitPolicy{QueueBehindLocal: true}, func(context.Context) (func(), error) {
		return func() {}, nil
	}, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	<-started
	c.Close()
	if completion := <-first.Completed; !errors.Is(completion.Error, context.Canceled) {
		t.Fatalf("active completion error = %v, want context canceled", completion.Error)
	}
	if completion := <-second.Completed; !errors.Is(completion.Error, ErrSubmissionCancelled) {
		t.Fatalf("queued completion error = %v, want submission cancelled", completion.Error)
	}
}

func TestSubmissionCoordinatorWaitForExternalRetriesOnWake(t *testing.T) {
	c := NewSubmissionCoordinator()
	defer c.Close()
	var attempts int
	ready := make(chan struct{})
	handle, err := c.Submit(context.Background(), "external", SubmitPolicy{WaitForExternal: true}, func(context.Context) (func(), error) {
		attempts++
		if attempts == 1 {
			return nil, session.ErrRuntimeLeaseBusy
		}
		return func() {}, nil
	}, func(context.Context) error {
		close(ready)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	c.Wake()
	<-ready
	if completion := <-handle.Completed; completion.Error != nil {
		t.Fatalf("completion error = %v", completion.Error)
	}
	if attempts != 2 {
		t.Fatalf("admission attempts = %d, want 2", attempts)
	}
}

func TestSubmissionCoordinatorMapsExternalBusyWithoutWait(t *testing.T) {
	c := NewSubmissionCoordinator()
	defer c.Close()
	handle, err := c.Submit(context.Background(), "external", SubmitPolicy{}, func(context.Context) (func(), error) {
		return nil, session.ErrRuntimeLeaseBusy
	}, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if completion := <-handle.Completed; !errors.Is(completion.Error, ErrSubmissionExternalBusy) {
		t.Fatalf("completion error = %v, want external busy", completion.Error)
	}
}

func TestSubmissionCoordinatorPublishesCanonicalStateSequence(t *testing.T) {
	c := NewSubmissionCoordinator()
	defer c.Close()
	started := make(chan struct{})
	release := make(chan struct{})
	handle, err := c.Submit(context.Background(), "state-run", SubmitPolicy{}, func(context.Context) (func(), error) {
		return func() {}, nil
	}, func(ctx context.Context) error {
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	close(release)
	completion := <-handle.Completed
	if completion.Error != nil {
		t.Fatalf("completion error = %v", completion.Error)
	}

	var states []SubmissionState
	for event := range handle.Events {
		states = append(states, event.State)
	}
	want := []SubmissionState{SubmissionAccepted, SubmissionAdmitting, SubmissionRunning, SubmissionCompleting, SubmissionCompleted}
	if len(states) != len(want) {
		t.Fatalf("state sequence = %v, want %v", states, want)
	}
	for i := range want {
		if states[i] != want[i] {
			t.Fatalf("state[%d] = %q, want %q; sequence=%v", i, states[i], want[i], states)
		}
	}
}
