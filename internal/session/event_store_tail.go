package session

import (
	"context"
	"errors"
	"fmt"

	"github.com/oschina/mothx/internal/provider"
)

// ErrTailReplayUnsupported means the session cannot be projected from the tail
// alone: it is not an EventStore session, it has compaction or content override
// entries whose replay state depends on earlier messages, or its derived
// projection is unavailable. Callers must fall back to the canonical full open.
var ErrTailReplayUnsupported = errors.New("session tail replay is unsupported")

// TailTranscriptPage is one page read from the end of an EventStore transcript.
// Start is the zero-based message index of Messages[0] in the full conversation.
type TailTranscriptPage struct {
	Messages []provider.Message
	EntryIDs []string
	Start    int
	Total    int
}

// TailReplaySupported reports whether ReadEventStoreTailPage can project this
// session without replaying earlier messages. It requires an EventStore session,
// a persisted display projection for bindings/title, and no compaction or
// content-override entry whose replay depends on the full log.
func TailReplaySupported(sessionDir, sessionID string) bool {
	if sessionDir == "" || sessionID == "" {
		return false
	}
	ctx := context.Background()
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return false
	}
	var contentStore string
	if err := db.Bun().NewSelect().Table("sessions").Column("content_store").
		Where("id = ?", sessionID).Scan(ctx, &contentStore); err != nil || contentStore != "event_store" {
		return false
	}
	var projections, sensitive int
	if err := db.Bun().NewSelect().Table("session_display_projection").ColumnExpr("COUNT(*)").
		Where("session_id = ?", sessionID).Scan(ctx, &projections); err != nil || projections == 0 {
		return false
	}
	if err := db.Bun().NewRaw(`SELECT COUNT(*) FROM session_event_projections
		WHERE session_id = ? AND type IN (?, ?)`, sessionID, string(EntryCompaction), string(EntryContentOverride)).
		Scan(ctx, &sensitive); err != nil || sensitive > 0 {
		return false
	}
	// The committed intent's sequence range and the derived projection must agree.
	// If they do not, the tail cannot be aligned to the full message index without
	// inspection; fall back to the canonical open.
	var maxIntent, maxProjection int64
	if err := db.Bun().NewRaw(`SELECT COALESCE(MAX(seq_to), 0) FROM session_event_intents
		WHERE session_id = ? AND state = ?`, sessionID, string(EventIntentCommitted)).Scan(ctx, &maxIntent); err != nil {
		return false
	}
	if err := db.Bun().NewSelect().Table("session_event_projections").ColumnExpr("COALESCE(MAX(seq), 0)").
		Where("session_id = ?", sessionID).Scan(ctx, &maxProjection); err != nil {
		return false
	}
	return maxIntent == maxProjection
}

// ReadEventStoreTailPage reads only the segments needed for the requested page
// instead of replaying the whole EventStore. before is the exclusive message
// index the page ends at; zero means the end of the transcript. The read is
// read-only and never converges, repairs, or takes a lease.
//
// Compaction and content overrides change messages that may live before the
// page, so those sessions deliberately fall back to the full open rather than
// risk showing a transcript that differs from canonical replay.
func ReadEventStoreTailPage(sessionDir, sessionID string, before, limit int) (TailTranscriptPage, error) {
	if sessionDir == "" || sessionID == "" || limit <= 0 {
		return TailTranscriptPage{}, ErrTailReplayUnsupported
	}
	ctx := context.Background()
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return TailTranscriptPage{}, ErrTailReplayUnsupported
	}
	var contentStore string
	if err := db.Bun().NewSelect().Table("sessions").Column("content_store").
		Where("id = ?", sessionID).Scan(ctx, &contentStore); err != nil || contentStore != "event_store" {
		return TailTranscriptPage{}, ErrTailReplayUnsupported
	}
	var sensitive int
	if err := db.Bun().NewRaw(`SELECT COUNT(*) FROM session_event_projections
		WHERE session_id = ? AND type IN (?, ?)`, sessionID, string(EntryCompaction), string(EntryContentOverride)).
		Scan(ctx, &sensitive); err != nil || sensitive > 0 {
		return TailTranscriptPage{}, ErrTailReplayUnsupported
	}
	var total int
	if err := db.Bun().NewSelect().Table("session_event_projections").ColumnExpr("COUNT(*)").
		Where("session_id = ? AND type = ?", sessionID, string(EntryMessage)).Scan(ctx, &total); err != nil {
		return TailTranscriptPage{}, ErrTailReplayUnsupported
	}
	var committedIDs []string
	if err := db.Bun().NewSelect().Table("session_event_intents").Column("id").
		Where("session_id = ? AND state = ?", sessionID, string(EventIntentCommitted)).Scan(ctx, &committedIDs); err != nil {
		return TailTranscriptPage{}, ErrTailReplayUnsupported
	}
	committed := make(map[string]struct{}, len(committedIDs))
	for _, id := range committedIDs {
		committed[id] = struct{}{}
	}
	if before == 0 {
		before = total
	} else if before < 0 || before > total {
		return TailTranscriptPage{}, fmt.Errorf("invalid transcript cursor %d for %d messages", before, total)
	}
	start := before - limit
	if start < 0 {
		start = 0
	}
	if start == before {
		return TailTranscriptPage{Start: start, Total: total}, nil
	}
	store, err := NewEventStore(sessionDir, sessionID)
	if err != nil {
		return TailTranscriptPage{}, err
	}
	layout, err := store.scanLayout()
	if err != nil {
		return TailTranscriptPage{}, err
	}
	type tailMessage struct {
		entryID string
		message provider.Message
	}
	collected := make([]tailMessage, 0, before-start)
	rank := total
	done := false
	for index := len(layout.paths) - 1; index >= 0 && !done; index-- {
		result, scanErr := scanEventStore(layout.paths[index], false, false)
		if scanErr != nil {
			return TailTranscriptPage{}, scanErr
		}
		messages := make([]tailMessage, 0)
		for _, batch := range result.batches {
			if _, ok := committed[batch.BatchID]; !ok {
				continue
			}
			for _, event := range batch.Events {
				if EntryType(event.Kind) != EntryMessage {
					continue
				}
				entry, decodeErr := DecodeEventRecord(event)
				if decodeErr != nil {
					return TailTranscriptPage{}, decodeErr
				}
				messageEntry, ok := entry.(MessageEntry)
				if !ok {
					continue
				}
				messages = append(messages, tailMessage{entryID: messageEntry.ID, message: messageEntry.Message})
			}
		}
		for messageIndex := len(messages) - 1; messageIndex >= 0; messageIndex-- {
			rank--
			if rank >= before {
				continue
			}
			if rank < start {
				done = true
				break
			}
			collected = append(collected, messages[messageIndex])
		}
	}
	if len(collected) != before-start {
		return TailTranscriptPage{}, fmt.Errorf("event store tail is inconsistent: got %d messages, want %d", len(collected), before-start)
	}
	page := TailTranscriptPage{
		Messages: make([]provider.Message, len(collected)),
		EntryIDs: make([]string, len(collected)),
		Start:    start,
		Total:    total,
	}
	for i := range collected {
		message := collected[len(collected)-1-i]
		page.Messages[i] = message.message
		page.EntryIDs[i] = message.entryID
	}
	return page, nil
}
