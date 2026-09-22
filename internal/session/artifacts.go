package session

import (
	"context"
	"time"

	"github.com/oschina/mothx/internal/dao"
)

// GeneratedArtifact is the canonical read-only projection of one persisted
// session attachment row. Despite the artifact-oriented name it projects any
// attachment origin/status; the bytes themselves stay in the Runtime-owned
// private store referenced by the attachment row and are never embedded here.
type GeneratedArtifact struct {
	ID        string
	SessionID string
	RunID     string
	Origin    string
	Kind      string
	Filename  string
	MediaType string
	Bytes     int64
	Status    string
	CreatedAt time.Time
}

// ListGeneratedArtifacts returns every attachment persisted for sessionID
// whose lifecycle status is "generated", in durable creation order. Adapters
// use it for replay projections (for example ACP session/load); content access
// stays with the Runtime-owned attachment service and all SQL stays in the DAO.
func ListGeneratedArtifacts(ctx context.Context, sessionDir, sessionID string) ([]GeneratedArtifact, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if sessionID == "" {
		return nil, nil
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	records, err := dao.NewAttachmentDAO(db.Bun()).ListBySessionStatus(ctx, sessionID, "generated")
	if err != nil {
		return nil, err
	}
	artifacts := make([]GeneratedArtifact, 0, len(records))
	for _, record := range records {
		artifacts = append(artifacts, generatedArtifactFromRecord(record))
	}
	return artifacts, nil
}

// ListSessionAttachments returns metadata-only projections of the persisted
// attachment rows of one session, optionally filtered by lifecycle status (an
// empty status returns every row), in durable creation order. Adapters use it
// for listing surfaces (for example ACP mothx/attachment/list); content access
// stays with the Runtime-owned attachment service and all SQL stays in the DAO.
func ListSessionAttachments(ctx context.Context, sessionDir, sessionID, status string) ([]GeneratedArtifact, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if sessionID == "" {
		return nil, nil
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	records, err := dao.NewAttachmentDAO(db.Bun()).ListBySession(ctx, sessionID, status)
	if err != nil {
		return nil, err
	}
	artifacts := make([]GeneratedArtifact, 0, len(records))
	for _, record := range records {
		artifacts = append(artifacts, generatedArtifactFromRecord(record))
	}
	return artifacts, nil
}

func generatedArtifactFromRecord(record dao.AttachmentRecord) GeneratedArtifact {
	return GeneratedArtifact{
		ID: record.ID, SessionID: record.SessionID, RunID: record.RunID, Origin: record.Origin,
		Kind: record.Kind, Filename: record.Filename, MediaType: record.MediaType,
		Bytes: record.Bytes, Status: record.Status, CreatedAt: parseSessionTimestamp(record.CreatedAt),
	}
}
