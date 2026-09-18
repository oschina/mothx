package esm

import "time"

// Status is the persisted lifecycle state for a supervised objective.
type Status string

const (
	StatusActive            Status = "active"
	StatusPaused            Status = "paused"
	StatusBlocked           Status = "blocked"
	StatusUsageLimited      Status = "usage_limited"
	StatusCompleteCandidate Status = "complete_candidate"
	StatusComplete          Status = "complete"
)

// BlockedAuditLimit is the number of consecutive ESM runs reporting the same
// blocker before the objective becomes blocked.
const BlockedAuditLimit = 3

// Phase identifies the current role in the ESM completion pipeline.
type Phase string

const (
	PhaseWorker   Phase = "worker"
	PhaseCritic   Phase = "critic"
	PhaseAudit    Phase = "audit"
	PhaseComplete Phase = "complete"
)

// Objective is the per-session Enable Supervisor Mode objective.
// TokensUsed and TimeUsedMS are observability counters only; ESM no longer
// enforces token or time limits.
type Objective struct {
	SessionID        string
	ESMID            string
	Objective        string
	Status           Status
	TokensUsed       int64
	TimeUsedMS       int64
	BlockedCount     int
	BlockedReason    string
	BlockedRunID     string
	CompletionReason string
	CompletionRunID  string
	CompletionReview string
	Phase            Phase
	ProgressSummary  string
	RemainingWork    []string
	RejectionCount   int
	RejectionRunID   string
	RecoveryCount    int
	RecoveryReason   string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// HasObjective reports whether the row contains a real objective.
func (o *Objective) HasObjective() bool {
	return o != nil && o.SessionID != "" && o.ESMID != ""
}

// CanAutoRun reports whether TUI idle continuation may start a new agent run.
func (o *Objective) CanAutoRun() bool {
	return o != nil && (o.Status == StatusActive || o.Status == StatusCompleteCandidate)
}

// IsUnfinishedStatus reports whether a status still represents an open
// objective. "complete" is terminal; clearing the objective deletes the row.
func IsUnfinishedStatus(status Status) bool {
	return status != "" && status != StatusComplete
}

// IsRunnableStatus reports whether normal ESM tools should remain visible.
func IsRunnableStatus(status Status) bool {
	switch status {
	case StatusActive, StatusPaused, StatusBlocked, StatusUsageLimited:
		return true
	default:
		return false
	}
}
