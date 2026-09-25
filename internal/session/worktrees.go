package session

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/dao"
)

// Worktree lifecycle status values. A worktree's status is a resource
// lifecycle state, not a run state machine.
const (
	WorktreeStatusPending = "pending"
	WorktreeStatusReady   = "ready"
	WorktreeStatusFailed  = "failed"
	WorktreeStatusRemoved = "removed"
)

// Worktree is the session-domain projection of one Runtime-owned worktree
// registry row. Git remains the content authority (existence, branch,
// cleanliness); this record is the identity/ownership/authorization authority.
type Worktree struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Branch         string `json:"branch,omitempty"`
	Directory      string `json:"directory"`
	RepositoryRoot string `json:"repositoryRoot"`
	ProjectID      string `json:"projectId,omitempty"`
	StartCommand   string `json:"startCommand,omitempty"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
	// External marks a git worktree that exists in the repository but is not
	// registered (for example one created by hand). It is read-only.
	External  bool      `json:"external,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func worktreeFromRecord(record *dao.WorktreeRecord) Worktree {
	wt := Worktree{
		ID:             record.ID,
		Name:           record.Name,
		Directory:      record.Directory,
		RepositoryRoot: record.RepositoryRoot,
		StartCommand:   record.StartCommand,
		Status:         record.Status,
		Error:          record.Error,
		CreatedAt:      parseProjectTime(record.CreatedAt),
		UpdatedAt:      parseProjectTime(record.UpdatedAt),
	}
	if record.Branch != nil {
		wt.Branch = *record.Branch
	}
	if record.ProjectID != nil {
		wt.ProjectID = *record.ProjectID
	}
	return wt
}

// CreateWorktreeRecord inserts a new registry row. It is the only writer of a
// worktree's identity; callers must supply a stable id and the canonical
// directory. Git materialization is a separate, Runtime-owned step.
func CreateWorktreeRecord(sessionDir string, wt Worktree) (Worktree, error) {
	wt.ID = strings.TrimSpace(wt.ID)
	wt.Name = strings.TrimSpace(wt.Name)
	wt.Directory = strings.TrimSpace(wt.Directory)
	wt.RepositoryRoot = strings.TrimSpace(wt.RepositoryRoot)
	wt.ProjectID = strings.TrimSpace(wt.ProjectID)
	if wt.ID == "" || wt.Directory == "" || wt.RepositoryRoot == "" {
		return Worktree{}, fmt.Errorf("worktree id, directory, and repositoryRoot are required")
	}
	if wt.Status == "" {
		wt.Status = WorktreeStatusPending
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return Worktree{}, err
	}
	now := time.Now().UTC()
	record := &dao.WorktreeRecord{
		ID:             wt.ID,
		RepositoryRoot: wt.RepositoryRoot,
		Directory:      wt.Directory,
		Name:           wt.Name,
		StartCommand:   wt.StartCommand,
		Status:         wt.Status,
		Error:          wt.Error,
		CreatedAt:      now.Format(time.RFC3339Nano),
		UpdatedAt:      now.Format(time.RFC3339Nano),
	}
	if wt.Branch != "" {
		branch := wt.Branch
		record.Branch = &branch
	}
	if wt.ProjectID != "" {
		projectID := wt.ProjectID
		record.ProjectID = &projectID
	}
	if err := dao.NewWorktreeDAO(db.Bun()).Insert(context.Background(), record); err != nil {
		return Worktree{}, err
	}
	wt.CreatedAt = now
	wt.UpdatedAt = now
	return wt, nil
}

// GetWorktreeByID returns the registered worktree, or a zero Worktree when it
// does not exist.
func GetWorktreeByID(sessionDir, id string) (Worktree, error) {
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return Worktree{}, err
	}
	record, err := dao.NewWorktreeDAO(db.Bun()).GetByID(context.Background(), strings.TrimSpace(id))
	if err != nil {
		return Worktree{}, err
	}
	if record == nil {
		return Worktree{}, nil
	}
	return worktreeFromRecord(record), nil
}

// GetWorktreeByDirectory returns the registered worktree for a canonical
// directory, or a zero Worktree when it does not exist.
func GetWorktreeByDirectory(sessionDir, directory string) (Worktree, error) {
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return Worktree{}, err
	}
	record, err := dao.NewWorktreeDAO(db.Bun()).GetByDirectory(context.Background(), strings.TrimSpace(directory))
	if err != nil {
		return Worktree{}, err
	}
	if record == nil {
		return Worktree{}, nil
	}
	return worktreeFromRecord(record), nil
}

// ListWorktrees returns registry rows, optionally filtered by repository root
// and/or project id.
func ListWorktrees(sessionDir, repositoryRoot, projectID string) ([]Worktree, error) {
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return []Worktree{}, err
	}
	records, err := dao.NewWorktreeDAO(db.Bun()).List(context.Background(), strings.TrimSpace(repositoryRoot), strings.TrimSpace(projectID))
	if err != nil {
		return nil, err
	}
	worktrees := make([]Worktree, 0, len(records))
	for i := range records {
		worktrees = append(worktrees, worktreeFromRecord(&records[i]))
	}
	return worktrees, nil
}

// UpdateWorktreeStatus records a lifecycle transition. Unknown ids are a
// no-op so a concurrently removed worktree never fails a terminal write.
func UpdateWorktreeStatus(sessionDir, id, status, errMsg string) error {
	id = strings.TrimSpace(id)
	status = strings.TrimSpace(status)
	if id == "" || status == "" {
		return fmt.Errorf("worktree id and status are required")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	_, err = dao.NewWorktreeDAO(db.Bun()).UpdateStatus(context.Background(), id, status, errMsg, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// DeleteWorktreeRecord removes a registry row after the underlying git worktree
// has been deleted. A missing row is not an error.
func DeleteWorktreeRecord(sessionDir, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("worktree id is required")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	return dao.NewWorktreeDAO(db.Bun()).Delete(context.Background(), id)
}
