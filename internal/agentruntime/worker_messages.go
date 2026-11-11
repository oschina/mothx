package agentruntime

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Worker message kinds. They are the worker-facing vocabulary; only finding,
// question, and completion may enter the lead context or wake an idle lead.
const (
	WorkerMessageNotice     = "notice"
	WorkerMessageProgress   = "progress"
	WorkerMessageFinding    = "finding"
	WorkerMessageQuestion   = "question"
	WorkerMessageCompletion = "completion"
)

// Worker message delivery states, folded from the append-only event log.
const (
	WorkerMessageStatusCreated   = "created"
	WorkerMessageStatusDelivered = "delivered"
	WorkerMessageStatusObserved  = "observed"
	WorkerMessageStatusExpired   = "expired"
	WorkerMessageStatusCancelled = "cancelled"
	// WorkerMessageStatusSuperseded marks a queued finding that was replaced by
	// a newer finding from the same worker before the lead consumed it.
	WorkerMessageStatusSuperseded = "superseded"
)

// Worker message log operations.
const (
	workerMessageOpCreated    = "created"
	workerMessageOpDelivered  = "delivered"
	workerMessageOpObserved   = "observed"
	workerMessageOpExpired    = "expired"
	workerMessageOpCancelled  = "cancelled"
	workerMessageOpSuperseded = "superseded"
)

// Limits for one worker report/ask. They bound what a hostile or confused
// external Agent can append to the durable log and inject into the lead.
const (
	MaxWorkerReportBytes = 8 << 10
	MaxWorkerAskBytes    = 4 << 10
	MaxWorkerAskOptions  = 8
	// One option is a short choice label, not free-form context. Bounding both
	// the per-option size and the total keeps a hostile worker from turning the
	// options array into an unbounded payload for the durable decision log and
	// the lead mailbox.
	MaxWorkerAskOptionBytes  = 512
	MaxWorkerAskOptionsBytes = 2 << 10
	maxWorkerDedupeKey       = 256
	// workerMessageDirectoryName is the Runtime-private root below the shared
	// sessions directory. Each session owns one child directory, so independent
	// sessions can never append to, dedupe against, or compact the same file.
	workerMessageDirectoryName = "worker-messages"
	workerMessageFile          = "worker_messages.jsonl"
)

// workerMessageCompactBytes triggers one atomic rewrite of the folded state. It
// is a variable so tests can exercise compaction without writing 8 MiB.
var workerMessageCompactBytes int64 = 8 << 20

// WorkerMessage is the folded, read-only projection of one worker message.
type WorkerMessage struct {
	ID          string    `json:"id"`
	SessionID   string    `json:"sessionId"`
	ChildID     string    `json:"childId"`
	ParentRunID string    `json:"parentRunId,omitempty"`
	Direction   string    `json:"direction"`
	Kind        string    `json:"kind"`
	Body        string    `json:"body"`
	DedupeKey   string    `json:"dedupeKey,omitempty"`
	DecisionID  string    `json:"decisionId,omitempty"`
	Status      string    `json:"status"`
	RunID       string    `json:"runId,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"`
	Reason      string    `json:"reason,omitempty"`
}

// workerMessageEvent is one line of the append-only log.
type workerMessageEvent struct {
	Op          string `json:"op"`
	ID          string `json:"id"`
	SessionID   string `json:"sessionId,omitempty"`
	ChildID     string `json:"childId,omitempty"`
	ParentRunID string `json:"parentRunId,omitempty"`
	Direction   string `json:"direction,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Body        string `json:"body,omitempty"`
	DedupeKey   string `json:"dedupeKey,omitempty"`
	DecisionID  string `json:"decisionId,omitempty"`
	RunID       string `json:"runId,omitempty"`
	At          string `json:"at,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// WorkerMessageLog is the single-writer append-only log for one session. It
// owns no SQL state; the canonical pending decision remains a DecisionRecord.
type WorkerMessageLog struct {
	path      string
	sessionID string
	mu        sync.Mutex
	// dedupe indexes (sessionID, childID, dedupeKey) to the first message ID.
	dedupe map[string]string
	file   *os.File
	// compactFloor prevents a pathological loop when the folded state itself
	// exceeds the threshold: after a rewrite, wait for another threshold-sized
	// growth before compacting again.
	compactFloor int64
}

// workerMessageLogRegistry gives every concurrent opener of the same session
// log the same instance. Independent instances would each keep their own
// dedupe index and could compact the same file concurrently, losing rows.
type workerMessageLogRegistryEntry struct {
	log  *WorkerMessageLog
	refs int
}

var workerMessageLogRegistry = struct {
	sync.Mutex
	entries map[string]*workerMessageLogRegistryEntry
}{entries: map[string]*workerMessageLogRegistryEntry{}}

// WorkerMessageLogPath returns the per-session append-only log path. The
// session ID must be a single path component; explicit IDs supplied by an
// adapter are never allowed to escape the Runtime-private directory.
func WorkerMessageLogPath(sessionDir, sessionID string) (string, error) {
	dir, err := workerMessageSessionDir(sessionDir, sessionID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, workerMessageFile), nil
}

func workerMessageSessionDir(sessionDir, sessionID string) (string, error) {
	if strings.TrimSpace(sessionDir) == "" {
		return "", fmt.Errorf("worker message log requires a session directory")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || filepath.Base(sessionID) != sessionID || sessionID == "." || sessionID == ".." {
		return "", fmt.Errorf("worker message log requires a single-component session ID")
	}
	return filepath.Join(sessionDir, workerMessageDirectoryName, sessionID), nil
}

// OpenWorkerMessageLog opens (and creates) the session's worker message log. A
// crash may leave an incomplete final line; reads tolerate it.
func OpenWorkerMessageLog(sessionDir, sessionID string) (*WorkerMessageLog, error) {
	path, err := WorkerMessageLogPath(sessionDir, sessionID)
	if err != nil {
		return nil, err
	}
	key := filepath.Clean(path)
	workerMessageLogRegistry.Lock()
	if entry := workerMessageLogRegistry.entries[key]; entry != nil {
		entry.refs++
		workerMessageLogRegistry.Unlock()
		return entry.log, nil
	}
	workerMessageLogRegistry.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create worker message directory: %w", err)
	}
	log := &WorkerMessageLog{path: path, sessionID: strings.TrimSpace(sessionID), dedupe: map[string]string{}}
	if err := log.rebuildIndex(); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open worker message log: %w", err)
	}
	log.file = file

	workerMessageLogRegistry.Lock()
	if entry := workerMessageLogRegistry.entries[key]; entry != nil {
		// Another opener won the race while this one built its index.
		entry.refs++
		workerMessageLogRegistry.Unlock()
		_ = file.Close()
		return entry.log, nil
	}
	workerMessageLogRegistry.entries[key] = &workerMessageLogRegistryEntry{log: log, refs: 1}
	workerMessageLogRegistry.Unlock()
	return log, nil
}

// RemoveWorkerMessages deletes one session's worker evidence log after its
// canonical session row has been removed. The shared sessions root and other
// sessions are never touched.
func RemoveWorkerMessages(sessionDir, sessionID string) error {
	dir, err := workerMessageSessionDir(sessionDir, sessionID)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, workerMessageFile)
	workerMessageLogRegistry.Lock()
	entry := workerMessageLogRegistry.entries[filepath.Clean(path)]
	delete(workerMessageLogRegistry.entries, filepath.Clean(path))
	workerMessageLogRegistry.Unlock()
	if entry != nil {
		_ = entry.log.closeFile()
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove worker message log: %w", err)
	}
	return nil
}

// Close releases one reference to the shared append handle. It is safe to call
// more than once; the file is closed when the last opener releases it.
func (l *WorkerMessageLog) Close() error {
	if l == nil {
		return nil
	}
	key := filepath.Clean(l.path)
	workerMessageLogRegistry.Lock()
	entry := workerMessageLogRegistry.entries[key]
	if entry == nil || entry.log != l {
		workerMessageLogRegistry.Unlock()
		return l.closeFile()
	}
	entry.refs--
	if entry.refs > 0 {
		workerMessageLogRegistry.Unlock()
		return nil
	}
	delete(workerMessageLogRegistry.entries, key)
	workerMessageLogRegistry.Unlock()
	return l.closeFile()
}

func (l *WorkerMessageLog) closeFile() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

// Path returns the log path for diagnostics only; it is never exposed to a
// worker or persisted in child metadata.
func (l *WorkerMessageLog) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// Create appends one created event. A duplicate (child, dedupeKey) returns the
// existing message instead of appending a second row.
func (l *WorkerMessageLog) Create(message WorkerMessage) (WorkerMessage, bool, error) {
	if l == nil {
		return WorkerMessage{}, false, fmt.Errorf("worker message log is not available")
	}
	if len(message.Body) > MaxWorkerReportBytes {
		message.Body = truncateUTF8Bytes(message.Body, MaxWorkerReportBytes)
	}
	message.DedupeKey = strings.TrimSpace(message.DedupeKey)
	if len(message.DedupeKey) > maxWorkerDedupeKey {
		message.DedupeKey = message.DedupeKey[:maxWorkerDedupeKey]
	}
	message.SessionID = strings.TrimSpace(message.SessionID)
	if message.SessionID == "" {
		message.SessionID = l.sessionID
	}
	if l.sessionID != "" && message.SessionID != l.sessionID {
		return WorkerMessage{}, false, fmt.Errorf("worker message session %q does not belong to this log", message.SessionID)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if message.DedupeKey != "" {
		if existing, ok := l.dedupe[workerDedupeIndex(message.SessionID, message.ChildID, message.DedupeKey)]; ok {
			folded, err := l.foldLocked()
			if err != nil {
				return WorkerMessage{}, true, err
			}
			for _, item := range folded {
				if item.ID == existing {
					return item, true, nil
				}
			}
			return WorkerMessage{ID: existing}, true, nil
		}
	}
	if message.ID == "" {
		message.ID = "wm_" + sanitizeWorkerIDPart(message.ChildID) + "_" + fmt.Sprintf("%d", time.Now().UnixNano())
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now().UTC()
	}
	if message.Direction == "" {
		message.Direction = "worker_to_lead"
	}
	if message.Status == "" {
		message.Status = WorkerMessageStatusCreated
	}
	event := workerMessageEvent{
		Op: workerMessageOpCreated, ID: message.ID, SessionID: message.SessionID,
		ChildID: message.ChildID, ParentRunID: message.ParentRunID,
		Direction: message.Direction, Kind: message.Kind, Body: message.Body,
		DedupeKey: message.DedupeKey, DecisionID: message.DecisionID,
		At: message.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if err := l.appendLocked(event); err != nil {
		return WorkerMessage{}, false, err
	}
	if message.DedupeKey != "" {
		l.dedupe[workerDedupeIndex(message.SessionID, message.ChildID, message.DedupeKey)] = message.ID
	}
	return message, false, nil
}

// Mark appends one state transition. The log is append-only: an event for an
// unknown ID is retained but ignored by the fold, so a stale worker cannot
// create a phantom message.
func (l *WorkerMessageLog) Mark(id, op, runID, reason string) error {
	if l == nil {
		return fmt.Errorf("worker message log is not available")
	}
	switch op {
	case workerMessageOpDelivered, workerMessageOpObserved, workerMessageOpExpired, workerMessageOpCancelled, workerMessageOpSuperseded:
	default:
		return fmt.Errorf("unsupported worker message transition %q", op)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	event := workerMessageEvent{Op: op, ID: id, RunID: runID, Reason: reason, At: time.Now().UTC().Format(time.RFC3339Nano)}
	return l.appendLocked(event)
}

// List folds the event log into the current message state, oldest first.
func (l *WorkerMessageLog) List() ([]WorkerMessage, error) {
	if l == nil {
		return nil, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.foldLocked()
}

// Find returns one folded message by ID.
func (l *WorkerMessageLog) Find(id string) (WorkerMessage, bool, error) {
	messages, err := l.List()
	if err != nil {
		return WorkerMessage{}, false, err
	}
	for _, message := range messages {
		if message.ID == id {
			return message, true, nil
		}
	}
	return WorkerMessage{}, false, nil
}

func (l *WorkerMessageLog) appendLocked(event workerMessageEvent) error {
	if l.file == nil {
		return fmt.Errorf("worker message log is closed")
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode worker message: %w", err)
	}
	encoded = append(encoded, '\n')
	if _, err := l.file.Write(encoded); err != nil {
		return fmt.Errorf("append worker message: %w", err)
	}
	// Keep the append-only file bounded. Compaction is best-effort: the row is
	// already durable, so a failed rewrite must not report the append as lost.
	if info, statErr := l.file.Stat(); statErr == nil && info.Size() >= workerMessageCompactBytes &&
		(l.compactFloor == 0 || info.Size() >= l.compactFloor) {
		if compactErr := l.compactLocked(); compactErr != nil {
			log.Printf("[agentruntime] worker message compaction failed: %v", compactErr)
		}
	}
	return nil
}

func (l *WorkerMessageLog) foldLocked() ([]WorkerMessage, error) {
	file, err := os.Open(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read worker message log: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4<<10), 1<<20)
	byID := map[string]*WorkerMessage{}
	order := make([]string, 0)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event workerMessageEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			// A torn final line after a crash is expected; ignore unparsable
			// trailing data rather than failing the whole projection.
			continue
		}
		switch event.Op {
		case workerMessageOpCreated:
			if event.ID == "" {
				continue
			}
			// Every read is scoped to this log's session. A copied or corrupt
			// file cannot project another session's worker evidence.
			if l.sessionID != "" && event.SessionID != "" && event.SessionID != l.sessionID {
				continue
			}
			if _, exists := byID[event.ID]; !exists {
				order = append(order, event.ID)
			}
			message := &WorkerMessage{
				ID: event.ID, SessionID: event.SessionID, ChildID: event.ChildID,
				ParentRunID: event.ParentRunID, Direction: event.Direction, Kind: event.Kind,
				Body: event.Body, DedupeKey: event.DedupeKey, DecisionID: event.DecisionID,
				Status: WorkerMessageStatusCreated,
			}
			if at, err := time.Parse(time.RFC3339Nano, event.At); err == nil {
				message.CreatedAt = at
			}
			byID[event.ID] = message
			if event.DedupeKey != "" {
				index := workerDedupeIndex(l.sessionID, event.ChildID, event.DedupeKey)
				if _, exists := l.dedupe[index]; !exists {
					l.dedupe[index] = event.ID
				}
			}
		default:
			message, ok := byID[event.ID]
			if !ok {
				continue
			}
			message.Status = event.Op
			message.RunID = event.RunID
			message.Reason = event.Reason
			if at, err := time.Parse(time.RFC3339Nano, event.At); err == nil {
				message.UpdatedAt = at
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan worker message log: %w", err)
	}
	out := make([]WorkerMessage, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out, nil
}

// rebuildIndex folds the existing log once so dedupe survives a restart.
func (l *WorkerMessageLog) rebuildIndex() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err := l.foldLocked()
	return err
}

// Compact rewrites the log as one created row per current message when it grows
// past the threshold. The replacement is atomic (temp file + rename), so a crash
// leaves either the old complete log or the new complete log.
func (l *WorkerMessageLog) Compact() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.compactLocked()
}

// compactLocked rewrites the log while the caller holds l.mu.
func (l *WorkerMessageLog) compactLocked() error {
	info, err := os.Stat(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Size() < workerMessageCompactBytes {
		return nil
	}
	messages, err := l.foldLocked()
	if err != nil {
		return err
	}
	tempPath := l.path + ".compact"
	temp, err := os.OpenFile(tempPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(temp)
	for _, message := range messages {
		event := workerMessageEvent{
			Op: workerMessageOpCreated, ID: message.ID, SessionID: message.SessionID,
			ChildID: message.ChildID, ParentRunID: message.ParentRunID,
			Direction: message.Direction, Kind: message.Kind, Body: message.Body,
			DedupeKey: message.DedupeKey, DecisionID: message.DecisionID,
			At: message.CreatedAt.UTC().Format(time.RFC3339Nano),
		}
		if err := encoder.Encode(event); err != nil {
			temp.Close()
			os.Remove(tempPath)
			return err
		}
		// Compaction must preserve the folded state, not just the creation
		// facts: replay one terminal transition for every message that already
		// moved past "created" (delivered/observed/expired/cancelled).
		if message.Status != "" && message.Status != WorkerMessageStatusCreated {
			at := message.UpdatedAt
			if at.IsZero() {
				at = message.CreatedAt
			}
			if err := encoder.Encode(workerMessageEvent{
				Op: message.Status, ID: message.ID, RunID: message.RunID,
				Reason: message.Reason, At: at.UTC().Format(time.RFC3339Nano),
			}); err != nil {
				temp.Close()
				os.Remove(tempPath)
				return err
			}
		}
	}
	if err := temp.Close(); err != nil {
		os.Remove(tempPath)
		return err
	}
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
	if err := os.Rename(tempPath, l.path); err != nil {
		l.reopenLocked()
		return err
	}
	file, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		// Keep the log writable for the rest of the session even if the reopen
		// after a successful rename fails unexpectedly.
		l.reopenLocked()
		return err
	}
	l.file = file
	if info, statErr := file.Stat(); statErr == nil {
		l.compactFloor = info.Size() + workerMessageCompactBytes
	}
	return nil
}

// reopenLocked best-effort restores the append handle after a failed compaction
// rewrite. Callers hold l.mu.
func (l *WorkerMessageLog) reopenLocked() {
	file, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		log.Printf("[agentruntime] reopen worker message log after compaction failure: %v", err)
		return
	}
	l.file = file
}

// MessagesByChild returns the folded messages for one child, oldest first.
func (l *WorkerMessageLog) MessagesByChild(childID string) ([]WorkerMessage, error) {
	messages, err := l.List()
	if err != nil {
		return nil, err
	}
	out := make([]WorkerMessage, 0, len(messages))
	for _, message := range messages {
		if message.ChildID == childID {
			out = append(out, message)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func workerDedupeIndex(sessionID, childID, key string) string {
	return sessionID + "\x00" + childID + "\x00" + key
}

func sanitizeWorkerIDPart(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	if builder.Len() == 0 {
		return "worker"
	}
	return builder.String()
}

// truncateUTF8Bytes keeps the longest valid UTF-8 prefix within limit bytes.
func truncateUTF8Bytes(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && (text[cut]&0xC0) == 0x80 {
		cut--
	}
	return text[:cut]
}
