package agentruntime

import (
	"fmt"

	"github.com/oschina/mothx/internal/session"
)

// CreateDurableRun is the Runtime-owned entry point for callers that need to
// create a canonical row before an in-memory ExecutionRuntime is available.
// Normal Agent executions should use ExecutionRuntime.BeginDurable.
func CreateDurableRun(sessionDir string, run DurableRun) error {
	return (RunStore{SessionDir: sessionDir}).Create(run)
}

// UpdateDurableRun applies a canonical non-terminal transition for a recovered
// or externally-owned run. Active Agent loops should use UpdateDurable.
func UpdateDurableRun(sessionDir, runID string, state RunState, message string) error {
	return (RunStore{SessionDir: sessionDir}).Update(runID, state, message)
}

// FinishDurableRun applies a Runtime-owned terminal transition when no live
// ExecutionRuntime can be reattached. It remains monotonic and idempotent.
func FinishDurableRun(sessionDir, runID string, state RunState, message string) error {
	return (RunStore{SessionDir: sessionDir}).Finish(runID, state, message)
}

// RecoverDurableRun terminalizes a local orphan and records the corresponding
// Runtime recovery event as one shared operation. The caller may perform
// adapter-specific decision cleanup before invoking it.
func RecoverDurableRun(sessionDir string, run session.SessionRun, state RunState, message string, event RunEvent) error {
	if run.ID == "" || run.SessionID == "" {
		return fmt.Errorf("recovered run identity is required")
	}
	if message == "" {
		message = "run recovered without a live execution owner"
	}
	if event.SessionID == "" {
		event.SessionID = run.SessionID
	}
	if event.RunID == "" {
		event.RunID = run.ID
	}
	if event.Source == "" {
		event.Source = run.Source
	}
	if event.Model == "" {
		event.Model = run.Model
	}
	if event.Mode == "" {
		event.Mode = run.Mode
	}
	if event.Status == "" {
		event.Status = string(state)
	}
	if event.ID == "" {
		event.ID = "recovery_" + run.ID + "_" + string(state)
	}
	// Record the deterministic recovery event before terminalizing the row. If
	// row persistence fails, a retry finds the same event and does not append a
	// duplicate projection.
	events, err := session.ListSessionRunEvents(sessionDir, run.SessionID)
	if err != nil {
		return fmt.Errorf("check recovered run event: %w", err)
	}
	found := false
	for _, existing := range events {
		if existing.ID == event.ID {
			found = true
			break
		}
	}
	if !found {
		if _, err := (SessionRunEventSink{SessionDir: sessionDir}).Record(event); err != nil {
			// Another recovery worker may have inserted the deterministic event
			// between the read and insert. Re-read before treating a uniqueness
			// error as a failed recovery so the operation remains idempotent.
			latest, listErr := session.ListSessionRunEvents(sessionDir, run.SessionID)
			if listErr != nil {
				return fmt.Errorf("record recovered run event: %w (verify existing event: %v)", err, listErr)
			}
			for _, existing := range latest {
				if existing.ID == event.ID {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("record recovered run event: %w", err)
			}
		}
	}
	if err := FinishDurableRun(sessionDir, run.ID, state, message); err != nil {
		return fmt.Errorf("finish recovered run: %w", err)
	}
	return nil
}
