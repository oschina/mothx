package agentruntime

import (
	"context"
	"testing"
	"time"

	coreagent "github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/tools"
)

func TestExecutionRuntimeExclusiveBeginAndFinish(t *testing.T) {
	var runtime ExecutionRuntime
	ctx, err := runtime.Begin(context.Background(), "run-1")
	if err != nil || ctx == nil {
		t.Fatalf("Begin: ctx=%v err=%v", ctx, err)
	}
	if _, err := runtime.Begin(context.Background(), "run-2"); err == nil {
		t.Fatal("second Begin succeeded while run is active")
	}
	if id, active := runtime.Active(); !active || id != "run-1" {
		t.Fatalf("Active() = %q, %v", id, active)
	}
	if !runtime.Cancel() {
		t.Fatal("Cancel returned false for active run")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("Cancel did not cancel run context")
	}
	runtime.Finish("run-1")
	if _, active := runtime.Active(); active {
		t.Fatal("run stayed active after Finish")
	}
	if got := runtime.State(); got != RunStateCompleted {
		t.Fatalf("State() after Finish = %q, want %q", got, RunStateCompleted)
	}
	if _, err := runtime.Begin(context.Background(), "run-2"); err != nil {
		t.Fatalf("Begin after Finish: %v", err)
	}
}

func TestExecutionRuntimeCancelNeedsExplicitTerminalState(t *testing.T) {
	var runtime ExecutionRuntime
	ctx, err := runtime.Begin(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if !runtime.Cancel() {
		t.Fatal("Cancel returned false")
	}
	if got := runtime.State(); got != RunStateCancelling {
		t.Fatalf("State() after Cancel = %q, want %q", got, RunStateCancelling)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("Cancel did not cancel context")
	}
	if err := runtime.FinishWithState("run-1", RunStateCancelled); err != nil {
		t.Fatalf("FinishWithState: %v", err)
	}
	if got := runtime.State(); got != RunStateCancelled {
		t.Fatalf("terminal State() = %q, want %q", got, RunStateCancelled)
	}
}

func TestExecutionRuntimeWaitAndResume(t *testing.T) {
	var runtime ExecutionRuntime
	if _, err := runtime.Begin(context.Background(), "run-1"); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if got := runtime.State(); got != RunStateRunning {
		t.Fatalf("initial State() = %q", got)
	}
	if err := runtime.WaitForApproval("run-1"); err != nil {
		t.Fatalf("WaitForApproval: %v", err)
	}
	if got := runtime.State(); got != RunStateWaitingApproval {
		t.Fatalf("approval State() = %q", got)
	}
	if err := runtime.Resume("run-1"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if got := runtime.State(); got != RunStateRunning {
		t.Fatalf("resumed State() = %q", got)
	}
	if err := runtime.WaitForQuestion("other"); err == nil {
		t.Fatal("WaitForQuestion accepted a different run ID")
	}
	runtime.Finish("run-1")
}

func TestExecutionRuntimeExplicitTerminalStates(t *testing.T) {
	var runtime ExecutionRuntime
	if _, err := runtime.Begin(context.Background(), "run-1"); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := runtime.FinishWithState("run-1", RunStateFailed); err != nil {
		t.Fatalf("FinishWithState: %v", err)
	}
	if got := runtime.State(); got != RunStateFailed {
		t.Fatalf("State() = %q, want %q", got, RunStateFailed)
	}
	if err := runtime.FinishWithState("run-1", RunStateCompleted); err == nil {
		t.Fatal("FinishWithState succeeded after the run was already terminal")
	}
}

func TestExecutionRuntimeFinishIgnoresDifferentRun(t *testing.T) {
	var runtime ExecutionRuntime
	if _, err := runtime.Begin(context.Background(), "run-1"); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	runtime.Finish("other")
	if id, active := runtime.Active(); !active || id != "run-1" {
		t.Fatalf("wrong Finish cleared active run: %q, %v", id, active)
	}
}

// TestExecutionRuntimeShutdownReleasesLifecycleLockOnConcurrentTerminalization
// guards the !hasRunner shutdown branch. When another path terminalizes the run
// between the Active() snapshot and the lifecycle lock, ShutdownContext must
// release transitionMu before returning: a leak blocks every later Begin,
// FinishDurable, and CancelDurable on this runtime for the life of the process.
func TestExecutionRuntimeShutdownReleasesLifecycleLockOnConcurrentTerminalization(t *testing.T) {
	for iteration := 0; iteration < 25; iteration++ {
		var runtime ExecutionRuntime
		if _, err := runtime.Begin(context.Background(), "run-race"); err != nil {
			t.Fatalf("Begin: %v", err)
		}
		// Hold the lifecycle lock so the shutdown goroutine samples the run as
		// active, then parks on transitionMu while we simulate the competing
		// terminal transition.
		runtime.transitionMu.Lock()
		shutdownDone := make(chan error, 1)
		go func() {
			shutdownDone <- runtime.ShutdownContext(context.Background(), "shutdown requested")
		}()
		time.Sleep(20 * time.Millisecond)
		runtime.mu.Lock()
		runtime.finished = true
		runtime.mu.Unlock()
		runtime.transitionMu.Unlock()

		select {
		case err := <-shutdownDone:
			if err != nil {
				t.Fatalf("ShutdownContext: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("ShutdownContext did not return (iteration %d)", iteration)
		}
		if !runtime.transitionMu.TryLock() {
			t.Fatalf("ShutdownContext leaked transitionMu with the run already terminal (iteration %d)", iteration)
		}
		runtime.transitionMu.Unlock()

		// The runtime must remain usable for the next admission.
		if _, err := runtime.Begin(context.Background(), "run-next"); err != nil {
			t.Fatalf("Begin after shutdown race: %v", err)
		}
		if !runtime.Cancel() {
			t.Fatal("Cancel after shutdown race returned false")
		}
		runtime.Finish("run-next")
	}
}

func TestExecutionRuntimeShutdownWaitsForBoundAgentLoop(t *testing.T) {
	var runtime ExecutionRuntime
	ctx, err := runtime.Begin(context.Background(), "run-shutdown")
	if err != nil {
		t.Fatal(err)
	}
	runtime.SetAgent(coreagent.New(coreagent.Config{}, tools.NewRegistry(t.TempDir(), nil)))
	release := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		<-ctx.Done()
		<-release
		_ = runtime.FinishWithState("run-shutdown", RunStateCancelled)
		close(finished)
	}()

	deadline, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := runtime.ShutdownContext(deadline, "shutdown requested"); err != context.DeadlineExceeded {
		t.Fatalf("ShutdownContext error = %v, want deadline exceeded", err)
	}
	if _, active := runtime.Active(); !active {
		t.Fatal("shutdown terminalized a loop-owned run before its goroutine finished")
	}

	close(release)
	if err := runtime.ShutdownContext(context.Background(), "shutdown requested"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("agent loop did not finish")
	}
	if _, active := runtime.Active(); active {
		t.Fatal("execution remains active after loop completion")
	}
	if got := runtime.State(); got != RunStateCancelled {
		t.Fatalf("state = %q, want cancelled", got)
	}
}

func TestExecutionRuntimeWaitSeesTerminalTransition(t *testing.T) {
	var runtime ExecutionRuntime
	if _, err := runtime.Begin(context.Background(), "run-wait"); err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	go func() {
		time.Sleep(10 * time.Millisecond)
		_ = runtime.FinishWithState("run-wait", RunStateCompleted)
		close(finished)
	}()
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Wait(waitCtx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("finish goroutine did not complete")
	}
}
