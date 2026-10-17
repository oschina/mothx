package agentruntime

import (
	"context"
	"testing"
)

func TestExecutionRuntimeCompletionClosesAfterFinish(t *testing.T) {
	runtime := &ExecutionRuntime{}
	ctx, err := runtime.Begin(context.Background(), "completion-run")
	if err != nil {
		t.Fatal(err)
	}
	if ctx == nil {
		t.Fatal("Begin returned nil context")
	}
	completion := runtime.Completion("completion-run")
	select {
	case <-completion:
		t.Fatal("completion barrier closed before finish")
	default:
	}
	if err := runtime.FinishWithState("completion-run", RunStateCompleted); err != nil {
		t.Fatal(err)
	}
	select {
	case <-completion:
	default:
		t.Fatal("completion barrier did not close after finish")
	}
}

func TestExecutionRuntimeCompletionRejectsDifferentRun(t *testing.T) {
	runtime := &ExecutionRuntime{}
	if _, err := runtime.Begin(context.Background(), "completion-run"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runtime.Completion("other-run"):
	default:
		t.Fatal("mismatched run completion should be closed")
	}
}
