package session

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/oschina/mothx/internal/dao"
)

var ErrConversationTurnNotOpen = errors.New("conversation turn is not open")

// ConversationTurn is the durable boundary index used by Session fork
// resolution. It is intentionally separate from SessionRun because a Run may
// execute tools or maintenance work without producing a conversation turn.
type ConversationTurn struct {
	ID        string
	SessionID string
	IntentID  string
	RunID     string
	Attempt   int
	Kind      string
	Status    string
	StartSeq  int64
	EndSeq    *int64
	StartedAt time.Time
	EndedAt   *time.Time
}

func normalizeTurnStatus(status string) string {
	if status == "" {
		return "open"
	}
	return status
}

func appendTurnEntryTx(tx *dao.Tx, sessionID string, entry any, parentID string) (int64, error) {
	return appendTurnEntryTxContext(context.Background(), tx, sessionID, entry, parentID)
}

func appendTurnEntryTxContext(ctx context.Context, tx *dao.Tx, sessionID string, entry any, parentID string) (int64, error) {
	id, typeName, _, timestamp := getEntryMetadata(entry)
	if id == "" || typeName == "" {
		return 0, fmt.Errorf("turn entry identity is required")
	}
	data, err := marshalSessionEntry(entry)
	if err != nil {
		return 0, fmt.Errorf("marshal turn entry: %w", err)
	}
	var parentPtr *string
	if parentID != "" {
		parentPtr = &parentID
	}
	return dao.NewConversationTurnDAO(nil).AppendEntry(ctx, tx, &dao.EntryRecord{SessionID: sessionID, ID: id,
		Type: typeName, ParentID: parentPtr,
		Timestamp: timestamp.Format(time.RFC3339Nano), Data: string(data)})
}

func currentLeafTx(tx *dao.Tx, sessionID string) (string, error) {
	return currentLeafTxContext(context.Background(), tx, sessionID)
}

func currentLeafTxContext(ctx context.Context, tx *dao.Tx, sessionID string) (string, error) {
	return dao.NewConversationTurnDAO(nil).CurrentLeaf(ctx, tx, sessionID, string(EntrySession))
}

// StartConversationTurn atomically writes turn/start and its boundary row.
func StartConversationTurn(sessionDir string, turn ConversationTurn) error {
	if turn.SessionID == "" || turn.ID == "" {
		return fmt.Errorf("conversation turn ID and session ID are required")
	}
	if turn.StartedAt.IsZero() {
		turn.StartedAt = time.Now()
	}
	if turn.Kind == "" {
		turn.Kind = "conversation"
	}
	turn.Status = "open"
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := validateRuntimeLeaseTx(tx, sessionDir, turn.SessionID); err != nil {
		return err
	}
	if err := startConversationTurnTx(tx, turn); err != nil {
		return err
	}
	return tx.Commit()
}

func (m *Manager) startEventStoreTurnLocked(ctx context.Context, turn ConversationTurn) error {
	sessionDir := m.sessionDir
	if sessionDir == "" {
		sessionDir = filepath.Dir(resolveDBPath(m.file))
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	turnDAO := dao.NewConversationTurnDAO(db.Bun())
	state, err := turnDAO.State(ctx, db.Bun(), turn.SessionID, turn.ID)
	if err == nil && state.Status == "open" {
		if state.RunID == turn.RunID && turn.RunID != "" {
			return nil
		}
		return fmt.Errorf("conversation turn already open for session %s", turn.SessionID)
	}
	if err != nil && err != dao.ErrNoRows {
		return err
	}
	if turn.StartedAt.IsZero() {
		turn.StartedAt = time.Now()
	}
	if turn.Kind == "" {
		turn.Kind = "conversation"
	}
	entryID := stableTurnEntryID(turn.ID, "start")
	entry := TurnStartEntry{EntryBase: EntryBase{Type: EntryTurnStart, ID: entryID, ParentID: m.leafID, Timestamp: turn.StartedAt}, TurnID: turn.ID, IntentID: turn.IntentID, RunID: turn.RunID, Attempt: turn.Attempt}
	// If the process crashed after the start event committed but before the
	// control row was inserted, retain the original timestamp and parent on the
	// idempotent retry.
	if batches, scanErr := ReadCommittedEventBatches(ctx, sessionDir, turn.SessionID); scanErr == nil {
		for _, batch := range batches {
			for _, record := range batch.Events {
				if record.EventID != entryID {
					continue
				}
				decoded, decodeErr := DecodeEventRecord(record)
				if decodeErr != nil {
					return decodeErr
				}
				previous, ok := decoded.(TurnStartEntry)
				if !ok || previous.TurnID != turn.ID || previous.RunID != turn.RunID {
					return fmt.Errorf("turn-start event %s conflicts with requested turn", entryID)
				}
				entry = previous
			}
		}
	} else {
		return scanErr
	}
	ids, err := m.appendEventStoreEntriesLocked(ctx, turn.RunID, turn.ID, []interface{}{entry})
	if err != nil {
		return err
	}
	seq, err := dao.NewSessionEventProjectionDAO(nil).Seq(ctx, db.Bun(), turn.SessionID, ids[0])
	if err != nil {
		return err
	}
	return db.RunInTx(ctx, nil, func(_ context.Context, tx dao.Tx) error {
		if err := validateRuntimeLeaseTx(&tx, sessionDir, turn.SessionID); err != nil {
			return err
		}
		openCount, err := dao.NewConversationTurnDAO(nil).OpenCount(ctx, &tx, turn.SessionID)
		if err != nil {
			return err
		}
		if openCount != 0 {
			return fmt.Errorf("conversation turn already open for session %s", turn.SessionID)
		}
		if existing, stateErr := dao.NewConversationTurnDAO(nil).State(ctx, &tx, turn.SessionID, turn.ID); stateErr == nil {
			if existing.Status == "open" {
				return nil
			}
			return dao.NewConversationTurnDAO(nil).Reopen(ctx, &tx, &dao.ConversationTurnRecord{ID: turn.ID, SessionID: turn.SessionID, IntentID: turn.IntentID, Kind: turn.Kind, StartedAt: entry.Timestamp.Format(time.RFC3339Nano)})
		} else if stateErr != dao.ErrNoRows {
			return stateErr
		}
		return dao.NewConversationTurnDAO(nil).Insert(ctx, &tx, &dao.ConversationTurnRecord{ID: turn.ID, SessionID: turn.SessionID, IntentID: turn.IntentID, Kind: turn.Kind, Status: "open", StartSeq: seq, StartedAt: entry.Timestamp.Format(time.RFC3339Nano)})
	})
}

func (m *Manager) endEventStoreTurnLocked(ctx context.Context, turnID, status, stopReason string) error {
	sessionDir := m.sessionDir
	if sessionDir == "" {
		sessionDir = filepath.Dir(resolveDBPath(m.file))
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	turnDAO := dao.NewConversationTurnDAO(db.Bun())
	state, err := turnDAO.State(ctx, db.Bun(), m.header.ID, turnID)
	if err == dao.ErrNoRows {
		return fmt.Errorf("%w: %s", ErrConversationTurnNotOpen, turnID)
	}
	if err != nil {
		return err
	}
	if state.Status != "open" {
		return nil
	}
	endedAt := time.Now()
	entryID := stableTurnEntryID(turnID, "end")
	entry := TurnEndEntry{EntryBase: EntryBase{Type: EntryTurnEnd, ID: entryID, ParentID: m.leafID, Timestamp: endedAt}, TurnID: turnID, IntentID: state.IntentID, RunID: state.RunID, Status: normalizeTurnStatus(status), StopReason: stopReason}
	if batches, scanErr := ReadCommittedEventBatches(ctx, sessionDir, m.header.ID); scanErr == nil {
		for _, batch := range batches {
			for _, record := range batch.Events {
				if record.EventID != entryID {
					continue
				}
				decoded, decodeErr := DecodeEventRecord(record)
				if decodeErr != nil {
					return decodeErr
				}
				previous, ok := decoded.(TurnEndEntry)
				if !ok || previous.TurnID != turnID || previous.Status != normalizeTurnStatus(status) {
					return fmt.Errorf("turn-end event %s conflicts with requested status", entryID)
				}
				entry = previous
			}
		}
	} else {
		return scanErr
	}
	ids, err := m.appendEventStoreEntriesLocked(ctx, state.RunID, turnID, []interface{}{entry})
	if err != nil {
		return err
	}
	seq, err := dao.NewSessionEventProjectionDAO(nil).Seq(ctx, db.Bun(), m.header.ID, ids[0])
	if err != nil {
		return err
	}
	return db.RunInTx(ctx, nil, func(_ context.Context, tx dao.Tx) error {
		if err := validateRuntimeLeaseTx(&tx, sessionDir, m.header.ID); err != nil {
			return err
		}
		current, err := dao.NewConversationTurnDAO(nil).State(ctx, &tx, m.header.ID, turnID)
		if err != nil {
			return err
		}
		if current.Status != "open" {
			return nil
		}
		return dao.NewConversationTurnDAO(nil).Close(ctx, &tx, m.header.ID, turnID, normalizeTurnStatus(status), seq, entry.Timestamp.Format(time.RFC3339Nano))
	})
}

// startConversationTurnTx is the shared transaction primitive used by both
// standalone turn admission and atomic durable Run admission. The caller owns
// lease validation and transaction commit/rollback.
func startConversationTurnTx(tx *dao.Tx, turn ConversationTurn) error {
	if turn.SessionID == "" || turn.ID == "" {
		return fmt.Errorf("conversation turn ID and session ID are required")
	}
	if turn.StartedAt.IsZero() {
		turn.StartedAt = time.Now()
	}
	if turn.Kind == "" {
		turn.Kind = "conversation"
	}
	turn.Status = "open"
	state, existingErr := dao.NewConversationTurnDAO(nil).State(context.Background(), tx, turn.SessionID, turn.ID)
	if existingErr != nil && existingErr != dao.ErrNoRows {
		return existingErr
	}
	if existingErr == nil {
		if state.IntentID != "" && turn.IntentID != "" && state.IntentID != turn.IntentID {
			return fmt.Errorf("conversation turn %s belongs to another intent", turn.ID)
		}
		if state.Status == "open" {
			if state.RunID == turn.RunID && turn.RunID != "" {
				return nil
			}
			return fmt.Errorf("conversation turn already open for session %s", turn.SessionID)
		}
	}
	openCount, err := dao.NewConversationTurnDAO(nil).OpenCount(context.Background(), tx, turn.SessionID)
	if err != nil {
		return err
	}
	if openCount != 0 {
		return fmt.Errorf("conversation turn already open for session %s", turn.SessionID)
	}
	parentID, err := currentLeafTx(tx, turn.SessionID)
	if err != nil {
		return err
	}
	entry := TurnStartEntry{EntryBase: EntryBase{Type: EntryTurnStart, ID: GenerateID(), ParentID: stringPtr(parentID), Timestamp: turn.StartedAt}, TurnID: turn.ID, IntentID: turn.IntentID, RunID: turn.RunID, Attempt: turn.Attempt}
	startSeq, err := appendTurnEntryTx(tx, turn.SessionID, entry, parentID)
	if err != nil {
		return err
	}
	if existingErr == nil {
		err = dao.NewConversationTurnDAO(nil).Reopen(context.Background(), tx, &dao.ConversationTurnRecord{ID: turn.ID, SessionID: turn.SessionID, IntentID: turn.IntentID, StartedAt: turn.StartedAt.Format(time.RFC3339Nano)})
	} else {
		err = dao.NewConversationTurnDAO(nil).Insert(context.Background(), tx, &dao.ConversationTurnRecord{ID: turn.ID, SessionID: turn.SessionID, IntentID: turn.IntentID, Kind: turn.Kind, Status: "open", StartSeq: startSeq, StartedAt: turn.StartedAt.Format(time.RFC3339Nano)})
	}
	if err != nil {
		return err
	}
	return nil
}

// EndConversationTurn atomically writes turn/end and closes its boundary row.
func EndConversationTurn(sessionDir, sessionID, turnID, status, stopReason string, endedAt time.Time) error {
	if sessionID == "" || turnID == "" {
		return fmt.Errorf("conversation turn and session ID are required")
	}
	if endedAt.IsZero() {
		endedAt = time.Now()
	}
	status = normalizeTurnStatus(status)
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := validateRuntimeLeaseTx(tx, sessionDir, sessionID); err != nil {
		return err
	}
	state, err := dao.NewConversationTurnDAO(nil).State(context.Background(), tx, sessionID, turnID)
	if err == dao.ErrNoRows {
		return fmt.Errorf("%w: %s", ErrConversationTurnNotOpen, turnID)
	}
	if err != nil {
		return err
	}
	if state.Status != "open" {
		// Closing an already-closed turn is an idempotent success; no new
		// turn/end entry is appended.
		return nil
	}
	intentID, runID := state.IntentID, state.RunID
	parentID, err := currentLeafTx(tx, sessionID)
	if err != nil {
		return err
	}
	entry := TurnEndEntry{EntryBase: EntryBase{Type: EntryTurnEnd, ID: GenerateID(), ParentID: stringPtr(parentID), Timestamp: endedAt}, TurnID: turnID, IntentID: intentID, RunID: runID, Status: status, StopReason: stopReason}
	endSeq, err := appendTurnEntryTx(tx, sessionID, entry, parentID)
	if err != nil {
		return err
	}
	if err := dao.NewConversationTurnDAO(nil).Close(context.Background(), tx, sessionID, turnID, status, endSeq, endedAt.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// ListConversationTurns returns boundary rows in transcript order.
func ListConversationTurns(sessionDir, sessionID string) ([]ConversationTurn, error) {
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	records, err := dao.NewConversationTurnDAO(db.Bun()).List(context.Background(), sessionID)
	if err != nil {
		return nil, err
	}
	var turns []ConversationTurn
	for _, record := range records {
		turn := ConversationTurn{ID: record.ID, SessionID: record.SessionID, IntentID: record.IntentID,
			Kind: record.Kind, Status: record.Status, StartSeq: record.StartSeq, EndSeq: record.EndSeq,
			StartedAt: parseSessionTimestamp(record.StartedAt)}
		if record.EndedAt != nil {
			ended := parseSessionTimestamp(*record.EndedAt)
			turn.EndedAt = &ended
		}
		turns = append(turns, turn)
	}
	return turns, nil
}

func scanConversationTurnRecord(record dao.ConversationTurnRecord) ConversationTurn {
	turn := ConversationTurn{ID: record.ID, SessionID: record.SessionID, IntentID: record.IntentID,
		Kind: record.Kind, Status: record.Status, StartSeq: record.StartSeq, EndSeq: record.EndSeq,
		StartedAt: parseSessionTimestamp(record.StartedAt)}
	if record.EndedAt != nil {
		ended := parseSessionTimestamp(*record.EndedAt)
		turn.EndedAt = &ended
	}
	return turn
}
