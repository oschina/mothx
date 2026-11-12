package session

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/oschina/mothx/internal/dao"
)

// OpenByIDExactTail opens an EventStore session with only its tail transcript
// available. Bindings come from the persisted display projection, and the
// Manager is marked Partial: adapters must use ReadEventStoreTailPage for
// history and Reload before the first prompt/append.
//
// This is a display fast path, not a second lifecycle. The canonical full open
// remains the authority, and any unsupported shape simply returns an error so
// the caller can fall back to it.
func OpenByIDExactTail(sessionDir, sessionID, cwd string, historyLimit int) (*Manager, error) {
	if sessionDir == "" || sessionID == "" || historyLimit <= 0 {
		return nil, ErrTailReplayUnsupported
	}
	if !TailReplaySupported(sessionDir, sessionID) {
		return nil, ErrTailReplayUnsupported
	}
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return nil, ErrTailReplayUnsupported
	}
	record, err := dao.NewSessionDAO(db.Bun()).Header(context.Background(), "sessions", sessionID)
	if err != nil || record == nil {
		return nil, ErrTailReplayUnsupported
	}
	if cwd != "" && filepath.Clean(record.CWD) != filepath.Clean(cwd) {
		return nil, ErrTailReplayUnsupported
	}
	m := &Manager{
		sessionDir:     sessionDir,
		cwd:            record.CWD,
		partial:        true,
		eventStoreMode: true,
	}
	createdAt, _ := time.Parse(time.RFC3339Nano, record.Timestamp)
	if createdAt.IsZero() {
		createdAt, _ = time.Parse(time.RFC3339, record.Timestamp)
	}
	if !createdAt.IsZero() {
		m.file = filepath.Join(sessionDir, fmt.Sprintf("%s_%s.db", createdAt.Format("20060102-150405"), sessionID))
	}
	if m.file == "" {
		m.file = filepath.Join(sessionDir, sessionID+".db")
	}
	m.header = &Header{
		Type: EntrySession, Version: record.Version, ID: sessionID, Timestamp: createdAt,
		Cwd: record.CWD, ParentSession: stringValue(record.ParentSession),
		ChannelType: record.ChannelType, ChannelID: record.ChannelID,
		ForkBoundarySeq: record.ForkBoundarySeq, SeedLength: record.SeedLength,
		ForkKind: record.ForkKind, ExpertID: record.ExpertID,
	}
	var leafID string
	if err := db.Bun().NewSelect().Table("session_event_projections").Column("id").
		Where("session_id = ?", sessionID).OrderExpr("seq DESC").Limit(1).Scan(context.Background(), &leafID); err == nil && leafID != "" {
		m.leafID = &leafID
	}
	bindings, err := LatestSessionBindings(sessionDir, []string{sessionID})
	if err == nil {
		if state, ok := bindings[sessionID]; ok {
			m.bindingOverrides = &state
		}
	}
	return m, nil
}
