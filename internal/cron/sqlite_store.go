package cron

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/session"
)

// SQLiteCronStore persists cron jobs in the shared sessions.db database.
// Query construction lives in dao.CronDAO; this type only maps persistence
// records to the cron domain model.
type SQLiteCronStore struct {
	sessionDir string
}

// NewSQLiteCronStore creates a SQLite-backed cron store rooted at sessionDir.
func NewSQLiteCronStore(sessionDir string) *SQLiteCronStore {
	return &SQLiteCronStore{sessionDir: sessionDir}
}

func (s *SQLiteCronStore) db() (*dao.Database, error) {
	return session.OpenBunDatabase(session.RootDatabasePath(s.sessionDir))
}

func (s *SQLiteCronStore) dao() (*dao.CronDAO, error) {
	database, err := s.db()
	if err != nil {
		return nil, err
	}
	return dao.NewCronDAO(database.Bun()), nil
}

// List returns all cron jobs.
func (s *SQLiteCronStore) List() ([]CronJob, error) {
	cronDAO, err := s.dao()
	if err != nil {
		return nil, err
	}
	records, err := cronDAO.List(context.Background())
	if err != nil {
		return nil, err
	}
	jobs := make([]CronJob, 0, len(records))
	for _, record := range records {
		jobs = append(jobs, cronJobFromRecord(record))
	}
	return jobs, nil
}

// Get returns a cron job by ID.
func (s *SQLiteCronStore) Get(id string) (*CronJob, error) {
	cronDAO, err := s.dao()
	if err != nil {
		return nil, err
	}
	record, err := cronDAO.Get(context.Background(), id)
	if errors.Is(err, dao.ErrNoRows) {
		return nil, fmt.Errorf("cron job %q not found", id)
	}
	if err != nil {
		return nil, err
	}
	job := cronJobFromRecord(*record)
	return &job, nil
}

// Create adds a new cron job.
func (s *SQLiteCronStore) Create(job CronJob) (*CronJob, error) {
	if job.ID == "" {
		job.ID = newCronID()
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now()
	}
	cronDAO, err := s.dao()
	if err != nil {
		return nil, err
	}
	record := cronJobRecord(job)
	if err := cronDAO.Create(context.Background(), &record); err != nil {
		return nil, fmt.Errorf("create cron job %q: %w", job.ID, err)
	}
	return &job, nil
}

// Update updates an existing cron job.
func (s *SQLiteCronStore) Update(job CronJob) error {
	cronDAO, err := s.dao()
	if err != nil {
		return err
	}
	record := cronJobRecord(job)
	if err := cronDAO.Update(context.Background(), &record); errors.Is(err, dao.ErrNoRows) {
		return fmt.Errorf("cron job %q not found", job.ID)
	} else if err != nil {
		return fmt.Errorf("update cron job %q: %w", job.ID, err)
	}
	return nil
}

// Delete removes a cron job.
func (s *SQLiteCronStore) Delete(id string) error {
	cronDAO, err := s.dao()
	if err != nil {
		return err
	}
	if err := cronDAO.Delete(context.Background(), id); errors.Is(err, dao.ErrNoRows) {
		return fmt.Errorf("cron job %q not found", id)
	} else if err != nil {
		return fmt.Errorf("delete cron job %q: %w", id, err)
	}
	return nil
}

// ClaimDue atomically marks a due job as running. Only the caller that updates
// a row may execute it, preventing duplicate runs across scheduler instances.
func (s *SQLiteCronStore) ClaimDue(id string, now time.Time) (bool, error) {
	cronDAO, err := s.dao()
	if err != nil {
		return false, err
	}
	stamp := formatCronTime(now)
	staleBefore := formatCronTime(now.Add(-runningLeaseTimeout))
	return cronDAO.ClaimDue(context.Background(), id, stamp, staleBefore)
}

func cronJobRecord(job CronJob) dao.CronJobRecord {
	return dao.CronJobRecord{
		ID: job.ID, SessionID: job.SessionID, Name: job.Name, Prompt: job.Prompt,
		Schedule: job.Schedule, OneShot: job.OneShot, Mode: job.Mode, WorkDir: job.WorkDir,
		A2ATarget: job.A2ATarget, A2AToken: job.A2AToken, Enabled: job.Enabled,
		CreatedAt: formatCronTime(job.CreatedAt), LastRun: formatCronTime(job.LastRun),
		NextRun: formatCronTime(job.NextRun), RunCount: job.RunCount, LastStatus: job.LastStatus,
		LastError: job.LastError,
	}
}

func cronJobFromRecord(record dao.CronJobRecord) CronJob {
	return CronJob{
		ID: record.ID, SessionID: record.SessionID, Name: record.Name, Prompt: record.Prompt,
		Schedule: record.Schedule, OneShot: record.OneShot, Mode: record.Mode, WorkDir: record.WorkDir,
		A2ATarget: record.A2ATarget, A2AToken: record.A2AToken, Enabled: record.Enabled,
		CreatedAt: parseCronTime(record.CreatedAt), LastRun: parseCronTime(record.LastRun),
		NextRun: parseCronTime(record.NextRun), RunCount: record.RunCount, LastStatus: record.LastStatus,
		LastError: record.LastError,
	}
}

func formatCronTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseCronTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	t, _ := time.Parse(time.RFC3339, s)
	return t
}
