package session

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/oschina/mothx/internal/dao"
)

var ErrEventIntentFenced = errors.New("session event intent lost its fenced owner")

// ErrEventStoreNotHealthy is returned by every EventStore *write* gate whose
// inspection does not come back `healthy`. It exists so callers can distinguish
// "this store cannot accept my bytes right now" from an unrelated failure
// without matching on message text, and so tests can pin that the write fence
// stays closed while the read path deliberately tolerates `reconciling`.
var ErrEventStoreNotHealthy = errors.New("session event store is not healthy")

var eventIntentLocks sync.Map // map[event-store root]*sync.Mutex

const (
	EventIntentPrepared         = "prepared"
	EventIntentAppended         = "appended"
	EventIntentCommitted        = "committed"
	EventIntentAborted          = "aborted"
	EventIntentRecoveryRequired = "recovery_required"
	EventStoreHealthy           = "healthy"
	EventStoreReconciling       = "reconciling"
	EventStoreBlocked           = "blocked"
	EventStoreInconsistent      = "inconsistent"
	EventStoreCorrupt           = "corrupt"
)

// EventStoreInspection combines physical event batches with their SQLite
// control-plane intents. It is a diagnostic/recovery projection, not an
// authorization decision; callers still need a current fenced lease.
type EventStoreInspection struct {
	State   string
	Batches []EventBatch
	Intents []dao.SessionEventIntentRecord
	Reason  string
}

// ReadCommittedEventBatches returns only content whose SQLite intent is
// committed. Physically appended but in-doubt bytes are deliberately excluded
// from replay until reconciliation settles them.
func ReadCommittedEventBatches(ctx context.Context, sessionDir, sessionID string) ([]EventBatch, error) {
	inspection, err := InspectEventStore(ctx, sessionDir, sessionID)
	if err != nil {
		return nil, err
	}
	if inspection.State == EventStoreCorrupt || inspection.State == EventStoreInconsistent || inspection.State == EventStoreBlocked {
		return nil, fmt.Errorf("event store is not replayable: %s", inspection.Reason)
	}
	return committedEventBatches(inspection), nil
}

// committedEventBatches filters the physically complete batches of an already
// performed inspection by their committed SQLite intents. It deliberately does
// not rescan the EventStore: callers that just inspected it must not pay for a
// second full digest pass merely to select the replayable subset.
func committedEventBatches(inspection EventStoreInspection) []EventBatch {
	committed := make(map[string]struct{}, len(inspection.Intents))
	for _, intent := range inspection.Intents {
		if intent.State == EventIntentCommitted {
			committed[intent.ID] = struct{}{}
		}
	}
	batches := make([]EventBatch, 0, len(committed))
	for _, batch := range inspection.Batches {
		if _, ok := committed[batch.BatchID]; ok {
			batches = append(batches, batch)
		}
	}
	return batches
}

// RebuildEventProjection reconstructs the SQLite metadata index from
// committed EventStore batches. It requires a local mutation or recovery lease
// so a projection rebuild cannot race a live writer or silently authorize a
// stale process.
func RebuildEventProjection(ctx context.Context, sessionDir, sessionID string, guard *RuntimeLeaseGuard) error {
	if guard == nil {
		return ErrEventIntentFenced
	}
	binding := guard.Binding()
	if binding.SessionID != sessionID || (binding.Purpose != RuntimeLeasePurposeMutation && binding.Purpose != RuntimeLeasePurposeRecovery) {
		return ErrRuntimeLeaseLost
	}
	batches, err := ReadCommittedEventBatches(ctx, sessionDir, sessionID)
	if err != nil {
		return err
	}
	projections := make([]dao.SessionEventProjectionRecord, 0)
	for _, batch := range batches {
		batchProjections, err := eventProjectionRecords(batch)
		if err != nil {
			return err
		}
		projections = append(projections, batchProjections...)
	}
	return WriteRootDatabase(ctx, sessionDir, func(tx *dao.Tx) error {
		if _, err := validateRuntimeLeaseBindingTxContext(ctx, tx, sessionDir, sessionID, binding.RunID, binding.Purpose); err != nil {
			return err
		}
		projectionDAO := dao.NewSessionEventProjectionDAO(nil)
		if err := projectionDAO.DeleteSession(ctx, tx, sessionID); err != nil {
			return fmt.Errorf("clear event projection: %w", err)
		}
		if err := projectionDAO.InsertBatch(ctx, tx, projections); err != nil {
			return fmt.Errorf("rebuild event projection: %w", err)
		}
		return nil
	})
}

// EventIntentSpec is the SQLite control-plane reservation for one event batch.
// OwnerInstanceID, LeaseEpoch, and LeaseTokenHash must come from the Runtime's
// current fenced binding; callers must never accept them from a protocol peer.
type EventIntentSpec struct {
	ID              string
	SessionID       string
	RunID           string
	TurnID          string
	SeqFrom         int64
	SeqTo           int64
	Digest          string
	OwnerInstanceID string
	LeaseEpoch      int64
	LeaseTokenHash  string
}

// AppendPreparedEventBatch implements the first half of the cross-storage
// protocol: reserve the batch in SQLite, then append and fsync its bytes. If
// the file operation fails, the prepared intent is intentionally retained for
// reconciliation instead of being guessed into aborted.
func AppendPreparedEventBatch(ctx context.Context, sessionDir string, spec EventIntentSpec, events []EventRecord) (EventBatch, error) {
	if spec.ID == "" || spec.SessionID == "" || len(events) == 0 {
		return EventBatch{}, errors.New("event intent requires ID, session ID, and events")
	}
	now := time.Now().UTC()
	normalized := make([]EventRecord, len(events))
	copy(normalized, events)
	for i := range normalized {
		if normalized[i].SessionID == "" {
			normalized[i].SessionID = spec.SessionID
		}
		if normalized[i].CreatedAt.IsZero() {
			normalized[i].CreatedAt = now
		}
	}
	digest, err := DigestEventBatch(normalized)
	if err != nil {
		return EventBatch{}, err
	}
	if spec.Digest != "" && spec.Digest != digest {
		return EventBatch{}, fmt.Errorf("event intent digest does not match batch: %s", spec.ID)
	}
	spec.Digest = digest
	store, err := NewEventStore(sessionDir, spec.SessionID)
	if err != nil {
		return EventBatch{}, err
	}
	protocolLock := &sync.Mutex{}
	actual, _ := eventIntentLocks.LoadOrStore(filepath.Clean(store.Path()), protocolLock)
	protocolLock = actual.(*sync.Mutex)
	protocolLock.Lock()
	defer protocolLock.Unlock()
	seqFrom, err := store.NextSequence()
	if err != nil {
		return EventBatch{}, err
	}
	if spec.SeqFrom != seqFrom || spec.SeqTo != seqFrom+int64(len(normalized))-1 {
		return EventBatch{}, fmt.Errorf("event intent sequence does not match event store: got %d-%d, next %d", spec.SeqFrom, spec.SeqTo, seqFrom)
	}
	if err := PrepareEventIntent(ctx, sessionDir, spec); err != nil {
		return EventBatch{}, err
	}
	batch, err := store.Append(spec.ID, spec.SessionID, spec.RunID, spec.TurnID, normalized)
	if err != nil {
		return EventBatch{}, err
	}
	if batch.Digest != spec.Digest || batch.SeqFrom != spec.SeqFrom || batch.SeqTo != spec.SeqTo {
		return EventBatch{}, fmt.Errorf("event store batch does not match prepared intent: %s", spec.ID)
	}
	if err := markEventIntent(ctx, sessionDir, spec, EventIntentPrepared, EventIntentAppended, ""); err != nil {
		return EventBatch{}, err
	}
	return batch, nil
}

func PrepareEventIntent(ctx context.Context, sessionDir string, spec EventIntentSpec) error {
	now := time.Now().Unix()
	return WriteRootDatabase(ctx, sessionDir, func(tx *dao.Tx) error {
		rows, err := dao.NewSessionEventIntentDAO(nil).InsertFenced(ctx, tx, &dao.SessionEventIntentRecord{
			ID: spec.ID, SessionID: spec.SessionID, RunID: spec.RunID, TurnID: spec.TurnID,
			SeqFrom: spec.SeqFrom, SeqTo: spec.SeqTo, Digest: spec.Digest, State: EventIntentPrepared,
			OwnerInstanceID: spec.OwnerInstanceID, LeaseEpoch: spec.LeaseEpoch, LeaseTokenHash: spec.LeaseTokenHash,
			CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrEventIntentFenced
		}
		return nil
	})
}

func markEventIntent(ctx context.Context, sessionDir string, spec EventIntentSpec, fromState, toState, lastError string) error {
	now := time.Now().Unix()
	return WriteRootDatabase(ctx, sessionDir, func(tx *dao.Tx) error {
		rows, err := dao.NewSessionEventIntentDAO(nil).MarkState(ctx, tx, spec.ID, spec.SessionID, spec.OwnerInstanceID, spec.LeaseEpoch, spec.LeaseTokenHash, fromState, toState, lastError, now)
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrEventIntentFenced
		}
		return nil
	})
}

// CommitEventIntent closes the second half of the protocol. finalize runs in
// the same SQLite transaction as the fenced appended->committed transition,
// allowing Runtime to update Run/turn/decision state atomically.
//
// verified, when non-nil, is the batch this process just appended and fsynced
// through AppendPreparedEventBatch. Supplying it lets the hot append path skip
// the whole-log Scan() that otherwise exists only to re-read a batch this
// process did not write (crash recovery, reconciliation). A nil batch always
// rescans, so recovery keeps its exhaustive verification.
func CommitEventIntent(ctx context.Context, sessionDir string, spec EventIntentSpec, verified *EventBatch, finalize func(*dao.Tx) error) error {
	database, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	intent, err := dao.NewSessionEventIntentDAO(nil).Find(ctx, database.Bun(), spec.ID)
	if err != nil {
		return err
	}
	if intent.SessionID != spec.SessionID || intent.OwnerInstanceID != spec.OwnerInstanceID || intent.LeaseEpoch != spec.LeaseEpoch || intent.LeaseTokenHash != spec.LeaseTokenHash || intent.State != EventIntentAppended {
		return ErrEventIntentFenced
	}
	if spec.Digest == "" {
		spec.Digest = intent.Digest
	}
	if spec.SeqFrom == 0 && spec.SeqTo == 0 && (intent.SeqFrom != 0 || intent.SeqTo != 0) {
		spec.SeqFrom, spec.SeqTo = intent.SeqFrom, intent.SeqTo
	}
	committedBatch := verified
	if committedBatch == nil {
		store, err := NewEventStore(sessionDir, spec.SessionID)
		if err != nil {
			return err
		}
		batches, err := store.Scan()
		if err != nil {
			return err
		}
		for i := range batches {
			if batches[i].BatchID == spec.ID {
				committedBatch = &batches[i]
				break
			}
		}
	}
	if committedBatch == nil || committedBatch.BatchID != spec.ID || committedBatch.SeqFrom != spec.SeqFrom || committedBatch.SeqTo != spec.SeqTo || committedBatch.Digest != spec.Digest {
		return fmt.Errorf("event batch %s is not complete for commit", spec.ID)
	}
	projections, err := eventProjectionRecords(*committedBatch)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	return WriteRootDatabase(ctx, sessionDir, func(tx *dao.Tx) error {
		rows, err := dao.NewSessionEventIntentDAO(nil).MarkState(ctx, tx, spec.ID, spec.SessionID, spec.OwnerInstanceID, spec.LeaseEpoch, spec.LeaseTokenHash, EventIntentAppended, EventIntentCommitted, "", now)
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrEventIntentFenced
		}
		if err := dao.NewSessionEventProjectionDAO(nil).InsertBatch(ctx, tx, projections); err != nil {
			return fmt.Errorf("persist event projection: %w", err)
		}
		if err := dao.NewSessionDAO(nil).SetContentStore(ctx, tx, spec.SessionID, "event_store"); err != nil {
			return fmt.Errorf("mark EventStore as canonical: %w", err)
		}
		if finalize != nil {
			return finalize(tx)
		}
		return nil
	})
}

func eventProjectionRecords(batch EventBatch) ([]dao.SessionEventProjectionRecord, error) {
	projections := make([]dao.SessionEventProjectionRecord, 0, len(batch.Events))
	for i, event := range batch.Events {
		decoded, err := DecodeEventRecord(event)
		if err != nil {
			return nil, err
		}
		id, kind, parentID, timestamp := getEntryMetadata(decoded)
		if id != event.EventID || kind != event.Kind {
			return nil, fmt.Errorf("event projection metadata mismatch for %s", event.EventID)
		}
		// The batch envelope carries the Run/Turn of the append, which is not the
		// same thing for a turn boundary marker: one fenced append can legitimately
		// carry the terminal assistant message and the `turn_end` marker of the turn
		// being closed, and the migration export appends every legacy entry as an
		// unowned batch. The marker payload is therefore the authority for its own
		// identity, exactly as it is for the legacy `entries.data` column that
		// `ConversationTurnDAO.State` and `RecoveryDAO.ListOpenTurns` still read.
		runID, turnID := event.RunID, event.TurnID
		switch marker := decoded.(type) {
		case TurnStartEntry:
			runID, turnID = marker.RunID, marker.TurnID
		case *TurnStartEntry:
			runID, turnID = marker.RunID, marker.TurnID
		case TurnEndEntry:
			runID, turnID = marker.RunID, marker.TurnID
		case *TurnEndEntry:
			runID, turnID = marker.RunID, marker.TurnID
		}
		projections = append(projections, dao.SessionEventProjectionRecord{
			ID: id, SessionID: event.SessionID, Seq: batch.SeqFrom + int64(i), Type: kind,
			ParentID: parentID, RunID: runID, TurnID: turnID,
			Timestamp: timestamp.UTC().Format(time.RFC3339Nano), BatchID: batch.BatchID,
		})
	}
	return projections, nil
}

// ReconcileEventIntents lets a fenced recovery owner settle event intents for
// one orphaned Run. Complete bytes are adopted and committed; a prepared intent
// with no bytes is safely aborted; an appended intent with missing bytes is
// preserved as recovery_required because its prior commit cannot be inferred.
func ReconcileEventIntents(ctx context.Context, sessionDir, sessionID, runID string, guard *RuntimeLeaseGuard, finalize func(*dao.Tx, EventBatch) error) error {
	if guard == nil {
		return ErrEventIntentFenced
	}
	binding := guard.Binding()
	if binding.SessionID != sessionID || binding.RunID != runID || binding.Purpose != RuntimeLeasePurposeRecovery {
		return ErrRuntimeLeaseLost
	}
	inspection, err := InspectEventStore(ctx, sessionDir, sessionID)
	if err != nil {
		return err
	}
	if inspection.State == EventStoreCorrupt || inspection.State == EventStoreBlocked {
		return fmt.Errorf("event store cannot be reconciled: %s", inspection.Reason)
	}
	byID := make(map[string]EventBatch, len(inspection.Batches))
	for _, batch := range inspection.Batches {
		byID[batch.BatchID] = batch
	}
	for _, intent := range inspection.Intents {
		if intent.SessionID != sessionID || intent.RunID != runID || (intent.State != EventIntentPrepared && intent.State != EventIntentAppended) {
			continue
		}
		batch, exists := byID[intent.ID]
		spec := EventIntentSpec{ID: intent.ID, SessionID: sessionID, RunID: runID, TurnID: intent.TurnID, SeqFrom: intent.SeqFrom, SeqTo: intent.SeqTo, Digest: intent.Digest, OwnerInstanceID: binding.OwnerInstanceID, LeaseEpoch: binding.Epoch, LeaseTokenHash: binding.TokenHash}
		if err := adoptEventIntent(ctx, sessionDir, spec); err != nil {
			return err
		}
		if !exists {
			if intent.State == EventIntentPrepared {
				if err := markEventIntent(ctx, sessionDir, spec, EventIntentPrepared, EventIntentAborted, "event bytes were not found during recovery"); err != nil {
					return err
				}
				continue
			}
			if err := markEventIntent(ctx, sessionDir, spec, EventIntentAppended, EventIntentRecoveryRequired, "appended event bytes were not found during recovery"); err != nil {
				return err
			}
			return fmt.Errorf("event intent %s is missing appended bytes", intent.ID)
		}
		if intent.State == EventIntentPrepared {
			if err := markEventIntent(ctx, sessionDir, spec, EventIntentPrepared, EventIntentAppended, ""); err != nil {
				return err
			}
		}
		if err := CommitEventIntent(ctx, sessionDir, spec, &batch, func(tx *dao.Tx) error {
			if finalize == nil {
				return nil
			}
			return finalize(tx, batch)
		}); err != nil {
			return err
		}
	}
	return nil
}

func adoptEventIntent(ctx context.Context, sessionDir string, spec EventIntentSpec) error {
	now := time.Now().Unix()
	return WriteRootDatabase(ctx, sessionDir, func(tx *dao.Tx) error {
		rows, err := dao.NewSessionEventIntentDAO(nil).Adopt(ctx, tx, spec.ID, spec.SessionID, spec.RunID, spec.OwnerInstanceID, spec.LeaseEpoch, spec.LeaseTokenHash, now)
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrEventIntentFenced
		}
		return nil
	})
}

// InspectEventStore is the first reconciliation boundary between the file
// EventStore and SQLite. It deliberately fails closed: an orphan batch,
// missing committed bytes, or a digest/sequence mismatch is never promoted to
// canonical session content.
func InspectEventStore(ctx context.Context, sessionDir, sessionID string) (EventStoreInspection, error) {
	return inspectEventStore(ctx, sessionDir, sessionID, false)
}

// InspectEventStoreProjection is the display-only variant used by list rows.
// It validates record framing, sequence contiguity, and committed intent
// identity, but skips the per-batch SHA-256 payload check. The result is never
// used for replay or canonical writes; a corrupt payload is still rejected by
// InspectEventStore when the session is actually opened.
func InspectEventStoreProjection(ctx context.Context, sessionDir, sessionID string) (EventStoreInspection, error) {
	return inspectEventStore(ctx, sessionDir, sessionID, true)
}

func inspectEventStore(ctx context.Context, sessionDir, sessionID string, projection bool) (EventStoreInspection, error) {
	store, err := NewEventStore(sessionDir, sessionID)
	if err != nil {
		return EventStoreInspection{}, err
	}
	// Reconciliation is observational: it must never repair or materialise the
	// store, because it runs on session load without holding the writer lease.
	var physical []EventBatch
	var scanErr error
	if projection {
		physical, scanErr = store.inspectProjection()
	} else {
		physical, scanErr = store.Inspect()
	}
	inspection := EventStoreInspection{Batches: physical}
	database, err := OpenRootDB(sessionDir)
	if err != nil {
		return EventStoreInspection{}, err
	}
	intents, err := dao.NewSessionEventIntentDAO(database.Bun()).List(ctx, database.Bun(), sessionID)
	if err != nil {
		return EventStoreInspection{}, fmt.Errorf("list event intents: %w", err)
	}
	inspection.Intents = intents
	if scanErr != nil {
		inspection.State = EventStoreCorrupt
		inspection.Reason = scanErr.Error()
		return inspection, nil
	}
	byID := make(map[string]EventBatch, len(physical))
	for _, batch := range physical {
		for _, event := range batch.Events {
			if event.SessionID != sessionID {
				inspection.State = EventStoreCorrupt
				inspection.Reason = "event belongs to another session: " + event.EventID
				return inspection, nil
			}
		}
		if _, exists := byID[batch.BatchID]; exists {
			inspection.State = EventStoreCorrupt
			inspection.Reason = "duplicate event batch ID " + batch.BatchID
			return inspection, nil
		}
		byID[batch.BatchID] = batch
	}
	for _, intent := range intents {
		batch, exists := byID[intent.ID]
		if exists && (batch.SeqFrom != intent.SeqFrom || batch.SeqTo != intent.SeqTo || batch.Digest != intent.Digest) {
			inspection.State = EventStoreCorrupt
			inspection.Reason = "event intent does not match its batch: " + intent.ID
			return inspection, nil
		}
		switch intent.State {
		case EventIntentCommitted:
			if !exists {
				inspection.State = EventStoreInconsistent
				inspection.Reason = "committed event intent has no event batch: " + intent.ID
				return inspection, nil
			}
		case EventIntentPrepared, EventIntentAppended:
			if intent.State == EventIntentAppended && !exists {
				inspection.State = EventStoreInconsistent
				inspection.Reason = "appended event intent has no event batch: " + intent.ID
				return inspection, nil
			}
			inspection.State = EventStoreReconciling
		case EventIntentRecoveryRequired:
			inspection.State = EventStoreBlocked
			inspection.Reason = "event intent requires recovery: " + intent.ID
			return inspection, nil
		case EventIntentAborted:
			if exists {
				inspection.State = EventStoreBlocked
				inspection.Reason = "aborted event intent still has event bytes: " + intent.ID
				return inspection, nil
			}
		default:
			inspection.State = EventStoreBlocked
			inspection.Reason = "unknown event intent state: " + intent.State
			return inspection, nil
		}
		delete(byID, intent.ID)
	}
	if len(byID) != 0 {
		inspection.State = EventStoreBlocked
		inspection.Reason = "event store contains bytes without SQLite intent"
		return inspection, nil
	}
	if inspection.State == "" {
		inspection.State = EventStoreHealthy
	}
	return inspection, nil
}

// eventStoreWritable fails closed unless the session's EventStore may accept a
// new append. It is the write gate in front of every EventStore append.
//
// The common case is O(1). When the process-local append-state cache is fresh
// for the current physical revision — which only a validated scan or one of our
// own appends can produce — the physical log has not changed since it was last
// validated, and the fence guarantees we are its single writer, so the
// exhaustive reconciliation can only reach the same verdict: no intent is in a
// non-committed state and the committed batch count equals the physical one.
// Any staleness, or any failed cheap check, falls back to the exhaustive
// InspectEventStore, so correctness never depends on the fast path.
func eventStoreWritable(ctx context.Context, sessionDir, sessionID string, store *EventStore) error {
	if cache, fresh := store.peekAppendState(); fresh {
		db, err := OpenRootDB(sessionDir)
		if err != nil {
			return err
		}
		committed, nonCommitted, err := dao.NewSessionEventIntentDAO(db.Bun()).CommittedStateCounts(ctx, db.Bun(), sessionID)
		if err != nil {
			return err
		}
		if nonCommitted == 0 && committed == len(cache.batchIDs) {
			return nil
		}
	}
	inspection, err := InspectEventStore(ctx, sessionDir, sessionID)
	if err != nil {
		return err
	}
	if inspection.State != EventStoreHealthy {
		return fmt.Errorf("%w: cannot append while session EventStore is %s: %s", ErrEventStoreNotHealthy, inspection.State, inspection.Reason)
	}
	return nil
}
