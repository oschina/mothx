package session

import (
	"expvar"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// leaseHealthExpvarKey is the expvar name under which this process publishes its
// runtime lease heartbeat health and fence-loss counter. Debug entry points
// serve it through /debug/vars next to mothx_sqlite; every process exposes its
// own values.
const leaseHealthExpvarKey = "mothx_runtime_lease"

// LeaseHeartbeatHealth is a diagnostics-only view of this process's runtime
// lease heartbeat for one session database. It never authorizes anything:
// ownership is decided solely by the fenced owner/epoch/token CAS, so a stalled
// heartbeat (ConsecutiveFailures > 0 with a stale LastSuccessAt) is an
// availability problem to retry, not ownership loss. A non-zero FenceLosses
// means a renewal actually ran and the CAS no longer matched, i.e. another
// process took the session over. DatabaseIdentity is the session database's
// base name: the local /debug/vars surface does not expose absolute paths.
type LeaseHeartbeatHealth struct {
	DatabaseIdentity    string        `json:"databaseIdentity"`
	LastAttemptAt       time.Time     `json:"lastAttemptAt,omitempty"`
	LastSuccessAt       time.Time     `json:"lastSuccessAt,omitempty"`
	ConsecutiveFailures int           `json:"consecutiveFailures"`
	Attempts            uint64        `json:"attempts"`
	Failures            uint64        `json:"failures"`
	FenceLosses         uint64        `json:"fenceLosses"`
	MaxRenewDuration    time.Duration `json:"maxRenewDurationNs"`
}

type leaseHeartbeatHealthEntry struct {
	lastAttempt time.Time
	lastSuccess time.Time
	consecutive int
	attempts    uint64
	failures    uint64
	fenceLosses uint64
	maxRenew    time.Duration
}

var leaseHeartbeatHealth = struct {
	sync.Mutex
	byDir map[string]*leaseHeartbeatHealthEntry
}{byDir: make(map[string]*leaseHeartbeatHealthEntry)}

// leaseFenceLossCount counts leases this process lost to a fenced takeover
// across all databases. It is the owner-side fence-health signal from the write
// pressure proposal (§4.1): a healthy owner keeps it at zero.
var leaseFenceLossCount atomic.Uint64

func heartbeatHealthEntryLocked(dirKey string) *leaseHeartbeatHealthEntry {
	entry := leaseHeartbeatHealth.byDir[dirKey]
	if entry == nil {
		entry = &leaseHeartbeatHealthEntry{}
		leaseHeartbeatHealth.byDir[dirKey] = entry
	}
	return entry
}

// recordHeartbeatSuccess records a heartbeat tick whose batch renewal
// transaction committed. elapsed is the whole-tick duration, including any
// retries, so MaxRenewDuration captures the slowest observed tick.
func recordHeartbeatSuccess(dirKey string, elapsed time.Duration) {
	leaseHeartbeatHealth.Lock()
	defer leaseHeartbeatHealth.Unlock()
	now := time.Now().UTC()
	entry := heartbeatHealthEntryLocked(dirKey)
	entry.lastAttempt = now
	entry.lastSuccess = now
	entry.consecutive = 0
	entry.attempts++
	if elapsed > entry.maxRenew {
		entry.maxRenew = elapsed
	}
}

// recordHeartbeatFailure records a heartbeat tick that exhausted its renewal
// budget without a successful commit. It is explicitly not ownership loss.
func recordHeartbeatFailure(dirKey string, elapsed time.Duration) {
	leaseHeartbeatHealth.Lock()
	defer leaseHeartbeatHealth.Unlock()
	entry := heartbeatHealthEntryLocked(dirKey)
	entry.lastAttempt = time.Now().UTC()
	entry.consecutive++
	entry.attempts++
	entry.failures++
	if elapsed > entry.maxRenew {
		entry.maxRenew = elapsed
	}
}

// recordLeaseFenceLoss records that a renewal actually executed and the fenced
// CAS no longer matched, so this process lost the lease to another owner.
func recordLeaseFenceLoss(dirKey string) {
	leaseFenceLossCount.Add(1)
	leaseHeartbeatHealth.Lock()
	defer leaseHeartbeatHealth.Unlock()
	heartbeatHealthEntryLocked(dirKey).fenceLosses++
}

// LeaseLostCount returns the process-wide count of leases lost to a fenced
// takeover. A healthy owner keeps it at zero.
func LeaseLostCount() uint64 {
	return leaseFenceLossCount.Load()
}

// LeaseHeartbeatHealthStats returns one health row per session database this
// process has heartbeated, sorted by database identity for stable output.
func LeaseHeartbeatHealthStats() []LeaseHeartbeatHealth {
	leaseHeartbeatHealth.Lock()
	defer leaseHeartbeatHealth.Unlock()
	stats := make([]LeaseHeartbeatHealth, 0, len(leaseHeartbeatHealth.byDir))
	for dirKey, entry := range leaseHeartbeatHealth.byDir {
		stats = append(stats, LeaseHeartbeatHealth{
			DatabaseIdentity:    filepath.Base(dirKey),
			LastAttemptAt:       entry.lastAttempt,
			LastSuccessAt:       entry.lastSuccess,
			ConsecutiveFailures: entry.consecutive,
			Attempts:            entry.attempts,
			Failures:            entry.failures,
			FenceLosses:         entry.fenceLosses,
			MaxRenewDuration:    entry.maxRenew,
		})
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].DatabaseIdentity < stats[j].DatabaseIdentity })
	return stats
}

func leaseHealthSnapshot() any {
	return map[string]any{
		"leaseLostCount": LeaseLostCount(),
		"heartbeat":      LeaseHeartbeatHealthStats(),
	}
}

func init() {
	expvar.Publish(leaseHealthExpvarKey, expvar.Func(leaseHealthSnapshot))
}
