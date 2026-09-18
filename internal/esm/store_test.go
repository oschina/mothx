package esm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/startvibecoding/mothx/internal/session"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	sessionDir := t.TempDir()
	m := session.New("/tmp/esm-test", sessionDir)
	if err := m.Init(); err != nil {
		t.Fatalf("Init session: %v", err)
	}
	return NewStore(sessionDir), m.GetHeader().ID
}

func TestStoreCreateAndUsageAccounting(t *testing.T) {
	ctx := context.Background()
	store, sessionID := newTestStore(t)

	obj, err := store.Create(ctx, sessionID, "ship esm")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if obj.Status != StatusActive || obj.Objective != "ship esm" {
		t.Fatalf("created objective = %#v", obj)
	}

	if _, err := store.Create(ctx, sessionID, "replace"); !errors.Is(err, ErrObjectiveExists) {
		t.Fatalf("duplicate Create error = %v, want ErrObjectiveExists", err)
	}

	if obj, err = store.AccountUsage(ctx, sessionID, 60, 1000); err != nil {
		t.Fatalf("AccountUsage first: %v", err)
	}
	if obj.Status != StatusActive || obj.TokensUsed != 60 {
		t.Fatalf("after first usage = %#v", obj)
	}

	// Usage is observability only: accumulating tokens never changes status.
	if obj, err = store.AccountUsage(ctx, sessionID, 40, 500); err != nil {
		t.Fatalf("AccountUsage second: %v", err)
	}
	if obj.Status != StatusActive || obj.TokensUsed != 100 || obj.TimeUsedMS != 1500 {
		t.Fatalf("after usage = %#v", obj)
	}

	if _, err := store.Pause(ctx, sessionID); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	obj, err = store.Resume(ctx, sessionID)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if obj.Status != StatusActive {
		t.Fatalf("status after resume = %s, want active", obj.Status)
	}
}

func TestStoreBlockedAuditAndComplete(t *testing.T) {
	ctx := context.Background()
	store, sessionID := newTestStore(t)
	if _, err := store.Create(ctx, sessionID, "finish migration"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	for i, runID := range []string{"run-1", "run-2"} {
		obj, err := store.UpdateFromModelForRun(ctx, sessionID, StatusBlocked, "missing API token", runID)
		if err != nil {
			t.Fatalf("UpdateFromModel blocked %d: %v", i+1, err)
		}
		if obj.Status != StatusActive || obj.BlockedCount != i+1 {
			t.Fatalf("blocked audit %d = %#v", i+1, obj)
		}
	}
	obj, err := store.UpdateFromModelForRun(ctx, sessionID, StatusBlocked, "missing API token", "run-3")
	if err != nil {
		t.Fatalf("UpdateFromModel blocked 3: %v", err)
	}
	if obj.Status != StatusBlocked || obj.BlockedCount != 3 {
		t.Fatalf("third blocked audit = %#v", obj)
	}

	obj, err = store.Resume(ctx, sessionID)
	if err != nil {
		t.Fatalf("Resume blocked: %v", err)
	}
	if obj.BlockedCount != 0 || obj.BlockedReason != "" || obj.Status != StatusActive {
		t.Fatalf("resume did not reset blocked audit: %#v", obj)
	}

	obj, err = store.UpdateFromModel(ctx, sessionID, StatusComplete, "all checks pass")
	if err != nil {
		t.Fatalf("UpdateFromModel complete: %v", err)
	}
	if obj.Status != StatusCompleteCandidate || obj.CompletionReason != "all checks pass" {
		t.Fatalf("complete candidate = %#v", obj)
	}

	obj, err = store.MarkCompleteFromAudit(ctx, sessionID, "auditor verified every requirement")
	if err != nil {
		t.Fatalf("MarkCompleteFromAudit: %v", err)
	}
	if obj.Status != StatusComplete {
		t.Fatalf("status = %s, want complete after audit", obj.Status)
	}
}

func TestStoreBlockedAuditRequiresConsecutiveRuns(t *testing.T) {
	ctx := context.Background()
	store, sessionID := newTestStore(t)
	if _, err := store.Create(ctx, sessionID, "finish migration"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	obj, err := store.UpdateFromModelForRun(ctx, sessionID, StatusBlocked, "missing API token", "run-1")
	if err != nil {
		t.Fatalf("blocked run 1: %v", err)
	}
	if obj.BlockedCount != 1 || obj.BlockedRunID != "run-1" || obj.Status != StatusActive {
		t.Fatalf("blocked run 1 = %#v", obj)
	}

	obj, err = store.UpdateFromModelForRun(ctx, sessionID, StatusBlocked, "missing API token", "run-1")
	if err != nil {
		t.Fatalf("duplicate blocked run 1: %v", err)
	}
	if obj.BlockedCount != 1 {
		t.Fatalf("duplicate blocked same run count = %d, want 1", obj.BlockedCount)
	}

	obj, err = store.FinishRun(ctx, sessionID, "run-2")
	if err != nil {
		t.Fatalf("FinishRun run 2: %v", err)
	}
	if obj.BlockedCount != 0 || obj.BlockedReason != "" || obj.BlockedRunID != "" {
		t.Fatalf("non-blocked run did not reset audit: %#v", obj)
	}

	for i, runID := range []string{"run-3", "run-4"} {
		obj, err = store.UpdateFromModelForRun(ctx, sessionID, StatusBlocked, "missing API token", runID)
		if err != nil {
			t.Fatalf("blocked %s: %v", runID, err)
		}
		if obj.BlockedCount != i+1 || obj.Status != StatusActive {
			t.Fatalf("blocked %s = %#v", runID, obj)
		}
	}
	obj, err = store.UpdateFromModelForRun(ctx, sessionID, StatusBlocked, "missing API token", "run-5")
	if err != nil {
		t.Fatalf("blocked run 5: %v", err)
	}
	if obj.BlockedCount != 3 || obj.Status != StatusBlocked {
		t.Fatalf("third consecutive blocker = %#v", obj)
	}
}

func TestStoreCompleteRequiresEvidence(t *testing.T) {
	ctx := context.Background()
	store, sessionID := newTestStore(t)
	if _, err := store.Create(ctx, sessionID, "finish migration"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := store.UpdateFromModel(ctx, sessionID, StatusComplete, ""); err == nil {
		t.Fatal("UpdateFromModel complete without evidence succeeded, want error")
	}
}

func TestStoreRejectCompletionCandidateReturnsActive(t *testing.T) {
	ctx := context.Background()
	store, sessionID := newTestStore(t)
	if _, err := store.Create(ctx, sessionID, "finish migration"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	obj, err := store.UpdateFromModelForRun(ctx, sessionID, StatusComplete, "worker evidence", "run-1")
	if err != nil {
		t.Fatalf("UpdateFromModelForRun complete: %v", err)
	}
	if obj.Status != StatusCompleteCandidate || obj.CompletionRunID != "run-1" {
		t.Fatalf("candidate = %#v", obj)
	}

	obj, err = store.RejectCompletionCandidate(ctx, sessionID, "missing requirement")
	if err != nil {
		t.Fatalf("RejectCompletionCandidate: %v", err)
	}
	if obj.Status != StatusActive || obj.CompletionReview != "missing requirement" || obj.RejectionCount != 1 {
		t.Fatalf("rejected candidate = %#v", obj)
	}
}

func TestStorePersistsWorkerProgress(t *testing.T) {
	ctx := context.Background()
	store, sessionID := newTestStore(t)
	if _, err := store.Create(ctx, sessionID, "finish migration"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	obj, err := store.RecordWorkerProgress(ctx, sessionID, "implemented parser", []string{"add tests", "update docs"})
	if err != nil {
		t.Fatalf("RecordWorkerProgress: %v", err)
	}
	if obj.Phase != PhaseWorker || obj.ProgressSummary != "implemented parser" {
		t.Fatalf("worker progress = %#v", obj)
	}
	if len(obj.RemainingWork) != 2 || obj.RemainingWork[0] != "add tests" || obj.RemainingWork[1] != "update docs" {
		t.Fatalf("remaining work = %#v", obj.RemainingWork)
	}

	obj, err = store.UpdateFromModelForRun(ctx, sessionID, StatusComplete, "worker evidence", "run-1")
	if err != nil {
		t.Fatalf("UpdateFromModelForRun: %v", err)
	}
	obj, err = store.SetPhase(ctx, sessionID, PhaseAudit)
	if err != nil {
		t.Fatalf("SetPhase: %v", err)
	}
	if obj.Phase != PhaseAudit || len(obj.RemainingWork) != 2 {
		t.Fatalf("phase update lost progress = %#v", obj)
	}
}

func TestStoreRepeatedRecoveryAndWorkerProgressReset(t *testing.T) {
	ctx := context.Background()
	store, sessionID := newTestStore(t)
	if _, err := store.Create(ctx, sessionID, "finish migration"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	for i := 1; i <= 4; i++ {
		obj, err := store.RecordRecovery(ctx, sessionID, "worker timed out", "observer found resumable work", []string{"finish tests"})
		if err != nil {
			t.Fatalf("RecordRecovery %d: %v", i, err)
		}
		if obj.Status != StatusActive || obj.RecoveryCount != i || obj.RecoveryReason != "worker timed out" {
			t.Fatalf("recovery %d = %#v", i, obj)
		}
	}

	obj, err := store.RecordWorkerProgress(ctx, sessionID, "implemented tests", []string{"run verification"})
	if err != nil {
		t.Fatalf("RecordWorkerProgress: %v", err)
	}
	if obj.RecoveryCount != 0 || obj.RecoveryReason != "" {
		t.Fatalf("worker progress did not reset recovery state: %#v", obj)
	}
}

func TestStoreRepeatedCompletionRejectionsRemainActive(t *testing.T) {
	ctx := context.Background()
	store, sessionID := newTestStore(t)
	if _, err := store.Create(ctx, sessionID, "finish migration"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	for i := 1; i <= 4; i++ {
		runID := fmt.Sprintf("run-%d", i)
		if _, err := store.UpdateFromModelForRun(ctx, sessionID, StatusComplete, "worker evidence", runID); err != nil {
			t.Fatalf("candidate %d: %v", i, err)
		}
		obj, err := store.RejectCompletionCandidateForRun(ctx, sessionID, runID, "missing requirement", []string{"add tests"})
		if err != nil {
			t.Fatalf("rejection %d: %v", i, err)
		}
		if obj.Status != StatusActive || obj.RejectionCount != i || obj.RejectionRunID != runID || !obj.CanAutoRun() {
			t.Fatalf("rejection %d = %#v", i, obj)
		}
		if len(obj.RemainingWork) != 1 || obj.RemainingWork[0] != "add tests" {
			t.Fatalf("rejection %d missing work = %#v", i, obj.RemainingWork)
		}

		duplicate, err := store.RejectCompletionCandidateForRun(ctx, sessionID, runID, "duplicate", []string{"add tests"})
		if err != nil {
			t.Fatalf("duplicate rejection %d: %v", i, err)
		}
		if duplicate.RejectionCount != i {
			t.Fatalf("duplicate rejection count = %d, want %d", duplicate.RejectionCount, i)
		}
	}

	obj, err := store.Get(ctx, sessionID)
	if err != nil {
		t.Fatalf("Get paused objective: %v", err)
	}
	if !obj.CanAutoRun() {
		t.Fatal("rejected objective must continue automatically")
	}
	obj, err = store.FinishRun(ctx, sessionID, "run-after-rejection")
	if err != nil {
		t.Fatalf("FinishRun: %v", err)
	}
	if obj.Status != StatusActive || obj.RejectionCount != 0 || obj.RejectionRunID != "" || obj.CompletionReview != "missing requirement" {
		t.Fatalf("new continuation did not reset rejection state = %#v", obj)
	}
	if prompt := WorkerTaskPrompt(obj); !strings.Contains(prompt, "missing requirement") {
		t.Fatalf("worker prompt lost rejection review:\n%s", prompt)
	}
}

func TestStoreNonRejectedRunResetsCompletionRejectionStreak(t *testing.T) {
	ctx := context.Background()
	store, sessionID := newTestStore(t)
	if _, err := store.Create(ctx, sessionID, "finish migration"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.UpdateFromModelForRun(ctx, sessionID, StatusComplete, "worker evidence", "run-1"); err != nil {
		t.Fatalf("candidate: %v", err)
	}
	if _, err := store.RejectCompletionCandidateForRun(ctx, sessionID, "run-1", "missing test", []string{"add test"}); err != nil {
		t.Fatalf("reject: %v", err)
	}

	obj, err := store.FinishRun(ctx, sessionID, "run-2")
	if err != nil {
		t.Fatalf("FinishRun: %v", err)
	}
	if obj.RejectionCount != 0 || obj.RejectionRunID != "" {
		t.Fatalf("non-rejected run did not reset streak: %#v", obj)
	}
}

func TestStoreWorkerPrecheckRejectionKeepsObjectiveActive(t *testing.T) {
	ctx := context.Background()
	store, sessionID := newTestStore(t)
	if _, err := store.Create(ctx, sessionID, "finish migration"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var obj *Objective
	for i := 1; i <= 4; i++ {
		var err error
		obj, err = store.RejectWorkerReport(ctx, sessionID, fmt.Sprintf("run-%d", i), "remaining work", []string{"finish implementation"})
		if err != nil {
			t.Fatalf("worker rejection %d: %v", i, err)
		}
	}
	if obj.Status != StatusActive || obj.RejectionCount != 4 || !obj.CanAutoRun() {
		t.Fatalf("worker rejection must remain recoverable = %#v", obj)
	}
}

func TestStoreRecordCompletionReviewWhileActive(t *testing.T) {
	ctx := context.Background()
	store, sessionID := newTestStore(t)
	if _, err := store.Create(ctx, sessionID, "finish migration"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	obj, err := store.RecordCompletionReview(ctx, sessionID, "worker completion lacked tool-backed evidence")
	if err != nil {
		t.Fatalf("RecordCompletionReview: %v", err)
	}
	if obj.Status != StatusActive || obj.CompletionReview != "worker completion lacked tool-backed evidence" {
		t.Fatalf("recorded review = %#v", obj)
	}
}
