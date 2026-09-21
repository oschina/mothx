package session

import (
	"sync"
	"time"

	"github.com/startvibecoding/mothx/internal/provider"
)

// MemoryStore is an in-memory implementation of Store for testing.
// It does not persist data to disk.
type MemoryStore struct {
	mu      sync.RWMutex
	header  *Header
	entries []interface{}
	leafID  *string
	file    string
}

// NewMemoryStore creates a new in-memory session store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

func (m *MemoryStore) Init() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := GenerateID()
	m.header = &Header{
		Type:      EntrySession,
		Version:   CurrentVersion,
		ID:        id,
		Timestamp: time.Now(),
	}
	return nil
}

func (m *MemoryStore) InitWithID(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" {
		id = GenerateID()
	}
	m.header = &Header{
		Type:      EntrySession,
		Version:   CurrentVersion,
		ID:        id,
		Timestamp: time.Now(),
	}
	return nil
}

func (m *MemoryStore) AppendMessage(msg provider.Message) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
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
	m.entries = append(m.entries, entry)
	m.leafID = &id
	return id, nil
}

func (m *MemoryStore) AppendCompaction(summary, firstKeptEntryID string, tokensBefore int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := GenerateID()
	entry := CompactionEntry{
		EntryBase: EntryBase{
			Type:      EntryCompaction,
			ID:        id,
			ParentID:  m.leafID,
			Timestamp: time.Now(),
		},
		Summary:        summary,
		FirstKeptEntry: firstKeptEntryID,
		TokensBefore:   tokensBefore,
	}
	m.entries = append(m.entries, entry)
	m.leafID = &id
	return id, nil
}

func (m *MemoryStore) AppendModelChange(providerName, modelID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
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
	m.entries = append(m.entries, entry)
	m.leafID = &id
	return id, nil
}

func (m *MemoryStore) AppendModeChange(mode string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := GenerateID()
	entry := ModeChangeEntry{EntryBase: EntryBase{Type: EntryModeChange, ID: id, ParentID: m.leafID, Timestamp: time.Now()}, Mode: mode}
	m.entries = append(m.entries, entry)
	m.leafID = &id
	return id, nil
}

func (m *MemoryStore) AppendThinkingLevelChange(level string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
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
	m.entries = append(m.entries, entry)
	m.leafID = &id
	return id, nil
}

func (m *MemoryStore) AppendAdditionalDirectories(directories []string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := GenerateID()
	entry := AdditionalDirectoriesEntry{EntryBase: EntryBase{Type: EntryAdditionalDirectories, ID: id, ParentID: m.leafID, Timestamp: time.Now()}, Directories: append([]string(nil), directories...)}
	m.entries = append(m.entries, entry)
	m.leafID = &id
	return id, nil
}

func (m *MemoryStore) AppendSessionInfo(name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := GenerateID()
	entry := SessionInfoEntry{
		EntryBase: EntryBase{
			Type:      EntrySessionInfo,
			ID:        id,
			ParentID:  m.leafID,
			Timestamp: time.Now(),
		},
		Name: name,
	}
	m.entries = append(m.entries, entry)
	m.leafID = &id
	return id, nil
}

func (m *MemoryStore) GetMessages() []provider.Message {
	state := m.GetReplayState()
	return state.Messages
}

func (m *MemoryStore) GetReplayState() ReplayState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := buildReplayState(m.entries)
	return ReplayState{
		Messages: state.messages,
		EntryIDs: state.entryIDs,
	}
}

func (m *MemoryStore) GetLeafID() *string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.leafID == nil {
		return nil
	}
	leafID := *m.leafID
	return &leafID
}

func (m *MemoryStore) GetLatestCompaction() (CompactionEntry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return latestCompactionLocked(m.entries)
}

func (m *MemoryStore) GetLatestModelChange() (ModelChangeEntry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := len(m.entries) - 1; i >= 0; i-- {
		if entry, ok := m.entries[i].(ModelChangeEntry); ok {
			return entry, true
		}
	}
	return ModelChangeEntry{}, false
}

func (m *MemoryStore) GetLatestModeChange() (ModeChangeEntry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := len(m.entries) - 1; i >= 0; i-- {
		if entry, ok := m.entries[i].(ModeChangeEntry); ok {
			return entry, true
		}
	}
	return ModeChangeEntry{}, false
}

func (m *MemoryStore) GetLatestThinkingLevelChange() (ThinkingLevelChangeEntry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := len(m.entries) - 1; i >= 0; i-- {
		if entry, ok := m.entries[i].(ThinkingLevelChangeEntry); ok {
			return entry, true
		}
	}
	return ThinkingLevelChangeEntry{}, false
}

func (m *MemoryStore) GetLatestAdditionalDirectories() (AdditionalDirectoriesEntry, bool) {
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

func (m *MemoryStore) GetFile() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.file
}

func (m *MemoryStore) GetHeader() *Header {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.header == nil {
		return nil
	}
	header := *m.header
	return &header
}

// Compile-time check that MemoryStore implements Store.
var _ Store = (*MemoryStore)(nil)
