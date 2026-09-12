package dao_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/dao"
	"github.com/startvibecoding/mothx/internal/session"
)

// TestRuntimeLeaseRenewKeepsOwnExpiredRow guards the heartbeat fencing rules:
// an expired row that still carries the owner's identity stays renewable (a
// stalled owner must not be killed by its own TTL), while a displaced owner
// (epoch bumped by Acquire) can never renew again.
func TestRuntimeLeaseRenewKeepsOwnExpiredRow(t *testing.T) {
	root := t.TempDir()
	database, err := session.OpenBunDatabase(filepath.Join(root, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.CloseDatabases() })

	leaseDAO := dao.NewRuntimeLeaseDAO(database.Bun())
	ctx := context.Background()
	past := time.Now().Add(-time.Minute).Unix()

	// A stalled but still-live owner: the row expired without a takeover.
	stalled := &dao.RuntimeLeaseRecord{
		SessionID: "session-stalled", OwnerID: "owner-a", OwnerPID: 1, OwnerKind: "process",
		TokenHash: "token-a", Epoch: 1, Purpose: "execution", State: "active",
		AcquiredAt: past, HeartbeatAt: past, ExpiresAt: past, UpdatedAt: past,
	}
	if err := leaseDAO.Insert(ctx, database.Bun(), stalled); err != nil {
		t.Fatal(err)
	}
	renewed, err := leaseDAO.Renew(ctx, &dao.RuntimeLeaseRecord{
		SessionID: "session-stalled", OwnerID: "owner-a", Epoch: 1, TokenHash: "token-a",
	}, int64((15*time.Second)/time.Second))
	if err != nil {
		t.Fatalf("Renew of own expired lease: %v", err)
	}
	if renewed != 1 {
		t.Fatalf("Renew of own expired lease = %d rows, want 1 (the owner lost its live lease without a takeover)", renewed)
	}

	// A real takeover: another process takes the expired row (CAS on epoch 1).
	victim := &dao.RuntimeLeaseRecord{
		SessionID: "session-takeover", OwnerID: "owner-a", OwnerPID: 1, OwnerKind: "process",
		TokenHash: "token-a", Epoch: 1, Purpose: "execution", State: "active",
		AcquiredAt: past, HeartbeatAt: past, ExpiresAt: past, UpdatedAt: past,
	}
	if err := leaseDAO.Insert(ctx, database.Bun(), victim); err != nil {
		t.Fatal(err)
	}
	taken, err := leaseDAO.Acquire(ctx, database.Bun(), &dao.RuntimeLeaseRecord{
		SessionID: "session-takeover", OwnerID: "owner-b", OwnerPID: 2, OwnerKind: "process",
		TokenHash: "token-b", Epoch: 2, Purpose: "recovery", ExpiresAt: time.Now().Add(time.Minute).Unix(),
	}, 1, time.Now().Unix())
	if err != nil {
		t.Fatalf("Acquire takeover: %v", err)
	}
	if taken != 1 {
		t.Fatalf("Acquire takeover = %d rows, want 1", taken)
	}
	renewed, err = leaseDAO.Renew(ctx, &dao.RuntimeLeaseRecord{
		SessionID: "session-takeover", OwnerID: "owner-a", Epoch: 1, TokenHash: "token-a",
	}, int64((15*time.Second)/time.Second))
	if err != nil {
		t.Fatalf("Renew from a displaced owner: %v", err)
	}
	if renewed != 0 {
		t.Fatalf("Renew from a displaced owner = %d rows, want 0 (fencing must hold)", renewed)
	}
}
