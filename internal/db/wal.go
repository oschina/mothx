package db

import (
	"os"
	"sort"
	"sync/atomic"
)

// checkpointCounters accumulates WAL checkpoint outcomes process-wide. Under
// WAL + synchronous(NORMAL) a commit no longer fsyncs while holding the write
// lock, so fsync cost moves to checkpoint time; these counters (and the live WAL
// size in WalStats) are how the write pressure proposal tracks whether
// checkpointing keeps up with write pressure. See
// docs/proposal/sqlite-write-pressure-reduction-proposal.md §4.1.
var checkpointCounters = struct {
	attempts     atomic.Uint64
	failures     atomic.Uint64
	busyFrames   atomic.Uint64
	logFrames    atomic.Uint64
	checkpointed atomic.Uint64
}{}

// recordCheckpoint records one PRAGMA wal_checkpoint(PASSIVE/TRUNCATE) result.
// The three integer columns are SQLite's (busy, log, checkpointed) frame counts;
// they are only accumulated on success because a failed checkpoint reports no
// meaningful progress.
func recordCheckpoint(busy, logFrames, checkpointed int, err error) {
	checkpointCounters.attempts.Add(1)
	if err != nil {
		checkpointCounters.failures.Add(1)
		return
	}
	if busy > 0 {
		checkpointCounters.busyFrames.Add(uint64(busy))
	}
	if logFrames > 0 {
		checkpointCounters.logFrames.Add(uint64(logFrames))
	}
	if checkpointed > 0 {
		checkpointCounters.checkpointed.Add(uint64(checkpointed))
	}
}

// CheckpointStats returns the cumulative checkpoint attempt/failure counts and
// the accumulated (busy, log, checkpointed) frame counts since process start.
func CheckpointStats() (attempts, failures uint64, busyFrames, logFrames, checkpointed int64) {
	return checkpointCounters.attempts.Load(),
		checkpointCounters.failures.Load(),
		int64(checkpointCounters.busyFrames.Load()),
		int64(checkpointCounters.logFrames.Load()),
		int64(checkpointCounters.checkpointed.Load())
}

// DatabaseWalStat reports one cached database's on-disk write-ahead log size.
type DatabaseWalStat struct {
	Path     string `json:"path"`
	WalBytes int64  `json:"walBytes"`
}

// WalStats reports the write-ahead log size for every database this process
// currently holds open. A WAL that grows without shrinking between checkpoints
// is the direct signal that write pressure outpaces checkpointing.
func WalStats() []DatabaseWalStat {
	state.Lock()
	paths := make([]string, 0, len(state.dbs))
	for path := range state.dbs {
		paths = append(paths, path)
	}
	state.Unlock()

	sort.Strings(paths)
	stats := make([]DatabaseWalStat, 0, len(paths))
	for _, path := range paths {
		var size int64
		if info, err := os.Stat(path + "-wal"); err == nil {
			size = info.Size()
		}
		stats = append(stats, DatabaseWalStat{Path: path, WalBytes: size})
	}
	return stats
}
