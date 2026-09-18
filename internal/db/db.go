// Package db owns the process-wide SQLite connection lifecycle.
//
// Database access outside schema migrations should go through this package and
// a DAO. Only this package may open, configure, cache, or close the underlying
// SQLite connection; table operations belong to internal/dao.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"

	// This package registers the SQLite driver for the whole process: every
	// managed and standalone connection it opens uses the "sqlite" DSN, and no
	// other production package imports the driver. Keep the blank import even when
	// no symbol from it is referenced.
	_ "modernc.org/sqlite"
)

// Migrator initializes or validates a database. It is intentionally kept
// compatible with the existing session migration implementation; migrations
// are the one place where schema SQL is required.
type Migrator func(*sql.DB) error

// Options configures per-database SQLite connection behavior.
//
// ForeignKeys enables SQLite foreign key enforcement for one database file.
// The canonical session database must keep it disabled: project policy
// enforces referential integrity in the repository layer (transactional
// writes, centralized deletion cleanup lists, and integrity tests such as
// TestDeleteSessionRemovesEveryChildRow), not in the database engine, so that
// cross-version recovery and partially corrupted canonical stores are never
// amplified by hard constraints.
//
// A private, rebuildable derived store may opt in. The per-knowledge-base
// graph/FTS database (see docs/proposal/desktop-knowledge-base-agent-proposal.md)
// designs its snapshot lifecycle around ON DELETE CASCADE pruning and can
// always be rebuilt from its source directory, so it enables enforcement
// without exposing canonical session data to it. Options must be
// consistent for a given file path because connections are cached per path.
type Options struct {
	ForeignKeys bool
}

var state = struct {
	sync.Mutex
	dbs map[string]*bun.DB
}{dbs: make(map[string]*bun.DB)}

// CanonicalPath returns the absolute, cleaned database path used as the cache
// key. Keeping this in one package prevents duplicate connections to the same
// SQLite file through differently spelled paths.
func CanonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("resolve database path: %w", err)
	}
	return abs, nil
}

// Open returns the process-wide Bun connection for path with foreign key
// enforcement disabled (the canonical session database policy). Callers must
// not close it; CloseAll owns the lifecycle. Derived stores that need cascade
// behavior use OpenWithOptions.
func Open(path string, migrate Migrator) (*bun.DB, error) {
	return OpenWithOptions(path, migrate, Options{})
}

// OpenWithOptions returns the process-wide Bun connection for path, applying
// per-database Options. The connection is cached by canonical path, so the
// options for a given file must be stable across callers.
func OpenWithOptions(path string, migrate Migrator, opts Options) (*bun.DB, error) {
	canonical, err := CanonicalPath(path)
	if err != nil {
		return nil, err
	}
	state.Lock()
	defer state.Unlock()
	if existing := state.dbs[canonical]; existing != nil {
		return existing, nil
	}
	db, err := open(canonical, migrate, opts)
	if err != nil {
		return nil, err
	}
	state.dbs[canonical] = db
	return db, nil
}

// OpenStandalone opens an uncached connection for callers that explicitly own
// its lifecycle, such as offline integrity checks. Foreign key enforcement
// stays disabled to match the canonical session database policy.
func OpenStandalone(path string, migrate Migrator) (*bun.DB, error) {
	canonical, err := CanonicalPath(path)
	if err != nil {
		return nil, err
	}
	return open(canonical, migrate, Options{})
}

// Query runs a read operation through the process-wide connection.
func Query(path string, migrate Migrator, fn func(*bun.DB) error) error {
	connection, err := Open(path, migrate)
	if err != nil {
		return err
	}
	return fn(connection)
}

// Write runs a write operation in one Bun transaction.
func Write(ctx context.Context, path string, migrate Migrator, fn func(context.Context, bun.Tx) error) error {
	connection, err := Open(path, migrate)
	if err != nil {
		return err
	}
	return RunInTx(ctx, connection, nil, fn)
}

// CloseAll checkpoints and closes all process-owned connections.
func CloseAll() error {
	state.Lock()
	defer state.Unlock()
	var errs []error
	for path, connection := range state.dbs {
		var busy, logFrames, checkpointed int
		if err := connection.QueryRow("PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &logFrames, &checkpointed); err != nil {
			errs = append(errs, fmt.Errorf("checkpoint %s: %w", path, err))
		}
		if err := connection.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close %s: %w", path, err))
		}
		delete(state.dbs, path)
	}
	return errors.Join(errs...)
}

// Close releases one process-owned SQLite connection. It is used by resource
// stores that own a whole database file and need to remove that exact file
// after their data has been deleted. General callers should normally keep
// using CloseAll at process shutdown.
func Close(path string) error {
	canonical, err := CanonicalPath(path)
	if err != nil {
		return err
	}
	state.Lock()
	connection := state.dbs[canonical]
	if connection != nil {
		delete(state.dbs, canonical)
	}
	state.Unlock()
	if connection == nil {
		return nil
	}
	var errs []error
	var busy, logFrames, checkpointed int
	if err := connection.QueryRow("PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &logFrames, &checkpointed); err != nil {
		errs = append(errs, fmt.Errorf("checkpoint %s: %w", canonical, err))
	}
	if err := connection.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close %s: %w", canonical, err))
	}
	return errors.Join(errs...)
}

// open opens one database file and recovers from a schema migration failure
// that cannot be repaired in place (see SchemaIncompatible): the unrecoverable
// database is snapshotted next to the original and a fresh database is
// initialized in its place, so a stale or damaged schema never blocks startup.
//
// Every other failure is returned unchanged, including a migration error that
// was not classified as incompatible and a classified failure that a rebuild
// must not "fix" (writer contention, a cancelled migration, a read-only file).
// Those are reported with the reason appended so the distinction stays visible.
func open(path string, migrate Migrator, opts Options) (*bun.DB, error) {
	connection, err := openOnce(path, migrate, opts)
	if err == nil {
		return connection, nil
	}
	var failed *migrationFailedError
	if !errors.As(err, &failed) {
		return nil, err
	}
	if !IsSchemaIncompatible(failed.err) {
		// A migration failure nobody classified: report it and release the
		// connection that openOnce intentionally left open.
		_ = failed.sqlDB.Close()
		return nil, err
	}
	if reason := unrebuildableReason(failed.err); reason != "" {
		_ = failed.sqlDB.Close()
		return nil, fmt.Errorf("%w (%s; the database was left untouched)", err, reason)
	}
	recovery, recoveryErr := recoverFromMigrationFailure(failed.sqlDB, path, failed.err)
	if recoveryErr != nil {
		return nil, errors.Join(err, fmt.Errorf("rebuild database after migration failure: %w", recoveryErr))
	}
	rebuilt, rebuildErr := openOnce(path, migrate, opts)
	if rebuildErr != nil {
		var retried *migrationFailedError
		if errors.As(rebuildErr, &retried) {
			_ = retried.sqlDB.Close()
		}
		return nil, errors.Join(err, fmt.Errorf("open database rebuilt from %s: %w", recovery.BackupPath, rebuildErr))
	}
	recordMigrationRecovery(recovery)
	return rebuilt, nil
}

// unrebuildableReason reports why a migration failure must not trigger a
// schema rebuild, or "" when a rebuild is allowed. Rebuilding deletes the
// database file, so every failure that a healthy database can also produce under
// external pressure has to be excluded explicitly: writer contention from
// another process, a cancelled migration, and a read-only file or directory.
func unrebuildableReason(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return "the migration was cancelled"
	case isSQLiteBusy(err):
		return "another process holds the SQLite writer lock"
	case isSQLiteReadOnly(err):
		return "the database file is read-only"
	default:
		return ""
	}
}

// openOnce opens, checks, and initializes one database file without any
// recovery. A migration failure is returned as a migrationFailedError so the
// caller can snapshot the database through the connection that produced it, so
// that connection stays open on that one error path and is closed by the caller.
func openOnce(path string, migrate Migrator, opts Options) (*bun.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	sqlDB, err := sql.Open("sqlite", dsn(path, opts.ForeignKeys))
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("initialize sqlite connection: %w", err)
	}
	if err := checkIntegrity(sqlDB, path); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	if err := enableWAL(sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	if migrate != nil {
		if err := migrate(sqlDB); err != nil {
			return nil, &migrationFailedError{sqlDB: sqlDB, err: fmt.Errorf("apply database migration: %w", err)}
		}
	}
	return bun.NewDB(sqlDB, sqlitedialect.New()), nil
}

// indexRepairBudget bounds how long opening a database keeps retrying a lost
// index repair. It is the same per-statement stall budget every managed
// connection already carries, and it is a variable only so the contention path
// can be tested without a full-budget stall.
var indexRepairBudget = BusyTimeout

// checkIntegrity validates the database before it is used. SQLite can report a
// stale secondary-index entry after an interrupted write even when every table
// page remains intact. Rebuilding indexes is lossless because their contents
// are derived from table rows, so repair that precise case and verify it before
// continuing. Other integrity failures may affect canonical data and must stay
// visible to the caller rather than being treated as recoverable.
//
// A repair that cannot take the writer lock is a contention failure, not damage:
// another MothX process is alive against this file, and the error has to say so
// instead of reporting a failed repair of a corrupted database.
func checkIntegrity(sqlDB *sql.DB, path string) error {
	integrity, err := quickCheck(sqlDB)
	if err != nil {
		return fmt.Errorf("run sqlite integrity check: %w", err)
	}
	if integrity == "ok" {
		return nil
	}
	if !isStaleIndexReport(integrity) {
		return fmt.Errorf("sqlite integrity check failed: %s", integrity)
	}
	cause := integrity

	if err := attemptWhileBusy(indexRepairBudget, func() error {
		_, err := sqlDB.Exec("REINDEX")
		return err
	}); err != nil {
		switch {
		case isSQLiteBusy(err):
			return fmt.Errorf("sqlite reported a stale index (%s) but the writer lock was unavailable for %s, so it was not repaired: another process still holds the SQLite writer lock on %s; stop it and retry: %w", cause, indexRepairBudget, path, err)
		case isSQLiteReadOnly(err):
			return fmt.Errorf("sqlite reported a stale index (%s) but %s is read-only, so it could not be repaired: %w", cause, path, err)
		default:
			return fmt.Errorf("repair SQLite indexes after integrity check %q: %w", cause, err)
		}
	}
	integrity, err = quickCheck(sqlDB)
	if err != nil {
		return fmt.Errorf("run sqlite integrity check after index repair: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("sqlite integrity check failed after index repair: %s", integrity)
	}
	recordIndexRepair(IndexRepair{Path: path, Cause: cause, At: time.Now()})
	return nil
}

// isStaleIndexReport reports whether a quick_check line describes only a
// secondary index disagreeing with the table rows it derives from - the one
// integrity outcome that REINDEX fixes without touching data.
func isStaleIndexReport(integrity string) bool {
	return strings.Contains(strings.ToLower(integrity), "wrong # of entries in index")
}

func quickCheck(sqlDB *sql.DB) (string, error) {
	var integrity string
	if err := sqlDB.QueryRow("PRAGMA quick_check").Scan(&integrity); err != nil {
		return "", err
	}
	return integrity, nil
}

func dsn(path string, foreignKeys bool) string {
	return dsnForOS(path, runtime.GOOS == "windows", foreignKeys)
}

// DSNForOS returns the configured SQLite file URI with foreign key enforcement
// disabled, which is the canonical session database policy. It is exported for
// platform-specific integration tests.
func DSNForOS(path string, windows bool) string {
	return dsnForOS(path, windows, false)
}

// synchronousMode returns the SQLite synchronous pragma value for new
// connections.
//
// WAL + NORMAL is the default: a commit no longer fsyncs while holding the
// single writer lock (the fsync moves to checkpoint time), so the
// cross-process writer queue shrinks from fsync-scale to page-cache-scale.
// Durability semantics: a process crash loses nothing (committed frames
// survive in the WAL); an OS crash or power loss can roll back commits made
// since the last checkpoint without corrupting the database. That window is
// already covered by the session recovery model (missing run terminal state
// converges through lease expiry, orphaned projection, and bounded
// terminalization; see
// docs/proposal/cross-process-session-execution-ownership-proposal.md and
// docs/proposal/sqlite-write-pressure-reduction-proposal.md).
//
// MOTHX_SQLITE_SYNCHRONOUS=FULL restores the legacy per-commit fsync for
// deployments that require it. The variable is read per connection open, so
// processes running different builds or settings (for example a desktop
// vendored runtime next to a newer CLI) safely share one database file; each
// connection's setting only affects its own commit durability.
func synchronousMode() string {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("MOTHX_SQLITE_SYNCHRONOUS")), "FULL") {
		return "FULL"
	}
	return "NORMAL"
}

// BusyTimeout is the SQLite busy_timeout applied to every managed connection: a
// writer waits this long for the single writer lock before returning
// SQLITE_BUSY. It is the per-statement stall budget that callers which must
// tolerate transient contention (for example the session runtime heartbeat)
// need to exceed to absorb one contended begin within a single attempt.
const BusyTimeout = 10 * time.Second

func dsnForOS(path string, windows bool, foreignKeys bool) string {
	uriPath := filepath.ToSlash(path)
	if windows && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := u.Query()
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", BusyTimeout.Milliseconds()))
	// Foreign key enforcement is opt-in per database. The canonical session
	// database keeps it OFF so referential integrity stays a repository-layer
	// concern (transactional writes, centralized deletion cleanup, integrity
	// tests) and dormant REFERENCES clauses in schema.go/migrations.go are
	// never activated as hard constraints. Only private, rebuildable derived
	// stores such as the per-knowledge-base graph/FTS database opt in, because
	// their snapshot lifecycle is designed around ON DELETE CASCADE pruning.
	// An architecture guard rejects re-enabling this for the session database.
	if foreignKeys {
		q.Add("_pragma", "foreign_keys(1)")
	}
	q.Add("_pragma", "synchronous("+synchronousMode()+")")
	// _txlock=immediate makes every non-read-only transaction take the writer
	// lock up front, which avoids a deferred read-to-write upgrade failing with
	// SQLITE_BUSY. It also means a begin waits for the writer, so transient
	// contention is retried at the transaction boundaries (see busy.go) instead of
	// failing once busy_timeout elapses.
	q.Set("_txlock", "immediate")
	q.Set("_dqs", "false")
	u.RawQuery = q.Encode()
	return u.String()
}

// sqliteRetryDelay is the pause between retries of a startup statement that hit
// writer contention.
const sqliteRetryDelay = 25 * time.Millisecond

// attemptWhileBusy runs one SQLite statement, retrying only while the driver
// reports writer contention (SQLITE_BUSY/SQLITE_LOCKED) and the budget lasts.
// Both statements that open a database has to survive a peer holding the single
// writer: enabling WAL and rebuilding a stale index. Any other error returns
// immediately, so a genuinely damaged or unwritable file is never retried into
// a stall, and exhausting the budget returns the last transient error so the
// caller keeps the driver's diagnostic.
func attemptWhileBusy(budget time.Duration, statement func() error) error {
	deadline := time.Now().Add(budget)
	for {
		err := statement()
		if err == nil || !isSQLiteBusy(err) || !time.Now().Add(sqliteRetryDelay).Before(deadline) {
			return err
		}
		time.Sleep(sqliteRetryDelay)
	}
}

func enableWAL(sqlDB *sql.DB) error {
	var mode string
	if err := attemptWhileBusy(BusyTimeout, func() error {
		return sqlDB.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode)
	}); err != nil {
		return fmt.Errorf("enable sqlite WAL mode: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("sqlite journal mode is %q, want WAL", mode)
	}
	return nil
}
