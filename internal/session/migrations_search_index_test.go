package session

import (
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/dao"
)

// TestSessionContentFTSMigrationEnablesSubstringMatching pins migration 51's
// search contract: the index matches arbitrary substrings (including CJK)
// rather than only token prefixes, which is what keeps the derived candidate
// set compatible with the previous strings.Contains search semantics.
func TestSessionContentFTSMigrationEnablesSubstringMatching(t *testing.T) {
	db := fullyMigratedTestDatabase(t)

	var tableSQL string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'session_content_fts'`).Scan(&tableSQL); err != nil {
		t.Fatalf("read session_content_fts schema: %v", err)
	}
	if !strings.Contains(tableSQL, "trigram") {
		t.Fatalf("session_content_fts must use the trigram tokenizer, got %q", tableSQL)
	}
	var stateTable int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'session_content_index_state'`).Scan(&stateTable); err != nil {
		t.Fatalf("inspect index state table: %v", err)
	}
	if stateTable != 1 {
		t.Fatal("session_content_index_state is missing after migration 51")
	}

	if _, err := db.Exec(`INSERT INTO session_content_fts(session_id, entry_id, text) VALUES ('s1', 'e1', ?)`, "MothX Desktop 桌面检索系统"); err != nil {
		t.Fatalf("insert fts row: %v", err)
	}
	cases := []struct {
		query string
		want  int
	}{
		{"othx", 1},    // mid-token, case-insensitive
		{"desktop", 1}, // token prefix
		{"桌面检", 1},     // three-rune CJK substring
		{"检索", 0},      // two runes: trigram cannot answer, search must fall back
	}
	for _, tc := range cases {
		var count int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM session_content_fts WHERE session_content_fts MATCH ?`,
			dao.SessionSearchFTSPhrase(tc.query),
		).Scan(&count); err != nil {
			t.Fatalf("match %q: %v", tc.query, err)
		}
		if count != tc.want {
			t.Fatalf("match %q = %d, want %d", tc.query, count, tc.want)
		}
	}

	// Quoting must keep FTS operators literal.
	if _, err := db.Exec(`INSERT INTO session_content_fts(session_id, entry_id, text) VALUES ('s2', 'e2', ?)`, `literal "quoted" operator`); err != nil {
		t.Fatalf("insert quoted fts row: %v", err)
	}
	var quoted int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM session_content_fts WHERE session_content_fts MATCH ?`,
		dao.SessionSearchFTSPhrase(`"quoted"`),
	).Scan(&quoted); err != nil {
		t.Fatalf("match quoted text: %v", err)
	}
	if quoted != 1 {
		t.Fatalf("quoted match = %d, want 1", quoted)
	}
}
