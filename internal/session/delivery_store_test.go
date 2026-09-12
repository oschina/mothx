package session

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/startvibecoding/mothx/internal/dao"
	"testing"
	"time"
)

func deliveryFixture(t *testing.T) (string, string) {
	t.Helper()
	sessionDir := t.TempDir()
	mgr := New(t.TempDir(), sessionDir)
	if err := mgr.InitWithID("delivery-session"); err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()
	if err := CreateSessionRun(sessionDir, SessionRun{ID: "delivery-run", SessionID: mgr.GetHeader().ID, Status: "completed", StartedAt: started, UpdatedAt: started, FinishedAt: &started}); err != nil {
		t.Fatal(err)
	}
	return sessionDir, mgr.GetHeader().ID
}

func createDeliveryFixturePlan(t *testing.T, sessionDir, sessionID string) DeliveryPlan {
	t.Helper()
	now := time.Now().UTC()
	plan := DeliveryPlan{Intent: DeliveryIntent{ID: "delivery-intent", SessionID: sessionID, RunID: "delivery-run", Platform: "wechat", TargetID: "chat", Status: "pending", CreatedAt: now, UpdatedAt: now, TransportContext: json.RawMessage(`{"caption":"hello"}`)}, Operations: []DeliveryOperation{
		{ID: "delivery-op-caption", OperationKey: "caption", OperationKind: "send_text", Sequence: 1, IdempotencyKey: "delivery-op-caption", PayloadDigest: "sha256:caption", Status: "pending", CreatedAt: now, UpdatedAt: now},
		{ID: "delivery-op-file", OperationKey: "file", OperationKind: "send_artifact", Sequence: 2, DependsOn: "delivery-op-caption", IdempotencyKey: "delivery-op-file", PayloadDigest: "sha256:file", Status: "pending", CreatedAt: now, UpdatedAt: now},
	}}
	if err := CreateDeliveryPlan(context.Background(), sessionDir, plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestDeliveryClaimFencesExpiredWorkerAndHonorsDependency(t *testing.T) {
	sessionDir, sessionID := deliveryFixture(t)
	plan := createDeliveryFixturePlan(t, sessionDir, sessionID)
	now := time.Now().UTC()
	if _, err := ClaimDeliveryOperation(t.Context(), sessionDir, plan.Operations[1].ID, "worker-a", now, time.Minute); !errors.Is(err, ErrDeliveryOperationBusy) {
		t.Fatalf("dependency claim error = %v, want busy", err)
	}
	claimed, err := ClaimDeliveryOperation(t.Context(), sessionDir, plan.Operations[0].ID, "worker-a", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.LeaseOwner != "worker-a" || claimed.LeaseEpoch != 1 || claimed.AttemptCount != 1 {
		t.Fatalf("claim = %#v", claimed)
	}
	if _, err := ClaimDeliveryOperation(t.Context(), sessionDir, plan.Operations[0].ID, "worker-b", now, time.Minute); !errors.Is(err, ErrDeliveryOperationBusy) {
		t.Fatalf("second claim error = %v, want busy", err)
	}
	if err := UpdateDeliveryOperation(t.Context(), sessionDir, claimed.ID, "worker-b", claimed.LeaseEpoch, "delivered", "", "msg-a", nil, "", nil); !errors.Is(err, ErrDeliveryLeaseLost) {
		t.Fatalf("wrong owner update = %v, want lease lost", err)
	}
	if err := UpdateDeliveryOperation(t.Context(), sessionDir, claimed.ID, "worker-a", claimed.LeaseEpoch, "delivered", "", "msg-a", nil, "", nil); err != nil {
		t.Fatal(err)
	}
	dependent, err := ClaimDeliveryOperation(t.Context(), sessionDir, plan.Operations[1].ID, "worker-b", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if dependent.LeaseEpoch != 1 {
		t.Fatalf("dependent claim epoch = %d", dependent.LeaseEpoch)
	}
	if err := UpdateDeliveryOperation(t.Context(), sessionDir, dependent.ID, "worker-b", dependent.LeaseEpoch, "retry_wait", "", "", nil, "timeout", ptrTime(now.Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := QueryRootDatabase(sessionDir, func(db *dao.Database) error {
		return db.Bun().QueryRow(`SELECT status FROM delivery_intents WHERE id = ?`, plan.Intent.ID).Scan(&status)
	}); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Fatalf("intent status = %q, want pending", status)
	}
}

func TestDeliveryClaimCanRecoverExpiredLease(t *testing.T) {
	sessionDir, sessionID := deliveryFixture(t)
	plan := createDeliveryFixturePlan(t, sessionDir, sessionID)
	first, err := ClaimDeliveryOperation(t.Context(), sessionDir, plan.Operations[0].ID, "worker-a", time.Now().UTC().Add(-time.Minute), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ClaimDeliveryOperation(t.Context(), sessionDir, plan.Operations[0].ID, "worker-b", time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if second.LeaseEpoch != first.LeaseEpoch+1 || second.LeaseOwner != "worker-b" {
		t.Fatalf("recovered claim = %#v, first = %#v", second, first)
	}
	if err := UpdateDeliveryOperation(t.Context(), sessionDir, plan.Operations[0].ID, "worker-a", first.LeaseEpoch, "delivered", "", "stale", nil, "", nil); !errors.Is(err, ErrDeliveryLeaseLost) {
		t.Fatalf("stale worker update = %v, want lease lost", err)
	}
}

func TestUploadedPhaseCountsAsTerminalAfterDependentSend(t *testing.T) {
	sessionDir, sessionID := deliveryFixture(t)
	plan := createDeliveryFixturePlan(t, sessionDir, sessionID)
	now := time.Now().UTC()
	upload, err := ClaimDeliveryOperation(t.Context(), sessionDir, plan.Operations[0].ID, "worker-a", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	state := json.RawMessage(`{"provider_asset_id":"asset-1"}`)
	if err := UpdateDeliveryOperation(t.Context(), sessionDir, upload.ID, "worker-a", upload.LeaseEpoch, "uploaded", "asset-1", "", state, "", nil); err != nil {
		t.Fatal(err)
	}
	send, err := ClaimDeliveryOperation(t.Context(), sessionDir, plan.Operations[1].ID, "worker-a", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateDeliveryOperation(t.Context(), sessionDir, send.ID, "worker-a", send.LeaseEpoch, "delivered", "asset-1", "message-1", state, "", nil); err != nil {
		t.Fatal(err)
	}
	loaded, err := GetDeliveryPlan(t.Context(), sessionDir, plan.Intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Intent.Status != "delivered" {
		t.Fatalf("intent status = %q, want delivered", loaded.Intent.Status)
	}
	if err := UpdateDeliveryOperation(t.Context(), sessionDir, upload.ID, "worker-a", upload.LeaseEpoch, "uploaded", "asset-1", "", state, "", nil); err != nil {
		t.Fatalf("idempotent uploaded update = %v", err)
	}
}

func TestDeliveryFailureCascadesToDependentOperation(t *testing.T) {
	sessionDir, sessionID := deliveryFixture(t)
	plan := createDeliveryFixturePlan(t, sessionDir, sessionID)
	now := time.Now().UTC()
	upload, err := ClaimDeliveryOperation(t.Context(), sessionDir, plan.Operations[0].ID, "worker-a", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateDeliveryOperation(t.Context(), sessionDir, upload.ID, "worker-a", upload.LeaseEpoch, "failed", "", "", nil, "provider_rejected", nil); err != nil {
		t.Fatal(err)
	}
	dependent, err := GetDeliveryOperation(t.Context(), sessionDir, plan.Operations[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if dependent.Status != "failed" || dependent.FailureCode != "dependency_failed" {
		t.Fatalf("dependent after failed prerequisite = %#v", dependent)
	}
	intent, err := GetDeliveryPlan(t.Context(), sessionDir, plan.Intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if intent.Intent.Status != "failed" {
		t.Fatalf("intent after dependency failure = %q, want failed", intent.Intent.Status)
	}
}

func TestUncertainDeliveryCascadesUncertainDependent(t *testing.T) {
	sessionDir, sessionID := deliveryFixture(t)
	plan := createDeliveryFixturePlan(t, sessionDir, sessionID)
	now := time.Now().UTC()
	upload, err := ClaimDeliveryOperation(t.Context(), sessionDir, plan.Operations[0].ID, "worker-a", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateDeliveryOperation(t.Context(), sessionDir, upload.ID, "worker-a", upload.LeaseEpoch, "uncertain", "", "", nil, "provider_timeout", nil); err != nil {
		t.Fatal(err)
	}
	dependent, err := GetDeliveryOperation(t.Context(), sessionDir, plan.Operations[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if dependent.Status != "uncertain" || dependent.FailureCode != "dependency_uncertain" {
		t.Fatalf("dependent after uncertain prerequisite = %#v", dependent)
	}
	intent, err := GetDeliveryPlan(t.Context(), sessionDir, plan.Intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if intent.Intent.Status != "uncertain" {
		t.Fatalf("intent after uncertain dependency = %q, want uncertain", intent.Intent.Status)
	}
}

func ptrTime(value time.Time) *time.Time { return &value }

// TestReopenFailedDeliveryOperationRestartsRetryWindow guards the recovery path
// for operations that exhausted their retry budget: only a failed operation can
// be reopened, and the reopen clears the failure and stamps a fresh window.
func TestReopenFailedDeliveryOperationRestartsRetryWindow(t *testing.T) {
	sessionDir, _ := deliveryFixture(t)
	createDeliveryFixturePlan(t, sessionDir, "delivery-session")
	ctx := context.Background()

	if reopened, err := ReopenFailedDeliveryOperation(ctx, sessionDir, "delivery-op-caption", time.Now().UTC()); err != nil {
		t.Fatalf("reopen pending operation: %v", err)
	} else if reopened {
		t.Fatal("a pending operation must not be reopened")
	}

	claimed, err := ClaimDeliveryOperation(ctx, sessionDir, "delivery-op-caption", "test-worker", time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("claim operation: %v", err)
	}
	if err := UpdateDeliveryOperation(ctx, sessionDir, "delivery-op-caption", "test-worker", claimed.LeaseEpoch, "failed", "", "", nil, "delivery_retries_exhausted", nil); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	reopenedAt := time.Now().UTC()
	reopened, err := ReopenFailedDeliveryOperation(ctx, sessionDir, "delivery-op-caption", reopenedAt)
	if err != nil {
		t.Fatalf("reopen failed operation: %v", err)
	}
	if !reopened {
		t.Fatal("failed operation was not reopened")
	}

	operation, err := GetDeliveryOperation(ctx, sessionDir, "delivery-op-caption")
	if err != nil {
		t.Fatal(err)
	}
	if operation.Status != "retry_wait" || operation.FailureCode != "" || operation.NextAttemptAt != nil {
		t.Fatalf("reopened operation = %#v", operation)
	}
	if operation.RetryWindowStartedAt == nil || operation.RetryWindowStartedAt.Sub(reopenedAt).Abs() > time.Second {
		t.Fatalf("retry window start = %v, want ~%v", operation.RetryWindowStartedAt, reopenedAt)
	}

	// A second reopen must be a no-op: the operation is no longer failed.
	again, err := ReopenFailedDeliveryOperation(ctx, sessionDir, "delivery-op-caption", time.Now().UTC())
	if err != nil {
		t.Fatalf("second reopen: %v", err)
	}
	if again {
		t.Fatal("retry_wait operation must not be reopened again")
	}
}

// TestListFailedTransientDeliveryOperations guards the reconnect scan: only
// transport-level failures for the requested platform are reported, never
// permanent projection failures or other platforms.
func TestListFailedTransientDeliveryOperations(t *testing.T) {
	sessionDir, _ := deliveryFixture(t)
	createDeliveryFixturePlan(t, sessionDir, "delivery-session")
	ctx := context.Background()

	captionClaim, err := ClaimDeliveryOperation(ctx, sessionDir, "delivery-op-caption", "test-worker", time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("claim caption operation: %v", err)
	}
	if err := UpdateDeliveryOperation(ctx, sessionDir, "delivery-op-caption", "test-worker", captionClaim.LeaseEpoch, "failed", "", "", nil, "delivery_retries_exhausted", nil); err != nil {
		t.Fatalf("mark transient failure: %v", err)
	}
	// The dependent operation cannot be claimed while its dependency is failed,
	// so mark it failed through the unleased row identity instead.
	if err := WriteRootDatabase(ctx, sessionDir, func(tx *dao.Tx) error {
		_, err := dao.NewDeliveryDAO(nil).UpdateResult(ctx, tx, "delivery-op-file", "", 0, "failed", "", "", "{}", "delivery_projection_missing", nil, time.Now().UTC().Format(time.RFC3339Nano))
		return err
	}); err != nil {
		t.Fatalf("mark permanent failure: %v", err)
	}

	ids, err := ListFailedTransientDeliveryOperations(ctx, sessionDir, "wechat")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "delivery-op-caption" {
		t.Fatalf("failed transient ids = %v, want [delivery-op-caption]", ids)
	}
	if ids, err := ListFailedTransientDeliveryOperations(ctx, sessionDir, "feishu"); err != nil || len(ids) != 0 {
		t.Fatalf("other platform ids = %v, err = %v", ids, err)
	}
}

// TestReopenFailedDeliveryOperationRecoversDependentFailures guards the
// cascade: an attachment terminalized only because its caption failed must be
// retried too once the caption is reopened.
func TestReopenFailedDeliveryOperationRecoversDependentFailures(t *testing.T) {
	sessionDir, _ := deliveryFixture(t)
	createDeliveryFixturePlan(t, sessionDir, "delivery-session")
	ctx := context.Background()

	// Fail the caption and let dependency propagation terminalize the file.
	claimed, err := ClaimDeliveryOperation(ctx, sessionDir, "delivery-op-caption", "test-worker", time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("claim caption operation: %v", err)
	}
	if err := UpdateDeliveryOperation(ctx, sessionDir, "delivery-op-caption", "test-worker", claimed.LeaseEpoch, "failed", "", "", nil, "delivery_retries_exhausted", nil); err != nil {
		t.Fatalf("mark caption failed: %v", err)
	}
	fileBefore, err := GetDeliveryOperation(ctx, sessionDir, "delivery-op-file")
	if err != nil {
		t.Fatal(err)
	}
	if fileBefore.Status != "failed" || fileBefore.FailureCode != "dependency_failed" {
		t.Fatalf("dependent operation = %#v, want failed/dependency_failed", fileBefore)
	}

	reopened, err := ReopenFailedDeliveryOperation(ctx, sessionDir, "delivery-op-caption", time.Now().UTC())
	if err != nil || !reopened {
		t.Fatalf("reopen caption = %v, %v", reopened, err)
	}
	fileAfter, err := GetDeliveryOperation(ctx, sessionDir, "delivery-op-file")
	if err != nil {
		t.Fatal(err)
	}
	if fileAfter.Status != "retry_wait" || fileAfter.FailureCode != "" {
		t.Fatalf("dependent operation after reopen = %#v, want retry_wait", fileAfter)
	}
}

// TestReopenFailedDeliveryOperationRefusesPermanentFailures guards the SQL
// fence behind the operator retry: a permanent platform failure must stay failed
// instead of being reopened into another doomed attempt (the ACP entry refuses
// it explicitly; this blocks any other caller).
func TestReopenFailedDeliveryOperationRefusesPermanentFailures(t *testing.T) {
	sessionDir, _ := deliveryFixture(t)
	createDeliveryFixturePlan(t, sessionDir, "delivery-session")
	ctx := context.Background()

	claimed, err := ClaimDeliveryOperation(ctx, sessionDir, "delivery-op-caption", "test-worker", time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("claim operation: %v", err)
	}
	if err := UpdateDeliveryOperation(ctx, sessionDir, "delivery-op-caption", "test-worker", claimed.LeaseEpoch, "failed", "", "", nil, "unsupported_media_kind", nil); err != nil {
		t.Fatalf("mark permanent failure: %v", err)
	}

	reopened, err := ReopenFailedDeliveryOperation(ctx, sessionDir, "delivery-op-caption", time.Now().UTC())
	if err != nil {
		t.Fatalf("reopen permanent failure: %v", err)
	}
	if reopened {
		t.Fatal("a permanent failure must not be reopened")
	}
	operation, err := GetDeliveryOperation(ctx, sessionDir, "delivery-op-caption")
	if err != nil {
		t.Fatal(err)
	}
	if operation.Status != "failed" || operation.FailureCode != "unsupported_media_kind" {
		t.Fatalf("operation = %#v, want the original permanent failure", operation)
	}
}
