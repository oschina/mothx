package session

import (
	"testing"
	"time"
)

// withOwnershipClock resets the global view and installs a controllable clock.
func withOwnershipClock(t *testing.T) *time.Time {
	t.Helper()
	resetRuntimeOwnership()
	original := runtimeOwnershipClock
	current := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	runtimeOwnershipClock = func() time.Time { return current }
	t.Cleanup(func() {
		runtimeOwnershipClock = original
		resetRuntimeOwnership()
	})
	return &current
}

func acquiredNotification(db, session, origin string, epoch int64, seq uint64, expires time.Time) RuntimeLeaseNotification {
	return RuntimeLeaseNotification{
		Type: "acquired", DatabaseIdentity: db, SessionID: session,
		OriginInstanceID: origin, OwnerInstanceID: origin, OwnerPID: 4242,
		Origin: "execution", RunID: "run-" + session, Epoch: epoch, Seq: seq, ExpiresAt: expires.Unix(),
	}
}

func TestRuntimeOwnershipCacheTracksAcquireAndRelease(t *testing.T) {
	clock := withOwnershipClock(t)
	db := "/tmp/cache/sessions.db"

	ingestRuntimeOwnership(acquiredNotification(db, "s1", "peerA", 3, 1, clock.Add(time.Minute)))
	lookup := LookupRuntimeOwnership(db, "s1")
	if !lookup.Present || !lookup.Fresh || lookup.Uncertain {
		t.Fatalf("fresh acquire lookup = %#v", lookup)
	}
	if lookup.Entry.Epoch != 3 || lookup.Entry.OwnerPID != 4242 || lookup.Entry.RunID != "run-s1" {
		t.Fatalf("entry fields = %#v", lookup.Entry)
	}

	ingestRuntimeOwnership(RuntimeLeaseNotification{Type: "released", DatabaseIdentity: db, SessionID: "s1", OriginInstanceID: "peerA", Epoch: 3, Seq: 2})
	if lookup := LookupRuntimeOwnership(db, "s1"); lookup.Present || lookup.Uncertain {
		t.Fatalf("released entry must leave the cache: %#v", lookup)
	}
}

func TestRuntimeOwnershipCacheUnknownNeverMeansIdle(t *testing.T) {
	withOwnershipClock(t)
	if lookup := LookupRuntimeOwnership("/tmp/cache/sessions.db", "never-seen"); lookup.Present || lookup.Fresh || lookup.Uncertain {
		t.Fatalf("unknown session lookup = %#v, want the zero value", lookup)
	}
}

func TestRuntimeOwnershipCacheGapMarksOriginEntriesUncertain(t *testing.T) {
	clock := withOwnershipClock(t)
	db := "/tmp/cache/sessions.db"

	ingestRuntimeOwnership(acquiredNotification(db, "s1", "peerA", 1, 1, clock.Add(time.Minute)))
	ingestRuntimeOwnership(acquiredNotification(db, "s2", "peerA", 1, 3, clock.Add(time.Minute))) // gap: seq 2 lost

	if lookup := LookupRuntimeOwnership(db, "s1"); !lookup.Uncertain {
		t.Fatalf("gap must mark peerA's earlier entry uncertain: %#v", lookup)
	}
	if lookup := LookupRuntimeOwnership(db, "s2"); lookup.Uncertain || !lookup.Fresh {
		t.Fatalf("the message that closed the gap is itself known: %#v", lookup)
	}
}

func TestRuntimeOwnershipCacheRejectsOutOfOrderDuplicate(t *testing.T) {
	clock := withOwnershipClock(t)
	db := "/tmp/cache/sessions.db"

	ingestRuntimeOwnership(acquiredNotification(db, "s1", "peerA", 2, 5, clock.Add(time.Minute)))
	// A late copy of an older message for the same origin must not regress state.
	ingestRuntimeOwnership(acquiredNotification(db, "s1", "peerA", 1, 4, clock.Add(time.Minute)))
	if lookup := LookupRuntimeOwnership(db, "s1"); lookup.Entry.Epoch != 2 {
		t.Fatalf("stale message regressed the entry: %#v", lookup.Entry)
	}
}

func TestRuntimeOwnershipCacheConflictMarksUncertain(t *testing.T) {
	clock := withOwnershipClock(t)
	db := "/tmp/cache/sessions.db"

	ingestRuntimeOwnership(acquiredNotification(db, "s1", "peerA", 5, 1, clock.Add(time.Minute)))
	ingestRuntimeOwnership(acquiredNotification(db, "s1", "peerB", 5, 1, clock.Add(time.Minute)))

	if lookup := LookupRuntimeOwnership(db, "s1"); !lookup.Uncertain {
		t.Fatalf("two origins claiming epoch 5 must be uncertain: %#v", lookup)
	}
}

func TestRuntimeOwnershipCacheStaleEntryIsUncertain(t *testing.T) {
	clock := withOwnershipClock(t)
	db := "/tmp/cache/sessions.db"

	ingestRuntimeOwnership(acquiredNotification(db, "s1", "peerA", 1, 1, clock.Add(time.Minute)))
	*clock = clock.Add(runtimeOwnershipLivenessWindow + time.Second)
	if lookup := LookupRuntimeOwnership(db, "s1"); !lookup.Uncertain {
		t.Fatalf("an entry past the liveness window must be uncertain: %#v", lookup)
	}
}

func TestRuntimeOwnershipCachePrimeClearsUncertainty(t *testing.T) {
	clock := withOwnershipClock(t)
	db := "/tmp/cache/sessions.db"

	MarkRuntimeOwnershipUncertain(db, "s1")
	if lookup := LookupRuntimeOwnership(db, "s1"); !lookup.Uncertain {
		t.Fatalf("explicit uncertainty lookup = %#v", lookup)
	}
	PrimeRuntimeOwnership(db, "s1", &RuntimeLeaseSnapshot{
		SessionID: "s1", OwnerInstanceID: "peerA", OwnerPID: 7, Epoch: 4,
		Purpose: "execution", RunID: "run-s1", State: "active", ExpiresAt: clock.Add(time.Minute), Valid: true,
	}, "run-s1", "running")
	lookup := LookupRuntimeOwnership(db, "s1")
	if !lookup.Fresh || lookup.Uncertain || lookup.Entry.Epoch != 4 {
		t.Fatalf("primed lookup = %#v", lookup)
	}
	if !lookup.Entry.FromDatabase {
		t.Fatal("a primed entry must be marked as database-derived")
	}

	// A nil/inactive lease clears the entry so the next lookup re-reads.
	PrimeRuntimeOwnership(db, "s1", nil, "", "")
	if lookup := LookupRuntimeOwnership(db, "s1"); lookup.Present {
		t.Fatalf("cleared entry still present: %#v", lookup)
	}
}

func TestRuntimeOwnershipCacheAcceptsSnapshotAsRefreshAndRepair(t *testing.T) {
	clock := withOwnershipClock(t)
	db := "/tmp/cache/sessions.db"

	// A snapshot is how a peer that missed the original acquire learns ownership.
	ingestRuntimeOwnership(RuntimeLeaseNotification{
		Type: runtimeLeaseBusOwnershipSnapshot, DatabaseIdentity: db, SessionID: "s1",
		OriginInstanceID: "peerA", OwnerInstanceID: "peerA", OwnerPID: 9, Epoch: 2, Seq: 1,
		ExpiresAt: clock.Add(time.Minute).Unix(),
	})
	lookup := LookupRuntimeOwnership(db, "s1")
	if !lookup.Fresh || lookup.Entry.Epoch != 2 || lookup.Entry.OwnerPID != 9 {
		t.Fatalf("snapshot lookup = %#v", lookup)
	}
}

func TestRuntimeLeaseMetricsCountGapsAndUncertainty(t *testing.T) {
	clock := withOwnershipClock(t)
	resetRuntimeLeaseMetrics()
	db := "/tmp/metrics/sessions.db"

	ingestRuntimeOwnership(acquiredNotification(db, "s1", "peerA", 1, 1, clock.Add(time.Minute)))
	ingestRuntimeOwnership(acquiredNotification(db, "s2", "peerA", 1, 3, clock.Add(time.Minute))) // gap
	_ = LookupRuntimeOwnership(db, "s1")                                                          // uncertain: marked by the gap
	_ = LookupRuntimeOwnership(db, "absent")                                                      // uncertain: unknown session

	stats := RuntimeLeaseBusMetrics()
	if stats.Applied < 2 {
		t.Fatalf("applied = %d, want at least 2", stats.Applied)
	}
	if stats.Gaps != 1 {
		t.Fatalf("gaps = %d, want 1", stats.Gaps)
	}
	if stats.Uncertain < 2 {
		t.Fatalf("uncertain = %d, want at least 2", stats.Uncertain)
	}
}

func TestRuntimeOwnershipCacheIgnoresUnscopedNotifications(t *testing.T) {
	withOwnershipClock(t)
	// A database_rebuilt notice and an unscoped lease event carry no ownership.
	ingestRuntimeOwnership(RuntimeLeaseNotification{Type: runtimeLeaseBusDatabaseRebuilt, Path: "/tmp/x/sessions.db", OriginInstanceID: "peerA", Seq: 1})
	ingestRuntimeOwnership(RuntimeLeaseNotification{Type: "acquired", SessionID: "s1", OriginInstanceID: "peerA", Seq: 2})
	if len(runtimeOwnership.entries) != 0 {
		t.Fatalf("unscoped notifications populated the ownership cache: %#v", runtimeOwnership.entries)
	}
}
