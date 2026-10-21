package session

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"context"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/provider"
)

func stableTurnEntryID(turnID, boundary string) string {
	sum := sha256.Sum256([]byte("mothx-turn-entry\x00" + turnID + "\x00" + boundary))
	return "turn_" + hex.EncodeToString(sum[:8])
}

// appendEventStoreRunTerminal persists terminal transcript entries before the
// fenced SQLite Run/turn transaction. It returns handled=false for a legacy
// session, so the established SQLite path remains unchanged there.
func appendEventStoreRunTerminal(ctx context.Context, sessionDir string, run SessionRun, turnID, turnStatus, stopReason string, at time.Time) (bool, error) {
	inspection, err := InspectEventStore(ctx, sessionDir, run.SessionID)
	if err != nil {
		return false, err
	}
	if len(inspection.Batches) == 0 && len(inspection.Intents) == 0 {
		return false, nil
	}
	if inspection.State != EventStoreHealthy {
		return false, fmt.Errorf("%w: cannot terminalize Run with EventStore state %s: %s", ErrEventStoreNotHealthy, inspection.State, inspection.Reason)
	}
	manager, err := OpenByIDExact(sessionDir, run.SessionID)
	if err != nil {
		return false, err
	}
	committedBatches, err := ReadCommittedEventBatches(ctx, sessionDir, run.SessionID)
	if err != nil {
		return false, err
	}
	committed := make(map[string]EventRecord)
	for _, batch := range committedBatches {
		for _, record := range batch.Events {
			committed[record.EventID] = record
		}
	}
	entries := make([]interface{}, 0, 2)
	parent := manager.GetLeafID()
	if run.AssistantMessage != nil {
		message := *run.AssistantMessage
		message, _ = provider.NormalizeMessage(message)
		if message.Role == "" {
			message.Role = "assistant"
		}
		if message.Role != "assistant" || message.SystemInjected {
			return false, fmt.Errorf("runtime assistant entry must be a non-injected assistant message")
		}
		if message.Timestamp.IsZero() {
			if run.FinishedAt != nil {
				message.Timestamp = *run.FinishedAt
			} else {
				message.Timestamp = at
			}
		}
		id := run.AssistantEntryID
		if id == "" {
			id = RunAssistantEntryID(run.ID)
		}
		if existing, ok := committed[id]; ok {
			persisted, err := DecodeEventRecord(existing)
			if err != nil {
				return false, err
			}
			messageEntry, ok := persisted.(MessageEntry)
			if !ok || RunAssistantMessageFingerprint(run.ID, messageEntry.Message) != RunAssistantMessageFingerprint(run.ID, message) {
				return false, fmt.Errorf("assistant event %s conflicts with terminal Run payload", id)
			}
			// The assistant payload is already part of the committed transcript.
			// It is not necessarily the leaf: a Run that ended on a tool-call turn
			// (for example max iterations) persists the tool results after it. Keep
			// the manager's current leaf so the turn-end entry chains after the
			// latest committed entry instead of re-parenting the transcript under an
			// earlier message (which the leaf fence rejects forever).
		} else {
			entry := MessageEntry{EntryBase: EntryBase{Type: EntryMessage, ID: id, ParentID: parent, Timestamp: message.Timestamp}, Message: message}
			entries = append(entries, entry)
			parentID := id
			parent = &parentID
		}
	}
	if turnID != "" {
		db, err := OpenRootDB(sessionDir)
		if err != nil {
			return false, err
		}
		state, err := dao.NewConversationTurnDAO(db.Bun()).State(ctx, db.Bun(), run.SessionID, turnID)
		switch {
		case err == nil:
		case err == dao.ErrNoRows:
			// A recovered Run never had its turn boundary opened. There is no
			// turn to close, so the terminal transaction still finalizes the Run
			// and its assistant entry instead of failing forever. The SQLite
			// path below already tolerates a missing turn row.
			state = dao.ConversationTurnState{}
		default:
			return false, err
		}
		if state.Status == "open" {
			if at.IsZero() {
				at = time.Now()
			}
			entryID := stableTurnEntryID(turnID, "end")
			if existing, ok := committed[entryID]; ok {
				persisted, err := DecodeEventRecord(existing)
				if err != nil {
					return false, err
				}
				turnEnd, ok := persisted.(TurnEndEntry)
				if !ok || turnEnd.TurnID != turnID || turnEnd.Status != normalizeTurnStatus(turnStatus) {
					return false, fmt.Errorf("turn-end event %s conflicts with terminal state", entryID)
				}
			} else {
				entry := TurnEndEntry{EntryBase: EntryBase{Type: EntryTurnEnd, ID: entryID, ParentID: parent, Timestamp: at}, TurnID: turnID, IntentID: state.IntentID, RunID: state.RunID, Status: normalizeTurnStatus(turnStatus), StopReason: stopReason}
				entries = append(entries, entry)
			}
		}
	}
	if len(entries) > 0 {
		if _, err := manager.AppendEventStoreEntries(ctx, run.ID, turnID, entries); err != nil {
			return false, err
		}
	}
	return true, nil
}
