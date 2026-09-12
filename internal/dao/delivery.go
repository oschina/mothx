package dao

import (
	"context"
	"database/sql"
	"time"

	"github.com/uptrace/bun"
)

type DeliveryIntentRecord struct {
	bun.BaseModel    `bun:"table:delivery_intents"`
	ID               string `bun:"id,pk"`
	SessionID        string `bun:"session_id"`
	RunID            string `bun:"run_id"`
	Platform         string `bun:"platform"`
	TargetID         string `bun:"target_id"`
	ReplyMessageID   string `bun:"reply_message_id"`
	TransportContext string `bun:"transport_context"`
	Status           string `bun:"status"`
	CreatedAt        string `bun:"created_at"`
	UpdatedAt        string `bun:"updated_at"`
}

type DeliveryOperationRecord struct {
	bun.BaseModel     `bun:"table:delivery_operations"`
	ID                string  `bun:"id,pk"`
	IntentID          string  `bun:"intent_id"`
	OperationKey      string  `bun:"operation_key"`
	ArtifactID        *string `bun:"artifact_id,nullzero"`
	OperationKind     string  `bun:"operation_kind"`
	Sequence          int     `bun:"sequence"`
	DependsOn         *string `bun:"depends_on,nullzero"`
	IdempotencyKey    string  `bun:"idempotency_key"`
	PayloadDigest     string  `bun:"payload_digest"`
	Status            string  `bun:"status"`
	ProviderAssetID   string  `bun:"provider_asset_id"`
	ProviderMessageID string  `bun:"provider_message_id"`
	ProviderState     string  `bun:"provider_state"`
	AttemptCount      int     `bun:"attempt_count"`
	NextAttemptAt     *int64  `bun:"next_attempt_at,nullzero"`
	FailureCode       string  `bun:"failure_code"`
	// RetryWindowStartedAt restarts the transient retry budget after an explicit
	// retry. NULL means the budget started at CreatedAt.
	RetryWindowStartedAt *string `bun:"retry_window_started_at,nullzero"`
	LeaseOwner           string  `bun:"lease_owner"`
	LeaseEpoch           int64   `bun:"lease_epoch"`
	LeaseExpiresAt       *int64  `bun:"lease_expires_at,nullzero"`
	CreatedAt            string  `bun:"created_at"`
	UpdatedAt            string  `bun:"updated_at"`
}

type DeliveryDAO struct{ db *bun.DB }

func NewDeliveryDAO(db *bun.DB) *DeliveryDAO { return &DeliveryDAO{db: db} }

func (d *DeliveryDAO) RunExists(ctx context.Context, executor bun.IDB, sessionID, runID string) (bool, error) {
	var id string
	err := executor.NewSelect().Table("session_runs").Column("id").Where("id = ? AND session_id = ?", runID, sessionID).Limit(1).Scan(ctx, &id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (d *DeliveryDAO) AttachmentExists(ctx context.Context, executor bun.IDB, sessionID, runID, artifactID string) (bool, error) {
	var id string
	err := executor.NewSelect().Table("session_attachments").Column("id").Where("id = ? AND session_id = ? AND run_id = ?", artifactID, sessionID, runID).Limit(1).Scan(ctx, &id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (d *DeliveryDAO) InsertIntent(ctx context.Context, executor bun.IDB, record *DeliveryIntentRecord) (int64, error) {
	result, err := executor.NewInsert().Model(record).On("CONFLICT(run_id, platform, target_id) DO NOTHING").Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (d *DeliveryDAO) FindIntentByKey(ctx context.Context, executor bun.IDB, runID, platform, targetID string) (*DeliveryIntentRecord, error) {
	record := new(DeliveryIntentRecord)
	err := executor.NewSelect().Model(record).Where("run_id = ? AND platform = ? AND target_id = ?", runID, platform, targetID).Limit(1).Scan(ctx)
	return record, err
}

func (d *DeliveryDAO) FindIntent(ctx context.Context, executor bun.IDB, intentID string) (*DeliveryIntentRecord, error) {
	record := new(DeliveryIntentRecord)
	err := executor.NewSelect().Model(record).Where("id = ?", intentID).Limit(1).Scan(ctx)
	return record, err
}

func (d *DeliveryDAO) InsertOperation(ctx context.Context, executor bun.IDB, record *DeliveryOperationRecord) (int64, error) {
	result, err := executor.NewInsert().Model(record).On("CONFLICT(intent_id, operation_key) DO NOTHING").Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (d *DeliveryDAO) FindOperationByKey(ctx context.Context, executor bun.IDB, intentID, key string) (*DeliveryOperationRecord, error) {
	record := new(DeliveryOperationRecord)
	err := executor.NewSelect().Model(record).Where("intent_id = ? AND operation_key = ?", intentID, key).Limit(1).Scan(ctx)
	return record, err
}

func (d *DeliveryDAO) ListOperations(ctx context.Context, executor bun.IDB, intentID string) ([]DeliveryOperationRecord, error) {
	var records []DeliveryOperationRecord
	err := executor.NewSelect().Model(&records).Where("intent_id = ?", intentID).OrderExpr("sequence ASC").Scan(ctx)
	return records, err
}

func (d *DeliveryDAO) DependencyStatus(ctx context.Context, executor bun.IDB, operationID string) (string, string, error) {
	var row struct {
		IntentStatus string `bun:"intent_status"`
		DependsOn    string `bun:"depends_on"`
	}
	err := executor.NewSelect().TableExpr("delivery_operations AS o").Join("JOIN delivery_intents AS i ON i.id = o.intent_id").ColumnExpr("i.status AS intent_status, COALESCE(o.depends_on, '') AS depends_on").Where("o.id = ?", operationID).Limit(1).Scan(ctx, &row)
	return row.IntentStatus, row.DependsOn, err
}

func (d *DeliveryDAO) Claim(ctx context.Context, executor bun.IDB, operationID, owner string, now, expires int64) (int64, error) {
	result, err := executor.NewRaw(`UPDATE delivery_operations SET lease_owner = ?, lease_epoch = lease_epoch + 1,
		lease_expires_at = ?, attempt_count = attempt_count + 1, updated_at = ?
		WHERE id = ? AND status IN ('pending', 'uploading', 'sending', 'retry_wait')
		AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
		AND (lease_owner = '' OR lease_expires_at IS NULL OR lease_expires_at <= ?)
		AND (depends_on IS NULL OR depends_on = '' OR EXISTS (SELECT 1 FROM delivery_operations dependency
			WHERE dependency.id = delivery_operations.depends_on
			AND dependency.intent_id = delivery_operations.intent_id
			AND dependency.status IN ('uploaded', 'delivered', 'unsupported')))`, owner, expires,
		time.UnixMilli(now).UTC().Format(time.RFC3339Nano), operationID, now, now).Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (d *DeliveryDAO) FindOperation(ctx context.Context, executor bun.IDB, operationID string) (*DeliveryOperationRecord, error) {
	record := new(DeliveryOperationRecord)
	err := executor.NewSelect().Model(record).Where("id = ?", operationID).Limit(1).Scan(ctx)
	return record, err
}

func (d *DeliveryDAO) UpdateResult(ctx context.Context, executor bun.IDB, operationID, owner string, epoch int64, status, assetID, messageID, state, failure string, next *int64, updatedAt string) (int64, error) {
	result, err := executor.NewUpdate().Model((*DeliveryOperationRecord)(nil)).Set("status = ?", status).
		Set("provider_asset_id = ?", assetID).Set("provider_message_id = ?", messageID).
		Set("provider_state = ?", state).Set("failure_code = ?", failure).Set("next_attempt_at = ?", next).
		Set("lease_owner = ''").Set("lease_expires_at = NULL").Set("updated_at = ?", updatedAt).
		Where("id = ? AND lease_owner = ? AND lease_epoch = ?", operationID, owner, epoch).Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (d *DeliveryDAO) CurrentResult(ctx context.Context, executor bun.IDB, operationID string) (*DeliveryOperationRecord, error) {
	record := new(DeliveryOperationRecord)
	err := executor.NewSelect().Model(record).Column("status", "provider_asset_id", "provider_message_id", "provider_state", "failure_code").Where("id = ?", operationID).Limit(1).Scan(ctx)
	return record, err
}

func (d *DeliveryDAO) UpdateProgress(ctx context.Context, executor bun.IDB, operationID, owner string, epoch int64, status, assetID, messageID, state, failure, updatedAt string) (int64, error) {
	result, err := executor.NewUpdate().Model((*DeliveryOperationRecord)(nil)).Set("status = ?", status).
		Set("provider_asset_id = ?", assetID).Set("provider_message_id = ?", messageID).Set("provider_state = ?", state).
		Set("failure_code = ?", failure).Set("updated_at = ?", updatedAt).
		Where("id = ? AND lease_owner = ? AND lease_epoch = ?", operationID, owner, epoch).Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// TransientDeliveryFailureCodes returns the canonical set of delivery failure
// codes that may be reopened for another retry window (reconnect recovery or an
// explicit operator retry). Only transport-level failures qualify: a permanent
// platform rejection (for example a WeChat 4xx error code) or a broken recovery
// projection must stay failed instead of being retried forever.
//
// This is the single definition behind the ReopenTransientFailure SQL fence and
// the runtime/operator predicate (agentruntime.DeliveryFailureRetryable), so the
// operator entry and the persistence guard cannot drift apart.
func TransientDeliveryFailureCodes() []string {
	return []string{"delivery_retries_exhausted", "transport_error"}
}

// IsTransientDeliveryFailure reports whether a failed operation with this code
// may be reopened.
func IsTransientDeliveryFailure(failureCode string) bool {
	for _, code := range TransientDeliveryFailureCodes() {
		if code == failureCode {
			return true
		}
	}
	return false
}

// ReopenTransientFailure returns an exhausted operation to retry_wait and
// restarts its retry window. It is the reconnect/operator recovery path for
// operations that were abandoned after their budget ran out.
//
// The failure_code fence mirrors TransientDeliveryFailureCodes, which is also
// the predicate the ACP operator entry uses to explain a refusal.
func (d *DeliveryDAO) ReopenTransientFailure(ctx context.Context, executor bun.IDB, operationID, windowStartedAt, updatedAt string) (int64, error) {
	result, err := executor.NewUpdate().Model((*DeliveryOperationRecord)(nil)).
		Set("status = ?", "retry_wait").
		Set("failure_code = ''").
		Set("next_attempt_at = NULL").
		Set("retry_window_started_at = ?", windowStartedAt).
		Set("updated_at = ?", updatedAt).
		Where("id = ? AND status = ?", operationID, "failed").
		Where("failure_code IN (?)", bun.In(TransientDeliveryFailureCodes())).Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// FailedTransientIDsByPlatform lists one platform's operations that exhausted
// their retry budget on a transport-level failure, in delivery order.
func (d *DeliveryDAO) FailedTransientIDsByPlatform(ctx context.Context, executor bun.IDB, platform string) ([]string, error) {
	var ids []string
	err := executor.NewSelect().TableExpr("delivery_operations AS o").
		Column("o.id").
		Join("JOIN delivery_intents AS i ON i.id = o.intent_id").
		Where("i.platform = ?", platform).
		Where("o.status = ?", "failed").
		Where("o.failure_code IN (?)", bun.In(TransientDeliveryFailureCodes())).
		OrderExpr("o.sequence ASC, o.created_at ASC").
		Scan(ctx, &ids)
	return ids, err
}

// DeliveryFailureRecord is the operator-facing projection of a delivery
// operation that needs attention (failed or uncertain).
type DeliveryFailureRecord struct {
	OperationID   string `bun:"operation_id"`
	IntentID      string `bun:"intent_id"`
	SessionID     string `bun:"session_id"`
	RunID         string `bun:"run_id"`
	Platform      string `bun:"platform"`
	TargetID      string `bun:"target_id"`
	OperationKind string `bun:"operation_kind"`
	Status        string `bun:"status"`
	FailureCode   string `bun:"failure_code"`
	AttemptCount  int    `bun:"attempt_count"`
	UpdatedAt     string `bun:"updated_at"`
}

// deliveryFailureLimitMax caps one failure projection so a long-broken platform
// cannot flood the caller.
const deliveryFailureLimitMax = 200

// ListFailureRows lists delivery operations that need operator attention, most
// recently updated first. An empty sessionID lists every session in the
// database.
func (d *DeliveryDAO) ListFailureRows(ctx context.Context, executor bun.IDB, sessionID string, limit int) ([]DeliveryFailureRecord, error) {
	if limit <= 0 || limit > deliveryFailureLimitMax {
		limit = deliveryFailureLimitMax
	}
	query := executor.NewSelect().TableExpr("delivery_operations AS o").
		ColumnExpr("o.id AS operation_id, o.intent_id, i.session_id, i.run_id, i.platform, i.target_id, o.operation_kind, o.status, o.failure_code, o.attempt_count, o.updated_at").
		Join("JOIN delivery_intents AS i ON i.id = o.intent_id").
		Where("o.status IN (?)", bun.In([]string{"failed", "uncertain"}))
	if sessionID != "" {
		query = query.Where("i.session_id = ?", sessionID)
	}
	var rows []DeliveryFailureRecord
	err := query.OrderExpr("o.updated_at DESC").Limit(limit).Scan(ctx, &rows)
	return rows, err
}

// ReopenDependentFailures returns operations that were terminalized only
// because this intent's dependency failed. They can run again once the
// dependency succeeds; the claim path still refuses them while any prerequisite
// is unfinished, so an early reopen converges instead of sending out of order.
func (d *DeliveryDAO) ReopenDependentFailures(ctx context.Context, executor bun.IDB, intentID, windowStartedAt, updatedAt string) (int64, error) {
	result, err := executor.NewUpdate().Model((*DeliveryOperationRecord)(nil)).
		Set("status = ?", "retry_wait").
		Set("failure_code = ''").
		Set("next_attempt_at = NULL").
		Set("retry_window_started_at = ?", windowStartedAt).
		Set("updated_at = ?", updatedAt).
		Where("intent_id = ? AND status = ? AND failure_code = ?", intentID, "failed", "dependency_failed").Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (d *DeliveryDAO) DueIDs(ctx context.Context, now int64) ([]string, error) {
	var ids []string
	err := d.db.NewSelect().Table("delivery_operations").Column("id").Where("status IN (?)", bun.In([]string{"pending", "uploading", "sending", "retry_wait"})).Where("(next_attempt_at IS NULL OR next_attempt_at <= ?)", now).Where("(lease_owner = '' OR lease_expires_at IS NULL OR lease_expires_at <= ?)", now).OrderExpr("sequence ASC, created_at ASC").Scan(ctx, &ids)
	return ids, err
}

func (d *DeliveryDAO) IntentID(ctx context.Context, executor bun.IDB, operationID string) (string, error) {
	var id string
	err := executor.NewSelect().Table("delivery_operations").Column("intent_id").Where("id = ?", operationID).Limit(1).Scan(ctx, &id)
	return id, err
}

func (d *DeliveryDAO) Aggregate(ctx context.Context, executor bun.IDB, intentID string) (total, terminal, failed, uncertain int, err error) {
	var row struct {
		Total     int `bun:"total"`
		Terminal  int `bun:"terminal"`
		Failed    int `bun:"failed"`
		Uncertain int `bun:"uncertain"`
	}
	err = executor.NewSelect().Table("delivery_operations").ColumnExpr("COUNT(*) AS total, SUM(CASE WHEN status IN ('uploaded','delivered','unsupported') THEN 1 ELSE 0 END) AS terminal, SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END) AS failed, SUM(CASE WHEN status = 'uncertain' THEN 1 ELSE 0 END) AS uncertain").Where("intent_id = ?", intentID).Scan(ctx, &row)
	return row.Total, row.Terminal, row.Failed, row.Uncertain, err
}

// PropagateDependencyFailures terminalizes operations that can no longer run
// because a prerequisite failed or became uncertain. The loop handles chains
// of dependencies in one transaction, so callers can aggregate a consistent
// intent state afterward.
func (d *DeliveryDAO) PropagateDependencyFailures(ctx context.Context, executor bun.IDB, intentID, updatedAt string) error {
	for {
		result, err := executor.NewRaw(`UPDATE delivery_operations
			SET status = CASE dependency.status WHEN 'uncertain' THEN 'uncertain' ELSE 'failed' END,
				failure_code = CASE dependency.status WHEN 'uncertain' THEN 'dependency_uncertain' ELSE 'dependency_failed' END,
				next_attempt_at = NULL, lease_owner = '', lease_expires_at = NULL, updated_at = ?
			FROM delivery_operations dependency
			WHERE delivery_operations.intent_id = ?
			  AND delivery_operations.depends_on = dependency.id
			  AND dependency.intent_id = delivery_operations.intent_id
			  AND dependency.status IN ('failed', 'uncertain')
			  AND delivery_operations.status IN ('pending', 'retry_wait')`, updatedAt, intentID).Exec(ctx)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed == 0 {
			return nil
		}
	}
}

func (d *DeliveryDAO) UpdateIntentStatus(ctx context.Context, executor bun.IDB, intentID, status, updatedAt string) error {
	_, err := executor.NewUpdate().Model((*DeliveryIntentRecord)(nil)).Set("status = ?", status).Set("updated_at = ?", updatedAt).Where("id = ?", intentID).Exec(ctx)
	return err
}
