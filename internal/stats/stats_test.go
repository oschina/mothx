package stats

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/session"
	_ "modernc.org/sqlite"
)

func TestDashboardUsesMothXSmallFavicon(t *testing.T) {
	if !strings.Contains(dashboardHTML, `href="/mothx-small.ico"`) {
		t.Fatal("dashboard must reference mothx-small.ico")
	}
	if len(mothxSmallICO) == 0 {
		t.Fatal("embedded mothx-small.ico is empty")
	}
}
func TestDashboardShareActivityUsesSevenDayFlameHeatmap(t *testing.T) {
	for _, expected := range []string{
		"Last 7 days by 2-hour intensity",
		"groupBy: '1h'",
		"shareStartDate.setDate(shareStartDate.getDate() - 6)",
		"from: localDate(shareStartDate)",
		"const days = []",
		"const hourMap = {}",
		"const slotH = (chartH - slotGap * 11) / 12",
		"function flameColor(value)",
	} {
		if !strings.Contains(dashboardHTML, expected) {
			t.Errorf("dashboard share activity is missing %q", expected)
		}
	}
}

func createTestDB(t *testing.T) *DB {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sessions.db")

	// Create a minimal db file (simulates an old DB without request_stats)
	f, err := os.Create(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	// Open auto-creates request_stats table via ensureTable()
	db, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSummary(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	now := time.Now().Format(time.RFC3339Nano)
	_, err := db.db.Bun().Exec(
		"INSERT INTO request_stats (timestamp, session_id, provider, protocol, model, input_tokens, output_tokens, total_tokens, duration_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		now, "sess1", "openai", "openai-chat", "gpt-4", 1000, 500, 1500, 2000,
	)
	if err != nil {
		t.Fatal(err)
	}

	summary, err := db.Summary(Query{})
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalRequests != 1 {
		t.Errorf("expected 1 request, got %d", summary.TotalRequests)
	}
	if summary.InputTokens != 1000 {
		t.Errorf("expected 1000 input tokens, got %d", summary.InputTokens)
	}
	if summary.OutputTokens != 500 {
		t.Errorf("expected 500 output tokens, got %d", summary.OutputTokens)
	}
}

func TestTimeSeries(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	for i := 0; i < 3; i++ {
		ts := time.Date(2026, 6, 28+i, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
		_, err := db.db.Bun().Exec(
			"INSERT INTO request_stats (timestamp, session_id, provider, protocol, model, input_tokens, output_tokens, total_tokens, duration_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
			ts, "sess1", "openai", "openai-chat", "gpt-4", 100*(i+1), 50*(i+1), 150*(i+1), 1000,
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	data, err := db.TimeSeries(Query{GroupBy: "day"})
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 3 {
		t.Errorf("expected 3 data points, got %d", len(data))
	}
}

func TestTimeSeriesOneHour(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	rows := []struct {
		ts          time.Time
		totalTokens int
	}{
		{time.Date(2026, 6, 28, 12, 40, 0, 0, time.UTC), 100},
		{time.Date(2026, 6, 28, 14, 59, 59, 0, time.UTC), 200},
		{time.Date(2026, 6, 28, 15, 0, 0, 0, time.UTC), 300},
	}
	for _, row := range rows {
		_, err := db.db.Bun().Exec(
			"INSERT INTO request_stats (timestamp, session_id, provider, protocol, model, input_tokens, output_tokens, total_tokens, duration_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
			row.ts.Format(time.RFC3339Nano), "sess1", "openai", "openai-chat", "gpt-4", row.totalTokens, 0, row.totalTokens, 1000,
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	data, err := db.TimeSeries(Query{GroupBy: "1h"})
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 3 {
		t.Fatalf("expected 3 data points, got %d", len(data))
	}
	if data[0].Label != "2026-06-28 12:00" || data[0].TotalTokens != 100 {
		t.Errorf("expected first bucket 2026-06-28 12:00 with 100 tokens, got %s with %d", data[0].Label, data[0].TotalTokens)
	}
	if data[1].Label != "2026-06-28 14:00" || data[1].TotalTokens != 200 {
		t.Errorf("expected second bucket 2026-06-28 14:00 with 200 tokens, got %s with %d", data[1].Label, data[1].TotalTokens)
	}
	if data[2].Label != "2026-06-28 15:00" || data[2].TotalTokens != 300 {
		t.Errorf("expected third bucket 2026-06-28 15:00 with 300 tokens, got %s with %d", data[2].Label, data[2].TotalTokens)
	}
}

func TestByProvider(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	now := time.Now().Format(time.RFC3339Nano)
	_, _ = db.db.Bun().Exec("INSERT INTO request_stats (timestamp, provider, protocol, model, input_tokens, output_tokens, total_tokens) VALUES (?, ?, ?, ?, ?, ?, ?)",
		now, "openai", "openai-chat", "gpt-4", 1000, 500, 1500)
	_, _ = db.db.Bun().Exec("INSERT INTO request_stats (timestamp, provider, protocol, model, input_tokens, output_tokens, total_tokens) VALUES (?, ?, ?, ?, ?, ?, ?)",
		now, "anthropic", "anthropic-messages", "claude-3", 2000, 800, 2800)
	_, _ = db.db.Bun().Exec("INSERT INTO request_stats (timestamp, provider, protocol, model, input_tokens, output_tokens, total_tokens) VALUES (?, ?, ?, ?, ?, ?, ?)",
		now, "openai", "openai-chat", "gpt-4", 1200, 600, 1800)

	data, err := db.ByProvider(Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 2 {
		t.Errorf("expected 2 providers, got %d", len(data))
	}
	// openai should be first because its aggregate total is higher.
	if data[0].Vendor != "openai" {
		t.Errorf("expected openai first, got %s", data[0].Vendor)
	}
	if data[0].Protocol != "openai-chat" {
		t.Errorf("expected openai-chat protocol, got %s", data[0].Protocol)
	}
	if data[0].TotalTokens != 3300 {
		t.Errorf("expected openai total 3300, got %d", data[0].TotalTokens)
	}
}

func TestByModel(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	now := time.Now().Format(time.RFC3339Nano)
	_, _ = db.db.Bun().Exec("INSERT INTO request_stats (timestamp, provider, protocol, model, input_tokens, output_tokens, total_tokens) VALUES (?, ?, ?, ?, ?, ?, ?)",
		now, "openai", "openai-chat", "gpt-4", 600, 300, 900)
	_, _ = db.db.Bun().Exec("INSERT INTO request_stats (timestamp, provider, protocol, model, input_tokens, output_tokens, total_tokens) VALUES (?, ?, ?, ?, ?, ?, ?)",
		now, "openai", "openai-chat", "gpt-3.5", 800, 200, 1000)
	_, _ = db.db.Bun().Exec("INSERT INTO request_stats (timestamp, provider, protocol, model, input_tokens, output_tokens, total_tokens) VALUES (?, ?, ?, ?, ?, ?, ?)",
		now, "openai", "openai-chat", "gpt-4", 900, 300, 1200)

	data, err := db.ByModel(Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 2 {
		t.Errorf("expected 2 models, got %d", len(data))
	}
	if data[0].Model != "gpt-4" {
		t.Errorf("expected gpt-4 first, got %s", data[0].Model)
	}
	if data[0].TotalTokens != 2100 {
		t.Errorf("expected gpt-4 total 2100, got %d", data[0].TotalTokens)
	}
}

func TestRecent(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	now := time.Now().Format(time.RFC3339Nano)
	for i := 0; i < 5; i++ {
		_, err := db.db.Bun().Exec("INSERT INTO request_stats (timestamp, provider, protocol, model, input_tokens, output_tokens, total_tokens) VALUES (?, ?, ?, ?, ?, ?, ?)",
			now, "openai", "openai-chat", "gpt-4", 100, 50, 150)
		if err != nil {
			t.Fatal(err)
		}
	}

	page, err := db.Recent(1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 {
		t.Errorf("expected 3 recent entries, got %d", len(page.Items))
	}
	if page.Total != 5 {
		t.Errorf("expected total 5, got %d", page.Total)
	}
	if page.Page != 1 {
		t.Errorf("expected page 1, got %d", page.Page)
	}
	if page.PageSize != 3 {
		t.Errorf("expected pageSize 3, got %d", page.PageSize)
	}
}

func TestRecentFiltered(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()

	_, _ = db.db.Bun().Exec("INSERT INTO request_stats (timestamp, provider, protocol, model, input_tokens, output_tokens, total_tokens) VALUES (?, ?, ?, ?, ?, ?, ?)",
		"2026-07-02T10:00:00Z", "openai", "openai-chat", "gpt-4", 100, 50, 150)
	_, _ = db.db.Bun().Exec("INSERT INTO request_stats (timestamp, provider, protocol, model, input_tokens, output_tokens, total_tokens) VALUES (?, ?, ?, ?, ?, ?, ?)",
		"2026-07-03T10:00:00Z", "moark", "openai-chat", "qwen3.6-plus", 200, 100, 300)

	page, err := db.RecentFiltered(Query{
		From:   time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC),
		To:     time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC),
		Vendor: "moark",
	}, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 {
		t.Fatalf("expected total 1, got %d", page.Total)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(page.Items))
	}
	if page.Items[0].Vendor != "moark" || page.Items[0].Model != "qwen3.6-plus" {
		t.Errorf("unexpected item: %+v", page.Items[0])
	}
}

// TestOpenBacksUpAndRebuildsOldSchema pins the recovery contract for a reader
// such as the stats dashboard: a database whose schema this build cannot upgrade
// is backed up next to itself, rebuilt empty, and the recovery is reported
// instead of blocking the command.
func TestOpenBacksUpAndRebuildsOldSchema(t *testing.T) {
	session.TakeDatabaseRecoveries()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sessions.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE sessions (
		id TEXT PRIMARY KEY,
		cwd TEXT,
		timestamp TEXT,
		parent_session TEXT,
		version INTEGER
	);`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE entries (
		seq INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT REFERENCES sessions(id) ON DELETE CASCADE,
		id TEXT UNIQUE,
		type TEXT NOT NULL,
		parent_id TEXT,
		timestamp TEXT NOT NULL,
		data TEXT NOT NULL
	);`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	sdb, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open after an incompatible legacy schema: %v", err)
	}
	defer sdb.Close()
	defer session.CloseDatabases()

	recoveries := session.TakeDatabaseRecoveries()
	if len(recoveries) != 1 {
		t.Fatalf("recoveries = %#v, want exactly one", recoveries)
	}
	if !strings.Contains(recoveries[0].Err.Error(), "database schema is incompatible") {
		t.Fatalf("recovery error = %v, want the incompatible-schema failure", recoveries[0].Err)
	}

	var count int
	if err := sdb.db.Bun().QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'request_stats'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("rebuilt database is missing the current schema")
	}

	// The previous database is preserved untouched in the backup.
	legacy, err := sql.Open("sqlite", recoveries[0].BackupPath)
	if err != nil {
		t.Fatalf("open backup database: %v", err)
	}
	defer legacy.Close()
	if err := legacy.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'request_stats'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("backup was modified with current schema tables")
	}
}

func TestCurrentSchemaInitializationIsIdempotent(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sessions.db")

	f, err := os.Create(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	db1, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	var count int
	if err := db1.db.Bun().QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'request_stats'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("request_stats count = %d, want 1", count)
	}
	db1.Close()

	db2, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()

	if err := db2.db.Bun().QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("schema_migrations table must be created")
	}
}

func TestOpenUsesSharedSessionConnection(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	shared, err := session.OpenBunDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	statsDB, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer statsDB.Close()
	if statsDB.db != shared {
		t.Fatal("stats database must use the shared session connection")
	}
}
