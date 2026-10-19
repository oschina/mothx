package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/oschina/mothx/internal/dao"
)

// maxEventStoreEntriesPerBatch keeps both the append fsync and the following
// SQLite intent/projection transaction bounded. It deliberately matches the
// existing legacy SQLite batch cap so a tool-heavy iteration has the same
// worst-case fairness envelope during migration.
const maxEventStoreEntriesPerBatch = maxEntriesPerTransaction

// MigrateToEventStore is the single migration entry point for a session's
// persisted content store. Every Runtime attach path (SessionRuntime.BindSession,
// AttachSessionResources, and Builder.Build) calls exactly this method and does
// no migration logic of its own, so no entry point can diverge on whether an old
// session is migrated.
//
// It exports the legacy SQLite transcript once, under a session content lease.
// The EventStore batch is committed and indexed before legacy payload rows are
// removed, so a failed export leaves the old source intact while a successful
// migration removes the duplicate payload copy.
//
// Migration is opportunistic and must never contend with the Run a session
// serves: an attach is not an execution, and an adapter must be able to open a
// session whose run is owned elsewhere. The Runtime binds — and therefore
// migrates — a session while the durable Run that owns the session lease is still
// active, so this routine never reserves an idle session with AcquireMutation. It
// either adopts facts that are already committed (no lease at all) or fences the
// one irreversible step, the content store flip, against the lease identity this
// process already holds. When no lease exists the session is genuinely idle and
// the flip needs no fence beyond the atomic statement itself.
//
// When another owner holds the session run — or this process loses its lease
// mid-export — there is no safe window to take the irreversible step, so the
// call returns nil having done nothing: the session keeps serving from its
// self-consistent legacy store and a later attach (or the owner's own bind)
// migrates it. Only a genuinely unsafe session fails closed: an unhealthy or
// corrupt EventStore, an entry type the store cannot replay, or a database error.
//
// Convergence is idempotent, so bind does not fork it: `ConvergeEventStore` —
// which every open passes through — is called first and brings this Manager to
// the canonical event log together with the two durable repairs. When the
// session has not been flipped yet, only this entry is entitled to take the
// irreversible step, so `exportLegacyTranscript` follows.
func (m *Manager) MigrateToEventStore(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	subAgent := m.subAgent
	m.mu.Unlock()
	if subAgent {
		return errors.New("sub-agent sessions use the isolated sub_entries compatibility store")
	}
	if err := m.ConvergeEventStore(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	converged := m.eventStoreMode
	m.mu.Unlock()
	if converged {
		// A converged Manager either loaded through the EventStore already or just
		// exported the transcript in ConvergeEventStore. Either way there is no
		// irreversible step left to take.
		return nil
	}
	err := m.exportLegacyTranscript(ctx)
	if errors.Is(err, ErrRuntimeLeaseLost) || errors.Is(err, ErrSessionRunActive) {
		// Another owner is running the session, or this process lost its lease
		// during the export. Leave the still-authoritative legacy store in place and
		// let a later call migrate; the transcript is never left half-flipped.
		return nil
	}
	return err
}

// ConvergeEventStore makes this Manager read the canonical EventStore. It is the
// single expression of "this session is now open": load() calls it for every
// Manager that observes the EventStore, and MigrateToEventStore calls it before
// deciding whether an export is needed.
//
// Reading canonically is a property of the session, not of whichever entry point
// happens to call SessionRuntime.BindSession. serve/chat, the background
// coordinator, channels, ESM, and ACP all open Managers directly, so leaving the
// read to a bind side effect let those Managers replay the legacy SQLite
// duplicate of a transcript that the EventStore already owns.
//
// The caller must not hold m.mu. Convergence takes neither a lease nor a
// reservation: both durable repairs it may run are recomputations of facts the
// committed event log already states, so they are idempotent and cannot destroy
// live data. The write fence for real content changes stays inside
// appendEventStoreEntriesLocked and the Runtime terminal transaction.
func (m *Manager) ConvergeEventStore(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.convergeEventStoreLocked(ctx)
}

// convergeEventStoreLocked is the convergence body of an open. The caller
// holds m.mu.
//
// An open converges fully: it adopts the committed event log and then retires
// the stale duplicates the old store left behind. Both writes are derived
// entirely from facts that are already committed, which makes them idempotent —
// once a session is flipped nothing re-adds legacy payload rows, and the turn
// boundary repair only rewrites a row whose bounds disagree with that turn's own
// markers, skipping rows that already agree. Leaving them undone would hand the
// next fork, deletion, or recovery pass a control plane addressing sequences
// that no longer exist, so the heal belongs here rather than on some future
// bind that may never come.
//
// It is deliberately a no-op while this Manager is already converging, so a
// nested load cannot re-enter it.
func (m *Manager) convergeEventStoreLocked(ctx context.Context) error {
	if err := m.ensureInitializedLocked(); err != nil {
		return err
	}
	if m.subAgent || m.convergingEventStore {
		// Sub-agent records live in the isolated sub_entries store and never
		// select the root EventStore, so there is nothing to converge.
		return nil
	}
	sessionDir := m.sessionDirOrDerive()
	// The persisted content store, not this process's in-memory view, decides
	// where canonical content lives. A Manager opened before another path flipped
	// the session still reports the legacy mode, so re-read the column: an
	// already-flipped session converges without acquiring a lease, which is what
	// lets the Runtime bind a session while its durable Run still holds the lease.
	flipped, err := m.sessionContentStoreFlipped(ctx, sessionDir)
	if err != nil {
		return err
	}
	if !flipped {
		return nil
	}
	m.convergingEventStore = true
	defer func() { m.convergingEventStore = false }()
	return m.convergeFlippedSession(ctx, sessionDir)
}

// exportLegacyTranscript performs the one irreversible step of migration: the
// legacy SQLite transcript becomes a committed EventStore batch and the session's
// content store flips. It runs only for a Manager that still observes a legacy
// transcript after convergence, i.e. only from MigrateToEventStore.
// exportLegacyTranscript is the one irreversible migration step: it exports a
// session's legacy SQLite transcript into its EventStore and then flips the
// canonical content store. Only MigrateToEventStore calls it, after convergence
// confirmed the session has not already been flipped.
//
// The export is bounded (batches of maxEventStoreEntriesPerBatch) so one fsync
// and one SQLite projection transaction stay bounded regardless of transcript
// size, and it is self-healing: because the content store is still sqlite the
// legacy rows are authoritative, so any EventStore bytes or intents a previous
// interrupted attempt left behind are discarded before a fresh export instead of
// wedging the attach. Because the exported batches stay reversible until the
// flip, they are written under the lease identity this process already holds
// rather than reserving an idle session.
func (m *Manager) exportLegacyTranscript(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureInitializedLocked(); err != nil {
		return err
	}
	if m.eventStoreMode {
		return nil
	}
	sessionDir := m.sessionDirOrDerive()
	sessionID := m.header.ID
	// The exported transcript replaces the legacy `entries` rows, so every
	// sequence number recorded before this point stops addressing anything.
	// Rebuild the transcript order from the persisted parent chain and translate
	// the turn boundaries into the new space once the whole export has committed.
	identities, err := m.orderedSessionEntries(ctx)
	if err != nil {
		return err
	}
	if len(identities) == 0 {
		// Nothing to export. Adopting the EventStore is a single metadata change,
		// so it is fenced against the held lease (when any) rather than gated on
		// the session being idle.
		return m.flipSessionContentStore(ctx, sessionDir)
	}
	binding, release, err := resolveEventStoreExportBinding(sessionDir, sessionID)
	if err != nil {
		return err
	}
	defer release()
	if err := resetEventStoreExportAttempt(ctx, sessionDir, sessionID); err != nil {
		return err
	}

	store, err := NewEventStore(sessionDir, sessionID)
	if err != nil {
		return err
	}
	seqFrom, err := store.NextSequence()
	if err != nil {
		return err
	}
	boundaries, err := exportTurnBoundaries(ctx, sessionDir, sessionID, identities, seqFrom)
	if err != nil {
		return err
	}

	total := len(identities)
	for start := 0; start < total; start += maxEventStoreEntriesPerBatch {
		end := min(start+maxEventStoreEntriesPerBatch, total)
		records, err := exportRecordChunk(sessionID, binding.RunID, identities, seqFrom, start, end)
		if err != nil {
			return err
		}
		digest, err := DigestEventBatch(records)
		if err != nil {
			return err
		}
		spec := EventIntentSpec{
			ID: GenerateID(), SessionID: sessionID, RunID: binding.RunID,
			SeqFrom: seqFrom + int64(start), SeqTo: seqFrom + int64(end) - 1, Digest: digest,
			OwnerInstanceID: binding.OwnerInstanceID, LeaseEpoch: binding.Epoch, LeaseTokenHash: binding.TokenHash,
		}
		batch, err := AppendPreparedEventBatch(ctx, sessionDir, spec, records)
		if err != nil {
			return err
		}
		// The final chunk carries the turn-boundary translation: every sequence a
		// boundary references must already exist on disk before it commits.
		if end < total {
			if err := CommitEventIntent(ctx, sessionDir, spec, &batch, nil); err != nil {
				return err
			}
			continue
		}
		if err := CommitEventIntent(ctx, sessionDir, spec, &batch, func(tx *dao.Tx) error {
			return translateConversationTurnBoundaries(ctx, tx, sessionID, boundaries)
		}); err != nil {
			return err
		}
	}

	if err := m.flipSessionContentStore(ctx, sessionDir); err != nil {
		return err
	}
	return m.convergeFlippedSession(ctx, sessionDir)
}

// resolveEventStoreExportBinding returns the lease identity to write an export
// under, plus a release for the reservation when one had to be taken. A session
// with no live lease is idle, and an idle session is the only case where a
// mutation reservation is legitimate; a session another owner is running returns
// ErrRuntimeLeaseLost so the attach defers instead of blocking.
func resolveEventStoreExportBinding(sessionDir, sessionID string) (RuntimeLeaseBinding, func(), error) {
	held, lostCh, holding := HeldRuntimeLeaseBinding(sessionDir, sessionID)
	if !holding {
		guard, err := AcquireMutation(sessionDir, sessionID)
		if err != nil {
			if err == ErrSessionRunActive {
				return RuntimeLeaseBinding{}, func() {}, fmt.Errorf("%w: session %s has an active durable run but no lease held by this process", ErrRuntimeLeaseLost, sessionID)
			}
			return RuntimeLeaseBinding{}, func() {}, err
		}
		if leaseLost(lostCh) {
			guard.Release()
			return RuntimeLeaseBinding{}, func() {}, fmt.Errorf("%w: session lease was lost before the EventStore export", ErrRuntimeLeaseLost)
		}
		return guard.Binding(), guard.Release, nil
	}
	if leaseLost(lostCh) {
		return RuntimeLeaseBinding{}, func() {}, fmt.Errorf("%w: session lease was lost before the EventStore export", ErrRuntimeLeaseLost)
	}
	return held, func() {}, nil
}

func leaseLost(lostCh <-chan struct{}) bool {
	if lostCh == nil {
		return false
	}
	select {
	case <-lostCh:
		return true
	default:
		return false
	}
}

// resetEventStoreExportAttempt discards EventStore bytes and intents a previous
// export attempt left for a session whose content store is still sqlite. Those
// bytes are never canonical — the legacy rows are — so a stale, partial, or
// unhealthy leftover must not wedge the next attach. Intents are removed first:
// a crash between the two steps leaves bytes without intents, which the next
// attempt resets again.
func resetEventStoreExportAttempt(ctx context.Context, sessionDir, sessionID string) error {
	if err := WriteRootDatabase(ctx, sessionDir, func(tx *dao.Tx) error {
		return dao.NewSessionEventIntentDAO(nil).DeleteSession(ctx, tx, sessionID)
	}); err != nil {
		return fmt.Errorf("clear stale event intents before export: %w", err)
	}
	if err := RemoveEventStore(sessionDir, sessionID); err != nil {
		return fmt.Errorf("remove stale event store before export: %w", err)
	}
	return nil
}

// sessionDirOrDerive returns the canonical root directory holding sessions.db
// for this Manager, falling back to the directory of the resolved handle path.
// Callers must already hold m.mu.
func (m *Manager) sessionDirOrDerive() string {
	if m.sessionDir != "" {
		return m.sessionDir
	}
	return filepath.Dir(resolveDBPath(m.file))
}

// reconcileControlPlane converges the durable facts that a bind must guarantee
// once the EventStore is already this session's canonical content source.
//
// Unlike convergeFlippedSession it never touches the in-memory transcript — the
// caller loaded through the EventStore already — and it never removes legacy
// payload rows, because a session that reached this state through load() has no
// duplicate left to clean up. What it does guarantee is that the control plane
// (`conversation_turns` bounds) addresses positions that exist in the event log.
// The repair is idempotent, takes no lease, and costs one read plus at most one
// transaction, so running it on every bind stays cheap. Callers must already
// hold m.mu and pass the canonical session directory.
func (m *Manager) reconcileControlPlane(ctx context.Context, sessionDir string) error {
	if err := repairUnmappedTurnBoundaries(ctx, sessionDir, m.header.ID); err != nil {
		return fmt.Errorf("repair turn boundaries of session %s: %w", m.header.ID, err)
	}
	return nil
}

// sessionContentStoreFlipped reports whether the session already selected the
// EventStore as its canonical content source.
func (m *Manager) sessionContentStoreFlipped(ctx context.Context, sessionDir string) (bool, error) {
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return false, err
	}
	return sessionUsesEventStore(ctx, db, m.header.ID)
}

// eventStoreObservation is what an open needs to know about a session whose
// canonical content store is the EventStore: the replayed transcript and
// whether the committed log actually holds events.
type eventStoreObservation struct {
	entries   []interface{}
	hasEvents bool
}

// observeEventStoreLocked reads the canonical EventStore for this Manager
// without adopting it. The caller holds m.mu.
//
// This is the read-only half of convergence. It deliberately performs no write
// at all: an open is not a mutation, so it must never truncate uncommitted
// bytes, remove a legacy payload duplicate, or take a lease. Whatever it
// observes becomes durable only when a caller that legitimately owns the write
// (an append, the Runtime terminal transaction, or an export under a content
// lease) applies it through applyEventStoreObservationLocked.
func (m *Manager) observeEventStoreLocked(ctx context.Context) (eventStoreObservation, error) {
	eventEntries, useEventStore, err := m.loadCommittedEventEntries(ctx, m.header.ID)
	if err != nil {
		return eventStoreObservation{}, err
	}
	if !useEventStore {
		return eventStoreObservation{}, fmt.Errorf("session %s content store is the EventStore but its committed events cannot be replayed", m.header.ID)
	}
	return eventStoreObservation{entries: eventEntries, hasEvents: len(eventEntries) > 0}, nil
}

// applyEventStoreObservationLocked adopts an observed EventStore into this
// Manager's in-memory view. The caller holds m.mu.
func (m *Manager) applyEventStoreObservationLocked(observed eventStoreObservation) {
	replayed := make([]interface{}, 0, len(observed.entries)+1)
	replayed = append(replayed, *m.header)
	replayed = append(replayed, observed.entries...)
	m.entries = replayed
	m.leafID = nil
	for _, entry := range observed.entries {
		id, _, _, _ := getEntryMetadata(entry)
		if id != "" {
			leaf := id
			m.leafID = &leaf
		}
	}
	m.eventStoreMode = true
}

// convergeFlippedSession makes this Manager observe the EventStore as the
// session's canonical content. It replays the committed events into the local
// view and then removes the duplicated legacy payload rows. Both steps are
// idempotent convergence of facts that are already committed, so neither takes
// a lease or a reservation: the write fence for real content changes stays
// inside AppendEventStoreEntries and the Runtime terminal transaction.
func (m *Manager) convergeFlippedSession(ctx context.Context, sessionDir string) error {
	observed, err := m.observeEventStoreLocked(ctx)
	if err != nil {
		return err
	}
	sessionID := m.header.ID
	m.applyEventStoreObservationLocked(observed)
	if !observed.hasEvents {
		// Nothing was ever exported, so there is no duplicate payload to remove.
		// Skipping the delete keeps the common new-session bind free of a write
		// transaction while a durable Run is active.
		return nil
	}
	if err := WriteRootDatabase(ctx, sessionDir, func(tx *dao.Tx) error {
		return dao.NewSessionEventProjectionDAO(nil).DeleteLegacyEntries(ctx, tx, sessionID)
	}); err != nil {
		return fmt.Errorf("remove legacy transcript payload after EventStore commit: %w", err)
	}
	return m.reconcileControlPlane(ctx, sessionDir)
}

// repairUnmappedTurnBoundaries retargets every turn whose recorded bounds do not
// agree with the turn's own markers in the canonical EventStore.
//
// Sessions flipped by a build that predates the migration remap keep the legacy
// `entries.seq` numbers: the markers moved into the EventStore but the control
// rows were never translated. Such a bound silently addresses nothing, which
// makes fork resolution drop the turn and recovery mis-read the transcript.
// Convergence re-runs on every bind, so repairing here heals that population
// without a new schema version or a data migration over sessions.db.
//
// The comparison is by identity, not by range: a legacy sequence can coincide
// with an unrelated EventStore position, so "does this number fit in the new
// space" proves nothing. Each turn is matched against the `turn_start`/`turn_end`
// marker that carries its own turn ID, and only those two positions are trusted.
// A turn with no matching marker stays untouched — guessing a boundary would
// fabricate a fork point the transcript never had.
func repairUnmappedTurnBoundaries(ctx context.Context, sessionDir, sessionID string) error {
	batches, err := ReadCommittedEventBatches(ctx, sessionDir, sessionID)
	if err != nil {
		return err
	}
	markerSeqs := make(map[string]int64, len(batches)*2)
	for _, batch := range batches {
		for i, event := range batch.Events {
			if event.Kind != string(EntryTurnStart) && event.Kind != string(EntryTurnEnd) {
				continue
			}
			decoded, err := DecodeEventRecord(event)
			if err != nil {
				return err
			}
			// The marker payload is the authority for which logical turn it bounds;
			// the derived projection column only mirrors it and was left empty by an
			// earlier build, so reading the canonical bytes also heals those rows.
			turnID := exportMarkerTurnID(decoded)
			if turnID == "" {
				continue
			}
			key := turnID + "\x00" + event.Kind
			if _, exists := markerSeqs[key]; !exists {
				markerSeqs[key] = batch.SeqFrom + int64(i)
			}
		}
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	rows, err := dao.NewConversationTurnDAO(db.Bun()).List(ctx, sessionID)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	boundaries := make([]conversationTurnTranslation, 0, len(rows))
	for _, row := range rows {
		newStart, okStart := markerSeqs[row.ID+"\x00"+string(EntryTurnStart)]
		if !okStart {
			// No marker carries this turn identity, so nothing can be inferred.
			continue
		}
		if row.EndSeq == nil {
			if newStart == row.StartSeq {
				continue
			}
			boundaries = append(boundaries, conversationTurnTranslation{turnID: row.ID, legacyStart: row.StartSeq, hasEnd: false, startSeq: newStart})
			continue
		}
		newEnd, okEnd := markerSeqs[row.ID+"\x00"+string(EntryTurnEnd)]
		if !okEnd {
			continue
		}
		if newStart == row.StartSeq && newEnd == *row.EndSeq {
			continue
		}
		boundaries = append(boundaries, conversationTurnTranslation{turnID: row.ID, legacyStart: row.StartSeq, legacyEnd: *row.EndSeq, hasEnd: true, startSeq: newStart, endSeq: newEnd})
	}
	if len(boundaries) == 0 {
		return nil
	}
	return WriteRootDatabase(ctx, sessionDir, func(tx *dao.Tx) error {
		return translateConversationTurnBoundaries(ctx, tx, sessionID, boundaries)
	})
}

// flipSessionContentStore makes the EventStore the canonical content source for
// the session. It runs only after the exported batch has committed, so a crash
// before it leaves the legacy transcript authoritative and a later attempt
// re-migrates from scratch.
//
// This is the one irreversible step of the migration, so it is fenced rather
// than reserved. When this process holds the session lease — the normal case
// while a durable Run is executing — the update also requires that exact
// owner/epoch/token row to still be active, which makes a concurrent takeover
// lose the race. With no lease the session is idle and a plain set is enough.
//
// On success the Manager's in-memory view is switched to the EventStore as well,
// so callers cannot leave a flipped session still writing through the legacy
// path (that divergence would strand the transcript with no committed batch).
func (m *Manager) flipSessionContentStore(ctx context.Context, sessionDir string) error {
	sessionID := m.header.ID
	binding, _, holding := HeldRuntimeLeaseBinding(sessionDir, sessionID)
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	err = db.RunInTx(ctx, nil, func(_ context.Context, tx dao.Tx) error {
		sessionDAO := dao.NewSessionDAO(nil)
		if !holding {
			return sessionDAO.SetContentStore(ctx, &tx, sessionID, "event_store")
		}
		affected, err := sessionDAO.FlipContentStoreOnLease(ctx, &tx, sessionID, "event_store", binding.OwnerInstanceID, binding.Epoch, binding.TokenHash)
		if err != nil {
			return err
		}
		if affected != 1 {
			return fmt.Errorf("%w: session lease changed before the content store flip", ErrRuntimeLeaseLost)
		}
		return nil
	})
	if err != nil {
		return err
	}
	m.eventStoreMode = true
	return nil
}

// AppendEventStoreEntries appends a complete bounded batch to the canonical
// session EventStore. It is the Runtime-owned write entry point for sessions
// that have opted into the new content store; the legacy AppendMessage APIs
// intentionally remain SQLite-compatible until their callers migrate.
func (m *Manager) AppendEventStoreEntries(ctx context.Context, runID, turnID string, entries []interface{}) ([]string, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.subAgent {
		return nil, errors.New("sub-agent sessions cannot write the root EventStore")
	}
	return m.appendEventStoreEntriesLocked(ctx, runID, turnID, entries)
}

func (m *Manager) appendEventStoreEntriesLocked(ctx context.Context, runID, turnID string, entries []interface{}) ([]string, error) {
	if err := m.ensureInitializedLocked(); err != nil {
		return nil, err
	}
	if len(entries) > maxEventStoreEntriesPerBatch {
		ids := make([]string, 0, len(entries))
		for start := 0; start < len(entries); start += maxEventStoreEntriesPerBatch {
			end := min(start+maxEventStoreEntriesPerBatch, len(entries))
			chunkIDs, err := m.appendEventStoreEntriesLocked(ctx, runID, turnID, entries[start:end])
			if err != nil {
				return nil, err
			}
			ids = append(ids, chunkIDs...)
		}
		return ids, nil
	}
	if m.eventStoreMode {
		// Continue below; the in-memory entry chain is the local optimistic view.
	} else if len(m.entries) != 0 {
		return nil, errors.New("cannot enable EventStore for a legacy session with existing SQLite entries")
	}
	sessionID := m.header.ID
	sessionDir := m.sessionDir
	if sessionDir == "" {
		sessionDir = filepath.Dir(resolveDBPath(m.file))
	}
	store, err := NewEventStore(sessionDir, sessionID)
	if err != nil {
		return nil, err
	}
	if err := eventStoreWritable(ctx, sessionDir, sessionID, store); err != nil {
		return nil, err
	}

	binding, release, err := eventStoreWriteBinding(sessionDir, sessionID, runID)
	if err != nil {
		return nil, err
	}
	defer release()
	if runID == "" {
		runID = binding.RunID
	}

	records := make([]EventRecord, len(entries))
	ids := make([]string, len(entries))
	for i, entry := range entries {
		id, _, parentID, _ := getEntryMetadata(entry)
		if id == "" {
			return nil, fmt.Errorf("event entry %d has no ID", i)
		}
		if i == 0 && parentID != nil && m.leafID != nil && *parentID != *m.leafID {
			return nil, fmt.Errorf("%w: expected leaf %q, event entry parent %q", ErrSessionModified, *m.leafID, *parentID)
		}
		record, err := EventRecordFromEntry(entry, sessionID, runID, turnID)
		if err != nil {
			return nil, err
		}
		records[i] = record
		ids[i] = id
	}
	// A process may crash after the EventStore batch commits but before its
	// caller observes success. Stable entry IDs make a retry idempotent; reject
	// partial or payload-divergent retries rather than duplicating transcript
	// entries or silently choosing one version. An incoming ID can only already
	// be committed on such a retry, so the projection index answers the common
	// case in one probe and the full committed transcript is read only then.
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	retry, err := dao.NewSessionEventProjectionDAO(nil).AnyIDPresent(ctx, db.Bun(), sessionID, ids)
	if err != nil {
		return nil, err
	}
	if retry {
		committed, err := ReadCommittedEventBatches(ctx, sessionDir, sessionID)
		if err != nil {
			return nil, err
		}
		existing := make(map[string]EventRecord)
		for _, committedBatch := range committed {
			for _, record := range committedBatch.Events {
				existing[record.EventID] = record
			}
		}
		matched := 0
		for _, record := range records {
			if old, ok := existing[record.EventID]; ok {
				oldBytes, _ := json.Marshal(old)
				newBytes, _ := json.Marshal(record)
				if !bytes.Equal(oldBytes, newBytes) {
					return nil, fmt.Errorf("event entry %s already exists with different content", record.EventID)
				}
				matched++
			}
		}
		if matched > 0 {
			if matched != len(records) {
				return nil, errors.New("event batch retry overlaps only part of an existing committed batch")
			}
			for _, entry := range entries {
				m.entries = append(m.entries, entry)
			}
			lastID := ids[len(ids)-1]
			m.leafID = &lastID
			m.eventStoreMode = true
			return ids, nil
		}
	}
	seqFrom, err := store.NextSequence()
	if err != nil {
		return nil, err
	}
	digest, err := DigestEventBatch(records)
	if err != nil {
		return nil, err
	}
	spec := EventIntentSpec{
		ID: GenerateID(), SessionID: sessionID, RunID: runID, TurnID: turnID,
		SeqFrom: seqFrom, SeqTo: seqFrom + int64(len(records)) - 1, Digest: digest,
		OwnerInstanceID: binding.OwnerInstanceID, LeaseEpoch: binding.Epoch, LeaseTokenHash: binding.TokenHash,
	}
	batch, err := AppendPreparedEventBatch(ctx, sessionDir, spec, records)
	if err != nil {
		return nil, err
	}
	if err := CommitEventIntent(ctx, sessionDir, spec, &batch, nil); err != nil {
		return nil, err
	}
	for _, entry := range entries {
		m.entries = append(m.entries, entry)
	}
	lastID := ids[len(ids)-1]
	m.leafID = &lastID
	m.eventStoreMode = true
	return ids, nil
}

func eventStoreWriteBinding(sessionDir, sessionID, runID string) (RuntimeLeaseBinding, func(), error) {
	if binding, ok := CurrentRuntimeLeaseBinding(sessionDir, sessionID); ok {
		if runID != "" && binding.RunID != "" && binding.RunID != runID {
			return RuntimeLeaseBinding{}, nil, ErrRuntimeLeaseRunMismatch
		}
		return binding, func() {}, nil
	}
	// No Runtime lease is held by this process. Content appends must keep the
	// legacy compatibility contract: a single cross-process writer, but no
	// idle-session requirement, because direct Session Manager callers (serve
	// background runs, channel finalizers, attachment archiving) legitimately
	// append while their own durable Run is still open. AcquireMutation would
	// refuse such a session with ErrSessionRunActive and freeze that turn.
	guard, err := AcquireContentWriteReservation(sessionDir, sessionID)
	if err != nil {
		return RuntimeLeaseBinding{}, nil, err
	}
	binding := guard.Binding()
	return binding, guard.Release, nil
}

// conversationTurnTranslation is one turn's boundary interval expressed in both
// sequence spaces: `legacyStart`/`legacyEnd` address rows of the shared SQLite
// `entries` table, `startSeq`/`endSeq` address committed events of the session
// EventStore.
type conversationTurnTranslation struct {
	turnID      string
	legacyStart int64
	legacyEnd   int64
	hasEnd      bool
	startSeq    int64
	endSeq      int64
}

// planEventStoreExport assigns every exported entry its EventStore sequence and
// reports the turn boundaries that must follow the transcript into the new
// sequence space.
//
// The legacy numbering dies with the export: `entries.seq` is a database-global
// AUTOINCREMENT, so once the payload rows are gone the recorded
// `conversation_turns.start_seq`/`end_seq` values address nothing and would
// silently point fork resolution and recovery at unrelated or nonexistent
// positions. Each bound names the `turn_start`/`turn_end` row that opened or
// closed the turn, so the translation walks
// legacy seq -> that row's entry ID -> the ID's new EventStore sequence.
//
// Identities come from the persisted transcript rather than the Manager's
// in-memory view because the standalone turn helpers append their marker rows
// straight to the database without touching `m.entries`.
//
// A turn whose start marker is absent from the export keeps its interval
// untouched: it addressed a row that no longer exists either way, and inventing
// a bound would fabricate a fork boundary the transcript never had. An open
// turn (`end_seq IS NULL`) translates its start bound only, and leaves the end
// NULL for the live Runtime path to close in the new space.

// exportMarkerIdentity returns the logical Run/Turn identity carried by a turn
// boundary marker payload. Every other entry type exports as unowned content, so
// it contributes empty identity.
func exportMarkerIdentity(entry interface{}) (runID, turnID string) {
	switch marker := entry.(type) {
	case TurnStartEntry:
		return marker.RunID, marker.TurnID
	case *TurnStartEntry:
		return marker.RunID, marker.TurnID
	case TurnEndEntry:
		return marker.RunID, marker.TurnID
	case *TurnEndEntry:
		return marker.RunID, marker.TurnID
	default:
		return "", ""
	}
}

// exportMarkerTurnID is the turn-only half of exportMarkerIdentity, used by the
// repair path that needs to know which logical turn a committed marker bounds.
func exportMarkerTurnID(entry interface{}) string {
	_, turnID := exportMarkerIdentity(entry)
	return turnID
}

// exportRecordChunk converts identities[start:end] into export records; the
// chunk occupies sequences seqFrom+start .. seqFrom+end-1. Building records one
// bounded chunk at a time is what keeps the export's peak memory to a single
// batch of marshaled payloads instead of a second full copy of the transcript.
func exportRecordChunk(sessionID, runID string, identities []sessionEntryIdentity, seqFrom int64, start, end int) ([]EventRecord, error) {
	records := make([]EventRecord, 0, end-start)
	for i := start; i < end; i++ {
		identity := identities[i]
		// The exported batch is unowned, but a turn boundary marker carries its
		// own logical identity in the payload. Stamping it here is what lets the
		// projection row address the turn afterwards; without it every migrated
		// marker would be indistinguishable from an ordinary append.
		markerRunID, turnID := exportMarkerIdentity(identity.self)
		record, err := EventRecordFromEntry(identity.self, sessionID, markerRunID, turnID)
		if err != nil {
			return nil, err
		}
		// The batch envelope carries the exporting owner's run, not the marker's.
		record.RunID = runID
		records = append(records, record)
	}
	return records, nil
}

// exportTurnBoundaries plans the conversation_turn translations for an export
// whose entries start at seqFrom. It retains only identity metadata, so it never
// holds a second copy of the message payloads.
func exportTurnBoundaries(ctx context.Context, sessionDir, sessionID string, identities []sessionEntryIdentity, seqFrom int64) ([]conversationTurnTranslation, error) {
	seqByEntryID := make(map[string]int64, len(identities))
	// Legacy sequence -> entry ID, so a recorded turn bound can be resolved to
	// the exported sequence of the very row it pointed at.
	entryIDByLegacySeq := make(map[int64]string, len(identities))
	for i, identity := range identities {
		seqByEntryID[identity.id] = seqFrom + int64(i)
		if existing, duplicate := entryIDByLegacySeq[identity.seq]; duplicate && existing != identity.id {
			// Two rows of one session claiming the same legacy sequence means the
			// transcript itself is inconsistent; refuse rather than pick one.
			return nil, fmt.Errorf("legacy transcript has entries %s and %s both at sequence %d", existing, identity.id, identity.seq)
		}
		entryIDByLegacySeq[identity.seq] = identity.id
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	rows, err := dao.NewConversationTurnDAO(db.Bun()).List(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	boundaries := make([]conversationTurnTranslation, 0, len(rows))
	for _, row := range rows {
		startID, okStartRow := entryIDByLegacySeq[row.StartSeq]
		if !okStartRow {
			// The bound addresses a row this session never exported (already
			// dangling, or a header row). Leave the interval alone.
			continue
		}
		newStart, okStart := seqByEntryID[startID]
		if !okStart {
			continue
		}
		translation := conversationTurnTranslation{turnID: row.ID, legacyStart: row.StartSeq, hasEnd: false, startSeq: newStart}
		if row.EndSeq != nil {
			endID, okEndRow := entryIDByLegacySeq[*row.EndSeq]
			if !okEndRow {
				continue
			}
			newEnd, okEnd := seqByEntryID[endID]
			if !okEnd {
				continue
			}
			translation.legacyEnd, translation.endSeq, translation.hasEnd = *row.EndSeq, newEnd, true
		}
		boundaries = append(boundaries, translation)
	}
	return boundaries, nil
}

// translateConversationTurnBoundaries rewrites the exported turns' boundary
// intervals into the EventStore sequence space. It runs inside the commit
// transaction, so the projection rows and the retargeted intervals become
// visible together; a crash before commit leaves the legacy numbers next to the
// legacy rows, which is still a consistent pair.
func translateConversationTurnBoundaries(ctx context.Context, tx *dao.Tx, sessionID string, boundaries []conversationTurnTranslation) error {
	if len(boundaries) == 0 {
		return nil
	}
	turnDAO := dao.NewConversationTurnDAO(nil)
	for _, boundary := range boundaries {
		var endSeq *int64
		if boundary.hasEnd {
			end := boundary.endSeq
			endSeq = &end
		}
		if _, err := turnDAO.TranslateBoundaries(ctx, tx, sessionID, boundary.turnID, boundary.startSeq, endSeq); err != nil {
			return fmt.Errorf("translate conversation turn %s to event store sequences: %w", boundary.turnID, err)
		}
	}
	return nil
}
