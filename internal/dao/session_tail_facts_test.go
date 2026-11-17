package dao_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/session"
)

// TestSessionEventTailFactsDAOs pins the read-only projections the display tail
// path needs. They used to be Bun queries inside internal/session, which the
// architecture guard rejects; this test keeps the DAO owners honest.
func TestSessionEventTailFactsDAOs(t *testing.T) {
	root := t.TempDir()
	database, err := session.OpenBunDatabase(filepath.Join(root, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.CloseDatabases() })
	ctx := context.Background()
	executor := database.Bun()

	sessions := dao.NewSessionDAO(executor)
	if err := sessions.InsertSession(ctx, executor, "sessions", "sess-tail", root, "2026-01-01T00:00:00Z", "", 1, "", "", 0, 0, "", ""); err != nil {
		t.Fatal(err)
	}
	store, err := sessions.ContentStore(ctx, executor, "sess-tail")
	if err != nil {
		t.Fatal(err)
	}
	if store != "sqlite" {
		t.Fatalf("default content store = %q, want sqlite", store)
	}
	if err := sessions.SetContentStore(ctx, executor, "sess-tail", "event_store"); err != nil {
		t.Fatal(err)
	}
	if store, err = sessions.ContentStore(ctx, executor, "sess-tail"); err != nil || store != "event_store" {
		t.Fatalf("content store = %q err=%v, want event_store", store, err)
	}

	projections := dao.NewSessionEventProjectionDAO(executor)
	records := []dao.SessionEventProjectionRecord{
		{ID: "e1", SessionID: "sess-tail", Seq: 1, Type: "message", Timestamp: "2026-01-01T00:00:01Z"},
		{ID: "e2", SessionID: "sess-tail", Seq: 2, Type: "message", Timestamp: "2026-01-01T00:00:02Z"},
		{ID: "e3", SessionID: "sess-tail", Seq: 3, Type: "compaction", Timestamp: "2026-01-01T00:00:03Z"},
		{ID: "e4", SessionID: "other-session", Seq: 9, Type: "message", Timestamp: "2026-01-01T00:00:04Z"},
	}
	if err := projections.InsertBatch(ctx, executor, records); err != nil {
		t.Fatal(err)
	}
	if got, err := projections.CountByTypes(ctx, executor, "sess-tail", []string{"message"}); err != nil || got != 2 {
		t.Fatalf("message count = %d err=%v, want 2", got, err)
	}
	if got, err := projections.CountByTypes(ctx, executor, "sess-tail", []string{"compaction", "content_override"}); err != nil || got != 1 {
		t.Fatalf("sensitive count = %d err=%v, want 1", got, err)
	}
	if got, err := projections.CountByTypes(ctx, executor, "sess-tail", nil); err != nil || got != 0 {
		t.Fatalf("empty-type count = %d err=%v, want 0", got, err)
	}
	if got, err := projections.MaxSeq(ctx, executor, "sess-tail"); err != nil || got != 3 {
		t.Fatalf("max seq = %d err=%v, want 3", got, err)
	}
	if got, err := projections.MaxSeq(ctx, executor, "absent"); err != nil || got != 0 {
		t.Fatalf("max seq for absent session = %d err=%v, want 0", got, err)
	}
	if got, err := projections.LeafID(ctx, executor, "sess-tail"); err != nil || got != "e3" {
		t.Fatalf("leaf id = %q err=%v, want e3", got, err)
	}

	intents := dao.NewSessionEventIntentDAO(executor)
	for _, record := range []*dao.SessionEventIntentRecord{
		{ID: "i1", SessionID: "sess-tail", SeqFrom: 1, SeqTo: 3, State: "committed", CreatedAt: 1, UpdatedAt: 1},
		{ID: "i2", SessionID: "sess-tail", SeqFrom: 4, SeqTo: 7, State: "pending", CreatedAt: 2, UpdatedAt: 2},
	} {
		if err := intents.Insert(ctx, executor, record); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := intents.MaxSeqToByState(ctx, executor, "sess-tail", "committed"); err != nil || got != 3 {
		t.Fatalf("max committed seq_to = %d err=%v, want 3", got, err)
	}
	if got, err := intents.MaxSeqToByState(ctx, executor, "sess-tail", "pending"); err != nil || got != 7 {
		t.Fatalf("max pending seq_to = %d err=%v, want 7", got, err)
	}
	ids, err := intents.IDsByState(ctx, executor, "sess-tail", "committed")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "i1" {
		t.Fatalf("committed ids = %#v, want [i1]", ids)
	}

	display := dao.NewSessionDisplayProjectionDAO(executor)
	if has, err := display.HasProjection(ctx, executor, "sess-tail"); err != nil || has {
		t.Fatalf("has projection = %v err=%v, want false before the row exists", has, err)
	}
	if err := display.Upsert(ctx, executor, &dao.SessionDisplayProjectionRecord{SessionID: "sess-tail", Title: "tail", UpdatedAt: "2026-01-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if has, err := display.HasProjection(ctx, executor, "sess-tail"); err != nil || !has {
		t.Fatalf("has projection = %v err=%v, want true", has, err)
	}
}
