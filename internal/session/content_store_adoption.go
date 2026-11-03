package session

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/oschina/mothx/internal/dao"
)

// ErrSessionStoreBroken reports a split store that cannot be adopted: a legacy
// row references an ancestor that exists in neither the event log nor the delta.
// The delta is the only content the event log does not own, so guessing a parent
// would fabricate history; the repair refuses and keeps every legacy row.
var ErrSessionStoreBroken = errors.New("legacy transcript cannot be adopted into the event store")

// AdoptionResult reports what AdoptLegacyContent changed.
type AdoptionResult struct {
	// AdoptedEntries is how many legacy payload rows were appended to the event
	// log (the delta a build that still writes the legacy transcript left behind).
	AdoptedEntries int
	// RetiredEntries is how many now-covered duplicate rows were removed from the
	// legacy transcript after adoption.
	RetiredEntries int64
	// Deferred means another owner holds the session or this process lost its
	// lease, so nothing was changed and a later call retries.
	Deferred bool
}

// AdoptLegacyContent reconciles a split store: it appends the legacy payload rows
// the event log does not cover into the canonical log, then retires the duplicate.
//
// This is the repair for sessions migrated while a build that still writes the
// legacy transcript was alive (see docs/notes/20261101-session-content-store-split-brain.md).
// It is additive and idempotent:
//   - only uncovered rows are appended, and entry identity survives export, so a
//     crash halfway leaves the rest adoptable on the next call;
//   - nothing is reset or deleted before the bytes are committed;
//   - it runs under the session's content lease and defers while another owner is
//     executing the session.
func (m *Manager) AdoptLegacyContent(ctx context.Context) (AdoptionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureInitializedLocked(); err != nil {
		return AdoptionResult{}, err
	}
	if m.subAgent {
		return AdoptionResult{}, errors.New("sub-agent sessions use the isolated sub_entries compatibility store")
	}
	sessionDir := m.sessionDirOrDerive()
	sessionID := m.header.ID
	result, err := m.adoptLegacyDeltaLocked(ctx, sessionDir, sessionID)
	if err != nil {
		return result, err
	}
	if result.AdoptedEntries > 0 {
		// The canonical log grew: refresh this handle so it stops replaying the
		// pre-adoption view.
		if reloadErr := m.reloadLocked(ctx); reloadErr != nil {
			log.Printf("[session] %s: reload after legacy adoption: %v", sessionID, reloadErr)
		}
	}
	return result, nil
}

// adoptLegacyDeltaLocked is the adoption body. The caller holds m.mu.
func (m *Manager) adoptLegacyDeltaLocked(ctx context.Context, sessionDir, sessionID string) (AdoptionResult, error) {
	if sessionDir == "" || sessionID == "" {
		return AdoptionResult{}, fmt.Errorf("session directory and ID are required")
	}
	// A session that still selects the legacy store has one authoritative
	// transcript; there is no delta to adopt and the migration path owns it.
	flipped, err := m.sessionContentStoreFlipped(ctx, sessionDir)
	if err != nil {
		return AdoptionResult{}, err
	}
	if !flipped {
		return AdoptionResult{}, fmt.Errorf("%w: session %s does not select the event store", ErrLegacyPayloadSplit, sessionID)
	}

	identities, err := m.orderedSessionEntries(ctx)
	if err != nil {
		return AdoptionResult{}, err
	}
	if len(identities) == 0 {
		return AdoptionResult{}, nil
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return AdoptionResult{}, err
	}
	projectionDAO := dao.NewSessionEventProjectionDAO(nil)
	uncoveredIDs, err := projectionDAO.UncoveredLegacyEntryIDs(ctx, db.Bun(), sessionID)
	if err != nil {
		return AdoptionResult{}, fmt.Errorf("read legacy transcript delta: %w", err)
	}
	if len(uncoveredIDs) == 0 {
		// Already converged content-wise; retire the duplicate if this process may
		// act, and defer otherwise.
		retired, retireErr := retireLegacyPayloadRows(ctx, sessionDir, sessionID, true)
		if retireErr != nil {
			if isLeaseDeferral(retireErr) {
				return AdoptionResult{Deferred: true}, nil
			}
			return AdoptionResult{}, retireErr
		}
		return AdoptionResult{RetiredEntries: retired}, nil
	}

	deltaSet := make(map[string]struct{}, len(uncoveredIDs))
	for _, id := range uncoveredIDs {
		deltaSet[id] = struct{}{}
	}
	delta := make([]sessionEntryIdentity, 0, len(uncoveredIDs))
	for _, identity := range identities {
		if _, ok := deltaSet[identity.id]; ok {
			delta = append(delta, identity)
		}
	}
	if len(delta) == 0 {
		return AdoptionResult{}, nil
	}
	// A delta row may only reference a parent that is already canonical or part of
	// the delta itself. Anything else means the deleted ancestor is gone for good;
	// inventing a parent would fabricate history, so refuse and keep every row.
	for _, identity := range delta {
		_, _, parentID, _ := getEntryMetadata(identity.self)
		if parentID == nil || *parentID == "" {
			continue
		}
		if _, inDelta := deltaSet[*parentID]; inDelta {
			continue
		}
		covered, probeErr := projectionDAO.AnyIDPresent(ctx, db.Bun(), sessionID, []string{*parentID})
		if probeErr != nil {
			return AdoptionResult{}, fmt.Errorf("probe legacy delta parent: %w", probeErr)
		}
		if !covered {
			return AdoptionResult{}, fmt.Errorf("%w: entry %s references missing parent %s", ErrSessionStoreBroken, identity.id, *parentID)
		}
	}

	// Adoption is content-plane work: one writer, and never while another owner is
	// running the session.
	binding, release, err := resolveEventStoreExportBinding(sessionDir, sessionID)
	if err != nil {
		if isLeaseDeferral(err) {
			return AdoptionResult{Deferred: true}, nil
		}
		return AdoptionResult{}, err
	}
	defer release()

	store, err := NewEventStore(sessionDir, sessionID)
	if err != nil {
		return AdoptionResult{}, err
	}
	// Fail closed on a log another writer left in the prepared/appended window:
	// the delta must not be appended next to in-doubt bytes.
	if err := eventStoreWritable(ctx, sessionDir, sessionID, store); err != nil {
		return AdoptionResult{}, err
	}
	seqFrom, err := store.NextSequence()
	if err != nil {
		return AdoptionResult{}, err
	}
	total := len(delta)
	for start := 0; start < total; start += maxEventStoreEntriesPerBatch {
		end := min(start+maxEventStoreEntriesPerBatch, total)
		records, err := exportRecordChunk(sessionID, binding.RunID, delta, seqFrom, start, end)
		if err != nil {
			return AdoptionResult{}, err
		}
		digest, err := DigestEventBatch(records)
		if err != nil {
			return AdoptionResult{}, err
		}
		spec := EventIntentSpec{
			ID: GenerateID(), SessionID: sessionID, RunID: binding.RunID,
			SeqFrom: seqFrom + int64(start), SeqTo: seqFrom + int64(end) - 1, Digest: digest,
			OwnerInstanceID: binding.OwnerInstanceID, LeaseEpoch: binding.Epoch, LeaseTokenHash: binding.TokenHash,
		}
		batch, err := AppendPreparedEventBatch(ctx, sessionDir, spec, records)
		if err != nil {
			return AdoptionResult{AdoptedEntries: start}, err
		}
		if err := CommitEventIntent(ctx, sessionDir, spec, &batch, nil); err != nil {
			return AdoptionResult{AdoptedEntries: start}, err
		}
	}
	// The delta's turn markers are committed now, so the identity-based repair can
	// retarget their control-plane bounds out of the legacy sequence space.
	batches, err := ReadCommittedEventBatches(ctx, sessionDir, sessionID)
	if err != nil {
		return AdoptionResult{AdoptedEntries: total}, fmt.Errorf("read committed transcript after adoption: %w", err)
	}
	if err := repairUnmappedTurnBoundaries(ctx, sessionDir, sessionID, batches); err != nil {
		return AdoptionResult{AdoptedEntries: total}, fmt.Errorf("repair turn boundaries after adoption: %w", err)
	}
	retired, err := retireLegacyPayloadRows(ctx, sessionDir, sessionID, true)
	if err != nil {
		if isLeaseDeferral(err) {
			return AdoptionResult{AdoptedEntries: total, Deferred: true}, nil
		}
		return AdoptionResult{AdoptedEntries: total}, err
	}
	log.Printf("[session] %s: adopted %d legacy transcript row(s) into the event store and retired %d duplicate row(s)", sessionID, total, retired)
	return AdoptionResult{AdoptedEntries: total, RetiredEntries: retired}, nil
}

// isLeaseDeferral reports the lease outcomes that mean "another owner is running
// the session" rather than a real failure.
func isLeaseDeferral(err error) bool {
	return errors.Is(err, ErrRuntimeLeaseLost) ||
		errors.Is(err, ErrSessionRunActive) ||
		errors.Is(err, ErrRuntimeLeaseBusy)
}

// ListSplitContentStoreSessions reports every session whose event log does not
// cover all of its legacy payload rows. It is the read-only entry point behind
// the doctor check and the repair enumeration; adapters render it and must not
// query the tables themselves.
func ListSplitContentStoreSessions(sessionDir string) ([]SplitStoreSession, error) {
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	records, err := dao.NewSessionEventProjectionDAO(nil).SplitContentStoreSessions(context.Background(), db.Bun())
	if err != nil {
		return nil, err
	}
	sessions := make([]SplitStoreSession, 0, len(records))
	for _, record := range records {
		sessions = append(sessions, SplitStoreSession{SessionID: record.SessionID, CWD: record.CWD, UncoveredRows: record.Uncovered})
	}
	return sessions, nil
}

// SplitStoreSession is one session whose canonical EventStore is missing content
// that only its legacy SQLite transcript still holds.
type SplitStoreSession struct {
	SessionID     string
	CWD           string
	UncoveredRows int64
}

// UnmappedBoundarySession is one session whose canonical EventStore is the
// transcript authority but whose conversation-turn control rows no longer
// address a matching turn marker.
//
// This is the residue of a flip performed by a build that predates the boundary
// remap, or of content loss the repair could not recover: the turn lifecycle
// facts survived while the marker that bounds them did not. Convergence repairs
// what it can on bind, but a turn with no marker at all cannot be inferred
// without fabricating a fork point, so it stays and has to be reported.
type UnmappedBoundarySession struct {
	SessionID     string
	CWD           string
	UnmappedTurns int64
}

// ListUnmappedTurnBoundarySessions reports every session whose turn boundaries
// address no marker of their own identity. It is the read-only entry point behind
// the doctor check; it never writes and never opens a session, so it is safe to
// run while another process serves the database.
func ListUnmappedTurnBoundarySessions(sessionDir string) ([]UnmappedBoundarySession, error) {
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	rows, err := dao.NewConversationTurnDAO(nil).UnmappedTurnBoundarySessions(context.Background(), db.Bun())
	if err != nil {
		return nil, err
	}
	sessions := make([]UnmappedBoundarySession, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, UnmappedBoundarySession{
			SessionID: row.SessionID, CWD: row.CWD, UnmappedTurns: row.UnmappedTurns,
		})
	}
	return sessions, nil
}

// AdoptLegacyContentByID opens a persisted session and adopts its split-store
// delta. It is the session-layer entry point for the doctor repair, so the
// command line never manipulates the transcript or the lease itself.
func AdoptLegacyContentByID(ctx context.Context, sessionDir, sessionID string) (AdoptionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	manager, err := OpenByIDExact(sessionDir, sessionID)
	if err != nil {
		return AdoptionResult{}, err
	}
	return manager.AdoptLegacyContent(ctx)
}
