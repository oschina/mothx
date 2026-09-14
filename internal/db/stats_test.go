package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"expvar"
	"path/filepath"
	"testing"

	"github.com/uptrace/bun"
)

// TestSQLiteStatsPublishedViaExpvar pins the observability contract: the
// process-wide contention counters are published under "mothx_sqlite" as a
// JSON object that the debug server's /debug/vars handler renders, and a real
// committed transaction is reflected in the begin statistics.
func TestSQLiteStatsPublishedViaExpvar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.db")
	migrate := func(sqlDB *sql.DB) error {
		_, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS stats_probe (value TEXT NOT NULL)`)
		return err
	}
	if err := Write(context.Background(), path, migrate, func(_ context.Context, tx bun.Tx) error {
		_, err := tx.Exec(`INSERT INTO stats_probe (value) VALUES ('x')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if count, _, _ := BeginWaitStats(); count == 0 {
		t.Fatal("a committed transaction must record at least one begin attempt")
	}

	published := expvar.Get(sqliteExpvarKey)
	if published == nil {
		t.Fatalf("expvar %q is not published", sqliteExpvarKey)
	}
	var snapshot map[string]int64
	if err := json.Unmarshal([]byte(published.String()), &snapshot); err != nil {
		t.Fatalf("decode snapshot %q: %v", published.String(), err)
	}
	for _, key := range []string{"busyRetryHits", "busyRetryWaitMs", "beginCount", "beginTotalWaitMs", "beginMaxWaitMs"} {
		if _, ok := snapshot[key]; !ok {
			t.Fatalf("snapshot %q is missing %q", published.String(), key)
		}
	}
	if snapshot["beginCount"] < 1 {
		t.Fatalf("beginCount = %d, want at least the transaction this test committed", snapshot["beginCount"])
	}

	if err := CloseAll(); err != nil {
		t.Fatal(err)
	}
}
