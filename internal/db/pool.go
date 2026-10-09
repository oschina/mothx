package db

import "sort"

// DatabasePoolStat reports one open database's connection-pool counters. Every
// managed connection uses SetMaxOpenConns(1), so WaitCount/WaitDuration is the
// direct measure of how often and how long a transaction or query had to queue
// for the single connection: a growing value under concurrent sessions is the
// baseline that would justify (or rule out) a separate read connection pool.
type DatabasePoolStat struct {
	Path               string `json:"path"`
	MaxOpenConnections int    `json:"maxOpenConnections"`
	OpenConnections    int    `json:"openConnections"`
	InUse              int    `json:"inUse"`
	Idle               int    `json:"idle"`
	WaitCount          int64  `json:"waitCount"`
	WaitDurationMillis int64  `json:"waitDurationMs"`
	MaxIdleClosed      int64  `json:"maxIdleClosed"`
	MaxIdleTimeClosed  int64  `json:"maxIdleTimeClosed"`
	MaxLifetimeClosed  int64  `json:"maxLifetimeClosed"`
}

// PoolStats reports the connection-pool counters for every database this
// process currently holds open, sorted by path. It is the measurement half of
// deciding whether the single-writer connection model needs a read pool; it
// changes no behavior.
func PoolStats() []DatabasePoolStat {
	state.Lock()
	stats := make([]DatabasePoolStat, 0, len(state.dbs))
	for path, connection := range state.dbs {
		raw := connection.Stats()
		stats = append(stats, DatabasePoolStat{
			Path:               path,
			MaxOpenConnections: raw.MaxOpenConnections,
			OpenConnections:    raw.OpenConnections,
			InUse:              raw.InUse,
			Idle:               raw.Idle,
			WaitCount:          raw.WaitCount,
			WaitDurationMillis: raw.WaitDuration.Milliseconds(),
			MaxIdleClosed:      raw.MaxIdleClosed,
			MaxIdleTimeClosed:  raw.MaxIdleTimeClosed,
			MaxLifetimeClosed:  raw.MaxLifetimeClosed,
		})
	}
	state.Unlock()

	sort.Slice(stats, func(i, j int) bool { return stats[i].Path < stats[j].Path })
	return stats
}
