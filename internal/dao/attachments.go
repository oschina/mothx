package dao

import (
	"context"
	"database/sql"
	"strings"

	"github.com/uptrace/bun"
)

type AttachmentRecord struct {
	bun.BaseModel `bun:"table:session_attachments"`
	ID            string `bun:"id,pk"`
	SessionID     string `bun:"session_id"`
	RunID         string `bun:"run_id"`
	Origin        string `bun:"origin"`
	Kind          string `bun:"kind"`
	Filename      string `bun:"filename"`
	MediaType     string `bun:"media_type"`
	Bytes         int64  `bun:"byte_size"`
	SHA256        string `bun:"sha256"`
	StorageKey    string `bun:"storage_key"`
	Status        string `bun:"status"`
	CreatedAt     string `bun:"created_at"`
	ExpiresAt     string `bun:"expires_at"`
	Metadata      string `bun:"metadata"`
}

type AttachmentDAO struct{ db *bun.DB }

func NewAttachmentDAO(db *bun.DB) *AttachmentDAO { return &AttachmentDAO{db: db} }

func (d *AttachmentDAO) Insert(ctx context.Context, executor bun.IDB, record *AttachmentRecord) error {
	_, err := executor.NewInsert().Model(record).Exec(ctx)
	return err
}

func (d *AttachmentDAO) Find(ctx context.Context, sessionID, attachmentID string) (*AttachmentRecord, error) {
	record := new(AttachmentRecord)
	err := d.db.NewSelect().Model(record).Where("session_id = ? AND id = ?", sessionID, attachmentID).Limit(1).Scan(ctx)
	return record, err
}

// ListBySessionStatus returns every attachment row of one session filtered by
// lifecycle status in durable creation order. It backs read-only replay
// projections such as generated-artifact listing; content stays in the
// Runtime-owned private store.
func (d *AttachmentDAO) ListBySessionStatus(ctx context.Context, sessionID, status string) ([]AttachmentRecord, error) {
	var records []AttachmentRecord
	err := d.db.NewSelect().Model(&records).Where("session_id = ? AND status = ?", sessionID, status).OrderExpr("created_at ASC, id ASC").Scan(ctx)
	return records, err
}

// ListBySession returns attachment rows of one session in durable creation
// order, optionally filtered by lifecycle status. An empty status returns
// every row of the session. It backs metadata-only listing projections; the
// content bytes stay in the Runtime-owned private store.
func (d *AttachmentDAO) ListBySession(ctx context.Context, sessionID, status string) ([]AttachmentRecord, error) {
	var records []AttachmentRecord
	query := d.db.NewSelect().Model(&records).Where("session_id = ?", sessionID)
	if status = strings.TrimSpace(status); status != "" {
		query = query.Where("status = ?", status)
	}
	err := query.OrderExpr("created_at ASC, id ASC").Scan(ctx)
	return records, err
}

// ListStorageReferences returns the ID and storage key of every attachment row
// in the database, across sessions and regardless of lifecycle status. It is the
// durable side of the private-store reconciliation: the Runtime compares the
// artifact directories it finds on disk against this set, so a row in any status
// keeps its bytes referenced. An expired row still protects its content until
// CleanupExpired removes both together.
func (d *AttachmentDAO) ListStorageReferences(ctx context.Context, executor bun.IDB) ([]AttachmentStorageReference, error) {
	var records []AttachmentStorageReference
	err := executor.NewSelect().Table("session_attachments").
		Column("id", "storage_key").
		OrderExpr("id ASC").
		Scan(ctx, &records)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return records, err
}

// AttachmentStorageReference is the minimal projection the private-store
// reconciliation needs: which artifact object a durable row still claims.
type AttachmentStorageReference struct {
	ID         string `bun:"id"`
	StorageKey string `bun:"storage_key"`
}

func (d *AttachmentDAO) Expired(ctx context.Context, executor bun.IDB, now string) ([]AttachmentRecord, error) {
	var records []AttachmentRecord
	err := executor.NewSelect().Model(&records).Column("id", "storage_key").Where("expires_at <= ?", now).Scan(ctx)
	return records, err
}

func (d *AttachmentDAO) MarkExpired(ctx context.Context, executor bun.IDB, now string) error {
	_, err := executor.NewUpdate().Model((*AttachmentRecord)(nil)).Set("status = ?", "expired").Where("expires_at <= ? AND status != ?", now, "expired").Exec(ctx)
	return err
}

func (d *AttachmentDAO) SetStatus(ctx context.Context, executor bun.IDB, sessionID, attachmentID, status string) (int64, error) {
	result, err := executor.NewUpdate().Model((*AttachmentRecord)(nil)).Set("status = ?", status).Where("session_id = ? AND id = ?", sessionID, attachmentID).Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func IsNoRowsAttachment(err error) bool { return err == sql.ErrNoRows }
