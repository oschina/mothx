package db

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"time"

	"github.com/uptrace/bun"
)

// SQLite writer contention: the session DSN sets _txlock=immediate (see
// dsnForOS), so every non-read-only transaction begins with BEGIN IMMEDIATE and
// takes the single writer lock up front. That is what keeps a deferred
// read-to-write upgrade from failing with SQLITE_BUSY, but it also means a
// begin waits for the lock. The connection waits up to busy_timeout (10s); when
// that budget elapses while other processes keep the writer busy (several mothx
// processes sharing one session directory, each commit fsyncing under
// synchronous(FULL)), the begin returns SQLITE_BUSY even though nothing is
// wrong with the database.
//
// The bounded retry below absorbs that transient outcome instead of turning it
// into a hard failure of the whole operation. It never hides a permanent error:
// only SQLITE_BUSY/SQLITE_LOCKED are retried, and a context deadline still wins.
const (
	// busyRetryBudget bounds the total time one transaction begin may spend
	// waiting for the writer across retries. Writers queue behind each other's
	// commits, so the budget must cover a plausible queue instead of a single
	// busy_timeout; callers that need a shorter bound pass a context with a
	// deadline.
	busyRetryBudget = 90 * time.Second
	// busyRetryDelay is the first backoff between attempts.
	busyRetryDelay = 200 * time.Millisecond
	// busyRetryMaxDelay caps the exponential backoff.
	busyRetryMaxDelay = 2 * time.Second
)

// isSQLiteBusy reports whether err is SQLITE_BUSY (5) or SQLITE_LOCKED (6):
// another writer holds the lock right now, which is transient rather than a
// permanent failure. modernc.org/sqlite's error type exposes the result code
// through Code(); naming only that method keeps this check independent of the
// driver package and lets the retry policy be tested without provoking a real
// lock conflict.
func isSQLiteBusy(err error) bool {
	var coded interface {
		error
		Code() int
	}
	if !errors.As(err, &coded) {
		return false
	}
	switch coded.Code() & 0xff {
	case 5, 6:
		return true
	default:
		return false
	}
}

// busyRetryCounters accumulates transient SQLITE_BUSY/SQLITE_LOCKED begin
// retries process-wide so cross-process writer contention stays observable:
// a healthy workload keeps hits near zero even with several processes sharing
// one session directory, while sustained growth points at a writer queue
// worth investigating (see
// docs/proposal/sqlite-write-pressure-reduction-proposal.md).
var busyRetryCounters = struct {
	hits       atomic.Uint64
	waitNanods atomic.Uint64
}{}

// beginWaitCounters tracks every transaction begin attempt (successful or
// retried): how often the process begins write/read transactions, how much
// wall time the begin calls take in total, and the single slowest begin. A
// growing max/total under multi-process load is the direct signal of writer
// queueing on the shared session database.
var beginWaitCounters = struct {
	count   atomic.Uint64
	totalNs atomic.Uint64
	maxNs   atomic.Uint64
}{}

// BusyRetryStats returns the cumulative begin-retry hit count and the total
// backoff time slept between attempts since process start. The wait total
// counts the scheduled backoff delays only; time blocked inside a begin call
// (busy_timeout) belongs to the driver and is reported by BeginWaitStats.
func BusyRetryStats() (hits uint64, totalWait time.Duration) {
	return busyRetryCounters.hits.Load(), time.Duration(busyRetryCounters.waitNanods.Load())
}

// BeginWaitStats returns the cumulative transaction begin attempt count, the
// total wall time spent inside begin calls, and the slowest single begin
// since process start.
func BeginWaitStats() (count uint64, total, max time.Duration) {
	return beginWaitCounters.count.Load(),
		time.Duration(beginWaitCounters.totalNs.Load()),
		time.Duration(beginWaitCounters.maxNs.Load())
}

func recordBeginWait(elapsed time.Duration) {
	beginWaitCounters.count.Add(1)
	beginWaitCounters.totalNs.Add(uint64(elapsed))
	for {
		previous := beginWaitCounters.maxNs.Load()
		if uint64(elapsed) <= previous || beginWaitCounters.maxNs.CompareAndSwap(previous, uint64(elapsed)) {
			return
		}
	}
}

// BeginTx begins a Bun transaction on a managed (or caller-owned) connection,
// retrying a transient SQLITE_BUSY/SQLITE_LOCKED begin within the shared budget.
func BeginTx(ctx context.Context, connection *bun.DB, opts *sql.TxOptions) (bun.Tx, error) {
	if connection == nil {
		return bun.Tx{}, errors.New("sqlite connection is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return retryBusy(ctx, busyRetryBudget, func() (bun.Tx, error) {
		return connection.BeginTx(ctx, opts)
	})
}

// BeginSQLTx begins a transaction on a raw *sql.DB handle with the same begin
// retry. Raw-SQL owners (schema initialization and migrations) use it so
// concurrent startup on one session directory cannot fail on a transient
// writer conflict.
func BeginSQLTx(ctx context.Context, connection *sql.DB, opts *sql.TxOptions) (*sql.Tx, error) {
	if connection == nil {
		return nil, errors.New("sqlite connection is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return retryBusy(ctx, busyRetryBudget, func() (*sql.Tx, error) {
		return connection.BeginTx(ctx, opts)
	})
}

// RunInTx mirrors (*bun.DB).RunInTx with the shared begin retry: the callback
// runs in one transaction that is committed on success and rolled back
// otherwise (including a panic).
func RunInTx(ctx context.Context, connection *bun.DB, opts *sql.TxOptions, fn func(context.Context, bun.Tx) error) error {
	tx, err := BeginTx(ctx, connection, opts)
	if err != nil {
		return err
	}
	var done bool
	defer func() {
		if !done {
			_ = tx.Rollback()
		}
	}()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	done = true
	return tx.Commit()
}

// retryBusy runs begin until it succeeds, fails with a non-transient error, or
// exhausts the busy budget. The last transient error is returned so callers
// keep the driver's diagnostic.
func retryBusy[T any](ctx context.Context, budget time.Duration, begin func() (T, error)) (T, error) {
	var zero T
	deadline := time.Now().Add(budget)
	delay := busyRetryDelay
	var lastErr error
	for {
		attemptStart := time.Now()
		value, err := begin()
		recordBeginWait(time.Since(attemptStart))
		if err == nil {
			return value, nil
		}
		if !isSQLiteBusy(err) {
			return zero, err
		}
		lastErr = err
		busyRetryCounters.hits.Add(1)
		if !time.Now().Add(delay).Before(deadline) {
			return zero, lastErr
		}
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(delay):
		}
		busyRetryCounters.waitNanods.Add(uint64(delay))
		if delay < busyRetryMaxDelay {
			delay *= 2
			if delay > busyRetryMaxDelay {
				delay = busyRetryMaxDelay
			}
		}
	}
}
