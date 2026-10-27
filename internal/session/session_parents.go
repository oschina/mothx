package session

import (
	"context"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/platform"
)

// SessionParent is one row of the lightweight fork-lineage projection. The
// slice order is the canonical session-list order (last active first), so a
// caller that walks the child graph produces the same traversal order without
// loading previews, titles, or transcripts.
type SessionParent struct {
	ID            string
	ParentSession string
}

// ListSessionParents returns the id → parent_session relationship for every
// persisted session. It is a read-only metadata projection: no session is
// opened, no transcript is replayed, and no write transaction is taken.
func ListSessionParents(sessionDir string) ([]SessionParent, error) {
	if sessionDir == "" {
		sessionDir = platform.SessionDir()
	}
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return nil, err
	}
	records, err := dao.NewSessionDAO(db.Bun()).ListParents(context.Background())
	if err != nil {
		return nil, err
	}
	parents := make([]SessionParent, len(records))
	for i, record := range records {
		parents[i] = SessionParent{ID: record.ID, ParentSession: stringValue(record.ParentSession)}
	}
	return parents, nil
}
