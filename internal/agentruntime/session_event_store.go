package agentruntime

import (
	"context"
	"fmt"

	"github.com/oschina/mothx/internal/session"
)

// InspectSessionEventStore is the Runtime-owned read boundary for session
// content consistency. Adapters must not open event-store files directly.
func (r *SessionRuntime) InspectSessionEventStore(ctx context.Context) (session.EventStoreInspection, error) {
	if r == nil {
		return session.EventStoreInspection{}, fmt.Errorf("agent runtime is nil")
	}
	r.mu.RLock()
	manager := r.Manager
	r.mu.RUnlock()
	if manager == nil || manager.GetHeader() == nil {
		return session.EventStoreInspection{}, fmt.Errorf("session runtime is not bound")
	}
	return session.InspectEventStore(ctx, manager.GetSessionDir(), manager.GetHeader().ID)
}

// ReadCommittedSessionEvents exposes only replayable, SQLite-committed content
// through the shared Runtime boundary. In-doubt batches never reach adapters.
func (r *SessionRuntime) ReadCommittedSessionEvents(ctx context.Context) ([]session.EventBatch, error) {
	if r == nil {
		return nil, fmt.Errorf("agent runtime is nil")
	}
	r.mu.RLock()
	manager := r.Manager
	r.mu.RUnlock()
	if manager == nil || manager.GetHeader() == nil {
		return nil, fmt.Errorf("session runtime is not bound")
	}
	return session.ReadCommittedEventBatches(ctx, manager.GetSessionDir(), manager.GetHeader().ID)
}

// AppendSessionEventEntries is the Runtime-owned content write boundary. The
// caller supplies already-normalized canonical entries; lease fencing,
// EventStore fsync, intent commit, and metadata projection remain in session.
func (r *SessionRuntime) AppendSessionEventEntries(ctx context.Context, runID, turnID string, entries []interface{}) ([]string, error) {
	if r == nil {
		return nil, fmt.Errorf("agent runtime is nil")
	}
	r.mu.RLock()
	manager := r.Manager
	r.mu.RUnlock()
	if manager == nil || manager.GetHeader() == nil {
		return nil, fmt.Errorf("session runtime is not bound")
	}
	return manager.AppendEventStoreEntries(ctx, runID, turnID, entries)
}

// EventStoreWriteStats projects process-local EventStore pressure telemetry
// through Runtime. The counters are observational only and never authorize a
// write, lease change, or recovery action.
func (r *SessionRuntime) EventStoreWriteStats() (session.EventStoreStats, error) {
	if r == nil {
		return session.EventStoreStats{}, fmt.Errorf("agent runtime is nil")
	}
	r.mu.RLock()
	bound := r.Manager != nil && r.Manager.GetHeader() != nil
	r.mu.RUnlock()
	if !bound {
		return session.EventStoreStats{}, fmt.Errorf("session runtime is not bound")
	}
	return session.EventStoreWriteStats(), nil
}
