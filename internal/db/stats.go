package db

import "expvar"

// sqliteExpvarKey is the expvar name under which the process-wide SQLite
// contention metrics are published. Debug entry points serve it through the
// standard /debug/vars handler (see internal/debugpprof); every process
// exposes its own counters, which keeps the per-process tagging the write
// pressure proposal asks for.
const sqliteExpvarKey = "mothx_sqlite"

func init() {
	expvar.Publish(sqliteExpvarKey, expvar.Func(sqliteStatsSnapshot))
}

// sqliteStatsSnapshot renders the cumulative contention/checkpoint counters and
// the live WAL sizes as JSON: transient busy begin retries with their backoff
// total, the begin-call attempt count / total wait / slowest single wait with
// bucket-approximated p50 and p99, the checkpoint attempt/failure and frame
// counts, and the write-ahead log size of each open database. Durations are
// reported in whole milliseconds and sizes in bytes so the values stay readable
// in /debug/vars output.
func sqliteStatsSnapshot() any {
	hits, retryWait := BusyRetryStats()
	beginCount, beginTotal, beginMax := BeginWaitStats()
	beginP50, beginP99 := BeginWaitQuantiles()
	attempts, failures, busyFrames, logFrames, checkpointed := CheckpointStats()
	return map[string]any{
		"busyRetryHits":        int64(hits),
		"busyRetryWaitMs":      retryWait.Milliseconds(),
		"beginCount":           int64(beginCount),
		"beginTotalWaitMs":     beginTotal.Milliseconds(),
		"beginMaxWaitMs":       beginMax.Milliseconds(),
		"beginP50WaitMs":       beginP50.Milliseconds(),
		"beginP99WaitMs":       beginP99.Milliseconds(),
		"checkpointAttempts":   int64(attempts),
		"checkpointFailures":   int64(failures),
		"checkpointBusyFrames": busyFrames,
		"checkpointLogFrames":  logFrames,
		"checkpointDoneFrames": checkpointed,
		"wal":                  WalStats(),
		"pool":                 PoolStats(),
	}
}
