package session

import (
	"context"
	"fmt"
	"testing"

	"github.com/oschina/mothx/internal/provider"
)

// TestMigrateToEventStoreWritesBoundedBatches pins that the one-time export is
// chunked at the same cap as runtime appends. A large transcript must never
// become a single unbounded batch (one giant fsync plus one unbounded SQLite
// projection transaction that holds the shared write lock for the whole
// transcript).
func TestMigrateToEventStoreWritesBoundedBatches(t *testing.T) {
	sessionDir := t.TempDir()
	m := New("/tmp/migration-bounded", sessionDir)
	if err := m.InitWithID("bounded-export"); err != nil {
		t.Fatal(err)
	}
	const messages = maxEventStoreEntriesPerBatch*3 + 7
	for start := 0; start < messages; start += maxEventStoreEntriesPerBatch {
		end := min(start+maxEventStoreEntriesPerBatch, messages)
		bulk := make([]provider.Message, 0, end-start)
		for i := start; i < end; i++ {
			bulk = append(bulk, provider.NewUserMessage(fmt.Sprintf("m-%d", i)))
		}
		if _, err := m.AppendMessages(bulk); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}

	batches, err := ReadCommittedEventBatches(context.Background(), sessionDir, "bounded-export")
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) < 2 {
		t.Fatalf("export produced %d batch(es), want it chunked into several", len(batches))
	}
	exported := 0
	for _, batch := range batches {
		if n := len(batch.Events); n > maxEventStoreEntriesPerBatch {
			t.Fatalf("batch %s has %d events, want <= %d", batch.BatchID, n, maxEventStoreEntriesPerBatch)
		}
		exported += len(batch.Events)
	}
	if want := (exported + maxEventStoreEntriesPerBatch - 1) / maxEventStoreEntriesPerBatch; len(batches) != want {
		t.Fatalf("exported %d events in %d batches, want %d batch(es)", exported, len(batches), want)
	}
	if got := len(m.GetMessages()); got != messages {
		t.Fatalf("replayed messages = %d, want %d", got, messages)
	}
}
