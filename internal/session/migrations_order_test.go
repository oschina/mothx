package session

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// legacyBaseSchema is the smallest pre-migration database shape: the tables
// that existed before versioned migrations were introduced.
const legacyBaseSchema = `
CREATE TABLE sessions (
	id TEXT PRIMARY KEY,
	cwd TEXT,
	timestamp TEXT,
	parent_session TEXT,
	version INTEGER
);
CREATE TABLE entries (
	seq INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT REFERENCES sessions(id),
	id TEXT UNIQUE,
	type TEXT NOT NULL,
	parent_id TEXT,
	timestamp TEXT NOT NULL,
	data TEXT NOT NULL
);
CREATE TABLE session_capabilities (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id),
	mode TEXT NOT NULL DEFAULT '',
	delegate_mode INTEGER NOT NULL DEFAULT 0,
	multi_agent INTEGER NOT NULL DEFAULT 0,
	workflows INTEGER NOT NULL DEFAULT 0,
	web_search INTEGER NOT NULL DEFAULT 0,
	browser INTEGER NOT NULL DEFAULT 0,
	a2a_master INTEGER NOT NULL DEFAULT 0,
	updated_at TEXT NOT NULL
);
`

// TestSchemaMigrationsApplyInAscendingVersionOrder upgrades a legacy database
// with foreign key enforcement enabled. Several migrations reference tables
// created by other migrations (delivery_operations -> session_attachments,
// runtime_submissions -> session_execution_intents, input_resource_events ->
// input_resources), so any newest-first application order would fail the
// CREATE TABLE statements once SQLite resolves FK parents.
func TestSchemaMigrationsApplyInAscendingVersionOrder(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(legacyBaseSchema); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}

	if err := applySchemaMigrations(db); err != nil {
		t.Fatalf("apply migrations in ascending order: %v", err)
	}

	// Migrations must be recorded in strictly ascending version order.
	rows, err := db.Query("SELECT version FROM schema_migrations ORDER BY rowid")
	if err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	defer rows.Close()
	var applied []int
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			t.Fatalf("scan migration version: %v", err)
		}
		applied = append(applied, version)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migration versions: %v", err)
	}
	if len(applied) != len(schemaMigrations) {
		t.Fatalf("applied %d migrations, want %d", len(applied), len(schemaMigrations))
	}
	for i := 1; i < len(applied); i++ {
		if applied[i] <= applied[i-1] {
			t.Fatalf("migration order not ascending: %v", applied)
		}
	}

	// Tables whose creation depends on earlier migrations must exist now.
	for _, table := range []string{
		"session_attachments",
		"attachment_deliveries",
		"delivery_intents",
		"delivery_operations",
		"session_execution_intents",
		"runtime_submissions",
		"input_resources",
		"input_resource_events",
		"session_run_recoveries",
		"session_esm_guidance",
		"projects",
		"session_metadata",
		"session_channel_tools",
		"session_channel_tool_generations",
		"conversation_turns",
		"session_fork_requests",
		"session_runtime_leases",
		"response_turns",
		"response_items",
		"response_runs",
		"response_session_state",
		"tool_execution_records",
	} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("table %s missing after ascending migration", table)
		}
	}

	// Re-running migrations must stay idempotent.
	if err := applySchemaMigrations(db); err != nil {
		t.Fatalf("re-apply migrations: %v", err)
	}
	var total int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&total); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if total != len(schemaMigrations) {
		t.Fatalf("schema_migrations rows = %d, want %d", total, len(schemaMigrations))
	}
}

// TestSchemaMigrationVersionsAreUnique guards the append-only migration list
// against duplicate version numbers, which ascending sort cannot repair.
func TestSchemaMigrationVersionsAreUnique(t *testing.T) {
	seen := make(map[int]string, len(schemaMigrations))
	maxVersion := 0
	for _, migration := range schemaMigrations {
		if migration.version <= 0 {
			t.Fatalf("migration %s has invalid version %d", migration.name, migration.version)
		}
		if migration.version > currentSchemaVersion {
			t.Fatalf("migration %s version %d exceeds currentSchemaVersion %d", migration.name, migration.version, currentSchemaVersion)
		}
		if name, exists := seen[migration.version]; exists {
			t.Fatalf("duplicate migration version %d: %s and %s", migration.version, name, migration.name)
		}
		seen[migration.version] = migration.name
		if migration.version > maxVersion {
			maxVersion = migration.version
		}
	}
	if maxVersion != currentSchemaVersion {
		t.Fatalf("highest migration version = %d, want currentSchemaVersion %d", maxVersion, currentSchemaVersion)
	}
}

func TestSchemaMigrationAppliesAfterHistoricalVersionCollision(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(legacyBaseSchema); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	// Existing MothX databases can retain a historical migration row at
	// version 36. The expert-binding migration must use a later append-only
	// version so this unrelated row cannot cause the column migration to be
	// skipped.
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
		INSERT INTO schema_migrations(version, name) VALUES (36, '001_create_sessions_table');`); err != nil {
		t.Fatalf("seed historical migration: %v", err)
	}
	if err := applySchemaMigrations(db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	rows, err := db.Query(`PRAGMA table_info(sessions)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var found bool
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		if name == "expert_id" {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("sessions.expert_id was not added after historical migration-version collision")
	}
	var version int
	if err := db.QueryRow(`SELECT version FROM schema_migrations WHERE name = 'add_sessions_expert_id'`).Scan(&version); err != nil {
		t.Fatalf("read expert migration: %v", err)
	}
	if version != 41 {
		t.Fatalf("expert migration version = %d, want 41", version)
	}
}

// TestEnsureCurrentSchemaConvergesPartialContentStoreMigration reproduces the
// database shape left behind when migration 47 recorded itself as applied while
// only the sessions half of the change landed. requiredSchema hard-requires
// sub_session.content_store, so without the version-48 convergence step this
// database is reported incompatible and internal/db responds by backing it up
// and rebuilding it empty, discarding every session.
func TestEnsureCurrentSchemaConvergesPartialContentStoreMigration(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "partial-47.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(currentSchema); err != nil {
		t.Fatalf("create current schema: %v", err)
	}
	// Simulate a build that applied 47 to sessions only, then recorded it.
	if _, err := db.Exec(`ALTER TABLE sub_session DROP COLUMN content_store`); err != nil {
		t.Fatalf("drop sub_session.content_store: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("create migration history: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations(version, name) VALUES (47, 'add_session_content_store_mode')`); err != nil {
		t.Fatalf("seed partial migration 47 row: %v", err)
	}

	if err := EnsureCurrentSchema(db); err != nil {
		t.Fatalf("EnsureCurrentSchema on partial v47 database: %v", err)
	}

	for _, table := range []string{"sessions", "sub_session"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = 'content_store'", table).Scan(&count); err != nil {
			t.Fatalf("check %s.content_store: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("table %s is missing content_store after convergence", table)
		}
	}
	var version int
	if err := db.QueryRow(`SELECT version FROM schema_migrations WHERE name = 'converge_content_store_columns'`).Scan(&version); err != nil {
		t.Fatalf("read convergence migration: %v", err)
	}
	if version != 48 {
		t.Fatalf("convergence migration version = %d, want 48", version)
	}
	// Re-running must stay idempotent, so this database never reaches the
	// destructive backup-and-rebuild path.
	if err := EnsureCurrentSchema(db); err != nil {
		t.Fatalf("EnsureCurrentSchema must stay idempotent: %v", err)
	}
	var after int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name = 'converge_content_store_columns'`).Scan(&after); err != nil {
		t.Fatalf("count convergence rows: %v", err)
	}
	if after != 1 {
		t.Fatalf("convergence migration rows = %d, want 1", after)
	}
}
