package session

import (
	"context"
	"fmt"
	"time"

	"github.com/oschina/mothx/internal/dao"
)

// ChildRun is the session-domain view of one durable sub-agent execution.
// External ACP children persist their remote session identity and transcript
// here; local children use the same record so adapters have one history shape.
type ChildRun struct {
	ChildID           string
	SessionID         string
	ParentRunID       string
	Backend           string
	ProfileID         string
	RemoteSessionID   string
	WorkDir           string
	WorktreeID        string
	BaseRevision      string
	ChangeSetJSON     string
	ChangeSetError    string
	IntegrationID     string
	IntegrationDigest string
	IntegrationStatus string
	IntegrationError  string
	IntegratedAt      time.Time
	Status            string
	ContractJSON      string
	TranscriptJSON    string
	// ResolvedCapabilitiesJSON and CapabilityDegradationsJSON are the diagnosed
	// capability projection of an external ACP worker.
	ResolvedCapabilitiesJSON   string
	CapabilityDegradationsJSON string
	MCPBridge                  string
	CreatedAt                  time.Time
	UpdatedAt                  time.Time
}

// SaveChildRun upserts durable child history for an existing session database.
func SaveChildRun(sessionDir string, record ChildRun) error {
	if record.SessionID == "" || record.ChildID == "" {
		return fmt.Errorf("child run requires session and child ids")
	}
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("session database does not exist")
	}
	now := record.UpdatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	created := record.CreatedAt
	if created.IsZero() {
		created = now
	}
	row := &dao.ChildRunRecord{
		ChildID: record.ChildID, SessionID: record.SessionID, ParentRunID: record.ParentRunID,
		Backend: record.Backend, ProfileID: record.ProfileID, RemoteSessionID: record.RemoteSessionID,
		WorkDir: record.WorkDir, WorktreeID: record.WorktreeID, BaseRevision: record.BaseRevision,
		ChangeSetJSON: record.ChangeSetJSON, ChangeSetError: record.ChangeSetError,
		IntegrationID: record.IntegrationID, IntegrationDigest: record.IntegrationDigest,
		IntegrationStatus: record.IntegrationStatus, IntegrationError: record.IntegrationError,
		IntegratedAt: formatChildRunTime(record.IntegratedAt),
		Status:       record.Status, ContractJSON: record.ContractJSON,
		TranscriptJSON:             record.TranscriptJSON,
		ResolvedCapabilitiesJSON:   record.ResolvedCapabilitiesJSON,
		CapabilityDegradationsJSON: record.CapabilityDegradationsJSON,
		MCPBridge:                  record.MCPBridge,
		CreatedAt:                  created.UTC().Format(time.RFC3339Nano),
		UpdatedAt:                  now.UTC().Format(time.RFC3339Nano),
	}
	return dao.NewChildRunDAO(db.Bun()).Upsert(context.Background(), row)
}

// LoadChildRun returns one durable child record. found is false when no row
// exists; a missing session database is not an error for read-only projections.
func LoadChildRun(sessionDir, sessionID, childID string) (*ChildRun, bool, error) {
	if sessionID == "" || childID == "" {
		return nil, false, nil
	}
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return nil, false, err
	}
	row, err := dao.NewChildRunDAO(db.Bun()).Get(context.Background(), sessionID, childID)
	if err != nil || row == nil {
		return nil, false, err
	}
	return childRunFromRecord(row), true, nil
}

// ListChildRuns returns durable child history for one session, newest first.
func ListChildRuns(sessionDir, sessionID string) ([]ChildRun, error) {
	if sessionID == "" {
		return nil, nil
	}
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return nil, err
	}
	rows, err := dao.NewChildRunDAO(db.Bun()).List(context.Background(), sessionID)
	if err != nil {
		return nil, err
	}
	records := make([]ChildRun, 0, len(rows))
	for i := range rows {
		records = append(records, *childRunFromRecord(&rows[i]))
	}
	return records, nil
}

func childRunFromRecord(row *dao.ChildRunRecord) *ChildRun {
	created, _ := time.Parse(time.RFC3339Nano, row.CreatedAt)
	updated, _ := time.Parse(time.RFC3339Nano, row.UpdatedAt)
	integrated, _ := time.Parse(time.RFC3339Nano, row.IntegratedAt)
	return &ChildRun{
		ChildID: row.ChildID, SessionID: row.SessionID, ParentRunID: row.ParentRunID,
		Backend: row.Backend, ProfileID: row.ProfileID, RemoteSessionID: row.RemoteSessionID,
		WorkDir: row.WorkDir, WorktreeID: row.WorktreeID, BaseRevision: row.BaseRevision,
		ChangeSetJSON: row.ChangeSetJSON, ChangeSetError: row.ChangeSetError,
		IntegrationID: row.IntegrationID, IntegrationDigest: row.IntegrationDigest,
		IntegrationStatus: row.IntegrationStatus, IntegrationError: row.IntegrationError,
		IntegratedAt: integrated,
		Status:       row.Status, ContractJSON: row.ContractJSON,
		TranscriptJSON:             row.TranscriptJSON,
		ResolvedCapabilitiesJSON:   row.ResolvedCapabilitiesJSON,
		CapabilityDegradationsJSON: row.CapabilityDegradationsJSON, MCPBridge: row.MCPBridge,
		CreatedAt: created, UpdatedAt: updated,
	}
}

// MarkChildRunIntegration records the latest integration outcome for selected
// child snapshots. The Runtime calls this only after it has applied or rejected
// the immutable snapshot; a missing child row is a caller error.
func MarkChildRunIntegration(sessionDir, sessionID string, childIDs []string, integrationID, digest, status, message string, at time.Time) error {
	if sessionID == "" || len(childIDs) == 0 {
		return fmt.Errorf("child integration requires session and child ids")
	}
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("session database does not exist")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return dao.NewChildRunDAO(db.Bun()).MarkIntegration(
		context.Background(), sessionID, childIDs, integrationID, digest, status, message, at.UTC().Format(time.RFC3339Nano),
	)
}

func formatChildRunTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
