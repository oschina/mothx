package session

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// EventStore is the session-scoped append-only content store. It is not an
// ownership mechanism: callers must already hold the Runtime fenced lease.
// SQLite intent/commit records remain the authority for deciding which batches
// are canonical; Scan only reports physically complete batches.
type EventStore struct {
	mu   *sync.Mutex
	root string
}

// EventManifest records which sealed segments are part of the current
// rebuildable physical layout. It is never canonical replay state: SQLite
// intents decide committed batches, and a missing/stale manifest can be
// reconstructed by scanning the segment directory.
type EventManifest struct {
	SchemaVersion int      `json:"schemaVersion"`
	Active        []string `json:"active"`
	NextSeq       int64    `json:"nextSeq"`
	UpdatedAt     string   `json:"updatedAt,omitempty"`
}

var eventStoreLocks sync.Map // map[string]*sync.Mutex, one defensive writer lock per path

type eventStoreAppendCache struct {
	size     int64
	modTime  time.Time
	nextSeq  int64
	batchIDs map[string]struct{}
	eventIDs map[string]struct{}
}

var eventStoreAppendCaches sync.Map  // map[events.jsonl]*eventStoreAppendCache
var eventStoreSegmentCaches sync.Map // map[event-store root]*eventStoreAppendCache

// eventStoreRollBytes overrides the segment size ceiling for tests that need to
// exercise rotation without writing tens of megabytes. Zero means production
// default. It is process-local telemetry-style tuning only and never changes
// sequence, digest, or commit semantics.
var eventStoreRollBytes atomic.Int64

var eventStoreStats = struct {
	appendBatches atomic.Uint64
	appendEvents  atomic.Uint64
	appendBytes   atomic.Uint64
	cacheHits     atomic.Uint64
	cacheMisses   atomic.Uint64
	fullScans     atomic.Uint64
}{}

// EventStoreStats is a process-local write-pressure snapshot. It is advisory
// telemetry only: SQLite intents and event files remain the durable facts.
type EventStoreStats struct {
	AppendBatches uint64
	AppendEvents  uint64
	AppendBytes   uint64
	CacheHits     uint64
	CacheMisses   uint64
	// FullScans counts whole-log physical scans. A steady-state append path must
	// not grow this with transcript size; it exists to pin that the hot path is
	// O(1) instead of re-reading the ever-growing log per append.
	FullScans uint64
}

func EventStoreWriteStats() EventStoreStats {
	return EventStoreStats{
		AppendBatches: eventStoreStats.appendBatches.Load(),
		AppendEvents:  eventStoreStats.appendEvents.Load(),
		AppendBytes:   eventStoreStats.appendBytes.Load(),
		CacheHits:     eventStoreStats.cacheHits.Load(),
		CacheMisses:   eventStoreStats.cacheMisses.Load(),
		FullScans:     eventStoreStats.fullScans.Load(),
	}
}

// EventRecord is the front-end-neutral content event persisted by EventStore.
// Payload is intentionally opaque here; Agent Runtime owns its schema.
type EventRecord struct {
	EventID   string          `json:"eventId"`
	SessionID string          `json:"sessionId"`
	RunID     string          `json:"runId,omitempty"`
	TurnID    string          `json:"turnId,omitempty"`
	Kind      string          `json:"kind"`
	ParentSeq int64           `json:"parentSeq,omitempty"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"createdAt"`
}

// EventBatch is a physically complete append batch. It is still pending until
// the SQLite control plane records its fenced commit marker.
type EventBatch struct {
	BatchID string
	SeqFrom int64
	SeqTo   int64
	Events  []EventRecord
	Digest  string
}

type eventStoreLine struct {
	Kind      string          `json:"kind"`
	BatchID   string          `json:"batchId"`
	Seq       int64           `json:"seq,omitempty"`
	SeqFrom   int64           `json:"seqFrom,omitempty"`
	SeqTo     int64           `json:"seqTo,omitempty"`
	Index     int             `json:"index,omitempty"`
	Count     int             `json:"count,omitempty"`
	Digest    string          `json:"digest,omitempty"`
	EventID   string          `json:"eventId,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	RunID     string          `json:"runId,omitempty"`
	TurnID    string          `json:"turnId,omitempty"`
	EventKind string          `json:"eventKind,omitempty"`
	ParentSeq int64           `json:"parentSeq,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt time.Time       `json:"createdAt,omitempty"`
}

const (
	eventLineBatchBegin   = "batch_begin"
	eventLineEvent        = "event"
	eventLineBatchFooter  = "batch_footer"
	eventStoreSegmentName = "events.jsonl"
	eventStoreSegmentsDir = "segments"
	eventStoreManifest    = "manifest.json"
	// A segment is sealed by size, not by time: rotation only bounds how much
	// physical data a single rescan/repair must read. Sequences stay global.
	maxEventStoreSegmentBytes = 32 << 20
	maxEventStoreSegmentFiles = 128

	// eventStoreSeqBase is the first event sequence number of a session.
	//
	// Event sequences double as the replay cursor handed to every entry point
	// (`SequencedMessage.Seq`, `session_event_projections.seq`, and
	// `conversation_turns.start_seq`/`end_seq`). The legacy SQLite `entries`
	// table numbers rows from an AUTOINCREMENT ROWID, so its cursor starts at
	// 1, and every caller in the codebase uses zero as the "no cursor yet"
	// sentinel that must still admit the first row. Starting the EventStore at
	// 1 keeps one cursor space across both stores, so TUI, CLI, WebUI/API, ACP,
	// and Channel page identically without any adapter-side special casing.
	eventStoreSeqBase = 1
)

type eventStoreLayout struct {
	legacy bool
	paths  []string
}

type eventStoreScan struct {
	batches []EventBatch
	nextSeq int64
}

func eventStoreSegmentNameForRange(seqFrom, seqTo int64) string {
	return fmt.Sprintf("%020d-%020d.jsonl", seqFrom, seqTo)
}

// NewEventStore opens the event store for one session. The directory is kept
// outside sessions.db so content corruption has a session-sized blast radius.
func NewEventStore(sessionDir, sessionID string) (*EventStore, error) {
	if sessionDir == "" || sessionID == "" {
		return nil, errors.New("session event store requires session directory and ID")
	}
	if filepath.Base(sessionID) != sessionID || strings.TrimSpace(sessionID) != sessionID || sessionID == "." || sessionID == ".." {
		return nil, errors.New("session event store ID must be a single non-empty path component")
	}
	root := filepath.Join(sessionDir, "event-store", sessionID)
	lock := &sync.Mutex{}
	actual, _ := eventStoreLocks.LoadOrStore(root, lock)
	return &EventStore{mu: actual.(*sync.Mutex), root: root}, nil
}

// Path returns the private directory owned by this EventStore. Callers should
// use RemoveEventStore for lifecycle cleanup rather than deleting children.
func (s *EventStore) Path() string { return s.root }

// RemoveEventStore removes one session's content store after its canonical
// session row has been deleted under the Runtime mutation lease.
func RemoveEventStore(sessionDir, sessionID string) error {
	store, err := NewEventStore(sessionDir, sessionID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(store.Path()); err != nil {
		return fmt.Errorf("remove session event store: %w", err)
	}
	eventStoreAppendCaches.Delete(filepath.Join(store.Path(), eventStoreSegmentName))
	eventStoreSegmentCaches.Delete(store.Path())
	return nil
}

// ForgetEventStoreCaches retires process-local append state after a reset or
// archival move. Cache entries are only an acceleration hint, never authority.
func ForgetEventStoreCaches(sessionDir string) {
	prefix := filepath.Clean(filepath.Join(sessionDir, eventStoreDirectoryName)) + string(filepath.Separator)
	eventStoreAppendCaches.Range(func(key, _ any) bool {
		path, ok := key.(string)
		if ok && strings.HasPrefix(filepath.Clean(path), prefix) {
			eventStoreAppendCaches.Delete(key)
		}
		return true
	})
	eventStoreSegmentCaches.Range(func(key, _ any) bool {
		root, ok := key.(string)
		if ok && strings.HasPrefix(filepath.Clean(root), prefix) {
			eventStoreSegmentCaches.Delete(key)
		}
		return true
	})
}

// Append writes one bounded batch. The footer is written only after every
// event line, so a scanner can distinguish a complete batch from a crash tail.
// It does not mark the batch canonical; the SQLite commit protocol does that.
func (s *EventStore) Append(batchID, sessionID, runID, turnID string, events []EventRecord) (EventBatch, error) {
	if batchID == "" || sessionID == "" || len(events) == 0 {
		return EventBatch{}, errors.New("event batch requires ID, session ID, and events")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.root, 0700); err != nil {
		return EventBatch{}, fmt.Errorf("create event store: %w", err)
	}
	if err := syncEventStoreDirectory(s.root); err != nil {
		return EventBatch{}, err
	}
	layout, err := s.scanLayout()
	if err != nil {
		return EventBatch{}, err
	}
	cache, err := eventStoreAppendState(s.root, layout)
	if err != nil {
		return EventBatch{}, err
	}
	if _, exists := cache.batchIDs[batchID]; exists {
		return EventBatch{}, fmt.Errorf("event batch %s already exists", batchID)
	}
	seqFrom := cache.nextSeq
	if seqFrom < eventStoreSeqBase {
		// A stale cache must never resurrect the pre-1 sequence space: it would
		// hand out a cursor that every caller reads as "no cursor yet".
		return EventBatch{}, fmt.Errorf("event store append state has invalid next sequence %d", seqFrom)
	}
	path, roll := chooseEventStoreSegment(s.root, layout, cache, seqFrom, int64(approximateEventStoreBatchBytes(events)))
	lines := make([]eventStoreLine, 0, len(events)+2)
	lines = append(lines, eventStoreLine{Kind: eventLineBatchBegin, BatchID: batchID, SeqFrom: seqFrom, SeqTo: seqFrom + int64(len(events)) - 1, Count: len(events)})
	canonicalEvents := make([]EventRecord, len(events))
	eventIDs := make(map[string]struct{}, len(events))
	for i, event := range events {
		if event.EventID == "" || event.Kind == "" {
			return EventBatch{}, fmt.Errorf("event %d requires event ID and kind", i)
		}
		if _, exists := eventIDs[event.EventID]; exists {
			return EventBatch{}, fmt.Errorf("event ID %s is duplicated in batch", event.EventID)
		}
		eventIDs[event.EventID] = struct{}{}
		if _, exists := cache.eventIDs[event.EventID]; exists {
			return EventBatch{}, fmt.Errorf("event ID %s already exists", event.EventID)
		}
		if event.SessionID == "" {
			event.SessionID = sessionID
		}
		if event.SessionID != sessionID {
			return EventBatch{}, fmt.Errorf("event %s belongs to session %s, want %s", event.EventID, event.SessionID, sessionID)
		}
		if event.CreatedAt.IsZero() {
			event.CreatedAt = time.Now().UTC()
		}
		event.Payload = append(json.RawMessage(nil), event.Payload...)
		canonicalEvents[i] = event
		lines = append(lines, eventStoreLine{
			Kind: eventLineEvent, BatchID: batchID, Seq: seqFrom + int64(i), Index: i, Count: len(events),
			EventID: event.EventID, SessionID: event.SessionID, RunID: event.RunID, TurnID: event.TurnID,
			EventKind: event.Kind, ParentSeq: event.ParentSeq, Payload: event.Payload, CreatedAt: event.CreatedAt,
		})
	}
	digest, err := batchDigest(canonicalEvents)
	if err != nil {
		return EventBatch{}, err
	}
	lines = append(lines, eventStoreLine{Kind: eventLineBatchFooter, BatchID: batchID, SeqFrom: seqFrom, SeqTo: seqFrom + int64(len(events)) - 1, Count: len(events), Digest: digest})
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return EventBatch{}, fmt.Errorf("create event store segment directory: %w", err)
	}
	if roll {
		if err := syncEventStoreDirectory(filepath.Dir(path)); err != nil {
			return EventBatch{}, err
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return EventBatch{}, fmt.Errorf("open event store: %w", err)
	}
	writer := bufio.NewWriter(file)
	for _, line := range lines {
		encoded, marshalErr := json.Marshal(line)
		if marshalErr != nil {
			_ = file.Close()
			return EventBatch{}, fmt.Errorf("encode event store line: %w", marshalErr)
		}
		if _, writeErr := writer.Write(append(encoded, '\n')); writeErr != nil {
			_ = file.Close()
			return EventBatch{}, fmt.Errorf("write event store: %w", writeErr)
		}
	}
	if err := writer.Flush(); err != nil {
		_ = file.Close()
		return EventBatch{}, fmt.Errorf("flush event store: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return EventBatch{}, fmt.Errorf("sync event store: %w", err)
	}
	if err := file.Close(); err != nil {
		return EventBatch{}, fmt.Errorf("close event store: %w", err)
	}
	if err := syncEventStoreDirectory(s.root); err != nil {
		return EventBatch{}, err
	}
	if !layout.legacy {
		if err := syncEventStoreDirectory(filepath.Dir(path)); err != nil {
			return EventBatch{}, err
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return EventBatch{}, fmt.Errorf("inspect appended event store: %w", err)
	}
	previousSize := cache.size
	cache.batchIDs[batchID] = struct{}{}
	for _, event := range canonicalEvents {
		cache.eventIDs[event.EventID] = struct{}{}
	}
	cache.size = info.Size()
	cache.modTime = info.ModTime()
	cache.nextSeq = seqFrom + int64(len(events))
	if layout.legacy {
		eventStoreAppendCaches.Store(path, cache)
	} else {
		eventStoreSegmentCaches.Store(s.root, cache)
	}
	if err := s.writeManifest(layout, cache.nextSeq); err != nil {
		return EventBatch{}, err
	}
	eventStoreStats.appendBatches.Add(1)
	eventStoreStats.appendEvents.Add(uint64(len(canonicalEvents)))
	eventStoreStats.appendBytes.Add(uint64(info.Size() - previousSize))
	return EventBatch{BatchID: batchID, SeqFrom: seqFrom, SeqTo: seqFrom + int64(len(events)) - 1, Events: canonicalEvents, Digest: digest}, nil
}

// Scan reports physically complete batches and repairs only an incomplete
// final tail. A malformed middle line is corruption and is never skipped.
func (s *EventStore) Scan() ([]EventBatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scan(true)
}

// Inspect reports physically complete batches without mutating the store. It is
// the read-only counterpart of Scan and is what reconciliation and session load
// must use: those paths run without the fenced writer lease, so they may neither
// truncate a crash tail nor materialise the rebuildable manifest. Corruption
// classification is identical to Scan; only the filesystem side effects differ.
func (s *EventStore) Inspect() ([]EventBatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	layout, err := s.scanLayout()
	if err != nil {
		return nil, err
	}
	state, err := s.scanState(layout, false, true)
	if err != nil {
		return nil, err
	}
	return state.batches, nil
}

// inspectProjection is the read-only structural scan used by the non-canonical
// list projection. It validates record framing and sequence contiguity but
// skips the per-batch SHA-256 payload digest, which is the dominant cost when
// a page of sessions is projected at startup. Canonical replay always uses
// Inspect/Scan and still fails closed on any digest mismatch.
func (s *EventStore) inspectProjection() ([]EventBatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	layout, err := s.scanLayout()
	if err != nil {
		return nil, err
	}
	state, err := s.scanState(layout, false, false)
	if err != nil {
		return nil, err
	}
	return state.batches, nil
}

func (s *EventStore) scanLayout() (eventStoreLayout, error) {
	legacyPath := filepath.Join(s.root, eventStoreSegmentName)
	if _, err := os.Stat(legacyPath); err == nil {
		return eventStoreLayout{legacy: true, paths: []string{legacyPath}}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return eventStoreLayout{}, fmt.Errorf("inspect legacy event store: %w", err)
	}
	segmentsDir := filepath.Join(s.root, eventStoreSegmentsDir)
	entries, err := os.ReadDir(segmentsDir)
	if errors.Is(err, os.ErrNotExist) {
		return eventStoreLayout{}, nil
	}
	if err != nil {
		return eventStoreLayout{}, fmt.Errorf("read event store segments: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	if len(names) > maxEventStoreSegmentFiles {
		return eventStoreLayout{}, fmt.Errorf("event store has %d segments, maximum supported is %d", len(names), maxEventStoreSegmentFiles)
	}
	paths := make([]string, 0, len(names))
	for _, name := range names {
		paths = append(paths, filepath.Join(segmentsDir, name))
	}
	return eventStoreLayout{paths: paths}, nil
}

func (s *EventStore) scanState(layout eventStoreLayout, repairTail, verifyDigest bool) (eventStoreScan, error) {
	eventStoreStats.fullScans.Add(1)
	all := make([]EventBatch, 0)
	nextSeq := int64(eventStoreSeqBase)
	seenBatchIDs := make(map[string]struct{})
	seenEventIDs := make(map[string]struct{})
	for index, path := range layout.paths {
		result, err := scanEventStore(path, repairTail && (!layout.legacy || index == len(layout.paths)-1), verifyDigest)
		if err != nil {
			return eventStoreScan{}, err
		}
		for _, batch := range result.batches {
			if batch.SeqFrom != nextSeq {
				return eventStoreScan{}, fmt.Errorf("event store segments are not contiguous at %d: want %d", batch.SeqFrom, nextSeq)
			}
			if _, exists := seenBatchIDs[batch.BatchID]; exists {
				return eventStoreScan{}, fmt.Errorf("event store has duplicate batch ID %s", batch.BatchID)
			}
			seenBatchIDs[batch.BatchID] = struct{}{}
			for _, event := range batch.Events {
				if _, exists := seenEventIDs[event.EventID]; exists {
					return eventStoreScan{}, fmt.Errorf("event store has duplicate event ID %s", event.EventID)
				}
				seenEventIDs[event.EventID] = struct{}{}
			}
			all = append(all, batch)
			nextSeq = batch.SeqTo + 1
		}
	}
	return eventStoreScan{batches: all, nextSeq: nextSeq}, nil
}

func (s *EventStore) scan(repairTail bool) ([]EventBatch, error) {
	layout, err := s.scanLayout()
	if err != nil {
		return nil, err
	}
	state, err := s.scanState(layout, repairTail, true)
	if err != nil {
		return nil, err
	}
	cache := &eventStoreAppendCache{nextSeq: state.nextSeq, batchIDs: make(map[string]struct{}, len(state.batches)), eventIDs: make(map[string]struct{})}
	for _, batch := range state.batches {
		cache.batchIDs[batch.BatchID] = struct{}{}
		for _, event := range batch.Events {
			cache.eventIDs[event.EventID] = struct{}{}
		}
	}
	if layout.legacy {
		if len(layout.paths) > 0 {
			if info, statErr := os.Stat(layout.paths[len(layout.paths)-1]); statErr == nil {
				cache.size, cache.modTime = info.Size(), info.ModTime()
			}
		}
		for _, path := range layout.paths {
			eventStoreAppendCaches.Store(path, cloneEventStoreAppendCache(cache))
		}
	} else {
		if len(layout.paths) > 0 {
			if info, statErr := os.Stat(layout.paths[len(layout.paths)-1]); statErr == nil {
				cache.size, cache.modTime = info.Size(), info.ModTime()
			}
		}
		eventStoreSegmentCaches.Store(s.root, cache)
		if err := s.writeManifest(layout, state.nextSeq); err != nil {
			return nil, err
		}
	}
	return state.batches, nil
}

// NextSequence returns the next event sequence after the last physically
// complete batch. It is a reservation hint; the SQLite event intent and the
// fenced writer are what make the reservation authoritative.
func (s *EventStore) NextSequence() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	layout, err := s.scanLayout()
	if err != nil {
		return 0, err
	}
	if len(layout.paths) == 0 {
		return eventStoreSeqBase, nil
	}
	// The append-state cache already tracks the next sequence and is validated
	// against the physical revision, so the hot append path never rescans the
	// whole log to learn where it ends.
	cache, err := eventStoreAppendState(s.root, layout)
	if err != nil {
		return 0, err
	}
	if cache.nextSeq < eventStoreSeqBase {
		return eventStoreSeqBase, nil
	}
	return cache.nextSeq, nil
}

func (s *EventStore) writeManifest(layout eventStoreLayout, nextSeq int64) error {
	if layout.legacy {
		return nil
	}
	if err := os.MkdirAll(s.root, 0700); err != nil {
		return fmt.Errorf("create event store manifest directory: %w", err)
	}
	active := make([]string, 0, len(layout.paths))
	for _, path := range layout.paths {
		rel, relErr := filepath.Rel(s.root, path)
		if relErr != nil {
			return relErr
		}
		active = append(active, filepath.ToSlash(rel))
	}
	manifest := EventManifest{SchemaVersion: 1, Active: active, NextSeq: nextSeq, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("encode event store manifest: %w", err)
	}
	path := filepath.Join(s.root, eventStoreManifest)
	temporary, err := os.CreateTemp(s.root, ".manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("create event store manifest temp: %w", err)
	}
	temporaryName := temporary.Name()
	// A successful publish renames the temp away, so this deferred unlink only
	// ever sees the failure paths. Its error stays ignored for the same reason
	// every other atomic write in the repository ignores it: the rename target
	// already exists, so nothing can observe the leftover.
	defer os.Remove(temporaryName)
	written, err := temporary.Write(append(encoded, '\n'))
	if err == nil && written != len(encoded)+1 {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write event store manifest: %w", err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("publish event store manifest: %w", err)
	}
	return syncEventStoreDirectory(s.root)
}

func chooseEventStoreSegment(root string, layout eventStoreLayout, cache *eventStoreAppendCache, seqFrom int64, approximateBytes int64) (string, bool) {
	if layout.legacy {
		path := layout.paths[0]
		return path, cache.size >= eventStoreSegmentLimit()
	}
	segmentsDir := filepath.Join(root, eventStoreSegmentsDir)
	if len(layout.paths) == 0 {
		return filepath.Join(segmentsDir, eventStoreSegmentNameForRange(seqFrom, seqFrom)), true
	}
	last := layout.paths[len(layout.paths)-1]
	if cache.size+approximateBytes <= eventStoreSegmentLimit() {
		return last, false
	}
	return filepath.Join(segmentsDir, eventStoreSegmentNameForRange(seqFrom, seqFrom)), true
}

func eventStoreSegmentLimit() int64 {
	if limit := eventStoreRollBytes.Load(); limit > 0 {
		return limit
	}
	return maxEventStoreSegmentBytes
}

func approximateEventStoreBatchBytes(events []EventRecord) int64 {
	var total int64
	for _, event := range events {
		total += int64(len(event.Payload)) + 512
	}
	return total
}

func cloneEventStoreAppendCache(cache *eventStoreAppendCache) *eventStoreAppendCache {
	out := &eventStoreAppendCache{size: cache.size, modTime: cache.modTime, nextSeq: cache.nextSeq, batchIDs: make(map[string]struct{}, len(cache.batchIDs)), eventIDs: make(map[string]struct{}, len(cache.eventIDs))}
	for key := range cache.batchIDs {
		out.batchIDs[key] = struct{}{}
	}
	for key := range cache.eventIDs {
		out.eventIDs[key] = struct{}{}
	}
	return out
}

func scanEventStore(path string, repairTail, verifyDigest bool) (eventStoreScan, error) {
	// Read-only projections must not require write permission on the store; only
	// the tail-repair path needs O_RDWR for truncation.
	flags := os.O_RDONLY
	if repairTail {
		flags = os.O_RDWR
	}
	file, err := os.OpenFile(path, flags, 0600)
	if errors.Is(err, os.ErrNotExist) {
		return eventStoreScan{}, nil
	}
	if err != nil {
		return eventStoreScan{}, fmt.Errorf("open event store for scan: %w", err)
	}
	defer file.Close()
	var result eventStoreScan
	reader := bufio.NewReader(file)
	var offset int64
	var begin *eventStoreLine
	var events []EventRecord
	seenEventIDs := make(map[string]struct{})
	lastComplete := int64(0)
	for {
		lineStart := offset
		raw, readErr := reader.ReadBytes('\n')
		offset += int64(len(raw))
		if len(raw) == 0 && errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return eventStoreScan{}, fmt.Errorf("read event store: %w", readErr)
		}
		if len(raw) == 0 || raw[len(raw)-1] != '\n' {
			if repairTail && lineStart >= lastComplete {
				if err := file.Truncate(lastComplete); err != nil {
					return eventStoreScan{}, fmt.Errorf("truncate incomplete event tail: %w", err)
				}
				if err := file.Sync(); err != nil {
					return eventStoreScan{}, fmt.Errorf("sync repaired event tail: %w", err)
				}
				break
			}
			return eventStoreScan{}, errors.New("event store has an incomplete middle record")
		}
		var line eventStoreLine
		if err := json.Unmarshal(raw, &line); err != nil {
			return eventStoreScan{}, fmt.Errorf("event store corruption at byte %d: %w", lineStart, err)
		}
		switch line.Kind {
		case eventLineBatchBegin:
			if begin != nil {
				return eventStoreScan{}, fmt.Errorf("event store has nested batch %s", line.BatchID)
			}
			begin = &line
			events = nil
		case eventLineEvent:
			if begin == nil || line.BatchID != begin.BatchID || line.Index != len(events) || line.Seq != begin.SeqFrom+int64(line.Index) {
				return eventStoreScan{}, fmt.Errorf("event store has invalid event sequence at byte %d", lineStart)
			}
			event := EventRecord{EventID: line.EventID, SessionID: line.SessionID, RunID: line.RunID, TurnID: line.TurnID, Kind: line.EventKind, ParentSeq: line.ParentSeq, Payload: append(json.RawMessage(nil), line.Payload...), CreatedAt: line.CreatedAt}
			if event.EventID == "" {
				return eventStoreScan{}, fmt.Errorf("event store has an event without ID at byte %d", lineStart)
			}
			if _, exists := seenEventIDs[event.EventID]; exists {
				return eventStoreScan{}, fmt.Errorf("event store has duplicate event ID %s", event.EventID)
			}
			seenEventIDs[event.EventID] = struct{}{}
			events = append(events, event)
		case eventLineBatchFooter:
			if begin == nil || line.BatchID != begin.BatchID || line.Count != len(events) || line.SeqFrom != begin.SeqFrom || line.SeqTo != begin.SeqTo {
				return eventStoreScan{}, fmt.Errorf("event store has invalid footer for batch %s", line.BatchID)
			}
			digest := line.Digest
			if verifyDigest {
				computed, digestErr := batchDigest(events)
				if digestErr != nil || computed != line.Digest {
					return eventStoreScan{}, fmt.Errorf("event store checksum mismatch for batch %s", line.BatchID)
				}
				digest = computed
			}
			result.batches = append(result.batches, EventBatch{BatchID: begin.BatchID, SeqFrom: begin.SeqFrom, SeqTo: begin.SeqTo, Events: events, Digest: digest})
			result.nextSeq = line.SeqTo + 1
			begin = nil
			events = nil
			lastComplete = offset
		default:
			return eventStoreScan{}, fmt.Errorf("event store has unknown record kind %q", line.Kind)
		}
		if errors.Is(readErr, io.EOF) {
			if begin != nil && repairTail {
				if err := file.Truncate(lastComplete); err != nil {
					return eventStoreScan{}, fmt.Errorf("truncate incomplete event batch: %w", err)
				}
				if err := file.Sync(); err != nil {
					return eventStoreScan{}, fmt.Errorf("sync repaired event batch: %w", err)
				}
				break
			}
			break
		}
	}
	if verifyDigest {
		if info, statErr := os.Stat(path); statErr == nil {
			batchIDs := make(map[string]struct{}, len(result.batches))
			eventIDs := make(map[string]struct{})
			for _, batch := range result.batches {
				batchIDs[batch.BatchID] = struct{}{}
				for _, event := range batch.Events {
					eventIDs[event.EventID] = struct{}{}
				}
			}
			eventStoreAppendCaches.Store(path, &eventStoreAppendCache{size: info.Size(), modTime: info.ModTime(), nextSeq: result.nextSeq, batchIDs: batchIDs, eventIDs: eventIDs})
		}
	}
	return result, nil
}

// eventStoreAppendState avoids rescanning an ever-growing log before every
// local append. The cache is used only when file size and modification time
// still match; a peer write, external change, or startup cache miss falls back
// to a full validated scan, preserving cross-process correctness.
func eventStoreAppendState(root string, layout eventStoreLayout) (*eventStoreAppendCache, error) {
	if cache, ok, err := eventStoreAppendStateFresh(root, layout); err != nil {
		return nil, err
	} else if ok {
		return cache, nil
	}
	store := &EventStore{mu: &sync.Mutex{}, root: root}
	batches, err := store.scan(true)
	eventStoreStats.cacheMisses.Add(1)
	if err != nil {
		return nil, err
	}
	if layout.legacy {
		if cached, ok := eventStoreAppendCaches.Load(layout.paths[0]); ok {
			return cached.(*eventStoreAppendCache), nil
		}
	} else if cached, ok := eventStoreSegmentCaches.Load(root); ok {
		return cached.(*eventStoreAppendCache), nil
	}
	return newEventStoreAppendCache(root, layout, batches), nil
}

// eventStoreAppendStateFresh reports the cached append state only when it still
// describes the current physical revision. found=false means "scan it": a
// missing cache, a stale size/mtime, or an absent file. A non-NotExist stat
// error is surfaced so append correctness still fails closed.
func eventStoreAppendStateFresh(root string, layout eventStoreLayout) (cache *eventStoreAppendCache, found bool, err error) {
	if layout.legacy {
		if len(layout.paths) == 0 {
			return nil, false, nil
		}
		path := layout.paths[0]
		info, statErr := os.Stat(path)
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				return nil, false, nil
			}
			return nil, false, fmt.Errorf("inspect event store append state: %w", statErr)
		}
		cached, ok := eventStoreAppendCaches.Load(path)
		if !ok {
			return nil, false, nil
		}
		c := cached.(*eventStoreAppendCache)
		if c.size == info.Size() && c.modTime.Equal(info.ModTime()) {
			eventStoreStats.cacheHits.Add(1)
			return c, true, nil
		}
		return nil, false, nil
	}
	cached, ok := eventStoreSegmentCaches.Load(root)
	if !ok {
		return nil, false, nil
	}
	c := cached.(*eventStoreAppendCache)
	if len(layout.paths) == 0 {
		if c.size == 0 {
			eventStoreStats.cacheHits.Add(1)
			return c, true, nil
		}
		return nil, false, nil
	}
	info, statErr := os.Stat(layout.paths[len(layout.paths)-1])
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("inspect event store append state: %w", statErr)
	}
	if c.size == info.Size() && c.modTime.Equal(info.ModTime()) {
		eventStoreStats.cacheHits.Add(1)
		return c, true, nil
	}
	return nil, false, nil
}

// peekAppendState returns the process-local, validated append state only while
// it is fresh for the current physical revision, without ever scanning. A miss
// means the cached state cannot be trusted to describe the file right now, so
// callers that need an exact answer run the full inspection instead.
func (s *EventStore) peekAppendState() (*eventStoreAppendCache, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	layout, err := s.scanLayout()
	if err != nil {
		return nil, false
	}
	cache, ok, err := eventStoreAppendStateFresh(s.root, layout)
	if err != nil {
		return nil, false
	}
	return cache, ok
}

func newEventStoreAppendCache(root string, layout eventStoreLayout, batches []EventBatch) *eventStoreAppendCache {
	cache := &eventStoreAppendCache{nextSeq: eventStoreSeqBase, batchIDs: make(map[string]struct{}), eventIDs: make(map[string]struct{})}
	for _, batch := range batches {
		cache.batchIDs[batch.BatchID] = struct{}{}
		for _, event := range batch.Events {
			cache.eventIDs[event.EventID] = struct{}{}
		}
		cache.nextSeq = batch.SeqTo + 1
	}
	if len(layout.paths) > 0 {
		if info, err := os.Stat(layout.paths[len(layout.paths)-1]); err == nil {
			cache.size, cache.modTime = info.Size(), info.ModTime()
		}
	}
	return cache
}

func batchDigest(events []EventRecord) (string, error) {
	hash := sha256.New()
	for _, event := range events {
		encoded, err := json.Marshal(event)
		if err != nil {
			return "", err
		}
		_, _ = hash.Write(encoded)
		_, _ = hash.Write([]byte{'\n'})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// DigestEventBatch returns the stable digest used by SQLite event intents.
func DigestEventBatch(events []EventRecord) (string, error) {
	return batchDigest(events)
}

// syncEventStoreDirectory makes directory entry creation durable where the
// platform supports directory fsync. Some Windows filesystems reject Sync on
// directories, while the file fsync above still provides the available
// durability boundary there.
func syncEventStoreDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open event store directory: %w", err)
	}
	err = directory.Sync()
	closeErr := directory.Close()
	if err == nil || errors.Is(err, os.ErrInvalid) || runtime.GOOS == "windows" {
		err = nil
	}
	if err != nil {
		return fmt.Errorf("sync event store directory: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close event store directory: %w", closeErr)
	}
	return nil
}
