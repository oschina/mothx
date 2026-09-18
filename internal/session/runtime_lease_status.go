package session

import (
	"context"
	"fmt"
	"time"

	"github.com/startvibecoding/mothx/internal/dao"
)

// ActiveRuntimeLease is one session lease still recorded as active in a session
// database. It names the process that holds it, so destructive maintenance can
// refuse while another mothx process may still be executing a run there.
type ActiveRuntimeLease struct {
	SessionID string
	OwnerID   string
	OwnerPID  int
	OwnerKind string
	RunID     string
	Purpose   string
	ExpiresAt time.Time
}

// Describe renders the holder in one line for an operator-facing refusal.
func (lease ActiveRuntimeLease) Describe() string {
	description := fmt.Sprintf("session %s holds an active %s lease owned by %s (pid %d)", lease.SessionID, lease.Purpose, lease.OwnerKind, lease.OwnerPID)
	if lease.RunID != "" {
		description += fmt.Sprintf(", run %s", lease.RunID)
	}
	if remaining := time.Until(lease.ExpiresAt); remaining > 0 {
		description += fmt.Sprintf(", still renewing (expires in %s)", remaining.Round(time.Second))
	}
	return description
}

// ActiveRuntimeLeases reports leases still marked active in a session
// directory's database. A directory with no database holds nothing and is not
// an error, so this is safe to call before the first run or after a reset.
//
// The check is deliberately narrow: a lease exists only while a process is
// admitted or executing a run, so an idle TUI, serve, or ACP process holding the
// same directory open is not reported here. It opens the file read-only and
// never migrates, repairs, or initializes it: a safety preflight cannot modify
// the database it is deciding whether to archive.
func ActiveRuntimeLeases(sessionDir string) ([]ActiveRuntimeLease, error) {
	db, ok, err := openExistingSessionDBReadOnly(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("inspect session leases: %w", err)
	}
	if !ok {
		return nil, nil
	}
	defer db.Close()
	records, err := dao.NewRuntimeLeaseDAO(db.Bun()).ListHeld(context.Background(), db.Bun())
	if err != nil {
		return nil, fmt.Errorf("list active session leases: %w", err)
	}
	leases := make([]ActiveRuntimeLease, 0, len(records))
	for _, record := range records {
		leases = append(leases, ActiveRuntimeLease{
			SessionID: record.SessionID,
			OwnerID:   record.OwnerID,
			OwnerPID:  record.OwnerPID,
			OwnerKind: record.OwnerKind,
			RunID:     record.RunID,
			Purpose:   record.Purpose,
			ExpiresAt: time.Unix(record.ExpiresAt, 0),
		})
	}
	return leases, nil
}
