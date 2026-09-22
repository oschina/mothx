package session

import (
	"context"
	"errors"
	"fmt"
	"github.com/oschina/mothx/internal/dao"
	"strings"
	"time"
)

var ErrRuntimeSubmissionExists = errors.New("runtime submission already exists")
var ErrRuntimeSubmissionConflict = errors.New("runtime submission key conflicts with another request")

// RuntimeSubmission is the durable admission identity for one original or
// retry submission. Only a digest of the transport key is persisted.
type RuntimeSubmission struct {
	ID                 string
	SessionID          string
	Scope              string
	KeyHash            string
	RequestFingerprint string
	IntentID           string
	RunID              string
	CreatedAt          time.Time
}

// RuntimeSubmissionError carries the canonical Run identity selected by a
// competing or replayed admission.
type RuntimeSubmissionError struct {
	Existing RuntimeSubmission
	Conflict bool
}

func (e *RuntimeSubmissionError) Error() string {
	if e == nil {
		return "runtime submission admission failed"
	}
	if e.Conflict {
		return fmt.Sprintf("%v: existing Run %s", ErrRuntimeSubmissionConflict, e.Existing.RunID)
	}
	return fmt.Sprintf("%v: Run %s", ErrRuntimeSubmissionExists, e.Existing.RunID)
}

func (e *RuntimeSubmissionError) Unwrap() error {
	if e != nil && e.Conflict {
		return ErrRuntimeSubmissionConflict
	}
	return ErrRuntimeSubmissionExists
}

func reserveRuntimeSubmissionTx(tx *dao.Tx, run SessionRun) error {
	keyHash := strings.TrimSpace(run.SubmissionKeyHash)
	if keyHash == "" {
		return nil
	}
	scope := strings.TrimSpace(run.SubmissionScope)
	if scope == "" {
		return fmt.Errorf("runtime submission scope is required")
	}
	fingerprint := strings.TrimSpace(run.SubmissionFingerprint)
	existing, err := getRuntimeSubmissionTx(tx, run.SessionID, scope, keyHash)
	if err != nil && err != dao.ErrNoRows {
		return err
	}
	if err == nil {
		return &RuntimeSubmissionError{Existing: existing, Conflict: existing.RequestFingerprint != "" && fingerprint != "" && existing.RequestFingerprint != fingerprint}
	}
	createdAt := run.StartedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	err = dao.NewRuntimeSubmissionDAO(nil).Insert(context.Background(), tx, &dao.RuntimeSubmissionRecord{
		ID: GenerateID(), SessionID: run.SessionID, Scope: scope, KeyHash: keyHash,
		RequestFingerprint: fingerprint, IntentID: run.IntentID, RunID: run.ID,
		CreatedAt: createdAt.Format(time.RFC3339Nano),
	})
	if err == nil {
		return nil
	}
	// A concurrent process can win the unique constraint after our initial
	// lookup. Resolve the durable winner before returning the typed result.
	existing, lookupErr := getRuntimeSubmissionTx(tx, run.SessionID, scope, keyHash)
	if lookupErr == nil {
		return &RuntimeSubmissionError{Existing: existing, Conflict: existing.RequestFingerprint != "" && fingerprint != "" && existing.RequestFingerprint != fingerprint}
	}
	return err
}

func getRuntimeSubmissionTx(tx *dao.Tx, sessionID, scope, keyHash string) (RuntimeSubmission, error) {
	record, err := dao.NewRuntimeSubmissionDAO(nil).Find(context.Background(), tx, sessionID, scope, keyHash)
	if err != nil {
		return RuntimeSubmission{}, err
	}
	return RuntimeSubmission{ID: record.ID, SessionID: record.SessionID, Scope: record.Scope,
		KeyHash: record.KeyHash, RequestFingerprint: record.RequestFingerprint, IntentID: record.IntentID,
		RunID: record.RunID, CreatedAt: parseSessionTimestamp(record.CreatedAt)}, nil
}

func GetRuntimeSubmission(ctx context.Context, sessionDir, sessionID, scope, keyHash string) (*RuntimeSubmission, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if sessionID == "" || strings.TrimSpace(scope) == "" || strings.TrimSpace(keyHash) == "" {
		return nil, nil
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	record, err := dao.NewRuntimeSubmissionDAO(db.Bun()).Find(ctx, db.Bun(), sessionID, scope, keyHash)
	if err == dao.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &RuntimeSubmission{ID: record.ID, SessionID: record.SessionID, Scope: record.Scope,
		KeyHash: record.KeyHash, RequestFingerprint: record.RequestFingerprint, IntentID: record.IntentID,
		RunID: record.RunID, CreatedAt: parseSessionTimestamp(record.CreatedAt)}, nil
}
