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
