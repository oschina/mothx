package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/agent"
)

func TestClassifyErrorRetrySafety(t *testing.T) {
	base := ClassifyError(fmt.Errorf("HTTP 503 upstream unavailable"), ErrorClassificationOptions{})
	if base.Code != "provider_unavailable" || base.RetryMode != RetryAutomatic || !base.Retryable {
		t.Fatalf("base classification = %#v", base)
	}
	if base.Message != "HTTP 503 upstream unavailable" || base.Detail != base.Message {
		t.Fatalf("base diagnostic = %#v, want the provider error preserved", base)
	}

	unsafe := ClassifyError(fmt.Errorf("HTTP 503 upstream unavailable"), ErrorClassificationOptions{
		SideEffectState: SideEffectUnknown,
	})
	if unsafe.RetryMode != RetryDecisionRequired || !unsafe.Retryable {
		t.Fatalf("unsafe classification = %#v", unsafe)
	}
}

func TestClassifyAgentStuckPreservesReason(t *testing.T) {
	err := fmt.Errorf("%w: 5 consecutive turns without text or reasoning output after warning", agent.ErrAgentStuck)
	info := ClassifyError(err, ErrorClassificationOptions{Phase: PhaseTool, SideEffectState: SideEffectUnknown})
	if info.Code != "run_stuck" || info.Type != "incomplete_error" || info.FailureClass != FailureIncomplete {
		t.Fatalf("classification = %#v, want run_stuck/incomplete_error/incomplete", info)
	}
	if info.MessageKey != "run.error.stuck" || info.Phase != PhaseTool {
		t.Fatalf("classification = %#v, want run.error.stuck in phase tool", info)
	}
	if !strings.Contains(info.Message, "agent appears stuck") || !strings.Contains(info.Detail, "without text or reasoning output") {
		t.Fatalf("diagnostic = %#v, want the stuck reason preserved", info)
	}
	if info.RetryMode != RetryDecisionRequired || !info.Retryable {
		t.Fatalf("retry safety = %#v, want decision_required", info)
	}
}

func TestClassifyErrorCancellationAndTimeout(t *testing.T) {
	cancelled := ClassifyError(context.Canceled, ErrorClassificationOptions{})
	if cancelled.Code != "run_cancelled" || cancelled.RetryMode != RetryUser || cancelled.FailureClass != FailureCancelled {
		t.Fatalf("cancelled classification = %#v", cancelled)
	}
	timedOut := ClassifyError(context.DeadlineExceeded, ErrorClassificationOptions{})
	if timedOut.Code != "run_timed_out" || timedOut.RetryMode != RetryAutomatic || !timedOut.Retryable {
		t.Fatalf("timed out classification = %#v", timedOut)
	}

	plain := ClassifyError(errors.New("HTTP 400: invalid tool sequence"), ErrorClassificationOptions{Phase: PhaseTool})
	if plain.Code != "provider_request_failed" || plain.Phase != PhaseTool || plain.FailureClass != FailureTransient || plain.RetryMode != RetryAutomatic {
		t.Fatalf("plain classification = %#v", plain)
	}
	if plain.Message != "HTTP 400: invalid tool sequence" || plain.Detail != plain.Message {
		t.Fatalf("plain diagnostic = %#v, want the provider error preserved", plain)
	}
}

func TestSharedFailureContractAcrossAdapters(t *testing.T) {
	// The adapters intentionally have different wire formats, but the runtime
	// classification is the contract they must all project.
	entries := []struct {
		name  string
		phase RunPhase
	}{
		{name: "tui", phase: PhaseModel},
		{name: "acp", phase: PhaseModel},
		{name: "channel", phase: PhaseModel},
		{name: "webui", phase: PhaseModel},
	}
	for _, entry := range entries {
		t.Run(entry.name, func(t *testing.T) {
			info := ClassifyError(errors.New("HTTP 503 provider secret=should-not-leak"), ErrorClassificationOptions{
				Phase: entry.phase, RunID: "run-1", IntentID: "intent-1", RequestID: "req-1",
			})
			if info.Code != "provider_unavailable" || info.FailureClass != FailureTransient || info.RetryMode != RetryAutomatic || !info.Retryable {
				t.Fatalf("classification = %#v", info)
			}
			if info.Message == "" || !containsError(errors.New(info.Message), "503") {
				t.Fatalf("missing provider diagnostic = %q", info.Message)
			}
			payload, err := json.Marshal(info)
			if err != nil {
				t.Fatalf("marshal ErrorInfo: %v", err)
			}
			if !containsError(errors.New(string(payload)), "detail") || containsError(errors.New(string(payload)), "should-not-leak") {
				t.Fatalf("diagnostic payload = %s", payload)
			}
		})
	}
}

func TestDisplayErrorMessageIncludesProviderDetail(t *testing.T) {
	info := ErrorInfo{Message: "The model service is temporarily unavailable.", Detail: "API error 503: upstream overloaded"}
	if got := DisplayErrorMessage(info); got != "The model service is temporarily unavailable.: API error 503: upstream overloaded" {
		t.Fatalf("display message = %q", got)
	}
}
