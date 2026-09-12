package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/startvibecoding/mothx/internal/dao"
	"sort"
	"strings"
	"time"
)

var (
	ErrDeliveryLeaseLost       = errors.New("delivery operation lease was lost")
	ErrDeliveryOperationBusy   = errors.New("delivery operation is leased or not ready")
	ErrDeliveryOperationAbsent = errors.New("delivery operation was not found")
)

const defaultDeliveryLease = 30 * time.Second

// DeliveryIntent is the run-level durable outbox identity. TransportContext
// is opaque Runtime-owned state and must never be projected into prompts or
// ordinary logs.
type DeliveryIntent struct {
	ID               string
	SessionID        string
	RunID            string
	Platform         string
	TargetID         string
	ReplyMessageID   string
	TransportContext json.RawMessage
	Status           string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// DeliveryOperation is one ordered, independently recoverable outbox step.
type DeliveryOperation struct {
	ID                string
	IntentID          string
	OperationKey      string
	ArtifactID        string
	OperationKind     string
	Sequence          int
	DependsOn         string
	IdempotencyKey    string
	PayloadDigest     string
	Status            string
	ProviderAssetID   string
	ProviderMessageID string
	ProviderState     json.RawMessage
	AttemptCount      int
	NextAttemptAt     *time.Time
	FailureCode       string
	// RetryWindowStartedAt restarts the transient retry budget after an explicit
	// retry; nil means the budget started at CreatedAt.
	RetryWindowStartedAt *time.Time
	LeaseOwner           string
	LeaseEpoch           int64
	LeaseExpiresAt       *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// DeliveryPlan is created in the same transaction that terminalizes its Run.
type DeliveryPlan struct {
	Intent     DeliveryIntent
	Operations []DeliveryOperation
}

// CreateDeliveryPlan persists a plan outside terminalization only for
// recovery reconciliation and focused tools. Normal execution must attach the
// plan to FinishSessionRunAndConversationTurn instead.
func CreateDeliveryPlan(ctx context.Context, sessionDir string, plan DeliveryPlan) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return WriteRootDatabase(ctx, sessionDir, func(tx *dao.Tx) error {
		return createDeliveryPlanTx(ctx, tx, plan)
	})
}

func createDeliveryPlanTx(ctx context.Context, tx *dao.Tx, plan DeliveryPlan) error {
	deliveryDAO := dao.NewDeliveryDAO(nil)
	intent := plan.Intent
	intent.ID = strings.TrimSpace(intent.ID)
	intent.SessionID = strings.TrimSpace(intent.SessionID)
	intent.RunID = strings.TrimSpace(intent.RunID)
	intent.Platform = strings.TrimSpace(intent.Platform)
	if intent.ID == "" || intent.SessionID == "" || intent.RunID == "" || intent.Platform == "" {
		return fmt.Errorf("delivery intent identity, session, run, and platform are required")
	}
	if len(plan.Operations) == 0 {
		return fmt.Errorf("delivery intent requires at least one operation")
	}
	if intent.Status == "" {
		intent.Status = "pending"
	}
	if intent.Status != "pending" {
		return fmt.Errorf("new delivery intent status must be pending")
	}
	if intent.CreatedAt.IsZero() {
		intent.CreatedAt = time.Now().UTC()
	}
	if intent.UpdatedAt.IsZero() {
		intent.UpdatedAt = intent.CreatedAt
	}
	intent.TransportContext = normalizedRunJSON(intent.TransportContext)
	runExists, err := deliveryDAO.RunExists(ctx, tx, intent.SessionID, intent.RunID)
	if err != nil {
		return err
	}
	if !runExists {
		return fmt.Errorf("delivery Run %s does not belong to session", intent.RunID)
	}
	changed, err := deliveryDAO.InsertIntent(ctx, tx, &dao.DeliveryIntentRecord{ID: intent.ID, SessionID: intent.SessionID, RunID: intent.RunID, Platform: intent.Platform, TargetID: intent.TargetID, ReplyMessageID: intent.ReplyMessageID, TransportContext: string(intent.TransportContext), Status: intent.Status, CreatedAt: intent.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: intent.UpdatedAt.Format(time.RFC3339Nano)})
	if err != nil {
		return fmt.Errorf("create delivery intent: %w", err)
	}
	if changed == 0 {
		existingRecord, err := deliveryDAO.FindIntentByKey(ctx, tx, intent.RunID, intent.Platform, intent.TargetID)
		if err != nil {
			return err
		}
		existing := deliveryIntentFromRecord(existingRecord)
		if existing.ID != intent.ID || existing.SessionID != intent.SessionID || existing.ReplyMessageID != intent.ReplyMessageID ||
			strings.TrimSpace(string(existing.TransportContext)) != strings.TrimSpace(string(intent.TransportContext)) {
			return fmt.Errorf("delivery intent conflicts with existing Run projection")
		}
	}

	operations := append([]DeliveryOperation(nil), plan.Operations...)
	sort.SliceStable(operations, func(i, j int) bool { return operations[i].Sequence < operations[j].Sequence })
	seenKeys := make(map[string]struct{}, len(operations))
	seenSequences := make(map[int]struct{}, len(operations))
	seenIDs := make(map[string]struct{}, len(operations))
	for _, operation := range operations {
		operation.IntentID = intent.ID
		operation.ID = strings.TrimSpace(operation.ID)
		operation.OperationKey = strings.TrimSpace(operation.OperationKey)
		operation.OperationKind = strings.TrimSpace(operation.OperationKind)
		operation.IdempotencyKey = strings.TrimSpace(operation.IdempotencyKey)
		operation.PayloadDigest = strings.TrimSpace(operation.PayloadDigest)
		if operation.ID == "" || operation.OperationKey == "" || operation.OperationKind == "" || operation.Sequence <= 0 || operation.IdempotencyKey == "" || operation.PayloadDigest == "" {
			return fmt.Errorf("delivery operation identity, key, kind, sequence, idempotency key, and digest are required")
		}
		if _, exists := seenKeys[operation.OperationKey]; exists {
			return fmt.Errorf("duplicate delivery operation key %q", operation.OperationKey)
		}
		if _, exists := seenSequences[operation.Sequence]; exists {
			return fmt.Errorf("duplicate delivery operation sequence %d", operation.Sequence)
		}
		if operation.DependsOn != "" {
			if _, exists := seenIDs[operation.DependsOn]; !exists {
				return fmt.Errorf("delivery operation %s depends on a missing or later operation", operation.ID)
			}
		}
		seenKeys[operation.OperationKey] = struct{}{}
		seenSequences[operation.Sequence] = struct{}{}
		seenIDs[operation.ID] = struct{}{}
		if operation.ArtifactID != "" {
			exists, err := deliveryDAO.AttachmentExists(ctx, tx, intent.SessionID, intent.RunID, operation.ArtifactID)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("delivery artifact %s does not belong to Run", operation.ArtifactID)
			}
		}
		if operation.Status == "" {
			operation.Status = "pending"
		}
		if operation.Status != "pending" && operation.Status != "unsupported" {
			return fmt.Errorf("new delivery operation status must be pending or unsupported")
		}
		if operation.CreatedAt.IsZero() {
			operation.CreatedAt = intent.CreatedAt
		}
		if operation.UpdatedAt.IsZero() {
			operation.UpdatedAt = operation.CreatedAt
		}
		operation.ProviderState = normalizedRunJSON(operation.ProviderState)
		var artifactID, dependsOn *string
		if operation.ArtifactID != "" {
			artifactID = &operation.ArtifactID
		}
		if operation.DependsOn != "" {
			dependsOn = &operation.DependsOn
		}
		inserted, err := deliveryDAO.InsertOperation(ctx, tx, &dao.DeliveryOperationRecord{ID: operation.ID, IntentID: intent.ID, OperationKey: operation.OperationKey, ArtifactID: artifactID, OperationKind: operation.OperationKind, Sequence: operation.Sequence, DependsOn: dependsOn, IdempotencyKey: operation.IdempotencyKey, PayloadDigest: operation.PayloadDigest, Status: operation.Status, ProviderState: string(operation.ProviderState), CreatedAt: operation.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: operation.UpdatedAt.Format(time.RFC3339Nano)})
		if err != nil {
			return fmt.Errorf("create delivery operation %s: %w", operation.OperationKey, err)
		}
		if inserted == 0 {
			existingRecord, err := deliveryDAO.FindOperationByKey(ctx, tx, intent.ID, operation.OperationKey)
			if err != nil {
				return err
			}
			existingID, existingArtifact, existingKind, existingDepends, existingIdempotency, existingDigest := existingRecord.ID, stringValue(existingRecord.ArtifactID), existingRecord.OperationKind, stringValue(existingRecord.DependsOn), existingRecord.IdempotencyKey, existingRecord.PayloadDigest
			existingSequence := existingRecord.Sequence
			if existingID != operation.ID || existingArtifact != operation.ArtifactID || existingKind != operation.OperationKind ||
				existingSequence != operation.Sequence || existingDepends != operation.DependsOn ||
				existingIdempotency != operation.IdempotencyKey || existingDigest != operation.PayloadDigest {
				return fmt.Errorf("delivery operation %q conflicts with existing projection", operation.OperationKey)
			}
		}
	}
	return nil
}

// GetDeliveryPlan loads one intent and its operations in execution order.
func GetDeliveryPlan(ctx context.Context, sessionDir, intentID string) (*DeliveryPlan, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	deliveryDAO := dao.NewDeliveryDAO(db.Bun())
	record, err := deliveryDAO.FindIntent(ctx, db.Bun(), intentID)
	if err == dao.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	plan := &DeliveryPlan{Intent: deliveryIntentFromRecord(record)}
	operationRecords, err := deliveryDAO.ListOperations(ctx, db.Bun(), intentID)
	if err != nil {
		return nil, err
	}
	for _, record := range operationRecords {
		plan.Operations = append(plan.Operations, deliveryOperationFromRecord(&record))
	}
	return plan, nil
}

// ClaimDeliveryOperation atomically claims the next due operation. The lease
// epoch is incremented on every claim, so a delayed worker cannot overwrite a
// later retry after its lease expires.
func ClaimDeliveryOperation(ctx context.Context, sessionDir, operationID, owner string, now time.Time, lease time.Duration) (*DeliveryOperation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	operationID = strings.TrimSpace(operationID)
	owner = strings.TrimSpace(owner)
	if operationID == "" || owner == "" {
		return nil, fmt.Errorf("delivery operation ID and owner are required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if lease <= 0 {
		lease = defaultDeliveryLease
	}
	claimedUntil := now.Add(lease)
	var operation *DeliveryOperation
	err := WriteRootDatabase(ctx, sessionDir, func(tx *dao.Tx) error {
		deliveryDAO := dao.NewDeliveryDAO(nil)
		intentStatus, _, err := deliveryDAO.DependencyStatus(ctx, tx, operationID)
		if err != nil {
			if err == dao.ErrNoRows {
				return ErrDeliveryOperationAbsent
			}
			return err
		}
		if intentStatus == "delivered" || intentStatus == "failed" || intentStatus == "cancelled" {
			return ErrDeliveryOperationBusy
		}
		nowMillis := now.UnixMilli()
		leaseMillis := claimedUntil.UnixMilli()
		result, err := deliveryDAO.Claim(ctx, tx, operationID, owner, nowMillis, leaseMillis)
		if err != nil {
			return err
		}
		if result != 1 {
			return ErrDeliveryOperationBusy
		}
		loadedRecord, err := deliveryDAO.FindOperation(ctx, tx, operationID)
		if err != nil {
			return err
		}
		loaded := deliveryOperationFromRecord(loadedRecord)
		operation = &loaded
		return nil
	})
	if err != nil {
		return nil, err
	}
	return operation, nil
}

// UpdateDeliveryOperation applies a fenced provider result. Terminal updates
// are idempotent when a retry repeats the same result after an unknown commit.
func UpdateDeliveryOperation(ctx context.Context, sessionDir, operationID, owner string, epoch int64, status, providerAssetID, providerMessageID string, providerState json.RawMessage, failureCode string, nextAttemptAt *time.Time) error {
	if ctx == nil {
		ctx = context.Background()
	}
	operationID = strings.TrimSpace(operationID)
	owner = strings.TrimSpace(owner)
	status = strings.TrimSpace(status)
	if operationID == "" || owner == "" || epoch <= 0 {
		return fmt.Errorf("delivery operation identity and lease are required")
	}
	if !validDeliveryOperationStatus(status) {
		return fmt.Errorf("invalid delivery operation status %q", status)
	}
	providerState = normalizedRunJSON(providerState)
	updatedAt := time.Now().UTC()
	var next any
	if nextAttemptAt != nil {
		next = nextAttemptAt.UnixMilli()
	}
	err := WriteRootDatabase(ctx, sessionDir, func(tx *dao.Tx) error {
		if status == "retry_wait" {
			if nextAttemptAt == nil {
				return fmt.Errorf("retry_wait requires next attempt time")
			}
		}
		result, err := dao.NewDeliveryDAO(nil).UpdateResult(ctx, tx, operationID, owner, epoch, status, strings.TrimSpace(providerAssetID), strings.TrimSpace(providerMessageID), string(providerState), strings.TrimSpace(failureCode), int64Ptr(next), updatedAt.Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		if result == 1 {
			return refreshDeliveryIntentStatusTx(ctx, tx, operationID, updatedAt)
		}
		currentRecord, err := dao.NewDeliveryDAO(nil).CurrentResult(ctx, tx, operationID)
		if err != nil {
			if err == dao.ErrNoRows {
				return ErrDeliveryOperationAbsent
			}
			return err
		}
		if currentRecord.Status == status && (status == "uploaded" || status == "delivered" || status == "unsupported" || status == "failed" || status == "uncertain") && currentRecord.ProviderAssetID == strings.TrimSpace(providerAssetID) && currentRecord.ProviderMessageID == strings.TrimSpace(providerMessageID) && currentRecord.FailureCode == strings.TrimSpace(failureCode) && strings.TrimSpace(currentRecord.ProviderState) == strings.TrimSpace(string(providerState)) {
			return nil
		}
		return ErrDeliveryLeaseLost
	})
	return err
}

// UpdateDeliveryOperationProgress persists an in-flight provider phase while
// retaining the current lease. A subsequent terminal update must use the same
// owner and epoch, so a stale worker remains fenced throughout upload/send.
func UpdateDeliveryOperationProgress(ctx context.Context, sessionDir, operationID, owner string, epoch int64, status, providerAssetID, providerMessageID string, providerState json.RawMessage, failureCode string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	operationID = strings.TrimSpace(operationID)
	owner = strings.TrimSpace(owner)
	status = strings.TrimSpace(status)
	if operationID == "" || owner == "" || epoch <= 0 {
		return fmt.Errorf("delivery operation identity and lease are required")
	}
	if status != "uploading" && status != "sending" {
		return fmt.Errorf("invalid in-flight delivery operation status %q", status)
	}
	providerState = normalizedRunJSON(providerState)
	now := time.Now().UTC()
	return WriteRootDatabase(ctx, sessionDir, func(tx *dao.Tx) error {
		result, err := dao.NewDeliveryDAO(nil).UpdateProgress(ctx, tx, operationID, owner, epoch, status, strings.TrimSpace(providerAssetID), strings.TrimSpace(providerMessageID), string(providerState), strings.TrimSpace(failureCode), now.Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		if result != 1 {
			return ErrDeliveryLeaseLost
		}
		return refreshDeliveryIntentStatusTx(ctx, tx, operationID, now)
	})
}

// RequeueDeliveryOperation releases a fenced lease for an explicit retry. It
// is used by recovery when a provider call did not yield a trustworthy result.
func RequeueDeliveryOperation(ctx context.Context, sessionDir, operationID, owner string, epoch int64, nextAttemptAt time.Time, failureCode string) error {
	return UpdateDeliveryOperation(ctx, sessionDir, operationID, owner, epoch, "retry_wait", "", "", nil, failureCode, &nextAttemptAt)
}

// ReopenFailedDeliveryOperation returns an operation that exhausted its retry
// budget to retry_wait and restarts its retry window. Reconnect recovery and an
// explicit operator retry both use it; only a failed operation can be reopened
// so an in-flight or delivered operation is never clobbered.
func ReopenFailedDeliveryOperation(ctx context.Context, sessionDir, operationID string, now time.Time) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(operationID) == "" {
		return false, fmt.Errorf("delivery operation ID is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	reopened := false
	err := WriteRootDatabase(ctx, sessionDir, func(tx *dao.Tx) error {
		changed, err := dao.NewDeliveryDAO(nil).ReopenTransientFailure(ctx, tx, operationID, stamp, stamp)
		if err != nil {
			return err
		}
		if changed != 1 {
			return nil
		}
		reopened = true
		deliveryDAO := dao.NewDeliveryDAO(nil)
		intentID, err := deliveryDAO.IntentID(ctx, tx, operationID)
		if err != nil {
			return err
		}
		// Operations that were terminalized only because this dependency failed
		// must get another chance, otherwise a reopened caption would still be
		// followed by a permanently failed attachment.
		if _, err := deliveryDAO.ReopenDependentFailures(ctx, tx, intentID, stamp, stamp); err != nil {
			return err
		}
		return refreshDeliveryIntentStatusByIDTx(ctx, tx, intentID, now)
	})
	if err != nil {
		return false, err
	}
	return reopened, nil
}

// ListFailedTransientDeliveryOperations returns one platform's operations that
// exhausted their retry budget on a transport-level failure, in delivery order.
// Reconnect recovery uses it to grant another retry window.
func ListFailedTransientDeliveryOperations(ctx context.Context, sessionDir, platform string) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(sessionDir) == "" || strings.TrimSpace(platform) == "" {
		return nil, nil
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	return dao.NewDeliveryDAO(db.Bun()).FailedTransientIDsByPlatform(ctx, db.Bun(), platform)
}

// DeliveryFailure is the operator-facing view of one delivery operation that
// needs attention. It never carries payload, provider state, or transport
// context.
type DeliveryFailure struct {
	OperationID   string
	IntentID      string
	SessionID     string
	RunID         string
	Platform      string
	TargetID      string
	OperationKind string
	Status        string
	FailureCode   string
	AttemptCount  int
	UpdatedAt     time.Time
}

// ListDeliveryFailures lists operations that need operator attention (failed
// or uncertain), optionally narrowed to one session.
func ListDeliveryFailures(ctx context.Context, sessionDir, sessionID string, limit int) ([]DeliveryFailure, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(sessionDir) == "" {
		return nil, nil
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	rows, err := dao.NewDeliveryDAO(db.Bun()).ListFailureRows(ctx, db.Bun(), sessionID, limit)
	if err != nil {
		return nil, err
	}
	failures := make([]DeliveryFailure, 0, len(rows))
	for _, row := range rows {
		failures = append(failures, DeliveryFailure{
			OperationID: row.OperationID, IntentID: row.IntentID, SessionID: row.SessionID, RunID: row.RunID,
			Platform: row.Platform, TargetID: row.TargetID, OperationKind: row.OperationKind,
			Status: row.Status, FailureCode: row.FailureCode, AttemptCount: row.AttemptCount,
			UpdatedAt: parseSessionTimestamp(row.UpdatedAt),
		})
	}
	return failures, nil
}

// RefreshDeliveryIntentStatus recomputes the intent aggregate after an
// operation result. An intent is delivered only when all operations are
// delivered/unsupported; failed and uncertain operations remain visible.
func RefreshDeliveryIntentStatus(ctx context.Context, sessionDir, intentID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return WriteRootDatabase(ctx, sessionDir, func(tx *dao.Tx) error {
		return refreshDeliveryIntentStatusByIDTx(ctx, tx, intentID, time.Now().UTC())
	})
}

// ListDueDeliveryOperations returns recoverable operations whose retry time is
// due or whose previous worker lease has expired. It is intentionally a read;
// callers must claim each row through ClaimDeliveryOperation before executing.
func ListDueDeliveryOperations(ctx context.Context, sessionDir string, now time.Time) ([]DeliveryOperation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	operationIDs, err := dao.NewDeliveryDAO(db.Bun()).DueIDs(ctx, now.UnixMilli())
	if err != nil {
		return nil, err
	}
	var operations []DeliveryOperation
	for _, operationID := range operationIDs {
		operation, err := GetDeliveryOperation(ctx, sessionDir, operationID)
		if err != nil {
			return nil, err
		}
		if operation != nil {
			operations = append(operations, *operation)
		}
	}
	return operations, nil
}

// GetDeliveryOperation loads one operation without exposing transport
// credentials or requiring callers to know its parent intent ID.
func GetDeliveryOperation(ctx context.Context, sessionDir, operationID string) (*DeliveryOperation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	record, err := dao.NewDeliveryDAO(db.Bun()).FindOperation(ctx, db.Bun(), operationID)
	if err == dao.ErrNoRows {
		return nil, ErrDeliveryOperationAbsent
	}
	if err != nil {
		return nil, err
	}
	operation := deliveryOperationFromRecord(record)
	return &operation, nil
}

func refreshDeliveryIntentStatusTx(ctx context.Context, tx *dao.Tx, operationID string, now time.Time) error {
	intentID, err := dao.NewDeliveryDAO(nil).IntentID(ctx, tx, operationID)
	if err != nil {
		return err
	}
	return refreshDeliveryIntentStatusByIDTx(ctx, tx, intentID, now)
}

func refreshDeliveryIntentStatusByIDTx(ctx context.Context, tx *dao.Tx, intentID string, now time.Time) error {
	// A terminal prerequisite can never make its dependent operation valid.
	// Resolve dependent operations before calculating the intent aggregate.
	deliveryDAO := dao.NewDeliveryDAO(nil)
	if err := deliveryDAO.PropagateDependencyFailures(ctx, tx, intentID, now.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	total, terminal, failed, uncertain, err := deliveryDAO.Aggregate(ctx, tx, intentID)
	if err != nil {
		return err
	}
	resolved := terminal + failed + uncertain
	status := "pending"
	switch {
	case total > 0 && terminal == total:
		status = "delivered"
	case failed > 0 && resolved == total:
		status = "failed"
	case uncertain > 0 && resolved == total:
		status = "uncertain"
	}
	return deliveryDAO.UpdateIntentStatus(ctx, tx, intentID, status, now.Format(time.RFC3339Nano))
}

func validDeliveryOperationStatus(status string) bool {
	switch status {
	case "pending", "uploading", "uploaded", "sending", "retry_wait", "delivered", "unsupported", "failed", "uncertain":
		return true
	default:
		return false
	}
}

func deliveryIntentFromRecord(record *dao.DeliveryIntentRecord) DeliveryIntent {
	return DeliveryIntent{ID: record.ID, SessionID: record.SessionID, RunID: record.RunID, Platform: record.Platform, TargetID: record.TargetID, ReplyMessageID: record.ReplyMessageID, TransportContext: json.RawMessage(record.TransportContext), Status: record.Status, CreatedAt: parseSessionTimestamp(record.CreatedAt), UpdatedAt: parseSessionTimestamp(record.UpdatedAt)}
}

func deliveryOperationFromRecord(record *dao.DeliveryOperationRecord) DeliveryOperation {
	operation := DeliveryOperation{ID: record.ID, IntentID: record.IntentID, OperationKey: record.OperationKey, ArtifactID: stringValue(record.ArtifactID), OperationKind: record.OperationKind, Sequence: record.Sequence, DependsOn: stringValue(record.DependsOn), IdempotencyKey: record.IdempotencyKey, PayloadDigest: record.PayloadDigest, Status: record.Status, ProviderAssetID: record.ProviderAssetID, ProviderMessageID: record.ProviderMessageID, ProviderState: json.RawMessage(record.ProviderState), AttemptCount: record.AttemptCount, FailureCode: record.FailureCode, LeaseOwner: record.LeaseOwner, LeaseEpoch: record.LeaseEpoch, CreatedAt: parseSessionTimestamp(record.CreatedAt), UpdatedAt: parseSessionTimestamp(record.UpdatedAt)}
	if record.NextAttemptAt != nil {
		value := time.UnixMilli(*record.NextAttemptAt)
		operation.NextAttemptAt = &value
	}
	if record.LeaseExpiresAt != nil {
		value := time.UnixMilli(*record.LeaseExpiresAt)
		operation.LeaseExpiresAt = &value
	}
	if record.RetryWindowStartedAt != nil && *record.RetryWindowStartedAt != "" {
		value := parseSessionTimestamp(*record.RetryWindowStartedAt)
		operation.RetryWindowStartedAt = &value
	}
	return operation
}

func int64Ptr(value any) *int64 {
	if value == nil {
		return nil
	}
	v, ok := value.(int64)
	if !ok {
		return nil
	}
	return &v
}
