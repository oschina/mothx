package agentruntime

import (
	"errors"
	"testing"
)

func TestDecisionServiceFirstResponseWins(t *testing.T) {
	var service DecisionService
	request := DecisionRequest{ID: "approval-1", RunID: "run-1", SessionID: "session-1", Kind: DecisionApproval}
	if err := service.Register(request); err != nil {
		t.Fatal(err)
	}
	got, err := service.Resolve(DecisionResolution{ID: request.ID, Kind: DecisionApproval, Status: "resolved", Value: "approve_once"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != request.ID || got.RunID != request.RunID || got.SessionID != request.SessionID || got.Kind != request.Kind {
		t.Fatalf("resolved request = %#v, want %#v", got, request)
	}
	if _, err := service.Resolve(DecisionResolution{ID: request.ID, Kind: DecisionApproval}); err == nil {
		t.Fatal("second resolution unexpectedly succeeded")
	}
}

func TestDecisionServiceRejectsDuplicateAndClearRun(t *testing.T) {
	var service DecisionService
	request := DecisionRequest{ID: "question-1", RunID: "run-1", Kind: DecisionQuestion}
	if err := service.Register(request); err != nil {
		t.Fatal(err)
	}
	if err := service.Register(request); err == nil {
		t.Fatal("duplicate decision unexpectedly registered")
	}
	cleared := service.ClearRun(request.RunID)
	if len(cleared) != 1 || cleared[0].ID != request.ID || cleared[0].RunID != request.RunID || cleared[0].Kind != request.Kind {
		t.Fatalf("cleared = %#v, want %#v", cleared, []DecisionRequest{request})
	}
	if pending := service.Pending(); len(pending) != 0 {
		t.Fatalf("pending decisions remain: %#v", pending)
	}
}

func TestDecisionServiceRetainsDecisionWhenResolverFails(t *testing.T) {
	var service DecisionService
	if err := service.Register(DecisionRequest{ID: "approval-1", RunID: "run-1", Kind: DecisionApproval}); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	if err := service.Bind("approval-1", func(string) error {
		attempts++
		if attempts == 1 {
			return errors.New("agent callback unavailable")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Resolve(DecisionResolution{ID: "approval-1", Kind: DecisionApproval, Value: "allow"}); err == nil {
		t.Fatal("first resolution unexpectedly succeeded")
	}
	if len(service.Pending()) != 1 {
		t.Fatal("failed callback consumed the pending decision")
	}
	if _, err := service.Resolve(DecisionResolution{ID: "approval-1", Kind: DecisionApproval, Value: "allow"}); err != nil {
		t.Fatalf("retry resolution: %v", err)
	}
	if len(service.Pending()) != 0 {
		t.Fatal("successful retry left the decision pending")
	}
}

func TestDecisionServiceDiscardRemovesUncommittablePending(t *testing.T) {
	var service DecisionService
	request := DecisionRequest{ID: "approval-1", RunID: "run-1", Kind: DecisionApproval}
	if err := service.Register(request); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResolveWith(DecisionResolution{
		ID: "approval-1", Kind: DecisionApproval, Status: DecisionStatusResolved, Value: "allow_once",
	}, func(DecisionRequest) error {
		return errors.New("durable commit failed")
	}); err == nil {
		t.Fatal("failed commit unexpectedly resolved the decision")
	}
	if len(service.Pending()) != 1 {
		t.Fatal("failed commit consumed the pending decision before the caller could record a terminal row")
	}
	service.Discard("approval-1")
	if pending := service.Pending(); len(pending) != 0 {
		t.Fatalf("discard left a pending decision: %#v", pending)
	}
	if _, err := service.Resolve(DecisionResolution{ID: "approval-1", Kind: DecisionApproval}); err == nil {
		t.Fatal("discarded decision remained resolvable")
	}
}

func TestDecisionServiceRegisterRejectsNilReceiver(t *testing.T) {
	var service *DecisionService
	err := service.Register(DecisionRequest{ID: "decision-1", RunID: "run-1", Kind: DecisionApproval})
	if err == nil || err.Error() != "decision service is nil" {
		t.Fatalf("Register error = %v, want nil service error", err)
	}
}

func TestDecisionServiceResolutionIsIdempotentOnlyForExactValue(t *testing.T) {
	var service DecisionService
	request := DecisionRequest{ID: "approval-idempotent", RunID: "run-1", SessionID: "session-1", Kind: DecisionApproval}
	if err := service.Register(request); err != nil {
		t.Fatal(err)
	}
	resolution := DecisionResolution{ID: request.ID, Kind: request.Kind, Status: "resolved", Value: "allow"}
	if _, err := service.Resolve(resolution); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Resolve(resolution); err != nil {
		t.Fatalf("exact duplicate resolution: %v", err)
	}
	if _, err := service.Resolve(DecisionResolution{ID: request.ID, Kind: request.Kind, Status: "resolved", Value: "deny"}); err == nil {
		t.Fatal("conflicting duplicate resolution unexpectedly succeeded")
	}
}

func TestDecisionServiceClearRunWithValueRetainsCallbackFailures(t *testing.T) {
	var service DecisionService
	if err := service.Register(DecisionRequest{ID: "decision-clear", RunID: "run-1", Kind: DecisionQuestion}); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	if err := service.Bind("decision-clear", func(string) error {
		attempts++
		if attempts == 1 {
			return errors.New("callback unavailable")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if cleared := service.ClearRunWithValue("run-1", ""); len(cleared) != 0 {
		t.Fatalf("failed callback cleared %d decisions", len(cleared))
	}
	if len(service.Pending()) != 1 {
		t.Fatal("failed callback did not retain pending decision")
	}
	if cleared := service.ClearRunWithValue("run-1", ""); len(cleared) != 1 {
		t.Fatalf("retry cleared %d decisions, want 1", len(cleared))
	}
}

func TestDecisionServiceResolveWithRetainsPendingOnCommitFailure(t *testing.T) {
	var service DecisionService
	request := DecisionRequest{ID: "decision-commit", RunID: "run-1", SessionID: "session-1", Kind: DecisionApproval}
	if err := service.Register(request); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	if _, err := service.ResolveWith(DecisionResolution{ID: request.ID, Kind: request.Kind, Value: "allow"}, func(DecisionRequest) error {
		attempts++
		return errors.New("persistence unavailable")
	}); err == nil {
		t.Fatal("ResolveWith unexpectedly succeeded")
	}
	if len(service.Pending()) != 1 {
		t.Fatal("commit failure consumed the pending decision")
	}
	resolved, err := service.ResolveWith(DecisionResolution{ID: request.ID, Kind: request.Kind, Value: "allow"}, func(got DecisionRequest) error {
		if got.ID != request.ID || got.RunID != request.RunID {
			t.Fatalf("commit request = %#v, want %#v", got, request)
		}
		return nil
	})
	if err != nil || resolved.RunID != request.RunID {
		t.Fatalf("retry ResolveWith = %#v, %v", resolved, err)
	}
	if attempts != 1 || len(service.Pending()) != 0 {
		t.Fatalf("attempts=%d pending=%#v", attempts, service.Pending())
	}
}
