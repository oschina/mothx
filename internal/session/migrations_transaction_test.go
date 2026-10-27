package session

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// countingSchemaDatabase wraps a real SQLite handle and counts the write
// transactions the migration path opens. Reads pass straight through, so the
// test still exercises the production SQL instead of a fake query engine. It is
// the schema counterpart of internal/db's transaction counters: deterministic
// observability rather than a timing assertion.
type countingSchemaDatabase struct {
	*sql.DB
	begins int
}

func (c *countingSchemaDatabase) Begin() (*sql.Tx, error) {
	c.begins++
	return c.DB.Begin()
}

// fullyMigratedTestDatabase returns a file-backed database that already passed
// EnsureCurrentSchema, i.e. the state every later open of sessions.db sees.
func fullyMigratedTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "migrated.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := EnsureCurrentSchema(db); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	return db
}

// TestApplySchemaMigrationsSkipsWritesWhenCurrent pins the steady state: opening
// an up-to-date sessions.db must not open a single write transaction for the
// migration check. Before the ledger was read once up front, the check began one
// transaction per migration plus one for the ledger probe (~29 BEGIN IMMEDIATE
// on a current database).
func TestApplySchemaMigrationsSkipsWritesWhenCurrent(t *testing.T) {
	db := fullyMigratedTestDatabase(t)
	wrapped := &countingSchemaDatabase{DB: db}

	if err := applySchemaMigrations(wrapped); err != nil {
		t.Fatalf("apply schema migrations: %v", err)
	}
	if wrapped.begins != 0 {
		t.Fatalf("migration check on a current database began %d write transactions, want 0", wrapped.begins)
	}

	// Repeated opens must stay write-free as well.
	if err := applySchemaMigrations(wrapped); err != nil {
		t.Fatalf("re-apply schema migrations: %v", err)
	}
	if wrapped.begins != 0 {
		t.Fatalf("repeated migration check began %d write transactions, want 0", wrapped.begins)
	}
}

// TestApplySchemaMigrationsBeginsOnceForMissingMigration pins the incremental
// case: only the missing migration gets a transaction, and its ledger row and
// schema effect land exactly once.
func TestApplySchemaMigrationsBeginsOnceForMissingMigration(t *testing.T) {
	db := fullyMigratedTestDatabase(t)
	const missingVersion = 49
	const missingName = "add_session_list_aggregate_indexes"
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = ?`, missingVersion); err != nil {
		t.Fatalf("forget migration %d: %v", missingVersion, err)
	}
	wrapped := &countingSchemaDatabase{DB: db}

	if err := applySchemaMigrations(wrapped); err != nil {
		t.Fatalf("apply schema migrations: %v", err)
	}
	if wrapped.begins != 1 {
		t.Fatalf("migration check with one missing migration began %d write transactions, want 1", wrapped.begins)
	}
	var version int
	if err := db.QueryRow(`SELECT version FROM schema_migrations WHERE name = ?`, missingName).Scan(&version); err != nil {
		t.Fatalf("read re-applied migration: %v", err)
	}
	if version != missingVersion {
		t.Fatalf("re-applied migration version = %d, want %d", version, missingVersion)
	}
	var indexCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_entries_session_timestamp'`).Scan(&indexCount); err != nil {
		t.Fatalf("inspect re-created index: %v", err)
	}
	if indexCount != 1 {
		t.Fatalf("index count = %d, want the missing migration to create it", indexCount)
	}
}

// TestApplySchemaMigrationsBackfillsLegacyNullVersionRow covers the one legacy
// repair that still needs a write: a name-only ledger row whose version column
// is NULL. The backfill must use the original UPDATE ... WHERE version IS NULL
// statement, preserve the row's applied_at, and leave exactly one row behind.
//
// The fixture is the current base schema plus a pre-version ledger, the shape
// EnsureCurrentSchema leaves before migrations run, so applying the rest of the
// migration set is the same work a real upgrade does.
func TestApplySchemaMigrationsBackfillsLegacyNullVersionRow(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-ledger.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(currentSchema); err != nil {
		t.Fatalf("create base schema: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (name TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatalf("create legacy ledger: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations(name, applied_at) VALUES ('add_channel_binding_columns', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed legacy ledger row: %v", err)
	}

	if err := applySchemaMigrations(&countingSchemaDatabase{DB: db}); err != nil {
		t.Fatalf("apply schema migrations: %v", err)
	}

	var version int
	var appliedAt string
	if err := db.QueryRow(`SELECT version, applied_at FROM schema_migrations WHERE name = 'add_channel_binding_columns'`).Scan(&version, &appliedAt); err != nil {
		t.Fatalf("read backfilled row: %v", err)
	}
	if version != 16 {
		t.Fatalf("backfilled version = %d, want 16", version)
	}
	if appliedAt != "2026-01-01T00:00:00Z" {
		t.Fatalf("backfill rewrote applied_at to %q, want the original timestamp", appliedAt)
	}
	var rowCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name = 'add_channel_binding_columns'`).Scan(&rowCount); err != nil {
		t.Fatalf("count backfilled row: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("backfilled row count = %d, want 1", rowCount)
	}
	var nullVersions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version IS NULL`).Scan(&nullVersions); err != nil {
		t.Fatalf("count legacy version rows: %v", err)
	}
	if nullVersions != 0 {
		t.Fatalf("rows with a NULL version = %d, want every legacy row backfilled", nullVersions)
	}
	var total int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&total); err != nil {
		t.Fatalf("count ledger rows: %v", err)
	}
	if total != len(schemaMigrations) {
		t.Fatalf("ledger rows = %d, want %d (one per migration incl. the legacy row)", total, len(schemaMigrations))
	}
}
