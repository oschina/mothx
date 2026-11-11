package dao

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/uptrace/bun"
)

// ChildRunRecord is the durable projection of one sub-agent execution,
// including external ACP backend identity and its retained transcript. It is a
// child history record, not a replacement for canonical parent Run rows.
type ChildRunRecord struct {
	bun.BaseModel     `bun:"table:session_child_runs"`
	ChildID           string `bun:"child_id,pk"`
	SessionID         string `bun:"session_id"`
	ParentRunID       string `bun:"parent_run_id"`
	Backend           string `bun:"backend"`
	ProfileID         string `bun:"profile_id"`
	RemoteSessionID   string `bun:"remote_session_id"`
	WorkDir           string `bun:"work_dir"`
	WorktreeID        string `bun:"worktree_id"`
	BaseRevision      string `bun:"base_revision"`
	ChangeSetJSON     string `bun:"change_set_json"`
	ChangeSetError    string `bun:"change_set_error"`
	IntegrationID     string `bun:"integration_id"`
	IntegrationDigest string `bun:"integration_digest"`
	IntegrationStatus string `bun:"integration_status"`
	IntegrationError  string `bun:"integration_error"`
	IntegratedAt      string `bun:"integrated_at"`
	Status            string `bun:"status"`
	ContractJSON      string `bun:"contract_json"`
	TranscriptJSON    string `bun:"transcript_json"`
	// ResolvedCapabilitiesJSON and CapabilityDegradationsJSON carry the
	// negotiated worker capability projection; MCPBridge is its compact state.
	ResolvedCapabilitiesJSON   string `bun:"resolved_capabilities_json"`
	CapabilityDegradationsJSON string `bun:"capability_degradations_json"`
	MCPBridge                  string `bun:"mcp_bridge"`
	CreatedAt                  string `bun:"created_at"`
	UpdatedAt                  string `bun:"updated_at"`
}

// ChildRunDAO owns durable child history persistence.
type ChildRunDAO struct {
	db *bun.DB
}

func NewChildRunDAO(db *bun.DB) *ChildRunDAO { return &ChildRunDAO{db: db} }

func (d *ChildRunDAO) Upsert(ctx context.Context, record *ChildRunRecord) error {
	if record == nil || record.ChildID == "" || record.SessionID == "" {
		return fmt.Errorf("child run requires child and session ids")
	}
	_, err := d.db.NewInsert().Model(record).
		On(`CONFLICT(child_id) DO UPDATE SET
			session_id = excluded.session_id,
			parent_run_id = excluded.parent_run_id,
			backend = excluded.backend,
			profile_id = excluded.profile_id,
			remote_session_id = excluded.remote_session_id,
			work_dir = excluded.work_dir,
			worktree_id = excluded.worktree_id,
			base_revision = excluded.base_revision,
			change_set_json = excluded.change_set_json,
			change_set_error = excluded.change_set_error,
			integration_id = CASE WHEN excluded.integration_id <> '' THEN excluded.integration_id ELSE integration_id END,
			integration_digest = CASE WHEN excluded.integration_digest <> '' THEN excluded.integration_digest ELSE integration_digest END,
			integration_status = CASE WHEN excluded.integration_status <> '' THEN excluded.integration_status ELSE integration_status END,
			integration_error = CASE WHEN excluded.integration_error <> '' THEN excluded.integration_error ELSE integration_error END,
			integrated_at = CASE WHEN excluded.integrated_at <> '' THEN excluded.integrated_at ELSE integrated_at END,
			status = excluded.status,
			contract_json = excluded.contract_json,
			transcript_json = excluded.transcript_json,
			resolved_capabilities_json = excluded.resolved_capabilities_json,
			capability_degradations_json = excluded.capability_degradations_json,
			mcp_bridge = excluded.mcp_bridge,
			updated_at = excluded.updated_at`).
		Exec(ctx)
	return err
}

// MarkIntegration records the latest Runtime-owned integration outcome for the
// selected child snapshots. It is a separate write so a later child follow-up
// cannot erase the evidence recorded here.
func (d *ChildRunDAO) MarkIntegration(ctx context.Context, sessionID string, childIDs []string, integrationID, digest, status, message, at string) error {
	if sessionID == "" || len(childIDs) == 0 {
		return fmt.Errorf("child integration requires session and child ids")
	}
	_, err := d.db.NewUpdate().Model((*ChildRunRecord)(nil)).
		Set("integration_id = ?", integrationID).
		Set("integration_digest = ?", digest).
		Set("integration_status = ?", status).
		Set("integration_error = ?", message).
		Set("integrated_at = ?", at).
		Where("session_id = ? AND child_id IN (?)", sessionID, bun.In(childIDs)).
		Exec(ctx)
	return err
}

func (d *ChildRunDAO) Get(ctx context.Context, sessionID, childID string) (*ChildRunRecord, error) {
	record := new(ChildRunRecord)
	err := d.db.NewSelect().Model(record).
		Where("session_id = ? AND child_id = ?", sessionID, childID).Limit(1).Scan(ctx, record)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return record, nil
}

func (d *ChildRunDAO) List(ctx context.Context, sessionID string) ([]ChildRunRecord, error) {
	var records []ChildRunRecord
	err := d.db.NewSelect().Model(&records).
		Where("session_id = ?", sessionID).OrderExpr("updated_at DESC").Scan(ctx, &records)
	return records, err
}

func (d *ChildRunDAO) Delete(ctx context.Context, sessionID, childID string) error {
	_, err := d.db.NewDelete().Model((*ChildRunRecord)(nil)).
		Where("session_id = ? AND child_id = ?", sessionID, childID).Exec(ctx)
	return err
}
