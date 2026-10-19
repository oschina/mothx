package session

import (
	"context"
	"fmt"
	"testing"

	"github.com/oschina/mothx/internal/provider"
)

// TestEventStoreAppendHotPathDoesNotRescan pins the append path as O(1) in
// transcript size: once a session EventStore is warm, appending more batches
// must not perform whole-log physical scans. It guards the accidental
// per-append rescan (health inspection, idempotency check, next-sequence
// resolution, commit verification) that made appending quadratic in the
// transcript; see docs/proposal/session-control-plane-event-store-proposal.md.
//
// The assertion is deterministic and does not time anything: the process-local
// FullScans counter must stay flat across the measured appends. The first
// append is excluded because establishing that the physical log is valid at the
// current revision legitimately costs one scan.
func TestEventStoreAppendHotPathDoesNotRescan(t *testing.T) {
	sessionDir := t.TempDir()
	m := New(t.TempDir(), sessionDir)
	if err := m.InitWithID("append-scaling"); err != nil {
		t.Fatal(err)
	}
	if err := m.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}

	appendBatch := func(i int) {
		t.Helper()
		bulk := make([]provider.Message, 8)
		for j := range bulk {
			bulk[j] = provider.NewUserMessage(fmt.Sprintf("m-%d-%d", i, j))
		}
		if _, err := m.AppendMessages(bulk); err != nil {
			t.Fatal(err)
		}
	}

	appendBatch(0)

	before := EventStoreWriteStats().FullScans
	const batches = 16
	for i := 1; i <= batches; i++ {
		appendBatch(i)
	}
	if scanned := EventStoreWriteStats().FullScans - before; scanned > 1 {
		t.Fatalf("appending %d batches to a warm session performed %d whole-log scans, want at most 1", batches, scanned)
	}

	// The transcript must still replay exactly: the fast path must not have
	// skipped any content.
	if got, want := len(m.GetMessages()), (batches+1)*8; got != want {
		t.Fatalf("replayed messages = %d, want %d", got, want)
	}
}
