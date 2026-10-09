package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/uptrace/bun"
)

func TestBeginWaitBucketUsesInclusiveUpperBounds(t *testing.T) {
	cases := []struct {
		elapsed time.Duration
		want    int
	}{
		{0, 0},
		{100 * time.Microsecond, 0},
		{101 * time.Microsecond, 1},
		{30 * time.Millisecond, 8},                // first bound >= 30ms is the 50ms bucket
		{20 * time.Second, len(beginWaitBuckets)}, // overflow
	}
	for _, tc := range cases {
		if got := beginWaitBucket(tc.elapsed); got != tc.want {
			t.Fatalf("beginWaitBucket(%s) = %d, want %d", tc.elapsed, got, tc.want)
		}
	}
}

func TestBeginQuantileReturnsBucketUpperBound(t *testing.T) {
	cumulative := make([]uint64, len(beginWaitBuckets)+1)
	var total uint64
	for i := 0; i < 100; i++ {
		cumulative[3]++ // all samples in the 1ms bucket
		total++
	}
	p50 := beginQuantile(cumulative, total, 0.50, 0)
	p99 := beginQuantile(cumulative, total, 0.99, 0)
	if p50 != beginWaitBuckets[3] || p99 != beginWaitBuckets[3] {
		t.Fatalf("quantiles = (%s, %s), want both %s", p50, p99, beginWaitBuckets[3])
	}

	// A quantile spanning buckets reports the upper bound of the bucket that
	// first reaches the target, so it is never an understatement.
	mixed := make([]uint64, len(beginWaitBuckets)+1)
	mixed[0] = 90 // 100us
	mixed[6] = 10 // 10ms
	p50 = beginQuantile(mixed, 100, 0.50, 0)
	if p50 != beginWaitBuckets[0] {
		t.Fatalf("p50 = %s, want %s (90%% of samples are faster)", p50, beginWaitBuckets[0])
	}
	p99 = beginQuantile(mixed, 100, 0.99, 0)
	if p99 != beginWaitBuckets[6] {
		t.Fatalf("p99 = %s, want %s", p99, beginWaitBuckets[6])
	}
}

// TestBeginQuantileOverflowUsesObservedMaximum pins that a quantile landing in
// the overflow bucket reports the observed maximum (an upper bound) instead of
// being capped at the last bounded bucket's upper bound, which would understate
// the slow tail.
func TestBeginQuantileOverflowUsesObservedMaximum(t *testing.T) {
	overflow := make([]uint64, len(beginWaitBuckets)+1)
	overflow[len(beginWaitBuckets)] = 100 // every sample slower than the last bound
	observedMax := 20 * time.Second
	if got := beginQuantile(overflow, 100, 0.50, observedMax); got != observedMax {
		t.Fatalf("p50 = %s, want the observed maximum %s", got, observedMax)
	}
	if got := beginQuantile(overflow, 100, 0.99, observedMax); got != observedMax {
		t.Fatalf("p99 = %s, want the observed maximum %s", got, observedMax)
	}
	// A stale/absent maximum must not understate: fall back to the last bound.
	if got := beginQuantile(overflow, 100, 0.99, 0); got != beginWaitBuckets[len(beginWaitBuckets)-1] {
		t.Fatalf("p99 = %s, want the last bound %s", got, beginWaitBuckets[len(beginWaitBuckets)-1])
	}
}

func TestCheckpointStatsAccumulate(t *testing.T) {
	_, _, busyBefore, logBefore, doneBefore := CheckpointStats()
	recordCheckpoint(2, 5, 3, nil)
	recordCheckpoint(0, 0, 0, sql.ErrConnDone)
	attempts, failures, busy, logFrames, done := CheckpointStats()
	if busy-busyBefore != 2 || logFrames-logBefore != 5 || done-doneBefore != 3 {
		t.Fatalf("frame deltas = (%d,%d,%d), want (2,5,3)", busy-busyBefore, logFrames-logBefore, done-doneBefore)
	}
	if attempts == 0 || failures == 0 {
		t.Fatalf("attempts=%d failures=%d, want both recorded", attempts, failures)
	}
}

func TestWalStatsReportsOpenDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "walstats.db")
	migrate := func(sqlDB *sql.DB) error {
		_, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS wal_probe (value TEXT NOT NULL)`)
		return err
	}
	if err := Write(context.Background(), path, migrate, func(_ context.Context, tx bun.Tx) error {
		_, err := tx.Exec(`INSERT INTO wal_probe (value) VALUES ('x')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := Close(path); err != nil {
			t.Errorf("close %s: %v", path, err)
		}
	}()

	canonical, err := CanonicalPath(path)
	if err != nil {
		t.Fatal(err)
	}
	var found *DatabaseWalStat
	for _, stat := range WalStats() {
		if stat.Path == filepath.Base(canonical) {
			copy := stat
			found = &copy
		}
	}
	if found == nil {
		t.Fatalf("WalStats did not report the open database %s", canonical)
	}
	if filepath.IsAbs(found.Path) {
		t.Fatalf("WalStats path %q must be a base name, not an absolute path", found.Path)
	}
	if found.WalBytes <= 0 {
		t.Fatalf("walBytes = %d, want the committed write to leave WAL frames", found.WalBytes)
	}
}

func TestPoolStatsReportsSingleConnectionModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "poolstats.db")
	migrate := func(sqlDB *sql.DB) error {
		_, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS pool_probe (value TEXT NOT NULL)`)
		return err
	}
	if err := Write(context.Background(), path, migrate, func(_ context.Context, tx bun.Tx) error {
		_, err := tx.Exec(`INSERT INTO pool_probe (value) VALUES ('x')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := Close(path); err != nil {
			t.Errorf("close %s: %v", path, err)
		}
	}()

	canonical, err := CanonicalPath(path)
	if err != nil {
		t.Fatal(err)
	}
	var found *DatabasePoolStat
	for _, stat := range PoolStats() {
		if stat.Path == filepath.Base(canonical) {
			copy := stat
			found = &copy
		}
	}
	if found == nil {
		t.Fatalf("PoolStats did not report the open database %s", canonical)
	}
	if filepath.IsAbs(found.Path) {
		t.Fatalf("PoolStats path %q must be a base name, not an absolute path", found.Path)
	}
	if found.MaxOpenConnections != 1 {
		t.Fatalf("maxOpenConnections = %d, want 1 (the single-writer connection model)", found.MaxOpenConnections)
	}
}
