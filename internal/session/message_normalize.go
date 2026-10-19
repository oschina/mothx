package session

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/provider"
)

// normalizeSessionEntry repairs provider messages at the persistence boundary.
// This is intentionally a last line of defense: callers should normalize as
// soon as a streamed tool call is received, but Runtime-owned durable writes
// must remain safe even when a message arrived through another adapter.
func normalizeSessionEntry(entry any) any {
	switch value := entry.(type) {
	case MessageEntry:
		value.Message, _ = provider.NormalizeMessage(value.Message)
		return value
	case *MessageEntry:
		if value == nil {
			return entry
		}
		copyValue := *value
		copyValue.Message, _ = provider.NormalizeMessage(copyValue.Message)
		return &copyValue
	case ContentOverrideEntry:
		value.Message, _ = provider.NormalizeMessage(value.Message)
		return value
	case *ContentOverrideEntry:
		if value == nil {
			return entry
		}
		copyValue := *value
		copyValue.Message, _ = provider.NormalizeMessage(copyValue.Message)
		return &copyValue
	default:
		return entry
	}
}

// marshalSessionEntry retries a failed JSON-v2 marshal after applying the
// message repair above. The first marshal is deliberate: it preserves the
// original error for diagnostics and keeps this fallback focused on malformed
// streamed payloads rather than masking unrelated schema bugs.
func marshalSessionEntry(entry any) ([]byte, error) {
	data, err := json.Marshal(entry)
	if err == nil {
		return data, nil
	}

	repaired := normalizeSessionEntry(entry)
	if retryData, retryErr := json.Marshal(repaired); retryErr == nil {
		log.Printf("[session] repaired an invalid message payload after marshal failure: %v", err)
		return retryData, nil
	}
	return nil, fmt.Errorf("marshal entry: %w", err)
}

// sessionEntryIdentity is the persisted identity triple of a Session entry.
// Callers that map transcript positions across stores need exactly this: the
// row's sequence number, its entry ID, and its entry type.
type sessionEntryIdentity struct {
	seq  int64
	id   string
	typ  EntryType
	self any
}

// orderedSessionEntries reconstructs the persisted order and persisted identity
// of this session's transcript.
//
// The legacy `entries` table numbers rows with a database-global AUTOINCREMENT,
// so `seq` interleaves every session sharing `sessions.db` and cannot be
// compared against another session's or another store's numbering. What *is*
// durable is the parent chain: each entry stores the ID of the entry it was
// appended after. Rebuilding the chain therefore recovers the exact persisted
// order, and the position of an entry in the result equals the ordinal it must
// receive in a session-scoped EventStore.
//
// This exists because the in-memory view is not always the persisted order:
// turn boundary helpers write their `turn_start`/`turn_end` row straight to the
// database without touching `m.entries`, so a Manager assembled by a direct
// Session caller holds an incomplete view of its own transcript.
// The `seq` carried here is the legacy database sequence, not a derived
// ordinal: migrating a transcript has to translate the `conversation_turns`
// bounds that were written against exactly that number.
func (m *Manager) orderedSessionEntries(ctx context.Context) ([]sessionEntryIdentity, error) {
	sessionDir := m.sessionDir
	if sessionDir == "" {
		sessionDir = filepath.Dir(resolveDBPath(m.file))
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	rows, err := dao.NewSessionDAO(db.Bun()).TranscriptRows(ctx, m.entriesTable(), m.header.ID)
	if err != nil {
		return nil, err
	}
	inPool := make(map[string]sessionEntryIdentity, len(rows))
	pool := make([]sessionEntryIdentity, 0, len(rows))
	for _, row := range rows {
		if row.ID == "" || row.Type == "" || row.Type == string(EntrySession) {
			continue
		}
		if _, duplicate := inPool[row.ID]; duplicate {
			continue
		}
		// Decode through the shared event decoder so an unsupported legacy row
		// type refuses the migration outright instead of silently vanishing from
		// the exported transcript.
		decoded, err := DecodeEventRecord(EventRecord{EventID: row.ID, SessionID: m.header.ID, Kind: row.Type, Payload: json.RawMessage(row.Data)})
		if err != nil {
			return nil, fmt.Errorf("legacy entry %s (%s) cannot be exported: %w", row.ID, row.Type, err)
		}
		id, typeStr, _, _ := getEntryMetadata(decoded)
		if id != row.ID || typeStr != row.Type {
			return nil, fmt.Errorf("legacy entry %s has payload identity %s/%s that disagrees with its row identity %s/%s", row.ID, typeStr, id, row.Type, row.ID)
		}
		inPool[row.ID] = sessionEntryIdentity{seq: row.Seq, id: row.ID, typ: EntryType(row.Type), self: decoded}
		pool = append(pool, inPool[row.ID])
	}
	parentOf := make(map[string]string, len(rows))
	for _, row := range rows {
		if row.ID == "" || row.ParentID == nil || *row.ParentID == "" {
			continue
		}
		parentOf[row.ID] = *row.ParentID
	}

	ordered := make([]sessionEntryIdentity, 0, len(pool))
	visited := make(map[string]struct{}, len(pool))
	appended := make(map[string]struct{}, len(pool))
	// appendChain walks from an entry towards the entries it was built on, so
	// parents land before children even when the row scan saw them in the
	// opposite order. A cycle stops at the already-visited node instead of
	// recursing forever.
	var appendChain func(string)
	appendChain = func(id string) {
		if _, seen := visited[id]; seen {
			return
		}
		visited[id] = struct{}{}
		if parent, ok := parentOf[id]; ok && parent != id {
			if _, known := inPool[parent]; known {
				appendChain(parent)
			}
		}
		if item, ok := inPool[id]; ok {
			if _, twice := appended[id]; !twice {
				ordered = append(ordered, item)
				appended[id] = struct{}{}
			}
		}
	}
	for _, item := range pool {
		appendChain(item.id)
	}
	return ordered, nil
}
