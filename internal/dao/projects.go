package dao

import (
	"context"
	"database/sql"
	"time"

	"github.com/uptrace/bun"
)

type ProjectRecord struct {
	bun.BaseModel `bun:"table:projects"`
	ID            string `bun:"id,pk"`
	Name          string `bun:"name"`
	CreatedAt     string `bun:"created_at"`
	UpdatedAt     string `bun:"updated_at"`
}

type SessionMetadataRecord struct {
	bun.BaseModel `bun:"table:session_metadata"`
	SessionID     string  `bun:"session_id,pk"`
	ProjectID     *string `bun:"project_id,nullzero"`
	Pinned        int     `bun:"pinned"`
	UpdatedAt     string  `bun:"updated_at"`
}

type ProjectDAO struct{ db *bun.DB }

func NewProjectDAO(db *bun.DB) *ProjectDAO { return &ProjectDAO{db: db} }

func (d *ProjectDAO) List(ctx context.Context) ([]ProjectRecord, error) {
	var records []ProjectRecord
	err := d.db.NewSelect().Model(&records).
		OrderExpr("updated_at DESC, name COLLATE NOCASE").Scan(ctx)
	return records, err
}

func (d *ProjectDAO) Insert(ctx context.Context, record *ProjectRecord) error {
	_, err := d.db.NewInsert().Model(record).Exec(ctx)
	return err
}

func (d *ProjectDAO) UpdateName(ctx context.Context, id, name, updatedAt string) (int64, error) {
	result, err := d.db.NewUpdate().Model((*ProjectRecord)(nil)).
		Set("name = ?", name).Set("updated_at = ?", updatedAt).
		Where("id = ?", id).Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (d *ProjectDAO) Delete(ctx context.Context, id string) error {
	_, err := d.db.NewDelete().Model((*ProjectRecord)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}

// DeleteWithMetadata atomically realizes the schema's ON DELETE SET NULL
// semantics even when SQLite foreign-key enforcement is disabled.
func (d *ProjectDAO) DeleteWithMetadata(ctx context.Context, executor bun.IDB, id string) error {
	if _, err := executor.NewUpdate().Model((*SessionMetadataRecord)(nil)).
		Set("project_id = NULL").
		Where("project_id = ?", id).
		Exec(ctx); err != nil {
		return err
	}
	_, err := executor.NewDelete().Model((*ProjectRecord)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}

func (d *ProjectDAO) Exists(ctx context.Context, id string) (bool, error) {
	return d.ExistsWith(ctx, d.db, id)
}

func (d *ProjectDAO) ExistsWith(ctx context.Context, executor bun.IDB, id string) (bool, error) {
	var value int
	err := executor.NewSelect().Model((*ProjectRecord)(nil)).ColumnExpr("1").Where("id = ?", id).Limit(1).Scan(ctx, &value)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (d *ProjectDAO) UpsertMetadata(ctx context.Context, record *SessionMetadataRecord) error {
	_, err := d.db.NewInsert().Model(record).
		On("CONFLICT(session_id) DO UPDATE SET project_id = excluded.project_id, pinned = excluded.pinned, updated_at = excluded.updated_at").
		Exec(ctx)
	return err
}

// UpsertMetadataIfReferencesExist performs the reference checks and write in
// one SQLite statement. This prevents a concurrent project deletion from
// committing between an application-level existence check and the upsert.
func (d *ProjectDAO) UpsertMetadataIfReferencesExist(ctx context.Context, executor bun.IDB, record *SessionMetadataRecord) (int64, error) {
	var projectID any
	if record.ProjectID != nil {
		projectID = *record.ProjectID
	}
	result, err := executor.NewRaw(`INSERT INTO session_metadata (session_id, project_id, pinned, updated_at)
		SELECT ?, ?, ?, ?
		WHERE EXISTS (SELECT 1 FROM sessions WHERE id = ?)
		  AND (? IS NULL OR EXISTS (SELECT 1 FROM projects WHERE id = ?))
		ON CONFLICT(session_id) DO UPDATE SET
		  project_id = excluded.project_id,
		  pinned = excluded.pinned,
		  updated_at = excluded.updated_at`,
		record.SessionID, projectID, record.Pinned, record.UpdatedAt,
		record.SessionID, projectID, projectID).Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (d *ProjectDAO) Metadata(ctx context.Context, sessionID string) (*SessionMetadataRecord, error) {
	record := new(SessionMetadataRecord)
	err := d.db.NewSelect().Model(record).Where("session_id = ?", sessionID).Limit(1).Scan(ctx)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return record, err
}

// MetadataForSessions returns the persisted metadata rows of the given
// sessions in one read-only query, in stable session order. Sessions without
// a row are simply absent from the result.
func (d *ProjectDAO) MetadataForSessions(ctx context.Context, sessionIDs []string) ([]SessionMetadataRecord, error) {
	var records []SessionMetadataRecord
	if len(sessionIDs) == 0 {
		return records, nil
	}
	err := d.db.NewSelect().Model(&records).
		Where("session_id IN (?)", bun.In(sessionIDs)).
		OrderExpr("session_id ASC").
		Scan(ctx)
	return records, err
}

// SessionCountsByProject counts how many session metadata rows reference each
// project. It backs the optional sessionCount projection of project listings.
func (d *ProjectDAO) SessionCountsByProject(ctx context.Context) (map[string]int, error) {
	var rows []struct {
		ProjectID string `bun:"project_id"`
		Count     int    `bun:"count"`
	}
	err := d.db.NewSelect().Table("session_metadata").
		ColumnExpr("project_id, COUNT(*) AS count").
		Where("project_id IS NOT NULL AND project_id != ''").
		Group("project_id").
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(rows))
	for _, row := range rows {
		counts[row.ProjectID] = row.Count
	}
	return counts, nil
}

// ClearMetadataProject detaches every session metadata row from one project.
// It realizes the ON DELETE SET NULL reference semantics declared by the
// session_metadata schema regardless of SQLite foreign-key enforcement, so a
// deleted project never leaves dangling assignments behind.
func (d *ProjectDAO) ClearMetadataProject(ctx context.Context, projectID string) error {
	_, err := d.db.NewUpdate().Model((*SessionMetadataRecord)(nil)).
		Set("project_id = NULL").
		Where("project_id = ?", projectID).
		Exec(ctx)
	return err
}

func (d *ProjectDAO) LatestSessionInfoData(ctx context.Context, sessionID string) (string, error) {
	var data string
	err := d.db.NewSelect().Table("entries").Column("data").
		Where("session_id = ? AND type = ?", sessionID, "session_info").
		OrderExpr("seq DESC").Limit(1).Scan(ctx, &data)
	return data, err
}

func (d *ProjectDAO) Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
