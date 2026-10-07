package session

import (
	"strings"
	"sync"
	"time"
)

// runtimeOwnershipLivenessWindow bounds how long a synchronized ownership entry
// stays fresh without a refresh. The owning process republishes its held leases
// periodically (ownership snapshots) and on every change; if it goes silent for
// longer than this window the entry is treated as uncertain and the reader
// falls back to SQLite. Heartbeat renewals are deliberately not broadcast, so
// this window - not the lease ExpiresAt - is the freshness signal.
const runtimeOwnershipLivenessWindow = 30 * time.Second

// runtimeOwnershipPruneThreshold gates the (rarely needed) stale-entry sweep so
// normal ingestion stays O(1) even with a global, host-wide view.
const runtimeOwnershipPruneThreshold = 512

// runtimeOwnershipClock is overridable in tests.
var runtimeOwnershipClock = func() time.Time { return time.Now() }

// RuntimeOwnershipEntry is one process's synchronized view of who holds a
// Session-wide lease. It is a projection hint derived from a SQLite lease row
// (either read locally or announced by the owner over the advisory bus); it
// never authorizes a write.
type RuntimeOwnershipEntry struct {
	DatabaseIdentity string
	SessionID        string
	OwnerInstanceID  string
	OwnerPID         int
	Epoch            int64
	Purpose          string
	RunID            string
	State            string
	RunStatus        string
	Phase            string
	ExpiresAt        time.Time
	Seq              uint64
	OriginInstanceID string
	AppliedAt        time.Time
	// FromDatabase marks an entry primed by a local SQLite read rather than a
	// peer announcement. Both are hints; this is for diagnostics only.
	FromDatabase bool
}

// RuntimeOwnershipLookup is the result of a cache query. Exactly one of the
// following holds:
//   - Present && Fresh: use the entry to project ownership without a DB read;
//   - Uncertain: the entry is stale, conflict-marked, or gap-marked; re-read SQLite;
//   - zero value: the session is unknown to the cache; re-read SQLite (absence
//     never means "idle").
type RuntimeOwnershipLookup struct {
	Entry     RuntimeOwnershipEntry
	Present   bool
	Fresh     bool
	Uncertain bool
}

type runtimeOwnershipKey struct {
	databaseIdentity string
	sessionID        string
}

func (k runtimeOwnershipKey) valid() bool {
	return k.databaseIdentity != "" && k.sessionID != ""
}

type runtimeOwnershipCacheState struct {
	sync.Mutex
	entries   map[runtimeOwnershipKey]*RuntimeOwnershipEntry
	originSeq map[string]uint64
	uncertain map[runtimeOwnershipKey]time.Time
}

var runtimeOwnership = runtimeOwnershipCacheState{
	entries:   make(map[runtimeOwnershipKey]*RuntimeOwnershipEntry),
	originSeq: make(map[string]uint64),
	uncertain: make(map[runtimeOwnershipKey]time.Time),
}

func runtimeOwnershipNotificationKey(n RuntimeLeaseNotification) (runtimeOwnershipKey, bool) {
	key := runtimeOwnershipKey{databaseIdentity: strings.TrimSpace(n.DatabaseIdentity), sessionID: strings.TrimSpace(n.SessionID)}
	if !key.valid() {
		return key, false
	}
	return key, true
}

func validRuntimeOwnershipEventType(eventType string) bool {
	switch eventType {
	case "acquired", "released", "lost", "state_changed", "ownership_snapshot":
		return true
	default:
		return false
	}
}

func staleByEpoch(existing *RuntimeOwnershipEntry, epoch int64) bool {
	return existing != nil && existing.Epoch != 0 && epoch != 0 && epoch < existing.Epoch
}

// ingestRuntimeOwnership applies one ownership notification to the global
// in-memory view. It runs on the bus reader goroutine and never touches SQLite.
func ingestRuntimeOwnership(n RuntimeLeaseNotification) {
	if !validRuntimeOwnershipEventType(n.Type) {
		return
	}
	key, ok := runtimeOwnershipNotificationKey(n)
	if !ok {
		// Unscoped or database-rebuild notices are not ownership facts; readers
		// fall back to SQLite for them.
		return
	}
	now := runtimeOwnershipClock()
	runtimeOwnership.Lock()
	defer runtimeOwnership.Unlock()
	runtimeOwnership.pruneLocked(now)

	if n.Seq != 0 {
		lastSeq := runtimeOwnership.originSeq[n.OriginInstanceID]
		if n.Seq <= lastSeq {
			return // stale or duplicate
		}
		if n.Seq > lastSeq+1 {
			runtimeLeaseMetrics.gaps.Add(1)
			// A gap means we lost at least one message from this origin; mark all
			// of its known entries uncertain so readers re-read SQLite.
			for candidate, entry := range runtimeOwnership.entries {
				if entry.OriginInstanceID == n.OriginInstanceID {
					runtimeOwnership.uncertain[candidate] = now
				}
			}
		}
		runtimeOwnership.originSeq[n.OriginInstanceID] = n.Seq
	}

	switch n.Type {
	case "released", "lost":
		existing := runtimeOwnership.entries[key]
		if staleByEpoch(existing, n.Epoch) {
			return
		}
		delete(runtimeOwnership.entries, key)
		delete(runtimeOwnership.uncertain, key)
		runtimeLeaseMetrics.applied.Add(1)
	case "state_changed":
		// state_changed carries no ownership identity; refresh an existing entry
		// (and its optional Run hints) but never invent one from it.
		if existing := runtimeOwnership.entries[key]; existing != nil {
			existing.AppliedAt = now
			if n.RunStatus != "" {
				existing.RunStatus = n.RunStatus
			}
			if n.Phase != "" {
				existing.Phase = n.Phase
			}
			runtimeLeaseMetrics.applied.Add(1)
		}
	case "acquired", "ownership_snapshot":
		existing := runtimeOwnership.entries[key]
		if existing != nil && existing.OriginInstanceID != "" && existing.OriginInstanceID != n.OriginInstanceID &&
			existing.Epoch != 0 && n.Epoch != 0 && n.Epoch == existing.Epoch {
			// Two owners claim the same epoch: contradictory, do not trust either.
			runtimeOwnership.uncertain[key] = now
			runtimeLeaseMetrics.uncertain.Add(1)
			return
		}
		if staleByEpoch(existing, n.Epoch) {
			return
		}
		runtimeOwnership.entries[key] = ownershipEntryFromNotification(key, n, now)
		delete(runtimeOwnership.uncertain, key)
		runtimeLeaseMetrics.applied.Add(1)
	}
}

func ownershipEntryFromNotification(key runtimeOwnershipKey, n RuntimeLeaseNotification, now time.Time) *RuntimeOwnershipEntry {
	entry := &RuntimeOwnershipEntry{
		DatabaseIdentity: key.databaseIdentity,
		SessionID:        key.sessionID,
		OwnerInstanceID:  n.OwnerInstanceID,
		OwnerPID:         n.OwnerPID,
		Epoch:            n.Epoch,
		Purpose:          n.Origin,
		RunID:            n.RunID,
		State:            "active",
		RunStatus:        n.RunStatus,
		Phase:            n.Phase,
		Seq:              n.Seq,
		OriginInstanceID: n.OriginInstanceID,
		AppliedAt:        now,
	}
	if n.ExpiresAt != 0 {
		entry.ExpiresAt = time.Unix(n.ExpiresAt, 0).UTC()
	}
	return entry
}

// LookupRuntimeOwnership returns the synchronized ownership view for one
// session. An empty/zero result means "unknown, re-read SQLite".
func LookupRuntimeOwnership(databaseIdentity, sessionID string) RuntimeOwnershipLookup {
	key := runtimeOwnershipKey{databaseIdentity: strings.TrimSpace(databaseIdentity), sessionID: strings.TrimSpace(sessionID)}
	if !key.valid() {
		return RuntimeOwnershipLookup{}
	}
	now := runtimeOwnershipClock()
	runtimeOwnership.Lock()
	defer runtimeOwnership.Unlock()
	if _, uncertain := runtimeOwnership.uncertain[key]; uncertain {
		runtimeLeaseMetrics.uncertain.Add(1)
		return RuntimeOwnershipLookup{Uncertain: true}
	}
	entry := runtimeOwnership.entries[key]
	if entry == nil {
		runtimeLeaseMetrics.uncertain.Add(1)
		return RuntimeOwnershipLookup{}
	}
	if now.Sub(entry.AppliedAt) > runtimeOwnershipLivenessWindow {
		runtimeLeaseMetrics.uncertain.Add(1)
		return RuntimeOwnershipLookup{Entry: *entry, Present: true, Uncertain: true}
	}
	return RuntimeOwnershipLookup{Entry: *entry, Present: true, Fresh: true}
}

// PrimeRuntimeOwnership records the authoritative SQLite view for one session
// and clears any uncertainty, so a fresh read immediately benefits later reads.
// A nil, inactive, or expired lease clears the entry (a subsequent lookup then
// reports "unknown" and re-reads, which is the safe direction).
func PrimeRuntimeOwnership(databaseIdentity, sessionID string, lease *RuntimeLeaseSnapshot, runID, runStatus string) {
	key := runtimeOwnershipKey{databaseIdentity: strings.TrimSpace(databaseIdentity), sessionID: strings.TrimSpace(sessionID)}
	if !key.valid() {
		return
	}
	now := runtimeOwnershipClock()
	runtimeOwnership.Lock()
	defer runtimeOwnership.Unlock()
	if lease == nil || lease.State != "active" || !lease.Valid {
		delete(runtimeOwnership.entries, key)
		delete(runtimeOwnership.uncertain, key)
		return
	}
	if runID == "" {
		runID = lease.RunID
	}
	entry := &RuntimeOwnershipEntry{
		DatabaseIdentity: key.databaseIdentity,
		SessionID:        key.sessionID,
		OwnerInstanceID:  lease.OwnerInstanceID,
		OwnerPID:         lease.OwnerPID,
		Epoch:            lease.Epoch,
		Purpose:          string(lease.Purpose),
		RunID:            runID,
		RunStatus:        runStatus,
		State:            lease.State,
		ExpiresAt:        lease.ExpiresAt,
		OriginInstanceID: lease.OwnerInstanceID,
		AppliedAt:        now,
		FromDatabase:     true,
	}
	runtimeOwnership.entries[key] = entry
	delete(runtimeOwnership.uncertain, key)
}

// MarkRuntimeOwnershipUncertain forces the next lookup for a session to re-read
// SQLite. Adapters and reconciliation call it when they learn the view may have
// drifted (for example after a database rebuild).
func MarkRuntimeOwnershipUncertain(databaseIdentity, sessionID string) {
	key := runtimeOwnershipKey{databaseIdentity: strings.TrimSpace(databaseIdentity), sessionID: strings.TrimSpace(sessionID)}
	if !key.valid() {
		return
	}
	runtimeOwnership.Lock()
	runtimeOwnership.uncertain[key] = runtimeOwnershipClock()
	runtimeOwnership.Unlock()
}

func (c *runtimeOwnershipCacheState) pruneLocked(now time.Time) {
	if len(c.entries) < runtimeOwnershipPruneThreshold {
		return
	}
	cutoff := now.Add(-10 * runtimeOwnershipLivenessWindow)
	for key, entry := range c.entries {
		if entry.AppliedAt.Before(cutoff) {
			delete(c.entries, key)
			delete(c.uncertain, key)
		}
	}
}

// resetRuntimeOwnership clears the global view. Test-only helper.
func resetRuntimeOwnership() {
	runtimeOwnership.Lock()
	defer runtimeOwnership.Unlock()
	runtimeOwnership.entries = make(map[runtimeOwnershipKey]*RuntimeOwnershipEntry)
	runtimeOwnership.originSeq = make(map[string]uint64)
	runtimeOwnership.uncertain = make(map[runtimeOwnershipKey]time.Time)
}
