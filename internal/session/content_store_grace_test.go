package session

import (
	"context"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/provider"
)

func storeMetaCounterForTest(t *testing.T, sessionDir, key string) int64 {
	t.Helper()
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	value, err := dao.NewSessionStoreMetaDAO(nil).Counter(context.Background(), db.Bun(), key)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// TestRetireWaitsForPostFlipGrace pins the mixed-build safety window: right after
// a session flips, the covered legacy duplicate is kept, because a build that
// still writes the legacy transcript may be appending to it (deleting its rows
// would break that writer's leaf chain). Once the window has elapsed the next
// attach retires the covered rows and records it.
func TestRetireWaitsForPostFlipGrace(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "retire-grace"
	splitBefore := SessionStoreSplitDetections()
	manager := New("/tmp/event-retire-grace", sessionDir)
	if err := manager.InitWithID(sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.NewUserMessage("legacy")); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.MigrateToEventStoreOutcome(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Inside the window: the export folded the transcript into the log but kept the
	// duplicate.
	inside := legacyTranscriptPayloadCount(t, sessionDir, sessionID)
	if inside == 0 {
		t.Fatal("migration retired the covered duplicate inside the post-flip grace window")
	}
	if got := storeMetaCounterForTest(t, sessionDir, dao.SessionStoreContentLegacyRowsRetiredKey); got != 0 {
		t.Fatalf("retired-row counter inside the grace window = %d, want 0", got)
	}
	// A converged attach inside the window is a no-op, not an error.
	if _, err := manager.MigrateToEventStoreOutcome(context.Background()); err != nil {
		t.Fatalf("attach inside the grace window = %v, want a tolerated no-op", err)
	}
	if got := legacyTranscriptPayloadCount(t, sessionDir, sessionID); got != inside {
		t.Fatalf("attach inside the grace window changed legacy rows: %d -> %d", inside, got)
	}

	// After the window the fenced attach cleanup runs.
	elapseContentStoreRetireGraceForTest(t, sessionDir, sessionID)
	if _, err := manager.MigrateToEventStoreOutcome(context.Background()); err != nil {
		t.Fatalf("attach after the grace window = %v", err)
	}
	if got := legacyTranscriptPayloadCount(t, sessionDir, sessionID); got != 0 {
		t.Fatalf("attach after the grace window left %d legacy rows, want 0", got)
	}
	if got := storeMetaCounterForTest(t, sessionDir, dao.SessionStoreContentLegacyRowsRetiredKey); got != int64(inside) {
		t.Fatalf("retired-row counter = %d, want %d", got, inside)
	}
	// Retirement is not a split: covered rows disappear without a warning. The
	// counter is process-wide, so only the delta of this test is meaningful.
	if got := SessionStoreSplitDetections(); got != splitBefore {
		t.Fatalf("split detections rose from %d to %d for a covered duplicate", splitBefore, got)
	}
}

// TestContentStoreCountersRecordMigrationOutcomes keeps the durable diagnostic
// counters honest: adoptions and deferrals are both recorded, so an operator can
// tell "nothing needed migrating" from "migration kept being blocked".
func TestContentStoreCountersRecordMigrationOutcomes(t *testing.T) {
	t.Run("adopted", func(t *testing.T) {
		sessionDir := t.TempDir()
		manager := New("/tmp/event-counter-adopted", sessionDir)
		if err := manager.InitWithID("counter-adopted"); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.AppendMessage(provider.NewUserMessage("legacy")); err != nil {
			t.Fatal(err)
		}
		if outcome, err := manager.MigrateToEventStoreOutcome(context.Background()); err != nil {
			t.Fatal(err)
		} else if outcome != MigrationAdopted {
			t.Fatalf("outcome = %q, want %q", outcome, MigrationAdopted)
		}
		if got := storeMetaCounterForTest(t, sessionDir, dao.SessionStoreContentMigrationAdoptedKey); got != 1 {
			t.Fatalf("adopted counter = %d, want 1", got)
		}
		if got := storeMetaCounterForTest(t, sessionDir, dao.SessionStoreContentMigrationDeferredKey); got != 0 {
			t.Fatalf("deferred counter = %d, want 0", got)
		}
	})

	t.Run("deferred", func(t *testing.T) {
		sessionDir := t.TempDir()
		const sessionID = "counter-deferred"
		manager := New("/tmp/event-counter-deferred", sessionDir)
		if err := manager.InitWithID(sessionID); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.AppendMessage(provider.NewUserMessage("legacy")); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		if err := SaveSessionRun(sessionDir, SessionRun{
			ID: "run-elsewhere", SessionID: sessionID, Status: "running", StartedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		if outcome, err := manager.MigrateToEventStoreOutcome(context.Background()); err != nil {
			t.Fatal(err)
		} else if outcome != MigrationDeferred {
			t.Fatalf("outcome = %q, want %q", outcome, MigrationDeferred)
		}
		if got := storeMetaCounterForTest(t, sessionDir, dao.SessionStoreContentMigrationDeferredKey); got != 1 {
			t.Fatalf("deferred counter = %d, want 1", got)
		}
		if got := storeMetaCounterForTest(t, sessionDir, dao.SessionStoreContentMigrationAdoptedKey); got != 0 {
			t.Fatalf("adopted counter = %d, want 0", got)
		}
	})
}
