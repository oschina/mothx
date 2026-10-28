package session

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/util"
)

// DefaultDisplayProjectionBackfillBatch bounds one maintenance pass over
// EventStore sessions that predate the display projection.
const DefaultDisplayProjectionBackfillBatch = 512

// DisplayProjectionBackfillResult reports one bounded display-projection pass.
type DisplayProjectionBackfillResult struct {
	Sessions int
	Skipped  int
	Failed   int
	More     bool
}

// loadDisplayProjections reads the derived list projection for a set of
// sessions. Missing rows are simply absent from the result, which keeps the
// EventStore scan fallback authoritative for unprojected sessions.
func loadDisplayProjections(ctx context.Context, sessionDir string, sessionIDs []string) (map[string]eventStoreDisplayState, error) {
	if len(sessionIDs) == 0 {
		return map[string]eventStoreDisplayState{}, nil
	}
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return nil, err
	}
	records, err := dao.NewSessionDisplayProjectionDAO(db.Bun()).FindMany(ctx, db.Bun(), sessionIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[string]eventStoreDisplayState, len(records))
	for id, record := range records {
		out[id] = eventStoreDisplayState{
			Preview:     record.Preview,
			PreviewSeen: record.PreviewSeen,
			Title:       record.Title,
			Bindings:    decodeDisplayBindings(record.BindingsJSON),
		}
	}
	return out, nil
}

func decodeDisplayBindings(raw string) SessionBindings {
	var bindings SessionBindings
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &bindings)
	}
	return bindings
}

func encodeDisplayBindings(bindings SessionBindings) (string, error) {
	data, err := json.Marshal(bindings)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// applyDisplayEntry merges one canonical entry into a display state. It mirrors
// eventStoreDisplayFromBatches exactly: only the first user message (seen or
// not) supplies the preview, the latest title wins, and the latest binding
// entry of each kind wins.
func applyDisplayEntry(state *eventStoreDisplayState, entry interface{}) {
	switch e := entry.(type) {
	case MessageEntry:
		if state.PreviewSeen || e.Message.Role != "user" {
			return
		}
		state.PreviewSeen = true
		text := displayPreviewText(e.Message)
		if text != "" {
			state.Preview = util.TruncateWithSuffix(text, 60, "...")
		}
	case *MessageEntry:
		if e != nil {
			applyDisplayEntry(state, *e)
		}
	case SessionInfoEntry:
		state.Title = e.Name
	case *SessionInfoEntry:
		if e != nil {
			state.Title = e.Name
		}
	case ModelChangeEntry:
		state.Bindings.ModelChange, state.Bindings.HasModelChange = e, true
	case *ModelChangeEntry:
		if e != nil {
			state.Bindings.ModelChange, state.Bindings.HasModelChange = *e, true
		}
	case ModeChangeEntry:
		state.Bindings.ModeChange, state.Bindings.HasModeChange = e, true
	case *ModeChangeEntry:
		if e != nil {
			state.Bindings.ModeChange, state.Bindings.HasModeChange = *e, true
		}
	case ThinkingLevelChangeEntry:
		state.Bindings.ThinkingLevelChange, state.Bindings.HasThinkingLevelChange = e, true
	case *ThinkingLevelChangeEntry:
		if e != nil {
			state.Bindings.ThinkingLevelChange, state.Bindings.HasThinkingLevelChange = *e, true
		}
	case AdditionalDirectoriesEntry:
		state.Bindings.AdditionalDirectories, state.Bindings.HasAdditionalDirectories = e, true
	case *AdditionalDirectoriesEntry:
		if e != nil {
			state.Bindings.AdditionalDirectories, state.Bindings.HasAdditionalDirectories = *e, true
		}
	}
}

func displayPreviewText(message provider.Message) string {
	if message.Content != "" {
		return message.Content
	}
	for _, block := range message.Contents {
		if block.Type == "text" && block.Text != "" {
			return block.Text
		}
	}
	return ""
}

func displayStateFromEntries(entries []interface{}) eventStoreDisplayState {
	state := eventStoreDisplayState{}
	for _, entry := range entries {
		applyDisplayEntry(&state, entry)
	}
	return state
}

func upsertDisplayProjectionTx(ctx context.Context, tx *dao.Tx, sessionID string, state eventStoreDisplayState) error {
	bindings, err := encodeDisplayBindings(state.Bindings)
	if err != nil {
		return err
	}
	return dao.NewSessionDisplayProjectionDAO(nil).Upsert(ctx, tx, &dao.SessionDisplayProjectionRecord{
		SessionID:    sessionID,
		Preview:      state.Preview,
		PreviewSeen:  state.PreviewSeen,
		Title:        state.Title,
		BindingsJSON: bindings,
		UpdatedAt:    time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// updateDisplayProjectionTx advances an existing projection with the entries of
// one committed batch. A session without a row is left alone: only the full
// backfill can create a complete projection, and a later append must never
// create a row that silently omits earlier title/binding entries.
func updateDisplayProjectionTx(ctx context.Context, tx *dao.Tx, sessionID string, entries []interface{}) error {
	records, err := dao.NewSessionDisplayProjectionDAO(nil).FindMany(ctx, tx, []string{sessionID})
	if err != nil {
		return err
	}
	record, ok := records[sessionID]
	if !ok {
		return nil
	}
	state := eventStoreDisplayState{
		Preview:     record.Preview,
		PreviewSeen: record.PreviewSeen,
		Title:       record.Title,
		Bindings:    decodeDisplayBindings(record.BindingsJSON),
	}
	for _, entry := range entries {
		applyDisplayEntry(&state, entry)
	}
	return upsertDisplayProjectionTx(ctx, tx, sessionID, state)
}

// ensureDisplayProjection creates a session's list projection from the committed
// EventStore log when it is missing. The read happens inside the write
// transaction: an append that wrote bytes before this transaction is visible,
// and one that commits after it updates the row it finds. A session with no
// committed batches is left unprojected so the existing scan fallback remains.
func ensureDisplayProjection(ctx context.Context, sessionDir, sessionID, contentStore string) (bool, error) {
	if contentStore != "event_store" || sessionDir == "" || sessionID == "" {
		return false, nil
	}
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return false, err
	}
	records, err := dao.NewSessionDisplayProjectionDAO(db.Bun()).FindMany(ctx, db.Bun(), []string{sessionID})
	if err != nil {
		return false, err
	}
	if _, ok := records[sessionID]; ok {
		return false, nil
	}
	written := false
	err = WriteRootDatabase(ctx, sessionDir, func(tx *dao.Tx) error {
		records, err := dao.NewSessionDisplayProjectionDAO(nil).FindMany(ctx, tx, []string{sessionID})
		if err != nil {
			return err
		}
		if _, ok := records[sessionID]; ok {
			return nil
		}
		batches, err := ReadCommittedEventBatches(ctx, sessionDir, sessionID)
		if err != nil {
			return err
		}
		if len(batches) == 0 {
			return nil
		}
		if err := upsertDisplayProjectionTx(ctx, tx, sessionID, eventStoreDisplayFromBatches(batches)); err != nil {
			return err
		}
		written = true
		return nil
	})
	return written, err
}

func (m *Manager) ensureDisplayProjection(ctx context.Context) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if m.subAgent || m.header == nil {
		m.mu.Unlock()
		return false, nil
	}
	sessionID := m.header.ID
	sessionDir := m.sessionDirOrDerive()
	eventStoreMode := m.eventStoreMode
	m.mu.Unlock()
	if !eventStoreMode {
		return false, nil
	}
	return ensureDisplayProjection(ctx, sessionDir, sessionID, "event_store")
}

// BackfillSessionDisplayProjections projects EventStore sessions created before
// the display table existed. It never touches legacy sessions (their list rows
// are already indexed SQLite queries) and never modifies transcript content.
func BackfillSessionDisplayProjections(ctx context.Context, sessionDir string, limit int) (DisplayProjectionBackfillResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if sessionDir == "" {
		return DisplayProjectionBackfillResult{}, fmt.Errorf("session directory is required")
	}
	if limit <= 0 {
		limit = DefaultDisplayProjectionBackfillBatch
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return DisplayProjectionBackfillResult{}, err
	}
	ids, err := dao.NewSessionDisplayProjectionDAO(db.Bun()).UnprojectedEventStoreSessionIDs(ctx, db.Bun(), limit)
	if err != nil {
		return DisplayProjectionBackfillResult{}, err
	}
	result := DisplayProjectionBackfillResult{More: len(ids) == limit}
	for _, id := range ids {
		written, err := ensureDisplayProjection(ctx, sessionDir, id, "event_store")
		switch {
		case err != nil:
			result.Failed++
		case written:
			result.Sessions++
		default:
			result.Skipped++
		}
	}
	return result, nil
}
