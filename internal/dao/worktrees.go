package dao

import (
	"context"
	"database/sql"

	"github.com/uptrace/bun"
)

// WorktreeRecord is one row of the Runtime-owned worktree registry. It is the
// identity/ownership/authorization authority for a managed git worktree; git
// remains the content authority (existence, branch, cleanliness).
type WorktreeRecord struct {
	bun.BaseModel  `bun:"table:worktrees"`
	ID             string  `bun:"id,pk"`
	RepositoryRoot string  `bun:"repository_root"`
	Directory      string  `bun:"directory"`
	Name           string  `bun:"name"`
	Branch         *string `bun:"branch,nullzero"`
	ProjectID      *string `bun:"project_id,nullzero"`
	StartCommand   string  `bun:"start_command"`
	Status         string  `bun:"status"`
	Error          string  `bun:"error"`
	CreatedAt      string  `bun:"created_at"`
	UpdatedAt      string  `bun:"updated_at"`
}

type WorktreeDAO struct{ db *bun.DB }

func NewWorktreeDAO(db *bun.DB) *WorktreeDAO { return &WorktreeDAO{db: db} }

func (d *WorktreeDAO) Insert(ctx context.Context, record *WorktreeRecord) error {
	_, err := d.db.NewInsert().Model(record).Exec(ctx)
	return err
}

// GetByID returns the registry row for id, or nil when it does not exist.
func (d *WorktreeDAO) GetByID(ctx context.Context, id string) (*WorktreeRecord, error) {
	record := new(WorktreeRecord)
	err := d.db.NewSelect().Model(record).Where("id = ?", id).Limit(1).Scan(ctx)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return record, nil
}

// GetByDirectory returns the registry row registered for a canonical worktree
// directory, or nil when it does not exist.
func (d *WorktreeDAO) GetByDirectory(ctx context.Context, directory string) (*WorktreeRecord, error) {
	record := new(WorktreeRecord)
	err := d.db.NewSelect().Model(record).Where("directory = ?", directory).Limit(1).Scan(ctx)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return record, nil
}

// List returns registry rows, optionally filtered by repository root and/or
// project id, newest first. Empty filters are ignored.
func (d *WorktreeDAO) List(ctx context.Context, repositoryRoot, projectID string) ([]WorktreeRecord, error) {
	var records []WorktreeRecord
	query := d.db.NewSelect().Model(&records)
	if repositoryRoot != "" {
		query = query.Where("repository_root = ?", repositoryRoot)
	}
	if projectID != "" {
		query = query.Where("project_id = ?", projectID)
	}
	err := query.OrderExpr("updated_at DESC, name COLLATE NOCASE").Scan(ctx)
	return records, err
}

// UpdateStatus records a lifecycle transition (pending|ready|failed|removed)
// and its optional error message.
func (d *WorktreeDAO) UpdateStatus(ctx context.Context, id, status, errMsg, updatedAt string) (int64, error) {
	result, err := d.db.NewUpdate().Model((*WorktreeRecord)(nil)).
		Set("status = ?", status).Set("error = ?", errMsg).Set("updated_at = ?", updatedAt).
		Where("id = ?", id).Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// Delete removes a registry row. Worktree deletion is a filesystem/git
// operation; the registry row is dropped only after that succeeds.
func (d *WorktreeDAO) Delete(ctx context.Context, id string) error {
	_, err := d.db.NewDelete().Model((*WorktreeRecord)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}
