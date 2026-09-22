package agentruntime

import (
	"context"
	"strings"

	"github.com/oschina/mothx/internal/session"
)

// GetDurableRun loads one canonical Run row for inspection by an adapter.
// Durable lifecycle writes remain owned by ExecutionRuntime/RunStore; this
// read boundary keeps adapters from treating session storage as their own
// execution state store.
func GetDurableRun(ctx context.Context, sessionDir, runID string) (*session.SessionRun, error) {
	return session.GetSessionRunContext(ctx, sessionDir, runID)
}

// GetActiveDurableRun loads the canonical non-terminal Run for a Session.
// Callers that need ownership, submit, or cancellation decisions must use
// InspectSessionExecution instead, since an active row alone does not prove a
// local execution owner.
func GetActiveDurableRun(ctx context.Context, sessionDir, sessionID string) (*session.SessionRun, error) {
	return session.GetActiveSessionRunContext(ctx, sessionDir, sessionID)
}

// ListLatestDurableRunsBySessions loads the most recent canonical Run row per
// session as a read-only projection keyed by session ID. Sessions without any
// Run are absent from the result. Like GetDurableRun, this is an inspection
// boundary for adapters (for example ACP session/list lastRun status); durable
// lifecycle writes remain owned by ExecutionRuntime/RunStore.
func ListLatestDurableRunsBySessions(ctx context.Context, sessionDir string, sessionIDs []string) (map[string]session.SessionRun, error) {
	return session.ListLatestSessionRuns(ctx, sessionDir, sessionIDs)
}

// AnnotateDurableRunError records a terminal error reason on a canonical Run
// row that reached a terminal status without one, for example a background
// run abandoned after interrupted tool execution whose finalizer could no
// longer persist the reason. It never changes the run status, never revives a
// terminal run, and is a no-op when the run is missing, still active, or
// already carries an error. It reports whether the annotation was applied.
func AnnotateDurableRunError(ctx context.Context, sessionDir, runID, errMsg string) (bool, error) {
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(errMsg) == "" {
		return false, nil
	}
	run, err := GetDurableRun(ctx, sessionDir, runID)
	if err != nil {
		return false, err
	}
	if run == nil || session.IsNonTerminalSessionRunStatus(run.Status) {
		return false, nil
	}
	if strings.TrimSpace(run.Error) != "" {
		return false, nil
	}
	return session.AnnotateSessionRunError(sessionDir, runID, errMsg)
}
