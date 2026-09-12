package dao

import (
	"context"
	"database/sql"

	"github.com/uptrace/bun"
)

type RuntimeLeaseRecord struct {
	bun.BaseModel `bun:"table:session_runtime_leases"`
	SessionID     string `bun:"session_id,pk"`
	OwnerID       string `bun:"owner_instance_id"`
	OwnerPID      int    `bun:"owner_pid"`
	OwnerKind     string `bun:"owner_kind"`
	TokenHash     string `bun:"lease_token_hash"`
	Epoch         int64  `bun:"epoch"`
	RunID         string `bun:"run_id"`
	Purpose       string `bun:"purpose"`
	State         string `bun:"state"`
	AcquiredAt    int64  `bun:"acquired_at"`
	HeartbeatAt   int64  `bun:"heartbeat_at"`
	ExpiresAt     int64  `bun:"expires_at"`
	UpdatedAt     int64  `bun:"updated_at"`
}

type RuntimeLeaseDAO struct{ db *bun.DB }

func NewRuntimeLeaseDAO(db *bun.DB) *RuntimeLeaseDAO { return &RuntimeLeaseDAO{db: db} }

func (d *RuntimeLeaseDAO) Now(ctx context.Context, executor bun.IDB) (int64, error) {
	var now int64
	err := executor.NewSelect().ColumnExpr("CAST(strftime('%s','now') AS INTEGER)").Scan(ctx, &now)
	return now, err
}

func (d *RuntimeLeaseDAO) SessionExists(ctx context.Context, executor bun.IDB, sessionID string) (bool, error) {
	var id string
	err := executor.NewSelect().Table("sessions").Column("id").Where("id = ?", sessionID).Limit(1).Scan(ctx, &id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (d *RuntimeLeaseDAO) Find(ctx context.Context, executor bun.IDB, sessionID string) (*RuntimeLeaseRecord, error) {
	record := new(RuntimeLeaseRecord)
	err := executor.NewSelect().Model(record).Where("session_id = ?", sessionID).Limit(1).Scan(ctx)
	return record, err
}

func (d *RuntimeLeaseDAO) Insert(ctx context.Context, executor bun.IDB, record *RuntimeLeaseRecord) error {
	_, err := executor.NewInsert().Model(record).Exec(ctx)
	return err
}

func (d *RuntimeLeaseDAO) Acquire(ctx context.Context, executor bun.IDB, record *RuntimeLeaseRecord, previousEpoch, now int64) (int64, error) {
	result, err := executor.NewUpdate().Model((*RuntimeLeaseRecord)(nil)).
		Set("owner_instance_id = ?", record.OwnerID).Set("owner_pid = ?", record.OwnerPID).Set("owner_kind = ?", record.OwnerKind).
		Set("lease_token_hash = ?", record.TokenHash).Set("epoch = ?", record.Epoch).Set("run_id = ?", record.RunID).
		Set("purpose = ?", record.Purpose).Set("state = ?", "active").Set("acquired_at = ?", now).
		Set("heartbeat_at = ?", now).Set("expires_at = ?", record.ExpiresAt).Set("updated_at = ?", now).
		Where("session_id = ? AND epoch = ? AND (state != ? OR expires_at <= ?)", record.SessionID, previousEpoch, "active", now).Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (d *RuntimeLeaseDAO) ActiveRunIDs(ctx context.Context, executor bun.IDB, sessionID string, statuses []string) ([]string, error) {
	var ids []string
	err := executor.NewSelect().Table("session_runs").Column("id").Where("session_id = ?", sessionID).Where("status IN (?)", bun.In(statuses)).OrderExpr("started_at DESC").Scan(ctx, &ids)
	return ids, err
}

// Renew extends the lease owned by the caller. Matching owner, epoch, and token
// is the fencing condition: a competing process can only take over through
// Acquire, which bumps the epoch, so an expired row that still carries our
// identity is still ours and must stay renewable. Requiring expires_at > now
// here would permanently kill a live owner after any stall longer than the TTL
// even though no other process ever took the lease.
func (d *RuntimeLeaseDAO) Renew(ctx context.Context, record *RuntimeLeaseRecord, ttl int64) (int64, error) {
	result, err := d.db.NewUpdate().Model((*RuntimeLeaseRecord)(nil)).
		Set("heartbeat_at = CAST(strftime('%s','now') AS INTEGER)").
		Set("expires_at = CAST(strftime('%s','now') AS INTEGER) + ?", ttl).
		Set("updated_at = CAST(strftime('%s','now') AS INTEGER)").
		Where("session_id = ? AND owner_instance_id = ? AND epoch = ? AND lease_token_hash = ? AND state = ?", record.SessionID, record.OwnerID, record.Epoch, record.TokenHash, "active").Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (d *RuntimeLeaseDAO) Release(ctx context.Context, record *RuntimeLeaseRecord) (int64, error) {
	result, err := d.db.NewUpdate().Model((*RuntimeLeaseRecord)(nil)).
		Set("state = ?", "released").Set("expires_at = CAST(strftime('%s','now') AS INTEGER)").
		Set("heartbeat_at = CAST(strftime('%s','now') AS INTEGER)").Set("updated_at = CAST(strftime('%s','now') AS INTEGER)").
		Where("session_id = ? AND owner_instance_id = ? AND epoch = ? AND lease_token_hash = ? AND state = ?", record.SessionID, record.OwnerID, record.Epoch, record.TokenHash, "active").Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (d *RuntimeLeaseDAO) Exists(ctx context.Context, executor bun.IDB, sessionID string) (bool, error) {
	var value int
	err := executor.NewSelect().Table("session_runtime_leases").ColumnExpr("1").Where("session_id = ?", sessionID).Limit(1).Scan(ctx, &value)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (d *RuntimeLeaseDAO) Bind(ctx context.Context, executor bun.IDB, sessionID, ownerID string, epoch int64, tokenHash, runID string, purposes []string) (int64, error) {
	result, err := executor.NewUpdate().Model((*RuntimeLeaseRecord)(nil)).Set("run_id = ?", runID).Set("purpose = ?", "execution").Set("updated_at = CAST(strftime('%s','now') AS INTEGER)").Where("session_id = ? AND owner_instance_id = ? AND epoch = ? AND lease_token_hash = ? AND state = ? AND expires_at > CAST(strftime('%s','now') AS INTEGER) AND purpose IN (?) AND (run_id = '' OR run_id = ?)", sessionID, ownerID, epoch, tokenHash, "active", bun.In(purposes), runID).Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (d *RuntimeLeaseDAO) Binding(ctx context.Context, executor bun.IDB, sessionID, ownerID string, epoch int64, tokenHash string) (*RuntimeLeaseRecord, error) {
	record := new(RuntimeLeaseRecord)
	err := executor.NewSelect().Model(record).Where("session_id = ? AND owner_instance_id = ? AND epoch = ? AND lease_token_hash = ? AND state = ? AND expires_at > CAST(strftime('%s','now') AS INTEGER)", sessionID, ownerID, epoch, tokenHash, "active").Limit(1).Scan(ctx)
	return record, err
}

func (d *RuntimeLeaseDAO) RunStatus(ctx context.Context, executor bun.IDB, runID, sessionID string) (string, error) {
	var status string
	err := executor.NewSelect().Table("session_runs").Column("status").Where("id = ? AND session_id = ?", runID, sessionID).Limit(1).Scan(ctx, &status)
	return status, err
}
