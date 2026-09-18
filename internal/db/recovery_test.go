package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// codedError satisfies the driver-independent code interface isSQLiteBusy and
// isSQLiteReadOnly rely on, so the transient-failure guardrails can be tested
// without provoking a real lock or permission conflict.
type codedError struct {
	code int
	msg  string
}

func (e codedError) Error() string { return e.msg }

func (e codedError) Code() int { return e.code }

func migrationBackups(path string) []string {
	matches, _ := filepath.Glob(path + ".migration-failed-*")
	return matches
}

func seededDatabase(t *testing.T, path string) {
	t.Helper()
	seed := func(sqlDB *sql.DB) error {
		if _, err := sqlDB.Exec(`CREATE TABLE legacy_items (id TEXT PRIMARY KEY)`); err != nil {
			return err
		}
		_, err := sqlDB.Exec(`INSERT INTO legacy_items (id) VALUES ('kept')`)
		return err
	}
	if _, err := Open(path, seed); err != nil {
		t.Fatal(err)
	}
	if err := CloseAll(); err != nil {
		t.Fatal(err)
	}
}

func requireLegacyRow(t *testing.T, path string) {
	t.Helper()
	connection, err := sql.Open("sqlite", DSNForOS(path, false))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	var id string
	if err := connection.QueryRow(`SELECT id FROM legacy_items`).Scan(&id); err != nil {
		t.Fatalf("read preserved row from %s: %v", path, err)
	}
	if id != "kept" {
		t.Fatalf("preserved row id = %q, want kept", id)
	}
}

// TestMigrationFailureBacksUpAndRebuildsDatabase covers the recovery contract: a
// schema the current build cannot upgrade is snapshotted next to the original
// database, a fresh empty database is built in its place, and the recovery is
// reported once so a front-end can tell the user where the previous data went.
func TestMigrationFailureBacksUpAndRebuildsDatabase(t *testing.T) {
	TakeMigrationRecoveries()
	path := filepath.Join(t.TempDir(), "sessions.db")
	seededDatabase(t, path)

	current := func(sqlDB *sql.DB) error {
		var legacy int
		if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'legacy_items'`).Scan(&legacy); err != nil {
			return err
		}
		if legacy != 0 {
			return SchemaIncompatible(errors.New("legacy schema cannot be upgraded"))
		}
		if _, err := sqlDB.Exec(`CREATE TABLE current_items (id TEXT PRIMARY KEY)`); err != nil {
			return err
		}
		_, err := sqlDB.Exec(`INSERT INTO current_items (id) VALUES ('fresh')`)
		return err
	}

	rebuilt, err := Open(path, current)
	if err != nil {
		t.Fatalf("Open after a recoverable migration failure: %v", err)
	}
	defer func() {
		if err := CloseAll(); err != nil {
			t.Fatal(err)
		}
	}()

	var legacyTables int
	if err := rebuilt.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'legacy_items'`).Scan(&legacyTables); err != nil {
		t.Fatal(err)
	}
	if legacyTables != 0 {
		t.Fatal("rebuilt database still contains the unrecoverable legacy schema")
	}
	var fresh string
	if err := rebuilt.QueryRow(`SELECT id FROM current_items`).Scan(&fresh); err != nil {
		t.Fatalf("rebuilt database is not usable: %v", err)
	}
	if fresh != "fresh" {
		t.Fatalf("rebuilt database row = %q, want fresh", fresh)
	}

	recoveries := MigrationRecoveries()
	if len(recoveries) != 1 {
		t.Fatalf("recoveries = %#v, want exactly one", recoveries)
	}
	recovery := recoveries[0]
	canonical, err := CanonicalPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if recovery.Path != canonical {
		t.Fatalf("recovery path = %q, want %q", recovery.Path, canonical)
	}
	if recovery.BackupPath == "" || filepath.Dir(recovery.BackupPath) != filepath.Dir(path) {
		t.Fatalf("recovery backup path = %q, want a sibling of %s", recovery.BackupPath, path)
	}
	if recovery.Err == nil || !strings.Contains(recovery.Err.Error(), "legacy schema cannot be upgraded") {
		t.Fatalf("recovery error = %v, want the migration failure", recovery.Err)
	}
	if recovery.At.IsZero() {
		t.Fatal("recovery timestamp is zero")
	}
	requireLegacyRow(t, recovery.BackupPath)
	if drained := TakeMigrationRecoveries(); len(drained) != 1 {
		t.Fatalf("drain = %#v, want the single recovery back", drained)
	}
	if remaining := MigrationRecoveries(); len(remaining) != 0 {
		t.Fatalf("recoveries survived the drain: %#v", remaining)
	}
}

// TestUnclassifiedMigrationFailureKeepsDatabase pins the default: a migration
// error that was not marked as an incompatible schema is reported unchanged and
// must never cost the user their database.
func TestUnclassifiedMigrationFailureKeepsDatabase(t *testing.T) {
	TakeMigrationRecoveries()
	path := filepath.Join(t.TempDir(), "sessions.db")
	seededDatabase(t, path)

	_, err := Open(path, func(*sql.DB) error { return errors.New("disk on fire") })
	if err == nil {
		t.Fatal("expected the unclassified migration failure to be returned")
	}
	if !strings.Contains(err.Error(), "disk on fire") {
		t.Fatalf("error = %v, want the migration failure", err)
	}
	if len(MigrationRecoveries()) != 0 {
		t.Fatal("an unclassified migration failure must not trigger a rebuild")
	}
	if backups := migrationBackups(path); len(backups) != 0 {
		t.Fatalf("unexpected backups: %#v", backups)
	}
	requireLegacyRow(t, path)
}

// TestOpenRepairsStaleSecondaryIndex exercises the narrow repair path used
// when SQLite's quick_check reports that an index contains the wrong number of
// entries. The table pages are deliberately left intact; REINDEX must restore
// the derived index without losing those rows, and the repair must be reported
// so a front-end can tell the user the file had already survived a bad write.
func TestOpenRepairsStaleSecondaryIndex(t *testing.T) {
	TakeIndexRepairs()
	path := filepath.Join(t.TempDir(), "sessions.db")
	migrate := staleIndexSchema()
	staleIndexDatabase(t, path)

	repaired, err := Open(path, migrate)
	if err != nil {
		t.Fatalf("Open should repair the stale index: %v", err)
	}
	defer func() {
		if err := CloseAll(); err != nil {
			t.Fatal(err)
		}
	}()
	var count int
	if err := repaired.QueryRow(`SELECT COUNT(*) FROM entries INDEXED BY idx_entries_session_type`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("table rows after index repair = %d, want 2", count)
	}
	var integrity string
	if err := repaired.QueryRow(`PRAGMA quick_check`).Scan(&integrity); err != nil {
		t.Fatal(err)
	}
	if integrity != "ok" {
		t.Fatalf("quick_check after index repair = %q, want ok", integrity)
	}

	repairs := TakeIndexRepairs()
	if len(repairs) != 1 {
		t.Fatalf("reported index repairs = %#v, want exactly one", repairs)
	}
	canonical, err := CanonicalPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if repairs[0].Path != canonical || !strings.Contains(repairs[0].Cause, "wrong # of entries in index") || repairs[0].At.IsZero() {
		t.Fatalf("index repair = %#v, want the canonical path, the triggering quick_check line, and a timestamp", repairs[0])
	}
	if description := repairs[0].Describe(); !strings.Contains(description, canonical) {
		t.Fatalf("Describe() = %q, want it to name the repaired database", description)
	}
	if remaining := TakeIndexRepairs(); len(remaining) != 0 {
		t.Fatalf("index repairs survived the drain: %#v", remaining)
	}
}

// TestCheckIntegrityReportsContentionInsteadOfDamage keeps the repair path from
// describing writer contention as corruption. A peer holding the writer lock is
// an availability problem: startup must abort with the reason, leave the
// database untouched, and repair the same file successfully once the lock is
// released.
func TestCheckIntegrityReportsContentionInsteadOfDamage(t *testing.T) {
	TakeIndexRepairs()
	path := filepath.Join(t.TempDir(), "sessions.db")
	staleIndexDatabase(t, path)

	// Short driver busy timeouts keep the test observing SQLITE_BUSY in
	// milliseconds instead of absorbing the contention inside the driver for the
	// full production budget.
	gripper, err := sql.Open("sqlite", testDSNWithBusyTimeout(path, 50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer gripper.Close()
	writer, err := gripper.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("take the writer lock: %v", err)
	}

	victim, err := sql.Open("sqlite", testDSNWithBusyTimeout(path, 50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer victim.Close()

	if _, probeErr := victim.Exec("REINDEX"); probeErr == nil {
		_ = writer.Rollback()
		t.Skip("this filesystem did not report writer contention; the test cannot prove the classification")
	} else if !isSQLiteBusy(probeErr) {
		_ = writer.Rollback()
		t.Fatalf("probe REINDEX error = %v, want SQLITE_BUSY", probeErr)
	}

	originalBudget := indexRepairBudget
	indexRepairBudget = 200 * time.Millisecond
	defer func() { indexRepairBudget = originalBudget }()

	err = checkIntegrity(victim, path)
	_ = writer.Rollback()
	if err == nil {
		t.Fatal("checkIntegrity repaired an index it could not take the writer lock for")
	}
	if !strings.Contains(err.Error(), "another process still holds the SQLite writer lock") ||
		!strings.Contains(err.Error(), "it was not repaired") {
		t.Fatalf("error = %v, want the contention reason rather than a corruption verdict", err)
	}
	if strings.Contains(err.Error(), "integrity check failed") {
		t.Fatalf("error = %v, must not be classified as integrity failure", err)
	}
	if repairs := TakeIndexRepairs(); len(repairs) != 0 {
		t.Fatalf("a refused repair was reported as completed: %#v", repairs)
	}

	// With the writer released, the very same damaged index repairs cleanly.
	if err := checkIntegrity(victim, path); err != nil {
		t.Fatalf("checkIntegrity after the writer lock was released: %v", err)
	}
	if repairs := TakeIndexRepairs(); len(repairs) != 1 {
		t.Fatalf("index repairs after the successful retry = %#v, want one", repairs)
	}
}

// TestCheckIntegrityNeverRepairsTableDamage pins the other half of the narrow
// repair scope: when a table page is broken, quick_check lists that first, and
// startup must abort instead of trying to derive rows from a damaged table.
func TestCheckIntegrityNeverRepairsTableDamage(t *testing.T) {
	TakeIndexRepairs()
	path := filepath.Join(t.TempDir(), "sessions.db")
	migrate := staleIndexSchema()

	db, err := Open(path, migrate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO entries (session_id, type) VALUES ('session-1', 'message'), ('session-2', 'tool')`); err != nil {
		t.Fatal(err)
	}
	var rootPage, pageSize int
	if err := db.QueryRow(`SELECT rootpage FROM sqlite_master WHERE type = 'table' AND name = 'entries'`).Scan(&rootPage); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	if err := CloseAll(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Break the table b-tree page type, leaving the index mechanically valid.
	contents[(rootPage-1)*pageSize] = 0xf5
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(path, migrate); err == nil {
		t.Fatal("Open accepted a database with a broken table page")
	} else if !strings.Contains(err.Error(), "sqlite integrity check failed") || strings.Contains(err.Error(), "after index repair") {
		t.Fatalf("error = %v, want the plain integrity abort for table damage", err)
	}
	if repairs := TakeIndexRepairs(); len(repairs) != 0 {
		t.Fatalf("table damage was reported as an index repair: %#v", repairs)
	}
}

// TestAttemptWhileBusyRetriesOnlyWriterContention covers the shared retry shape
// without provoking a real lock: only SQLITE_BUSY/SQLITE_LOCKED is transient,
// anything else must fail on the first attempt, and the budget must end the
// retries with the driver's last error.
func TestAttemptWhileBusyRetriesOnlyWriterContention(t *testing.T) {
	attempts := 0
	err := attemptWhileBusy(time.Second, func() error {
		attempts++
		if attempts < 3 {
			return codedError{code: 5, msg: "database is locked"}
		}
		return nil
	})
	if err != nil || attempts != 3 {
		t.Fatalf("retry until free: err=%v attempts=%d, want success on the third attempt", err, attempts)
	}

	attempts = 0
	err = attemptWhileBusy(time.Second, func() error {
		attempts++
		return codedError{code: 1, msg: "sql logic error"}
	})
	if attempts != 1 || err == nil || !strings.Contains(err.Error(), "sql logic error") {
		t.Fatalf("non-transient error: err=%v attempts=%d, want one attempt and the original error", err, attempts)
	}

	attempts = 0
	err = attemptWhileBusy(80*time.Millisecond, func() error {
		attempts++
		return codedError{code: 6, msg: "database table is locked"}
	})
	if err == nil || attempts < 2 {
		t.Fatalf("exhausted budget: err=%v attempts=%d, want the last transient error after retrying", err, attempts)
	}
	if !isSQLiteBusy(err) {
		t.Fatalf("exhausted budget error = %v, want the driver's contention diagnostic", err)
	}
}

// TestSQLiteDriverIsRegistered guards the blank import in db.go: this package
// registers the "sqlite" driver for the whole process, so cleaning up its
// imports must never remove the driver registration.
func TestSQLiteDriverIsRegistered(t *testing.T) {
	for _, name := range sql.Drivers() {
		if name == "sqlite" {
			return
		}
	}
	t.Fatalf("the sqlite driver is not registered; drivers = %v", sql.Drivers())
}

// staleIndexSchema returns a migrator for the two-column table plus one
// secondary index the repair paths operate on.
func staleIndexSchema() Migrator {
	return func(sqlDB *sql.DB) error {
		if _, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS entries (session_id TEXT NOT NULL, type TEXT NOT NULL)`); err != nil {
			return err
		}
		_, err := sqlDB.Exec(`CREATE INDEX IF NOT EXISTS idx_entries_session_type ON entries(session_id, type)`)
		return err
	}
}

// staleIndexDatabase builds a database whose table rows are intact but whose
// secondary index has genuinely fallen behind: the index page as it stood before
// a second row was committed is spliced back over the current one, which is
// exactly what an interrupted write leaves behind. SQLite then reports
// "wrong # of entries in index ...", the one condition the repair path handles.
//
// Rewriting a key inside the index page is not a substitute: the entry count
// stays correct, quick_check reports "ok", and the repair path never runs.
func staleIndexDatabase(t *testing.T, path string) {
	t.Helper()
	db, err := Open(path, func(sqlDB *sql.DB) error {
		if _, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS entries (session_id TEXT NOT NULL, type TEXT NOT NULL)`); err != nil {
			return err
		}
		_, err := sqlDB.Exec(`CREATE INDEX IF NOT EXISTS idx_entries_session_type ON entries(session_id, type)`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO entries (session_id, type) VALUES ('session-1', 'message')`); err != nil {
		t.Fatal(err)
	}
	var rootPage, pageSize int
	if err := db.QueryRow(`SELECT rootpage FROM sqlite_master WHERE type = 'index' AND name = 'idx_entries_session_type'`).Scan(&rootPage); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	if err := CloseAll(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	db, err = Open(path, func(sqlDB *sql.DB) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO entries (session_id, type) VALUES ('session-2', 'tool')`); err != nil {
		t.Fatal(err)
	}
	if err := CloseAll(); err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	pageOffset := (rootPage - 1) * pageSize
	if pageOffset < 0 || pageOffset+pageSize > len(current) || pageOffset+pageSize > len(before) {
		t.Fatalf("index root page is outside the database: page=%d", rootPage)
	}
	if before[pageOffset] != 0x0a || current[pageOffset] != 0x0a {
		t.Fatalf("index root page is not an index leaf: before=%#x current=%#x", before[pageOffset], current[pageOffset])
	}
	if indexCellCount(before[pageOffset:pageOffset+pageSize]) != 1 || indexCellCount(current[pageOffset:pageOffset+pageSize]) != 2 {
		t.Fatalf("index pages do not differ by one entry: before=%d current=%d",
			indexCellCount(before[pageOffset:pageOffset+pageSize]), indexCellCount(current[pageOffset:pageOffset+pageSize]))
	}
	copy(current[pageOffset:pageOffset+pageSize], before[pageOffset:pageOffset+pageSize])
	if err := os.WriteFile(path, current, 0600); err != nil {
		t.Fatal(err)
	}

	// Prove the fixture is the real condition, not a coincidence of wording.
	connection, err := sql.Open("sqlite", DSNForOS(path, false))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	var integrity string
	if err := connection.QueryRow("PRAGMA quick_check").Scan(&integrity); err != nil {
		t.Fatal(err)
	}
	if !isStaleIndexReport(integrity) {
		t.Fatalf("tampered database reports %q, want the stale-index condition the repair path handles", integrity)
	}
	var rows int
	if err := connection.QueryRow(`SELECT COUNT(*) FROM entries`).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("table rows after the tamper = %d (err=%v), want 2: only the index may be stale", rows, err)
	}
}

// indexCellCount reads the number of cells in an index b-tree page header, which
// is what SQLite compares against the table row count.
func indexCellCount(page []byte) int {
	return int(page[3])<<8 | int(page[4])
}

// testDSNWithBusyTimeout opens the same file URI the package uses, but with a
// caller-chosen busy_timeout so a test can observe SQLITE_BUSY quickly instead
// of waiting out the production budget inside the driver.
func testDSNWithBusyTimeout(path string, timeout time.Duration) string {
	uriPath := filepath.ToSlash(path)
	if runtime.GOOS == "windows" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	return fmt.Sprintf("file:%s?_pragma=busy_timeout(%d)&_txlock=immediate", uriPath, timeout.Milliseconds())
}

// TestRebuildGuardrailsSkipTransientFailures proves a rebuild is refused when a
// healthy database can produce the same failure: writer contention from another
// process and a read-only file must leave the database untouched, even when the
// migration owner marked the failure as incompatible.
func TestRebuildGuardrailsSkipTransientFailures(t *testing.T) {
	tests := []struct {
		name    string
		failure error
		want    string
	}{
		{name: "writer lock held by another process", failure: codedError{code: 5, msg: "database is locked"}, want: "another process holds the SQLite writer lock"},
		{name: "read-only database file", failure: codedError{code: 8, msg: "attempt to write a readonly database"}, want: "the database file is read-only"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			TakeMigrationRecoveries()
			path := filepath.Join(t.TempDir(), "sessions.db")
			seededDatabase(t, path)

			_, err := Open(path, func(*sql.DB) error { return SchemaIncompatible(tt.failure) })
			if err == nil {
				t.Fatal("expected the migration failure to be returned")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want the guardrail reason %q", err, tt.want)
			}
			if !strings.Contains(err.Error(), "left untouched") {
				t.Fatalf("error = %v, want an explicit note that the database was left untouched", err)
			}
			if len(MigrationRecoveries()) != 0 {
				t.Fatal("a transient failure must not trigger a rebuild")
			}
			if backups := migrationBackups(path); len(backups) != 0 {
				t.Fatalf("unexpected backups: %#v", backups)
			}
			requireLegacyRow(t, path)
		})
	}
}

// TestSnapshotFailureKeepsTheOriginalDatabase keeps the failure honest: if the
// snapshot itself cannot be written, the original database is left in place, the
// failed connection is released, and nothing is reported as a recovery.
func TestSnapshotFailureKeepsTheOriginalDatabase(t *testing.T) {
	TakeMigrationRecoveries()
	path := filepath.Join(t.TempDir(), "sessions.db")
	seededDatabase(t, path)

	connection, err := sql.Open("sqlite", DSNForOS(path, false))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	// A backup path under a missing directory fails VACUUM INTO and the raw-copy
	// fallback alike, whatever the SQLite version does internally.
	unwritable := filepath.Join(t.TempDir(), "missing-dir", "backup.bak")
	backupPath, err := backupPathFor(unwritable)
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshotDatabase(connection, path, backupPath); err == nil {
		t.Fatalf("snapshotDatabase succeeded for unwritable backup path %s", backupPath)
	}
	if len(MigrationRecoveries()) != 0 {
		t.Fatal("a failed snapshot must not be reported as a recovery")
	}
	requireLegacyRow(t, path)
}

// TestRecoverFromMigrationFailureClosesTheFailedConnection proves the recovery
// path never leaks the connection that produced the migration failure.
func TestRecoverFromMigrationFailureClosesTheFailedConnection(t *testing.T) {
	TakeMigrationRecoveries()
	path := filepath.Join(t.TempDir(), "sessions.db")
	seededDatabase(t, path)

	connection, err := sql.Open("sqlite", DSNForOS(path, false))
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := recoverFromMigrationFailure(connection, path, errors.New("boom"))
	if err != nil {
		t.Fatalf("recoverFromMigrationFailure: %v", err)
	}
	if recovery.BackupPath == "" {
		t.Fatal("recovery did not record a backup path")
	}
	requireLegacyRow(t, recovery.BackupPath)
	if err := connection.Ping(); err == nil {
		t.Fatal("the failed connection was left open after recovery")
	}
}

func TestSchemaIncompatibleMarker(t *testing.T) {
	if SchemaIncompatible(nil) != nil {
		t.Fatal("SchemaIncompatible(nil) must stay nil")
	}
	if IsSchemaIncompatible(errors.New("plain")) {
		t.Fatal("a plain error must not be reported as schema-incompatible")
	}
	marked := SchemaIncompatible(errors.New("boom"))
	if !IsSchemaIncompatible(marked) || marked.Error() != "boom" {
		t.Fatalf("marked error = %v, want a schema-incompatible wrapper of boom", marked)
	}
	if IsSchemaIncompatible(errors.New("wrapped: " + marked.Error())) {
		t.Fatal("unrelated text must not be classified as schema-incompatible")
	}
}

// TestMigrationRecoveryNotifierAnnouncesTheRecovery proves the schema owner can
// announce a recovery over its inter-process bus without internal/db importing
// that package, and that a cleared hook stops receiving.
func TestMigrationRecoveryNotifierAnnouncesTheRecovery(t *testing.T) {
	TakeMigrationRecoveries()
	announced := make(chan MigrationRecovery, 1)
	SetMigrationRecoveryNotifier(func(recovery MigrationRecovery) { announced <- recovery })
	defer SetMigrationRecoveryNotifier(nil)

	current := func(sqlDB *sql.DB) error {
		var legacy int
		if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'legacy_items'`).Scan(&legacy); err != nil {
			return err
		}
		if legacy != 0 {
			return SchemaIncompatible(errors.New("legacy schema cannot be upgraded"))
		}
		_, err := sqlDB.Exec(`CREATE TABLE current_items (id TEXT PRIMARY KEY)`)
		return err
	}

	path := filepath.Join(t.TempDir(), "sessions.db")
	seededDatabase(t, path)
	if _, err := Open(path, current); err != nil {
		t.Fatalf("Open after a recoverable migration failure: %v", err)
	}
	if err := CloseAll(); err != nil {
		t.Fatal(err)
	}
	select {
	case recovery := <-announced:
		if recovery.Path == "" || recovery.BackupPath == "" || recovery.Peer {
			t.Fatalf("announced recovery = %#v, want a completed local rebuild", recovery)
		}
	default:
		t.Fatal("the recovery was recorded without announcing it")
	}
	TakeMigrationRecoveries()

	SetMigrationRecoveryNotifier(nil)
	second := filepath.Join(t.TempDir(), "sessions.db")
	seededDatabase(t, second)
	if _, err := Open(second, current); err != nil {
		t.Fatalf("Open after a second recoverable migration failure: %v", err)
	}
	if err := CloseAll(); err != nil {
		t.Fatal(err)
	}
	if len(TakeMigrationRecoveries()) != 1 {
		t.Fatal("the second recovery was not recorded")
	}
	select {
	case recovery := <-announced:
		t.Fatalf("a cleared notifier still received %#v", recovery)
	default:
	}
}
