package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/uptrace/bun"
)

// fakeSQLiteError implements the driver error surface the retry classifies,
// without provoking a real lock conflict.
type fakeSQLiteError struct{ code int }

func (e fakeSQLiteError) Error() string { return fmt.Sprintf("sqlite error %d", e.code) }
func (e fakeSQLiteError) Code() int     { return e.code }

var (
	fakeBusy   = fakeSQLiteError{code: 5}  // SQLITE_BUSY
	fakeLocked = fakeSQLiteError{code: 6}  // SQLITE_LOCKED
	fakeFailed = fakeSQLiteError{code: 19} // SQLITE_CONSTRAINT
)

// TestRetryBusyRetriesOnlyTransientFailures pins the retry policy: transient
// SQLITE_BUSY/SQLITE_LOCKED outcomes are retried, anything else fails
// immediately with the original error.
func TestRetryBusyRetriesOnlyTransientFailures(t *testing.T) {
	attempts := 0
	value, err := retryBusy(context.Background(), time.Second, func() (string, error) {
		attempts++
		if attempts < 3 {
			return "", fakeBusy
		}
		return "ok", nil
	})
	if err != nil || value != "ok" || attempts != 3 {
		t.Fatalf("retryBusy = (%q, %v) after %d attempts, want (ok, nil) after 3", value, err, attempts)
	}

	attempts = 0
	_, err = retryBusy(context.Background(), 350*time.Millisecond, func() (string, error) {
		attempts++
		return "", errors.Join(errors.New("write session entry"), fakeLocked)
	})
	if err == nil {
		t.Fatal("a locked begin must surface when the retry budget is exhausted")
	}
	if attempts < 2 {
		t.Fatalf("attempts = %d, want the locked begin to be retried", attempts)
	}

	attempts = 0
	permanent := errors.New("constraint failed")
	if _, err := retryBusy(context.Background(), time.Second, func() (string, error) {
		attempts++
		return "", permanent
	}); !errors.Is(err, permanent) {
		t.Fatalf("err = %v, want the original permanent error", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want a permanent error to fail immediately", attempts)
	}
}

// TestRetryBusyHonorsContextDeadline guards the caller escape hatch: a context
// deadline must win over the busy budget so ctx-aware callers never wait the
// full retry window.
func TestRetryBusyHonorsContextDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := retryBusy(ctx, time.Minute, func() (string, error) { return "", fakeBusy })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("retryBusy waited %s despite a canceled context", elapsed)
	}
}

// TestIsSQLiteBusyDetectsDriverCodes guards the classification itself.
func TestIsSQLiteBusyDetectsDriverCodes(t *testing.T) {
	if !isSQLiteBusy(fakeBusy) || !isSQLiteBusy(fakeLocked) {
		t.Fatal("SQLITE_BUSY/SQLITE_LOCKED must be transient")
	}
	if isSQLiteBusy(fakeFailed) {
		t.Fatal("SQLITE_CONSTRAINT must not be retried")
	}
	if isSQLiteBusy(errors.New("not a sqlite error")) {
		t.Fatal("non-driver errors must not be retried")
	}
	if !isSQLiteBusy(errors.Join(errors.New("begin transaction"), fakeBusy)) {
		t.Fatal("a wrapped busy error must be recognized")
	}
}

func busyTestMigrator(sqlDB *sql.DB) error {
	_, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS busy_test_values (value TEXT NOT NULL)`)
	return err
}

// TestRunInTxCommitsAndRollsBack keeps the hand-written RunInTx faithful to
// (*bun.DB).RunInTx: the callback runs in one transaction, an error rolls back,
// and a panic does not leave the transaction open.
func TestRunInTxCommitsAndRollsBack(t *testing.T) {
	connection, err := Open(filepath.Join(t.TempDir(), "busy.db"), busyTestMigrator)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if err := RunInTx(ctx, connection, nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO busy_test_values (value) VALUES (?)`, "committed")
		return err
	}); err != nil {
		t.Fatalf("commit path: %v", err)
	}

	rollbackErr := errors.New("callback failed")
	if err := RunInTx(ctx, connection, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO busy_test_values (value) VALUES (?)`, "rolled-back"); err != nil {
			return err
		}
		return rollbackErr
	}); !errors.Is(err, rollbackErr) {
		t.Fatalf("error path = %v, want the callback error", err)
	}

	func() {
		defer func() { _ = recover() }()
		_ = RunInTx(ctx, connection, nil, func(ctx context.Context, tx bun.Tx) error {
			if _, err := tx.ExecContext(ctx, `INSERT INTO busy_test_values (value) VALUES (?)`, "panicked"); err != nil {
				return err
			}
			panic("callback panicked")
		})
	}()

	rows, err := connection.QueryContext(ctx, `SELECT value FROM busy_test_values ORDER BY value`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	if len(values) != 1 || values[0] != "committed" {
		t.Fatalf("values = %v, want only the committed row", values)
	}
}
