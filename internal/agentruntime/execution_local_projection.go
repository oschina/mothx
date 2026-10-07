package agentruntime

// LocalSessionExecutionFacts are the process-local facts an adapter owns for a
// session that has no shared session root (for example an embedded fixture).
// The adapter supplies only these facts; the snapshot projection stays here so
// no adapter ever assembles a SessionExecutionSnapshot itself.
type LocalSessionExecutionFacts struct {
	SessionID     string
	SessionExists bool
	// Active means a durable run is registered as executing in this process.
	Active    bool
	RunID     string
	RunStatus string
	// LegacyBusy carries the pre-durable "running" bit of embedded fixtures.
	LegacyBusy bool
}

// InspectLocalSessionExecution projects the execution state for a session with
// no shared session root. It is the Runtime-owned replacement for adapter-local
// snapshot assembly.
func InspectLocalSessionExecution(facts LocalSessionExecutionFacts) SessionExecutionSnapshot {
	snapshot := newSessionExecutionSnapshot(facts.SessionID)
	snapshot.Source = snapshotSourceLocal
	snapshot.SessionExists = facts.SessionExists
	switch {
	case facts.Active:
		snapshot.State = SessionExecutionLocal
		snapshot.Phase = "executing"
		snapshot.Running = true
		snapshot.CanCancelLocal = true
		snapshot.LinkageState = "legacy_unbound"
		snapshot.DisplayOwnerScope = "local"
		snapshot.ActiveRun = &SessionRunSummary{ID: facts.RunID, Status: facts.RunStatus}
	case facts.LegacyBusy:
		snapshot.State = SessionExecutionUnknown
		snapshot.Phase = "legacy"
		snapshot.LinkageState = "legacy_unbound"
	default:
		snapshot.State = SessionExecutionIdle
		snapshot.Phase = "idle"
		snapshot.Busy = false
		snapshot.CanSubmit = true
		snapshot.DisplayOwnerScope = "none"
		snapshot.LinkageState = "none"
	}
	return snapshot
}

// UnknownSessionExecution returns the conservative unknown projection used when
// a session cannot be inspected at all. Adapters must use this instead of
// mutating an existing snapshot's Busy/CanSubmit/state fields.
func UnknownSessionExecution(sessionID string) SessionExecutionSnapshot {
	return newSessionExecutionSnapshot(sessionID)
}
