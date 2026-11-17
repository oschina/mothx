package session

import (
	"context"
	"errors"
	"fmt"

	"github.com/oschina/mothx/internal/dao"
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
	if !tailProjectionReadable(ctx, db, sessionID) {
		return false
	}
	// The committed intent's sequence range and the derived projection must agree.
	// If they do not, the tail cannot be aligned to the full message index without
	// inspection; fall back to the canonical open.
	executor := db.Bun()
	maxIntent, err := dao.NewSessionEventIntentDAO(executor).MaxSeqToByState(ctx, executor, sessionID, string(EventIntentCommitted))
	if err != nil {
		return false
	}
	maxProjection, err := dao.NewSessionEventProjectionDAO(executor).MaxSeq(ctx, executor, sessionID)
	if err != nil {
		return false
	}
	return maxIntent == maxProjection
}

// tailProjectionReadable reports the checks both tail readers share: the
// session must be an EventStore session with a display projection (bindings and
// title) and without compaction or content-override entries, whose replay state
// depends on messages that may live before the requested page.
func tailProjectionReadable(ctx context.Context, db *dao.Database, sessionID string) bool {
	eventStore, err := sessionUsesEventStore(ctx, db, sessionID)
	if err != nil || !eventStore {
		return false
	}
	executor := db.Bun()
	display, err := dao.NewSessionDisplayProjectionDAO(executor).HasProjection(ctx, executor, sessionID)
	if err != nil || !display {
		return false
	}
	sensitive, err := dao.NewSessionEventProjectionDAO(executor).CountByTypes(ctx, executor, sessionID,
		[]string{string(EntryCompaction), string(EntryContentOverride)})
	return err == nil && sensitive == 0
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
	if !tailProjectionReadable(ctx, db, sessionID) {
		return TailTranscriptPage{}, ErrTailReplayUnsupported
	}
	executor := db.Bun()
	totalCount, err := dao.NewSessionEventProjectionDAO(executor).CountByTypes(ctx, executor, sessionID, []string{string(EntryMessage)})
	if err != nil {
		return TailTranscriptPage{}, ErrTailReplayUnsupported
	}
	total := int(totalCount)
	committedIDs, err := dao.NewSessionEventIntentDAO(executor).IDsByState(ctx, executor, sessionID, string(EventIntentCommitted))
	if err != nil {
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
