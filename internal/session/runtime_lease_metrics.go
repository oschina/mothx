package session

import "sync/atomic"

// RuntimeLeaseBusStats is a snapshot of the advisory ownership bus and cache
// counters. They are diagnostics only: none of them authorizes anything.
type RuntimeLeaseBusStats struct {
	// Published counts datagrams written to the loopback broadcast.
	Published uint64
	// SendFailures counts publishes that never reached the socket.
	SendFailures uint64
	// Received counts accepted ownership datagrams (after validity and self
	// filtering).
	Received uint64
	// Invalid counts dropped datagrams that failed to parse or validate.
	Invalid uint64
	// SelfSkipped counts loopback copies of this process's own announcements.
	SelfSkipped uint64
	// Duplicates counts datagrams suppressed by the dedupe window.
	Duplicates uint64
	// Applied counts notifications that mutated the ownership cache.
	Applied uint64
	// Gaps counts detected per-origin sequence gaps (a lost datagram).
	Gaps uint64
	// Uncertain counts lookups that could not be answered from a fresh entry
	// (miss, staleness, gap, or conflict) and therefore require a SQLite read.
	Uncertain uint64
}

var runtimeLeaseMetrics struct {
	published    atomic.Uint64
	sendFailures atomic.Uint64
	received     atomic.Uint64
	invalid      atomic.Uint64
	selfSkipped  atomic.Uint64
	duplicates   atomic.Uint64
	applied      atomic.Uint64
	gaps         atomic.Uint64
	uncertain    atomic.Uint64
}

// RuntimeLeaseBusMetrics returns a snapshot of the ownership bus counters.
func RuntimeLeaseBusMetrics() RuntimeLeaseBusStats {
	return RuntimeLeaseBusStats{
		Published:    runtimeLeaseMetrics.published.Load(),
		SendFailures: runtimeLeaseMetrics.sendFailures.Load(),
		Received:     runtimeLeaseMetrics.received.Load(),
		Invalid:      runtimeLeaseMetrics.invalid.Load(),
		SelfSkipped:  runtimeLeaseMetrics.selfSkipped.Load(),
		Duplicates:   runtimeLeaseMetrics.duplicates.Load(),
		Applied:      runtimeLeaseMetrics.applied.Load(),
		Gaps:         runtimeLeaseMetrics.gaps.Load(),
		Uncertain:    runtimeLeaseMetrics.uncertain.Load(),
	}
}

func resetRuntimeLeaseMetrics() {
	runtimeLeaseMetrics.published.Store(0)
	runtimeLeaseMetrics.sendFailures.Store(0)
	runtimeLeaseMetrics.received.Store(0)
	runtimeLeaseMetrics.invalid.Store(0)
	runtimeLeaseMetrics.selfSkipped.Store(0)
	runtimeLeaseMetrics.duplicates.Store(0)
	runtimeLeaseMetrics.applied.Store(0)
	runtimeLeaseMetrics.gaps.Store(0)
	runtimeLeaseMetrics.uncertain.Store(0)
}
