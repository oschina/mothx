package session

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/dao"
)

// forkEventStoreSession copies committed EventStore entries into a new
// session-scoped EventStore. SQLite remains the control-plane owner of the
// child session, turn rows, capabilities, project membership, and idempotency
// request; transcript payload never passes through entries.
func forkEventStoreSession(ctx context.Context, sessionDir string, options ForkOptions, requestHash, fingerprint string) (result ForkResult, retErr error) {
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return ForkResult{}, err
	}
	sourceLease, err := AcquireFork(sessionDir, options.SourceSessionID)
	if err != nil {
		if err == ErrRuntimeSessionNotFound {
			return ForkResult{}, ErrForkSessionNotFound
		}
		if err == ErrRuntimeLeaseBusy || err == ErrSessionRunActive {
			return ForkResult{}, ErrForkSessionActive
		}
		return ForkResult{}, err
	}
	defer sourceLease.Release()

	entries, err := loadForkEventStoreEntries(ctx, sessionDir, options.SourceSessionID)
	if err != nil {
		return ForkResult{}, err
	}
	readTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ForkResult{}, err
	}
	defer readTx.Rollback()
	// Parity with the legacy transcript fork: AcquireFork only refuses an active
	// Run row, so an idle-but-pending decision (for example a restarted process
	// whose lease expired) and an open turn must still block the copy. Without
	// these gates the EventStore branch could fork a session mid-approval.
	forkDAO := dao.NewForkDAO(nil)
	activeRuns, err := forkDAO.ActiveRunCount(ctx, readTx, options.SourceSessionID, NonTerminalSessionRunStatuses())
	if err != nil {
		return ForkResult{}, err
	}
	if activeRuns != 0 {
		return ForkResult{}, ErrForkSessionActive
	}
	openTurns, err := forkDAO.OpenTurnCount(ctx, readTx, options.SourceSessionID)
	if err != nil {
		return ForkResult{}, err
	}
	if openTurns != 0 {
		return ForkResult{}, ErrForkSessionActive
	}
	pendingDecisions, err := pendingDecisionsTx(ctx, readTx, options.SourceSessionID)
	if err != nil {
		return ForkResult{}, err
	}
	if pendingDecisions {
		return ForkResult{}, ErrForkSessionActive
	}
	turns, err := loadForkTurnsTx(ctx, readTx, options.SourceSessionID)
	if err != nil {
		return ForkResult{}, err
	}
	// loadForkTurnsTx only reads; use a short-lived transaction and close it
	// before the child write path to avoid holding a shared snapshot.
	boundary, kind, err := resolveForkBoundaryTx(ctx, readTx, options.SourceSessionID, entries, turns, options.AtSeq)
	if err != nil {
		return ForkResult{}, err
	}
	if boundary <= 0 {
		return ForkResult{}, ErrForkNoCompletedTurn
	}
	copyEntries := make([]forkSourceEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Seq <= boundary {
			copyEntries = append(copyEntries, entry)
		}
	}
	if len(copyEntries) == 0 {
		return ForkResult{}, ErrForkNoCompletedTurn
	}
	_ = readTx.Rollback()

	childID := GenerateID()
	entryIDMap := make(map[string]string, len(copyEntries))
	for _, entry := range copyEntries {
		entryIDMap[entry.ID] = GenerateID()
	}
	turnIDMap := make(map[string]string)
	for _, turn := range turns {
		if turn.EndSeq != nil && *turn.EndSeq <= boundary {
			turnIDMap[turn.ID] = GenerateID()
		}
	}

	// Create only the child control-plane row first. Its content is not valid
	// until the child EventStore batch and turn projection have committed.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ForkResult{}, err
	}
	if err := validateRuntimeLeaseTxContext(ctx, tx, sessionDir, options.SourceSessionID); err != nil {
		_ = tx.Rollback()
		return ForkResult{}, err
	}
	if err := dao.NewForkDAO(nil).InsertSessionFrom(ctx, tx, childID, options.SourceSessionID, boundary, int64(len(copyEntries)), string(kind)); err != nil {
		_ = tx.Rollback()
		return ForkResult{}, err
	}
	if options.ExpertID != nil {
		if err := dao.NewSessionDAO(nil).UpdateSessionExpertID(ctx, tx, "sessions", childID, *options.ExpertID); err != nil {
			_ = tx.Rollback()
			return ForkResult{}, err
		}
	}
	if err := dao.NewForkDAO(nil).CopyCapabilities(ctx, tx, options.SourceSessionID, childID); err != nil {
		_ = tx.Rollback()
		return ForkResult{}, err
	}
	if err := dao.NewForkDAO(nil).CopyProject(ctx, tx, options.SourceSessionID, childID); err != nil {
		_ = tx.Rollback()
		return ForkResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ForkResult{}, err
	}

	child, err := OpenByIDExact(sessionDir, childID)
	if err != nil {
		return ForkResult{}, err
	}
	childLease, err := AcquireMutation(sessionDir, childID)
	if err != nil {
		return ForkResult{}, err
	}
	forkCommitted := false
	defer func() {
		if forkCommitted {
			childLease.Release()
			return
		}
		cleanupErr := DeleteSessionWithMutation(child.GetFile(), sessionDir, childLease)
		childLease.Release()
		if cleanupErr != nil {
			retErr = fmt.Errorf("%w (also failed to remove incomplete fork child %s: %v)", retErr, childID, cleanupErr)
		}
	}()
	childEntries := make([]interface{}, 0, len(copyEntries))
	seqMap := make(map[int64]int64, len(copyEntries))
	for i, source := range copyEntries {
		data, err := remapForkData(source.Type, source.Data, entryIDMap, turnIDMap)
		if err != nil {
			return ForkResult{}, fmt.Errorf("%w: entry %s: %v", ErrForkUnsupportedEntry, source.ID, err)
		}
		parentID := ""
		if source.ParentID.Valid {
			parentID = entryIDMap[source.ParentID.String]
		}
		data, err = rewriteForkEntryIdentity(data, entryIDMap[source.ID], parentID)
		if err != nil {
			return ForkResult{}, fmt.Errorf("%w: entry identity %s: %v", ErrForkUnsupportedEntry, source.ID, err)
		}
		record := EventRecord{EventID: entryIDMap[source.ID], SessionID: childID, Kind: source.Type, Payload: []byte(data), CreatedAt: parseSessionTimestamp(source.Timestamp)}
		decoded, err := DecodeEventRecord(record)
		if err != nil {
			return ForkResult{}, err
		}
		childEntries = append(childEntries, decoded)
		// The child EventStore allocates its first event at eventStoreSeqBase,
		// so the copied ordinals must share that base for the turn-boundary
		// mapping below to name real child sequences.
		seqMap[source.Seq] = int64(i) + eventStoreSeqBase
	}
	if _, err := child.AppendEventStoreEntries(ctx, "", "", childEntries); err != nil {
		return ForkResult{}, err
	}
	baseTitle := titleFromEntries(copyEntries)
	if strings.TrimSpace(baseTitle) != "" {
		// The legacy fork path allocates a sibling title through an entries
		// query. EventStore children have no payload rows to query, so allocate
		// the same deterministic first sibling suffix through the canonical
		// EventStore title entry while the child mutation lease is held.
		title, err := nextEventStoreForkTitle(ctx, db, sessionDir, options.SourceSessionID, baseTitle)
		if err != nil {
			return ForkResult{}, err
		}
		if _, err := child.AppendSessionTitle(title, "auto"); err != nil {
			return ForkResult{}, err
		}
	}
	if _, err := ensureDisplayProjection(ctx, sessionDir, childID, "event_store"); err != nil {
		// Derived list metadata; a later attach or maintenance pass retries.
		log.Printf("[session] project forked session %s: %v", childID, err)
	}

	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		return ForkResult{}, err
	}
	if err := validateRuntimeLeaseTxContext(ctx, tx, sessionDir, childID); err != nil {
		_ = tx.Rollback()
		return ForkResult{}, err
	}
	for _, turn := range turns {
		if turn.EndSeq == nil || *turn.EndSeq > boundary {
			continue
		}
		startSeq, okStart := seqMap[turn.StartSeq]
		endSeq, okEnd := seqMap[*turn.EndSeq]
		if !okStart || !okEnd {
			_ = tx.Rollback()
			return ForkResult{}, fmt.Errorf("%w: turn %s boundary mapping", ErrForkUnsupportedEntry, turn.ID)
		}
		if err := dao.NewForkDAO(nil).InsertTurn(ctx, tx, turnIDMap[turn.ID], childID, turn.IntentID, turn.Kind, turn.Status, startSeq, endSeq, turn.StartedAt.Format(time.RFC3339Nano), formatOptionalTime(turn.EndedAt)); err != nil {
			_ = tx.Rollback()
			return ForkResult{}, err
		}
	}
	if err := dao.NewForkDAO(nil).InsertForkRequest(ctx, tx, &dao.ForkRequestRecord{RequestKeyHash: requestHash, RequestFingerprint: fingerprint, SourceSessionID: options.SourceSessionID, ChildSessionID: childID, CreatedAt: time.Now().Format(time.RFC3339Nano)}); err != nil {
		_ = tx.Rollback()
		return ForkResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ForkResult{}, err
	}
	forkCommitted = true
	return ForkResult{SessionID: childID, ParentSessionID: options.SourceSessionID, ForkKind: kind, BoundarySeq: boundary, SeedLength: int64(len(copyEntries))}, nil
}

func nextEventStoreForkTitle(ctx context.Context, db *dao.Database, sessionDir, parentID, base string) (string, error) {
	children, err := dao.NewForkDAO(nil).ChildSessionIDs(ctx, db.Bun(), parentID)
	if err != nil {
		return "", err
	}
	used := make(map[string]struct{}, len(children))
	for _, childID := range children {
		name, _, err := LatestSessionTitle(sessionDir, childID)
		if err != nil {
			continue
		}
		used[name] = struct{}{}
	}
	for index := 1; index < 10000; index++ {
		candidate := fmt.Sprintf("%s (%d)", base, index)
		if _, exists := used[candidate]; !exists {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("unable to allocate EventStore fork title")
}

func loadForkEventStoreEntries(ctx context.Context, sessionDir, sessionID string) ([]forkSourceEntry, error) {
	batches, err := ReadCommittedEventBatches(ctx, sessionDir, sessionID)
	if err != nil {
		return nil, err
	}
	entries := make([]forkSourceEntry, 0)
	for _, batch := range batches {
		for i, event := range batch.Events {
			decoded, err := DecodeEventRecord(event)
			if err != nil {
				return nil, err
			}
			id, typ, parent, timestamp := getEntryMetadata(decoded)
			parentRecord := dao.NullString{}
			if parent != nil {
				parentRecord = dao.NullString{String: *parent, Valid: true}
			}
			entries = append(entries, forkSourceEntry{Seq: batch.SeqFrom + int64(i), ID: id, Type: typ, ParentID: parentRecord, Timestamp: timestamp.Format(time.RFC3339Nano), Data: string(event.Payload)})
		}
	}
	return entries, nil
}
