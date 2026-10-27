package session

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// sessionListAggregateQuery is the session-list projection's last-active
// subquery shape: each session's newest `entries` timestamp with the
// EventStore projection as the migrated fallback. It mirrors the expression in
// dao.sessionListColumns so the planner under test is the one production uses.
const sessionListAggregateQuery = `
SELECT s.id,
	COALESCE(
		(SELECT MAX(e.timestamp) FROM entries e WHERE e.session_id = s.id),
		(SELECT MAX(p.timestamp) FROM session_event_projections p WHERE p.session_id = s.id),
		s.timestamp
	) AS last_active_at
FROM sessions AS s`

// TestSessionListAggregatesUseIncrementalIndexes pins migration 49's whole
// purpose: the session list's MAX(timestamp) aggregates must be answered from a
// (session_id, timestamp) index instead of walking every row of that session.
//
// The assertion is on the planner's own output, not on timing: the plan must
// name the new index. It is written so an equivalent index with a different
// name is not silently accepted, because the migration is what has to provide
// this shape.
func TestSessionListAggregatesUseIncrementalIndexes(t *testing.T) {
	db := fullyMigratedTestDatabase(t)

	// Seed enough rows that a full scan would look plausible to the planner.
	if _, err := db.Exec(`INSERT INTO sessions (id, cwd, timestamp, version) VALUES ('session-a', '/tmp/a', '2026-01-01T00:00:00Z', 3)`); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	for i := 0; i < 64; i++ {
		if _, err := db.Exec(`INSERT INTO entries (session_id, id, type, timestamp, data) VALUES ('session-a', ?, 'message', ?, '{}')`,
			fmt.Sprintf("entry-%02d", i), "2026-01-02T00:00:00Z"); err != nil {
			t.Fatalf("seed entry %d: %v", i, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO session_event_projections (id, session_id, seq, type, timestamp, batch_id) VALUES ('event-1', 'session-a', 1, 'message', '2026-01-03T00:00:00Z', 'batch-1')`); err != nil {
		t.Fatalf("seed projection: %v", err)
	}

	rows, err := db.Query("EXPLAIN QUERY PLAN " + sessionListAggregateQuery)
	if err != nil {
		t.Fatalf("explain session list query: %v", err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		plan.WriteString(detail)
		plan.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate query plan: %v", err)
	}

	details := plan.String()
	for _, index := range []string{
		"idx_entries_session_timestamp",
		"idx_session_event_projections_session_timestamp",
	} {
		if !strings.Contains(details, index) {
			t.Fatalf("session list plan does not use %s, so the aggregate still scans the session's rows:\n%s", index, details)
		}
	}
	if strings.Contains(details, "SCAN entries") {
		t.Fatalf("session list plan still scans entries:\n%s", details)
	}
	if strings.Contains(details, "SCAN session_event_projections") {
		t.Fatalf("session list plan still scans session_event_projections:\n%s", details)
	}

	// The third incremental index backs per-session non-terminal Run reads.
	for _, index := range []string{
		"idx_entries_session_timestamp",
		"idx_session_event_projections_session_timestamp",
		"idx_session_runs_session_status_started",
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&count); err != nil {
			t.Fatalf("inspect index %s: %v", index, err)
		}
		if count != 1 {
			t.Fatalf("index %s is missing after migration 49", index)
		}
	}
}

// TestSessionRunSessionStatusStartedIndexIsUsed pins that migration 49's Run
// index matches the queries production actually issues. Run reads (ActiveRun,
// ActiveBySessions, OrphanedFrom) filter on session_id plus an IN list of
// statuses and order by started_at; a status-leading index cannot serve that
// shape, so the assertion covers the multi-session production query rather than
// only a status-only projection. A single-status read is kept as well because it
// is the narrow case the same index must also answer without a temp sort.
func TestSessionRunSessionStatusStartedIndexIsUsed(t *testing.T) {
	db := fullyMigratedTestDatabase(t)
	seedSessionRunsForPlan(t, db)

	plans := []struct {
		name  string
		query string
		args  []any
	}{
		{
			// RunDAO.ActiveBySessions: WHERE session_id IN (...) AND status IN (...)
			// ORDER BY started_at ASC.
			name:  "multi-session active runs",
			query: `SELECT id, started_at FROM session_runs WHERE session_id IN (?,?) AND status IN ('running','starting') ORDER BY started_at ASC`,
			args:  []any{"session-a", "session-b"},
		},
		{
			// The single-session read (ActiveRun/OrphanedFrom) with one status.
			name:  "single-session single-status active run",
			query: `SELECT id, started_at FROM session_runs WHERE session_id = 'session-a' AND status = 'running' ORDER BY started_at DESC LIMIT 1`,
		},
	}
	for _, tc := range plans {
		details := explainQueryPlan(t, db, tc.query, tc.args...)
		if !strings.Contains(details, "idx_session_runs_session_status_started") {
			t.Fatalf("%s does not use idx_session_runs_session_status_started:\n%s", tc.name, details)
		}
		if strings.Contains(details, "SCAN session_runs") {
			t.Fatalf("%s scans every Run row instead of narrowing by session/status:\n%s", tc.name, details)
		}
	}
}

// seedSessionRunsForPlan inserts enough Run rows across sessions and statuses
// for the planner's choice to be meaningful. Only one non-terminal row per
// session is allowed because of idx_session_runs_active_session, so the bulk
// rows use terminal statuses and 'starting' (not part of the non-terminal set).
func seedSessionRunsForPlan(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, sessionID := range []string{"session-a", "session-b"} {
		if _, err := db.Exec(`INSERT INTO sessions (id, cwd, timestamp, version) VALUES (?, ?, '2026-01-01T00:00:00Z', 3)`, sessionID, "/tmp/"+sessionID); err != nil {
			t.Fatalf("seed session %s: %v", sessionID, err)
		}
		if _, err := db.Exec(`INSERT INTO session_runs (id, session_id, status, started_at, updated_at) VALUES (?, ?, 'running', '2026-01-15T00:00:00Z', '2026-01-15T00:00:00Z')`, "run-live-"+sessionID, sessionID); err != nil {
			t.Fatalf("seed running run for %s: %v", sessionID, err)
		}
		for i := 0; i < 40; i++ {
			status := "completed"
			if i%8 == 0 {
				status = "starting" // Runner status outside the non-terminal set.
			}
			if _, err := db.Exec(`INSERT INTO session_runs (id, session_id, status, started_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
				fmt.Sprintf("run-%s-%02d", sessionID, i), sessionID, status, fmt.Sprintf("2026-01-%02dT00:00:00Z", i+1), fmt.Sprintf("2026-01-%02dT00:00:00Z", i+1)); err != nil {
				t.Fatalf("seed run %s/%d: %v", sessionID, i, err)
			}
		}
	}
}

// explainQueryPlan returns SQLite's planner output for query, one detail per line.
func explainQueryPlan(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explain query plan: %v", err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		plan.WriteString(detail)
		plan.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate query plan: %v", err)
	}
	return plan.String()
}
