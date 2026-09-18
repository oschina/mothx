package esm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/startvibecoding/mothx/internal/dao"
	"github.com/startvibecoding/mothx/internal/session"
)

var (
	ErrNotFound          = errors.New("esm objective not found")
	ErrObjectiveExists   = errors.New("esm objective already exists")
	ErrInvalidObjective  = errors.New("esm objective cannot be empty")
	ErrInvalidTransition = errors.New("invalid esm status transition")
)

// Store persists Enable Supervisor Mode state in the shared sessions database.
type Store struct {
	sessionDir string
	now        func() time.Time
}

// NewStore returns a store backed by the root sessions.db under sessionDir.
func NewStore(sessionDir string) *Store {
	return &Store{
		sessionDir: sessionDir,
		now: func() time.Time {
			return time.Now().UTC()
		},
	}
}

func (s *Store) db() (*dao.Database, error) {
	return session.OpenRootDB(s.sessionDir)
}

func getObjective(ctx context.Context, executor dao.Executor, sessionID string) (*Objective, error) {
	record, err := (&dao.ESMDAO{}).GetFrom(ctx, executor, sessionID)
	if err != nil {
		if errors.Is(err, dao.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return objectiveFromRecord(record)
}

func objectiveFromRecord(record *dao.ESMObjectiveRecord) (*Objective, error) {
	if record == nil {
		return nil, ErrNotFound
	}
	var remaining []string
	if err := json.Unmarshal([]byte(record.RemainingWork), &remaining); err != nil {
		return nil, fmt.Errorf("decode esm remaining work: %w", err)
	}
	return &Objective{
		SessionID: record.SessionID, ESMID: record.ESMID, Objective: record.Objective,
		Status: Status(record.Status), TokensUsed: record.TokensUsed,
		TimeUsedMS: record.TimeUsedMS, BlockedCount: record.BlockedCount, BlockedReason: record.BlockedReason,
		BlockedRunID: record.BlockedRunID, CompletionReason: record.CompletionReason,
		CompletionRunID: record.CompletionRunID, CompletionReview: record.CompletionReview,
		Phase: Phase(record.Phase), ProgressSummary: record.ProgressSummary, RemainingWork: remaining,
		RejectionCount: record.RejectionCount, RejectionRunID: record.RejectionRunID,
		RecoveryCount: record.RecoveryCount, RecoveryReason: record.RecoveryReason,
		CreatedAt: parseTime(record.CreatedAt), UpdatedAt: parseTime(record.UpdatedAt),
	}, nil
}

func objectiveRecord(obj *Objective) (*dao.ESMObjectiveRecord, error) {
	if obj == nil || obj.SessionID == "" {
		return nil, fmt.Errorf("esm objective is invalid")
	}
	remaining, err := encodeStringSlice(obj.RemainingWork)
	if err != nil {
		return nil, err
	}
	return &dao.ESMObjectiveRecord{
		SessionID: obj.SessionID, ESMID: obj.ESMID, Objective: obj.Objective, Status: string(obj.Status),
		TokensUsed: obj.TokensUsed, TimeUsedMS: obj.TimeUsedMS,
		BlockedCount: obj.BlockedCount, BlockedReason: obj.BlockedReason, BlockedRunID: obj.BlockedRunID,
		CompletionReason: obj.CompletionReason, CompletionRunID: obj.CompletionRunID,
		CompletionReview: obj.CompletionReview, Phase: string(obj.Phase), ProgressSummary: obj.ProgressSummary,
		RemainingWork: remaining, RejectionCount: obj.RejectionCount, RejectionRunID: obj.RejectionRunID,
		RecoveryCount: obj.RecoveryCount, RecoveryReason: obj.RecoveryReason,
		CreatedAt: formatTime(obj.CreatedAt), UpdatedAt: formatTime(obj.UpdatedAt),
	}, nil
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func saveObjective(ctx context.Context, executor dao.Executor, obj *Objective) error {
	record, err := objectiveRecord(obj)
	if err != nil {
		return err
	}
	return (&dao.ESMDAO{}).Update(ctx, executor, record)
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t
	}
	return time.Time{}
}

func (s *Store) timestamp() string {
	return s.now().UTC().Format(time.RFC3339Nano)
}

// Get returns the current objective for a session.
func (s *Store) Get(ctx context.Context, sessionID string) (*Objective, error) {
	if sessionID == "" {
		return nil, ErrNotFound
	}
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	return getObjective(ctx, db.Bun(), sessionID)
}

// Create creates a new objective. A completed row may be replaced; unfinished
// objectives must be edited or cleared explicitly.
func (s *Store) Create(ctx context.Context, sessionID, objective string) (*Objective, error) {
	objective = strings.TrimSpace(objective)
	if sessionID == "" {
		return nil, ErrNotFound
	}
	if objective == "" {
		return nil, ErrInvalidObjective
	}
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	now := s.timestamp()
	esmID := "esm-" + session.GenerateID()
	var existingObjective *Objective
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx dao.Tx) error {
		existing, err := getObjective(ctx, tx, sessionID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if existing != nil {
			if IsUnfinishedStatus(existing.Status) {
				existingObjective = existing
				return ErrObjectiveExists
			}
			if err := (&dao.ESMDAO{}).Delete(ctx, tx, sessionID); err != nil {
				return err
			}
		}
		return (&dao.ESMDAO{}).Insert(ctx, tx, &dao.ESMObjectiveRecord{
			SessionID: sessionID, ESMID: esmID, Objective: objective, Status: string(StatusActive),
			Phase: string(PhaseWorker), RemainingWork: "[]", CreatedAt: now, UpdatedAt: now,
		})
	}); err != nil {
		if errors.Is(err, ErrObjectiveExists) {
			return existingObjective, ErrObjectiveExists
		}
		return nil, err
	}
	return s.Get(ctx, sessionID)
}

// Edit updates the objective text for an unfinished objective.
func (s *Store) Edit(ctx context.Context, sessionID, objective string) (*Objective, error) {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return nil, ErrInvalidObjective
	}
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	current, err := getObjective(ctx, db.Bun(), sessionID)
	if err != nil {
		return nil, err
	}
	if !IsUnfinishedStatus(current.Status) {
		return current, ErrInvalidTransition
	}
	current.Objective = objective
	current.BlockedCount, current.BlockedReason, current.BlockedRunID = 0, "", ""
	current.CompletionReason, current.CompletionRunID, current.CompletionReview = "", "", ""
	current.Phase, current.ProgressSummary, current.RemainingWork = PhaseWorker, "", []string{}
	current.RejectionCount, current.RejectionRunID, current.RecoveryCount, current.RecoveryReason = 0, "", 0, ""
	current.UpdatedAt = s.now().UTC()
	if err := saveObjective(ctx, db.Bun(), current); err != nil {
		return nil, err
	}
	return s.Get(ctx, sessionID)
}

// Clear deletes the objective for a session.
func (s *Store) Clear(ctx context.Context, sessionID string) error {
	db, err := s.db()
	if err != nil {
		return err
	}
	return (&dao.ESMDAO{}).Delete(ctx, db.Bun(), sessionID)
}

// Pause disables idle continuation for an unfinished objective.
func (s *Store) Pause(ctx context.Context, sessionID string) (*Objective, error) {
	return s.setUserStatus(ctx, sessionID, StatusPaused)
}

// MarkUsageLimited records a runtime/provider limit and stops continuation.
func (s *Store) MarkUsageLimited(ctx context.Context, sessionID string) (*Objective, error) {
	return s.setRuntimeStatus(ctx, sessionID, StatusUsageLimited)
}

func (s *Store) setUserStatus(ctx context.Context, sessionID string, status Status) (*Objective, error) {
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	current, err := getObjective(ctx, db.Bun(), sessionID)
	if err != nil {
		return nil, err
	}
	if !IsUnfinishedStatus(current.Status) {
		return current, ErrInvalidTransition
	}
	current.Status = status
	current.UpdatedAt = s.now().UTC()
	if err := saveObjective(ctx, db.Bun(), current); err != nil {
		return nil, err
	}
	return s.Get(ctx, sessionID)
}

func (s *Store) setRuntimeStatus(ctx context.Context, sessionID string, status Status) (*Objective, error) {
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	current, err := getObjective(ctx, db.Bun(), sessionID)
	if err != nil {
		return nil, err
	}
	if current.Status != StatusActive {
		return current, nil
	}
	current.Status = status
	current.UpdatedAt = s.now().UTC()
	if err := saveObjective(ctx, db.Bun(), current); err != nil {
		return nil, err
	}
	return s.Get(ctx, sessionID)
}

// Resume returns paused/blocked/limited objectives to active when allowed.
func (s *Store) Resume(ctx context.Context, sessionID string) (*Objective, error) {
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	current, err := getObjective(ctx, db.Bun(), sessionID)
	if err != nil {
		return nil, err
	}
	switch current.Status {
	case StatusActive:
		return current, nil
	case StatusPaused, StatusBlocked, StatusUsageLimited:
	default:
		return current, ErrInvalidTransition
	}
	current.Status, current.BlockedCount, current.BlockedReason, current.BlockedRunID = StatusActive, 0, "", ""
	current.CompletionReason, current.CompletionRunID, current.Phase = "", "", PhaseWorker
	current.RejectionCount, current.RejectionRunID, current.RecoveryCount, current.RecoveryReason = 0, "", 0, ""
	current.UpdatedAt = s.now().UTC()
	if err := saveObjective(ctx, db.Bun(), current); err != nil {
		return nil, err
	}
	return s.Get(ctx, sessionID)
}

// SetPhase records the current role in the worker/critic/audit pipeline.
func (s *Store) SetPhase(ctx context.Context, sessionID string, phase Phase) (*Objective, error) {
	switch phase {
	case PhaseWorker, PhaseCritic, PhaseAudit, PhaseComplete:
	default:
		return nil, fmt.Errorf("invalid esm phase %q", phase)
	}
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	current, err := getObjective(ctx, db.Bun(), sessionID)
	if err != nil {
		return nil, err
	}
	validTransition := false
	switch phase {
	case PhaseWorker:
		validTransition = current.Status == StatusActive
	case PhaseCritic, PhaseAudit:
		validTransition = current.Status == StatusCompleteCandidate
	case PhaseComplete:
		validTransition = current.Status == StatusComplete
	}
	if !validTransition {
		return current, ErrInvalidTransition
	}
	current.Phase = phase
	current.UpdatedAt = s.now().UTC()
	if err := saveObjective(ctx, db.Bun(), current); err != nil {
		return nil, err
	}
	return s.Get(ctx, sessionID)
}

// RecordWorkerProgress persists the latest structured worker result so later
// runs and the TUI can show concrete progress and remaining work.
func (s *Store) RecordWorkerProgress(ctx context.Context, sessionID, summary string, remainingWork []string) (*Objective, error) {
	remainingWork = trimStringSlice(remainingWork)
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	current, err := getObjective(ctx, db.Bun(), sessionID)
	if err != nil {
		return nil, err
	}
	if current.Status != StatusActive {
		return current, ErrInvalidTransition
	}
	current.Phase, current.ProgressSummary, current.RemainingWork = PhaseWorker, strings.TrimSpace(summary), remainingWork
	current.RecoveryCount, current.RecoveryReason, current.UpdatedAt = 0, "", s.now().UTC()
	if err := saveObjective(ctx, db.Bun(), current); err != nil {
		return nil, err
	}
	return s.Get(ctx, sessionID)
}

// RecordRecovery persists a recovery diagnosis after an interrupted ESM role.
// Recovery is observability, not a circuit breaker: an active long-task
// objective remains active until it completes, is explicitly stopped, or a
// real blocker is recorded.
func (s *Store) RecordRecovery(ctx context.Context, sessionID, reason, summary string, remainingWork []string) (*Objective, error) {
	reason = strings.TrimSpace(reason)
	summary = strings.TrimSpace(summary)
	if reason == "" {
		return nil, fmt.Errorf("recovery requires an interruption reason")
	}
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	if summary == "" {
		summary = "Interrupted ESM role; recovery will continue from the current repository state."
	}
	var transitionObjective *Objective
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx dao.Tx) error {
		current, err := getObjective(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if current.Status != StatusActive {
			transitionObjective = current
			return ErrInvalidTransition
		}
		if remainingWork == nil {
			remainingWork = current.RemainingWork
		}
		current.Status, current.RecoveryCount, current.RecoveryReason = StatusActive, current.RecoveryCount+1, reason
		current.ProgressSummary, current.RemainingWork, current.UpdatedAt = summary, trimStringSlice(remainingWork), s.now().UTC()
		return saveObjective(ctx, tx, current)
	}); err != nil {
		if errors.Is(err, ErrInvalidTransition) {
			return transitionObjective, ErrInvalidTransition
		}
		return nil, err
	}
	return s.Get(ctx, sessionID)
}

// AccountUsage accumulates one agent run's usage. Usage counters are
// observability only; ESM no longer enforces token or time limits.
func (s *Store) AccountUsage(ctx context.Context, sessionID string, tokens, durationMS int64) (*Objective, error) {
	if tokens < 0 {
		tokens = 0
	}
	if durationMS < 0 {
		durationMS = 0
	}
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx dao.Tx) error {
		current, err := getObjective(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		current.TokensUsed += tokens
		current.TimeUsedMS += durationMS
		current.UpdatedAt = s.now().UTC()
		return saveObjective(ctx, tx, current)
	}); err != nil {
		return nil, err
	}
	return s.Get(ctx, sessionID)
}

// UpdateFromModel accepts the two model-controlled transitions: complete and
// blocked. Complete records a candidate only; the orchestrator must audit the
// candidate before marking the objective terminal complete. Blocked transitions
// should normally use UpdateFromModelForRun so the repeated-blocker audit is
// counted across consecutive agent runs.
func (s *Store) UpdateFromModel(ctx context.Context, sessionID string, status Status, reason string) (*Objective, error) {
	return s.UpdateFromModelForRun(ctx, sessionID, status, reason, "")
}

// UpdateFromModelForRun accepts model-controlled complete/blocked transitions.
// Complete becomes complete_candidate, never terminal complete. The terminal
// complete state is reserved for the ESM orchestrator after an independent
// audit sub-agent passes.
// Blocked only becomes terminal after the same blocker repeats in three
// consecutive ESM agent runs. A run can contribute at most once to the audit.
func (s *Store) UpdateFromModelForRun(ctx context.Context, sessionID string, status Status, reason, runID string) (*Objective, error) {
	reason = strings.TrimSpace(reason)
	runID = strings.TrimSpace(runID)
	switch status {
	case StatusComplete:
		if reason == "" {
			return nil, fmt.Errorf("complete status requires verification evidence")
		}
	case StatusBlocked:
		if reason == "" {
			return nil, fmt.Errorf("blocked status requires a concrete reason")
		}
		if runID == "" {
			return nil, fmt.Errorf("blocked status requires an ESM run id")
		}
	default:
		return nil, fmt.Errorf("model may only set esm status to %q or %q", StatusComplete, StatusBlocked)
	}

	db, err := s.db()
	if err != nil {
		return nil, err
	}
	var transitionCurrent *Objective
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx dao.Tx) error {
		current, err := getObjective(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if current.Status != StatusActive {
			transitionCurrent = current
			return ErrInvalidTransition
		}
		switch status {
		case StatusComplete:
			current.Status, current.BlockedCount, current.BlockedReason, current.BlockedRunID = StatusCompleteCandidate, 0, "", ""
			current.CompletionReason, current.CompletionRunID, current.CompletionReview, current.Phase = reason, runID, "", PhaseCritic
		case StatusBlocked:
			nextCount := current.BlockedCount
			if current.BlockedRunID == runID && sameBlockedReason(current.BlockedReason, reason) {
			} else if sameBlockedReason(current.BlockedReason, reason) {
				nextCount++
			} else {
				nextCount = 1
			}
			current.Status, current.BlockedCount, current.BlockedReason, current.BlockedRunID = StatusActive, nextCount, reason, runID
			if nextCount >= BlockedAuditLimit {
				current.Status = StatusBlocked
			}
			current.CompletionReason, current.CompletionRunID, current.CompletionReview = "", "", ""
			current.RejectionCount, current.RejectionRunID = 0, ""
		}
		current.UpdatedAt = s.now().UTC()
		return saveObjective(ctx, tx, current)
	}); err != nil {
		if errors.Is(err, ErrInvalidTransition) {
			return transitionCurrent, ErrInvalidTransition
		}
		return nil, err
	}
	if transitionCurrent != nil {
		return transitionCurrent, nil
	}
	return s.Get(ctx, sessionID)
}

// MarkCompleteFromAudit marks a completion candidate terminal complete after an
// independent ESM audit has verified the objective against the current state.
func (s *Store) MarkCompleteFromAudit(ctx context.Context, sessionID, review string) (*Objective, error) {
	review = strings.TrimSpace(review)
	if review == "" {
		return nil, fmt.Errorf("complete audit requires a review")
	}
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	current, err := getObjective(ctx, db.Bun(), sessionID)
	if err != nil {
		return nil, err
	}
	if current.Status != StatusCompleteCandidate {
		return current, ErrInvalidTransition
	}
	current.Status, current.BlockedCount, current.BlockedReason, current.BlockedRunID = StatusComplete, 0, "", ""
	current.CompletionReview, current.Phase, current.RemainingWork = review, PhaseComplete, []string{}
	current.RejectionCount, current.RejectionRunID, current.UpdatedAt = 0, "", s.now().UTC()
	if err := saveObjective(ctx, db.Bun(), current); err != nil {
		return nil, err
	}
	return s.Get(ctx, sessionID)
}

// RejectCompletionCandidate records a failed completion candidate. A rejected
// claim means work remains, so unattended continuation stays active.
func (s *Store) RejectCompletionCandidate(ctx context.Context, sessionID, review string) (*Objective, error) {
	current, err := s.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return s.RejectCompletionCandidateForRun(ctx, sessionID, current.CompletionRunID, review, nil)
}

// RejectCompletionCandidateForRun records a critic/audit rejection with its
// structured missing work. A run contributes at most once to the streak.
func (s *Store) RejectCompletionCandidateForRun(ctx context.Context, sessionID, runID, review string, missingWork []string) (*Objective, error) {
	return s.recordCompletionRejection(ctx, sessionID, runID, review, missingWork, StatusCompleteCandidate)
}

// RejectWorkerReport records a worker report rejected before supervisor review
// while the objective is still active.
func (s *Store) RejectWorkerReport(ctx context.Context, sessionID, runID, review string, remainingWork []string) (*Objective, error) {
	return s.recordCompletionRejection(ctx, sessionID, runID, review, remainingWork, StatusActive)
}

func (s *Store) recordCompletionRejection(ctx context.Context, sessionID, runID, review string, remainingWork []string, expectedStatus Status) (*Objective, error) {
	review = strings.TrimSpace(review)
	if review == "" {
		return nil, fmt.Errorf("completion rejection requires an audit review")
	}
	runID = strings.TrimSpace(runID)
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	var transitionCurrent *Objective
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx dao.Tx) error {
		current, err := getObjective(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if current.Status != expectedStatus {
			if runID != "" && current.RejectionRunID == runID {
				transitionCurrent = current
				return nil
			}
			transitionCurrent = current
			return ErrInvalidTransition
		}
		nextCount := current.RejectionCount
		if runID == "" || current.RejectionRunID != runID {
			nextCount++
		}
		current.Status, current.CompletionReview, current.RemainingWork = StatusActive, review, trimStringSlice(remainingWork)
		current.RejectionCount, current.RejectionRunID, current.UpdatedAt = nextCount, runID, s.now().UTC()
		return saveObjective(ctx, tx, current)
	}); err != nil {
		if errors.Is(err, ErrInvalidTransition) {
			return transitionCurrent, ErrInvalidTransition
		}
		return nil, err
	}
	if transitionCurrent != nil {
		return transitionCurrent, nil
	}
	return s.Get(ctx, sessionID)
}

// RecordCompletionReview stores a completion rejection/review note without
// changing the current lifecycle state. This lets later worker runs learn why a
// previous completion claim was not accepted.
func (s *Store) RecordCompletionReview(ctx context.Context, sessionID, review string) (*Objective, error) {
	review = strings.TrimSpace(review)
	if review == "" {
		return nil, fmt.Errorf("completion review cannot be empty")
	}
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	current, err := getObjective(ctx, db.Bun(), sessionID)
	if err != nil {
		return nil, err
	}
	if !IsUnfinishedStatus(current.Status) {
		return current, ErrInvalidTransition
	}
	current.CompletionReview, current.UpdatedAt = review, s.now().UTC()
	if err := saveObjective(ctx, db.Bun(), current); err != nil {
		return nil, err
	}
	return s.Get(ctx, sessionID)
}

// FinishRun clears repeated blocker/rejection streaks when an active ESM run
// finishes without reporting the same condition.
func (s *Store) FinishRun(ctx context.Context, sessionID, runID string) (*Objective, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return s.Get(ctx, sessionID)
	}
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	current, err := getObjective(ctx, db.Bun(), sessionID)
	if err != nil {
		return nil, err
	}
	if current.Status != StatusActive {
		return current, nil
	}
	nextBlockedCount := current.BlockedCount
	nextBlockedReason := current.BlockedReason
	nextBlockedRunID := current.BlockedRunID
	if current.BlockedCount > 0 && current.BlockedRunID != "" && current.BlockedRunID != runID {
		nextBlockedCount = 0
		nextBlockedReason = ""
		nextBlockedRunID = ""
	}
	nextRejectionCount := current.RejectionCount
	nextRejectionRunID := current.RejectionRunID
	if current.RejectionCount > 0 && current.RejectionRunID != "" && current.RejectionRunID != runID {
		nextRejectionCount = 0
		nextRejectionRunID = ""
	}
	if nextBlockedCount == current.BlockedCount && nextRejectionCount == current.RejectionCount {
		return current, nil
	}
	current.BlockedCount, current.BlockedReason, current.BlockedRunID = nextBlockedCount, nextBlockedReason, nextBlockedRunID
	current.RejectionCount, current.RejectionRunID, current.UpdatedAt = nextRejectionCount, nextRejectionRunID, s.now().UTC()
	if err := saveObjective(ctx, db.Bun(), current); err != nil {
		return nil, err
	}
	return s.Get(ctx, sessionID)
}

func encodeStringSlice(values []string) (string, error) {
	values = trimStringSlice(values)
	if values == nil {
		values = []string{}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("encode esm remaining work: %w", err)
	}
	return string(encoded), nil
}

func sameBlockedReason(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) && strings.TrimSpace(a) != ""
}

// IsUsageLimitError applies a conservative text heuristic for provider/account
// limits that should stop unattended continuation.
func IsUsageLimitError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	markers := []string{
		"usage limit",
		"rate limit",
		"quota",
		"insufficient_quota",
		"resource_exhausted",
		"billing",
		"too many requests",
	}
	for _, marker := range markers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}
