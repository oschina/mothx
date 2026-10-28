package session

import (
	"testing"
)

// TestSessionStoreWriteVersionTracksSearchWrites pins migration 53: every table
// whose rows can change a search result must bump the durable version in the
// same statement, and deleting a session must bump through the cascade too.
// The query cache depends on this being monotonic; a missed trigger would let a
// stale page survive a write.
func TestSessionStoreWriteVersionTracksSearchWrites(t *testing.T) {
	db := fullyMigratedTestDatabase(t)

	version := func() int64 {
		t.Helper()
		var value int64
		if err := db.QueryRow(`SELECT value FROM session_store_meta WHERE key = 'search_write_version'`).Scan(&value); err != nil {
			t.Fatalf("read write version: %v", err)
		}
		return value
	}
	assertBumped := func(before int64, operation string) {
		t.Helper()
		after := version()
		if after <= before {
			t.Fatalf("%s did not bump the search write version: %d -> %d", operation, before, after)
		}
	}

	if _, err := db.Exec(`INSERT INTO sessions (id, cwd, timestamp, version) VALUES ('version-session', '/tmp/version', '2026-01-01T00:00:00Z', 1)`); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	before := version()
	if _, err := db.Exec(`INSERT INTO entries (session_id, id, type, timestamp, data) VALUES ('version-session', 'version-entry', 'message', '2026-01-01T00:00:01Z', '{}')`); err != nil {
		t.Fatalf("insert entry: %v", err)
	}
	assertBumped(before, "entry insert")

	before = version()
	if _, err := db.Exec(`UPDATE entries SET data = '{"changed":true}' WHERE session_id = 'version-session'`); err != nil {
		t.Fatalf("update entry: %v", err)
	}
	assertBumped(before, "entry update")

	before = version()
	if _, err := db.Exec(`INSERT INTO session_event_projections (id, session_id, seq, type, timestamp, batch_id) VALUES ('version-projection', 'version-session', 1, 'message', '2026-01-01T00:00:02Z', 'version-batch')`); err != nil {
		t.Fatalf("insert projection: %v", err)
	}
	assertBumped(before, "projection insert")

	before = version()
	if _, err := db.Exec(`INSERT INTO session_metadata (session_id, updated_at) VALUES ('version-session', '2026-01-01T00:00:03Z')`); err != nil {
		t.Fatalf("insert session metadata: %v", err)
	}
	assertBumped(before, "metadata insert")

	before = version()
	if _, err := db.Exec(`UPDATE sessions SET cwd = '/tmp/version-two' WHERE id = 'version-session'`); err != nil {
		t.Fatalf("update session cwd: %v", err)
	}
	assertBumped(before, "session update")

	// Deleting the parent cascades to entries/projections/metadata. Even if a
	// child delete were not observed, the parent trigger alone must invalidate.
	before = version()
	if _, err := db.Exec(`DELETE FROM sessions WHERE id = 'version-session'`); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	assertBumped(before, "session delete")
}
