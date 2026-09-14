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

// sqliteStatsSnapshot renders the cumulative contention counters as JSON:
// transient busy begin retries with their backoff total, and the begin-call
// attempt count / total wait / slowest single wait. Durations are reported in
// whole milliseconds so the values stay readable in /debug/vars output.
func sqliteStatsSnapshot() any {
	hits, retryWait := BusyRetryStats()
	beginCount, beginTotal, beginMax := BeginWaitStats()
	return map[string]int64{
		"busyRetryHits":    int64(hits),
		"busyRetryWaitMs":  retryWait.Milliseconds(),
		"beginCount":       int64(beginCount),
		"beginTotalWaitMs": beginTotal.Milliseconds(),
		"beginMaxWaitMs":   beginMax.Milliseconds(),
	}
}
