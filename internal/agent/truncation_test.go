package agent

import (
	"context"
	"testing"

	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/tools"
)

// TestLoopReportsTruncatedOutputAsIncomplete guards the terminal semantics: a
// turn cut off by the output limit that neither escalation nor a continuation
// could recover must not be reported as a successful completion.
func TestLoopReportsTruncatedOutputAsIncomplete(t *testing.T) {
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{{ID: "m1", Name: "Model 1"}}, []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamTextDelta, TextDelta: "partial answer that was cut off"},
		{Type: provider.StreamDone, StopReason: "length"},
	})

	// MaxTokensUserSet disables the escalation retry, and without escalation the
	// continuation branch is skipped as well: the truncation is final.
	a := New(Config{
		ID:               "truncated",
		Provider:         mockProvider,
		Model:            mockProvider.Models()[0],
		Mode:             "yolo",
		MaxTokensUserSet: true,
	}, tools.NewRegistry(t.TempDir(), nil))

	var (
		terminal   TaskStatus
		stopReason string
	)
	for event := range a.Run(context.Background(), "write a very long answer") {
		if event.Type == EventRunFinished {
			terminal = event.Status
			stopReason = event.StopReason
		}
	}

	if terminal != TaskIncomplete {
		t.Fatalf("terminal status = %q, want %q (a truncated answer is incomplete)", terminal, TaskIncomplete)
	}
	if stopReason != "output_limit" {
		t.Fatalf("stop reason = %q, want output_limit", stopReason)
	}
	if got := mockProvider.GetCallCount(); got != 1 {
		t.Fatalf("provider calls = %d, want 1 (no retry is possible without escalation)", got)
	}
}
