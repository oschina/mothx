package db

import (
	"context"
	"database/sql"
	"errors"
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
		value, err := begin()
		if err == nil {
			return value, nil
		}
		if !isSQLiteBusy(err) {
			return zero, err
		}
		lastErr = err
		if !time.Now().Add(delay).Before(deadline) {
			return zero, lastErr
		}
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(delay):
		}
		if delay < busyRetryMaxDelay {
			delay *= 2
			if delay > busyRetryMaxDelay {
				delay = busyRetryMaxDelay
			}
		}
	}
}
