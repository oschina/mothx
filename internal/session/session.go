package session

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/startvibecoding/mothx/internal/dao"
	database "github.com/startvibecoding/mothx/internal/db"
	"github.com/startvibecoding/mothx/internal/platform"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/util"
)

const CurrentVersion = 3

var ErrSessionModified = errors.New("session was modified by another process")

// ErrSessionIDExists means a new session attempted to reuse an existing ID.
// A duplicate must be rejected: updating the sessions row would merge the new
// header with the old entries and create a forked conversation.
var ErrSessionIDExists = errors.New("session ID already exists")

// Manager manages a single session's state and persistence.
type Manager struct {
	mu         sync.RWMutex
	file       string // path to the session's .db handle file
	header     *Header
	entries    []interface{} // all entry types
	leafID     *string
	cwd        string
	sessionDir string
	subAgent   bool
}

type replayState struct {
	messages []provider.Message
	entryIDs []string
}

// SequencedMessage is a persisted conversation message with its entries.seq cursor.
type SequencedMessage struct {
	Seq     int64
	EntryID string
	Message provider.Message
}

// SequencedSessionRunEvent is a run lifecycle event with its table cursor.
type SequencedSessionRunEvent struct {
	Seq   int64
	Event SessionRunEvent
}

// SequencedSessionCapabilityEvent is a capability event with its table cursor.
type SequencedSessionCapabilityEvent struct {
	Seq   int64
	Event SessionCapabilityEvent
}

// encodePath encodes a directory path for use in a session directory name.
// Uses base64 URL encoding to avoid collisions from different characters mapping
// to the same replacement (e.g. "/" and ":" both mapped to "-").
func encodePath(p string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(p))
}

// New creates a new session manager for a new session.
func New(cwd, sessionDir string) *Manager {
	if sessionDir == "" {
		sessionDir = platform.SessionDir()
	}

	return &Manager{
		cwd:        cwd,
		sessionDir: sessionDir,
	}
}

// NewSubAgent creates a session manager whose records are stored separately
// from user-continuable sessions.
func NewSubAgent(cwd, sessionDir string) *Manager {
	m := New(cwd, sessionDir)
	m.subAgent = true
	return m
}

func (m *Manager) sessionTable() string {
	if m.subAgent {
		return "sub_session"
	}
	return "sessions"
}

func (m *Manager) entriesTable() string {
	if m.subAgent {
		return "sub_entries"
	}
	return "entries"
}

// Open opens an existing session file.
func Open(path string) (*Manager, error) {
	m := &Manager{file: path}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

// Reload refreshes a manager from the shared SQLite session database.
// Serve entry points may retain a Manager while another UI writes the same
// session; reload after acquiring the session runtime lock so the next append
// uses the current leaf instead of an old optimistic-lock parent.
func (m *Manager) Reload() error {
	if m == nil {
		return fmt.Errorf("session manager is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.file == "" {
		return fmt.Errorf("session manager is not initialized")
	}
	oldHeader, oldEntries, oldLeaf, oldCwd := m.header, m.entries, m.leafID, m.cwd
	m.header = nil
	m.entries = nil
	m.leafID = nil
	if err := m.load(); err != nil {
		m.header, m.entries, m.leafID, m.cwd = oldHeader, oldEntries, oldLeaf, oldCwd
		return err
	}
	return nil
}

// ContinueRecent continues the most recent session for a directory, or creates new.
func ContinueRecent(cwd, sessionDir string) (*Manager, error) {
	if sessionDir == "" {
		sessionDir = platform.SessionDir()
	}

	sessions, err := ListForDir(cwd, sessionDir)
	if err != nil {
		return nil, err
	}

	if len(sessions) > 0 {
		// Most recent
		sort.Slice(sessions, func(i, j int) bool {
			return sessions[i].ModTime.After(sessions[j].ModTime)
		})
		return Open(sessions[0].Path)
	}

	m := New(cwd, sessionDir)
	if err := m.Init(); err != nil {
		return nil, err
	}
	return m, nil
}

// OpenByPathOrID opens a session using either an explicit file path or a
// session ID for the supplied working directory.
func OpenByPathOrID(cwd, sessionDir, value string) (*Manager, error) {
	if value == "" {
		return nil, fmt.Errorf("session value is empty")
	}
	if strings.HasSuffix(value, ".db") || strings.ContainsRune(value, os.PathSeparator) {
		return Open(value)
	}
	return OpenByID(cwd, sessionDir, value)
}

// SessionInfo contains metadata about a session file.
type SessionInfo struct {
	Path            string
	ModTime         time.Time
	Name            string
	Cwd             string
	ChannelType     string
	ChannelID       string
	ParentSession   string
	ForkBoundarySeq int64
	SeedLength      int64
	ForkKind        string
	ExpertID        string
}

// sessionDirForCwd returns the encoded session directory path for a working directory.
func sessionDirForCwd(cwd, sessionDir string) string {
	encoded := encodePath(cwd)
	return filepath.Join(sessionDir, "--"+encoded+"--")
}

func canonicalDBPath(path string) (string, error) {
	return database.CanonicalPath(path)
}

func sqliteDSN(path string) string {
	return database.DSNForOS(path, false)
}

func sqliteDSNForOS(path string, windows bool) string {
	return database.DSNForOS(path, windows)
}

func cachedDB(path string) (*dao.Database, error) {
	connection, err := database.Open(path, EnsureCurrentSchema)
	if err != nil {
		return nil, err
	}
	return dao.WrapDatabase(connection), nil
}

// OpenStandaloneDB opens a configured standalone Bun connection. It is only
// intended for offline integrity checks; normal runtime code uses OpenRootDB.
func OpenStandaloneDB(path string) (*dao.Database, error) {
	connection, err := database.OpenStandalone(path, EnsureCurrentSchema)
	if err != nil {
		return nil, err
	}
	return dao.WrapStandaloneDatabase(connection), nil
}

// CloseDatabases checkpoints and closes all process-owned session connections.
func CloseDatabases() error {
	return database.CloseAll()
}

func openExistingSessionDB(sessionDir string) (*dao.Database, bool, error) {
	if sessionDir == "" {
		sessionDir = platform.SessionDir()
	}

	dbPath := filepath.Join(sessionDir, "sessions.db")
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}

	db, err := cachedDB(dbPath)
	return db, err == nil, err
}

// openExistingSessionDBReadOnly opens an existing sessions database for a
// safety preflight. Unlike openExistingSessionDB it never runs migrations,
// integrity repair, or WAL setup, and the caller must close the returned
// standalone handle.
func openExistingSessionDBReadOnly(sessionDir string) (*dao.Database, bool, error) {
	if sessionDir == "" {
		sessionDir = platform.SessionDir()
	}

	dbPath := filepath.Join(sessionDir, "sessions.db")
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}

	connection, err := database.OpenReadOnlyStandalone(dbPath)
	if err != nil {
		return nil, false, err
	}
	return dao.WrapStandaloneDatabase(connection), true, nil
}

// OpenRootDB opens the shared sessions.db through the DAO-owned database
// handle. Callers must not close it; use CloseDatabases for lifecycle control.
func OpenRootDB(sessionDir string) (*dao.Database, error) {
	connection, err := database.Open(rootDBPath(sessionDir), EnsureCurrentSchema)
	if err != nil {
		return nil, err
	}
	return dao.WrapDatabase(connection), nil
}

func rootDBPath(sessionDir string) string {
	if sessionDir == "" {
		sessionDir = platform.SessionDir()
	}
	return filepath.Join(sessionDir, "sessions.db")
}

func parseSessionTimestamp(timestampStr string) time.Time {
	ts, _ := time.Parse(time.RFC3339Nano, timestampStr)
	if ts.IsZero() {
		ts, _ = time.Parse(time.RFC3339, timestampStr)
	}
	return ts
}

func virtualSessionFile(sessionDir, id string, ts time.Time) string {
	return filepath.Join(sessionDir, fmt.Sprintf("%s_%s.db", ts.Format("20060102-150405"), id))
}

// ListForDir lists session files for a given working directory.
func ListForDir(cwd, sessionDir string) ([]SessionInfo, error) {
	if sessionDir == "" {
		sessionDir = platform.SessionDir()
	}

	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return nil, err
	}

	records, err := dao.NewSessionDAO(db.Bun()).ListForDir(context.Background(), cwd)
	if err != nil {
		return nil, err
	}
	var sessions []SessionInfo
	for _, record := range records {
		ts := parseSessionTimestamp(record.Timestamp)

		// Create a virtual file path in the sessionDir directory
		virtualFile := virtualSessionFile(sessionDir, record.ID, ts)

		sessions = append(sessions, SessionInfo{
			Path: virtualFile, ModTime: ts, Cwd: record.CWD, ChannelType: record.ChannelType, ChannelID: record.ChannelID,
			ParentSession: stringValue(record.ParentSession), ForkBoundarySeq: record.ForkBoundarySeq, SeedLength: record.SeedLength, ForkKind: record.ForkKind, ExpertID: record.ExpertID,
		})
	}
	return sessions, nil
}

// ListAll lists session files across all working directories.
func ListAll(sessionDir string, opts ...ListOption) ([]SessionInfo, error) {
	if sessionDir == "" {
		sessionDir = platform.SessionDir()
	}

	var opt listOptions
	for _, fn := range opts {
		fn(&opt)
	}

	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return nil, err
	}

	records, err := dao.NewSessionDAO(db.Bun()).List(context.Background(), dao.SessionListFilter{Search: opt.search, MessagesOnly: opt.messagesOnly, Limit: opt.limit, Offset: opt.offset})
	if err != nil {
		return nil, err
	}
	var sessions []SessionInfo
	for _, record := range records {
		ts := parseSessionTimestamp(record.Timestamp)
		sessions = append(sessions, SessionInfo{
			Path: virtualSessionFile(sessionDir, record.ID, ts), ModTime: ts, Cwd: record.CWD, ChannelType: record.ChannelType, ChannelID: record.ChannelID,
			ParentSession: stringValue(record.ParentSession), ForkBoundarySeq: record.ForkBoundarySeq, SeedLength: record.SeedLength, ForkKind: record.ForkKind, ExpertID: record.ExpertID,
		})
	}
	return sessions, nil
}

// CountWithMessages returns the number of sessions that contain at least one
// persisted conversation message. Empty sessions can be created transiently
// during startup or request setup and are not user-visible history.
func CountWithMessages(sessionDir string, opts ...ListOption) (int, error) {
	if sessionDir == "" {
		sessionDir = platform.SessionDir()
	}
	var opt listOptions
	opt.messagesOnly = true
	for _, fn := range opts {
		fn(&opt)
	}
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return 0, err
	}
	return dao.NewSessionDAO(db.Bun()).Count(context.Background(), dao.SessionListFilter{Search: opt.search, MessagesOnly: opt.messagesOnly})
}

func CountAll(sessionDir string) (int, error) {
	if sessionDir == "" {
		sessionDir = platform.SessionDir()
	}
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return 0, err
	}
	return dao.NewSessionDAO(db.Bun()).Count(context.Background(), dao.SessionListFilter{})
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

type listOptions struct {
	limit        int
	offset       int
	messagesOnly bool
	search       string
}

type ListOption func(*listOptions)

func WithLimit(limit int) ListOption {
	return func(o *listOptions) {
		o.limit = limit
	}
}

func WithOffset(offset int) ListOption {
	return func(o *listOptions) {
		o.offset = offset
	}
}

// WithMessagesOnly limits session listings to sessions containing at least one
// persisted conversation message. This avoids loading transient empty sessions
// during history pagination.
func WithMessagesOnly() ListOption {
	return func(o *listOptions) {
		o.messagesOnly = true
	}
}

// WithSearch filters sessions by ID, work directory, channel metadata, or
// persisted message/session-info content. It is intended for session listings.
func WithSearch(search string) ListOption {
	return func(o *listOptions) {
		o.search = strings.TrimSpace(search)
	}
}

// InitWithBinding initializes a new session with a channel binding.
func (m *Manager) InitWithBinding(channelType, channelID string) error {
	if err := validateBinding(channelType, channelID); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.initWithBindingLocked("", channelType, channelID)
}

// InitWithIDAndBinding initializes a session with a specific ID and channel binding.
func (m *Manager) InitWithIDAndBinding(id, channelType, channelID string) error {
	if err := validateBinding(channelType, channelID); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.initWithBindingLocked(id, channelType, channelID)
}

func validateBinding(channelType, channelID string) error {
	if channelType == "" {
		channelType = "local"
	}
	if channelType == "local" && channelID != "" {
		return fmt.Errorf("local session cannot have channel ID")
	}
	if channelType != "local" && channelType != "wechat" && channelType != "feishu" {
		return fmt.Errorf("unsupported channel type %q", channelType)
	}
	if channelType != "local" && channelID == "" {
		return fmt.Errorf("channel ID is required for %s session", channelType)
	}
	return nil
}

// Init initializes a new session with an auto-generated session ID.
// Must be called before appending entries.
func (m *Manager) Init() error {
	return m.InitWithID("")
}

// InitWithID initializes a new session using the provided session ID.
// If id is empty, a new random ID is generated.
func (m *Manager) InitWithID(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.initWithIDLocked(id)
}

func (m *Manager) initWithIDLocked(id string) error {
	return m.initWithBindingLocked(id, "local", "")
}

func (m *Manager) initWithBindingLocked(id, channelType, channelID string) error {
	explicitID := id != ""
	for attempt := 0; attempt < 8; attempt++ {
		now := time.Now()
		candidate := id
		if candidate == "" {
			candidate = GenerateID()
		}
		m.header = &Header{
			Type:        EntrySession,
			Version:     CurrentVersion,
			ID:          candidate,
			Timestamp:   now,
			Cwd:         m.cwd,
			ChannelType: channelType,
			ChannelID:   channelID,
		}
		m.entries = nil
		m.leafID = nil

		m.file = filepath.Join(m.sessionDir, fmt.Sprintf("%s_%s.db", now.Format("20060102-150405"), candidate))
		handlePath := ""

		// Write session ID to handle file only for per-channel user session directories.
		if strings.Contains(m.sessionDir, "channels") {
			dir := sessionDirForCwd(m.cwd, m.sessionDir)
			if err := os.MkdirAll(dir, 0700); err != nil {
				return fmt.Errorf("create session dir: %w", err)
			}
			m.file = filepath.Join(dir, fmt.Sprintf("%s_%s.db", now.Format("20060102-150405"), candidate))
			handlePath = m.file
		}

		err := m.writeEntry(m.header)
		if err == nil {
			if handlePath != "" {
				if err := os.WriteFile(handlePath, []byte(candidate), 0600); err != nil {
					return fmt.Errorf("write session handle file: %w", err)
				}
			}
			return nil
		}
		if explicitID || !errors.Is(err, ErrSessionIDExists) {
			return err
		}
	}
	return fmt.Errorf("generate unique session ID: %w", ErrSessionIDExists)
}

func (m *Manager) ensureInitializedLocked() error {
	if m.file != "" {
		return nil
	}
	return m.initWithIDLocked("")
}

// OpenByID opens the session for cwd whose session ID matches sessionID.
// Supports prefix matching — if sessionID matches multiple sessions, an error is returned.
func OpenByID(cwd, sessionDir, sessionID string) (*Manager, error) {
	if sessionDir == "" {
		sessionDir = platform.SessionDir()
	}

	dbPath := filepath.Join(sessionDir, "sessions.db")

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("session %s not found for cwd %s", sessionID, cwd)
	}

	db, err := cachedDB(dbPath)
	if err != nil {
		return nil, err
	}

	sessionDAO := dao.NewSessionDAO(db.Bun())
	exactID, err := sessionDAO.FindExact(context.Background(), "sessions", sessionID)
	if err == nil {
		row, headerErr := sessionDAO.Header(context.Background(), "sessions", sessionID)
		if headerErr == nil && row.CWD == cwd {
			return openSessionFromDB(exactID, sessionDir)
		}
	}
	matches, err := sessionDAO.PrefixIDs(context.Background(), "sessions", cwd, sessionID)
	if err != nil {
		return nil, err
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("session %s not found for cwd %s", sessionID, cwd)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("session ID %s is ambiguous for cwd %s", sessionID, cwd)
	}

	return openSessionFromDB(matches[0], sessionDir)
}

// OpenByIDExact opens a session by exact session ID regardless of cwd.
func OpenByIDExact(sessionDir, sessionID string) (*Manager, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session id is empty")
	}
	if sessionDir == "" {
		sessionDir = platform.SessionDir()
	}
	dbPath := filepath.Join(sessionDir, "sessions.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}
	return openSessionFromDB(sessionID, sessionDir)
}

// LatestAdditionalDirectoriesByID reads the replayed directory binding for a
// session without exposing SQLite details to protocol adapters.
func LatestAdditionalDirectoriesByID(sessionDir, sessionID string) ([]string, error) {
	m, err := OpenByIDExact(sessionDir, sessionID)
	if err != nil {
		return nil, err
	}
	entry, ok := m.GetLatestAdditionalDirectories()
	if !ok {
		return []string{}, nil
	}
	return append([]string(nil), entry.Directories...), nil
}

// LatestModelChangeByID reads the replayed provider/model binding for a
// session without exposing SQLite details to protocol adapters.
func LatestModelChangeByID(sessionDir, sessionID string) (ModelChangeEntry, bool, error) {
	m, err := OpenByIDExact(sessionDir, sessionID)
	if err != nil {
		return ModelChangeEntry{}, false, err
	}
	entry, ok := m.GetLatestModelChange()
	return entry, ok, nil
}

// findHandleForID finds the .db handle file that contains the given session ID.
func findHandleForID(dir, sessionID string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		// Skip sessions.db itself
		if e.Name() == "sessions.db" || strings.HasPrefix(e.Name(), "sessions.db-") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(data)) == sessionID {
			return path
		}
		// Also check filename pattern: timestamp_id.db
		base := strings.TrimSuffix(e.Name(), ".db")
		if idx := strings.Index(base, "_"); idx >= 0 {
			if strings.HasPrefix(base[idx+1:], sessionID) {
				return path
			}
		}
	}
	return ""
}

// openSessionFromDB reconstructs a Manager directly from the SQLite database
// when no handle file is available.
func openSessionFromDB(sessionID, dir string) (*Manager, error) {
	m := &Manager{
		sessionDir: dir,
	}

	dbPath := filepath.Join(dir, "sessions.db")
	db, err := cachedDB(dbPath)
	if err != nil {
		return nil, err
	}
	timestampStr, err := dao.NewSessionDAO(db.Bun()).Timestamp(context.Background(), "sessions", sessionID)
	if err != nil && err != dao.ErrNoRows {
		provider.DebugLogf("session open %q read timestamp: %v", sessionID, err)
	}

	if timestampStr != "" {
		ts, _ := time.Parse(time.RFC3339Nano, timestampStr)
		if ts.IsZero() {
			ts, _ = time.Parse(time.RFC3339, timestampStr)
		}
		if !ts.IsZero() {
			m.file = filepath.Join(dir, fmt.Sprintf("%s_%s.db", ts.Format("20060102-150405"), sessionID))
		}
	}

	if m.file == "" {
		m.file = filepath.Join(dir, sessionID+".db")
	}

	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

func sessionFileID(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, ".db")
	if idx := strings.Index(base, "_"); idx >= 0 {
		return base[idx+1:]
	}
	if base == "" || base == "active" || base == "sessions" {
		return ""
	}
	if len(base) >= 8 {
		return base
	}
	return ""
}

// AppendMessage adds a message entry.
func (m *Manager) AppendMessage(msg provider.Message) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ensureInitializedLocked(); err != nil {
		return "", err
	}

	id := GenerateID()
	entry := MessageEntry{
		EntryBase: EntryBase{
			Type:      EntryMessage,
			ID:        id,
			ParentID:  m.leafID,
			Timestamp: time.Now(),
		},
		Message: msg,
	}

	if err := m.writeEntry(entry); err != nil {
		return "", err
	}

	m.entries = append(m.entries, entry)
	m.leafID = &id
	return id, nil
}

// AppendMessages persists an ordered batch of conversation messages, folding
// one agent iteration's worth of tool results into a bounded number of write
// transactions (see maxEntriesPerTransaction) instead of one transaction per
// message. Entries are chained parent-to-child in order; the lease fence and
// the leaf-conflict check run once per transaction and provide the same
// guarantees AppendMessage provides per entry. On failure no entry of the
// failing transaction is persisted while messages committed by earlier chunk
// transactions stay durable — the same contract as a sequence of
// AppendMessage calls failing halfway through.
func (m *Manager) AppendMessages(msgs []provider.Message) ([]string, error) {
	if len(msgs) == 0 {
		return nil, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ensureInitializedLocked(); err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(msgs))
	for start := 0; start < len(msgs); start += maxEntriesPerTransaction {
		end := min(start+maxEntriesPerTransaction, len(msgs))
		chunk := msgs[start:end]
		batch := make([]MessageEntry, len(chunk))
		now := time.Now()
		parent := m.leafID
		for i, msg := range chunk {
			id := GenerateID()
			ids = append(ids, id)
			batch[i] = MessageEntry{
				EntryBase: EntryBase{
					Type:      EntryMessage,
					ID:        id,
					ParentID:  parent,
					Timestamp: now,
				},
				Message: msg,
			}
			// Each entry parents the previous one; copy the ID so a later
			// append reallocating ids cannot alias the pointer.
			entryID := id
			parent = &entryID
		}
		if err := m.writeEntries(batch); err != nil {
			return nil, err
		}
		for i := range batch {
			m.entries = append(m.entries, batch[i])
		}
		lastID := ids[len(ids)-1]
		m.leafID = &lastID
	}
	return ids, nil
}

// AppendModelChange records a model change.
func (m *Manager) AppendModelChange(providerName, modelID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ensureInitializedLocked(); err != nil {
		return "", err
	}

	id := GenerateID()
	entry := ModelChangeEntry{
		EntryBase: EntryBase{
			Type:      EntryModelChange,
			ID:        id,
			ParentID:  m.leafID,
			Timestamp: time.Now(),
		},
		Provider: providerName,
		ModelID:  modelID,
	}

	if err := m.writeEntry(entry); err != nil {
		return "", err
	}

	m.entries = append(m.entries, entry)
	m.leafID = &id
	return id, nil
}

// AppendModeChange records a session execution mode change.
func (m *Manager) AppendModeChange(mode string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ensureInitializedLocked(); err != nil {
		return "", err
	}

	id := GenerateID()
	entry := ModeChangeEntry{
		EntryBase: EntryBase{
			Type:      EntryModeChange,
			ID:        id,
			ParentID:  m.leafID,
			Timestamp: time.Now(),
		},
		Mode: mode,
	}
	if err := m.writeEntry(entry); err != nil {
		return "", err
	}
	m.entries = append(m.entries, entry)
	m.leafID = &id
	return id, nil
}

// AppendThinkingLevelChange records a thinking level change.
func (m *Manager) AppendThinkingLevelChange(level string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ensureInitializedLocked(); err != nil {
		return "", err
	}

	id := GenerateID()
	entry := ThinkingLevelChangeEntry{
		EntryBase: EntryBase{
			Type:      EntryThinkingChange,
			ID:        id,
			ParentID:  m.leafID,
			Timestamp: time.Now(),
		},
		ThinkingLevel: level,
	}

	if err := m.writeEntry(entry); err != nil {
		return "", err
	}

	m.entries = append(m.entries, entry)
	m.leafID = &id
	return id, nil
}

// AppendAdditionalDirectories records a complete replacement of the
// session's additional directory roots.
func (m *Manager) AppendAdditionalDirectories(directories []string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureInitializedLocked(); err != nil {
		return "", err
	}
	id := GenerateID()
	entry := AdditionalDirectoriesEntry{EntryBase: EntryBase{Type: EntryAdditionalDirectories, ID: id, ParentID: m.leafID, Timestamp: time.Now()}, Directories: append([]string(nil), directories...)}
	if err := m.writeEntry(entry); err != nil {
		return "", err
	}
	m.entries = append(m.entries, entry)
	m.leafID = &id
	return id, nil
}

// AppendCompaction records a context compaction.
func (m *Manager) AppendCompaction(summary, firstKeptEntryID string, tokensBefore int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ensureInitializedLocked(); err != nil {
		return "", err
	}

	summaryVersion := 1
	previousCompactionID := ""
	if previous, ok := latestCompactionLocked(m.entries); ok {
		previousCompactionID = previous.ID
		if previous.SummaryVersion > 0 {
			summaryVersion = previous.SummaryVersion + 1
		} else {
			summaryVersion = 2
		}
	}

	id := GenerateID()
	entry := CompactionEntry{
		EntryBase: EntryBase{
			Type:      EntryCompaction,
			ID:        id,
			ParentID:  m.leafID,
			Timestamp: time.Now(),
		},
		Summary:              summary,
		FirstKeptEntry:       firstKeptEntryID,
		TokensBefore:         tokensBefore,
		SummaryVersion:       summaryVersion,
		PreviousCompactionID: previousCompactionID,
		LastSummarizedEntry:  lastSummarizedEntryIDLocked(m.entries, firstKeptEntryID),
	}

	if err := m.writeEntry(entry); err != nil {
		return "", err
	}

	m.entries = append(m.entries, entry)
	m.leafID = &id
	return id, nil
}

// AppendContentOverride records an append-only replacement of a persisted
// message entry's content. Replay substitutes msg for the target entry while
// the original entry stays in the log for audit. It is the durable half of the
// provider content-rejection recovery: when a provider permanently refuses an
// image, the Runtime strips it in memory and records the override so every
// later replay (this process or a reload) never re-sends the refused content.
func (m *Manager) AppendContentOverride(targetEntryID string, msg provider.Message, reason, code string) (string, error) {
	if strings.TrimSpace(targetEntryID) == "" {
		return "", fmt.Errorf("content override target entry ID is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ensureInitializedLocked(); err != nil {
		return "", err
	}

	found := false
	for _, entry := range m.entries {
		if message, ok := entry.(MessageEntry); ok && message.ID == targetEntryID {
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("content override target %s is not a message entry", targetEntryID)
	}

	id := GenerateID()
	entry := ContentOverrideEntry{
		EntryBase: EntryBase{
			Type:      EntryContentOverride,
			ID:        id,
			ParentID:  m.leafID,
			Timestamp: time.Now(),
		},
		TargetEntryID: targetEntryID,
		Message:       cloneMessage(msg),
		Reason:        reason,
		Code:          code,
	}

	if err := m.writeEntry(entry); err != nil {
		return "", err
	}

	m.entries = append(m.entries, entry)
	m.leafID = &id
	return id, nil
}

func latestCompactionLocked(entries []interface{}) (CompactionEntry, bool) {
	for i := len(entries) - 1; i >= 0; i-- {
		if entry, ok := entries[i].(CompactionEntry); ok {
			return entry, true
		}
	}
	return CompactionEntry{}, false
}

func lastSummarizedEntryIDLocked(entries []interface{}, firstKeptEntryID string) string {
	state := buildReplayState(entries)
	if firstKeptEntryID == "" {
		for i := len(state.entryIDs) - 1; i >= 0; i-- {
			if state.entryIDs[i] != "" {
				return state.entryIDs[i]
			}
		}
		return ""
	}
	for i, id := range state.entryIDs {
		if id != firstKeptEntryID {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			if state.entryIDs[j] != "" {
				return state.entryIDs[j]
			}
		}
		return ""
	}
	return ""
}

// AppendSessionInfo records a session display name. It is retained for
// compatibility; new callers should use AppendSessionTitle with a source.
func (m *Manager) AppendSessionInfo(name string) (string, error) {
	return m.AppendSessionTitle(name, "manual")
}

// AppendSessionTitle records a session display name and its origin.
func (m *Manager) AppendSessionTitle(name, source string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("session title is required")
	}
	if source != "manual" && source != "auto" {
		return "", fmt.Errorf("invalid session title source")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureInitializedLocked(); err != nil {
		return "", err
	}
	id := GenerateID()
	entry := SessionInfoEntry{EntryBase: EntryBase{Type: EntrySessionInfo, ID: id, ParentID: m.leafID, Timestamp: time.Now()}, Name: name, Source: source}
	if err := m.writeEntry(entry); err != nil {
		return "", err
	}
	m.entries = append(m.entries, entry)
	m.leafID = &id
	return id, nil
}

// ReplayState is the reconstructed conversation state after applying compactions.
type ReplayState struct {
	Messages []provider.Message
	EntryIDs []string
}

// GetMessages extracts all messages from the current branch.
func (m *Manager) GetMessages() []provider.Message {
	state := m.GetReplayState()
	return state.Messages
}

// GetReplayState returns the current branch after applying compaction entries.
func (m *Manager) GetReplayState() ReplayState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state := buildReplayState(m.entries)
	return ReplayState{
		Messages: state.messages,
		EntryIDs: state.entryIDs,
	}
}

// GetLeafID returns the current leaf entry ID.
func (m *Manager) GetLeafID() *string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.leafID
}

// GetLatestCompaction returns the newest compaction entry in the current session.
func (m *Manager) GetLatestCompaction() (CompactionEntry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return latestCompactionLocked(m.entries)
}

// GetLatestModelChange returns the newest model binding in the session.
func (m *Manager) GetLatestModelChange() (ModelChangeEntry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := len(m.entries) - 1; i >= 0; i-- {
		if entry, ok := m.entries[i].(ModelChangeEntry); ok {
			return entry, true
		}
	}
	return ModelChangeEntry{}, false
}

// GetLatestModeChange returns the newest session mode in the session.
func (m *Manager) GetLatestModeChange() (ModeChangeEntry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := len(m.entries) - 1; i >= 0; i-- {
		if entry, ok := m.entries[i].(ModeChangeEntry); ok {
			return entry, true
		}
	}
	return ModeChangeEntry{}, false
}

// GetLatestThinkingLevelChange returns the newest thinking level in the session.
func (m *Manager) GetLatestThinkingLevelChange() (ThinkingLevelChangeEntry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := len(m.entries) - 1; i >= 0; i-- {
		if entry, ok := m.entries[i].(ThinkingLevelChangeEntry); ok {
			return entry, true
		}
	}
	return ThinkingLevelChangeEntry{}, false
}

// GetLatestAdditionalDirectories returns the latest complete directory-root
// binding persisted in this session.
func (m *Manager) GetLatestAdditionalDirectories() (AdditionalDirectoriesEntry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := len(m.entries) - 1; i >= 0; i-- {
		if entry, ok := m.entries[i].(AdditionalDirectoriesEntry); ok {
			entry.Directories = append([]string(nil), entry.Directories...)
			return entry, true
		}
	}
	return AdditionalDirectoriesEntry{}, false
}

// GetFile returns the session file path.
func (m *Manager) GetFile() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.file
}

// GetSessionDir returns the root directory containing this manager's shared
// sessions database. Runtime extensions use it for auxiliary session tables.
func (m *Manager) GetSessionDir() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.sessionDir == "" && m.file != "" {
		return filepath.Dir(resolveDBPath(m.file))
	}
	return m.sessionDir
}

// GetHeader returns the session header.
func (m *Manager) GetHeader() *Header {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.header
}

// StartConversationTurn opens the durable boundary used by Session fork
// resolution. It is intentionally optional on session.Store so transient and
// in-memory agents do not need a SQLite turn index.
func (m *Manager) StartConversationTurn(turnID, intentID, runID string) error {
	if m == nil {
		return fmt.Errorf("session manager is nil")
	}
	m.mu.Lock()
	if err := m.ensureInitializedLocked(); err != nil {
		m.mu.Unlock()
		return err
	}
	sessionDir, sessionID, file := m.sessionDir, m.header.ID, m.file
	m.mu.Unlock()
	if sessionDir == "" {
		sessionDir = filepath.Dir(resolveDBPath(file))
	}
	if err := StartConversationTurn(sessionDir, ConversationTurn{ID: turnID, SessionID: sessionID, IntentID: intentID, RunID: runID}); err != nil {
		return err
	}
	if err := m.Reload(); err != nil {
		if cleanupErr := EndConversationTurn(sessionDir, sessionID, turnID, "failed", "turn_reload", time.Now()); cleanupErr != nil {
			return fmt.Errorf("reload session after starting conversation turn: %w (turn cleanup: %v)", err, cleanupErr)
		}
		return err
	}
	return nil
}

// EndConversationTurn closes the durable boundary used by Session fork
// resolution. It is safe for callers to report failed, cancelled and
// incomplete outcomes; all are terminal turn states.
func (m *Manager) EndConversationTurn(turnID, status, stopReason string) error {
	if m == nil || m.header == nil {
		return fmt.Errorf("session manager is not initialized")
	}
	m.mu.RLock()
	sessionDir, sessionID, file := m.sessionDir, m.header.ID, m.file
	m.mu.RUnlock()
	if sessionDir == "" {
		sessionDir = filepath.Dir(resolveDBPath(file))
	}
	if err := EndConversationTurn(sessionDir, sessionID, turnID, status, stopReason, time.Now()); err != nil {
		return err
	}
	return m.Reload()
}

func buildReplayState(entries []interface{}) replayState {
	overrides := messageContentOverrides(entries)
	state := replayState{}
	for _, entry := range entries {
		switch e := entry.(type) {
		case MessageEntry:
			msg := e.Message
			if replacement, ok := overrides[e.ID]; ok {
				msg = replacement
			}
			state.messages = append(state.messages, cloneMessage(msg))
			state.entryIDs = append(state.entryIDs, e.ID)
		case CompactionEntry:
			applyCompactionEntry(&state, e)
		}
	}
	return state
}

// messageContentOverrides collects the newest content override for each target
// message entry. The overrides are resolved before any message is appended
// because an override entry is appended after its target, so a single forward
// pass cannot see it in time. Later overrides for the same target win.
func messageContentOverrides(entries []interface{}) map[string]provider.Message {
	overrides := make(map[string]provider.Message)
	for _, entry := range entries {
		e, ok := entry.(ContentOverrideEntry)
		if !ok || e.TargetEntryID == "" {
			continue
		}
		overrides[e.TargetEntryID] = e.Message
	}
	return overrides
}

type sequencedReplayState struct {
	messages []SequencedMessage
	entryIDs []string
}

func applySequencedCompactionEntry(state *sequencedReplayState, entry CompactionEntry, seq int64) {
	if state == nil {
		return
	}

	summary := provider.NewSystemInjectedUserMessage(entry.Summary)
	if entry.FirstKeptEntry == "" {
		state.messages = []SequencedMessage{{
			Seq:     seq,
			EntryID: entry.ID,
			Message: summary,
		}}
		state.entryIDs = []string{entry.ID}
		return
	}

	firstKept := -1
	for i, id := range state.entryIDs {
		if id == entry.FirstKeptEntry {
			firstKept = i
			break
		}
	}
	if firstKept < 0 {
		// Tolerant fallback: the first-kept entry may belong to a different
		// branch. Keep the full history instead of guessing, but surface the
		// skip so silent compaction loss is observable.
		log.Printf("[session] warning: sequenced compaction %s skipped at seq %d, first kept entry %q not found in replay state", entry.ID, seq, entry.FirstKeptEntry)
		return
	}
	if firstKept > len(state.messages) || firstKept > len(state.entryIDs) {
		log.Printf("[session] warning: sequenced compaction %s skipped at seq %d, replay state out of sync (firstKept=%d messages=%d entryIDs=%d)", entry.ID, seq, firstKept, len(state.messages), len(state.entryIDs))
		return
	}

	nextMessages := make([]SequencedMessage, 0, 1+len(state.messages[firstKept:]))
	nextMessages = append(nextMessages, SequencedMessage{
		Seq:     seq,
		EntryID: entry.ID,
		Message: summary,
	})
	for _, msg := range state.messages[firstKept:] {
		cloned := msg
		cloned.Message = cloneMessage(msg.Message)
		cloned.Message.Usage = nil
		nextMessages = append(nextMessages, cloned)
	}

	nextEntryIDs := make([]string, 0, 1+len(state.entryIDs[firstKept:]))
	nextEntryIDs = append(nextEntryIDs, entry.ID)
	nextEntryIDs = append(nextEntryIDs, append([]string(nil), state.entryIDs[firstKept:]...)...)

	state.messages = nextMessages
	state.entryIDs = nextEntryIDs
}

func applyCompactionEntry(state *replayState, entry CompactionEntry) {
	if state == nil {
		return
	}

	summary := provider.NewSystemInjectedUserMessage(entry.Summary)
	if entry.FirstKeptEntry == "" {
		state.messages = []provider.Message{summary}
		state.entryIDs = []string{""}
		return
	}

	firstKept := -1
	for i, id := range state.entryIDs {
		if id == entry.FirstKeptEntry {
			firstKept = i
			break
		}
	}
	if firstKept < 0 {
		// Tolerant fallback: the first-kept entry may belong to a different
		// branch. Keep the full history instead of guessing, but surface the
		// skip so silent compaction loss is observable.
		log.Printf("[session] warning: compaction %s skipped, first kept entry %q not found in replay state", entry.ID, entry.FirstKeptEntry)
		return
	}
	// Guard against message/entryID slices that may be out of sync to avoid
	// slicing out of bounds below.
	if firstKept > len(state.messages) || firstKept > len(state.entryIDs) {
		log.Printf("[session] warning: compaction %s skipped, replay state out of sync (firstKept=%d messages=%d entryIDs=%d)", entry.ID, firstKept, len(state.messages), len(state.entryIDs))
		return
	}

	nextMessages := make([]provider.Message, 0, 1+len(state.messages[firstKept:]))
	nextMessages = append(nextMessages, summary)
	for _, msg := range state.messages[firstKept:] {
		cloned := cloneMessage(msg)
		cloned.Usage = nil
		nextMessages = append(nextMessages, cloned)
	}

	nextEntryIDs := make([]string, 0, 1+len(state.entryIDs[firstKept:]))
	nextEntryIDs = append(nextEntryIDs, "")
	nextEntryIDs = append(nextEntryIDs, append([]string(nil), state.entryIDs[firstKept:]...)...)

	state.messages = nextMessages
	state.entryIDs = nextEntryIDs
}

func cloneMessage(msg provider.Message) provider.Message {
	cloned := msg
	if len(msg.Contents) > 0 {
		cloned.Contents = make([]provider.ContentBlock, len(msg.Contents))
		for i, block := range msg.Contents {
			cloned.Contents[i] = cloneContentBlock(block)
		}
	}
	if msg.Usage != nil {
		usage := *msg.Usage
		cloned.Usage = &usage
	}
	return cloned
}

func cloneContentBlock(block provider.ContentBlock) provider.ContentBlock {
	cloned := block
	if block.Image != nil {
		image := *block.Image
		cloned.Image = &image
	}
	if block.ToolCall != nil {
		toolCall := *block.ToolCall
		toolCall.Arguments = append([]byte(nil), block.ToolCall.Arguments...)
		cloned.ToolCall = &toolCall
	}
	if block.CacheControl != nil {
		cacheControl := *block.CacheControl
		cloned.CacheControl = &cacheControl
	}
	return cloned
}

// resolveDBPath determines the path to the shared sessions.db for a given session file.
//
// The mapping is heuristic and assumes the on-disk layout owned by this
// package: session handles live either in a per-project directory whose name
// contains "--" (the encoded-cwd convention, sharing one sessions.db in the
// session root) or in a per-user messaging channel directory under a
// "channels" path element (sessions.db beside the handles). A custom
// sessionDir that happens to contain "--" or "/channels/" will resolve to the
// corresponding layout; callers providing non-standard directories must
// follow those layout conventions.
func resolveDBPath(sessionFilePath string) string {
	clean := filepath.Clean(sessionFilePath)
	dir := filepath.Dir(clean)

	// If inside standard session dir --<encoded>--, use the shared DB in the parent session root.
	if strings.Contains(filepath.Base(dir), "--") {
		return filepath.Join(filepath.Dir(dir), "sessions.db")
	}

	// If inside messaging channel per-user sessions dir, use the DB beside active/archive handles.
	if strings.Contains(clean, string(filepath.Separator)+"channels"+string(filepath.Separator)) {
		return filepath.Join(dir, "sessions.db")
	}

	// If dir is "." or empty, or does not exist, use default home fallback if possible
	if dir == "." || dir == "" {
		return filepath.Join(platform.SessionDir(), "sessions.db")
	}

	return filepath.Join(dir, "sessions.db")
}

func (m *Manager) withDB(fn func(*dao.Database) error) error {
	dbPath := resolveDBPath(m.file)
	db, err := cachedDB(dbPath)
	if err != nil {
		return err
	}
	return fn(db)
}

func getEntryMetadata(entry interface{}) (id string, typeStr string, parentID *string, timestamp time.Time) {
	switch e := entry.(type) {
	case *Header:
		return e.ID, string(e.Type), nil, e.Timestamp
	case Header:
		return e.ID, string(e.Type), nil, e.Timestamp
	case *MessageEntry:
		return e.ID, string(e.Type), e.ParentID, e.Timestamp
	case MessageEntry:
		return e.ID, string(e.Type), e.ParentID, e.Timestamp
	case *ModelChangeEntry:
		return e.ID, string(e.Type), e.ParentID, e.Timestamp
	case ModelChangeEntry:
		return e.ID, string(e.Type), e.ParentID, e.Timestamp
	case *ModeChangeEntry:
		return e.ID, string(e.Type), e.ParentID, e.Timestamp
	case ModeChangeEntry:
		return e.ID, string(e.Type), e.ParentID, e.Timestamp
	case *ThinkingLevelChangeEntry:
		return e.ID, string(e.Type), e.ParentID, e.Timestamp
	case ThinkingLevelChangeEntry:
		return e.ID, string(e.Type), e.ParentID, e.Timestamp
	case *AdditionalDirectoriesEntry:
		return e.ID, string(e.Type), e.ParentID, e.Timestamp
	case AdditionalDirectoriesEntry:
		return e.ID, string(e.Type), e.ParentID, e.Timestamp
	case *CompactionEntry:
		return e.ID, string(e.Type), e.ParentID, e.Timestamp
	case CompactionEntry:
		return e.ID, string(e.Type), e.ParentID, e.Timestamp
	case *ContentOverrideEntry:
		return e.ID, string(e.Type), e.ParentID, e.Timestamp
	case ContentOverrideEntry:
		return e.ID, string(e.Type), e.ParentID, e.Timestamp
	case *SessionInfoEntry:
		return e.ID, string(e.Type), e.ParentID, e.Timestamp
	case SessionInfoEntry:
		return e.ID, string(e.Type), e.ParentID, e.Timestamp
	case *TurnStartEntry:
		return e.ID, string(e.Type), e.ParentID, e.Timestamp
	case TurnStartEntry:
		return e.ID, string(e.Type), e.ParentID, e.Timestamp
	case *TurnEndEntry:
		return e.ID, string(e.Type), e.ParentID, e.Timestamp
	case TurnEndEntry:
		return e.ID, string(e.Type), e.ParentID, e.Timestamp
	case *BranchSummaryEntry:
		return e.ID, string(e.Type), e.ParentID, e.Timestamp
	case BranchSummaryEntry:
		return e.ID, string(e.Type), e.ParentID, e.Timestamp
	case *LabelEntry:
		return e.ID, string(e.Type), e.ParentID, e.Timestamp
	case LabelEntry:
		return e.ID, string(e.Type), e.ParentID, e.Timestamp
	default:
		return "", "", nil, time.Now()
	}
}

// load reads a session from the SQLite database using the handle file's session ID.
func (m *Manager) load() error {
	var sessionID string
	idBytes, err := os.ReadFile(m.file)
	if err == nil {
		sessionID = strings.TrimSpace(string(idBytes))
	} else if os.IsNotExist(err) {
		sessionID = sessionFileID(m.file)
	} else {
		return fmt.Errorf("read session handle file: %w", err)
	}

	if sessionID == "" {
		return fmt.Errorf("could not determine session ID from %s", m.file)
	}

	return m.withDB(func(db *dao.Database) error {
		// Load session metadata
		var cwd, timestamp, parentSession, channelType, channelID, forkKind dao.NullString
		var forkBoundarySeq, seedLength int64
		var version int
		record, err := dao.NewSessionDAO(db.Bun()).Header(context.Background(), m.sessionTable(), sessionID)
		if record != nil {
			cwd.String, cwd.Valid = record.CWD, true
			timestamp.String, timestamp.Valid = record.Timestamp, true
			parentSession.String, parentSession.Valid = stringValue(record.ParentSession), record.ParentSession != nil
			version, channelType.String, channelType.Valid, channelID.String, channelID.Valid = record.Version, record.ChannelType, true, record.ChannelID, true
			forkBoundarySeq, seedLength, forkKind.String, forkKind.Valid = record.ForkBoundarySeq, record.SeedLength, record.ForkKind, true
		}
		if err != nil {
			if err == dao.ErrNoRows {
				return fmt.Errorf("session %q not registered in DB", sessionID)
			}
			return err
		}

		ts, _ := time.Parse(time.RFC3339Nano, timestamp.String)
		m.header = &Header{
			Type:            EntrySession,
			Version:         version,
			ID:              sessionID,
			Timestamp:       ts,
			Cwd:             cwd.String,
			ParentSession:   parentSession.String,
			ChannelType:     channelType.String,
			ChannelID:       channelID.String,
			ForkBoundarySeq: forkBoundarySeq,
			SeedLength:      seedLength,
			ForkKind:        forkKind.String,
			ExpertID:        record.ExpertID,
		}
		m.cwd = cwd.String

		records, err := dao.NewSessionDAO(db.Bun()).Entries(context.Background(), m.entriesTable(), sessionID)
		if err != nil {
			return err
		}

		var corruptRows int
		for _, record := range records {
			typeStr, dataStr := record.Type, record.Data

			line := []byte(dataStr)
			switch EntryType(typeStr) {
			case EntrySession:
				// Already loaded from sessions table

			case EntryMessage:
				var e MessageEntry
				if err := json.Unmarshal(line, &e); err != nil {
					corruptRows++
					continue
				}
				m.entries = append(m.entries, e)
				m.leafID = &e.ID

			case EntryModelChange:
				var e ModelChangeEntry
				if err := json.Unmarshal(line, &e); err != nil {
					corruptRows++
					continue
				}
				m.entries = append(m.entries, e)
				m.leafID = &e.ID

			case EntryModeChange:
				var e ModeChangeEntry
				if err := json.Unmarshal(line, &e); err != nil {
					corruptRows++
					continue
				}
				m.entries = append(m.entries, e)
				m.leafID = &e.ID

			case EntryThinkingChange:
				var e ThinkingLevelChangeEntry
				if err := json.Unmarshal(line, &e); err != nil {
					corruptRows++
					continue
				}
				m.entries = append(m.entries, e)
				m.leafID = &e.ID

			case EntryAdditionalDirectories:
				var e AdditionalDirectoriesEntry
				if err := json.Unmarshal(line, &e); err != nil {
					corruptRows++
					continue
				}
				e.Directories = append([]string(nil), e.Directories...)
				m.entries = append(m.entries, e)
				m.leafID = &e.ID

			case EntryCompaction:
				var e CompactionEntry
				if err := json.Unmarshal(line, &e); err != nil {
					corruptRows++
					continue
				}
				m.entries = append(m.entries, e)
				m.leafID = &e.ID

			case EntryContentOverride:
				var e ContentOverrideEntry
				if err := json.Unmarshal(line, &e); err != nil {
					corruptRows++
					continue
				}
				m.entries = append(m.entries, e)
				m.leafID = &e.ID

			case EntrySessionInfo:
				var e SessionInfoEntry
				if err := json.Unmarshal(line, &e); err != nil {
					corruptRows++
					continue
				}
				m.entries = append(m.entries, e)
				m.leafID = &e.ID

			case EntryTurnStart:
				var e TurnStartEntry
				if err := json.Unmarshal(line, &e); err != nil {
					corruptRows++
					continue
				}
				m.entries = append(m.entries, e)
				m.leafID = &e.ID

			case EntryTurnEnd:
				var e TurnEndEntry
				if err := json.Unmarshal(line, &e); err != nil {
					corruptRows++
					continue
				}
				m.entries = append(m.entries, e)
				m.leafID = &e.ID

			case EntryBranchSummary:
				var e BranchSummaryEntry
				if err := json.Unmarshal(line, &e); err != nil {
					corruptRows++
					continue
				}
				m.entries = append(m.entries, e)
				m.leafID = &e.ID
			}
		}

		if corruptRows > 0 {
			log.Printf("[session] warning: skipped %d corrupt row(s) in %s", corruptRows, m.file)
		}
		return nil
	})
}

// sessionChildTables lists every session_id-keyed child table of sessions,
// ordered child-first (tables that reference another child table come before
// their parent) so deletion stays safe even if SQLite foreign key enforcement
// is enabled later. delivery_operations and attachment_deliveries are not
// listed here because they have no session_id column; dao.DeleteSession prunes
// them through their session-owned parents. session_fork_requests uses
// source/child session columns and is handled by the DAO as well.
// request_stats is intentionally excluded: usage statistics survive session
// deletion for billing/stats purposes.
var sessionChildTables = []string{
	"runtime_submissions",    // references session_execution_intents, session_runs
	"input_resource_events",  // references input_resources
	"session_run_recoveries", // references session_runs
	"delivery_intents",       // references session_runs
	"input_resources",
	"session_attachments",
	"session_execution_intents",
	"session_run_events",
	"session_runs",
	"session_capability_events",
	"session_capabilities",
	"session_esm_objectives",
	"session_esm_guidance",
	"session_metadata",
	"conversation_turns",
	"session_runtime_leases",
	"cron_jobs",
	"response_items",
	"tool_execution_records",
	"response_runs",
	"response_session_state",
	"response_turns",
	"session_channel_tool_generations",
	"session_channel_tools",
	"entries",
}

// deleteSessionDataTx removes every root-session child row before deleting the
// session itself. Keep this list in one place so deletion and integrity tests
// cannot silently drift apart.
func deleteSessionDataTx(tx *dao.Tx, sessionID string) error {
	if tx == nil {
		return fmt.Errorf("delete session transaction is nil")
	}
	return dao.NewSessionDAO(nil).DeleteSession(context.Background(), tx, sessionID, sessionChildTables)
}

// DeleteSession deletes a session only after acquiring its shared mutation
// lease. This keeps a direct caller from deleting a session that another
// process still owns for execution. Callers which already hold a mutation
// lease for a multi-session operation use DeleteSessionWithMutation instead.
func DeleteSession(path string, sessionDir string) error {
	cleanPath, sessionID, err := deleteSessionTarget(path, sessionDir)
	if err != nil {
		return err
	}
	if sessionID == "" {
		return removeSessionHandle(cleanPath)
	}
	guard, err := AcquireMutation(sessionDir, sessionID)
	if err != nil {
		if errors.Is(err, ErrRuntimeSessionNotFound) {
			// Deleting a stale handle remains idempotent.
			return removeSessionHandle(cleanPath)
		}
		return fmt.Errorf("acquire deletion lease for session %s: %w", sessionID, err)
	}
	defer guard.Release()
	return deleteSessionWithMutationTarget(cleanPath, sessionDir, sessionID, guard)
}

// DeleteSessionWithMutation deletes a session while the caller holds its
// Runtime-owned mutation lease. It is for coordinated multi-session operations
// that cannot reacquire the process-local lock per child. The lease identity is
// checked again inside the deletion transaction before any session row is
// removed.
func DeleteSessionWithMutation(path string, sessionDir string, guard *RuntimeLeaseGuard) error {
	cleanPath, sessionID, err := deleteSessionTarget(path, sessionDir)
	if err != nil {
		return err
	}
	return deleteSessionWithMutationTarget(cleanPath, sessionDir, sessionID, guard)
}

// deleteSessionTarget validates a session handle and resolves the session ID it
// names. It deliberately accepts a missing handle: callers may be cleaning up
// an already-removed virtual handle, and the durable row remains authoritative.
func deleteSessionTarget(path string, sessionDir string) (cleanPath, sessionID string, err error) {
	cleanPath, err = filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", "", fmt.Errorf("resolve session path: %w", err)
	}
	cleanSessionDir, err := filepath.Abs(filepath.Clean(sessionDir))
	if err != nil {
		return "", "", fmt.Errorf("resolve session dir: %w", err)
	}
	rel, err := filepath.Rel(cleanSessionDir, cleanPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("session path %s is outside session directory %s", path, sessionDir)
	}
	if filepath.Ext(cleanPath) != ".db" {
		return "", "", fmt.Errorf("session path %s is not a .db file", path)
	}
	base := filepath.Base(cleanPath)
	if base == "sessions.db" || strings.HasPrefix(base, "sessions.db-") {
		return "", "", fmt.Errorf("refusing to delete shared SQLite database %s as a session handle", path)
	}

	idBytes, err := os.ReadFile(cleanPath)
	if err == nil {
		sessionID = strings.TrimSpace(string(idBytes))
	} else if os.IsNotExist(err) {
		sessionID = sessionFileID(cleanPath)
	} else {
		return "", "", fmt.Errorf("read session handle %s: %w", cleanPath, err)
	}
	return cleanPath, sessionID, nil
}

func deleteSessionWithMutationTarget(cleanPath, sessionDir, sessionID string, guard *RuntimeLeaseGuard) error {
	binding := guard.Binding()
	if binding.SessionID != sessionID || binding.Purpose != RuntimeLeasePurposeMutation || binding.RunID != "" ||
		binding.DatabaseIdentity != RuntimeDatabaseIdentity(sessionDir) {
		return ErrRuntimeLeaseLost
	}
	dbPath := resolveDBPath(cleanPath)
	db, err := cachedDB(dbPath)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin deleting session %s: %w", sessionID, err)
	}
	defer tx.Rollback()
	if _, err := validateRuntimeLeaseBindingTx(tx, sessionDir, sessionID, "", RuntimeLeasePurposeMutation); err != nil {
		return fmt.Errorf("validate deletion lease for session %s: %w", sessionID, err)
	}
	if err := deleteSessionDataTx(tx, sessionID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit deleting session %s: %w", sessionID, err)
	}
	return removeSessionHandle(cleanPath)
}

func removeSessionHandle(path string) error {
	if _, err := os.Stat(path); err == nil {
		return os.Remove(path)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

// SessionDetail contains detailed metadata about a session for display.
type SessionDetail struct {
	SessionInfo
	ID           string
	MessageCount int
	Preview      string // first user message (truncated)
}

// SessionCapabilities stores persisted per-session runtime capability state.
type SessionCapabilities struct {
	SessionID    string
	Mode         string
	DisplayMode  string
	DelegateMode bool
	MultiAgent   bool
	Workflows    bool
	WebSearch    bool
	Browser      bool
	A2AMaster    bool
	UpdatedAt    time.Time
}

// SessionRunEvent records one lifecycle event for a single chat/run execution.
type SessionRunEvent struct {
	ID        string
	SessionID string
	RunID     string
	EventType string
	Source    string
	Status    string
	Model     string
	Mode      string
	Timestamp time.Time
	Data      json.RawMessage
}

// SessionCapabilityEvent records one capability state transition.
type SessionCapabilityEvent struct {
	ID         string
	SessionID  string
	RunID      string
	EventType  string
	Source     string
	Actor      string
	Capability string
	OldValue   string
	NewValue   string
	Timestamp  time.Time
	Data       json.RawMessage
}

// LoadSessionCapabilities loads persisted capabilities for a session.
func LoadSessionCapabilities(sessionDir, sessionID string) (*SessionCapabilities, bool, error) {
	if sessionID == "" {
		return nil, false, nil
	}
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return nil, false, err
	}

	record, err := dao.NewSessionDAO(db.Bun()).Capability(context.Background(), sessionID)
	if err == dao.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if record == nil {
		return nil, false, nil
	}
	caps := SessionCapabilities{SessionID: record.SessionID, Mode: record.Mode, DisplayMode: record.DisplayMode,
		DelegateMode: record.DelegateMode != 0, MultiAgent: record.MultiAgent != 0, Workflows: record.Workflows != 0,
		WebSearch: record.WebSearch != 0, Browser: record.Browser != 0, A2AMaster: record.A2AMaster != 0, UpdatedAt: parseSessionTimestamp(record.UpdatedAt)}
	return &caps, true, nil
}

// SaveSessionCapabilities persists per-session runtime capability state.
func SaveSessionCapabilities(sessionDir string, caps SessionCapabilities) error {
	if caps.SessionID == "" {
		return fmt.Errorf("session capability session ID is empty")
	}
	if sessionDir == "" {
		sessionDir = platform.SessionDir()
	}
	m := &Manager{file: filepath.Join(sessionDir, caps.SessionID+".db"), sessionDir: sessionDir}
	updatedAt := caps.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	return m.withDB(func(db *dao.Database) error {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if err := validateRuntimeLeaseTx(tx, sessionDir, caps.SessionID); err != nil {
			return err
		}
		err = dao.NewSessionDAO(db.Bun()).UpsertCapability(context.Background(), tx, &dao.SessionCapabilityRecord{SessionID: caps.SessionID, Mode: caps.Mode, DisplayMode: caps.DisplayMode, DelegateMode: boolToInt(caps.DelegateMode), MultiAgent: boolToInt(caps.MultiAgent), Workflows: boolToInt(caps.Workflows), WebSearch: boolToInt(caps.WebSearch), Browser: boolToInt(caps.Browser), A2AMaster: boolToInt(caps.A2AMaster), UpdatedAt: updatedAt.Format(time.RFC3339Nano)})
		if err != nil {
			return err
		}
		return tx.Commit()
	})
}

// SaveSessionRunEvent appends a run lifecycle event to the independent run event table.
func SaveSessionRunEvent(sessionDir string, ev SessionRunEvent) (string, error) {
	if ev.SessionID == "" {
		return "", fmt.Errorf("session run event session ID is empty")
	}
	if ev.RunID == "" {
		return "", fmt.Errorf("session run event run ID is empty")
	}
	if ev.EventType == "" {
		return "", fmt.Errorf("session run event type is empty")
	}
	if ev.ID == "" {
		ev.ID = GenerateID()
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now()
	}
	if sessionDir == "" {
		sessionDir = platform.SessionDir()
	}
	m := &Manager{file: filepath.Join(sessionDir, ev.SessionID+".db"), sessionDir: sessionDir}
	data := normalizeEventData(ev.Data)
	return ev.ID, m.withDB(func(db *dao.Database) error {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := validateRuntimeLeaseTx(tx, sessionDir, ev.SessionID); err != nil {
			return err
		}
		if err := dao.NewSessionDAO(db.Bun()).InsertRunEvent(context.Background(), tx, &dao.SessionRunEventRecord{ID: ev.ID, SessionID: ev.SessionID, RunID: ev.RunID, EventType: ev.EventType, Source: ev.Source, Status: ev.Status, Model: ev.Model, Mode: ev.Mode, Timestamp: ev.Timestamp.Format(time.RFC3339Nano), Data: data}); err != nil {
			return err
		}
		return tx.Commit()
	})
}

// ListSessionRunEvents returns run events for a session, ordered by insertion.
func ListSessionRunEvents(sessionDir, sessionID string) ([]SessionRunEvent, error) {
	return ListSessionRunEventsContext(context.Background(), sessionDir, sessionID)
}

// ListSessionRunEventsContext is the cancellable event replay query used by
// bounded recovery and reconnect paths.
func ListSessionRunEventsContext(ctx context.Context, sessionDir, sessionID string) ([]SessionRunEvent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if sessionID == "" {
		return nil, nil
	}
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return nil, err
	}
	records, err := dao.NewSessionDAO(db.Bun()).ListRunEvents(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	var events []SessionRunEvent
	for _, record := range records {
		ev := SessionRunEvent{ID: record.ID, SessionID: record.SessionID, RunID: record.RunID, EventType: record.EventType, Source: record.Source, Status: record.Status, Model: record.Model, Mode: record.Mode, Timestamp: parseSessionTimestamp(record.Timestamp), Data: json.RawMessage(record.Data)}
		events = append(events, ev)
	}
	return events, nil
}

// LatestSessionRunEventSeq returns the durable replay cursor for one Run.
// Callers use it to reconcile a disconnected adapter before requesting only
// the missing portion of the session event stream.
func LatestSessionRunEventSeq(sessionDir, runID string) (int64, error) {
	if runID == "" {
		return 0, nil
	}
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return 0, err
	}
	return dao.NewSessionDAO(db.Bun()).MaxRunEventSeq(context.Background(), runID)
}

// SaveSessionCapabilityEvent appends a capability transition event to the independent event table.
func SaveSessionCapabilityEvent(sessionDir string, ev SessionCapabilityEvent) (string, error) {
	if ev.SessionID == "" {
		return "", fmt.Errorf("session capability event session ID is empty")
	}
	if ev.EventType == "" {
		return "", fmt.Errorf("session capability event type is empty")
	}
	if ev.Capability == "" {
		return "", fmt.Errorf("session capability event capability is empty")
	}
	if ev.ID == "" {
		ev.ID = GenerateID()
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now()
	}
	if sessionDir == "" {
		sessionDir = platform.SessionDir()
	}
	m := &Manager{file: filepath.Join(sessionDir, ev.SessionID+".db"), sessionDir: sessionDir}
	data := normalizeEventData(ev.Data)
	return ev.ID, m.withDB(func(db *dao.Database) error {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if err := validateRuntimeLeaseTx(tx, sessionDir, ev.SessionID); err != nil {
			return err
		}
		err = dao.NewSessionDAO(db.Bun()).InsertCapabilityEvent(context.Background(), tx, &dao.SessionCapabilityEventRecord{ID: ev.ID, SessionID: ev.SessionID, RunID: ev.RunID, EventType: ev.EventType, Source: ev.Source, Actor: ev.Actor, Capability: ev.Capability, OldValue: ev.OldValue, NewValue: ev.NewValue, Timestamp: ev.Timestamp.Format(time.RFC3339Nano), Data: data})
		if err != nil {
			return err
		}
		return tx.Commit()
	})
}

// ListSessionCapabilityEvents returns capability events for a session, ordered by insertion.
func ListSessionCapabilityEvents(sessionDir, sessionID string) ([]SessionCapabilityEvent, error) {
	if sessionID == "" {
		return nil, nil
	}
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return nil, err
	}
	records, err := dao.NewSessionDAO(db.Bun()).ListCapabilityEvents(context.Background(), sessionID)
	if err != nil {
		return nil, err
	}
	var events []SessionCapabilityEvent
	for _, record := range records {
		ev := SessionCapabilityEvent{ID: record.ID, SessionID: record.SessionID, RunID: record.RunID, EventType: record.EventType, Source: record.Source, Actor: record.Actor, Capability: record.Capability, OldValue: record.OldValue, NewValue: record.NewValue, Timestamp: parseSessionTimestamp(record.Timestamp), Data: json.RawMessage(record.Data)}
		events = append(events, ev)
	}
	return events, nil
}

// ListSessionMessagesWithSeq returns the visible replay messages for a session,
// preserving each message row's entries.seq cursor.
func ListSessionMessagesWithSeq(sessionDir, sessionID string) ([]SequencedMessage, error) {
	if sessionID == "" {
		return nil, nil
	}
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return nil, err
	}
	records, err := dao.NewSessionDAO(db.Bun()).Messages(context.Background(), sessionID)
	if err != nil {
		return nil, err
	}
	// Content overrides are appended after their target message, so resolve
	// them up front; a single forward pass cannot see the override in time.
	overrides := make(map[string]provider.Message)
	for _, record := range records {
		if EntryType(record.Type) != EntryContentOverride {
			continue
		}
		var e ContentOverrideEntry
		if err := json.Unmarshal([]byte(record.Data), &e); err != nil {
			continue
		}
		if e.TargetEntryID != "" {
			overrides[e.TargetEntryID] = e.Message
		}
	}
	state := sequencedReplayState{}
	for _, record := range records {
		seq, typeStr, data := record.Seq, record.Type, record.Data
		switch EntryType(typeStr) {
		case EntryMessage:
			var e MessageEntry
			if err := json.Unmarshal([]byte(data), &e); err != nil {
				continue
			}
			msg := e.Message
			if replacement, ok := overrides[e.ID]; ok {
				msg = replacement
			}
			state.messages = append(state.messages, SequencedMessage{
				Seq:     seq,
				EntryID: e.ID,
				Message: cloneMessage(msg),
			})
			state.entryIDs = append(state.entryIDs, e.ID)
		case EntryCompaction:
			var e CompactionEntry
			if err := json.Unmarshal([]byte(data), &e); err != nil {
				continue
			}
			applySequencedCompactionEntry(&state, e, seq)
		}
	}
	return state.messages, nil
}

// ListSessionMessagesAfter returns persisted message rows after entries.seq.
func ListSessionMessagesAfter(sessionDir, sessionID string, afterSeq int64, limit int) ([]SequencedMessage, error) {
	if sessionID == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return nil, err
	}
	records, err := dao.NewSessionDAO(db.Bun()).MessagesAfter(context.Background(), sessionID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	var messages []SequencedMessage
	for _, record := range records {
		seq, data := record.Seq, record.Data
		var e MessageEntry
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			continue
		}
		messages = append(messages, SequencedMessage{
			Seq:     seq,
			EntryID: e.ID,
			Message: cloneMessage(e.Message),
		})
	}
	return messages, nil
}

// ListSessionMessagesLatest returns the latest N message entries (highest seq first).
func ListSessionMessagesLatest(sessionDir, sessionID string, limit int) ([]SequencedMessage, error) {
	if sessionID == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return nil, err
	}
	records, err := dao.NewSessionDAO(db.Bun()).MessagesLatest(context.Background(), sessionID, limit)
	if err != nil {
		return nil, err
	}
	var messages []SequencedMessage
	for _, record := range records {
		seq, data := record.Seq, record.Data
		var e MessageEntry
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			continue
		}
		messages = append(messages, SequencedMessage{
			Seq:     seq,
			EntryID: e.ID,
			Message: cloneMessage(e.Message),
		})
	}
	// Reverse to ascending order.
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return messages, nil
}

// ListSessionMessagesBefore returns messages with seq < beforeSeq, newest first limited to `limit`.
func ListSessionMessagesBefore(sessionDir, sessionID string, beforeSeq int64, limit int) ([]SequencedMessage, error) {
	if sessionID == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return nil, err
	}
	records, err := dao.NewSessionDAO(db.Bun()).MessagesBefore(context.Background(), sessionID, beforeSeq, limit)
	if err != nil {
		return nil, err
	}
	var messages []SequencedMessage
	for _, record := range records {
		seq, data := record.Seq, record.Data
		var e MessageEntry
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			continue
		}
		messages = append(messages, SequencedMessage{
			Seq:     seq,
			EntryID: e.ID,
			Message: cloneMessage(e.Message),
		})
	}
	// Reverse to ascending order.
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return messages, nil
}

// ListSessionRunEventsWithSeq returns run events with their session_run_events.seq cursor.
func ListSessionRunEventsWithSeq(sessionDir, sessionID string) ([]SequencedSessionRunEvent, error) {
	return ListSessionRunEventsAfter(sessionDir, sessionID, 0, 0)
}

// ListSessionRunEventsAfter returns run events after session_run_events.seq.
func ListSessionRunEventsAfter(sessionDir, sessionID string, afterSeq int64, limit int) ([]SequencedSessionRunEvent, error) {
	if sessionID == "" {
		return nil, nil
	}
	if limit > 500 {
		limit = 500
	}
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return nil, err
	}
	records, err := dao.NewSessionDAO(db.Bun()).RunEventsAfter(context.Background(), sessionID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	var events []SequencedSessionRunEvent
	for _, record := range records {
		item := SequencedSessionRunEvent{Seq: record.Seq, Event: SessionRunEvent{ID: record.ID, SessionID: record.SessionID, RunID: record.RunID, EventType: record.EventType, Source: record.Source, Status: record.Status, Model: record.Model, Mode: record.Mode, Timestamp: parseSessionTimestamp(record.Timestamp), Data: json.RawMessage(record.Data)}}
		events = append(events, item)
	}
	return events, nil
}

// ListSessionCapabilityEventsWithSeq returns capability events with their seq cursor.
func ListSessionCapabilityEventsWithSeq(sessionDir, sessionID string) ([]SequencedSessionCapabilityEvent, error) {
	return ListSessionCapabilityEventsAfter(sessionDir, sessionID, 0, 0)
}

// ListSessionCapabilityEventsAfter returns capability events after session_capability_events.seq.
func ListSessionCapabilityEventsAfter(sessionDir, sessionID string, afterSeq int64, limit int) ([]SequencedSessionCapabilityEvent, error) {
	if sessionID == "" {
		return nil, nil
	}
	if limit > 500 {
		limit = 500
	}
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return nil, err
	}
	records, err := dao.NewSessionDAO(db.Bun()).CapabilityEventsAfter(context.Background(), sessionID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	var events []SequencedSessionCapabilityEvent
	for _, record := range records {
		item := SequencedSessionCapabilityEvent{Seq: record.Seq, Event: SessionCapabilityEvent{ID: record.ID, SessionID: record.SessionID, RunID: record.RunID, EventType: record.EventType, Source: record.Source, Actor: record.Actor, Capability: record.Capability, OldValue: record.OldValue, NewValue: record.NewValue, Timestamp: parseSessionTimestamp(record.Timestamp), Data: json.RawMessage(record.Data)}}
		events = append(events, item)
	}
	return events, nil
}

func normalizeEventData(data json.RawMessage) string {
	if len(data) == 0 {
		return "{}"
	}
	if !json.Valid(data) {
		return "{}"
	}
	return string(data)
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// ListForDirDetailed lists sessions with details (ID, message count, preview).
func ListForDirDetailed(cwd, sessionDir string) ([]SessionDetail, error) {
	sessions, err := ListForDir(cwd, sessionDir)
	if err != nil {
		return nil, err
	}
	return buildSessionDetails(sessions)
}

// ListAllDetailed lists sessions with details across all working directories.
func ListAllDetailed(sessionDir string, opts ...ListOption) ([]SessionDetail, error) {
	sessions, err := ListAll(sessionDir, opts...)
	if err != nil {
		return nil, err
	}
	return buildSessionDetails(sessions)
}

func buildSessionDetails(sessions []SessionInfo) ([]SessionDetail, error) {
	if len(sessions) == 0 {
		return nil, nil
	}

	// Open the shared DB from the first session's path.
	dbPath := resolveDBPath(sessions[0].Path)
	db, err := cachedDB(dbPath)
	if err != nil {
		return nil, err
	}

	// Build session ID list.
	ids := make([]string, len(sessions))
	idPos := make(map[string]int, len(sessions))
	for i, s := range sessions {
		id := sessionFileID(s.Path)
		ids[i] = id
		idPos[id] = i
	}

	details := make([]SessionDetail, len(sessions))
	for i, s := range sessions {
		details[i] = SessionDetail{SessionInfo: s, ID: sessionFileID(s.Path)}
	}

	aggregates, err := dao.NewSessionDAO(db.Bun()).DetailAggregates(context.Background(), ids)
	if err != nil {
		return nil, err
	}
	msgCounts, firstMsgData := aggregates.MessageCounts, aggregates.FirstMessages

	// Populate details from counts and first message data.
	for sessionID, count := range msgCounts {
		if idx, ok := idPos[sessionID]; ok {
			details[idx].MessageCount = count
			if data := firstMsgData[sessionID]; data != "" {
				var entry MessageEntry
				if err := json.Unmarshal([]byte(data), &entry); err == nil && entry.Message.Role == "user" {
					text := entry.Message.Content
					if text == "" {
						for _, b := range entry.Message.Contents {
							if b.Type == "text" && b.Text != "" {
								text = b.Text
								break
							}
						}
					}
					if text != "" {
						details[idx].Preview = util.TruncateWithSuffix(text, 60, "...")
					}
				}
			}
		}
	}

	for sessionID, data := range aggregates.LatestInfos {
		idx, ok := idPos[sessionID]
		if !ok {
			continue
		}
		var entry SessionInfoEntry
		if err := json.Unmarshal([]byte(data), &entry); err == nil {
			details[idx].Name = entry.Name
		}
	}
	// Sort by modification time (newest first).
	sort.Slice(details, func(i, j int) bool {
		return details[i].ModTime.After(details[j].ModTime)
	})

	return details, nil
}

// RecordUsage records a single LLM request's token usage and timing.
func (m *Manager) RecordUsage(provider, protocol, model string, inputTokens, outputTokens, totalTokens, durationMs int) error {
	m.mu.RLock()
	sessionID := ""
	if m.header != nil {
		sessionID = m.header.ID
	}
	m.mu.RUnlock()

	now := time.Now().Format(time.RFC3339Nano)
	db, err := OpenRootDB(filepath.Dir(resolveDBPath(m.file)))
	if err != nil {
		return err
	}
	return db.RunInTx(context.Background(), nil, func(_ context.Context, tx dao.Tx) error {
		if err := validateRuntimeLeaseTx(&tx, m.GetSessionDir(), sessionID); err != nil {
			return err
		}
		sessionIDValue := sessionID
		return dao.NewStatsDAO(db.Bun()).Insert(context.Background(), tx, &dao.StatsRecord{
			Timestamp: now, SessionID: &sessionIDValue, Provider: provider, Protocol: protocol,
			Model: model, InputTokens: inputTokens, OutputTokens: outputTokens,
			TotalTokens: totalTokens, DurationMs: durationMs,
		})
	})
}

// RecordUsageFromProviderUsage records usage from a provider.Usage struct.
func (m *Manager) RecordUsageFromProviderUsage(provider, protocol, model string, usage *provider.Usage, durationMs int) error {
	if usage == nil {
		return nil
	}
	return m.RecordUsage(provider, protocol, model, usage.TotalInputTokens(), usage.Output, usage.TotalInputTokens()+usage.Output, durationMs)
}

// maxEntriesPerTransaction caps how many session entries one AppendMessages
// batch persists in a single write transaction. It keeps the single-writer
// lock hold time bounded for very large parallel-tool rounds while still
// folding the common case (one iteration's tool results) into one commit.
const maxEntriesPerTransaction = 64

// resolveEntryWriteTarget verifies the session handle/database is writable to
// honor file permission settings and resolves the session ID for entry
// writes. It is the shared prologue of the single-entry and batched write
// paths.
func (m *Manager) resolveEntryWriteTarget() (dbPath, sessionID string, err error) {
	dbPath = resolveDBPath(m.file)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		return "", "", fmt.Errorf("create db dir: %w", err)
	}
	if _, statErr := os.Stat(dbPath); statErr == nil {
		f, openErr := os.OpenFile(dbPath, os.O_WRONLY, 0600)
		if openErr != nil {
			return "", "", fmt.Errorf("open session file: %w", openErr)
		}
		f.Close()
	}
	if m.header != nil {
		sessionID = m.header.ID
	} else {
		idBytes, readErr := os.ReadFile(m.file)
		if readErr == nil {
			sessionID = strings.TrimSpace(string(idBytes))
		} else if os.IsNotExist(readErr) {
			sessionID = sessionFileID(m.file)
		}
	}
	if sessionID == "" {
		return "", "", fmt.Errorf("no session ID found for writeEntry")
	}
	return dbPath, sessionID, nil
}

func (m *Manager) writeEntry(entry interface{}) error {
	// Verify handle file or its database is writable to honor file permission settings
	dbPath, sessionID, err := m.resolveEntryWriteTarget()
	if err != nil {
		return err
	}

	id, typeStr, parentID, ts := getEntryMetadata(entry)
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal entry: %w", err)
	}

	return m.withDB(func(db *dao.Database) error {
		sessionDAO := dao.NewSessionDAO(db.Bun())
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin writing session entry: %w", err)
		}
		defer tx.Rollback()
		if err := validateRuntimeLeaseTx(tx, filepath.Dir(dbPath), sessionID); err != nil {
			return err
		}

		if typeStr != string(EntrySession) {
			currentLeaf, err := sessionDAO.CurrentLeaf(context.Background(), tx, m.entriesTable(), sessionID, string(EntrySession))
			if err != nil {
				return fmt.Errorf("read current session leaf: %w", err)
			}
			expectedLeaf := ""
			if parentID != nil {
				expectedLeaf = *parentID
			}
			if currentLeaf != expectedLeaf {
				return fmt.Errorf("%w: expected leaf %q, current leaf %q; reopen the session before writing", ErrSessionModified, expectedLeaf, currentLeaf)
			}
		}

		// Register session if header is being written
		if typeStr == string(EntrySession) && m.header != nil {
			err = sessionDAO.InsertSession(context.Background(), tx, m.sessionTable(), sessionID, m.cwd, m.header.Timestamp.Format(time.RFC3339Nano), m.header.ParentSession, m.header.Version, m.header.ChannelType, m.header.ChannelID, m.header.ForkBoundarySeq, m.header.SeedLength, m.header.ForkKind, m.header.ExpertID)
			if err != nil {
				if strings.Contains(strings.ToLower(err.Error()), "unique constraint failed: "+strings.ToLower(m.sessionTable())+".id") {
					return fmt.Errorf("%w: %s", ErrSessionIDExists, err)
				}
				return fmt.Errorf("register session: %w", err)
			}
		}

		var parentIDVal interface{}
		if parentID != nil {
			parentIDVal = *parentID
		}
		err = sessionDAO.InsertEntry(context.Background(), tx, m.entriesTable(), sessionID, id, typeStr, parentIDVal, ts.Format(time.RFC3339Nano), string(data))
		if err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit session entry: %w", err)
		}
		return nil
	})
}

// writeEntries persists a chain of message entries in a single transaction:
// one lease validation, one leaf check against the first entry's parent, and
// one insert per entry. The caller builds the parent chain in memory (entry i
// parents entry i-1), so under the write lock held since BEGIN IMMEDIATE the
// single head-of-transaction leaf check covers the whole batch exactly like
// the per-entry check in writeEntry covers a single append.
func (m *Manager) writeEntries(batch []MessageEntry) error {
	if len(batch) == 0 {
		return nil
	}
	dbPath, sessionID, err := m.resolveEntryWriteTarget()
	if err != nil {
		return err
	}
	rows := make([][]byte, len(batch))
	for i := range batch {
		data, err := json.Marshal(batch[i])
		if err != nil {
			return fmt.Errorf("marshal entry: %w", err)
		}
		rows[i] = data
	}
	return m.withDB(func(db *dao.Database) error {
		sessionDAO := dao.NewSessionDAO(db.Bun())
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin writing session entries: %w", err)
		}
		defer tx.Rollback()
		if err := validateRuntimeLeaseTx(tx, filepath.Dir(dbPath), sessionID); err != nil {
			return err
		}
		currentLeaf, err := sessionDAO.CurrentLeaf(context.Background(), tx, m.entriesTable(), sessionID, string(EntrySession))
		if err != nil {
			return fmt.Errorf("read current session leaf: %w", err)
		}
		expectedLeaf := ""
		if batch[0].ParentID != nil {
			expectedLeaf = *batch[0].ParentID
		}
		if currentLeaf != expectedLeaf {
			return fmt.Errorf("%w: expected leaf %q, current leaf %q; reopen the session before writing", ErrSessionModified, expectedLeaf, currentLeaf)
		}
		for i := range batch {
			var parentIDVal interface{}
			if batch[i].ParentID != nil {
				parentIDVal = *batch[i].ParentID
			}
			if err := sessionDAO.InsertEntry(context.Background(), tx, m.entriesTable(), sessionID, batch[i].ID, string(EntryMessage), parentIDVal, batch[i].Timestamp.Format(time.RFC3339Nano), string(rows[i])); err != nil {
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit session entries: %w", err)
		}
		return nil
	})
}
