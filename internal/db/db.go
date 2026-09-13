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
	"modernc.org/sqlite"
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

func open(path string, migrate Migrator, opts Options) (*bun.DB, error) {
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
	var integrity string
	if err := sqlDB.QueryRow("PRAGMA quick_check").Scan(&integrity); err != nil || integrity != "ok" {
		_ = sqlDB.Close()
		if err != nil {
			return nil, fmt.Errorf("run sqlite integrity check: %w", err)
		}
		return nil, fmt.Errorf("sqlite integrity check failed: %s", integrity)
	}
	if err := enableWAL(sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	if migrate != nil {
		if err := migrate(sqlDB); err != nil {
			_ = sqlDB.Close()
			return nil, fmt.Errorf("apply database migration: %w", err)
		}
	}
	return bun.NewDB(sqlDB, sqlitedialect.New()), nil
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

func dsnForOS(path string, windows bool, foreignKeys bool) string {
	uriPath := filepath.ToSlash(path)
	if windows && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(10000)")
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
	q.Add("_pragma", "synchronous(FULL)")
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

func enableWAL(sqlDB *sql.DB) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		var mode string
		err := sqlDB.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode)
		if err == nil {
			if strings.EqualFold(mode, "wal") {
				return nil
			}
			return fmt.Errorf("sqlite journal mode is %q, want WAL", mode)
		}
		var sqliteErr *sqlite.Error
		if !errors.As(err, &sqliteErr) || (sqliteErr.Code()&0xff != 5 && sqliteErr.Code()&0xff != 6) || time.Now().After(deadline) {
			return fmt.Errorf("enable sqlite WAL mode: %w", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
