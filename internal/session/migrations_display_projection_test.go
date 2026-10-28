package session

import "testing"

// TestSessionDisplayProjectionMigration pins migration 52's table shape: the
// preview-seen flag is part of the persisted state so a later user message can
// never replace an empty first-user preview.
func TestSessionDisplayProjectionMigration(t *testing.T) {
	db := fullyMigratedTestDatabase(t)

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'session_display_projection'`).Scan(&count); err != nil {
		t.Fatalf("inspect display projection table: %v", err)
	}
	if count != 1 {
		t.Fatal("session_display_projection is missing after migration 52")
	}
	for _, column := range []string{"session_id", "preview", "preview_seen", "title", "bindings_json", "updated_at"} {
		var present int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('session_display_projection') WHERE name = ?`, column).Scan(&present); err != nil {
			t.Fatalf("inspect column %s: %v", column, err)
		}
		if present != 1 {
			t.Fatalf("session_display_projection is missing column %s", column)
		}
	}
}
