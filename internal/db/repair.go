package db

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// IndexRepair records a secondary index internal/db rebuilt while opening a
// database. An interrupted write can leave an index disagreeing with the table
// rows it derives from; rebuilding it is lossless, so the open succeeds instead
// of refusing a database whose canonical pages are intact.
//
// The repair is reported rather than hidden: a database that needed one has
// already survived a failed write, and front-ends drain these records through
// TakeIndexRepairs so the user learns about it exactly once.
type IndexRepair struct {
	// Path is the canonical database path whose indexes were rebuilt.
	Path string
	// Cause is the integrity-check line that triggered the repair, which names
	// the affected index.
	Cause string
	// At is when the repair happened.
	At time.Time
}

// Describe returns the one-line operator-facing summary of a repair.
func (r IndexRepair) Describe() string {
	return fmt.Sprintf("rebuilt stale SQLite indexes in %s after an integrity check reported %q", r.Path, r.Cause)
}

var indexRepairLog = struct {
	sync.Mutex
	entries []IndexRepair
}{}

// recordIndexRepair stores a completed repair and logs it. The log line is the
// notice headless entry points (serve, ACP, channels) are guaranteed to reach,
// so a self-healed database is never silent. Unlike a migration rebuild this
// replaces no file, so it is deliberately not announced over the advisory bus:
// peers must not retire a healthy connection because of it.
func recordIndexRepair(repair IndexRepair) {
	indexRepairLog.Lock()
	indexRepairLog.entries = append(indexRepairLog.entries, repair)
	indexRepairLog.Unlock()
	log.Printf("[db] %s", repair.Describe())
}

// TakeIndexRepairs returns the repairs recorded since the last call and clears
// them.
func TakeIndexRepairs() []IndexRepair {
	indexRepairLog.Lock()
	defer indexRepairLog.Unlock()
	entries := indexRepairLog.entries
	indexRepairLog.entries = nil
	return entries
}

// IntegrityFindingKind classifies the outcome of a startup integrity check that
// finished after its database was already open.
type IntegrityFindingKind string

const (
	// IntegrityIndexRepaired reports that a stale secondary index was rebuilt.
	// No row changed.
	IntegrityIndexRepaired IntegrityFindingKind = "index_repaired"
	// IntegrityIndexUnrepaired reports that a stale index was found but could
	// not be rebuilt, so queries answered from that index may miss rows until a
	// later startup check succeeds.
	IntegrityIndexUnrepaired IntegrityFindingKind = "index_unrepaired"
	// IntegrityCorrupt reports structural damage. The database was left
	// untouched: whether to rebuild it is the operator's decision, never a side
	// effect of a check.
	IntegrityCorrupt IntegrityFindingKind = "corrupt"
	// IntegrityCheckFailed reports that the check itself could not complete, for
	// example on an I/O error. It is not a corruption verdict.
	IntegrityCheckFailed IntegrityFindingKind = "check_failed"
)

// IntegrityFinding reports the outcome of a startup integrity check that
// finished in the background, after the open had already returned the database
// (see StartupCheckMode). Unlike a synchronous check it cannot refuse the open,
// so the result is delivered instead: failures are logged, kept for one-shot
// callers to drain through TakeIntegrityFailures, and pushed to every
// SubscribeIntegrityFindings sink so a long-running front-end can tell the user
// the moment the verdict is known.
type IntegrityFinding struct {
	// Path is the canonical database path that was checked.
	Path string
	// Kind says what the check concluded.
	Kind IntegrityFindingKind
	// Report is the quick_check output behind the finding: the line that named a
	// stale index, or the first damage line for corruption.
	Report string
	// Err is the failure for every kind except IntegrityIndexRepaired. A
	// corruption finding wraps *IntegrityError.
	Err error
	// At is when the check finished.
	At time.Time
}

// Describe returns the one-line operator-facing summary of a finding.
func (f IntegrityFinding) Describe() string {
	switch f.Kind {
	case IntegrityIndexRepaired:
		return fmt.Sprintf("rebuilt stale SQLite indexes in %s after an integrity check reported %q", f.Path, f.Report)
	case IntegrityIndexUnrepaired:
		return fmt.Sprintf("an integrity check of %s reported %q but the index was not rebuilt: %v", f.Path, f.Report, f.Err)
	case IntegrityCorrupt:
		return fmt.Sprintf("SQLite integrity check failed for %s: %s", f.Path, f.Report)
	default:
		return fmt.Sprintf("SQLite integrity check of %s could not complete: %v", f.Path, f.Err)
	}
}

var integrityFindings = struct {
	sync.Mutex
	failures []IntegrityFinding
	sinks    map[uint64]func(IntegrityFinding)
	nextID   uint64
}{sinks: make(map[uint64]func(IntegrityFinding))}

// SubscribeIntegrityFindings registers a sink that receives every startup
// integrity finding from now on. The sink runs on the check's goroutine and must
// not block. The returned function unsubscribes it.
func SubscribeIntegrityFindings(sink func(IntegrityFinding)) func() {
	if sink == nil {
		return func() {}
	}
	integrityFindings.Lock()
	integrityFindings.nextID++
	id := integrityFindings.nextID
	integrityFindings.sinks[id] = sink
	integrityFindings.Unlock()
	return func() {
		integrityFindings.Lock()
		delete(integrityFindings.sinks, id)
		integrityFindings.Unlock()
	}
}

// TakeIntegrityFailures returns the integrity failures recorded since the last
// call and clears them, so a one-shot caller can report them exactly once.
// Repairs are not kept here; TakeIndexRepairs already reports those.
func TakeIntegrityFailures() []IntegrityFinding {
	integrityFindings.Lock()
	defer integrityFindings.Unlock()
	entries := integrityFindings.failures
	integrityFindings.failures = nil
	return entries
}

// publishIntegrityFinding records and announces one background finding. The log
// line is the notice headless entry points (serve, ACP, channels) are guaranteed
// to reach; an index repair is only pushed, because recordIndexRepair already
// logged it and reported it through TakeIndexRepairs.
func publishIntegrityFinding(finding IntegrityFinding, ok bool) {
	if !ok {
		return
	}
	integrityFindings.Lock()
	if finding.Kind != IntegrityIndexRepaired {
		integrityFindings.failures = append(integrityFindings.failures, finding)
	}
	sinks := make([]func(IntegrityFinding), 0, len(integrityFindings.sinks))
	for _, sink := range integrityFindings.sinks {
		sinks = append(sinks, sink)
	}
	integrityFindings.Unlock()
	if finding.Kind != IntegrityIndexRepaired {
		log.Printf("[db] %s", finding.Describe())
	}
	for _, sink := range sinks {
		sink(finding)
	}
}
