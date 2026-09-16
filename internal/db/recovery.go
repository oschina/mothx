package db

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// MigrationRecovery records a schema migration failure that internal/db
// recovered from by snapshotting the unrecoverable database and starting a new
// empty one in its place.
//
// The previous data is not deleted: it stays in BackupPath, which callers should
// surface to the user (front-ends drain the records with
// TakeMigrationRecoveries so the notice is shown exactly once).
type MigrationRecovery struct {
	// Path is the canonical database path that was replaced.
	Path string
	// BackupPath is the snapshot of the pre-recovery database. It is empty only
	// when the database file did not exist and nothing needed to be preserved,
	// or when the recovery was performed by another process.
	BackupPath string
	// Err is the migration failure that triggered the recovery. It is nil for a
	// recovery another process announced over the advisory inter-process bus.
	Err error
	// Peer reports that another process performed the recovery, so the database
	// file was replaced while this process may still hold it open.
	Peer bool
	// At is when the recovery happened.
	At time.Time
}

// Describe returns the one-line operator-facing summary of a recovery. Callers
// that want a richer message use the fields directly.
func (r MigrationRecovery) Describe() string {
	if r.Peer {
		return fmt.Sprintf("another MothX process rebuilt the database at %s after a failed migration", r.Path)
	}
	if r.BackupPath == "" {
		return fmt.Sprintf("database migration failed for %s (%v); started a new empty database", r.Path, r.Err)
	}
	return fmt.Sprintf("database migration failed for %s (%v); backed up the previous database to %s and started a new empty database", r.Path, r.Err, r.BackupPath)
}

// schemaIncompatibleError marks a migration failure that cannot be repaired in
// place: the database's schema is not a version this build knows how to
// upgrade, so the only way forward is a backed-up rebuild.
type schemaIncompatibleError struct{ err error }

func (e *schemaIncompatibleError) Error() string { return e.err.Error() }

func (e *schemaIncompatibleError) Unwrap() error { return e.err }

// SchemaIncompatible marks a migration failure caused by a schema this build
// cannot upgrade in place. internal/db reacts by backing up and rebuilding the
// database; any other migration error is reported to the caller unchanged.
//
// Migration owners (internal/session) call this for the failures that a rebuild
// genuinely fixes - an unappliable migration or an incompatible legacy schema -
// and must not use it for transient or resource failures such as writer
// contention, cancellation, or a read-only file. internal/db additionally
// refuses to rebuild for those cases, because rebuilding deletes the database
// file and must never be triggered by a healthy database under pressure.
func SchemaIncompatible(err error) error {
	if err == nil {
		return nil
	}
	return &schemaIncompatibleError{err: err}
}

// IsSchemaIncompatible reports whether err was marked by SchemaIncompatible.
func IsSchemaIncompatible(err error) bool {
	var incompatible *schemaIncompatibleError
	return errors.As(err, &incompatible)
}

// migrationFailedError carries a failed schema migration together with the
// connection that must be snapshotted before the database file is replaced.
type migrationFailedError struct {
	sqlDB *sql.DB
	err   error
}

func (e *migrationFailedError) Error() string { return e.err.Error() }

func (e *migrationFailedError) Unwrap() error { return e.err }

var recoveryLog = struct {
	sync.Mutex
	entries []MigrationRecovery
}{}

// migrationRecoveryNotifier publishes a completed recovery to other processes.
// internal/db owns the recovery, but the inter-process bus lives in the schema
// owner (internal/session), so the hook keeps the dependency one-way.
var migrationRecoveryNotifier = struct {
	sync.Mutex
	fn func(MigrationRecovery)
}{}

// SetMigrationRecoveryNotifier registers a hook invoked for every completed
// recovery, after the recovery has been recorded and logged. Passing nil clears
// it. It is called once per recovery and must not block: the bus publisher is
// best-effort and only announces an advisory wake-up.
func SetMigrationRecoveryNotifier(fn func(MigrationRecovery)) {
	migrationRecoveryNotifier.Lock()
	migrationRecoveryNotifier.fn = fn
	migrationRecoveryNotifier.Unlock()
}

func recordMigrationRecovery(recovery MigrationRecovery) {
	recoveryLog.Lock()
	recoveryLog.entries = append(recoveryLog.entries, recovery)
	recoveryLog.Unlock()
	// The log line is the only notice headless entry points (serve, ACP,
	// channels) are guaranteed to reach, so the recovery is never silent.
	log.Printf("[db] %s", recovery.Describe())
	migrationRecoveryNotifier.Lock()
	notify := migrationRecoveryNotifier.fn
	migrationRecoveryNotifier.Unlock()
	if notify != nil {
		notify(recovery)
	}
}

// MigrationRecoveries returns the recoveries recorded in this process without
// clearing them.
func MigrationRecoveries() []MigrationRecovery {
	recoveryLog.Lock()
	defer recoveryLog.Unlock()
	return append([]MigrationRecovery(nil), recoveryLog.entries...)
}

// TakeMigrationRecoveries returns the recoveries recorded since the last call
// and clears them, so a front-end can tell the user exactly once.
func TakeMigrationRecoveries() []MigrationRecovery {
	recoveryLog.Lock()
	defer recoveryLog.Unlock()
	entries := recoveryLog.entries
	recoveryLog.entries = nil
	return entries
}

// recoverFromMigrationFailure snapshots the unrecoverable database, closes its
// connection, and removes the file set so the next open starts from empty.
// The failed connection is always closed, including on the error paths.
func recoverFromMigrationFailure(sqlDB *sql.DB, path string, cause error) (MigrationRecovery, error) {
	recovery, backupErr := snapshotUnrebuildableDatabase(sqlDB, path, cause)
	closeErr := sqlDB.Close()
	if backupErr != nil {
		return MigrationRecovery{}, errors.Join(backupErr, closeErr)
	}
	if closeErr != nil {
		return MigrationRecovery{}, fmt.Errorf("close failed database: %w", closeErr)
	}
	if err := removeDatabaseFiles(path); err != nil {
		return MigrationRecovery{}, err
	}
	return recovery, nil
}

// snapshotUnrebuildableDatabase writes a self-contained backup of a database
// whose schema cannot be migrated. The original file stays untouched here; only
// the backup is new, so a failed backup never costs data.
func snapshotUnrebuildableDatabase(sqlDB *sql.DB, path string, cause error) (MigrationRecovery, error) {
	if _, err := os.Stat(path); err != nil {
		return MigrationRecovery{}, fmt.Errorf("inspect database before rebuild: %w", err)
	}
	backupPath, err := backupPathFor(path)
	if err != nil {
		return MigrationRecovery{}, err
	}
	if err := snapshotDatabase(sqlDB, path, backupPath); err != nil {
		return MigrationRecovery{}, fmt.Errorf("back up database before rebuild: %w", err)
	}
	return MigrationRecovery{Path: path, BackupPath: backupPath, Err: cause, At: time.Now()}, nil
}

// backupPathFor returns an unused backup file name next to the database so the
// snapshot lands in the same directory as the data it preserves.
func backupPathFor(path string) (string, error) {
	stamp := time.Now().UTC().Format("20060102T150405Z")
	for attempt := 0; ; attempt++ {
		candidate := path + ".migration-failed-" + stamp + ".bak"
		if attempt > 0 {
			candidate = fmt.Sprintf("%s.migration-failed-%s-%d.bak", path, stamp, attempt)
		}
		_, err := os.Stat(candidate)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return candidate, nil
		case err != nil:
			return "", fmt.Errorf("check backup path %s: %w", candidate, err)
		case attempt >= 1000:
			return "", fmt.Errorf("no free backup path for %s", path)
		}
	}
}

// snapshotDatabase writes a consistent copy of the database to backupPath. It
// prefers SQLite's own VACUUM INTO, which folds committed WAL frames into one
// self-contained file, and falls back to a checkpoint plus raw file copies when
// the damage that broke the migration also breaks VACUUM. The raw copy keeps the
// -wal/-shm/-journal sidecars next to the backup so SQLite can still recover the
// data they hold.
func snapshotDatabase(connection *sql.DB, path, backupPath string) error {
	vacuumErr := vacuumInto(connection, backupPath)
	if vacuumErr == nil {
		return nil
	}
	// Best effort: fold the WAL into the main file so the plain copy carries the
	// committed frames even if the sidecars cannot be copied.
	_, _ = connection.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	errs := []error{fmt.Errorf("vacuum database: %w", vacuumErr)}
	if err := copyFile(path, backupPath); err != nil {
		errs = append(errs, err)
	}
	for _, suffix := range databaseFileSuffixes() {
		if err := copyFileIfExists(path+suffix, backupPath+suffix); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func vacuumInto(connection *sql.DB, backupPath string) error {
	// VACUUM INTO takes a literal path, so single quotes are doubled.
	escaped := strings.ReplaceAll(backupPath, "'", "''")
	if _, err := connection.Exec("VACUUM INTO '" + escaped + "'"); err != nil {
		return err
	}
	return nil
}

// removeDatabaseFiles deletes one database file together with its sidecars so a
// recreated database cannot inherit a stale WAL or shared-memory index.
func removeDatabaseFiles(path string) error {
	targets := []string{path}
	for _, suffix := range databaseFileSuffixes() {
		targets = append(targets, path+suffix)
	}
	var errs []error
	for _, target := range targets {
		if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove %s: %w", target, err))
		}
	}
	return errors.Join(errs...)
}

func databaseFileSuffixes() []string {
	return []string{"-wal", "-shm", "-journal"}
}

func copyFileIfExists(source, destination string) error {
	if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect %s: %w", source, err)
	}
	return copyFile(source, destination)
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open %s: %w", source, err)
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("create %s: %w", destination, err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return fmt.Errorf("copy %s to %s: %w", source, destination, err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close %s: %w", destination, err)
	}
	return nil
}

// isSQLiteReadOnly reports whether err is SQLITE_READONLY (8): the file or its
// directory forbids writes, which a rebuild would "fix" only by destroying data
// the user did not intend to replace.
func isSQLiteReadOnly(err error) bool {
	var coded interface {
		error
		Code() int
	}
	if !errors.As(err, &coded) {
		return false
	}
	return coded.Code()&0xff == 8
}
