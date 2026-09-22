package session

import "github.com/oschina/mothx/internal/provider"

// Store is the interface for session persistence backends.
// Manager implements this interface using SQLite. Alternative backends
// (in-memory for testing, cloud storage, etc.) can implement Store
// to swap the persistence layer without changing agent or UI code.
type Store interface {
	// Init initializes the session store, creating the underlying
	// database or storage if needed.
	Init() error

	// InitWithID initializes the session with a specific ID.
	// An empty id generates a new one.
	InitWithID(id string) error

	// AppendMessage persists a conversation message and returns its entry ID.
	AppendMessage(msg provider.Message) (string, error)

	// AppendCompaction records a context compaction event.
	AppendCompaction(summary, firstKeptEntryID string, tokensBefore int) (string, error)

	// AppendModelChange records a model switch.
	AppendModelChange(providerName, modelID string) (string, error)

	// AppendModeChange records a session execution mode change.
	AppendModeChange(mode string) (string, error)

	// AppendThinkingLevelChange records a thinking level change.
	AppendThinkingLevelChange(level string) (string, error)
	AppendAdditionalDirectories(directories []string) (string, error)

	// AppendSessionInfo records session metadata.
	AppendSessionInfo(name string) (string, error)

	// GetMessages returns all messages in the current branch,
	// with compaction summaries applied.
	GetMessages() []provider.Message

	// GetReplayState returns the full replay state including
	// messages and their entry IDs.
	GetReplayState() ReplayState

	// GetLeafID returns the current leaf entry ID, or nil if empty.
	GetLeafID() *string

	// GetLatestCompaction returns the most recent compaction entry,
	// or (zero, false) if none exists.
	GetLatestCompaction() (CompactionEntry, bool)

	// GetLatestModelChange returns the most recent persisted model binding.
	GetLatestModelChange() (ModelChangeEntry, bool)

	// GetLatestModeChange returns the most recent persisted session mode.
	GetLatestModeChange() (ModeChangeEntry, bool)

	// GetLatestThinkingLevelChange returns the most recent persisted thinking level.
	GetLatestThinkingLevelChange() (ThinkingLevelChangeEntry, bool)
	GetLatestAdditionalDirectories() (AdditionalDirectoriesEntry, bool)

	// GetFile returns the session file path (handle file for SQLite).
	GetFile() string

	// GetHeader returns the session header with metadata.
	GetHeader() *Header
}

// Compile-time check that Manager implements Store.
var _ Store = (*Manager)(nil)
